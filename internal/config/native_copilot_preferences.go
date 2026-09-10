package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Constraints from the pinned help and settings reference. A native fallback
// to a default is not successful activation of the requested value.
func nativeCopilotPreferenceConstraint(path []string, value any) string {
	field := strings.Join(path, ".")
	var minimum, maximum float64
	switch field {
	case "statusLine.command":
		command, ok := value.(string)
		if !ok || strings.TrimSpace(command) == "" || strings.ContainsRune(command, '\x00') {
			return "status-line command must be a nonempty string without NUL"
		}
		return ""
	case "commandHistoryMaxSize":
		minimum, maximum = 1, 1000
	case "inlineImageLiveWindow", "statusLine.padding":
		minimum, maximum = 0, 9007199254740991 // Largest exact JavaScript integer.
	case "statusLine.refreshInterval":
		minimum, maximum = 1, 2147483
	default:
		if len(path) == 3 && path[0] == "tabs" && (path[1] == "hide" || path[1] == "sort") {
			name, ok := value.(string)
			if !ok {
				return "tab identifiers must be strings"
			}
			switch strings.ToLower(name) {
			case "copilot":
				if path[1] == "hide" {
					return "Copilot ignores requests to hide the Session tab"
				}
			case "agents", "issues", "pull-requests", "gists":
			default:
				return "unknown tab identifiers are ignored by Copilot and remain inactive"
			}
		}
		return ""
	}
	if !nativeValueType("integer", value) {
		return "preference requires an integer; native fallback values are not projected"
	}
	number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
	if err != nil || number < minimum || number > maximum {
		return fmt.Sprintf("preference integer must be between %.0f and %.0f", minimum, maximum)
	}
	return ""
}

func nativeCopilotPreferenceFeature(feature NativeFeature, key string) NativeFeature {
	if feature.Scope != "user" {
		return feature
	}
	switch key {
	case "defaultMode":
		feature.Limitation = "Fresh interactive and plan sessions show the selected initial mode in the pinned terminal fixture. Autopilot execution and permission behavior need separate evidence."
	case "tabs":
		feature.Limitation = "The pinned terminal fixture verifies enabled, disabled, sorted, hidden, and restored tabs. Names are case-insensitive. Unknown names and requests to hide the Session tab remain inactive because native ignores them."
	case "statusLine":
		feature.Limitation = "The pinned terminal fixture correlates native session JSON, command file events, output, padding, timer refresh, and removal. Type is optional. Command failure handling and event-only refresh need separate evidence. Apply writes configuration; the native terminal runs the command."
	case "inlineImages", "inlineImageLiveWindow", "notifications", "commandHistoryMaxSize":
		feature.NativeStatus = "unverified"
		feature.Limitation = "Configuration values are validated and preserved. Native image rendering, notification delivery, and command history behavior need separate environment and execution evidence."
		return feature
	default:
		return feature
	}
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-preferences-final.json"}
	return feature
}

func nativeCopilotPreferenceCapabilities(scope, destination string) []NativeFeature {
	if scope != "user" {
		return nil
	}
	var fields []NativeFeature
	for _, key := range []string{"defaultMode", "tabs", "tabs.enabled", "tabs.sort", "tabs.hide", "statusLine", "statusLine.type", "statusLine.command", "statusLine.padding", "statusLine.refreshInterval"} {
		fields = append(fields, nativeCopilotPreferenceFeature(NativeFeature{
			Feature: "artifact:preferences:/" + strings.ReplaceAll(key, ".", "/"),
			Source:  "native_copilot_preferences.go", Destination: destination, Scope: scope,
			Disposition: "artifact-field-mapping", Activation: "requires value validation and a fresh native interactive session",
			Ownership: "setting", Authority: "user preferences; native trust and permission state remain external",
		}, strings.Split(key, ".")[0]))
	}
	return fields
}
