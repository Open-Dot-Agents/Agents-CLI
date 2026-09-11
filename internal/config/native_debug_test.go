package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeCodexPinnedSchema(t *testing.T) {
	for _, test := range []struct {
		name, data string
		valid      bool
	}{
		{"alias", "[agents]\nmax_threads = 2\n", true},
		{"memory-alias", "[memories]\nno_memories_if_mcp_or_web_search = true\n", true},
		{"duplicate-alias", "[agents]\nmax_threads = 2\nmax_concurrent_threads_per_session = 2\n", false},
		{"fraction", "[agents]\nmax_threads = 1.5\n", false},
		{"positive-bound", "[agents]\nmax_threads = 0\n", false},
		{"signed-bound", "[agents]\nmax_depth = 2147483648\n", false},
		{"enum", "model_verbosity = 'unknown'\n", false},
		{"reasoning-name", "model_reasoning_effort = 'vendor-level'\n", true},
		{"empty-reasoning", "model_reasoning_effort = ''\n", false},
		{"union-bool", "[tui]\nnotifications = false\n", true},
		{"union-array", "[tui]\nnotifications = ['agent-turn-complete']\n", true},
		{"invalid-union", "[tui]\nnotifications = [42]\n", false},
		{"structured-array", "[[skills.config]]\nname = 'fixture'\nenabled = false\n", true},
		{"array-required-field", "[[skills.config]]\nname = 'fixture'\n", false},
		{"unknown-nested", "[tui]\nunknown = true\n", false},
		{"removed-noop", "[agents]\njob_max_runtime_seconds = 30\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, err := parseNative([]byte(test.data), "toml")
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range values {
				if got := nativeCodexValue(key, value); got != test.valid {
					t.Fatalf("schema valid=%v, want %v", got, test.valid)
				}
			}
		})
	}
}

func TestNativeAliasConflictWithExistingTarget(t *testing.T) {
	repo := nativeFixture(t, "user", "[agents]\nmax_threads = 2\n")
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(home, "config.toml")
	original := "[agents]\nmax_concurrent_threads_per_session = 2\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}); err == nil {
		t.Fatal("duplicate native aliases activated across source and target")
	}
	if readNativeTest(t, path) != original {
		t.Fatal("failed projection changed the target")
	}
}

