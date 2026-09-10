package config

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// A native agent is an owned Markdown file. Preserve its source bytes. Refuse
// activation of the whole file when any frontmatter field has no mapping.
func nativeCopilotAgent(data []byte, path string) (string, error) {
	return nativeParseCopilotAgent(data, path, true)
}

func nativeDecodeCopilotAgent(data []byte) (map[string]any, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !utf8.Valid(data) || !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("native Copilot agent requires YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	closingSize := 5
	if end < 0 && strings.HasSuffix(text, "\n---") {
		end = len(text) - 4 - 4
		closingSize = 4
	}
	if end < 0 {
		return nil, fmt.Errorf("native Copilot agent has no closing frontmatter delimiter")
	}
	front, body := text[4:4+end], text[4+end+closingSize:]
	if utf8.RuneCountInString(body) > 30000 {
		return nil, fmt.Errorf("native Copilot agent prompt exceeds 30000 characters")
	}
	decoder := yaml.NewDecoder(strings.NewReader(front))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("malformed native Copilot agent YAML")
	}
	var checkNode func(*yaml.Node) bool
	checkNode = func(node *yaml.Node) bool {
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				if node.Content[i].Kind != yaml.ScalarNode || node.Content[i].Tag != "!!str" {
					return false
				}
			}
		}
		switch node.Tag {
		case "", "!!map", "!!seq", "!!str", "!!bool", "!!null", "!!int", "!!float", "!!merge":
		default:
			return false
		}
		for _, child := range node.Content {
			if !checkNode(child) {
				return false
			}
		}
		return true
	}
	if !checkNode(&document) {
		return nil, fmt.Errorf("native Copilot agent contains an unmapped YAML tag")
	}
	var fields map[string]any
	if err := document.Decode(&fields); err != nil {
		return nil, fmt.Errorf("malformed native Copilot agent YAML")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("native Copilot agent requires one YAML document")
	}
	return fields, nil
}

func nativeParseCopilotAgent(data []byte, path string, validateFields bool) (string, error) {
	fields, err := nativeDecodeCopilotAgent(data)
	if err != nil {
		return "", err
	}
	if validateFields {
		if err := nativeCheckCopilotAgentFields(fields); err != nil {
			return "", err
		}
	}
	description, ok := fields["description"].(string)
	if !ok || strings.TrimSpace(description) == "" {
		return "", fmt.Errorf("native Copilot agent requires a non-empty description string")
	}
	for _, key := range nativeSortedKeys(fields) {
		if !validateFields && key != "name" {
			continue
		}
		value := fields[key]
		valid := false
		switch key {
		case "name", "description", "model", "reasoningEffort":
			text, ok := value.(string)
			valid = ok && strings.TrimSpace(text) != ""
		case "infer", "disable-model-invocation", "user-invocable":
			_, valid = value.(bool)
		case "tools":
			// The common agent reference also documents a comma-separated string.
			if _, ok := value.(string); ok {
				valid = true
			} else {
				valid = nativeLSPValue("array<string>", value)
			}
		case "target":
			valid = value == "github-copilot"
		case "metadata":
			valid = nativeLSPValue("map<string,string>", value)
		case "mcp-servers":
			servers, ok := value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("native Copilot agent MCP servers must be an object")
			}
			// An agent is copied unchanged. Never validate a filtered subset and
			// then activate unknown fields from the original Markdown file.
			_, inactive, err := nativeSelectCopilotMCP(map[string]any{"mcpServers": servers})
			if err != nil {
				return "", err
			}
			if len(inactive) > 0 {
				return "", fmt.Errorf("native Copilot agent MCP field %s has no mapping", inactive[0].Path)
			}
			for _, name := range nativeSortedKeys(servers) {
				if err := nativeValidateCopilotMCPServer(servers[name].(map[string]any)); err != nil {
					return "", fmt.Errorf("native Copilot agent MCP server %s: %w", name, err)
				}
			}
			valid = true
		default:
			return "", fmt.Errorf("native Copilot agent field %s has no mapping", key)
		}
		if !valid {
			return "", fmt.Errorf("invalid native Copilot agent field %s", key)
		}
	}
	name, _ := fields["name"].(string)
	if name == "" {
		name = nativeCopilotAgentStem(path)
	}
	return strings.TrimSpace(name), nil
}

