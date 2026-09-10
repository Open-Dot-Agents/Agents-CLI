package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copilotHooksFixture(t *testing.T, scope, body string, required bool) string {
	t.Helper()
	repo := nativeLSPFixture(t, scope, `{}`, required)
	base := filepath.Join(repo, ".agents/native/com.github.copilot")
	profile := nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: required, Artifacts: []nativeArtifact{{Kind: "hooks", Source: "hooks.json", Name: "fixture.json"}}}
	data, _ := json.Marshal(profile)
	os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(base, "hooks.json"), []byte(body), 0600)
	return repo
}
func selectHooksTest(t *testing.T, text string) (map[string]any, []nativeInactiveField, error) {
	t.Helper()
	values, err := parseNative([]byte(text), "json")
	if err != nil {
		t.Fatal(err)
	}
	return nativeSelectCopilotHooks(values, false)
}
func TestNativeCopilotHooksValidation(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"hooks":{}}`,
		`{"version":1,"hooks":{"sessionStart":[{"bash":"true","command":"false"}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"exec":"program","args":["$HOME *"],"env":{"API_KEY":"${KEY}"},"cwd":"work","timeout":2,"timeoutSec":1,"matcher":"bash"}]}}`,
		`{"version":1,"hooks":{"postToolUse":[{"type":"http","url":"http://127.0.0.1:1234","headers":{"X-Fixture":"value"}}]}}`,
		`{"version":1,"hooks":{"sessionStart":[{"type":"prompt","prompt":"fixture"}]}}`,
	} {
		if _, inactive, err := selectHooksTest(t, body); err != nil || len(inactive) > 0 {
			t.Fatalf("valid hooks refused: %v %v", err, inactive)
		}
	}
	for _, body := range []string{
		`{"version":2,"hooks":{}}`, `{"version":1,"hooks":[]}`, `{"version":1,"disableAllHooks":"true","hooks":{}}`,
		`{"version":1,"hooks":{"preToolUse":{}}}`, `{"version":1,"hooks":{"preToolUse":[false]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"exec":"program","command":"true"}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"command":"true","args":[]}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"exec":"program","args":[false]}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"exec":"program","env":{"API_KEY":"literal-secret"}}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"type":"http","url":"http://fixture.invalid"}]}}`,
		`{"version":1,"hooks":{"postToolUse":[{"type":"http","url":"https://user:literal-secret@fixture.invalid"}]}}`,
		`{"version":1,"hooks":{"postToolUse":[{"type":"http","url":"http://localhost","allowedEnvVars":[]}]}}`,
		`{"version":1,"hooks":{"postToolUse":[{"type":"http","url":"http://external.invalid"}]}}`,
		`{"version":1,"hooks":{"preToolUse":[{"type":"prompt","prompt":"fixture"}]}}`,
	} {
		if _, _, err := selectHooksTest(t, body); err == nil {
			t.Fatalf("invalid hook accepted: %s", body)
		} else if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("secret in diagnostic")
		}
	}
}
func TestNativeCopilotHooksUnknownAtomicEvent(t *testing.T) {
	body := `{"version":1,"future":true,"hooks":{"preToolUse":[{"command":"first"},{"command":"second","future":true}],"sessionStart":[{"command":"start"}],"futureEvent":[{"command":"unknown"}]}}`
	selected, inactive, err := selectHooksTest(t, body)
	if err != nil {
		t.Fatal(err)
	}
	hooks := selected["hooks"].(map[string]any)
	if len(hooks) != 1 || hooks["sessionStart"] == nil || len(inactive) != 4 {
		t.Fatalf("partial hook event activated: %v %v", selected, inactive)
	}
	for _, required := range []bool{false, true} {
		repo := copilotHooksFixture(t, "project", body, required)
		plan, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
		if required {
			if err == nil {
				t.Fatal("required unknown fields accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, feature := range plan.Native.Features {
			if feature.Disposition == "inactive" && (feature.Activation != "inactive" || feature.NativeStatus != "unverified" || len(feature.Evidence) != 0) {
				t.Fatal("inactive feature reports activation")
			}
		}
		target := readNativeTest(t, filepath.Join(repo, ".github/hooks/fixture.json"))
		if strings.Contains(target, "second") || strings.Contains(target, "first") || strings.Contains(target, "unknown") {
			t.Fatal("unknown hook event copied")
		}
	}
	for _, body := range []string{`{"hooks":{"sessionStart":[{"command":"true"}]}}`, `{"version":1,"hooks":{"sessionStart":[{"powershell":"true"}]}}`} {
		selected, inactive, err := selectHooksTest(t, body)
		if err != nil || len(selected) != 0 || len(inactive) == 0 {
			t.Fatalf("unmapped source activated: %v %v %v", selected, inactive, err)
		}
	}
}
func TestNativeCopilotHooksRoundTripAndRemoval(t *testing.T) {
	body := `{"version":1,"hooks":{"preToolUse":[{"exec":"program","args":["$HOME *"],"env":{"API_KEY":"${KEY}"},"timeout":2,"timeoutSec":1,"matcher":"bash"}]}}`
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			repo := copilotHooksFixture(t, scope, body, true)
			base := repo
			options := ApplyOptions{Experimental: true, Scope: scope}
			if scope == "user" {
				options.NativeHome = t.TempDir()
				base = options.NativeHome
			}
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			path, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "hooks", Name: "fixture.json"})
			original := readNativeTest(t, path)
			if plan, err := PlanProjection("copilot", repo, options); err != nil || len(plan.Actions) > 0 {
				t.Fatal("non-idempotent hook projection", err)
			}
			imported := repo
			if scope == "user" {
				imported = t.TempDir()
			}
			if err := ImportRepositoryWithOptions("copilot", imported, WriteOptions{Experimental: true, Scope: scope, NativeHome: options.NativeHome}); err != nil {
				t.Fatal(err)
			}
			if readNativeTest(t, filepath.Join(imported, ".agents/native/com.github.copilot/hooks/fixture.json")) != original {
				t.Fatal("hook import changed values")
			}
			os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("owned hook file not removed")
			}
		})
	}
}
func TestNativeCopilotHookImportCredentialRefusal(t *testing.T) {
	for _, location := range []string{"hooks/fixture.json", "settings.json"} {
		home := t.TempDir()
		path := filepath.Join(home, location)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(`{"version":1,"hooks":{"preToolUse":[{"command":"true","env":{"API_KEY":"literal-secret"}}]}}`), 0600)
		repo := t.TempDir()
		err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home})
		if err == nil {
			t.Fatal("credential hook imported")
		}
		if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("credential in diagnostic")
		}
		if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
			t.Fatal("refused import wrote canonical files")
		}
	}
}

func TestNativeCopilotHookControlCannotAffectUnownedEvents(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, remove := range []bool{false, true} {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			home := t.TempDir()
			source := map[string]any{"version": 1, "disableAllHooks": initial, "hooks": map[string]any{"sessionStart": []any{map[string]any{"command": "true"}}}}
			data, _ := json.Marshal(source)
			repo := copilotHooksFixture(t, "user", string(data), true)
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, "hooks/fixture.json")
			current, _ := parseNative([]byte(readNativeTest(t, target)), "json")
			current["hooks"].(map[string]any)["postToolUse"] = []any{map[string]any{"command": "unowned"}}
			data, _ = nativeEncode(current, "json")
			os.WriteFile(target, data, 0600)
			if remove {
				os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
			} else {
				source["disableAllHooks"] = !initial
				body, _ := json.Marshal(source)
				os.WriteFile(filepath.Join(repo, ".agents/native/com.github.copilot/hooks.json"), body, 0600)
			}
			if _, err := ApplyProjection("copilot", repo, options); err == nil {
				t.Fatal("hook control or version removal affected unowned event")
			}
			if readNativeTest(t, target) != string(data) {
				t.Fatal("refusal changed shared hook file")
			}
		}
	}
}

func TestNativeCopilotHooksPortableMerge(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		body := `{"version":1,"hooks":{"sessionStart":[{"exec":"program"}]}}`
		if disabled {
			body = `{"version":1,"disableAllHooks":true,"hooks":{"sessionStart":[{"exec":"program"}]}}`
		}
		repo := copilotHooksFixture(t, "project", body, true)
		base := filepath.Join(repo, ".agents/native/com.github.copilot")
		var profile nativeProfile
		decodePolicy(filepath.Join(base, "profile.json"), &profile)
		profile.Artifacts[0].Name = "open-dot-agents.json"
		data, _ := json.Marshal(profile)
		os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
		os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["native","hooks"]}`), 0600)
		os.MkdirAll(filepath.Join(repo, ".agents/hooks"), 0700)
		os.WriteFile(filepath.Join(repo, ".agents/hooks/hooks.json"), []byte(`{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"portable"}]}]}}`), 0600)
		_, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
		if disabled {
			if err == nil {
				t.Fatal("native flag disabled selected portable hooks")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		target := readNativeTest(t, filepath.Join(repo, ".github/hooks/open-dot-agents.json"))
		if !strings.Contains(target, "program") || !strings.Contains(target, "portable") {
			t.Fatal("portable/native merge lost an event")
		}
	}
}

