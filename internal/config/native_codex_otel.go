package config

import (
	"net/url"
	"path/filepath"
	"strings"
)

// TLS files remain external. Resolve relative references against the source
// config directory before relocating configuration. Native HOME expressions
// remain unchanged: --native-home does not establish the native process HOME.
func nativeRebaseCodexOtelImport(values map[string]any, home string) bool {
	otel, _ := values["otel"].(map[string]any)
	changed := false
	for _, signal := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		exporter, _ := otel[signal].(map[string]any)
		for _, transport := range []string{"otlp-http", "otlp-grpc"} {
			options, _ := exporter[transport].(map[string]any)
			tls, _ := options["tls"].(map[string]any)
			for _, field := range []string{"ca-certificate", "client-certificate", "client-private-key"} {
				path, ok := tls[field].(string)
				if !ok || path == "" || strings.ContainsRune(path, 0) || filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
					continue
				}
				tls[field] = filepath.Join(home, path)
				changed = true
			}
		}
	}
	return changed
}

func nativeCodexOtelConstraint(path []string, value any) string {
	if len(path) != 3 || path[0] != "otel" || path[2] != "otlp-http" {
		return ""
	}
	switch path[1] {
	case "exporter", "trace_exporter", "metrics_exporter":
	default:
		return ""
	}
	options, _ := value.(map[string]any)
	tls, _ := options["tls"].(map[string]any)
	_, certificate := tls["client-certificate"]
	_, key := tls["client-private-key"]
	if certificate || key {
		return "Codex 0.154.0 cannot build the tested OTLP HTTP client identity; the complete exporter remains inactive to preserve authentication"
	}
	return ""
}

func nativeCodexOtelCAFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Codex 0.154.0 OTLP HTTP JSON logs and traces reach a local TLS collector using the configured CA. Imported relative TLS paths retain their original absolute targets. Absolute paths and native HOME expressions retain their meanings. No private files are read or copied by import/apply. HTTP client identities fail in the pin and keep the complete exporter inactive; gRPC, metrics, and certificate rotation need separate evidence."
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-ca-relative-final.json", "WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-ca-absolute-final.json", "WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-ca-home-final.json"}
	return feature
}

// Exporter URLs and headers carry values, unlike TLS certificate/key paths.
// Keep known inline credentials external. Do not read or own referenced files.
func nativeCodexOtelExcluded(path []string, value any) bool {
	if len(path) < 4 || path[0] != "otel" {
		return false
	}
	switch path[1] {
	case "exporter", "trace_exporter", "metrics_exporter":
	default:
		return false
	}
	credentialKey := func(key string) bool {
		key = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
		return nativeForbidden(key) || strings.Contains(key, "token") || key == "cookie" || key == "setcookie"
	}
	if len(path) == 5 && path[3] == "headers" {
		return credentialKey(path[4])
	}
	if len(path) == 4 && path[3] == "endpoint" {
		text, ok := value.(string)
		if !ok {
			return false // Type validation rejects non-string endpoints.
		}
		parsed, err := url.Parse(text)
		if err != nil {
			return true
		}
		if parsed.User != nil {
			return true
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return true
		}
		for key := range query {
			if credentialKey(key) {
				return true
			}
		}
	}
	return false
}

func nativeCodexOtelFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-fixture-execution"
	feature.Limitation = "Codex 0.154.0 user OTLP HTTP JSON logs and traces were delivered to local collectors with configured headers and environment. Prompt export on and off were correlated with completed native turns. Project telemetry is ignored and remains inactive. TLS paths refer to external files; HTTP client identities fail in the pin and keep the complete exporter inactive. CA-only TLS has separate bounded evidence. Other exporter modes, metrics delivery, and live reload need separate evidence."
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-http-final.json", "WORKBENCH/evidence/native-draft2-debug/codex-otel-direct-first.json"}
	return feature
}

func nativeCodexOtelCapabilities(scope, destination string) []NativeFeature {
	if scope != "user" {
		return nil
	}
	var features []NativeFeature
	for _, path := range []string{"otel.environment", "otel.exporter", "otel.trace_exporter", "otel.log_user_prompt",
		"otel.exporter.<name>.endpoint", "otel.exporter.<name>.headers", "otel.exporter.<name>.protocol",
		"otel.trace_exporter.<name>.endpoint", "otel.trace_exporter.<name>.headers", "otel.trace_exporter.<name>.protocol"} {
		features = append(features, nativeCodexOtelFeature(NativeFeature{
			Feature: "artifact:telemetry:/" + strings.ReplaceAll(path, ".", "/"), Source: "native_codex_otel.go",
			Destination: destination, Scope: scope, Disposition: "artifact-field-mapping",
			Activation: "requires value validation and native reload", Ownership: "setting",
			Authority: "user telemetry configuration; credentials and referenced files remain external",
		}))
	}
	for _, signal := range []string{"exporter", "trace_exporter"} {
		features = append(features, nativeCodexOtelCAFeature(NativeFeature{
			Feature: "artifact:telemetry:/otel/" + signal + "/<name>/tls/ca-certificate", Source: "native_codex_otel.go",
			Destination: destination, Scope: scope, Disposition: "artifact-field-mapping", Ownership: "setting",
			Activation: "requires value validation and native reload", Authority: "external CA reference; no file ownership",
		}))
	}
	for _, signal := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		for _, field := range []string{"client-certificate", "client-private-key"} {
			features = append(features, NativeFeature{
				Feature: "artifact:telemetry:/otel/" + signal + "/otlp-http/tls/" + field, Source: "native_codex_otel.go",
				Destination: destination, Scope: scope, Disposition: "inactive", Activation: "inactive", Ownership: "none",
				Authority: "external client identity; no file ownership", NativeStatus: "native-exporter-failure",
				Limitation: "Codex 0.154.0 reports an OTLP HTTP client identity builder error with valid EC and RSA fixtures. The complete exporter remains inactive; the adapter does not remove authentication. Other transports need separate evidence.",
				Evidence:   []string{"WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-identity-" + signal + "-ec-final.json", "WORKBENCH/evidence/native-draft2-debug/codex-otel-tls-identity-" + signal + "-rsa-final.json"},
			})
		}
	}
	return features
}