func nativeCopilotAgentStem(path string) string {
	return strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".md"), ".agent")
}

// Do not rewrite a credential-bearing agent: filtering its frontmatter can
// change its behavior. Refuse the import before the transaction writes files.
// Unknown noncredential fields remain preserved for inactive projection.
func nativeCheckCopilotAgentImport(data []byte) error {
	fields, err := nativeDecodeCopilotAgent(data)
	if err != nil {
		return err
	}
	return nativeCheckCopilotAgentFields(fields)
}

func nativeCheckCopilotAgentFields(source map[string]any) error {
	fields := map[string]any{}
	for key, value := range source {
		fields[key] = value
	}
	if servers, exists := fields["mcp-servers"]; exists {
		delete(fields, "mcp-servers")
		if nativeContainsExcluded("copilot", nil, map[string]any{"mcpServers": servers}) {
			return fmt.Errorf("native Copilot agent contains excluded authority or credential fields")
		}
		if definitions, ok := servers.(map[string]any); ok {
			for _, entry := range definitions {
				if server, ok := entry.(map[string]any); ok {
					if address, ok := server["url"].(string); ok {
						parsed, err := url.Parse(address)
						if err != nil || parsed.User != nil {
							return fmt.Errorf("native Copilot agent MCP URL is malformed or contains credentials")
						}
					}
				}
			}
		}
	}
	if nativeContainsExcluded("copilot", nil, fields) {
		return fmt.Errorf("native Copilot agent contains excluded authority or credential fields")
	}
	return nil
}

func nativeCopilotAgentFeature(feature NativeFeature) NativeFeature {
	feature.Disposition, feature.Activation = "artifact-mapping", "pending native reload"
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Copilot 1.0.83 fixture verifies discovery, instructions, delegation, and an approved child command. Model and reasoning overrides and other frontmatter controls need separate evidence. Agent-local MCP has separate discovery, delegation, and parent-isolation fixtures."
	evidence := "WORKBENCH/evidence/native-draft2-debug/copilot-agent-final-user.json"
	if feature.Scope == "project" {
		evidence = "WORKBENCH/evidence/native-draft2-debug/copilot-agent-final-project.json"
	}
	feature.Evidence = []string{evidence, "https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#custom-agent-frontmatter-fields"}
	return feature
}

func nativeCopilotAgentFields(feature NativeFeature) []NativeFeature {
	var fields []NativeFeature
	for _, name := range []string{"description", "disable-model-invocation", "infer", "mcp-servers", "metadata", "model", "name", "reasoningEffort", "target", "tools", "user-invocable"} {
		field := feature
		field.Feature, field.Source = "artifact:agent:/"+name, "native_copilot_agent.go"
		field.Disposition, field.NativeStatus = "validator-declared", "unverified"
		field.Limitation = "Documented frontmatter field with a typed validator. Effective native behavior needs separate evidence."
		if name == "name" || name == "description" || name == "tools" {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "Fixture verifies this field with native discovery and delegation. Other values need separate checks."
		}
		if name == "infer" {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-discovery"
			field.Limitation = "infer:false removes the fixture agent from automatic task selection."
			field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-agent-no-infer.json"}
			if feature.Scope == "user" {
				field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-agent-no-infer-user.json"}
			}
		}
		if name == "mcp-servers" {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "Local MCP fixture verifies child discovery and execution, parent isolation, and a child override of a same-name user server. Remote transports and other source precedence need separate evidence. Unknown nested fields keep the entire agent inactive."
			field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-agent-mcp-final-" + feature.Scope + ".json", "WORKBENCH/evidence/native-draft2-debug/copilot-agent-mcp-override-" + feature.Scope + ".json"}
		}
		fields = append(fields, field)
	}
	return fields
}
