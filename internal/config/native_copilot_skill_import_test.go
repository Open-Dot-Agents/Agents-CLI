package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const importedSkill = "---\nname: fixture\ndescription: Fixture.\n---\nUse the fixture.\n"

func TestNativeCopilotProjectSkillImport(t *testing.T) {
	for _, origin := range []string{".github", ".claude"} {
		t.Run(origin, func(t *testing.T) {
			repo := t.TempDir()
			packageRoot := filepath.Join(repo, origin, "skills", "fixture")
			writeFixture(t, filepath.Join(packageRoot, "SKILL.md"), importedSkill)
			writeFixture(t, filepath.Join(packageRoot, "scripts", "probe.sh"), "#!/bin/sh\nexit 0\n")
			if err := os.Chmod(filepath.Join(packageRoot, "scripts", "probe.sh"), 0700); err != nil {
				t.Fatal(err)
			}
			options := WriteOptions{Experimental: true}
			if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			if readNativeTest(t, filepath.Join(repo, ".agents/skills/fixture/SKILL.md")) != importedSkill {
				t.Fatal("definition not imported")
			}
			if info, err := os.Stat(filepath.Join(repo, ".agents/skills/fixture/scripts/probe.sh")); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("executable asset lost")
			}
			if !strings.Contains(readNativeTest(t, filepath.Join(repo, ".agents/manifest.json")), `"skills"`) {
				t.Fatal("profile not selected")
			}
			if err := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); err != nil {
				t.Fatal(err)
			}
			if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
				t.Fatalf("repeat import: %v", err)
			}
		})
	}
}

func TestNativeCopilotProjectSkillsAdditiveAndIdenticalImport(t *testing.T) {
	repo := t.TempDir()
	for _, origin := range []string{".github", ".claude"} {
		writeFixture(t, filepath.Join(repo, origin, "skills/fixture/SKILL.md"), importedSkill)
		writeFixture(t, filepath.Join(repo, origin, "skills/fixture/data.bin"), "\x00\xff\x01")
	}
	options := WriteOptions{Experimental: true}
	if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(repo, ".agents/skills/fixture/data.bin")) != "\x00\xff\x01" {
		t.Fatal("binary changed")
	}
	writeFixture(t, filepath.Join(repo, ".claude/skills/second/SKILL.md"), strings.ReplaceAll(importedSkill, "name: fixture", "name: second"))
	if err := ImportRepositoryWithOptions("copilot", repo, options); err != nil {
		t.Fatalf("disjoint import failed: %v", err)
	}
	if readNativeTest(t, filepath.Join(repo, ".agents/skills/fixture/SKILL.md")) != importedSkill {
		t.Fatal("existing package changed")
	}
	if !strings.Contains(readNativeTest(t, filepath.Join(repo, ".agents/native/com.github.copilot/import-report.json")), ".claude/skills/second") {
		t.Fatal("new source not reported")
	}
}

func TestNativeCopilotProjectSkillsDoNotImportOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	repo, home := filepath.Join(parent, "repo"), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", home)
	writeFixture(t, filepath.Join(parent, ".github/skills/parent/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(home, "skills/user/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(repo, ".github/skills/local/SKILL.md"), importedSkill)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, ".agents/skills"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "local" {
		t.Fatal("import crossed root scope")
	}
}

func TestNativeCopilotProjectSkillApplyRefusesShadowing(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, ".github/skills/fixture/SKILL.md"), importedSkill)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(repo, ".agents/skills/fixture/SKILL.md")
	writeFixture(t, canonical, importedSkill+"Updated canonical body.\n")
	before := nativeSkillTestSnapshot(t, repo)
	options := ApplyOptions{Experimental: true, Force: true, Backup: true}
	plan, err := PlanProjection("copilot", repo, options)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable || len(plan.Actions) != 0 {
		t.Fatal("native skill could shadow canonical content")
	}
	if !strings.Contains(strings.Join(plan.Diagnostics, " "), "project skill discovery conflict") {
		t.Fatal("missing shadowing diagnostic")
	}
	if _, err := ApplyProjection("copilot", repo, options); err == nil {
		t.Fatal("force replaced or ignored an unowned native skill")
	}
	after := nativeSkillTestSnapshot(t, repo)
	if len(before) != len(after) {
		t.Fatal("refusal wrote files")
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("refusal changed %s", path)
		}
	}
}

