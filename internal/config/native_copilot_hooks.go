package config

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

var nativeCopilotHookEvents = map[string]string{
	"sessionStart": "sessionStart", "SessionStart": "sessionStart",
	"sessionEnd": "sessionEnd", "SessionEnd": "sessionEnd",
	"userPromptSubmitted": "userPromptSubmitted", "UserPromptSubmit": "userPromptSubmitted",
	"userPromptTransformed": "userPromptTransformed",
	"preToolUse":            "preToolUse", "PreToolUse": "preToolUse",
	"postToolUse": "postToolUse", "PostToolUse": "postToolUse",
	"postToolUseFailure": "postToolUseFailure", "PostToolUseFailure": "postToolUseFailure",
	"agentStop": "agentStop", "Stop": "agentStop",
	"subagentStart": "subagentStart", "SubagentStart": "subagentStart",
	"subagentStop": "subagentStop", "SubagentStop": "subagentStop",
	"errorOccurred": "errorOccurred", "ErrorOccurred": "errorOccurred",
	"preCompact": "preCompact", "PreCompact": "preCompact",
	"permissionRequest": "permissionRequest", "PermissionRequest": "permissionRequest",
	"notification": "notification", "Notification": "notification",
}
var nativeCopilotHookFields = map[string]string{
	"type": "string", "bash": "string", "powershell": "string", "command": "string", "exec": "string",
	"args": "array<string>", "cwd": "string", "env": "map<string,string>", "timeout": "number", "timeoutSec": "number",
	"matcher": "string", "url": "string", "headers": "map<string,string>", "allowedEnvVars": "array<string>", "prompt": "string",
}

// Events are atomic ordered arrays. Do not activate a filtered array: unknown
// members or fields can affect the order, matcher, or a decision made by a hook.
func nativeSelectCopilotHooks(values map[string]any, inline bool) (map[string]any, []nativeInactiveField, error) {
	selected := map[string]any{}
	var inactive []nativeInactiveField
	reject := func(path, reason string) { inactive = append(inactive, nativeInactiveField{path, "inactive", reason}) }
	if !inline {
		if _, ok := values["version"]; !ok {
			return selected, []nativeInactiveField{{"/version", "inactive", "native hook files require version 1"}}, nil
		}
		if nativeHash(values["version"]) != nativeHash(1) {
			return nil, nil, fmt.Errorf("native Copilot hook file requires version 1")
		}
	}
	if err := nativeCheckCopilotHookCredentials(values); err != nil {
		return nil, nil, err
	}
	for _, key := range nativeSortedKeys(values) {
		value := values[key]
		switch key {
		case "version":
			if inline {
				reject("/version", "inline hooks have no version field")
			} else {
				selected[key] = value
			}
		case "disableAllHooks":
			if inline {
				reject("/disableAllHooks", "global hook control needs separate authority checks")
				continue
			}
			if _, ok := value.(bool); !ok {
				return nil, nil, fmt.Errorf("native hook disableAllHooks must be a boolean")
			}
			selected[key] = value
		case "description":
			if _, ok := value.(string); !ok {
				return nil, nil, fmt.Errorf("native hook description must be a string")
			}
			selected[key] = value
		case "hooks":
			events, ok := value.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("native hooks must be an object")
			}
			output := map[string]any{}
			for _, event := range nativeSortedKeys(events) {
				path := "/hooks/" + nativePointer(event)
				canonical, known := nativeCopilotHookEvents[event]
				if !known {
					reject(path, "unknown native hook event")
					continue
				}
				entries, ok := events[event].([]any)
				if !ok {
					return nil, nil, fmt.Errorf("native hook event %s must be an array", event)
				}
				valid := true
				for i, entry := range entries {
					item, ok := entry.(map[string]any)
					if !ok {
						return nil, nil, fmt.Errorf("native hook entry must be an object")
					}
					unmapped, err := nativeValidateCopilotHook(item, canonical)
					if err != nil {
						return nil, nil, fmt.Errorf("native hook %s/%d: %w", path, i, err)
					}
					if unmapped != "" {
						reject(fmt.Sprintf("%s/%d", path, i), unmapped)
						valid = false
					}
				}
				if valid {
					output[event] = entries
				} else {
					reject(path, "entire ordered hook event remains inactive")
				}
			}
			if len(output) > 0 || len(events) == 0 {
				selected[key] = output
			}
		default:
			reject("/"+nativePointer(key), "unknown native hook root field")
		}
	}
	// Metadata and an enable flag alone do not constitute an active hook mapping.
	if _, ok := selected["hooks"]; !ok {
		return map[string]any{}, inactive, nil
	}
	return selected, inactive, nil
}

