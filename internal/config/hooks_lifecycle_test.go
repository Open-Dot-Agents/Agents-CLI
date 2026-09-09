package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hookTree(t *testing.T, root string, disabled bool) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".agents", "AGENTS.md"), "# Instructions\n")
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["hooks"]}`)
	doc := hooksDocument{DisableAllHooks: disabled, Hooks: map[string][]HookGroup{
		"SessionStart": {{Hooks: []HookHandler{{Type: "command", Command: "echo hook", TimeoutSec: 5}}}},
	}}
	data, _ := json.Marshal(doc)
	writeFixture(t, filepath.Join(root, ".agents", "hooks", "hooks.json"), string(data))
}

func treeBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		result[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDisabledHooksRefuseBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "claude", "all"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			hookTree(t, root, true)
			before := treeBytes(t, root)
			result, err := ApplySync(vendor, root, ApplyOptions{Force: true, Backup: true})
			if err == nil || result.Applicable || !strings.Contains(err.Error(), "ODA-HOOK-0001") {
				t.Fatalf("disabled hooks accepted: %#v %v", result, err)
			}
			if !reflect.DeepEqual(before, treeBytes(t, root)) {
				t.Fatal("failed projection wrote files")
			}
		})
	}
}

func TestHookRemovalPreservesSettingsAndIsIdempotent(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			hookTree(t, root, false)
			path := vendorHooksPath(vendor, root)
			if vendor == "claude" {
				writeFixture(t, path, `{"theme":"dark","disableAllHooks":false}`)
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if vendor == "claude" {
				data, _ := os.ReadFile(path)
				writeFixture(t, path, strings.Replace(string(data), "dark", "light", 1))
			}
			plan, err := PlanProjection(vendor, root, ApplyOptions{})
			if err != nil || !plan.Applicable {
				t.Fatalf("unrelated settings edit caused conflict: %v %#v", err, plan)
			}
			for _, a := range plan.Actions {
				if a.Operation != "unchanged" {
					t.Fatalf("not idempotent: %#v", plan)
				}
			}
			writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":[]}`)
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if vendor == "claude" {
				data, _ := os.ReadFile(path)
				var doc map[string]any
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				if _, ok := doc["hooks"]; ok {
					t.Fatal("hooks remain")
				}
				if doc["theme"] != "light" || doc["disableAllHooks"] != false {
					t.Fatalf("unrelated settings lost: %s", data)
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("hook file remains: %v", err)
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHookDriftBackupAndRollback(t *testing.T) {
	root := t.TempDir()
	hookTree(t, root, false)
	if _, err := ApplySync("all", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	path := vendorHooksPath("claude", root)
	data, _ := os.ReadFile(path)
	changed := strings.Replace(string(data), "echo hook", "echo changed", 1)
	writeFixture(t, path, changed)
	plan, err := PlanProjection("claude", root, ApplyOptions{})
	if err != nil || plan.Applicable {
		t.Fatalf("drift accepted: %v %#v", err, plan)
	}
	if _, err := ApplyProjection("claude", root, ApplyOptions{Force: true, Backup: true}); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".backup-*")
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	saved, _ := os.ReadFile(backups[0])
	if string(saved) != changed {
		t.Fatal("backup did not retain prior settings")
	}
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":[]}`)
	before := treeBytes(t, root)
	prepared, result, err := prepareSync("all", root, ApplyOptions{})
	if err != nil || !result.Applicable {
		t.Fatal(err, result)
	}
	calls := 0
	err = applyPreparedProjections(prepared, ApplyOptions{}, func(path string, data []byte, mode fs.FileMode) error {
		calls++
		if calls == 2 {
			return errors.New("injected hook removal failure")
		}
		return atomicWrite(path, data, mode)
	})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("expected failure: %v", err)
	}
	if !reflect.DeepEqual(before, treeBytes(t, root)) {
		t.Fatal("rollback did not restore hooks, settings and state")
	}
}

func TestLegacyClaudeHookOwnershipMigrates(t *testing.T) {
	for _, remove := range []bool{false, true} {
		root := t.TempDir()
		hookTree(t, root, false)
		path := vendorHooksPath("claude", root)
		writeFixture(t, path, `{"theme":"dark"}`)
		if _, err := ApplyProjection("claude", root, ApplyOptions{}); err != nil {
			t.Fatal(err)
		}
		state, err := loadOwnership(root, "claude")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		state.HooksHash = ""
		state.Files[".claude/settings.json"] = digest(data)
		raw, _ := json.Marshal(state)
		writeFixture(t, filepath.Join(root, ".agents", ".state", "reference-cli", "claude.json"), string(raw))
		if remove {
			writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":[]}`)
		}
		if _, err := ApplyProjection("claude", root, ApplyOptions{}); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(path)
		if !strings.Contains(string(data), "dark") {
			t.Fatal("legacy removal lost settings")
		}
		state, err = loadOwnership(root, "claude")
		if err != nil {
			t.Fatal(err)
		}
		if state.Files[".claude/settings.json"] != "" || (!remove && state.HooksHash == "") {
			t.Fatalf("state not migrated: %#v", state)
		}
	}
}

func TestHookValidationMatchesSharedInvalidFixtures(t *testing.T) {
	paths, err := filepath.Glob("../../../SPEC/examples/invalid/hooks-*.json")
	if err != nil {
		t.Fatal(err)
	}
	// The CLI can also be tested as an independent checkout.
	if len(paths) == 0 {
		t.Skip("SPEC fixtures are tested in the root Workbench job")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			root := t.TempDir()
			hookTree(t, root, false)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(root, ".agents", "hooks", "hooks.json"), string(data))
			if err := Validate(filepath.Join(root, ".agents")); err == nil {
				t.Fatal("invalid shared fixture accepted")
			}
		})
	}
}

