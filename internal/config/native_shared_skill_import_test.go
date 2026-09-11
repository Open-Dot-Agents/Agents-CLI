package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeCopilotSharedSkillImport(t *testing.T) {
	for _, manifest := range []bool{false, true} {
		t.Run(map[bool]string{false: "bare", true: "draft2"}[manifest], func(t *testing.T) {
			repo := t.TempDir()
			root := filepath.Join(repo, ".agents")
			definition := filepath.Join(root, "skills/fixture/SKILL.md")
			writeFixture(t, definition, importedSkill)
			writeFixture(t, filepath.Join(root, "skills/fixture/data.bin"), "\x00\xff")
			if manifest {
				writeFixture(t, filepath.Join(root, "manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
				writeFixture(t, filepath.Join(root, "AGENTS.md"), "Keep canonical policy.\n")
			}
			info, err := os.Stat(definition)
			if err != nil {
				t.Fatal(err)
			}
			options := WriteOptions{Experimental: true}
			if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readNativeTest(t, filepath.Join(root, "manifest.json")), `"skills"`) {
				t.Fatal("shared skills were not selected")
			}
			if err := ValidateRepositoryWithOptions(root, true); err != nil {
				t.Fatal(err)
			}
			if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(definition)
			if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() {
				t.Fatal("import replaced the shared source")
			}
			if readNativeTest(t, definition) != importedSkill || readNativeTest(t, filepath.Join(root, "skills/fixture/data.bin")) != "\x00\xff" {
				t.Fatal("shared package bytes changed")
			}
			if manifest && readNativeTest(t, filepath.Join(root, "AGENTS.md")) != "Keep canonical policy.\n" {
				t.Fatal("canonical policy changed")
			}
			plan, err := PlanProjection("copilot", repo, ApplyOptions{Experimental: true})
			if err != nil || !plan.Applicable {
				t.Fatalf("shared skill plan: %v %+v", err, plan)
			}
		})
	}
}

func TestNativeCopilotBareSharedSkillImportPreservesPolicyAndMarker(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".agents")
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "Keep this policy.\n")
	writeFixture(t, filepath.Join(root, "skills/fixture/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(root, "skills/.gitkeep"), "")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Backup: true}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(root, "AGENTS.md")) != "Keep this policy.\n" {
		t.Fatal("existing policy was replaced")
	}
	if err := ValidateRepositoryWithOptions(root, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "native/com.github.copilot/profile.json", "native/com.github.copilot/import-report.json", "state/import-backups/skills.gitkeep.bak"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("new file is not private: %s %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "skills/.gitkeep")); !os.IsNotExist(err) {
		t.Fatal("marker remains selected")
	}
}

func TestNativeCopilotSharedSkillImportRefusals(t *testing.T) {
	for _, scenario := range []string{"stable", "draft1", "malformed", "null", "manifest-link", "unknown-tree", "skill-link", "core-link", "invalid-skill", "conflicting-policy", "duplicate-identity", "user-scope", "codex"} {
		t.Run(scenario, func(t *testing.T) {
			repo := t.TempDir()
			root := filepath.Join(repo, ".agents")
			writeFixture(t, filepath.Join(root, "skills/fixture/SKILL.md"), importedSkill)
			options := WriteOptions{Experimental: true, Force: true, Backup: true}
			vendor := "copilot"
			switch scenario {
			case "stable", "draft1":
				version := map[string]string{"stable": "1.0.0", "draft1": "1.1.0-draft.1"}[scenario]
				writeFixture(t, filepath.Join(root, "manifest.json"), `{"version":"`+version+`","profiles":[]}`)
			case "malformed":
				writeFixture(t, filepath.Join(root, "manifest.json"), "{")
			case "null":
				writeFixture(t, filepath.Join(root, "manifest.json"), "null")
			case "manifest-link", "core-link", "skill-link":
				name := map[string]string{"manifest-link": "manifest.json", "core-link": "AGENTS.md", "skill-link": "skills/fixture/escape"}[scenario]
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			case "unknown-tree":
				writeFixture(t, filepath.Join(root, "native/com.github.copilot/profile.json"), `{}`)
			case "invalid-skill":
				writeFixture(t, filepath.Join(root, "skills/fixture/SKILL.md"), "\xff")
			case "conflicting-policy":
				writeFixture(t, filepath.Join(root, "AGENTS.md"), "Keep canonical policy.\n")
				writeFixture(t, filepath.Join(repo, ".github/copilot-instructions.md"), "Different native policy.\n")
			case "duplicate-identity":
				writeFixture(t, filepath.Join(repo, ".github/skills/other/SKILL.md"), importedSkill)
			case "user-scope":
				options.Scope, options.NativeHome = "user", t.TempDir()
			case "codex":
				vendor = "codex"
			}
			before := nativeSkillTestSnapshot(t, repo)
			if err := ImportRepositoryWithOptions(vendor, repo, options); err == nil {
				t.Fatal("unsafe shared tree was imported")
			}
			if !reflect.DeepEqual(before, nativeSkillTestSnapshot(t, repo)) {
				t.Fatal("refusal changed source, policy, or backups")
			}
		})
	}
}