func nativeValidateCopilotHook(item map[string]any, event string) (string, error) {
	unknown := ""
	for _, field := range nativeSortedKeys(item) {
		kind, known := nativeCopilotHookFields[field]
		if !known {
			unknown = "unknown native hook field " + field
			continue
		}
		if !nativeLSPValue(kind, item[field]) {
			return "", fmt.Errorf("invalid native hook field %s", field)
		}
	}
	if unknown != "" {
		return unknown, nil
	}
	kind := "command"
	if value, exists := item["type"]; exists {
		kind = value.(string)
	}
	text := func(key string) string { value, _ := item[key].(string); return value }
	has := func(key string) bool { _, ok := item[key]; return ok }
	forbidden := func(keys ...string) error {
		for _, key := range keys {
			if has(key) {
				return fmt.Errorf("native %s hook contains incompatible field %s", kind, key)
			}
		}
		return nil
	}
	switch kind {
	case "command":
		if err := forbidden("url", "headers", "allowedEnvVars", "prompt"); err != nil {
			return "", err
		}
		if has("exec") {
			if text("exec") == "" {
				return "", fmt.Errorf("native hook exec must not be empty")
			}
			if err := forbidden("bash", "powershell", "command"); err != nil {
				return "", err
			}
		} else {
			if err := forbidden("args"); err != nil {
				return "", err
			}
			if text("bash") == "" && text("command") == "" {
				if text("powershell") != "" {
					return "Windows-only hook has no Linux command", nil
				}
				return "", fmt.Errorf("native command hook requires bash, command, or exec")
			}
		}
	case "http":
		if err := forbidden("exec", "args", "bash", "powershell", "command", "cwd", "env", "prompt"); err != nil {
			return "", err
		}
		parsed, err := url.Parse(text("url"))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return "", fmt.Errorf("native HTTP hook requires an HTTP or HTTPS URL without credentials")
		}
		address := net.ParseIP(parsed.Hostname())
		if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && (address == nil || !address.IsLoopback()) {
			return "", fmt.Errorf("native HTTP hooks require HTTPS outside localhost")
		}
		if parsed.Scheme != "https" && (event == "preToolUse" || event == "permissionRequest" || has("allowedEnvVars")) {
			return "", fmt.Errorf("native hook permission responses and allowedEnvVars require HTTPS")
		}
	case "prompt":
		if event != "sessionStart" || text("prompt") == "" {
			return "", fmt.Errorf("native prompt hook requires sessionStart and a non-empty prompt")
		}
		if err := forbidden("exec", "args", "bash", "powershell", "command", "cwd", "env", "url", "headers", "allowedEnvVars", "matcher", "timeout", "timeoutSec"); err != nil {
			return "", err
		}
	default:
		return "unknown native hook type", nil
	}
	return "", nil
}

