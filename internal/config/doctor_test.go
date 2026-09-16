package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func doctorFixtureEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	binary := filepath.Join(home, "native")
	writeFixture(t, binary, "#!/bin/sh\necho launched > \"$HOME/native-launched\"\nexit 99\n")
	if err := os.Chmod(binary, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_BIN", binary)
	t.Setenv("COPILOT_BIN", binary)
	return home
}

func doctorTestResult(t *testing.T, root, vendor string) DoctorResult {
	t.Helper()
	result, err := doctorDevelopment(vendor, root, func() ([]doctorMount, error) {
		return []doctorMount{{path: filepath.VolumeName(root) + string(filepath.Separator)}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func doctorHas(result DoctorResult, id, status string) bool {
	for _, check := range result.Checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func doctorEditPolicy(t *testing.T, root string, edit func(*DevelopmentPolicy)) {
	t.Helper()
	path := filepath.Join(root, ".agents", developmentPath)
	var policy DevelopmentPolicy
	if err := decodePolicy(path, &policy); err != nil {
		t.Fatal(err)
	}
	edit(&policy)
	data, _ := json.Marshal(policy)
	writeFixture(t, path, string(data))
}

func TestDoctorLifecycle(t *testing.T) {
	doctorFixtureEnvironment(t)
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root := practicalFixture(t)
			if result := doctorTestResult(t, root, vendor); result.Ready || result.ConfigurationState != "needs-apply" {
				t.Fatalf("unapplied: %+v", result)
			}
			options := ApplyOptions{Experimental: true, DevelopmentOnly: vendor == "codex"}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			if result := doctorTestResult(t, root, vendor); !result.Ready || result.ConfigurationState != "current" || !doctorHas(result, "session.authority", "unknown") {
				t.Fatalf("current: %+v", result)
			}
			doctorEditPolicy(t, root, func(p *DevelopmentPolicy) { p.LocalCommits = "deny" })
			if result := doctorTestResult(t, root, vendor); result.Ready || result.ConfigurationState != "needs-apply" || result.RequestedPolicy.LocalCommits != "deny" {
				t.Fatalf("policy edit: %+v", result)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			guardrails := filepath.Join(root, ".agents", developmentGuardrailsPath)
			writeFixture(t, guardrails, readNativeTest(t, guardrails)+"\nDOCTOR_PRIVATE_BODY\n")
			if result := doctorTestResult(t, root, vendor); result.Ready || result.ConfigurationState != "needs-apply" {
				t.Fatalf("guardrail edit: %+v", result)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[],"requires":[]}`)
			if result := doctorTestResult(t, root, vendor); result.Ready || result.ConfigurationState != "removal-pending" {
				t.Fatalf("pending removal: %+v", result)
			}
			if _, err := ApplyProjection(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			if result := doctorTestResult(t, root, vendor); !result.Ready || result.ConfigurationState != "not-selected" {
				t.Fatalf("removed: %+v", result)
			}
		})
	}
}

func TestDoctorMissingSetupAndStrictRefusal(t *testing.T) {
	doctorFixtureEnvironment(t)
	root := t.TempDir()
	if result := doctorTestResult(t, root, "codex"); !doctorHas(result, "setup.missing", "action-required") {
		t.Fatal(result)
	}
	if err := Init(root, false); err != nil {
		t.Fatal(err)
	}
	if result := doctorTestResult(t, root, "codex"); !doctorHas(result, "setup.adopt", "action-required") {
		t.Fatal(result)
	}
	strict := t.TempDir()
	if err := InitDevelopment(strict); err != nil {
		t.Fatal(err)
	}
	result := doctorTestResult(t, strict, "codex")
	if result.Ready || result.ConfigurationState != "refused" || result.RequestedPolicy.Enforcement != "strict" {
		t.Fatal(result)
	}
	for _, check := range result.Checks {
		if len(check.Commands) != 0 {
			t.Fatal("strict refusal offered a mutation", check)
		}
	}
}

func TestDoctorFailuresAndNoValueDisclosure(t *testing.T) {
	doctorFixtureEnvironment(t)
	for _, scenario := range []string{"policy", "guardrails", "manifest", "native-config", "ownership", "conflict", "legacy", "protected"} {
		t.Run(scenario, func(t *testing.T) {
			root := practicalFixture(t)
			if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, DevelopmentOnly: true}); err != nil {
				t.Fatal(err)
			}
			expected := "invalid"
			switch scenario {
			case "policy":
				writeFixture(t, filepath.Join(root, ".agents", developmentPath), `{"private":"DOCTOR_PRIVATE_BODY"}`)
			case "guardrails":
				os.Remove(filepath.Join(root, ".agents", developmentGuardrailsPath))
			case "manifest":
				writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"private":"DOCTOR_PRIVATE_BODY"}`)
			case "native-config":
				writeFixture(t, filepath.Join(root, ".codex/config.toml"), "DOCTOR_PRIVATE_BODY = [")
				expected = "blocked"
			case "ownership":
				writeFixture(t, filepath.Join(root, ".agents/state/native-codex.json"), `{"private":"DOCTOR_PRIVATE_BODY"}`)
				expected = "blocked"
			case "conflict":
				path := filepath.Join(root, ".codex/config.toml")
				writeFixture(t, path, strings.ReplaceAll(readNativeTest(t, path), "Local commits: allow", "DOCTOR_PRIVATE_BODY"))
				expected = "conflict"
			case "legacy":
				writeFixture(t, filepath.Join(root, ".codex/config.toml"), "sandbox_mode='danger-full-access'\nmodel='DOCTOR_PRIVATE_BODY'\n")
				expected = "legacy-settings"
			case "protected":
				doctorEditPolicy(t, root, func(p *DevelopmentPolicy) { p.ProtectedPaths = []string{".codex"} })
				expected = "unknown"
			}
			result := doctorTestResult(t, root, "codex")
			if result.Ready || result.ConfigurationState != expected {
				t.Fatalf("%s: %+v", scenario, result)
			}
			data, _ := json.Marshal(result)
			if strings.Contains(string(data), "DOCTOR_PRIVATE_BODY") {
				t.Fatal("private content reached doctor output")
			}
		})
	}
}

func TestDoctorDoesNotWriteOrLaunch(t *testing.T) {
	home := doctorFixtureEnvironment(t)
	root := practicalFixture(t)
	writeFixture(t, filepath.Join(root, ".env"), "DOCTOR_PRIVATE_BODY")
	writeFixture(t, filepath.Join(root, ".git/HEAD"), "ref: refs/heads/main\n")
	writeFixture(t, filepath.Join(root, ".git/index"), "fixture index")
	writeFixture(t, filepath.Join(home, ".codex/auth.json"), "DOCTOR_PRIVATE_BODY")
	writeFixture(t, filepath.Join(home, ".copilot/config.json"), "DOCTOR_PRIVATE_BODY")
	for _, vendor := range []string{"codex", "copilot"} {
		if _, err := ApplyProjection(vendor, root, ApplyOptions{Experimental: true, DevelopmentOnly: vendor == "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	before := []map[string]string{doctorSnapshot(t, root), doctorSnapshot(t, home)}
	for _, vendor := range []string{"codex", "copilot"} {
		result := doctorTestResult(t, root, vendor)
		if !result.Ready {
			t.Fatal(result)
		}
		body, _ := json.Marshal(result)
		if strings.Contains(string(body), "DOCTOR_PRIVATE_BODY") {
			t.Fatal("private contents reached report")
		}
	}
	after := []map[string]string{doctorSnapshot(t, root), doctorSnapshot(t, home)}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("doctor changed project, Git metadata, or user files")
	}
}

func TestDoctorExecutableSelection(t *testing.T) {
	home := doctorFixtureEnvironment(t)
	root := practicalFixture(t)
	if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, DevelopmentOnly: true}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"relative", filepath.Join(home, "missing"), home} {
		t.Setenv("CODEX_BIN", value)
		if result := doctorTestResult(t, root, "codex"); !doctorHas(result, "executable.location", "action-required") {
			t.Fatal(result)
		}
	}
	path := filepath.Join(home, "codex")
	writeFixture(t, path, "#!/bin/sh\nexit 99\n")
	os.Chmod(path, 0755)
	t.Setenv("CODEX_BIN", "")
	t.Setenv("PATH", home)
	if result := doctorTestResult(t, root, "codex"); !result.Ready || !doctorHas(result, "executable.location", "ok") {
		t.Fatal(result)
	}
}

func TestDoctorRemovalWithoutPolicyFile(t *testing.T) {
	doctorFixtureEnvironment(t)
	for _, vendor := range []string{"codex", "copilot"} {
		root := practicalFixture(t)
		options := ApplyOptions{Experimental: true, DevelopmentOnly: vendor == "codex"}
		if _, err := ApplyProjection(vendor, root, options); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":[],"requires":[]}`)
		if err := os.Remove(filepath.Join(root, ".agents", developmentPath)); err != nil {
			t.Fatal(err)
		}
		result := doctorTestResult(t, root, vendor)
		if result.ConfigurationState != "removal-pending" || result.Ready {
			t.Fatal("deleted policy hid pending removal", result)
		}
	}
}

func TestDoctorMountRestrictions(t *testing.T) {
	doctorFixtureEnvironment(t)
	root := practicalFixture(t)
	writeFixture(t, filepath.Join(root, ".git/HEAD"), "ref: refs/heads/main\n")
	if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, DevelopmentOnly: true}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, ".git")} {
		result, err := doctorDevelopment("codex", root, func() ([]doctorMount, error) {
			return []doctorMount{{path: "/"}, {path: path, readOnly: true}}, nil
		})
		if err != nil || result.Ready {
			t.Fatal("read-only mount was not actionable", result, err)
		}
	}
	result, err := doctorDevelopment("codex", root, func() ([]doctorMount, error) { return nil, errors.New("unavailable") })
	if err != nil || !result.Ready || !doctorHas(result, "filesystem.mounts", "unknown") {
		t.Fatal("unknown restriction changed readiness", result, err)
	}
}

