package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeFixture(t *testing.T, scope, config string) string {
	t.Helper()
	repo := t.TempDir()
	root := filepath.Join(repo, ".agents")
	dir := filepath.Join(root, "native", "com.openai.codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(root, "AGENTS.md"): "Test instructions.\n", filepath.Join(root, "manifest.json"): `{"version":"1.1.0-draft.2","profiles":["native"]}`, filepath.Join(dir, "profile.json"): `{"namespace":"com.openai.codex","harness_version":"=0.154.0","scope":"` + scope + `","required":true,"artifacts":[{"kind":"config","source":"config.toml"}]}`, filepath.Join(dir, "config.toml"): config} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}
func readNativeTest(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestNativeProjectIsolationAndIdempotence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	os.WriteFile(configPath, []byte("model = \"user-model\"\n"), 0640)
	repo := nativeFixture(t, "project", "model_reasoning_effort = \"high\"\n")
	if _, e := PlanProjection("codex", repo, ApplyOptions{}); e == nil {
		t.Fatal("draft accepted without opt-in")
	}
	options := ApplyOptions{Experimental: true}
	if _, e := ApplyProjection("codex", repo, options); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(readNativeTest(t, filepath.Join(repo, ".codex", "config.toml")), "high") {
		t.Fatal("missing native value")
	}
	if got := readNativeTest(t, configPath); got != "model = \"user-model\"\n" {
		t.Fatal("user config changed")
	}
	plan, e := PlanProjection("codex", repo, options)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("not idempotent: %+v", plan)
	}
}
func TestNativeSharedHomeOwnershipAndRemoval(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	a := nativeFixture(t, "user", "model = \"model-a\"\n")
	b := nativeFixture(t, "user", "model_reasoning_effort = \"high\"\n")
	opt := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}
	for _, repo := range []string{a, b} {
		if _, e := ApplyProjection("codex", repo, opt); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(home, "config.toml")
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("new config is not private")
	}
	source := filepath.Join(b, ".agents/native/com.openai.codex/config.toml")
	os.WriteFile(source, []byte("model = \"other\"\n"), 0600)
	before := readNativeTest(t, path)
	if _, e := ApplyProjection("codex", b, opt); e == nil {
		t.Fatal("force stole ownership")
	}
	if readNativeTest(t, path) != before {
		t.Fatal("conflict wrote config")
	}
	os.WriteFile(source, []byte(""), 0600)
	if _, e := ApplyProjection("codex", b, opt); e != nil {
		t.Fatal(e)
	}
	got := readNativeTest(t, path)
	if !strings.Contains(got, "model-a") || strings.Contains(got, "high") {
		t.Fatal(got)
	}
}
func TestNativeUnknownAndExcluded(t *testing.T) {
	for index, value := range []string{"new_setting = true", "approval_policy = \"never\"", "api_key = \"test-secret\"", "model = {unknown = true}"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			repo := nativeFixture(t, "project", value)
			opt := ApplyOptions{Experimental: true}
			if _, e := ApplyProjection("codex", repo, opt); e == nil {
				t.Fatal("required unmapped content activated")
			}
			path := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
			p := readNativeTest(t, path)
			os.WriteFile(path, []byte(strings.Replace(p, `"required":true`, `"required":false`, 1)), 0600)
			plan, e := ApplyProjection("codex", repo, opt)
			if e != nil {
				t.Fatal(e)
			}
			data, _ := json.Marshal(plan)
			if strings.Contains(string(data), "test-secret") {
				t.Fatal("credential leaked")
			}
			if _, e = os.Stat(filepath.Join(repo, ".codex/config.toml")); !os.IsNotExist(e) {
				t.Fatal("inactive configuration was written")
			}
		})
	}
}
func TestNativeTransactionRollback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	os.WriteFile(path, []byte("original"), 0640)
	bad := filepath.Join(dir, "directory")
	os.Mkdir(bad, 0700)
	err := nativeTransaction([]nativeChange{{path: path + ".bak", data: []byte("original"), mode: 0600}, {path: path, data: []byte("changed"), mode: 0600}, {path: bad, data: []byte("failure"), mode: 0600}})
	if err == nil {
		t.Fatal("expected failure")
	}
	if readNativeTest(t, path) != "original" {
		t.Fatal("config rollback failed")
	}
	if _, err = os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("backup rollback failed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("mode rollback failed")
	}
}
func TestNativeScopeAndSymlink(t *testing.T) {
	repo := nativeFixture(t, "user", "model = \"a\"")
	for _, o := range []ApplyOptions{{Experimental: true, Scope: "user"}, {Experimental: true, Scope: "user", NativeHome: "relative"}, {Experimental: true, Scope: "wrong"}, {Experimental: true, NativeHome: t.TempDir()}} {
		if _, e := ApplyProjection("codex", repo, o); e == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	home := t.TempDir()
	target := t.TempDir()
	os.Symlink(filepath.Join(target, "target"), filepath.Join(home, "config.toml"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestNativeImportRoundTripAndPolicyPreservation(t *testing.T) {
	sourceHome := t.TempDir()
	os.WriteFile(filepath.Join(sourceHome, "config.toml"), []byte("# native comment\nmodel = \"a\"\n"), 0600)
	repo := t.TempDir()
	opt := WriteOptions{Experimental: true, Scope: "user", NativeHome: sourceHome}
	if e := ImportRepositoryWithOptions("codex", repo, opt); e != nil {
		t.Fatal(e)
	}
	if e := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); e != nil {
		t.Fatal(e)
	}
	target := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); e != nil {
		t.Fatal(e)
	}
	again := t.TempDir()
	opt.NativeHome = target
	if e := ImportRepositoryWithOptions("codex", again, opt); e != nil {
		t.Fatal(e)
	}
	a, _ := parseNative([]byte(readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"))), "toml")
	b, _ := parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents/native/com.openai.codex/config.toml"))), "toml")
	if nativeHash(a) != nativeHash(b) {
		t.Fatal("round trip changed native values")
	}
	opt.Force = true
	if e := ImportRepositoryWithOptions("codex", repo, opt); e != nil {
		t.Fatal("equal import must be idempotent:", e)
	}
	os.WriteFile(filepath.Join(target, "config.toml"), []byte("model = 'conflicting'\n"), 0600)
	if e := ImportRepositoryWithOptions("codex", repo, opt); e == nil {
		t.Fatal("import replaced conflicting canonical configuration")
	}
}
func TestNativeJSONC(t *testing.T) {
	data := []byte("{ // a comment\n \"url\":\"https://example.org/a,}\", /* block */ \"enabled\":true, }")
	v, e := parseNative(data, "json")
	if e != nil {
		t.Fatal(e)
	}
	if v["url"] != "https://example.org/a,}" {
		t.Fatal(v)
	}
	if _, e = parseNative([]byte("{/*"), "json"); e == nil {
		t.Fatal("bad JSONC accepted")
	}
}

func TestNativeNestedOwnershipAndUnknownChild(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	a := nativeFixture(t, "user", "[model_providers.local]\nname = \"Local provider\"\n")
	b := nativeFixture(t, "user", "[model_providers.local]\nbase_url = \"http://localhost:1234\"\n")
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	for _, repo := range []string{a, b} {
		if _, e := ApplyProjection("codex", repo, o); e != nil {
			t.Fatal(e)
		}
	}
	plan, e := PlanProjection("codex", a, o)
	if e != nil || len(plan.Actions) != 0 {
		t.Fatalf("nested setting is not idempotent: %+v %v", plan, e)
	}
	source := filepath.Join(a, ".agents/native/com.openai.codex/config.toml")
	os.WriteFile(source, []byte("[model_providers.local]\nunknown_new_field = true\n"), 0600)
	if _, e := ApplyProjection("codex", a, o); e == nil {
		t.Fatal("unknown nested field activated")
	}
}
func TestNativeParentConflict(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := nativeFixture(t, "user", "[model_providers.local]\nname = \"Local\"\n")
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	os.WriteFile(path, []byte("model_providers = \"unowned\"\n"), 0600)
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}); e == nil {
		t.Fatal("parent setting overwritten")
	}
	if readNativeTest(t, path) != "model_providers = \"unowned\"\n" {
		t.Fatal("parent conflict wrote file")
	}
}
func TestNativePortableMCPMerge(t *testing.T) {
	repo := nativeFixture(t, "project", "model = \"native-model\"\n")
	root := filepath.Join(repo, ".agents")
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["native","tools"]}`), 0600)
	os.Mkdir(filepath.Join(root, "tools"), 0700)
	os.WriteFile(filepath.Join(root, "tools/mcp.json"), []byte(`{"mcpServers":{"fixture":{"type":"stdio","command":"fixture-server"}}}`), 0600)
	o := ApplyOptions{Experimental: true}
	if _, e := ApplyProjection("codex", repo, o); e != nil {
		t.Fatal(e)
	}
	got := readNativeTest(t, filepath.Join(repo, ".codex/config.toml"))
	if !strings.Contains(got, "native-model") || !strings.Contains(got, "fixture-server") {
		t.Fatal(got)
	}
	p, e := PlanProjection("codex", repo, o)
	if e != nil || len(p.Actions) != 0 {
		t.Fatalf("MCP merge not idempotent: %+v %v", p, e)
	}
}
func TestNativeDuplicateJSONAndInvalidTypes(t *testing.T) {
	for _, data := range []string{`{"model":"a","model":"b"}`, `{"nested":{"key":1,"key":2}}`} {
		if _, e := parseNative([]byte(data), "json"); e == nil {
			t.Fatal("duplicate JSON accepted")
		}
	}
	repo := nativeFixture(t, "project", "model = true\n")
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); e == nil {
		t.Fatal("invalid native value type activated")
	}
}

func TestNativeImportExcludesAuthorityAndCredentialStores(t *testing.T) {
	home := t.TempDir()
	original := "model = \"a\"\napi_key = \"test-secret-value\"\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n"
	os.WriteFile(filepath.Join(home, "config.toml"), []byte(original), 0600)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte("private-account-data"), 0600)
	repo := t.TempDir()
	if e := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(repo, ".agents/native/com.openai.codex/config.toml")
	data := readNativeTest(t, path)
	if strings.Contains(data, "test-secret-value") || strings.Contains(data, "trust_level") {
		t.Fatal("external data imported")
	}
	if readNativeTest(t, filepath.Join(home, "config.toml")) != original {
		t.Fatal("source home modified")
	}
	report := readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/import-report.json"))
	if strings.Contains(report, "test-secret-value") {
		t.Fatal("secret leaked in report")
	}
	if _, e := os.Stat(filepath.Join(repo, ".agents/native/com.openai.codex/auth.json")); !os.IsNotExist(e) {
		t.Fatal("account store copied")
	}
}

func TestNativeConfigFamiliesRoundTrip(t *testing.T) {
	families := map[string]string{
		"models":     "model = \"example-model\"\nmodel_reasoning_effort = \"high\"\nmodel_context_window = 32000\n",
		"delegation": "[agents]\nenabled = true\nmax_threads = 2\n",
		"context":    "compact_prompt = \"Compact the context.\"\nmodel_auto_compact_token_limit = 24000\n",
		"memory":     "[memories]\nuse_memories = false\ngenerate_memories = false\n",
		"session":    "[history]\npersistence = \"none\"\nmax_bytes = 4096\n",
		"plugins":    "[plugins.example]\nenabled = false\n",
		"ui":         "[tui]\nanimations = false\nnotification_method = \"bel\"\n",
		"telemetry":  "[otel]\nexporter = \"none\"\nlog_user_prompt = false\n",
	}
	for family, data := range families {
		t.Run(family, func(t *testing.T) {
			repo := nativeFixture(t, "user", data)
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
			if _, e := ApplyProjection("codex", repo, o); e != nil {
				t.Fatal(e)
			}
			again := t.TempDir()
			if e := ImportRepositoryWithOptions("codex", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); e != nil {
				t.Fatal(e)
			}
			original, e := parseNative([]byte(data), "toml")
			if e != nil {
				t.Fatal(e)
			}
			directory := "native"
			if family == "plugins" {
				directory = "plugins"
			}
			imported, e := parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents", directory, "com.openai.codex/config.toml"))), "toml")
			if e != nil {
				t.Fatal(e)
			}
			if nativeHash(original) != nativeHash(imported) {
				t.Fatalf("%s changed in round trip", family)
			}
		})
	}
}
func TestNativeCopilotJSONCRoundTrip(t *testing.T) {
	home := t.TempDir()
	input := "{ // user settings\n \"model\":\"gpt-5.5\", \"autoUpdate\":false, }\n"
	os.WriteFile(filepath.Join(home, "settings.json"), []byte(input), 0600)
	repo := t.TempDir()
	if e := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); e != nil {
		t.Fatal(e)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	target := t.TempDir()
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}
	if _, e := ApplyProjection("copilot", repo, o); e != nil {
		t.Fatal(e)
	}
	values, e := parseNative([]byte(readNativeTest(t, filepath.Join(target, "settings.json"))), "json")
	if e != nil {
		t.Fatal(e)
	}
	if values["model"] != "gpt-5.5" || values["autoUpdate"] != false {
		t.Fatal(values)
	}
	p, e := PlanProjection("copilot", repo, o)
	if e != nil || len(p.Actions) > 0 {
		t.Fatalf("Copilot is not idempotent: %+v %v", p, e)
	}
}

func TestNativeDraftDoesNotChangeDraftOnePolicyVersion(t *testing.T) {
	repo := nativeFixture(t, "project", "model = \"a\"\n")
	root := filepath.Join(repo, ".agents")
	os.Mkdir(filepath.Join(root, "permissions"), 0700)
	doc := `{"version":"1.1.0-draft.2","coverage":["shell"],"default":"deny","rules":[]}`
	os.WriteFile(filepath.Join(root, "permissions/permissions.json"), []byte(doc), 0600)
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":"1.1.0-draft.1","profiles":["permissions"]}`), 0600)
	if e := ValidateRepositoryWithOptions(root, true); e == nil {
		t.Fatal("draft.1 accepted a draft.2 policy")
	}
	os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["permissions","native"]}`), 0600)
	if e := ValidateRepositoryWithOptions(root, true); e != nil {
		t.Fatal(e)
	}
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); e == nil {
		t.Fatal("combined security activated without evidence")
	}
}
