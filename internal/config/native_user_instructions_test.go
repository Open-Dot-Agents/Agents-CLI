package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotUserInstructionsKeepDeclaredSource(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, home := t.TempDir(), t.TempDir()
	root := filepath.Join(repo, ".agents")
	writeFixture(t, filepath.Join(root, "manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "Keep project policy.\n")
	profile := filepath.Join(root, "native/com.github.copilot/profile.json")
	writeFixture(t, profile, `{"namespace":"com.github.copilot","harness_version":"=1.0.83","scope":"user","required":true,"artifacts":[{"kind":"instructions","source":"policy/local.md","name":"kept"}]}`)
	source := filepath.Join(root, "native/com.github.copilot/policy/local.md")
	writeFixture(t, source, "User policy.\n@policy.md\n")
	writeFixture(t, filepath.Join(home, "policy.md"), "External user reference.\n")
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, root)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("reimport changed the declared instruction source or profile")
	}
	if _, err := os.Stat(filepath.Join(root, "native/com.github.copilot/copilot-instructions.md")); !os.IsNotExist(err) {
		t.Fatal("reimport created a second instruction source")
	}
	plan, err := PlanProjection("copilot", repo, options)
	if err != nil || !plan.Applicable {
		t.Fatalf("reimported plan: %v %+v", err, plan)
	}
	if !strings.Contains(strings.Join(plan.Native.RequiredActions, " "), "referenced user instruction files") {
		t.Fatal("external reference requirement was omitted")
	}
	writeFixture(t, filepath.Join(home, "copilot-instructions.md"), "Conflicting runtime policy.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true, Backup: true}); err == nil {
		t.Fatal("forced reimport replaced declared policy")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("refusal changed source or backups")
	}
}

func TestNativeCodexUserInstructionsKeepDeclaredSource(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo, home := t.TempDir(), t.TempDir()
	root := filepath.Join(repo, ".agents")
	writeFixture(t, filepath.Join(root, "manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "Project policy.\n")
	writeFixture(t, filepath.Join(root, "native/com.openai.codex/profile.json"), `{"namespace":"com.openai.codex","harness_version":"=0.154.0","scope":"user","required":true,"artifacts":[{"kind":"instructions","source":"policy/local.md"}]}`)
	writeFixture(t, filepath.Join(root, "native/com.openai.codex/policy/local.md"), "User policy.\n")
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, root)
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("Codex reimport changed declared source")
	}
}

func TestNativeUserInstructionImportRefusesDuplicateTarget(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	root := filepath.Join(repo, ".agents")
	writeFixture(t, filepath.Join(root, "manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "Project policy.\n")
	writeFixture(t, filepath.Join(root, "native/com.github.copilot/profile.json"), `{"namespace":"com.github.copilot","harness_version":"=1.0.83","scope":"user","required":true,"artifacts":[{"kind":"instructions","source":"first.md"},{"kind":"instructions","source":"second.md"}]}`)
	for _, name := range []string{"first.md", "second.md"} {
		writeFixture(t, filepath.Join(root, "native/com.github.copilot", name), "User policy.\n")
	}
	writeFixture(t, filepath.Join(home, "copilot-instructions.md"), "User policy.\n")
	before := instructionSnapshot(t, root)
	err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true, Backup: true})
	if err == nil || !strings.Contains(err.Error(), "duplicate native user instruction target") {
		t.Fatal("duplicate user target was imported", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("duplicate refusal changed canonical files")
	}
}

func TestNativeCopilotUserInstructionOwnershipAndRemoval(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	repo, other, home := t.TempDir(), t.TempDir(), t.TempDir()
	path := filepath.Join(home, "copilot-instructions.md")
	writeFixture(t, path, "User policy.\n@policy.md\n")
	writeFixture(t, filepath.Join(home, "policy.md"), "External reference.\n")
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Adopt: true}
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("existing file mode changed", err)
	}
	if err := os.CopyFS(filepath.Join(other, ".agents"), os.DirFS(filepath.Join(repo, ".agents"))); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		return nativeHash([]any{instructionSnapshot(t, repo), instructionSnapshot(t, home), instructionSnapshot(t, state)})
	}
	before := snapshot()
	options.Force = true
	if _, err := ApplyProjection("copilot", other, options); err == nil {
		t.Fatal("foreign source acquired user instruction ownership")
	}
	if snapshot() != before {
		t.Fatal("foreign refusal changed files")
	}
	homeBefore := instructionSnapshot(t, home)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(homeBefore) != nativeHash(instructionSnapshot(t, home)) {
		t.Fatal("project apply changed user files")
	}
	profilePath := filepath.Join(repo, ".agents/native/com.github.copilot/profile.json")
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
	build, err := buildNativeProjection("copilot", repo, options)
	if err != nil || !build.plan.Applicable {
		t.Fatal(build.plan, err)
	}
	before = snapshot()
	writes := 0
	err = nativeRunTransaction(build.changes, func(stage string, index int) error {
		if stage == "after-write" {
			writes++
			if index == len(build.changes)-1 {
				return fmt.Errorf("injected final user instruction removal failure")
			}
		}
		return nil
	})
	if err == nil || writes != len(build.changes) {
		t.Fatal("removal failure was not injected", err)
	}
	if snapshot() != before {
		t.Fatal("removal rollback changed instructions, ownership, or backups")
	}
	if _, err := ApplyProjection("copilot", repo, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned instruction file remains")
	}
	if readNativeTest(t, filepath.Join(home, "policy.md")) != "External reference.\n" {
		t.Fatal("external reference changed")
	}
}
