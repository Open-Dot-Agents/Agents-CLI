package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotGitHubReferenceImport(t *testing.T) {
	for _, rootBody := range []string{"", "Root policy.\n", "Root policy.\n@root-policy.md\n"} {
		for _, targetLink := range []bool{false, true} {
			t.Run(rootBody+map[bool]string{false: "regular", true: "link"}[targetLink], func(t *testing.T) {
				source, target := t.TempDir(), t.TempDir()
				body := "GitHub policy.\n@policy.md\n"
				writeFixture(t, filepath.Join(source, ".github/copilot-instructions.md"), body)
				if rootBody != "" {
					writeFixture(t, filepath.Join(source, "AGENTS.md"), rootBody)
				}
				if err := ImportRepositoryWithOptions("copilot", source, WriteOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				core := rootBody
				if core == "" {
					core = "Use the selected native profile.\n"
				}
				if readNativeTest(t, filepath.Join(source, ".agents/AGENTS.md")) != core {
					t.Fatal("GitHub relative references moved into the root instruction body")
				}
				if readNativeTest(t, filepath.Join(source, ".agents/native/com.github.copilot/copilot-instructions.md")) != body {
					t.Fatal("GitHub body not preserved")
				}
				if err := os.CopyFS(filepath.Join(target, ".agents"), os.DirFS(filepath.Join(source, ".agents"))); err != nil {
					t.Fatal(err)
				}
				if targetLink {
					if err := os.Symlink(".agents/AGENTS.md", filepath.Join(target, "AGENTS.md")); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := ApplyProjection("copilot", target, ApplyOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				if readNativeTest(t, filepath.Join(target, ".github/copilot-instructions.md")) != body || readNativeTest(t, filepath.Join(target, "AGENTS.md")) != core {
					t.Fatal("projection changed instruction locations")
				}
				if err := ImportRepositoryWithOptions("copilot", target, WriteOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				before := instructionSnapshot(t, target)
				if err := ImportRepositoryWithOptions("copilot", target, WriteOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, target)) {
					t.Fatal("repeated import changed instruction sources")
				}
			})
		}
	}
}

func TestNativeCopilotGitHubReferenceLegacyLinkRefusal(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Legacy imported GitHub body.\n@policy.md\n")
	writeFixture(t, filepath.Join(repo, ".agents/native/com.github.copilot/profile.json"), `{"namespace":"com.github.copilot","harness_version":"=1.0.83","scope":"project","required":false,"artifacts":[]}`)
	if err := os.Symlink(".agents/AGENTS.md", filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, repo)
	for _, force := range []bool{false, true} {
		_, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Force: force, Adopt: true, Backup: true})
		if err == nil || !strings.Contains(err.Error(), "reference base") {
			t.Fatal("ambiguous legacy reference base accepted", err)
		}
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("refusal changed files")
	}
}

func TestNativeCopilotGitHubReferenceExistingCoreAndSource(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing canonical policy.\n")
	body := "GitHub policy.\n@policy.md\n"
	writeFixture(t, filepath.Join(repo, ".github/copilot-instructions.md"), body)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(repo, ".agents/AGENTS.md")) != "Existing canonical policy.\n" {
		t.Fatal("import changed existing core policy")
	}
	dir := filepath.Join(repo, ".agents/native/com.github.copilot")
	var profile nativeProfile
	if err := nativeDecodePolicy(filepath.Join(dir, "profile.json"), &profile); err != nil {
		t.Fatal(err)
	}
	for i := range profile.Artifacts {
		if profile.Artifacts[i].Kind == "instructions" {
			profile.Artifacts[i].Source = "policy/custom.md"
		}
	}
	writeFixture(t, filepath.Join(dir, "policy/custom.md"), body)
	if err := os.Remove(filepath.Join(dir, "copilot-instructions.md")); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "profile.json"), string(data))
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "copilot-instructions.md")); !os.IsNotExist(err) {
		t.Fatal("reimport added a duplicate source")
	}
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Adopt: true}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, ".github/copilot-instructions.md"), "Conflicting policy.\n@other.md\n")
	before := instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
		t.Fatal("force replaced an existing native policy")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("failed import changed canonical files")
	}
}

func TestNativeCopilotGitHubReferenceImportRefusals(t *testing.T) {
	for _, kind := range []string{"legacy-core", "native-root", "invalid-utf8", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			repo := t.TempDir()
			writeFixture(t, filepath.Join(repo, ".agents-import.lock"), "")
			path := filepath.Join(repo, ".github/copilot-instructions.md")
			writeFixture(t, path, "GitHub policy.\n@policy.md\n")
			switch kind {
			case "legacy-core", "native-root":
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing policy.\n@policy.md\n")
				artifacts := "[]"
				if kind == "native-root" {
					artifacts = `[{"kind":"agent-instructions","name":"AGENTS.md","source":"root.md"}]`
					writeFixture(t, filepath.Join(repo, "AGENTS.md"), "Separate root.\n")
					writeFixture(t, filepath.Join(repo, ".agents/native/com.github.copilot/root.md"), "Separate root.\n")
				}
				writeFixture(t, filepath.Join(repo, ".agents/native/com.github.copilot/profile.json"), `{"namespace":"com.github.copilot","harness_version":"=1.0.83","scope":"project","required":false,"artifacts":`+artifacts+`}`)
			case "invalid-utf8":
				writeFixture(t, path, "@policy.md\n\xff")
			case "directory", "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					outside := filepath.Join(t.TempDir(), "external.md")
					writeFixture(t, outside, "External policy.\n")
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := instructionSnapshot(t, repo)
			for _, force := range []bool{false, true} {
				if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: force, Backup: true}); err == nil {
					t.Fatal("unsafe import accepted")
				}
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
				t.Fatal("failed import changed files")
			}
		})
	}
}
