package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotMCPLocalTypeImport(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	source := `{"mcpServers":{"fixture":{"type":"local","command":"fixture-server","args":[],"tools":["record"],"env":{"MODE":"test","API_KEY":"${FIXTURE_KEY}"},"cwd":"/workspace","timeout":1200,"deferTools":"never","disableToolCache":true}}}`
	if err := os.WriteFile(filepath.Join(home, "mcp-config.json"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	target := t.TempDir()
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	want, _ := parseNative([]byte(source), "json")
	got, err := parseNative([]byte(readNativeTest(t, filepath.Join(target, "mcp-config.json"))), "json")
	if err != nil || nativeHash(got) != nativeHash(want) {
		t.Fatalf("native MCP fixture data changed: %v\ngot: %#v\nwant: %#v", err, got, want)
	}
}

func nativeCopilotMCPFixture(t *testing.T, scope, source string, required bool) string {
	t.Helper()
	repo := nativeLSPFixture(t, scope, `{}`, required)
	dir := filepath.Join(repo, ".agents/native/com.github.copilot")
	profile := nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: required, Artifacts: []nativeArtifact{{Kind: "mcp", Source: "mcp.json"}}}
	data, _ := json.Marshal(profile)
	os.WriteFile(filepath.Join(dir, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(source), 0600)
	return repo
}

func TestNativeCopilotMCPExtendedRoundTrip(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, source := range []string{
			`{"mcpServers":{"fixture":{"type":"stdio","command":"server","args":["--stdio"],"env":{"API_KEY":"${KEY}","MODE":"literal"},"tools":[],"timeout":2000,"cwd":"/fixture"}},"futureRoot":true}`,
			`{"fixture":{"type":"local","command":"server","args":[],"futureField":[1,2]}}`,
			`{"credentials":{"type":"local","command":"server","env":{"API_KEY":"${KEY}"}}}`,
			`{"mcpServers":{"fixture":{"type":"sse","url":"https://fixture.invalid/mcp","headers":{"Authorization":"Bearer ${TOKEN}"},"oauthClientId":"public-id","oauthPublicClient":true,"oauthGrantType":"authorization_code","oidc":false,"tools":["record"]}}}`,
			`{"mcpServers":{"fixture":{"type":"streamable-http","url":"http://127.0.0.1:8000/mcp","timeout":1000}}}`,
		} {
			t.Run(scope+source, func(t *testing.T) {
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				repo, home := t.TempDir(), t.TempDir()
				base := repo
				if scope == "user" {
					base = home
				}
				path, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "mcp"})
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, []byte(source), 0600)
				opts := WriteOptions{Experimental: true, Scope: scope}
				apply := ApplyOptions{Experimental: true, Scope: scope, Adopt: scope == "project"}
				if scope == "user" {
					opts.NativeHome = home
					apply.NativeHome = t.TempDir()
				}
				if err := ImportRepositoryWithOptions("copilot", repo, opts); err != nil {
					t.Fatal(err)
				}
				plan, err := ApplyProjection("copilot", repo, apply)
				if err != nil {
					details, _ := PlanProjection("copilot", repo, apply)
					t.Fatalf("%v diagnostics: %+v", err, details.Diagnostics)
				}
				if !plan.Applicable {
					t.Fatal("MCP plan not applicable")
				}
				if _, err := PlanProjection("copilot", repo, apply); err != nil {
					t.Fatal(err)
				}
				if scope == "user" {
					opts.NativeHome = apply.NativeHome
				}
				if err := ImportRepositoryWithOptions("copilot", repo, opts); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestNativeCopilotMCPRefusesMalformedAndRequiredUnknown(t *testing.T) {
	for _, source := range []string{`{"mcpServers":[]}`, `{"mcpServers":{"fixture":false}}`, `{"mcpServers":{"fixture":{"type":"local","url":"https://fixture.invalid"}}}`, `{"mcpServers":{"fixture":{"type":"http","command":"server"}}}`, `{"mcpServers":{"fixture":{"command":"server","tools":false}}}`, `{"mcpServers":{"fixture":{"command":"server","env":{"API_KEY":"literal-secret"}}}}`, `{"mcpServers":{"fixture":{"type":"http","url":"https://user:secret@fixture.invalid/mcp"}}}`, `{"mcpServers":{"fixture":{"command":"server","futureFlag":true}}}`} {
		repo := nativeCopilotMCPFixture(t, "project", source, true)
		if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err == nil {
			t.Fatalf("invalid MCP source accepted: %s", source)
		} else if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("secret in diagnostic")
		}
	}
}
func TestNativeCopilotMCPKeepsNativeReferenceSemantics(t *testing.T) {
	values, _ := parseNative([]byte(`{"mcpServers":{"fixture":{"type":"local","command":"server","env":{"API_KEY":"${KEY}"}}}}`), "json")
	core, err := nativeExtractMCP("copilot", values)
	if err != nil {
		t.Fatal(err)
	}
	if len(core["fixture"].Env) != 0 {
		t.Fatal("native expansion was promoted to portable fail-on-missing semantics")
	}
	if !strings.Contains(string(mustNativeJSON(t, values)), "${KEY}") {
		t.Fatal("native environment reference was lost")
	}
}
func mustNativeJSON(t *testing.T, value map[string]any) []byte {
	t.Helper()
	data, err := nativeEncode(value, "json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestNativeCopilotSettingsDoNotActivateMCP(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"mcpServers":{"fixture":{"command":"server"}}}`), 0600)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents/tools/mcp.json")); !os.IsNotExist(err) {
		t.Fatal("unmapped settings root activated MCP")
	}
}

func TestNativeCopilotMCPProjectSourcePriority(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, ".github"), 0700)
	os.WriteFile(filepath.Join(base, ".github/mcp.json"), []byte(`{"mcpServers":{"same":{"command":"low"},"lower":{"command":"kept"}}}`), 0600)
	os.WriteFile(filepath.Join(base, ".mcp.json"), []byte(`{"same":{"command":"high"},"higher":{"command":"kept-too"}}`), 0600)
	data, err := nativeReadCopilotProjectMCP(base)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := parseNative(data, "json")
	servers := value["mcpServers"].(map[string]any)
	if len(servers) != 3 || servers["same"].(map[string]any)["command"] != "high" {
		t.Fatal("project priority changed")
	}
}
func TestNativeCopilotMCPOldTargetMigration(t *testing.T) {
	repo := nativeCopilotMCPFixture(t, "project", `{"mcpServers":{"fixture":{"type":"local","command":"server"}}}`, true)
	options := ApplyOptions{Experimental: true}
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(repo, ".mcp.json")
	old := filepath.Join(repo, ".github/mcp.json")
	os.MkdirAll(filepath.Dir(old), 0700)
	os.Rename(current, old)
	statePath, err := nativeStatePath("copilot", "project", filepath.Join(repo, ".agents"), "")
	if err != nil {
		t.Fatal(err)
	}
	var state nativeRegistry
	if err := decodePolicy(statePath, &state); err != nil {
		t.Fatal(err)
	}
	for key, owner := range state.Settings {
		var parts []string
		json.Unmarshal([]byte(key), &parts)
		if parts[0] == current {
			delete(state.Settings, key)
			state.Settings[nativeKey(old, parts[1])] = owner
		}
	}
	data, _ := json.Marshal(state)
	os.WriteFile(statePath, data, 0600)
	existing, _ := parseNative([]byte(readNativeTest(t, old)), "json")
	existing["mcpServers"].(map[string]any)["unowned"] = map[string]any{"command": "leave-me"}
	os.WriteFile(old, mustNativeJSON(t, existing), 0600)
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readNativeTest(t, current), "fixture") {
		t.Fatal("new MCP target missing")
	}
	contents := readNativeTest(t, old)
	if strings.Contains(contents, "fixture") || !strings.Contains(contents, "leave-me") {
		t.Fatal("migration damaged unowned settings")
	}
	if plan, err := PlanProjection("copilot", repo, options); err != nil || len(plan.Actions) != 0 {
		t.Fatalf("migration not idempotent: %v", err)
	}
}
func TestNativeCopilotMCPExpandedCommandStaysNative(t *testing.T) {
	values, _ := parseNative([]byte(`{"mcpServers":{"fixture":{"type":"local","command":"${SERVER}","args":["${ARGS}"]}}}`), "json")
	portable, err := nativeExtractCopilotMCP(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(portable) != 0 || !strings.Contains(string(mustNativeJSON(t, values)), "${SERVER}") {
		t.Fatal("native command expansion acquired portable semantics")
	}
}

func TestNativeCopilotMCPSharedHomeAndRemoval(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	a := nativeCopilotMCPFixture(t, "user", `{"mcpServers":{"first":{"type":"local","command":"one"}}}`, true)
	b := nativeCopilotMCPFixture(t, "user", `{"mcpServers":{"second":{"type":"local","command":"two"}}}`, true)
	opts := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}
	for _, repo := range []string{a, b} {
		if _, err := ApplyProjection("copilot", repo, opts); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(home, "mcp-config.json")
	before := readNativeTest(t, target)
	source := filepath.Join(b, ".agents/native/com.github.copilot/mcp.json")
	os.WriteFile(source, []byte(`{"mcpServers":{"first":{"type":"local","command":"stolen"}}}`), 0600)
	if _, err := ApplyProjection("copilot", b, opts); err == nil {
		t.Fatal("force replaced another repository's MCP server")
	}
	if readNativeTest(t, target) != before {
		t.Fatal("conflict wrote MCP config")
	}
	os.WriteFile(source, []byte(`{}`), 0600)
	if _, err := ApplyProjection("copilot", b, opts); err != nil {
		t.Fatal(err)
	}
	after := readNativeTest(t, target)
	if strings.Contains(after, "second") || !strings.Contains(after, "first") {
		t.Fatal("MCP removal changed another source")
	}
}

func TestNativeCopilotMCPUnknownOnlyStaysInactive(t *testing.T) {
	repo := nativeCopilotMCPFixture(t, "project", `{"mcpServers":{"fixture":{"futureOption":true}}}`, false)
	plan, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range plan.Native.Features {
		if feature.Feature == "mcp" && feature.Activation != "inactive" {
			t.Fatal("empty native mapping claims activation")
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatal("unknown-only MCP artifact wrote native config")
	}
}

func TestNativeCopilotMCPPortableReferencesStillRefuse(t *testing.T) {
	repo := nativeCopilotMCPFixture(t, "project", `{}`, false)
	root := filepath.Join(repo, ".agents")
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["native","tools"]}`), 0600)
	os.MkdirAll(filepath.Join(root, "tools"), 0700)
	data, _ := json.Marshal(mcpDocument{Servers: map[string]MCPServer{"fixture": {Type: "stdio", Command: "server", Env: map[string]string{"API_KEY": "urn:open-dot-agents:env:KEY"}}}})
	os.WriteFile(filepath.Join(root, "tools/mcp.json"), data, 0600)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err == nil || !strings.Contains(err.Error(), "ODA-ADAPTER-0001") {
		t.Fatalf("portable reference refusal changed: %v", err)
	}
}
