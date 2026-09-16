package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func practicalFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := InitPracticalDevelopment(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPracticalDevelopmentLifecycle(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root := practicalFixture(t)
			options := ApplyOptions{Experimental: true}
			plan, err := ApplyProjection(vendor, root, options)
			if err != nil || !plan.Applicable || plan.Security.Status != "practical" || len(plan.Warnings) == 0 {
				t.Fatalf("apply: %+v %v", plan, err)
			}
			plan, err = PlanProjection(vendor, root, options)
			if err != nil || len(plan.Actions) != 0 {
				t.Fatalf("repeat: %+v %v", plan, err)
			}
			path := filepath.Join(root, ".agents", developmentPath)
			var p DevelopmentPolicy
			if err := decodePolicy(path, &p); err != nil {
				t.Fatal(err)
			}
			p.LocalCommits = "ask"
			data, _ := json.Marshal(p)
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			plan, err = ApplyProjection(vendor, root, options)
			if err != nil || len(plan.Actions) == 0 {
				t.Fatalf("update: %+v %v", plan, err)
			}
			destination := filepath.Join(root, ".github/copilot-instructions.md")
			if vendor == "codex" {
				destination = filepath.Join(root, ".codex/config.toml")
			}
			data, err = os.ReadFile(destination)
			if err != nil || !strings.Contains(string(data), "Local commits: ask") {
				t.Fatalf("guidance did not update: %s %v", data, err)
			}
			if vendor == "codex" {
				values, err := parseNative(data, "toml")
				if err != nil || values["approval_policy"] != "on-request" || values["default_permissions"] != "agents-development" {
					t.Fatalf("settings: %+v %v", values, err)
				}
			} else if _, err := os.Stat(filepath.Join(root, ".copilot/settings.json")); !os.IsNotExist(err) {
				t.Fatal("Copilot guidance changed native authority", err)
			}
			if err := os.WriteFile(filepath.Join(root, ".agents/manifest.json"), []byte("{\"version\":\"1.1.0-draft.2\",\"profiles\":[],\"requires\":[]}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal("remove", err)
			}
			data, err = os.ReadFile(destination)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "agents-development") || strings.Contains(string(data), "Local commits: ask") {
				t.Fatal("stale development settings", string(data))
			}
		})
	}
}