func TestNativeCopilotProjectSkillImportReplacesEmptyCanonicalMarker(t *testing.T) {
	for _, backup := range []bool{false, true} {
		repo := t.TempDir()
		writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Keep instructions.\n")
		writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
		marker := filepath.Join(repo, ".agents/skills/.gitkeep")
		writeFixture(t, marker, "")
		writeFixture(t, filepath.Join(repo, ".github/skills/.gitkeep"), "")
		writeFixture(t, filepath.Join(repo, ".github/skills/fixture/SKILL.md"), importedSkill)
		if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: backup, Backup: backup}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("selected skills retain invalid root marker")
		}
		if _, err := os.Stat(filepath.Join(repo, ".github/skills/.gitkeep")); err != nil {
			t.Fatal("native source marker changed")
		}
		if err := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); err != nil {
			t.Fatal(err)
		}
		backupPath := filepath.Join(repo, ".agents/state/import-backups/skills.gitkeep.bak")
		info, err := os.Stat(backupPath)
		if backup {
			if err != nil || info.Size() != 0 || info.Mode().Perm() != 0600 {
				t.Fatal("missing private marker backup")
			}
		} else if !os.IsNotExist(err) {
			t.Fatal("unexpected marker backup")
		}
	}
}

func TestNativeSkillMarkerRemovalRejectsConcurrentContent(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "skills/.gitkeep")
	writeFixture(t, marker, "")
	before, err := nativeReadSnapshot(marker)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, marker, "New user content.\n")
	if _, err := nativeMergeImport(root, []nativeChange{{path: marker, remove: true, before: &before}}, false); err == nil {
		t.Fatal("changed marker could be removed")
	}
}

func TestNativeCopilotProjectSkillImportConflicts(t *testing.T) {
	for _, scenario := range []string{"different-definition", "extra-asset", "different-folder", "canonical-extra", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			repo := t.TempDir()
			first := filepath.Join(repo, ".github/skills/fixture")
			second := filepath.Join(repo, ".claude/skills/fixture")
			if scenario == "different-folder" {
				second = filepath.Join(repo, ".claude/skills/other")
			}
			if scenario == "canonical-extra" {
				second = filepath.Join(repo, ".agents/skills/fixture")
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Keep canonical policy.\n")
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["skills"]}`)
			}
			writeFixture(t, filepath.Join(first, "SKILL.md"), importedSkill)
			writeFixture(t, filepath.Join(second, "SKILL.md"), importedSkill)
			switch scenario {
			case "different-definition":
				writeFixture(t, filepath.Join(second, "SKILL.md"), importedSkill+"Different body.\n")
			case "extra-asset", "canonical-extra":
				writeFixture(t, filepath.Join(second, "extra.txt"), "Do not combine packages.\n")
			case "symlink":
				if err := os.Symlink(filepath.Join(first, "SKILL.md"), filepath.Join(second, "link.md")); err != nil {
					t.Fatal(err)
				}
			}
			before := nativeSkillTestSnapshot(t, repo)
			if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
				t.Fatal("ambiguous project skills imported")
			}
			after := nativeSkillTestSnapshot(t, repo)
			if len(after) != len(before) {
				t.Fatal("refusal wrote files")
			}
			for name, content := range before {
				if after[name] != content {
					t.Fatalf("refusal changed %s", name)
				}
			}
		})
	}
}

func nativeSkillTestSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() == ".agents-import.lock" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			files[path] = "link:" + target
			return err
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
