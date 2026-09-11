package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativePreservesCanonicalInstructionLink(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			repo := initialInstructionFixture(t)
			if _, err := ApplyProjection(vendor, repo, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
			link := filepath.Join(repo, "AGENTS.md")
			before, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			options := ApplyOptions{Experimental: true}
			for _, text := range []string{"Canonical instructions.\n", "Updated canonical instructions.\n"} {
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), text)
				plan, err := ApplyProjection(vendor, repo, options)
				if err != nil {
					t.Fatal(err)
				}
				after, err := os.Lstat(link)
				if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink == 0 {
					t.Fatal("native projection replaced canonical link")
				}
				if readNativeTest(t, link) != text {
					t.Fatal("canonical update not visible")
				}
				for _, action := range plan.Actions {
					if action.Path == link || action.Path == "AGENTS.md" {
						t.Fatal("native plan owns compatibility link", action)
					}
				}
				if _, err := os.Stat(filepath.Join(repo, ".github/copilot-instructions.md")); !os.IsNotExist(err) {
					t.Fatal("duplicate Copilot instructions created")
				}
			}
			plan, err := PlanProjection(vendor, repo, options)
			if err != nil || len(plan.Actions) != 0 {
				t.Fatal("native link plan is not idempotent", plan, err)
			}
		})
	}
}

func TestNativeCanonicalLinkDoesNotAllowDuplicateArtifacts(t *testing.T) {
	repo := nativeFixture(t, "project", "model = 'fixture'\n")
	if err := os.Symlink(".agents/AGENTS.md", filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, ".agents/native/com.openai.codex")
	writeFixture(t, filepath.Join(dir, "instructions.md"), "Test instructions.\n")
	profile := nativeProfile{Namespace: "com.openai.codex", HarnessVersion: "=0.154.0", Scope: "project", Required: true,
		Artifacts: []nativeArtifact{{Kind: "config", Source: "config.toml"}, {Kind: "instructions", Source: "instructions.md"}}}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "profile.json"), string(data))
	before := instructionSnapshot(t, repo)
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Force: true, Adopt: true}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate native instructions accepted", err)
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("duplicate refusal changed files")
	}
}

func nativeInstructionMigrationFixture(t *testing.T, vendor string) (string, string, string, ApplyOptions) {
	t.Helper()
	repo := initialInstructionFixture(t)
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
	options := ApplyOptions{Experimental: true}
	if _, err := ApplyProjection(vendor, repo, options); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "AGENTS.md")
	path, _, err := nativeTargetPath(vendor, "project", repo, nativeArtifact{Kind: "instructions"})
	if err != nil {
		t.Fatal(err)
	}
	if vendor == "codex" {
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(".agents/AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	state, err := nativeStatePath(vendor, "project", filepath.Join(repo, ".agents"), "")
	if err != nil {
		t.Fatal(err)
	}
	return repo, path, state, options
}

func TestNativeCanonicalLinkOwnershipMigration(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			repo, path, state, options := nativeInstructionMigrationFixture(t, vendor)
			link := filepath.Join(repo, "AGENTS.md")
			before, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			options.Backup = true
			if _, err := ApplyProjection(vendor, repo, options); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(link)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("migration replaced canonical link", err)
			}
			if vendor == "copilot" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("owned duplicate survived migration", err)
				}
			}
			var ownership nativeRegistry
			if err := nativeDecodePolicy(state, &ownership); err != nil {
				t.Fatal(err)
			}
			if len(ownership.Settings) != 0 {
				t.Fatal("instruction file ownership survived migration", ownership)
			}
			plan, err := PlanProjection(vendor, repo, options)
			if err != nil || len(plan.Actions) != 0 {
				t.Fatal("migration was not idempotent", plan, err)
			}
		})
	}
}

func TestNativeCanonicalLinkDoesNotStealOrRemoveForeignInstructions(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			repo, path, state, options := nativeInstructionMigrationFixture(t, vendor)
			var ownership nativeRegistry
			if err := nativeDecodePolicy(state, &ownership); err != nil {
				t.Fatal(err)
			}
			key := nativeKey(path, "")
			owner := ownership.Settings[key]
			owner.Source = filepath.Join(t.TempDir(), ".agents")
			ownership.Settings[key] = owner
			data, err := json.MarshalIndent(ownership, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, state, string(data)+"\n")
			before := instructionSnapshot(t, repo)
			options.Force, options.Adopt = true, true
			_, err = ApplyProjection(vendor, repo, options)
			if vendor == "codex" && (err == nil || !strings.Contains(err.Error(), "another source")) {
				t.Fatal("foreign instruction ownership was released", err)
			}
			if vendor == "copilot" && err != nil {
				t.Fatal("unrelated foreign instructions blocked canonical link", err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
				t.Fatal("foreign ownership or instructions changed")
			}
		})
	}
}

