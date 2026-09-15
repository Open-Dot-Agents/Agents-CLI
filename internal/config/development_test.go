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

func developmentFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := InitDevelopment(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDevelopmentInitAndRefusal(t *testing.T) {
	root := developmentFixture(t)
	canonical := filepath.Join(root, ".agents")
	if err := ValidateRepositoryWithOptions(canonical, true); err != nil {
		t.Fatal(err)
	}
	if err := Validate(canonical); err == nil {
		t.Fatal("stable validation accepted experimental policy")
	}
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			before := developmentSnapshot(t, root)
			plan, err := PlanProjection(vendor, root, ApplyOptions{Experimental: true})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Applicable || len(plan.Actions) != 0 || plan.Security == nil || plan.Security.Declared.Development == nil {
				t.Fatalf("missing refusal or policy: %+v", plan)
			}
			wanted := DevelopmentPolicy{Version: "1", Preset: "development", ProjectWork: "allow", LocalCommits: "allow", ExternalChanges: "ask", DestructiveWork: "ask", ProtectedPaths: []string{".env", "secrets"}}
			if !reflect.DeepEqual(*plan.Security.Declared.Development, wanted) {
				t.Fatalf("wrong declared defaults: %+v", plan.Security.Declared.Development)
			}
			if !strings.Contains(strings.Join(plan.Diagnostics, " "), "ODA-DEVELOPMENT-0001") {
				t.Fatal(plan.Diagnostics)
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{Experimental: true, Force: true, Backup: true}); err == nil {
				t.Fatal("force activated an unverified security policy")
			}
			if !reflect.DeepEqual(before, developmentSnapshot(t, root)) {
				t.Fatal("refusal changed the repository")
			}
		})
	}
}

func TestDevelopmentEditedChoicesAppearInPlan(t *testing.T) {
	root := developmentFixture(t)
	policy := DevelopmentPolicy{Version: "1", Preset: "development", ProjectWork: "ask", LocalCommits: "deny", ExternalChanges: "deny", DestructiveWork: "deny", ProtectedPaths: []string{"packages/api/.env.local", "private"}}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents", developmentPath), data, 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanProjection("codex", root, ApplyOptions{Experimental: true})
	if err != nil || plan.Security == nil || plan.Security.Declared.Development == nil {
		t.Fatalf("missing edited policy: %+v %v", plan, err)
	}
	if !reflect.DeepEqual(*plan.Security.Declared.Development, policy) {
		t.Fatal("plan replaced user decisions with defaults")
	}
}

func developmentSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		result[path] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDevelopmentRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{"unknown-key", `"preset": "development"`, `"preset": "development", "unsafe": true`},
		{"duplicate-key", `"local_commits": "allow"`, `"local_commits": "allow", "local_commits": "deny"`},
		{"missing-decision", `"local_commits": "allow",`, ``},
		{"invalid-decision", `"external_changes": "ask"`, `"external_changes": "automatic"`},
		{"null-decision", `"external_changes": "ask"`, `"external_changes": null`},
		{"null-paths", `"protected_paths": [`, `"protected_paths": null, "invalid": [`},
		{"wrong-version", `"version": "1"`, `"version": "2"`},
		{"wrong-preset", `"preset": "development"`, `"preset": "unrestricted"`},
		{"traversal", `".env"`, `"../outside"`},
		{"absolute", `".env"`, `"/outside"`},
		{"glob", `".env"`, `"**/.env"`},
		{"entire-workspace", `".env"`, `"."`},
		{"duplicate-path", `"secrets"`, `".env"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := developmentFixture(t)
			path := filepath.Join(root, ".agents", developmentPath)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), test.old) {
				t.Fatal("mutation did not match")
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), test.old, test.replacement, 1)), 0644); err != nil {
				t.Fatal(err)
			}
			if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestDevelopmentExtensionCannotBeWeakened(t *testing.T) {
	for _, mutate := range []func(*PermissionsPolicy){
		func(p *PermissionsPolicy) { e := p.Extensions[DevelopmentExtension]; *e.Required = false },
		func(p *PermissionsPolicy) {
			e := p.Extensions[DevelopmentExtension]
			e.Data["path"] = json.RawMessage(`"../outside"`)
		},
		func(p *PermissionsPolicy) { p.Default = "allow" },
		func(p *PermissionsPolicy) { p.Coverage = []string{"shell"} },
		func(p *PermissionsPolicy) { p.Rules = []PermissionRule{{Kind: "tool", Name: "*", Effect: "allow"}} },
	} {
		root := developmentFixture(t)
		path := filepath.Join(root, ".agents/permissions/permissions.json")
		var p PermissionsPolicy
		if err := decodePolicy(path, &p); err != nil {
			t.Fatal(err)
		}
		mutate(&p)
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
			t.Fatal("invalid extension accepted")
		}
	}
}

func TestDevelopmentInitPreservesExistingWork(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("User instructions\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := InitDevelopment(root); err != nil {
		t.Fatal(err)
	}
	before := developmentSnapshot(t, root)
	if before[path] != "User instructions\n" || before[filepath.Join(root, ".agents/AGENTS.md")] != before[path] {
		t.Fatal("instructions were replaced or lost")
	}
	if err := InitDevelopment(root); err == nil {
		t.Fatal("existing tree overwritten")
	}
	if !reflect.DeepEqual(before, developmentSnapshot(t, root)) {
		t.Fatal("refused init changed existing files")
	}
}

func TestDevelopmentInitRollback(t *testing.T) {
	root := t.TempDir()
	before := developmentSnapshot(t, root)
	calls := 0
	err := initDevelopment(root, func(path string, data []byte, mode fs.FileMode) error {
		calls++
		if calls == 3 {
			return errors.New("injected write failure")
		}
		return atomicWrite(path, data, mode)
	})
	if err == nil || !reflect.DeepEqual(before, developmentSnapshot(t, root)) {
		t.Fatalf("init did not roll back: %v", err)
	}
}

func TestDevelopmentRejectsSymlinkPolicy(t *testing.T) {
	root := developmentFixture(t)
	path := filepath.Join(root, ".agents", developmentPath)
	external := filepath.Join(t.TempDir(), "policy.json")
	if err := os.Rename(path, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
		t.Fatal("symlinked policy accepted")
	}
}

func TestDevelopmentAdoptionPreservesProfilesAndBacksUpManifest(t *testing.T) {
	for _, version := range []string{manifestVersion, ExperimentalVersion, NativeVersion} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			if err := Init(root, false); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(root, ".agents/manifest.json")
			original := []byte(`{"version":"` + version + `","profiles":["tools","hooks","skills"],"requires":["skills"]}`)
			if err := os.WriteFile(manifestPath, original, 0644); err != nil {
				t.Fatal(err)
			}
			instructions, _ := os.ReadFile(filepath.Join(root, ".agents/AGENTS.md"))
			if err := AdoptDevelopment(root); err != nil {
				t.Fatal(err)
			}
			var manifest manifestDocument
			if err := decodePolicy(manifestPath, &manifest); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(manifest.Profiles, []string{"tools", "hooks", "skills", "permissions"}) || !reflect.DeepEqual(manifest.Requires, []string{"skills", "permissions"}) {
				t.Fatal("lost existing selections", manifest)
			}
			if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err != nil {
				t.Fatal(err)
			}
			backups, err := filepath.Glob(manifestPath + ".backup-*")
			if err != nil || len(backups) != 1 {
				t.Fatal("missing manifest backup", backups, err)
			}
			data, _ := os.ReadFile(backups[0])
			info, _ := os.Stat(backups[0])
			if string(data) != string(original) || info.Mode().Perm() != 0600 {
				t.Fatal("manifest backup contents or permissions differ")
			}
			data, _ = os.ReadFile(filepath.Join(root, ".agents/AGENTS.md"))
			if string(data) != string(instructions) {
				t.Fatal("adoption changed instructions")
			}
			plan, err := PlanProjection("codex", root, ApplyOptions{Experimental: true})
			if err != nil || plan.Applicable || plan.Security == nil || plan.Security.Declared.Development == nil {
				t.Fatalf("missing development refusal: %+v %v", plan, err)
			}
			if err := AdoptDevelopment(root); err == nil {
				t.Fatal("adoption overwrote existing policy")
			}
		})
	}
}

func TestDevelopmentAdoptionRollbackAndUnselectedPolicy(t *testing.T) {
	for _, collision := range []bool{false, true} {
		root := t.TempDir()
		if err := Init(root, false); err != nil {
			t.Fatal(err)
		}
		if collision {
			if err := atomicWrite(filepath.Join(root, ".agents", developmentPath), []byte("preserve unselected policy"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		before := developmentSnapshot(t, root)
		calls := 0
		err := adoptDevelopment(root, func(path string, data []byte, mode fs.FileMode) error {
			calls++
			if calls == 3 {
				return errors.New("injected adoption failure")
			}
			return atomicWrite(path, data, mode)
		})
		if err == nil || !reflect.DeepEqual(before, developmentSnapshot(t, root)) {
			t.Fatalf("failed adoption did not preserve original tree: %v", err)
		}
		if collision && calls != 0 {
			t.Fatal("collision was detected after a write")
		}
	}
}