func TestNativeExactNumbersThroughApply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(home, "settings.json")
	if err := os.WriteFile(path, []byte(`{"unowned":9007199254740993,"huge":1e999,"model":"a"}`), 0600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Adopt: true}
	if _, err := ApplyProjection("copilot", repo, o); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, ".agents/native/com.github.copilot/settings.json")
	if err := os.WriteFile(source, []byte(`{"model":"b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("copilot", repo, o); err != nil {
		t.Fatal(err)
	}
	got := readNativeTest(t, path)
	for _, exact := range []string{"9007199254740993", "1e999", `"b"`} {
		if !strings.Contains(got, exact) {
			t.Fatalf("missing exact value %s", exact)
		}
	}
}

func TestNativeNumericHashCompatibility(t *testing.T) {
	for _, pair := range [][2]any{{json.Number("1.00"), int64(1)}, {json.Number("-0"), json.Number("0")}, {json.Number("9.007199254740993e15"), json.Number("9007199254740993")}} {
		if nativeHash(pair[0]) != nativeHash(pair[1]) {
			t.Fatal("equivalent exact numbers have different hashes")
		}
	}
	if nativeHash(json.Number("9007199254740993")) == nativeHash(json.Number("9007199254740992")) {
		t.Fatal("distinct large integers have the same hash")
	}
	legacy, _ := json.Marshal(int64(42))
	if !nativeHashMatches(int64(42), fmt.Sprintf("%x", sha256.Sum256(legacy))) || nativeHashMatches(int64(43), fmt.Sprintf("%x", sha256.Sum256(legacy))) {
		t.Fatal("legacy ownership hash compatibility failed")
	}
}

func TestNativeBackupPlanMatchesApply(t *testing.T) {
	repo := nativeFixture(t, "user", "model = 'a'\n")
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	if _, err := ApplyProjection("codex", repo, o); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"), []byte("model = 'b'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	o.Backup = true
	plan, err := PlanProjection("codex", repo, o)
	if err != nil {
		t.Fatal(err)
	}
	backupCount := 0
	for _, action := range plan.Actions {
		if action.Detail == "backup" {
			backupCount++
			if _, err := os.Stat(action.Path); !os.IsNotExist(err) {
				t.Fatal("plan wrote a backup")
			}
		}
	}
	if backupCount != 2 {
		t.Fatalf("plan has %d backups, want config and ownership", backupCount)
	}
	applied, err := ApplyProjection("codex", repo, o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Actions, applied.Actions) {
		t.Fatal("apply used different actions from the plan")
	}
	for _, action := range plan.Actions {
		if action.Detail == "backup" {
			info, err := os.Stat(action.Path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("backup is not private")
			}
		}
	}
}

func TestNativeDuplicateTransactionRefusedBeforeWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	err := nativeTransaction([]nativeChange{{path: path, data: []byte("a"), mode: 0600}, {path: path, data: []byte("b"), mode: 0600}})
	if err == nil {
		t.Fatal("duplicate transaction accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("duplicate transaction wrote a target")
	}
}

func TestNativePolicyChecksUseFieldContext(t *testing.T) {
	for name, data := range map[string]string{
		"role-name":           "[agents.approval-reviewer]\ndescription = 'Review the change.'\n",
		"provider-name":       "[model_providers.auth-service]\nname = 'Fixture'\nbase_url = 'http://localhost:8888'\n",
		"header-reference":    "[model_providers.fixture]\nname = 'Fixture'\n[model_providers.fixture.env_http_headers]\nAuthorization = 'FIXTURE_TOKEN'\n",
		"approval-keybinding": "[tui.keymap.approval]\napprove = 'ctrl+y'\n",
	} {
		t.Run(name, func(t *testing.T) {
			scope := "project"
			options := ApplyOptions{Experimental: true}
			if strings.HasPrefix(data, "[model_providers.") {
				scope, options.Scope, options.NativeHome = "user", "user", t.TempDir()
				t.Setenv("XDG_STATE_HOME", t.TempDir())
			}
			repo := nativeFixture(t, scope, data)
			if _, err := ApplyProjection("codex", repo, options); err != nil {
				t.Fatal(err)
			}
			values, err := parseNative([]byte(data), "toml")
			if err != nil {
				t.Fatal(err)
			}
			filtered, excluded := nativeImportFilter("codex", values, nil)
			if len(excluded) != 0 || nativeHash(filtered) != nativeHash(values) {
				t.Fatal("configuration or reference was excluded as authority")
			}
		})
	}
	if disposition, _ := nativeSettingDisposition("copilot", "user", "includeCoAuthoredBy", true); disposition != "configuration" {
		t.Fatal("co-author preference was treated as account state")
	}
	values, err := parseNative([]byte("[model_providers.fixture.http_headers]\nAuthorization = 'fixture-secret'\n"), "toml")
	if err != nil {
		t.Fatal(err)
	}
	filtered, excluded := nativeImportFilter("codex", values, nil)
	encoded, _ := nativeEncode(filtered, "toml")
	if len(excluded) != 1 || strings.Contains(string(encoded), "fixture-secret") {
		t.Fatal("literal authorization value was imported")
	}
}

func TestNativeSkillExecutableRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for path, data := range map[string]string{
		"skills/fixture/SKILL.md":         "---\nname: fixture\ndescription: Fixture skill.\n---\nRun the fixture.\n",
		"skills/fixture/scripts/check.sh": "#!/bin/sh\nexit 0\n",
		"skills/.system/builtin/SKILL.md": "System package.\n",
	} {
		full := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	repo := t.TempDir()
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents/skills/.system")); !os.IsNotExist(err) {
		t.Fatal("system skills imported")
	}
	projected := t.TempDir()
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: projected}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projected, "skills/fixture/scripts/check.sh")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("executable skill asset lost its private executable mode")
	}
	if got := readNativeTest(t, path); got != "#!/bin/sh\nexit 0\n" {
		t.Fatal("executable asset content changed")
	}
}

func TestNativeImportMergesWithoutChangingPortablePolicy(t *testing.T) {
	repo := securityFixture(t)
	for _, relative := range []string{"manifest.json", "permissions/permissions.json", "sandbox/sandbox.json"} {
		path := filepath.Join(repo, ".agents", relative)
		data := readNativeTest(t, path)
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(data, ExperimentalVersion, NativeVersion)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policyPath := filepath.Join(repo, ".agents/permissions/permissions.json")
	policyBefore := readNativeTest(t, policyPath)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = 'fixture'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, policyPath) != policyBefore || readNativeTest(t, filepath.Join(repo, ".agents/AGENTS.md")) != "Use the policy.\n" {
		t.Fatal("import changed existing portable policy or instructions")
	}
	if err := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: t.TempDir()}); err == nil {
		t.Fatal("imported native config weakened the selected portable policy")
	}
}

func TestNativeImportConflictLeavesCanonicalTreeUnchanged(t *testing.T) {
	repo := nativeFixture(t, "user", "model = 'a'\n")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = 'b'\n[agents]\nenabled = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := portabilitySnapshot(t, filepath.Join(repo, ".agents"))
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}); err == nil {
		t.Fatal("conflicting import succeeded")
	}
	if !reflect.DeepEqual(before, portabilitySnapshot(t, filepath.Join(repo, ".agents"))) {
		t.Fatal("refused import changed the canonical tree")
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[agents]\nenabled = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	data := readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"))
	values, err := parseNative([]byte(data), "toml")
	if err != nil || values["model"] != "a" || values["agents"].(map[string]any)["enabled"] != false {
		t.Fatal("disjoint import did not preserve both sources")
	}
	var profile nativeProfile
	if err := decodePolicy(filepath.Join(repo, ".agents/native/com.openai.codex/profile.json"), &profile); err != nil || !profile.Required {
		t.Fatal("import relaxed required native content")
	}
}

func TestNativeJSONPreservesLargeUnknownNumber(t *testing.T) {
	values, e := parseNative([]byte(`{"unowned":9007199254740993,"model":"a"}`), "json")
	if e != nil {
		t.Fatal(e)
	}
	values["model"] = "b"
	data, e := nativeEncode(values, "json")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("unowned integer changed: %s", data)
	}
}
func TestNativeQuotedKeyIsNotNestedSetting(t *testing.T) {
	repo := nativeFixture(t, "project", `"agents.enabled" = true`)
	if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); e == nil {
		t.Fatal("unmapped literal key activated as nested field")
	}
}
func TestNativePermissionControlsRemainRefused(t *testing.T) {
	for name, data := range map[string]string{
		"default-profile": `default_permissions = "full-access"`,
		"app-approval":    "[apps._default]\ndefault_tools_approval_mode = \"approve\"\n",
		"mcp-approval":    "[mcp_servers.fixture]\ndefault_tools_approval_mode = \"approve\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := nativeFixture(t, "project", data)
			if _, e := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); e == nil {
				t.Fatal("unverified native permission control activated")
			}
		})
	}
}
func TestNativeIntegerTypeRejectsFraction(t *testing.T) {
	if nativeValueType("integer", 1.5) {
		t.Fatal("integer accepts fraction")
	}
}
func TestNativeSkillImportDoesNotSilentlyDropAssets(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"a\"\n"), 0600)
	dir := filepath.Join(home, "skills", "fixture")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: fixture\ndescription: Fixture skill.\n---\nUse the fixture.\n"), 0600)
	repo := t.TempDir()
	if e := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(repo, ".agents/skills/fixture/SKILL.md")); e != nil {
		t.Fatalf("recognized skill was not imported: %v", e)
	}
}
func TestNativeRegistryCannotOwnWholeConfigAsAsset(t *testing.T) {
	repo := nativeFixture(t, "user", "model = \"a\"\n")
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	b, e := buildNativeProjection("codex", repo, o)
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Dir(b.registryPath), 0700)
	config := filepath.Join(home, "config.toml")
	data := []byte("model = \"existing\"\n")
	os.WriteFile(config, data, 0600)
	s := nativeRegistry{Version: NativeVersion, Vendor: "codex", Home: home, Settings: map[string]nativeOwned{nativeKey(config, ""): {Source: filepath.Join(repo, ".agents"), Hash: nativeHash(data)}}}
	encoded, _ := json.Marshal(s)
	os.WriteFile(b.registryPath, encoded, 0600)
	os.WriteFile(filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"), nil, 0600)
	if _, e = ApplyProjection("codex", repo, o); e == nil {
		t.Fatal("invalid whole-file config ownership was used for deletion")
	}
	if _, e = os.Stat(config); e != nil {
		t.Fatal("config was deleted")
	}
}

func TestNativeMalformedForeignOwnershipRefused(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	owner := nativeOwned{Source: filepath.Join(t.TempDir(), ".agents"), Hash: nativeHash("fixture")}
	for name, fields := range map[string]map[string]nativeOwned{
		"invalid-pointer":          {nativeKey(path, "model"): owner},
		"bad-escape":               {nativeKey(path, "/model~3"): owner},
		"nested-credential":        {nativeKey(path, "/model_providers/fixture/http_headers/Authorization"): owner},
		"parent-child":             {nativeKey(path, "/agents"): owner, nativeKey(path, "/agents/enabled"): owner},
		"foreign-whole-config":     {nativeKey(path, ""): owner},
		"foreign-outside-registry": {nativeKey(filepath.Join(home, "auth.json"), ""): owner},
	} {
		t.Run(name, func(t *testing.T) {
			state := nativeRegistry{Version: NativeVersion, Vendor: "codex", Home: home, Settings: fields}
			if err := nativeValidateRegistry("codex", "user", home, state); err == nil {
				t.Fatal("invalid foreign ownership accepted")
			}
		})
	}
}
