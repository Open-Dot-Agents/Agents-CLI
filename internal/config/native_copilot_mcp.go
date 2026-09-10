package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var nativeCopilotMCPTypes = map[string]string{
	"type": "transport", "command": "string", "args": "array<string>", "env": "map<string,string>",
	"cwd": "string", "tools": "array<string>", "timeout": "number", "url": "string", "headers": "map<string,string>",
	"oauthClientId": "string", "oauthPublicClient": "boolean", "oauthGrantType": "grant", "oidc": "boolean",
	"disableToolCache": "boolean", "deferTools": "defer",
}
var nativeCopilotMCPReference = regexp.MustCompile(`^(?:(?:Bearer|Basic) )?\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*\})$`)

func nativeCopilotMCPValues(values map[string]any) map[string]any {
	if _, wrapped := values["mcpServers"]; wrapped || len(values) == 0 {
		return values
	}
	return map[string]any{"mcpServers": values}
}

func nativeSelectCopilotMCP(values map[string]any) (map[string]any, []nativeInactiveField, error) {
	values = nativeCopilotMCPValues(values)
	selected := map[string]any{}
	var inactive []nativeInactiveField
	for _, root := range nativeSortedKeys(values) {
		if root != "mcpServers" {
			inactive = append(inactive, nativeInactiveField{"/" + nativePointer(root), "inactive", "no native MCP root field mapping"})
			continue
		}
		servers, ok := values[root].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("native MCP servers must be an object")
		}
		output := map[string]any{}
		for _, name := range nativeSortedKeys(servers) {
			server, ok := servers[name].(map[string]any)
			if !ok || name == "" {
				return nil, nil, fmt.Errorf("native MCP server must have a name and an object definition")
			}
			fields := map[string]any{}
			for _, field := range nativeSortedKeys(server) {
				pointer := "/mcpServers/" + nativePointer(name) + "/" + nativePointer(field)
				value := server[field]
				kind, known := nativeCopilotMCPTypes[field]
				if !known {
					inactive = append(inactive, nativeInactiveField{pointer, "inactive", "no native MCP server field mapping"})
					continue
				}
				if nativeContainsExcluded("copilot", []string{"mcpServers", name, field}, value) {
					return nil, nil, fmt.Errorf("native MCP field %s contains excluded credential values", pointer)
				}
				valid := false
				switch kind {
				case "transport":
					valid = value == "local" || value == "stdio" || value == "http" || value == "sse" || value == "streamable-http"
				case "grant":
					valid = value == "authorization_code" || value == "client_credentials"
				case "defer":
					valid = value == "auto" || value == "never"
				default:
					valid = nativeLSPValue(kind, value)
				}
				if !valid {
					return nil, nil, fmt.Errorf("invalid native MCP field %s", pointer)
				}
				if field == "url" {
					parsed, err := url.Parse(value.(string))
					if err != nil || parsed.User != nil {
						return nil, nil, fmt.Errorf("native MCP URL is malformed or contains credentials")
					}
				}
				fields[field] = value
			}
			if len(fields) > 0 {
				output[name] = fields
			}
		}
		if len(output) > 0 {
			selected[root] = output
		}
	}
	return selected, inactive, nil
}

func nativeValidateCopilotMCPServer(server map[string]any) error {
	kind, _ := server["type"].(string)
	command, _ := server["command"].(string)
	remote, _ := server["url"].(string)
	if command != "" && remote != "" {
		return fmt.Errorf("native MCP server cannot have both command and URL")
	}
	if command != "" {
		if kind != "" && kind != "local" && kind != "stdio" {
			return fmt.Errorf("native MCP local server has conflicting transport")
		}
		for _, key := range []string{"headers", "oauthClientId", "oauthPublicClient", "oauthGrantType", "oidc"} {
			if _, ok := server[key]; ok {
				return fmt.Errorf("native MCP local server contains remote field %s", key)
			}
		}
		return nil
	}
	if remote != "" && (kind == "http" || kind == "sse" || kind == "streamable-http") {
		parsed, err := url.Parse(remote)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("native MCP remote server requires an HTTP or HTTPS URL")
		}
		for _, key := range []string{"args", "env", "cwd"} {
			if _, ok := server[key]; ok {
				return fmt.Errorf("native MCP remote server contains local field %s", key)
			}
		}
		return nil
	}
	return fmt.Errorf("native MCP server requires a command or a URL with explicit remote transport")
}

