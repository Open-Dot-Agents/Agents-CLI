package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCodexOtelImportPreservesReferenceTargets(t *testing.T) {
	for _, input := range []string{"tls/ca.pem", "../external/ca.pem", "/external/ca.pem", "~/tls/ca.pem"} {
		t.Run(input, func(t *testing.T) {
			source, target, repo := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			// No referenced file exists. Import must not read, create, or own it.
			data := fmt.Sprintf("[otel.exporter.otlp-http]\nendpoint = 'https://localhost/logs'\nprotocol = 'json'\n[otel.exporter.otlp-http.tls]\nca-certificate = %q\n", input)
			if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte(data), 0640); err != nil {
				t.Fatal(err)
			}
			before := portabilitySnapshot(t, source)
			if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
				t.Fatal(err)
			}
			if nativeHash(before) != nativeHash(portabilitySnapshot(t, source)) {
				t.Fatal("import changed source")
			}
			filename := ".agents/native/com.openai.codex/config.toml"
			first, err := parseNative([]byte(readNativeTest(t, filepath.Join(repo, filename))), "toml")
			if err != nil {
				t.Fatal(err)
			}
			want := input
			if !filepath.IsAbs(input) && !strings.HasPrefix(input, "~") {
				want = filepath.Join(source, input)
			}
			got := first["otel"].(map[string]any)["exporter"].(map[string]any)["otlp-http"].(map[string]any)["tls"].(map[string]any)["ca-certificate"]
			if got != want {
				t.Fatalf("reference became %v, want %s", got, want)
			}
			if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
				t.Fatal(err)
			}
			again := t.TempDir()
			if err := ImportRepositoryWithOptions("codex", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
				t.Fatal(err)
			}
			second, err := parseNative([]byte(readNativeTest(t, filepath.Join(again, filename))), "toml")
			if err != nil || nativeHash(first) != nativeHash(second) {
				t.Fatal("reimport changed reference")
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
				t.Fatal("import or apply created referenced assets")
			}
		})
	}
}

func TestNativeCodexOtelProjectScope(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "required", false: "optional"}[required], func(t *testing.T) {
			repo := nativeFixture(t, "project", "model = 'fixture'\n[otel]\nexporter = 'none'\n")
			profile := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
			if !required {
				data := strings.Replace(readNativeTest(t, profile), `"required":true`, `"required":false`, 1)
				if err := os.WriteFile(profile, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
			if required {
				if err == nil {
					t.Fatal("ignored project telemetry activated")
				}
				if _, err := os.Stat(filepath.Join(repo, ".codex/config.toml")); !os.IsNotExist(err) {
					t.Fatal("refusal wrote target")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readNativeTest(t, filepath.Join(repo, ".codex/config.toml")), "otel") {
				t.Fatal("optional ignored field projected")
			}
			encoded, _ := json.Marshal(plan)
			if !strings.Contains(string(encoded), "otel") || !strings.Contains(string(encoded), "inactive") {
				t.Fatal("missing inactive diagnostic")
			}
		})
	}
}

func TestNativeCodexOtelCredentials(t *testing.T) {
	for name, field := range map[string]string{
		"userinfo":     "endpoint = 'https://fixture:ODA_PRIVATE_VALUE@collector.invalid/logs'\n",
		"query":        "endpoint = 'https://collector.invalid/logs?access_token=ODA_PRIVATE_VALUE'\n",
		"cookie":       "endpoint = 'http://localhost:4318/logs'\nheaders.Cookie = 'ODA_PRIVATE_VALUE'\n",
		"token-header": "endpoint = 'http://localhost:4318/logs'\nheaders.X-Api-Key = 'ODA_PRIVATE_VALUE'\n",
	} {
		t.Run(name, func(t *testing.T) {
			data := "[otel.exporter.otlp-http]\nprotocol = 'json'\n" + field
			repo := nativeFixture(t, "user", data)
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home})
			if err == nil {
				t.Fatal("credential-bearing telemetry activated")
			}
			encoded, _ := json.Marshal(plan)
			if strings.Contains(err.Error()+string(encoded), "ODA_PRIVATE_VALUE") {
				t.Fatal("credential leaked in diagnostic")
			}
			if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("credential refusal wrote target")
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			imported := t.TempDir()
			if err := ImportRepositoryWithOptions("codex", imported, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
				t.Fatal(err)
			}
			for _, filename := range []string{"config.toml", "import-report.json"} {
				if strings.Contains(readNativeTest(t, filepath.Join(imported, ".agents/native/com.openai.codex", filename)), "ODA_PRIVATE_VALUE") {
					t.Fatal("credential imported")
				}
			}
			filtered, err := parseNative([]byte(readNativeTest(t, filepath.Join(imported, ".agents/native/com.openai.codex/config.toml"))), "toml")
			if err != nil {
				t.Fatal(err)
			}
			otel, _ := filtered["otel"].(map[string]any)
			if otel["exporter"] != "none" {
				t.Fatal("credential removal left the exporter active or allowed a native default")
			}
			if readNativeTest(t, filepath.Join(home, "config.toml")) != data {
				t.Fatal("import changed source")
			}
		})
	}
}