func TestHookMatcherLossAndTimeoutDefaults(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex", "claude"} {
		doc := hooksDocument{Hooks: map[string][]HookGroup{"Stop": {{Matcher: "never", Hooks: []HookHandler{{Type: "command", Command: "echo test"}}}}}}
		if _, err := nativeHooks(vendor, doc); err == nil {
			t.Fatalf("%s accepted ignored matcher", vendor)
		}
		doc.Hooks["Stop"][0].Matcher = ""
		native, err := nativeHooks(vendor, doc)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(native)
		if strings.Contains(string(data), "timeout") {
			t.Fatalf("zero timeout did not use native default: %s", data)
		}
	}
}

func TestImportHooksWithoutMCP(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Instructions\n")
			raw := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo test","timeout":5}]}]}}`
			if vendor == "copilot" {
				raw = `{"version":1,"disableAllHooks":true,"hooks":{"sessionStart":[{"type":"command","command":"echo test","timeoutSec":5}]}}`
			}
			if vendor == "claude" {
				raw = `{"disableAllHooks":true,"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo test","timeout":5}]}]}}`
			}
			writeFixture(t, vendorHooksPath(vendor, root), raw)
			if err := ImportRepository(vendor, root, false, false); err != nil {
				t.Fatal(err)
			}
			doc, err := readCanonicalHooks(filepath.Join(root, ".agents"))
			if err != nil {
				t.Fatal(err)
			}
			if vendor != "codex" && !doc.DisableAllHooks {
				t.Fatal("import enabled disabled hooks")
			}
			profiles, _, err := validateManifest(filepath.Join(root, ".agents", "manifest.json"))
			if err != nil || !reflect.DeepEqual(profiles, []string{"hooks"}) {
				t.Fatal(profiles, err)
			}
			if _, err := os.Stat(filepath.Join(root, ".agents", "tools", "mcp.json")); !os.IsNotExist(err) {
				t.Fatal("import created an unselected MCP catalogue")
			}
		})
	}
}

func TestCopilotDisabledHookRoundTrip(t *testing.T) {
	root := t.TempDir()
	hookTree(t, root, true)
	if _, err := ApplyProjection("copilot", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	hooks, found, err := readVendorHooks("copilot", root)
	if err != nil || !found || !hooks.DisableAllHooks {
		t.Fatalf("disabled state lost: %#v %v", hooks, err)
	}
	if hooks.Hooks["SessionStart"][0].Hooks[0].TimeoutSec != 5 {
		t.Fatal("timeout lost")
	}
}

func TestUnsupportedHookImportDoesNotWrite(t *testing.T) {
	for _, raw := range []string{
		`{"hooks":{"Unknown":[{"hooks":[{"type":"command","command":"echo test"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"prompt","command":"echo test"}]}]}}`,
		`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo test","async":true}]}]}}`,
	} {
		root := t.TempDir()
		writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Instructions\n")
		writeFixture(t, vendorHooksPath("claude", root), raw)
		before := treeBytes(t, root)
		if err := ImportRepository("claude", root, false, false); err == nil {
			t.Fatal("unsupported import accepted")
		}
		if !reflect.DeepEqual(before, treeBytes(t, root)) {
			t.Fatal("rejected import changed repository")
		}
	}
}

func TestDisabledExportAndConvertRefuseBeforeWrites(t *testing.T) {
	root := t.TempDir()
	hookTree(t, root, true)
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["tools","hooks"]}`)
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{}}`)
	native := t.TempDir()
	if _, err := ApplyProjection("copilot", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, vendor := range []string{"codex", "claude"} {
		writeFixture(t, vendorMCPPath(vendor, native), "existing settings")
		before := treeBytes(t, native)
		for _, err := range []error{
			ExportWithOptions(vendor, filepath.Join(root, ".agents"), native, WriteOptions{Force: true, Backup: true}),
			ConvertWithOptions("copilot", vendor, root, native, WriteOptions{Force: true, Backup: true}),
		} {
			if err == nil || !strings.Contains(err.Error(), "ODA-HOOK-0001") {
				t.Fatalf("expected hook refusal: %v", err)
			}
		}
		if !reflect.DeepEqual(before, treeBytes(t, native)) {
			t.Fatal("refusal wrote output or backups")
		}
	}
}
