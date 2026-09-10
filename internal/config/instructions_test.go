package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initialInstructionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.0.0","profiles":[]}`)
	writeFixture(t, filepath.Join(root, ".agents/AGENTS.md"), "Canonical instructions.\n")
	writeFixture(t, filepath.Join(root, ".agents/skills/.gitkeep"), "")
	return root
}

func TestInitialInstructionLinkLifecycle(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude", "all"} {
		t.Run(vendor, func(t *testing.T) {
			root := initialInstructionFixture(t)
			before := instructionSnapshot(t, root)
			plan, err := PlanSync(vendor, root, ApplyOptions{})
			if err != nil || !plan.Applicable {
				t.Fatal(plan, err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("plan changed tree")
			}
			for _, p := range plan.Vendors {
				found := false
				for _, a := range p.Actions {
					found = found || (a.Operation == "create-link" && a.Path == "AGENTS.md")
				}
				if !found {
					t.Fatal("missing planned instruction link", p)
				}
			}
			if _, err := ApplySync(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if target, err := os.Readlink(filepath.Join(root, "AGENTS.md")); err != nil || target != ".agents/AGENTS.md" {
				t.Fatal("missing canonical link")
			}
			if vendor == "claude" || vendor == "all" {
				if readNativeTest(t, filepath.Join(root, "CLAUDE.md")) != "@AGENTS.md\n" {
					t.Fatal("missing initial Claude bridge")
				}
			}
			for _, p := range plan.Vendors {
				state, err := loadOwnership(root, p.Vendor)
				if err != nil || state.Links["AGENTS.md"] != ".agents/AGENTS.md" {
					t.Fatal("link ownership missing")
				}
			}
			writeFixture(t, filepath.Join(root, ".agents/AGENTS.md"), "Updated canonical instructions.\n")
			if readNativeTest(t, filepath.Join(root, "AGENTS.md")) != "Updated canonical instructions.\n" {
				t.Fatal("link does not follow canonical updates")
			}
			if _, err := ApplySync(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(root, ".agents/skills/.gitkeep"), "not an empty placeholder")
			if vendor != "claude" {
				if _, err := ApplySync(vendor, root, ApplyOptions{}); err == nil {
					t.Fatal("nonempty unselected skills content accepted")
				}
			}
		})
	}
}

func TestInitialInstructionLinkRollback(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "concurrent-root-change"}[changed], func(t *testing.T) {
			root := initialInstructionFixture(t)
			before := instructionSnapshot(t, root)
			prepared, plan, err := prepareSync("all", root, ApplyOptions{})
			if err != nil || !plan.Applicable {
				t.Fatal(plan, err)
			}
			writes := 0
			writer := func(path string, data []byte, mode fs.FileMode) error {
				writes++
				if writes == 2 {
					if changed {
						if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
							return err
						}
						if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Concurrent user content.\n"), 0600); err != nil {
							return err
						}
					}
					return errors.New("injected instruction transaction failure")
				}
				return atomicWrite(path, data, mode)
			}
			err = applyPreparedProjections(prepared, ApplyOptions{}, writer)
			if err == nil || !strings.Contains(err.Error(), "injected instruction transaction failure") {
				t.Fatal(err)
			}
			if changed {
				if readNativeTest(t, filepath.Join(root, "AGENTS.md")) != "Concurrent user content.\n" {
					t.Fatal("rollback removed concurrent content")
				}
				before[filepath.Join(root, "AGENTS.md")] = "file:Concurrent user content.\n"
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("rollback did not restore tree")
			}
		})
	}
}

func TestInitialInstructionLinkProtectsExistingFile(t *testing.T) {
	root := initialInstructionFixture(t)
	prepared, plan, err := prepareSync("all", root, ApplyOptions{})
	if err != nil || !plan.Applicable {
		t.Fatal(plan, err)
	}
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "User instructions.\n")
	before := instructionSnapshot(t, root)
	if err := applyPreparedProjections(prepared, ApplyOptions{Force: true}, atomicWrite); err == nil {
		t.Fatal("late root file replaced")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("late file refusal changed tree")
	}
	if _, err := ApplySync("all", root, ApplyOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(root, "AGENTS.md")) != "User instructions.\n" {
		t.Fatal("existing user instructions replaced")
	}
}