func TestNativeCodexOtelUserRoundTrip(t *testing.T) {
	for _, exporter := range []string{"otlp-http", "otlp-grpc"} {
		t.Run(exporter, func(t *testing.T) {
			data := "[otel]\nenvironment = 'fixture'\nlog_user_prompt = false\nmetrics_exporter = 'none'\n"
			for _, kind := range []string{"exporter", "trace_exporter"} {
				data += "[otel." + kind + "." + exporter + "]\nendpoint = 'http://localhost:4318/logs?tenant=fixture'\nheaders = { x-oda-fixture = 'test' }\n"
				if exporter == "otlp-http" {
					data += "protocol = 'json'\n"
				}
				data += "[otel." + kind + "." + exporter + ".tls]\nca-certificate = '/external/ca.pem'\n"
				if exporter == "otlp-grpc" {
					data += "client-certificate = '/external/cert.pem'\nclient-private-key = '/external/private.pem'\n"
				}
			}
			repo := nativeFixture(t, "user", data)
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
				t.Fatal(err)
			}
			again := t.TempDir()
			if err := ImportRepositoryWithOptions("codex", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
				t.Fatal(err)
			}
			want, _ := parseNative([]byte(data), "toml")
			got, err := parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents/native/com.openai.codex/config.toml"))), "toml")
			if err != nil || nativeHash(want) != nativeHash(got) {
				t.Fatal("telemetry did not round trip")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
				t.Fatal("reference assets were copied or external state was written")
			}
		})
	}
}

func TestNativeCodexOtelHTTPIdentityIsAtomic(t *testing.T) {
	for _, signal := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		t.Run(signal, func(t *testing.T) {
			data := "[otel]\nenvironment = 'fixture'\n[otel." + signal + ".otlp-http]\nendpoint = 'https://localhost/logs'\nprotocol = 'json'\n[otel." + signal + ".otlp-http.tls]\nca-certificate = '/external/ca.pem'\nclient-certificate = '/external/client.pem'\nclient-private-key = '/external/client.key'\n"
			values, err := parseNative([]byte(data), "toml")
			if err != nil {
				t.Fatal(err)
			}
			before := nativeHash(values)
			selected, inactive := nativeSelectConfig("codex", "user", values)
			if len(inactive) != 1 || inactive[0].Path != "/otel/"+signal {
				t.Fatal("missing whole-exporter refusal", inactive)
			}
			if nativeHash(selected) != nativeHash(map[string]any{"otel": map[string]any{"environment": "fixture", signal: "none"}}) || before != nativeHash(values) {
				t.Fatal("identity was dropped or source changed")
			}
			if nativeMappedValue("codex", "user", "otel", values["otel"]) {
				t.Fatal("direct value mapping bypasses identity check")
			}
			repo := nativeFixture(t, "user", data)
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
			if _, err := ApplyProjection("codex", repo, options); err == nil {
				t.Fatal("required HTTP identity activated")
			}
			profile := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
			if err := os.WriteFile(profile, []byte(strings.Replace(readNativeTest(t, profile), `"required":true`, `"required":false`, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProjection("codex", repo, options); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readNativeTest(t, filepath.Join(home, "config.toml")), "endpoint") {
				t.Fatal("unauthenticated exporter activated")
			}
			applied, err := parseNative([]byte(readNativeTest(t, filepath.Join(home, "config.toml"))), "toml")
			if err != nil {
				t.Fatal(err)
			}
			if applied["otel"].(map[string]any)[signal] != "none" {
				t.Fatal("native default was not disabled")
			}
		})
	}
}

func TestNativeCodexOtelOptionalCredentialsDisableWholeExporter(t *testing.T) {
	for _, signal := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		for _, transport := range []string{"otlp-http", "otlp-grpc"} {
			t.Run(signal+"/"+transport, func(t *testing.T) {
				data := "[analytics]\nenabled = true\n[otel]\nenvironment = 'fixture'\n[otel." + signal + "." + transport + "]\nendpoint = 'https://collector.invalid'\nheaders.Authorization = 'Bearer ODA_PRIVATE_VALUE'\n"
				if transport == "otlp-http" {
					data += "protocol = 'binary'\n"
				}
				values, err := parseNative([]byte(data), "toml")
				if err != nil {
					t.Fatal(err)
				}
				before := nativeHash(values)
				selected, inactive := nativeSelectConfig("codex", "user", values)
				if len(inactive) != 1 || inactive[0].Path != "/otel/"+signal || inactive[0].Disposition != "external" {
					t.Fatalf("exporter exclusion missing: %v", inactive)
				}
				if selected["otel"].(map[string]any)[signal] != "none" || nativeHash(values) != before {
					t.Fatal("exporter remained active or source was changed")
				}
				filtered, excluded := nativeImportFilter("codex", values, nil)
				if filtered["otel"].(map[string]any)[signal] != "none" || len(excluded) != 1 || excluded[0] != "otel."+signal {
					t.Fatal("import did not retain explicit disablement")
				}
				repo := nativeFixture(t, "user", data)
				profile := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
				writeFixture(t, profile, strings.Replace(readNativeTest(t, profile), `"required":true`, `"required":false`, 1))
				home := t.TempDir()
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home})
				if err != nil {
					t.Fatal(err)
				}
				output := readNativeTest(t, filepath.Join(home, "config.toml"))
				encoded, _ := json.Marshal(plan)
				if strings.Contains(output+string(encoded), "ODA_PRIVATE_VALUE") || strings.Contains(output, "collector.invalid") {
					t.Fatal("credential-bearing exporter was projected")
				}
				applied, err := parseNative([]byte(output), "toml")
				if err != nil {
					t.Fatal(err)
				}
				if applied["otel"].(map[string]any)[signal] != "none" {
					t.Fatal("native default was not disabled")
				}
			})
		}
	}
}
