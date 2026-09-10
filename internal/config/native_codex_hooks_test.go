package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexHooksFixture(t *testing.T, scope, body string, required bool) string {
	t.Helper()
	repo := nativeFixture(t, scope, "")
	base := filepath.Join(repo, ".agents/native/com.openai.codex")
	p := nativeProfile{Namespace: "com.openai.codex", HarnessVersion: "=0.154.0", Scope: scope, Required: required, Artifacts: []nativeArtifact{{Kind: "hooks", Source: "hooks.json"}}}
	data, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(base, "hooks.json"), []byte(body), 0600)
	return repo
}
func selectCodexHooksTest(t *testing.T, body string, inline bool) (map[string]any, []nativeInactiveField, error) {
	t.Helper()
	values, err := parseNative([]byte(body), "json")
	if err != nil {
		t.Fatal(err)
	}
	return nativeSelectCodexHooks(values, inline)
}
func TestNativeCodexHookSchemaAndFields(t *testing.T) {
	for _, body := range []string{
		`{"hooks":{}}`,
		`{"description":"metadata","hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"true","timeout":18446744073709551615,"additionalContextLimit":0,"statusMessage":"status","async":false,"commandWindows":"Write-Output fixture"}]}]}}`,
		`{"hooks":{"PreToolUse":[{"hooks":[{"type":"mcp_tool","server":"fixture","tool":"record","input":{"marker":"fixture"},"timeout":5}]}]}}`,
	} {
		if _, inactive, err := selectCodexHooksTest(t, body, false); err != nil || len(inactive) > 0 {
			t.Fatalf("valid native fields refused: %v %v", err, inactive)
		}
	}
	for _, body := range []string{
		`{"hooks":[]}`, `{"hooks":{"SessionStart":false}}`, `{"hooks":{"SessionStart":[false]}}`,
		`{"hooks":{"SessionStart":[{"matcher":false}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","timeout":-1}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","timeout":0.5}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","timeout":18446744073709551616}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","additionalContextLimit":-1}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","async":"yes"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"mcp_tool","server":"fixture"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"mcp_tool","server":"fixture","tool":"record","input":{"apiKey":"literal-secret"}}]}]}}`,
	} {
		if _, _, err := selectCodexHooksTest(t, body, false); err == nil {
			t.Fatalf("invalid hook accepted: %s", body)
		} else if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("secret in diagnostic")
		}
	}
}
func TestNativeCodexUnknownHookEventsStayAtomic(t *testing.T) {
	for _, kind := range []string{"prompt", "agent", "future"} {
		body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"first"},{"type":"` + kind + `"}]}],"SessionStart":[{"hooks":[{"type":"command","command":"start"}]}]}}`
		selected, inactive, err := selectCodexHooksTest(t, body, false)
		if err != nil || len(inactive) == 0 {
			t.Fatal(err, inactive)
		}
		events := selected["hooks"].(map[string]any)
		if len(events) != 1 || events["SessionStart"] == nil {
			t.Fatal("part of an inactive event activated")
		}
		for _, required := range []bool{false, true} {
			repo := codexHooksFixture(t, "project", body, required)
			plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
			if required {
				if err == nil {
					t.Fatal("required skipped handler activated")
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readNativeTest(t, filepath.Join(repo, ".codex/hooks.json")), "first") {
				t.Fatal("inactive event was copied")
			}
			if !strings.Contains(strings.Join(plan.Native.RequiredActions, " "), "trust") {
				t.Fatal("native hook trust action missing")
			}
		}
	}
	for _, extra := range []string{`"future":true,`, `"disableAllHooks":false,`, `"disableAllHooks":true,`} {
		selected, inactive, err := selectCodexHooksTest(t, `{`+extra+`"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`, false)
		if err != nil || len(inactive) == 0 || len(selected) != 0 {
			t.Fatal("invalid native file root activated", err, inactive, selected)
		}
	}
}
func TestNativeCodexHookWindowsAlias(t *testing.T) {
	body := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true","command_windows":"Write-Output fixture"}]}]}}`
	values, _, err := selectCodexHooksTest(t, body, true)
	if err != nil || !strings.Contains(string(mustNativeJSON(t, values)), "command_windows") {
		t.Fatal("alias not preserved", err)
	}
	for _, field := range []string{`"command_windows":false`, `"command_windows":"one","commandWindows":"two"`} {
		if _, _, err := selectCodexHooksTest(t, `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true",`+field+`}]}]}}`, true); err == nil {
			t.Fatal("invalid or duplicate alias accepted")
		}
	}
}

func TestNativeCodexMCPHookNativeParserLimits(t *testing.T) {
	for _, input := range []string{`null`, `{"value":null}`, `{"nested":{"value":null}}`, `{"nested":[1,{"value":null}]}`} {
		body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"mcp_tool","server":"fixture","tool":"record","input":` + input + `}]}]}}`
		for _, inline := range []bool{false, true} {
			if _, _, err := selectCodexHooksTest(t, body, inline); err == nil {
				t.Fatal("native parser rejects null, but selector accepted it", input)
			}
		}
		repo := codexHooksFixture(t, "project", body, false)
		if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); err == nil {
			t.Fatal("invalid native hook file projected")
		}
		if _, err := os.Stat(filepath.Join(repo, ".codex/hooks.json")); !os.IsNotExist(err) {
			t.Fatal("native parser refusal wrote a file")
		}
	}
	for _, input := range []string{`{}`, `{"nested":[{"value":true},7,"${tool_input}",0.5]}`, `{"value":18446744073709551615}`} {
		body := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"mcp_tool","server":"fixture","tool":"record","input":` + input + `}]}]}}`
		selected, inactive, err := selectCodexHooksTest(t, body, false)
		original, parseErr := parseNative([]byte(body), "json")
		if err != nil || parseErr != nil || len(inactive) != 0 || nativeHash(selected) != nativeHash(original) {
			t.Fatal("valid MCP input changed or refused", err, inactive)
		}
	}
}