func TestScopedCanonicalInstructionLinks(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.0.0","profiles":[]}`)
			writeFixture(t, filepath.Join(root, ".agents/AGENTS.md"), "Root instructions.\n")
			if err := os.Symlink(".agents/AGENTS.md", filepath.Join(root, "AGENTS.md")); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(root, "packages/api/.agents/AGENTS.md"), "API instructions.\n")
			writeFixture(t, filepath.Join(root, "packages/api/src/AGENTS.md"), "Source instructions.\n")
			link := filepath.Join(root, "packages/api/AGENTS.md")
			if err := os.Symlink(".agents/AGENTS.md", link); err != nil {
				t.Fatal(err)
			}
			if err := ValidateRepository(filepath.Join(root, ".agents")); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if target, err := os.Readlink(link); err != nil || target != ".agents/AGENTS.md" {
				t.Fatal("scoped link changed")
			}
			if vendor == "claude" {
				for _, name := range []string{"CLAUDE.md", "packages/api/CLAUDE.md", "packages/api/src/CLAUDE.md"} {
					if readNativeTest(t, filepath.Join(root, name)) != "@AGENTS.md\n" {
						t.Fatal("missing scoped bridge", name)
					}
				}
				if _, err := os.Stat(filepath.Join(root, "packages/api/.agents/CLAUDE.md")); !os.IsNotExist(err) {
					t.Fatal("bridge written inside canonical tree")
				}
			}
		})
	}
}

func TestScopedInstructionLinksRejectUnsafeTargets(t *testing.T) {
	for _, mode := range []string{"external", "ancestor", "missing", "cycle", "canonical-link", "canonical-directory-link", "directory"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.0.0","profiles":[]}`)
			writeFixture(t, filepath.Join(root, ".agents/AGENTS.md"), "Root.\n")
			scope := filepath.Join(root, "packages/api")
			if err := os.MkdirAll(scope, 0755); err != nil {
				t.Fatal(err)
			}
			canonical := filepath.Join(scope, ".agents/AGENTS.md")
			external := filepath.Join(t.TempDir(), "AGENTS.md")
			writeFixture(t, external, "External.\n")
			target := ".agents/AGENTS.md"
			switch mode {
			case "external":
				target = external
			case "ancestor":
				target = "../../.agents/AGENTS.md"
			case "missing":
			case "cycle":
				target = "AGENTS.md"
			case "canonical-link":
				if err := os.Mkdir(filepath.Dir(canonical), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, canonical); err != nil {
					t.Fatal(err)
				}
			case "canonical-directory-link":
				if err := os.Symlink(filepath.Dir(external), filepath.Dir(canonical)); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(filepath.Join(scope, "AGENTS.md"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "external" || mode == "ancestor" || mode == "cycle" {
				writeFixture(t, canonical, "Own scope.\n")
			}
			if mode != "directory" {
				if err := os.Symlink(target, filepath.Join(scope, "AGENTS.md")); err != nil {
					t.Fatal(err)
				}
			}
			before := instructionSnapshot(t, root)
			if err := ValidateRepository(filepath.Join(root, ".agents")); err == nil {
				t.Fatal("unsafe instructions accepted")
			}
			if _, err := ApplyProjection("claude", root, ApplyOptions{Force: true}); err == nil {
				t.Fatal("force accepted unsafe instructions")
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("refusal changed repository")
			}
		})
	}
}

func instructionSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			result[path] = "link:" + target
			return err
		case entry.IsDir():
			result[path] = "directory"
		default:
			data, err := os.ReadFile(path)
			result[path] = "file:" + string(data)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
