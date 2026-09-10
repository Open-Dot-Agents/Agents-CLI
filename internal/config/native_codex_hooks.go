package config

import (
	"fmt"
	"sort"
)

var nativeCodexHookEvents = map[string]bool{"SessionStart": true, "SessionEnd": true, "PreToolUse": true, "PermissionRequest": true, "PostToolUse": true, "PreCompact": true, "PostCompact": true, "UserPromptSubmit": true, "SubagentStart": true, "SubagentStop": true, "Stop": true, "Interrupt": true}
var nativeCodexHookFields = map[string][]string{
	"command":  {"type", "command", "commandWindows", "timeout", "statusMessage", "additionalContextLimit", "async"},
	"mcp_tool": {"type", "server", "tool", "input", "timeout", "statusMessage"},
}

// Use the pinned native schema for values, but do not use its permissive
// unknown-property behavior as an activation map. Each event array is atomic.
func nativeSelectCodexHooks(values map[string]any, inline bool) (map[string]any, []nativeInactiveField, error) {
	selected := map[string]any{}
	blockedRoot := false
	var inactive []nativeInactiveField
	reject := func(path, reason string) { inactive = append(inactive, nativeInactiveField{path, "inactive", reason}) }
	for _, key := range nativeSortedKeys(values) {
		switch key {
		case "description":
			if inline {
				reject("/description", "description is metadata for a hooks.json file")
				continue
			}
			if _, ok := values[key].(string); !ok {
				return nil, nil, fmt.Errorf("native Codex hook description must be a string")
			}
			selected[key] = values[key]
		case "disableAllHooks":
			if _, ok := values[key].(bool); !ok {
				return nil, nil, fmt.Errorf("native hook disableAllHooks must be a boolean")
			}
			reject("/disableAllHooks", "Codex hooks.json does not implement disableAllHooks")
			blockedRoot = true
		case "hooks":
			events, ok := values[key].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("native Codex hooks must be an object")
			}
			output := map[string]any{}
			for _, event := range nativeSortedKeys(events) {
				path := "/hooks/" + nativePointer(event)
				if event == "state" {
					reject(path, "native hook trust and enable state remain external")
					continue
				}
				if !nativeCodexHookEvents[event] {
					reject(path, "unknown native Codex hook event")
					continue
				}
				groups, ok := events[event].([]any)
				if !ok {
					return nil, nil, fmt.Errorf("native Codex hook event must be an array")
				}
				valid := true
				validation := make([]any, 0, len(groups))
				for i, entry := range groups {
					group, ok := entry.(map[string]any)
					if !ok {
						return nil, nil, fmt.Errorf("native Codex hook group must be an object")
					}
					copyGroup := map[string]any{}
					for _, key := range nativeSortedKeys(group) {
						value := group[key]
						if key != "matcher" && key != "hooks" {
							reject(fmt.Sprintf("%s/%d/%s", path, i, nativePointer(key)), "unknown native hook group field")
							valid = false
						} else {
							copyGroup[key] = value
						}
					}
					if handlers, exists := group["hooks"]; exists {
						array, ok := handlers.([]any)
						if !ok {
							return nil, nil, fmt.Errorf("native Codex hook handlers must be an array")
						}
						copies := make([]any, 0, len(array))
						for j, handler := range array {
							fields, ok := handler.(map[string]any)
							if !ok {
								return nil, nil, fmt.Errorf("native Codex hook handler must be an object")
							}
							kind, ok := fields["type"].(string)
							if !ok {
								return nil, nil, fmt.Errorf("native Codex hook handler requires a type string")
							}
							keys, known := nativeCodexHookFields[kind]
							if !known {
								reason := "unknown native Codex hook handler"
								if kind == "agent" || kind == "prompt" {
									reason = "native Codex parses this handler type but skips execution"
								}
								reject(fmt.Sprintf("%s/%d/hooks/%d", path, i, j), reason)
								valid = false
								continue
							}
							if kind == "mcp_tool" {
								if input, exists := fields["input"]; exists && nativeCodexHookInputHasNull(input) {
									return nil, nil, fmt.Errorf("native Codex MCP hook input cannot contain null; the native parser requires TOML-compatible values")
								}
								if event == "SessionEnd" {
									reject(fmt.Sprintf("%s/%d/hooks/%d", path, i, j), "native Codex skips MCP tool handlers for SessionEnd")
									valid = false
								}
							}
							copyHandler := map[string]any{}
							for _, key := range nativeSortedKeys(fields) {
								name := key
								if key == "command_windows" {
									if _, duplicate := fields["commandWindows"]; duplicate {
										return nil, nil, fmt.Errorf("duplicate native hook commandWindows alias")
									}
									name = "commandWindows"
								}
								mapped := false
								for _, allowed := range keys {
									mapped = mapped || name == allowed
								}
								if !mapped {
									reject(fmt.Sprintf("%s/%d/hooks/%d/%s", path, i, j, nativePointer(key)), "unknown native hook handler field")
									valid = false
									continue
								}
								if nativeContainsExcluded("codex", []string{"hooks", event, "hooks", key}, fields[key]) {
									return nil, nil, fmt.Errorf("native Codex hook contains excluded authority or credential fields")
								}
								copyHandler[name] = fields[key]
							}
							copies = append(copies, copyHandler)
						}
						copyGroup["hooks"] = copies
					}
					validation = append(validation, copyGroup)
				}
				if !nativeCodexValue("hooks", map[string]any{event: validation}) {
					return nil, nil, fmt.Errorf("native Codex hook %s fails the pinned native schema", event)
				}
				if valid {
					output[event] = groups
				} else {
					reject(path, "entire native hook event remains inactive")
				}
			}
			if len(output) > 0 || len(events) == 0 {
				selected[key] = output
			}
		default:
			reject("/"+nativePointer(key), "unknown native hook root field; the native file parser rejects the whole file")
			blockedRoot = true
		}
	}
	if blockedRoot {
		return map[string]any{}, inactive, nil
	}
	if _, ok := selected["hooks"]; !ok {
		return map[string]any{}, inactive, nil
	}
	return selected, inactive, nil
}