func TestDoctorNestedGitAndWorktreeRestrictions(t *testing.T) {
	doctorFixtureEnvironment(t)
	for _, worktree := range []bool{false, true} {
		root := practicalFixture(t)
		writeFixture(t, filepath.Join(root, ".git/HEAD"), "ref: refs/heads/main\n")
		metadata := filepath.Join(root, ".git/modules/api")
		writeFixture(t, filepath.Join(metadata, "HEAD"), "ref: refs/heads/main\n")
		writeFixture(t, filepath.Join(root, "modules/api/.git"), "gitdir: ../../.git/modules/api\n")
		restricted := metadata
		if worktree {
			common := filepath.Join(t.TempDir(), "common")
			writeFixture(t, filepath.Join(common, "HEAD"), "ref: refs/heads/main\n")
			writeFixture(t, filepath.Join(metadata, "commondir"), common+"\n")
			restricted = common
		}
		if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true, DevelopmentOnly: true}); err != nil {
			t.Fatal(err)
		}
		result, err := doctorDevelopment("codex", root, func() ([]doctorMount, error) {
			return []doctorMount{{path: "/"}, {path: restricted, readOnly: true}}, nil
		})
		if err != nil || result.Ready {
			t.Fatal("nested Git mount was not inspected", result, err)
		}
		found := false
		for _, check := range result.Checks {
			if check.ID == "filesystem.git" && check.Status == "action-required" && reflect.DeepEqual(check.Paths, []string{restricted}) {
				found = true
			}
		}
		if !found {
			t.Fatal("missing exact metadata restriction", result)
		}
	}
}

// Include content, entry type, permissions and modification time. Access times
// can change when a read-only command inspects ordinary configuration.
func doctorSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := instructionSnapshot(t, root)
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result["metadata:"+path] = fmt.Sprintf("%s:%d", info.Mode(), info.ModTime().UnixNano())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