// Refuse a credential-bearing hook import, rather than remove part of an
// ordered hook array. Native reference syntax is preserved without expansion.
func nativeCheckCopilotHookCredentials(values map[string]any) error {
	hooks, exists := values["hooks"]
	if !exists {
		return nil
	}
	if nativeContainsExcluded("copilot", []string{"hooks"}, hooks) {
		return fmt.Errorf("native hooks contain excluded authority or credential fields")
	}
	var check func(any) error
	check = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if key == "url" {
					if address, ok := child.(string); ok {
						parsed, err := url.Parse(address)
						if err != nil || parsed.User != nil {
							return fmt.Errorf("native hook URL is malformed or contains credentials")
						}
					}
				}
				if err := check(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := check(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return check(hooks)
}

func nativeCopilotHookFeature(feature NativeFeature) NativeFeature {
	feature.Disposition, feature.Activation = "artifact-mapping", "pending native reload"
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Copilot 1.0.83 fixtures cover local command and HTTP hooks, direct arguments, literal environment values, working directory, matchers, file disable, and timeout behavior. Project evidence uses CLI prompt mode; ACP did not load project hooks. A timeout allows the normal permission flow to continue. Other hook types and events need separate evidence."
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-final-" + feature.Scope + ".json", "https://docs.github.com/en/copilot/reference/hooks-reference"}
	return feature
}

func nativeCopilotHookFieldFeatures(feature NativeFeature) []NativeFeature {
	var fields []NativeFeature
	names := make([]string, 0, len(nativeCopilotHookFields))
	for name := range nativeCopilotHookFields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := feature
		field.Feature = "artifact:hooks:/hooks/<event>[]/" + name
		field.Source = "native_copilot_hooks.go: " + nativeCopilotHookFields[name]
		field.Disposition, field.NativeStatus = "validator-declared", "unverified"
		field.Limitation = "Documented native hook field with typed validation; effective behavior needs separate evidence."
		if strings.Contains("|type|bash|command|exec|args|cwd|env|matcher|", "|"+name+"|") {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "Local command-hook fixture only. Direct exec environment values remain literal, including ${VAR}; no portable reference semantics are claimed."
		}
		if name == "timeout" || name == "timeoutSec" {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "A one-second timeout terminates the slow fixture, then tool execution continues. timeoutSec takes precedence over timeout. The alias-only fixture verifies timeout separately. This is not portable mandatory denial."
			field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-timeout-" + feature.Scope + ".json", "WORKBENCH/evidence/native-draft2-debug/copilot-hooks-timeout-alias-" + feature.Scope + ".json"}
		}
		if name == "url" || name == "headers" {
			field.Disposition, field.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
			field.Limitation = "Local HTTP postToolUse fixture with literal headers and native COPILOT_HOOK_ALLOW_LOCALHOST opt-in. TLS, header expansion, and HTTP permission decisions need separate evidence."
			field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-http-" + feature.Scope + ".json"}
		}
		fields = append(fields, field)
	}
	control := feature
	control.Feature, control.Source = "artifact:hooks:/disableAllHooks", "native_copilot_hooks.go: boolean"
	control.Disposition, control.NativeStatus = "artifact-field-mapping", "bounded-fixture-execution"
	control.Limitation = "File-scoped disable only; the neighbouring hook file still runs. Changes cannot affect events owned by another source. The global settings switch stays blocked."
	control.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-disabled-" + feature.Scope + ".json"}
	fields = append(fields, control)
	return fields
}

// A file-level switch affects all events in that file, including events owned
// by another repository. Key ownership alone is insufficient for this switch.
func nativeCopilotHookControlOwnership(path, root string, target *nativeTarget, values map[string]any, state nativeRegistry, fileControl bool) error {
	desired, selected := target.settings["/disableAllHooks"]
	owner, owned := state.Settings[nativeKey(path, "/disableAllHooks")]
	if !selected && (!owned || owner.Source != root) {
		return nil
	}
	if !fileControl {
		return fmt.Errorf("native global hook control requires separate authority checks")
	}
	oldDisabled, _ := values["/disableAllHooks"].(bool)
	newDisabled, _ := desired.(bool)
	if oldDisabled == newDisabled {
		return nil
	}
	for key := range values {
		if key != "/hooks" && !strings.HasPrefix(key, "/hooks/") {
			continue
		}
		eventOwner, eventOwned := state.Settings[nativeKey(path, key)]
		if eventOwned && eventOwner.Source == root {
			continue
		}
		if !eventOwned {
			if _, adopting := target.sources[key]; adopting {
				continue
			}
		}
		return fmt.Errorf("native hook control cannot enable or disable unowned events in %s", path)
	}
	return nil
}