func TestNativeCopilotInlineHooks(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		body := `{"hooks":{"preToolUse":[{"exec":"program","args":[],"env":{"API_KEY":"${KEY}"}}]},"beep":false}`
		repo := nativeLSPFixture(t, scope, `{}`, true)
		base := filepath.Join(repo, ".agents/native/com.github.copilot")
		profile := nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: true, Artifacts: []nativeArtifact{{Kind: "config", Source: "settings.json"}}}
		// The beep setting is a user-only field.
		if scope == "project" {
			body = `{"hooks":{"preToolUse":[{"exec":"program","args":[],"env":{"API_KEY":"${KEY}"}}]}}`
		}
		data, _ := json.Marshal(profile)
		os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
		os.WriteFile(filepath.Join(base, "settings.json"), []byte(body), 0600)
		options := ApplyOptions{Experimental: true, Scope: scope}
		targetBase := repo
		if scope == "user" {
			options.NativeHome = t.TempDir()
			targetBase = options.NativeHome
		}
		if _, err := ApplyProjection("copilot", repo, options); err != nil {
			t.Fatal(err)
		}
		target, _, _ := nativeTargetPath("copilot", scope, targetBase, nativeArtifact{Kind: "config"})
		actual, _ := parseNative([]byte(readNativeTest(t, target)), "json")
		expected, _ := parseNative([]byte(body), "json")
		if nativeHash(actual) != nativeHash(expected) {
			t.Fatal("inline hook values changed")
		}
		if plan, err := PlanProjection("copilot", repo, options); err != nil || len(plan.Actions) > 0 {
			t.Fatal("inline hooks not idempotent", err)
		}
	}
}

func TestNativeCopilotHookControlRejectsForeignAndGlobalAuthority(t *testing.T) {
	target := &nativeTarget{settings: map[string]any{"/disableAllHooks": true}, sources: map[string]string{"/disableAllHooks": "source"}}
	state := nativeRegistry{Settings: map[string]nativeOwned{nativeKey("target", "/hooks/preToolUse"): {Source: "other"}}}
	values := map[string]any{"/hooks/preToolUse": []any{map[string]any{"command": "other"}}}
	if err := nativeCopilotHookControlOwnership("target", "source", target, values, state, true); err == nil {
		t.Fatal("foreign event disabled")
	}
	if err := nativeCopilotHookControlOwnership("target", "source", target, map[string]any{}, state, false); err == nil {
		t.Fatal("global hook switch accepted")
	}
}
