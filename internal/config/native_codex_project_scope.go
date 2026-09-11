package config

import "strings"

// Codex 0.154.0 removes these fields from trusted project configuration before
// it merges native layers. Keep the pinned loader's scope rule explicit.
var nativeCodexProjectIgnored = map[string]bool{
	"openai_base_url": true, "chatgpt_base_url": true,
	"apps_mcp_product_sku": true, "responses_api_metadata": true,
	"model_provider": true, "model_providers": true,
	"notify": true, "profile": true, "profiles": true,
	"experimental_realtime_webrtc_call_base_url": true,
	"experimental_realtime_ws_base_url":          true, "otel": true,
	"features.respect_system_proxy": true,
}

func nativeCodexProjectRestriction(path []string) string {
	if len(path) == 0 {
		return ""
	}
	key := path[0]
	if key == "features" && len(path) > 1 {
		key += "." + path[1]
	}
	if !nativeCodexProjectIgnored[key] {
		return ""
	}
	if key == "otel" {
		return "Codex 0.154.0 ignores project telemetry; use native user scope"
	}
	return "Codex 0.154.0 ignores project " + key + "; use native user scope where the setting has a supported mapping"
}

func nativeCodexProjectScopeCapabilities(target string) []NativeFeature {
	var features []NativeFeature
	keys := map[string]any{}
	for key := range nativeCodexProjectIgnored {
		keys[key] = nil
	}
	for _, key := range nativeSortedKeys(keys) {
		features = append(features, NativeFeature{Feature: "artifact:project-scope:/" + strings.ReplaceAll(key, ".", "/"),
			Source: "native_codex_project_scope.go", Destination: target, Scope: "project",
			Disposition: "native-ignored", Activation: "inactive", Ownership: "no new ownership",
			Authority: "pinned native project-layer restriction", NativeStatus: "native-effective-configuration",
			Limitation: nativeCodexProjectRestriction(strings.Split(key, ".")) + ". Required profiles refuse before writes; optional source content remains preserved and inactive. The native fixture verifies effective configuration and routes the model request to the user provider. Credential-broker-dependent fields and user-scope behavior for other settings need separate checks.",
			Evidence:   nativeCodexProjectScopeEvidence()})
	}
	return features
}

func nativeCodexProjectScopeEvidence() []string {
	return []string{"WORKBENCH/evidence/native-draft2-debug/codex-project-provider-verified.json", "WORKBENCH/evidence/native-draft2-debug/codex-project-allkeys-verified.json", "WORKBENCH/evidence/native-draft2-debug/codex-provider-scope.sources.json"}
}
