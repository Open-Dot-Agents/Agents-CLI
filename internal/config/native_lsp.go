package config

import (
	"fmt"
	"regexp"
	"strings"
)

// LSP configuration is a separate artifact, not a settings.json root setting.
// These fields follow the Copilot LSP reference. Options are an opaque JSON
// payload for the selected language server; this is not a shared LSP schema.
var nativeLSPFields = map[string]string{
	"command": "string", "args": "array<string>", "fileExtensions": "map<string,string>",
	"env": "map<string,string>", "rootUri": "string", "initializationOptions": "json",
	"requestTimeoutMs": "number",
}
var nativeLSPName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var nativeLSPEnvReference = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

func nativeLSPCredentialReference(vendor string, path []string, value any) bool {
	if vendor != "copilot" || len(path) != 4 || path[0] != "lspServers" || path[2] != "env" {
		return false
	}
	text, ok := value.(string)
	return ok && nativeLSPEnvReference.MatchString(text)
}

func nativeSelectLSP(values map[string]any) (map[string]any, []nativeInactiveField) {
	selected := map[string]any{}
	var inactive []nativeInactiveField
	reject := func(path, reason string) { inactive = append(inactive, nativeInactiveField{path, "inactive", reason}) }
	for _, root := range nativeSortedKeys(values) {
		if root != "lspServers" {
			reject("/"+nativePointer(root), "no LSP artifact field mapping")
			continue
		}
		servers, ok := values[root].(map[string]any)
		if !ok {
			reject("/lspServers", "LSP servers must be an object")
			continue
		}
		output := map[string]any{}
		for _, name := range nativeSortedKeys(servers) {
			pointer := "/lspServers/" + nativePointer(name)
			server, ok := servers[name].(map[string]any)
			if !ok || !nativeLSPName.MatchString(name) {
				reject(pointer, "invalid LSP server name or definition")
				continue
			}
			fields := map[string]any{}
			valid := true
			for _, field := range nativeSortedKeys(server) {
				value := server[field]
				kind, known := nativeLSPFields[field]
				if !known {
					reject(pointer+"/"+nativePointer(field), "no LSP server field mapping")
					continue
				}
				if nativeContainsExcluded("copilot", []string{"lspServers", name, field}, value) {
					reject(pointer+"/"+field, "LSP payload contains excluded credential fields")
					valid = false
					continue
				}
				if !nativeLSPValue(kind, value) {
					reject(pointer+"/"+field, "invalid LSP field value")
					valid = false
					continue
				}
				fields[field] = value
			}
			command, _ := fields["command"].(string)
			if command == "" || fields["fileExtensions"] == nil {
				reject(pointer, "LSP server requires command and fileExtensions")
				valid = false
			}
			if valid {
				output[name] = fields
			} else {
				reject(pointer, "LSP server remains inactive because its configuration is invalid")
			}
		}
		if len(output) > 0 || len(servers) == 0 {
			selected[root] = output
		}
	}
	return selected, inactive
}

func nativeLSPValue(kind string, value any) bool {
	switch kind {
	case "json":
		// parseNative has already checked valid JSON and duplicate object keys.
		return true
	case "map<string,string>":
		fields, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for key, child := range fields {
			if key == "" || !nativeValueType("string", child) {
				return false
			}
		}
		return true
	case "array<string>":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if !nativeValueType("string", item) {
				return false
			}
		}
		return true
	default:
		return nativeValueType(kind, value)
	}
}

func nativeLSPRequired(inactive []nativeInactiveField) error {
	if len(inactive) != 0 {
		return fmt.Errorf("required native LSP field %s cannot activate: %s", inactive[0].Path, inactive[0].Reason)
	}
	return nil
}

func nativeLSPFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Copilot 1.0.83 fixture verifies launch, environment expansion, root URI, initialization options, extension matching, and hover. A one-second request timeout suppresses the delayed hover result without an explicit timeout diagnostic. Other server behavior needs separate evidence. Install the language server separately and start a new session."
	evidence := "WORKBENCH/evidence/native-draft2-debug/copilot-lsp-final-user-v2.json"
	if feature.Scope == "project" {
		evidence = "WORKBENCH/evidence/native-draft2-debug/copilot-lsp-final-project-v2.json"
	}
	timeout, control := "copilot-lsp-timeout-user.json", "copilot-lsp-delayed-user.json"
	if feature.Scope == "project" {
		timeout, control = "copilot-lsp-timeout-project-v2.json", "copilot-lsp-delayed-project.json"
	}
	feature.Evidence = []string{evidence, "WORKBENCH/evidence/native-draft2-debug/" + timeout, "WORKBENCH/evidence/native-draft2-debug/" + control, "https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/add-lsp-servers"}
	return feature
}

// Initialization options belong to the language server. Terms such as
// tokenTypes, authenticationMode, or permissions are data in this payload.
// Exclude explicit credential fields, not every field that contains these words.
func nativeFieldExcluded(vendor string, path []string, value any) bool {
	if vendor == "codex" && nativeCodexOtelExcluded(path, value) {
		return true
	}
	if nativePluginState(vendor, path) {
		return true
	}
	if vendor == "codex" && len(path) >= 2 && path[0] == "hooks" && path[1] == "state" {
		return true
	}
	if vendor == "copilot" && len(path) >= 5 && path[0] == "hooks" && (path[len(path)-2] == "env" || path[len(path)-2] == "headers") {
		if reference, ok := value.(string); ok && nativeCopilotMCPReference.MatchString(reference) {
			return false
		}
	}
	if vendor == "copilot" && len(path) >= 3 && path[0] == "mcpServers" {
		if len(path) == 3 {
			if _, known := nativeCopilotMCPTypes[path[2]]; known {
				return false
			}
		}
		if len(path) == 4 && (path[2] == "env" || path[2] == "headers") {
			if reference, ok := value.(string); ok && nativeCopilotMCPReference.MatchString(reference) {
				return false
			}
		}
	}

	if vendor == "copilot" && len(path) >= 4 && path[0] == "lspServers" && path[2] == "initializationOptions" {
		key := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(path[len(path)-1]))
		switch key {
		case "password", "passwd", "secret", "token", "apikey", "accesstoken", "refreshtoken", "authorization", "clientsecret", "privatekey", "credential", "credentials":
			return true
		default:
			return false
		}
	}
	return nativePolicyField(vendor, path) && nativeForbidden(path[len(path)-1]) && !nativeLSPCredentialReference(vendor, path, value)
}