// The pinned JSON hook parser converts MCP input to TOML before loading it.
// Its schema permits arbitrary JSON here, but null rejects the entire file.
func nativeCodexHookInputHasNull(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, child := range value {
			if nativeCodexHookInputHasNull(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if nativeCodexHookInputHasNull(child) {
				return true
			}
		}
	}
	return false
}

func nativeCodexHookFeature(feature NativeFeature) NativeFeature {
	feature.Disposition, feature.Activation = "artifact-mapping", "requires native hook trust and reload"
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Codex 0.154.0 fixtures verify command and PreToolUse/PostToolUse MCP handlers, native hook trust, matchers, context, and tool effects. Apply does not set trust. MCP input null is refused; SessionEnd MCP handlers stay inactive. Background PreToolUse command execution and context spilling also have bounded evidence. Other events and Windows overrides need separate evidence."
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-hooks-final-" + feature.Scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-final-execution-" + feature.Scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-background-final-background-next-" + feature.Scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-background-final-spill-" + feature.Scope + ".json", "https://learn.chatgpt.com/docs/hooks"}
	return feature
}
func nativeCodexHookFieldFeatures(feature NativeFeature) []NativeFeature {
	var fields []NativeFeature
	kinds := []string{"command", "mcp_tool"}
	for _, kind := range kinds {
		names := append([]string(nil), nativeCodexHookFields[kind]...)
		sort.Strings(names)
		for _, name := range names {
			field := feature
			field.Feature = "artifact:hooks:/hooks/<event>[]/hooks/<" + kind + ">/" + name
			field.Source = "native_codex_hooks.go; native_schemas/codex-0.154.0.json"
			field.Disposition, field.NativeStatus = "validator-declared", "unverified"
			field.Limitation = "Typed native field with a pinned schema check; effective behavior needs separate native evidence."
			if kind == "command" && (name == "command" || name == "type" || name == "statusMessage") {
				field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
				field.Limitation = "Synchronous command fixture with native status metadata. UI status display and other handler types need separate checks."
			}
			if kind == "command" && name == "async" {
				field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
				field.Limitation = "PreToolUse background fixtures continue before hook completion and deliver context at a later model step. A background denial cannot block the completed operation. This app-server path emits no background hook start/completion notifications. Timeout, archive, and shutdown stop the tested hook and attached child; a detached child can outlive the timeout and produce effects. Unsubscribe keeps the thread loaded during its native grace period. SessionEnd ignores async; other events need separate tests."
				field.Evidence = nil
				for _, scenario := range []string{"background-next", "background-active", "background-deny", "background-timeout", "background-detached-timeout", "background-unsubscribe", "background-shutdown", "background-archive"} {
					field.Evidence = append(field.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-background-final-"+scenario+"-"+feature.Scope+".json")
				}
			}
			if kind == "command" && name == "additionalContextLimit" {
				field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
				field.Limitation = "SessionStart fixtures verify zero (unlimited), positive, and omitted limits, full spill-file bytes, and independent handlers. Background PreToolUse fixtures verify delayed spilling and unlimited output. Tiny limits can leave only the saved-file notice. Native spill permissions, location, and cleanup are not adapter-managed. Unrelated events and spill-write failures need separate tests."
				field.Evidence = nil
				for _, scenario := range []string{"spill", "spill-tiny", "unlimited", "spill-default", "spill-mixed", "background-spill", "background-unlimited"} {
					field.Evidence = append(field.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-background-final-"+scenario+"-"+feature.Scope+".json")
				}
			}
			if kind == "mcp_tool" {
				field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
				field.Limitation = "PreToolUse and PostToolUse synchronous local MCP fixtures with native trust, input expansion, context, and native status metadata. SessionEnd is skipped by native Codex. UI rendering, remote transports, elicitation, and other events need separate tests."
				field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-final-execution-" + feature.Scope + ".json"}
				if name == "input" {
					field.Limitation += " Null input values are refused. Missing event references fail the hook without blocking the operation under the fixture policy."
					field.Evidence = append(field.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-final-template-missing-"+feature.Scope+".json", "WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-parser.json")
				}
				if name == "timeout" {
					field.Limitation = "The one-second hook timeout ends the native wait. A fixture server that ignores cancellation still finishes its work. This does not prove MCP tool termination. Server timeout precedence and elicitation need separate tests."
					field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-final-timeout-" + feature.Scope + ".json"}
				}
			}
			fields = append(fields, field)
		}
	}
	for _, name := range []string{"artifact:hooks:/hooks/<event>", "artifact:hooks:/hooks/<event>[]/hooks", "artifact:hooks:/hooks/<event>[]/matcher"} {
		field := feature
		field.Feature, field.Source = name, "native_codex_hooks.go"
		field.Disposition, field.NativeStatus = "validator-declared", "unverified"
		field.Limitation = "Explicit event and handler registry with pinned schema validation. Fixtures cover SessionStart, PreToolUse, and PostToolUse command handlers and PreToolUse/PostToolUse MCP handlers. SessionEnd MCP handlers remain inactive; other events need separate native tests."
		fields = append(fields, field)
	}
	return fields
}

// Trust and per-hook enable state belong to the native user. Exclude that
// subtree on import, but refuse credential-bearing event arrays as a whole.
func nativeCheckCodexHookImport(values map[string]any) error {
	hooks, ok := values["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	events := map[string]any{}
	for key, value := range hooks {
		if key != "state" {
			events[key] = value
		}
	}
	if nativeContainsExcluded("codex", []string{"hooks"}, events) {
		return fmt.Errorf("native Codex hooks contain excluded authority or credential fields")
	}
	return nil
}
