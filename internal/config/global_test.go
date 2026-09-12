package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGlobalInitPrivateTransaction(t *testing.T) {
	home := t.TempDir()
	injected := errors.New("injected global init failure")
	err := initGlobal(home, func(stage string, index int) error {
		if stage == "after-write" && index == 3 {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatal("missing final-write failure", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("global init did not roll back", err)
	}
	if err := InitGlobal(home); err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(filepath.Join(home, ".agents"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("nonprivate global path %s: %o", path, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUserCanonicalBindingRefusesLossAndConflicts(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		for _, problem := range []string{"references", "invalid-utf8", "unknown-version", "duplicate-target"} {
			t.Run(vendor+"/"+problem, func(t *testing.T) {
				home, native := t.TempDir(), t.TempDir()
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				if err := InitGlobal(home); err != nil {
					t.Fatal(err)
				}
				ns, _ := nativeNamespace(vendor)
				profile := filepath.Join(home, ".agents/native", ns, "profile.json")
				switch problem {
				case "references":
					writeFixture(t, filepath.Join(home, ".agents/AGENTS.md"), "@rules/global.md\n")
				case "invalid-utf8":
					writeFixture(t, filepath.Join(home, ".agents/AGENTS.md"), string([]byte{0xff}))
				case "unknown-version":
					writeFixture(t, profile, strings.ReplaceAll(readNativeTest(t, profile), "="+nativePinnedVersion(vendor), "=999.0.0"))
				case "duplicate-target":
					var p nativeProfile
					if err := nativeDecodePolicy(profile, &p); err != nil {
						t.Fatal(err)
					}
					p.Artifacts = append(p.Artifacts, nativeArtifact{Kind: "instructions", Source: "other.md"})
					data, err := json.Marshal(p)
					if err != nil {
						t.Fatal(err)
					}
					writeFixture(t, profile, string(data))
					writeFixture(t, filepath.Join(filepath.Dir(profile), "other.md"), "Separate native instructions.\n")
				}
				_, err := ApplyProjection(vendor, home, ApplyOptions{Experimental: true, Scope: "user", NativeHome: native, Force: true, Backup: true})
				if err == nil {
					t.Fatal("unsafe user canonical binding activated")
				}
				entries, err := os.ReadDir(native)
				if err != nil || len(entries) != 0 {
					t.Fatal("refusal changed native files", entries, err)
				}
			})
		}
	}
}

func TestGlobalOwnershipCannotBeTakenByProject(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			home, project, native := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			for _, root := range []string{home, project} {
				if err := InitGlobal(root); err != nil {
					t.Fatal(err)
				}
			}
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: native}
			if _, err := ApplyProjection(vendor, home, options); err != nil {
				t.Fatal(err)
			}
			options.Force, options.Backup = true, true
			if _, err := ApplyProjection(vendor, project, options); err == nil {
				t.Fatal("project adopted global ownership through force")
			}
		})
	}
}

func TestProjectCannotDropGlobalSecurityRequirements(t *testing.T) {
	home := securityFixture(t)
	t.Setenv("HOME", home)
	for _, path := range []string{"manifest.json", "permissions/permissions.json", "sandbox/sandbox.json"} {
		full := filepath.Join(home, ".agents", path)
		writeFixture(t, full, strings.ReplaceAll(readNativeTest(t, full), ExperimentalVersion, NativeVersion))
	}
	project := nativeFixture(t, "project", "model = 'fixture-model'\n")
	before := portabilitySnapshot(t, project)
	_, err := ApplyProjection("codex", project, ApplyOptions{Experimental: true, Force: true, Backup: true})
	if err == nil || !strings.Contains(err.Error(), "global portable security") {
		t.Fatal("global requirements were dropped", err)
	}
	if !reflect.DeepEqual(before, portabilitySnapshot(t, project)) {
		t.Fatal("global security refusal changed project")
	}
}

func TestProjectReportsGlobalDefaultsWithoutWritingUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := InitGlobal(home); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".codex/config.toml"), "model = 'global-model'\n")
	project := nativeFixture(t, "project", "model = 'project-model'\n")
	before := portabilitySnapshot(t, home)
	plan, err := ApplyProjection("codex", project, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Native.GlobalSource != filepath.Join(home, ".agents") {
		t.Fatal("global provenance omitted", plan.Native)
	}
	if !strings.Contains(readNativeTest(t, filepath.Join(project, ".codex/config.toml")), "project-model") {
		t.Fatal("project override missing")
	}
	if !reflect.DeepEqual(before, portabilitySnapshot(t, home)) {
		t.Fatal("project changed global source or user config")
	}
}

func TestGlobalSharedSkillsAreNotDuplicated(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if err := InitGlobal(home); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(home, ".agents/skills/fixture/SKILL.md"), "---\nname: fixture\ndescription: Global fixture.\n---\nUse the global skill.\n")
			native := filepath.Join(home, "."+vendor)
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: native}
			if _, err := ApplyProjection(vendor, home, options); err == nil {
				t.Fatal("unselected discoverable global skill accepted")
			}
			writeFixture(t, filepath.Join(home, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native","skills"]}`)
			before := portabilitySnapshot(t, filepath.Join(home, ".agents/skills"))
			if _, err := ApplyProjection(vendor, home, options); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(native, "skills/fixture")); !os.IsNotExist(err) {
				t.Fatal("shared skill was duplicated", err)
			}
			if !reflect.DeepEqual(before, portabilitySnapshot(t, filepath.Join(home, ".agents/skills"))) {
				t.Fatal("global source was changed")
			}
		})
	}
}