func TestNativeCanonicalLinkRefusesEditedCopilotDuplicate(t *testing.T) {
	repo, path, _, options := nativeInstructionMigrationFixture(t, "copilot")
	writeFixture(t, path, "User instructions added after projection.\n")
	before := instructionSnapshot(t, repo)
	options.Force, options.Adopt, options.Backup = true, true, true
	if _, err := ApplyProjection("copilot", repo, options); err == nil {
		t.Fatal("force removed edited Copilot instructions")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("refusal changed files")
	}
}

func TestNativeCanonicalLinkMigrationRollback(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			repo, _, _, options := nativeInstructionMigrationFixture(t, vendor)
			options.Backup = true
			build, err := buildNativeProjection(vendor, repo, options)
			if err != nil || !build.plan.Applicable {
				t.Fatal(build.plan, err)
			}
			before := instructionSnapshot(t, repo)
			writes := 0
			err = nativeRunTransaction(build.changes, func(stage string, index int) error {
				if stage == "after-write" {
					writes++
					if index == len(build.changes)-1 {
						return fmt.Errorf("injected ownership migration failure")
					}
				}
				return nil
			})
			if err == nil || writes != len(build.changes) {
				t.Fatal("failure did not occur after all writes", err, writes)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
				t.Fatal("rollback changed instructions, ownership, or backups")
			}
		})
	}
}

func TestNativeImportApplyImportPreservesCanonicalLink(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			repo := initialInstructionFixture(t)
			if _, err := ApplyProjection(vendor, repo, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
			link := filepath.Join(repo, "AGENTS.md")
			before, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"First canonical instructions.\n", "Updated canonical instructions.\n"} {
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), text)
				if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := ApplyProjection(vendor, repo, ApplyOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				snapshot := instructionSnapshot(t, repo)
				if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true}); err != nil {
					t.Fatal(err)
				}
				if nativeHash(snapshot) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("reimport changed files")
				}
				after, err := os.Lstat(link)
				if err != nil || !os.SameFile(before, after) || readNativeTest(t, link) != text {
					t.Fatal("import replaced canonical instructions or link", err)
				}
			}
		})
	}
}

func TestNativeInstructionImportRefusesUnsafeLinks(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		for _, target := range []string{"external", "broken", "cycle", "indirect"} {
			t.Run(vendor+"/"+target, func(t *testing.T) {
				repo := initialInstructionFixture(t)
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[]}`)
				outside := filepath.Join(t.TempDir(), "AGENTS.md")
				writeFixture(t, outside, "External instructions must stay external.\n")
				destination := outside
				switch target {
				case "broken":
					destination = "missing.md"
				case "cycle":
					destination = "AGENTS.md"
				case "indirect":
					canonical := filepath.Join(repo, ".agents/AGENTS.md")
					if err := os.Remove(canonical); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, canonical); err != nil {
						t.Fatal(err)
					}
					destination = ".agents/AGENTS.md"
				}
				if err := os.Symlink(destination, filepath.Join(repo, "AGENTS.md")); err != nil {
					t.Fatal(err)
				}
				before := instructionSnapshot(t, repo)
				if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
					t.Fatal("native import followed unsafe instruction link")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) || readNativeTest(t, outside) != "External instructions must stay external.\n" {
					t.Fatal("unsafe import changed files")
				}
			})
		}
	}
}

func TestNativeInstructionImportRefusesConflictingCopilotCopy(t *testing.T) {
	repo, path, _, _ := nativeInstructionMigrationFixture(t, "copilot")
	// Establish the import lock with an equivalent import before testing the
	// refusal. The persistent lock file is coordination state, not configuration.
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "Additional native instructions.\n")
	before := instructionSnapshot(t, repo)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Force: true, Backup: true}); err == nil {
		t.Fatal("native import replaced canonical instructions with a conflicting native copy")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
		t.Fatal("conflicting import changed files")
	}
}