// Extract only portable values with the same documented semantics. Keep native
// transport spelling, literal environment values, and extended fields intact.
func nativeExtractCopilotMCP(values map[string]any) (map[string]MCPServer, error) {
	normalized := map[string]any{}
	for key, value := range nativeCopilotMCPValues(values) {
		normalized[key] = value
	}
	selected, _, err := nativeSelectCopilotMCP(normalized)
	if err != nil {
		return nil, err
	}
	// Selection must not erase unknown optional fields from the imported source.
	source, ok := normalized["mcpServers"].(map[string]any)
	if !ok {
		return nil, nil
	}
	raw := map[string]any{}
	for key, value := range source {
		raw[key] = value
	}
	validated, _ := selected["mcpServers"].(map[string]any)
	for name, entry := range validated {
		if err := nativeValidateCopilotMCPServer(entry.(map[string]any)); err != nil {
			return nil, fmt.Errorf("native MCP server %s: %w", name, err)
		}
	}
	output := map[string]MCPServer{}
	for _, name := range nativeSortedKeys(raw) {
		server := raw[name].(map[string]any)
		core := MCPServer{}
		if command, ok := server["command"].(string); ok && command != "" {
			expanded := strings.Contains(command, "$")
			if args, ok := server["args"].([]any); ok {
				for _, arg := range args {
					expanded = expanded || strings.Contains(arg.(string), "$")
				}
			}
			if expanded {
				continue
			}
			core.Type, core.Command = "stdio", command
			if args, ok := server["args"].([]any); ok {
				for _, arg := range args {
					core.Args = append(core.Args, arg.(string))
				}
			}
			delete(server, "command")
			if len(core.Args) > 0 {
				delete(server, "args")
			}
		} else if remote, ok := server["url"].(string); ok && strings.HasPrefix(remote, "https://") {
			core.Type, core.URL = "remote", remote
			delete(server, "url")
		} else {
			continue
		}
		output[name] = core
		if len(server) == 0 {
			delete(raw, name)
		}
	}
	for key := range values {
		delete(values, key)
	}
	if len(raw) > 0 {
		values["mcpServers"] = raw
	}
	for key, value := range normalized {
		if key != "mcpServers" {
			values[key] = value
		}
	}
	return output, nil
}

func nativePortableCopilotMCP(name string, server MCPServer, target *nativeTarget) (map[string]any, error) {
	output := nativeJSONServer("copilot", server)
	typeKey := "/mcpServers/" + nativePointer(name) + "/type"
	if target != nil {
		if native, ok := target.settings[typeKey]; ok {
			compatible := server.Type == "stdio" && (native == "local" || native == "stdio") || server.Type == "remote" && (native == "http" || native == "sse" || native == "streamable-http")
			if !compatible {
				return nil, fmt.Errorf("portable and native MCP transports conflict for %s", name)
			}
			delete(output, "type")
		}
	}
	return output, nil
}

// Copilot reads both project files and selects the higher-priority definition
// when a server name repeats. Preserve disjoint servers and optional root data.
func nativeReadCopilotProjectMCP(base string) ([]byte, error) {
	combined := map[string]any{}
	servers := map[string]any{}
	found := false
	for _, relative := range []string{".github/mcp.json", ".mcp.json"} {
		data, err := nativeReadFile(filepath.Join(base, relative))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = true
		values, err := parseNative(data, "json")
		if err != nil {
			return nil, err
		}
		for key, value := range nativeCopilotMCPValues(values) {
			if key != "mcpServers" {
				combined[key] = value
				continue
			}
			definitions, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("native project MCP servers must be an object")
			}
			for name, definition := range definitions {
				servers[name] = definition
			}
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	combined["mcpServers"] = servers
	return nativeEncode(combined, "json")
}

func nativeCopilotMCPFeature(feature NativeFeature) NativeFeature {
	feature.Disposition, feature.Activation = "artifact-mapping", "pending native reload"
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Copilot 1.0.83 fixtures verify local and HTTP discovery, native expansion, working directory, and a filtered tool call. Project MCP needs native folder trust. OAuth, SSE, timeout enforcement, and cache controls need separate evidence."
	prefix := "WORKBENCH/evidence/native-draft2-debug/"
	feature.Evidence = []string{prefix + "copilot-mcp-final-" + feature.Scope + ".json", prefix + "copilot-mcp-http-" + feature.Scope + ".json", "https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#mcp-server-configuration-fields"}
	return feature
}

func nativeCopilotMCPFields(feature NativeFeature) []NativeFeature {
	var output []NativeFeature
	keys := make([]string, 0, len(nativeCopilotMCPTypes))
	for name := range nativeCopilotMCPTypes {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		field := feature
		field.Feature = "artifact:mcp:/mcpServers/<name>/" + name
		field.Source = "native_copilot_mcp.go: " + nativeCopilotMCPTypes[name]
		field.Disposition, field.NativeStatus = "validator-declared", "unverified"
		field.Limitation = "Documented native field with typed validation. Effective behavior needs separate evidence."
		switch name {
		case "type", "command", "args", "env", "cwd", "url", "headers", "tools":
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "Local and HTTP fixture behavior only. Native variable expansion does not establish portable fail-on-missing reference semantics. SSE remains unverified."
		}
		output = append(output, field)
	}
	return output
}