func TestNativeCodexMCPSessionEndRemainsAtomic(t *testing.T) {
	body := `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"first"},{"type":"mcp_tool","server":"fixture","tool":"record"}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"retained"}]}]}}`
	for _, inline := range []bool{false, true} {
		selected, inactive, err := selectCodexHooksTest(t, body, inline)
		if err != nil || len(inactive) == 0 {
			t.Fatal(err, inactive)
		}
		events := selected["hooks"].(map[string]any)
		if len(events) != 1 || events["PreToolUse"] == nil {
			t.Fatal("unsupported event partly activated")
		}
	}
	for _, required := range []bool{false, true} {
		repo := codexHooksFixture(t, "project", body, required)
		_, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
		if required {
			if err == nil {
				t.Fatal("required unsupported event activated")
			}
		} else if err != nil || strings.Contains(readNativeTest(t, filepath.Join(repo, ".codex/hooks.json")), "SessionEnd") {
			t.Fatal("optional unsupported event projected", err)
		}
	}
}

func TestNativeCodexHookImportExternalState(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		source := t.TempDir()
		base := source
		if scope == "project" {
			base = filepath.Join(source, ".codex")
			os.MkdirAll(base, 0700)
		}
		original := "[[hooks.SessionStart]]\n[[hooks.SessionStart.hooks]]\ntype='command'\ncommand='true'\n[hooks.state.fixture]\ntrusted_hash='fixture-hash'\nenabled=false\n"
		os.WriteFile(filepath.Join(base, "config.toml"), []byte(original), 0600)
		repo := source
		home := ""
		if scope == "user" {
			repo = t.TempDir()
			home = source
		}
		if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: scope, NativeHome: home}); err != nil {
			t.Fatal(err)
		}
		imported := readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"))
		if strings.Contains(imported, "fixture-hash") || strings.Contains(imported, "state") || !strings.Contains(imported, "SessionStart") {
			t.Fatal("native trust state imported or events lost")
		}
		if !strings.Contains(readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/import-report.json")), "hooks.state") {
			t.Fatal("excluded native state not reported")
		}
		if readNativeTest(t, filepath.Join(base, "config.toml")) != original {
			t.Fatal("native state source changed")
		}
	}
}
func TestNativeCodexHookCredentialImportRefusesTransaction(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "hooks.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"mcp_tool","server":"fixture","tool":"record","input":{"apiKey":"literal-secret"}}]}]}}`), 0600)
	repo := t.TempDir()
	err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home})
	if err == nil {
		t.Fatal("credential event imported")
	}
	if strings.Contains(err.Error(), "literal-secret") {
		t.Fatal("credential in error")
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
		t.Fatal("refusal wrote canonical files")
	}
}
func TestNativeCodexPortableDisableRefusal(t *testing.T) {
	repo := codexHooksFixture(t, "project", `{"hooks":{}}`, true)
	os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["hooks"]}`), 0600)
	os.MkdirAll(filepath.Join(repo, ".agents/hooks"), 0700)
	os.WriteFile(filepath.Join(repo, ".agents/hooks/hooks.json"), []byte(`{"disableAllHooks":true,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`), 0600)
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); err == nil {
		t.Fatal("unsupported disableAllHooks mapped")
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex/hooks.json")); !os.IsNotExist(err) {
		t.Fatal("refusal wrote native hooks")
	}
}

func TestNativeCodexHookSharedOwnershipAndRemoval(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	a := codexHooksFixture(t, "user", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"first"}]}]}}`, true)
	b := codexHooksFixture(t, "user", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"second"}]}]}}`, true)
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	for _, repo := range []string{a, b} {
		if _, err := ApplyProjection("codex", repo, options); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(home, "hooks.json")
	original := readNativeTest(t, target)
	if !strings.Contains(original, "first") || !strings.Contains(original, "second") {
		t.Fatal("disjoint sources did not merge")
	}
	os.WriteFile(filepath.Join(a, ".agents/native/com.openai.codex/hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"replacement"}]}]}}`), 0600)
	options.Force = true
	if _, err := ApplyProjection("codex", a, options); err == nil {
		t.Fatal("force replaced another source's hook")
	}
	if readNativeTest(t, target) != original {
		t.Fatal("refused conflict changed target")
	}
	os.WriteFile(filepath.Join(a, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
	if _, err := ApplyProjection("codex", a, options); err != nil {
		t.Fatal(err)
	}
	remaining := readNativeTest(t, target)
	if strings.Contains(remaining, "first") || !strings.Contains(remaining, "second") {
		t.Fatal("removal affected another source")
	}
	os.WriteFile(filepath.Join(b, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
	if _, err := ApplyProjection("codex", b, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("empty owned hook file remains")
	}
}

func TestNativeCodexUnknownRootHasNoPositiveActivationEvidence(t *testing.T) {
	repo := codexHooksFixture(t, "project", `{"future":true,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"true"}]}]}}`, false)
	plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, feature := range plan.Native.Features {
		if feature.Feature == "hooks" {
			found = true
			if feature.Activation != "inactive" || feature.NativeStatus != "unverified" || len(feature.Evidence) > 0 {
				t.Fatal("inactive artifact inherited positive evidence")
			}
		}
	}
	if !found {
		t.Fatal("inactive artifact absent from plan")
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex/hooks.json")); !os.IsNotExist(err) {
		t.Fatal("invalid native source activated")
	}
}
