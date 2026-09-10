package config

import (
	"fmt"
	"strconv"
	"strings"
)

// These are native dispatch preferences. Account-dependent execution limits
// still require the native billing prerequisite; apply cannot grant it.
func nativeCopilotConfigConstraint(path []string, value any) string {
	if reason := nativeCopilotPreferenceConstraint(path, value); reason != "" {
		return reason
	}
	if len(path) < 2 || path[0] != "subagents" {
		return ""
	}
	if len(path) == 3 && path[1] == "disabledSubagents" {
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) == "" || strings.ContainsRune(name, '\x00') {
			return "disabled subagent names must be nonempty strings without NUL"
		}
		if name == "rubber-duck" {
			return "Copilot ignores requests to disable rubber-duck through disabledSubagents"
		}
	}
	if len(path) == 3 && path[1] == "agents" {
		if strings.TrimSpace(path[2]) == "" || strings.ContainsRune(path[2], '\x00') {
			return "subagent names must be nonempty strings without NUL"
		}
	}
	if len(path) == 4 && path[1] == "agents" && path[3] == "model" {
		model, ok := value.(string)
		if !ok || strings.TrimSpace(model) == "" || strings.ContainsRune(model, '\x00') {
			return "subagent model must be a nonempty native model name or inherit"
		}
	}
	if len(path) == 2 && (path[1] == "maxConcurrency" || path[1] == "maxDepth") {
		maximum := float64(32)
		if path[1] == "maxDepth" {
			maximum = 256
		}
		if !nativeValueType("integer (positive)", value) {
			return "subagent limit must be a positive integer; native fallback values are not projected"
		}
		number, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		if err != nil || number > maximum {
			return fmt.Sprintf("subagent limit must not exceed %.0f; native clamping is not projected", maximum)
		}
	}
	return ""
}

func nativeCopilotSubagentFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Copilot 1.0.83 dispatch fixtures verify native model and effort selection, inherit, the configured context-tier event, child command execution, and disabled-agent refusal. Context capacity and billing are not measured. Depth and concurrency overrides require native usage-based billing; the isolated BYOK fixture ignores both settings. Apply does not change account state or enforce runtime resource limits."
	for _, name := range []string{"inherit", "override", "disabled", "limits"} {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/native-draft2-debug/copilot-subagents-verified-"+name+".json")
	}
	return feature
}

func nativeCopilotSubagentCapabilities(scope, destination string) []NativeFeature {
	if scope != "user" {
		return nil
	}
	var fields []NativeFeature
	for _, key := range []string{"subagents", "subagents.agents", "subagents.agents.<name>", "subagents.agents.<name>.model", "subagents.agents.<name>.effortLevel", "subagents.agents.<name>.contextTier", "subagents.disabledSubagents", "subagents.maxConcurrency", "subagents.maxDepth"} {
		field := nativeCopilotSubagentFeature(NativeFeature{
			Feature: "artifact:subagents:/" + strings.ReplaceAll(key, ".", "/"),
			Source:  "native_copilot_subagents.go", Destination: destination, Scope: scope,
			Disposition: "artifact-field-mapping", Activation: "requires value validation and native reload",
			Ownership: "setting", Authority: "user preferences; native account and permission state remain external",
		})
		if key == "subagents.maxConcurrency" || key == "subagents.maxDepth" {
			field.NativeStatus = "native-prerequisite-required"
			field.Activation = "requires native usage-based billing and reload"
			field.Limitation = "Positive integer mapping with native upper bounds. Copilot ignores this setting outside usage-based billing; the BYOK fixture still dispatches two nested agents with both limits set to one. Paid-plan limit enforcement and exact lower-bound behavior require a separate native account fixture. Apply does not change billing or enforce runtime limits."
			field.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-subagents-verified-limits.json"}
		}
		fields = append(fields, field)
	}
	return fields
}
