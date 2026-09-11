package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotAgentInstructionFiles(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	definitions := map[string]string{
		"AGENTS.md":         "Root policy.\n@policy.md\n",
		"CLAUDE.md":         "Other agent policy.\n@policy.md\n",
		".claude/CLAUDE.md": "Directory policy.\n@policy.md\n",
		"GEMINI.md":         "Keep literal @policy.md here.\n",
	}
	writeFixture(t, filepath.Join(source, ".github/copilot-instructions.md"), "Portable policy.\n")
	for name, body := range definitions {
		writeFixture(t, filepath.Join(source, name), body)
	}
	writeFixture(t, filepath.Join(source, "policy.md"), "External referenced content.\n")
	if err := ImportRepositoryWithOptions("copilot", source, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	for name, body := range definitions {
		if readNativeTest(t, filepath.Join(source, name)) != body || readNativeTest(t, filepath.Join(source, ".agents/native/com.github.copilot/agent-instructions", name)) != body {
			t.Fatal("native instruction bytes lost", name)
		}
	}
	if _, err := os.Stat(filepath.Join(source, ".agents/native/com.github.copilot/policy.md")); !os.IsNotExist(err) {
		t.Fatal("external reference copied", err)
	}
	if err := os.CopyFS(filepath.Join(target, ".agents"), os.DirFS(filepath.Join(source, ".agents"))); err != nil {
		t.Fatal(err)
	}
	options := ApplyOptions{Experimental: true}
	if _, err := ApplyProjection("copilot", target, options); err != nil {
		t.Fatal(err)
	}
	for name, body := range definitions {
		if readNativeTest(t, filepath.Join(target, name)) != body {
			t.Fatal("native location changed", name)
		}
	}
	if err := ImportRepositoryWithOptions("copilot", target, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, target)
	if err := ImportRepositoryWithOptions("copilot", target, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, target)) {
		t.Fatal("reimport changed files")
	}
	profilePath := filepath.Join(target, ".agents/native/com.github.copilot/profile.json")
	var profile nativeProfile
	if err := nativeDecodePolicy(profilePath, &profile); err != nil {
		t.Fatal(err)
	}
	profile.Artifacts = []nativeArtifact{}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, profilePath, string(data))
	options.Backup = true
	build, err := buildNativeProjection("copilot", target, options)
	if err != nil || !build.plan.Applicable {
		t.Fatal(build.plan, err)
	}
	before = instructionSnapshot(t, target)
	writes := 0
	err = nativeRunTransaction(build.changes, func(stage string, index int) error {
		if stage == "after-write" {
			writes++
			if index == len(build.changes)-1 {
				return fmt.Errorf("injected final instruction removal failure")
			}
		}
		return nil
	})
	if err == nil || writes != len(build.changes) {
		t.Fatal("rollback injection failed", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, target)) {
		t.Fatal("rollback lost native instructions or backups")
	}
	if _, err = ApplyProjection("copilot", target, options); err != nil {
		t.Fatal(err)
	}
	for name, body := range definitions {
		if _, err := os.Stat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatal("deselected native instructions remain", name, err)
		}
		if readNativeTest(t, filepath.Join(target, name+".bak")) != body {
			t.Fatal("backup content lost", name)
		}
	}
}

func TestNativeAgentInstructionRegistryScope(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md"} {
		if _, _, err := nativeTargetPath("copilot", "project", t.TempDir(), nativeArtifact{Kind: "agent-instructions", Name: name}); err != nil {
			t.Fatal(err)
		}
		for _, vendor := range []string{"codex", "copilot"} {
			if _, _, err := nativeTargetPath(vendor, "user", t.TempDir(), nativeArtifact{Kind: "agent-instructions", Name: name}); err == nil {
				t.Fatal("agent instruction mapping escaped project scope")
			}
		}
	}
	for _, name := range []string{"../AGENTS.md", "/AGENTS.md", "other/AGENTS.md", ".git/config", ".github/copilot-instructions.md"} {
		if _, _, err := nativeTargetPath("copilot", "project", t.TempDir(), nativeArtifact{Kind: "agent-instructions", Name: name}); err == nil {
			t.Fatal("arbitrary output path accepted", name)
		}
	}
}

func TestNativeAgentInstructionsPreservePortablePolicy(t *testing.T) {
	repo := t.TempDir()
	manifest := `{"version":"1.1.0-draft.2","profiles":[],"requires":["mcp.envRef"]}`
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), manifest)
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing mandatory policy.\n")
	writeFixture(t, filepath.Join(repo, "AGENTS.md"), "@project-policy.md\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(repo, ".agents/AGENTS.md")) != "Existing mandatory policy.\n" {
		t.Fatal("native root instructions replaced portable policy")
	}
	var m manifestDocument
	if err := nativeDecodePolicy(filepath.Join(repo, ".agents/manifest.json"), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Requires) != 1 || m.Requires[0] != "mcp.envRef" {
		t.Fatal("required capability lost", m)
	}
	before := instructionSnapshot(t, repo)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Force: true, Adopt: true}); err == nil {
		t.Fatal("native artifact bypassed mandatory policy")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("policy refusal changed files")
	}
}

func TestNativeAgentInstructionUnsafeSources(t *testing.T) {
	for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md"} {
		for _, kind := range []string{"symlink", "directory", "encoding"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				repo := t.TempDir()
				writeFixture(t, filepath.Join(repo, ".agents-import.lock"), "")
				path := filepath.Join(repo, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "symlink":
					outside := filepath.Join(t.TempDir(), "policy.md")
					writeFixture(t, outside, "External policy.")
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "encoding":
					writeFixture(t, path, string([]byte{0xff}))
				}
				before := instructionSnapshot(t, repo)
				if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
					t.Fatal("unsafe instruction source accepted")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("refusal changed source")
				}
			})
		}
	}
}

func TestNativeAgentInstructionDuplicateAssignments(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, "CLAUDE.md"), "Native policy.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".agents/native/com.github.copilot/profile.json")
	var profile nativeProfile
	if err := nativeDecodePolicy(path, &profile); err != nil {
		t.Fatal(err)
	}
	profile.Artifacts = append(profile.Artifacts, nativeArtifact{Kind: "agent-instructions", Name: "CLAUDE.md", Source: "second.md"})
	writeFixture(t, filepath.Join(filepath.Dir(path), "second.md"), "Another body.\n")
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(data))
	before := instructionSnapshot(t, repo)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Force: true, Adopt: true}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate projection accepted", err)
	}
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate import accepted", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("duplicate refusal changed files")
	}
}

func TestNativeAgentInstructionImportRetainsCustomSource(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, "CLAUDE.md"), "Native policy.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".agents/native/com.github.copilot")
	var profile nativeProfile
	if err := nativeDecodePolicy(filepath.Join(dir, "profile.json"), &profile); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, profile.Artifacts[0].Source)
	profile.Artifacts[0].Source = "custom.md"
	if err := os.Rename(old, filepath.Join(dir, "custom.md")); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "profile.json"), string(data))
	before := instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("reimport changed custom source routing")
	}
	writeFixture(t, filepath.Join(repo, "CLAUDE.md"), "Changed native policy.\n")
	before = instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
		t.Fatal("force replaced existing canonical native policy")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("refusal changed custom source or backups")
	}
}