func TestPracticalDevelopmentConflictsAndExport(t *testing.T) {
	root := practicalFixture(t)
	path := filepath.Join(root, ".codex/config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	before := []byte("sandbox_mode = 'danger-full-access'\nmodel = 'keep'\n")
	if err := os.WriteFile(path, before, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, Force: true, Backup: true}); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatal("legacy conflict not refused", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(before) {
		t.Fatal("conflict changed native config")
	}
	if err := ExportWithOptions("codex", filepath.Join(root, ".agents"), t.TempDir(), WriteOptions{Experimental: true}); err == nil {
		t.Fatal("legacy export bypassed practical lifecycle")
	}
}

func TestPracticalDevelopmentAdoption(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, false); err != nil {
		t.Fatal(err)
	}
	if err := AdoptPracticalDevelopment(root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"guardrails/development.md", "permissions/development.json"} {
		if _, err := os.Stat(filepath.Join(root, ".agents", path)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPracticalCopilotCanonicalLinkGuidance(t *testing.T) {
	root := practicalFixture(t)
	link := filepath.Join(root, "AGENTS.md")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".agents/AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	core := readNativeTest(t, filepath.Join(root, ".agents/AGENTS.md"))
	options := ApplyOptions{Experimental: true}
	if _, err := ApplyProjection("copilot", root, options); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".github/copilot-instructions.md")
	if text := readNativeTest(t, target); !strings.Contains(text, "Local commits: allow") || !strings.Contains(text, "Development guardrails") {
		t.Fatal("canonical link lost development guidance", text)
	}
	plan, err := PlanProjection("copilot", root, options)
	if err != nil || len(plan.Actions) != 0 {
		t.Fatal("repeat apply", plan, err)
	}
	path := filepath.Join(root, ".agents", developmentPath)
	var policy DevelopmentPolicy
	if err := decodePolicy(path, &policy); err != nil {
		t.Fatal(err)
	}
	policy.LocalCommits = "deny"
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data))
	if _, err := ApplyProjection("copilot", root, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readNativeTest(t, target), "Local commits: deny") {
		t.Fatal("updated guidance was lost")
	}
	writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[],"requires":[]}`)
	if _, err := ApplyProjection("copilot", root, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("removed policy left guidance", err)
	}
	if value, err := os.Readlink(link); err != nil || value != ".agents/AGENTS.md" || readNativeTest(t, link) != core {
		t.Fatal("development projection changed canonical instructions", err)
	}
}

func TestPracticalDevelopmentRollbackAndGuidance(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root := practicalFixture(t)
			options := ApplyOptions{Experimental: true}
			build, err := buildNativeProjection(vendor, root, options)
			if err != nil || !build.plan.Applicable {
				t.Fatal(build.plan, err)
			}
			before := instructionSnapshot(t, root)
			err = nativeRunTransaction(build.changes, func(stage string, index int) error {
				if stage == "after-write" && index == len(build.changes)-1 {
					return fmt.Errorf("injected practical apply failure")
				}
				return nil
			})
			if err == nil || nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("rollback changed project files", err)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			guidance := filepath.Join(root, ".agents", developmentGuardrailsPath)
			if err := os.WriteFile(guidance, []byte("Custom guardrail: use reviewed release artifacts.\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, ".github/copilot-instructions.md")
			if vendor == "codex" {
				target = filepath.Join(root, ".codex/config.toml")
			}
			data, _ := os.ReadFile(target)
			if !strings.Contains(string(data), "Custom guardrail") {
				t.Fatal("custom guidance was lost")
			}
			if err := os.WriteFile(target, append(data, []byte("\n# local user change\n")...), 0644); err != nil {
				t.Fatal(err)
			}
			// Asset edits must not be overwritten; structured config comments
			// are outside field ownership and must survive repeat apply.
			plan, err := PlanProjection(vendor, root, options)
			if err != nil {
				t.Fatal(err)
			}
			if vendor == "copilot" && plan.Applicable {
				t.Fatal("unowned instruction edit was ignored")
			}
		})
	}
}

func TestPracticalDevelopmentValidationAndStrictChoice(t *testing.T) {
	root := practicalFixture(t)
	path := filepath.Join(root, ".agents", developmentPath)
	var p DevelopmentPolicy
	if err := decodePolicy(path, &p); err != nil {
		t.Fatal(err)
	}
	p.Enforcement = "strict"
	data, _ := json.Marshal(p)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, root)
	if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true}); err == nil {
		t.Fatal("strict mode silently activated practical controls")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("strict refusal changed project")
	}
	p.Enforcement = "practical"
	data, _ = json.Marshal(p)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".agents", developmentGuardrailsPath)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
		t.Fatal("missing practical guardrails accepted")
	}
}

func TestPracticalDevelopmentPreservesUnrelatedNativeSettings(t *testing.T) {
	root := practicalFixture(t)
	path := filepath.Join(root, ".codex/config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = 'keep-model'\n[mcp_servers.keep]\ncommand = 'keep-command'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	values, err := parseNative(data, "toml")
	if err != nil || values["model"] != "keep-model" || values["mcp_servers"] == nil {
		t.Fatal("unrelated settings changed", values, err)
	}
	if !strings.Contains(string(data), "on-request") {
		t.Fatal("missing approval setting")
	}
	data = []byte(strings.Replace(string(data), "on-request", "never", 1))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanProjection("codex", root, ApplyOptions{Experimental: true, Force: true, Backup: true})
	if err != nil || plan.Applicable {
		t.Fatal("changed native authority was silently overwritten", plan, err)
	}
}

func TestDevelopmentOnlyMigratesLegacyAndPreservesOtherProfiles(t *testing.T) {
	root := practicalFixture(t)
	canonical := filepath.Join(root, ".agents")
	path := filepath.Join(root, ".codex/config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	original := "model = 'keep'\nsandbox_mode = 'danger-full-access'\napproval_policy = 'on-request'\napprovals_reviewer = 'auto_review'\n[sandbox_workspace_write]\nnetwork_access = true\n[mcp_servers.keep]\ncommand = 'keep-command'\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "manifest.json"), []byte("{\"version\":\"1.1.0-draft.2\",\"profiles\":[\"permissions\"],\"requires\":[\"permissions\",\"mcp.envRef\"]}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanProjection("codex", root, ApplyOptions{Experimental: true}); err == nil {
		t.Fatal("full apply ignored unmet required capability")
	}
	options := ApplyOptions{Experimental: true, DevelopmentOnly: true, Force: true, Backup: true}
	plan, err := ApplyProjection("codex", root, options)
	if err != nil || !plan.Applicable {
		t.Fatal(plan, err)
	}
	data, _ := os.ReadFile(path)
	values, err := parseNative(data, "toml")
	if err != nil || values["sandbox_mode"] != nil || values["sandbox_workspace_write"] != nil ||
		values["approvals_reviewer"] != "user" || values["model"] != "keep" || values["mcp_servers"] == nil {
		t.Fatal("incorrect migration", values, err)
	}
	found := false
	for _, action := range plan.Actions {
		if action.Detail != "backup" {
			continue
		}
		data, _ := os.ReadFile(action.Path)
		found = found || string(data) == original
	}
	if !found {
		t.Fatal("missing original config backup", plan.Actions)
	}
	options.Force, options.Backup = false, false
	statePath := filepath.Join(canonical, "state/native-codex.json")
	var state nativeRegistry
	if err := nativeDecodePolicy(statePath, &state); err != nil {
		t.Fatal(err)
	}
	key := nativeKey(path, "/model")
	state.Settings[key] = nativeOwned{Source: canonical, Hash: nativeHash("keep")}
	data, _ = json.Marshal(state)
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("codex", root, options); err != nil {
		t.Fatal(err)
	}
	if err := nativeDecodePolicy(statePath, &state); err != nil || state.Settings[key].Hash != nativeHash("keep") {
		t.Fatal("scoped apply removed unrelated ownership", err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "manifest.json"), []byte("{\"version\":\"1.1.0-draft.2\",\"profiles\":[],\"requires\":[\"mcp.envRef\"]}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("codex", root, options); err != nil {
		t.Fatal("scoped removal", err)
	}
	data, _ = os.ReadFile(path)
	values, err = parseNative(data, "toml")
	if err != nil || values["model"] != "keep" || values["mcp_servers"] == nil || values["default_permissions"] != nil {
		t.Fatal("scoped removal changed unrelated settings", values, err)
	}
}

func TestDevelopmentOnlyUsesTheTestedNativeSettings(t *testing.T) {
	var configurations []string
	for _, scoped := range []bool{false, true} {
		root := practicalFixture(t)
		if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, DevelopmentOnly: scoped}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, ".codex/config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		configurations = append(configurations, string(data))
	}
	if configurations[0] != configurations[1] {
		t.Fatal("scoped apply changed native settings or guidance")
	}
}

// TestDevelopmentRollbackEvidence prepares a restored fixture for a subsequent
// native session. Normal test runs do not use an external fixture directory.
func TestDevelopmentRollbackEvidence(t *testing.T) {
	root := os.Getenv("AGENTS_DEVELOPMENT_ROLLBACK_ROOT")
	if root == "" {
		t.Skip("native workflow fixture was not requested")
	}
	vendor := os.Getenv("AGENTS_DEVELOPMENT_ROLLBACK_VENDOR")
	output := os.Getenv("AGENTS_DEVELOPMENT_ROLLBACK_RESULT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(output) || filepath.Dir(output) != filepath.Dir(root) || (vendor != "codex" && vendor != "copilot") {
		t.Fatal("invalid disposable rollback fixture")
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("rollback fixture already has canonical configuration")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("rollback result already exists")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".agents-development-fixture"))
	if err != nil || string(marker) != "disposable native workflow\n" {
		t.Fatal("rollback directory is not a disposable workflow fixture")
	}
	if err := InitPracticalDevelopment(root); err != nil {
		t.Fatal(err)
	}
	options := ApplyOptions{Experimental: true, DevelopmentOnly: vendor == "codex"}
	if _, err := ApplyProjection(vendor, root, options); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".agents", developmentPath)
	var policy DevelopmentPolicy
	if err := decodePolicy(path, &policy); err != nil {
		t.Fatal(err)
	}
	policy.LocalCommits = "deny"
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	build, err := buildNativeProjection(vendor, root, options)
	if err != nil || !build.plan.Applicable || len(build.changes) == 0 {
		t.Fatal(build.plan, err)
	}
	before := instructionSnapshot(t, root)
	injected := false
	err = nativeRunTransaction(build.changes, func(stage string, index int) error {
		if stage == "after-write" && index == len(build.changes)-1 {
			injected = true
			return fmt.Errorf("development fixture rollback injection")
		}
		return nil
	})
	after := instructionSnapshot(t, root)
	if err == nil || !injected || nativeHash(before) != nativeHash(after) {
		t.Fatal("rollback did not restore the disposable fixture", err)
	}
	data, err = json.Marshal(map[string]any{"injected": injected, "before": before, "after": after})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
}
