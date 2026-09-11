package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotRootInstructionImport(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		repo := t.TempDir()
		body := "Keep the fixture policy.\n"
		writeFixture(t, filepath.Join(repo, "AGENTS.md"), body)
		if duplicate {
			writeFixture(t, filepath.Join(repo, ".github/copilot-instructions.md"), body)
		}
		if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
			t.Fatal(err)
		}
		if readNativeTest(t, filepath.Join(repo, ".agents/AGENTS.md")) != body || readNativeTest(t, filepath.Join(repo, "AGENTS.md")) != body {
			t.Fatal("root instructions were lost or changed")
		}
		if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Adopt: duplicate}); err != nil {
			t.Fatal(err)
		}
		if readNativeTest(t, filepath.Join(repo, ".github/copilot-instructions.md")) != body {
			t.Fatal("projected instructions changed")
		}
		before := instructionSnapshot(t, repo)
		if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
			t.Fatal(err)
		}
		if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
			t.Fatal("reimport changed files")
		}
	}
}

func TestNativeCopilotRootInstructionImportRefusals(t *testing.T) {
	for _, kind := range []string{"canonical-conflict", "directory"} {
		for _, force := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/normal", true: "/force"}[force], func(t *testing.T) {
				repo := t.TempDir()
				writeFixture(t, filepath.Join(repo, ".agents-import.lock"), "")
				body := "Root policy.\n"
				if kind == "directory" {
					if err := os.Mkdir(filepath.Join(repo, "AGENTS.md"), 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					writeFixture(t, filepath.Join(repo, "AGENTS.md"), body)
				}
				if kind == "directory" {
					writeFixture(t, filepath.Join(repo, ".github/copilot-instructions.md"), "Other policy.\n")
				}
				if kind == "canonical-conflict" {
					writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
					writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing canonical policy.\n")
				}
				before := instructionSnapshot(t, repo)
				err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: force, Backup: true})
				if err == nil {
					t.Fatal("unsafe import accepted")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("refusal changed files")
				}
			})
		}
	}
}

func TestNativeCopilotUserImportIgnoresProjectRootInstructions(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(repo, "AGENTS.md"), "@project-policy.md\n")
	writeFixture(t, filepath.Join(home, "copilot-instructions.md"), "User policy.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(repo, ".agents/AGENTS.md")) == "@project-policy.md\n" {
		t.Fatal("user import consumed project instructions")
	}
}

func TestNativeCopilotRootInstructionsRefuseStaleProjection(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, "AGENTS.md"), "Original policy.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "New policy.\n")
	before := instructionSnapshot(t, repo)
	for _, force := range []bool{false, true} {
		if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Force: force, Adopt: true, Backup: true}); err == nil || !strings.Contains(err.Error(), "distinct project instructions") {
			t.Fatal("stale root policy accepted", err)
		}
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("refusal changed policy or ownership")
	}
}
