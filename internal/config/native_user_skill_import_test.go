package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeUserSkillImportRefusesPartialPackageMerge(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, vendor := range []string{"copilot", "codex"} {
		t.Run(vendor, func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			body := "---\nname: fixture\ndescription: Fixture skill.\n---\nUse this package.\n"
			writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["skills"]}`)
			writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing policy.\n")
			writeFixture(t, filepath.Join(repo, ".agents/skills/fixture/SKILL.md"), body)
			writeFixture(t, filepath.Join(repo, ".agents/skills/fixture/old.txt"), "Old asset.\n")
			writeFixture(t, filepath.Join(home, "skills/fixture/SKILL.md"), body)
			writeFixture(t, filepath.Join(home, "skills/fixture/new.txt"), "New asset.\n")
			writeFixture(t, filepath.Join(repo, ".agents-import.lock"), "")
			before := instructionSnapshot(t, repo)
			for _, force := range []bool{false, true} {
				if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: force, Backup: true}); err == nil {
					t.Fatal("import combined different complete skill packages")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("refusal changed canonical content")
				}
			}
		})
	}
}

func TestNativeUserSkillOwnedLifecycle(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex"} {
		t.Run(vendor, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			repo, source, target := t.TempDir(), t.TempDir(), t.TempDir()
			writeFixture(t, filepath.Join(source, "skills/fixture/SKILL.md"), importedSkill)
			writeFixture(t, filepath.Join(source, "skills/fixture/data.txt"), "Original asset.\n")
			importOptions := WriteOptions{Experimental: true, Scope: "user", NativeHome: source}
			if err := ImportRepositoryWithOptions(vendor, repo, importOptions); err != nil {
				t.Fatal(err)
			}
			before := instructionSnapshot(t, repo)
			if err := ImportRepositoryWithOptions(vendor, repo, importOptions); err != nil {
				t.Fatal(err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
				t.Fatal("identical reimport changed content")
			}
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}
			if _, err := ApplyProjection(vendor, repo, options); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, ".agents/skills/fixture/data.txt"), "Updated asset.\n")
			if _, err := ApplyProjection(vendor, repo, options); err != nil {
				t.Fatal(err)
			}
			if readNativeTest(t, filepath.Join(target, "skills/fixture/data.txt")) != "Updated asset.\n" {
				t.Fatal("owned package update failed")
			}
			writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
			options.Backup = true
			build, err := buildNativeProjection(vendor, repo, options)
			if err != nil || !build.plan.Applicable {
				t.Fatal(build.plan, err)
			}
			snapshot := func() string { return nativeHash([]any{instructionSnapshot(t, target), instructionSnapshot(t, state)}) }
			prior := snapshot()
			writes := 0
			err = nativeRunTransaction(build.changes, func(stage string, index int) error {
				if stage == "after-write" {
					writes++
					if index == len(build.changes)-1 {
						return fmt.Errorf("injected final user skill removal failure")
					}
				}
				return nil
			})
			if err == nil || writes != len(build.changes) || snapshot() != prior {
				t.Fatal("removal rollback failed", err)
			}
			if _, err := ApplyProjection(vendor, repo, options); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(target, "skills/fixture/SKILL.md")); !os.IsNotExist(err) {
				t.Fatal("owned skill remains")
			}
			statePath, err := nativeStatePath(vendor, "user", filepath.Join(repo, ".agents"), target)
			if err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(filepath.Dir(statePath), "backups", strings.TrimSuffix(filepath.Base(statePath), ".json"), "skills/fixture/SKILL.md.bak")
			if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("backup is not private", err)
			}
		})
	}
}

func TestNativeUserSkillImportRemovesEmptyMarker(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, home := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing policy.\n")
	writeFixture(t, filepath.Join(repo, ".agents/skills/.gitkeep"), "")
	writeFixture(t, filepath.Join(home, "skills/fixture/SKILL.md"), importedSkill)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Backup: true}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents/skills/.gitkeep")); !os.IsNotExist(err) {
		t.Fatal("empty marker retained beside selected packages")
	}
}

func TestNativeUserSkillApplyRefusesPartialPackageAdoption(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, vendor := range []string{"copilot", "codex"} {
		t.Run(vendor, func(t *testing.T) {
			repo, source, target := t.TempDir(), t.TempDir(), t.TempDir()
			writeFixture(t, filepath.Join(source, "skills/fixture/SKILL.md"), importedSkill)
			writeFixture(t, filepath.Join(source, "skills/fixture/new.txt"), "New asset.\n")
			if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(target, "skills/fixture/SKILL.md"), importedSkill)
			writeFixture(t, filepath.Join(target, "skills/fixture/old.txt"), "Old asset.\n")
			_, err := ApplyProjection(vendor, repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target, Adopt: true, Force: true})
			if err == nil {
				t.Fatal("adoption combined different complete skill packages")
			}
		})
	}
}

func TestNativeUserSkillBackupStaysOutsidePackage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, source, target := t.TempDir(), t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(source, "skills/fixture/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(source, "skills/fixture/data.txt"), "Original asset.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
		t.Fatal(err)
	}
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, ".agents/skills/fixture/data.txt"), "Updated asset.\n")
	options.Backup = true
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	options.Backup = false
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal("private backup changed the active package", err)
	}
	entries, err := os.ReadDir(filepath.Join(target, "skills/fixture"))
	if err != nil || len(entries) != 2 {
		t.Fatal("backup became a skill asset", err)
	}
}

func TestNativeUserSkillIdentityConflict(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, source, target := t.TempDir(), t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(source, "skills/fixture/SKILL.md"), importedSkill)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(source, "skills/other/SKILL.md"), importedSkill)
	before := instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source, Force: true}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatal("duplicate imported skill identity accepted", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("identity conflict changed canonical content")
	}
	writeFixture(t, filepath.Join(target, "skills/other/SKILL.md"), importedSkill)
	before = instructionSnapshot(t, target)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target, Force: true, Adopt: true}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatal("unowned native skill shadowing accepted", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, target)) {
		t.Fatal("identity conflict changed user files")
	}
}

func TestNativeUserSkillImportDoesNotSelectSystemPackages(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing policy.\n")
	writeFixture(t, filepath.Join(repo, ".agents/skills/.system/builtin/SKILL.md"), "External system skill.\n")
	writeFixture(t, filepath.Join(home, "skills/fixture/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(repo, ".agents-import.lock"), "")
	before := instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err == nil {
		t.Fatal("import selected a nonportable system package")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("refusal changed canonical files")
	}
}
