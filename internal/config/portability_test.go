package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func portabilitySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUnselectedSkillsRefuseBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			writeRepositoryFixtureWithoutSecrets(t, root)
			manifest := filepath.Join(root, ".agents", "manifest.json")
			writeFixture(t, manifest, `{"version":"1.0.0","profiles":["tools","skills"]}`)
			writeFixture(t, filepath.Join(root, ".agents", "skills", "probe", "SKILL.md"), "---\nname: probe\ndescription: Test\n---\nTest\n")
			for _, phase := range []string{"initial-off", "enabled", "removed", "enabled-again"} {
				selected := phase == "enabled" || phase == "enabled-again"
				profiles := `["tools"]`
				if selected {
					profiles = `["tools","skills"]`
				}
				writeFixture(t, manifest, `{"version":"1.0.0","profiles":`+profiles+`}`)
				if selected {
					for i := 0; i < 2; i++ {
						if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
							t.Fatal(err)
						}
					}
					continue
				}
				before := portabilitySnapshot(t, root)
				for _, options := range []ApplyOptions{{}, {Force: true, Backup: true, Adopt: true}} {
					plan, err := PlanProjection(vendor, root, options)
					if err != nil || plan.Applicable || !strings.Contains(strings.Join(plan.Diagnostics, " "), "ODA-ADAPTER-0003") {
						t.Fatalf("%s: %#v %v", phase, plan, err)
					}
					if _, err := ApplyProjection(vendor, root, options); err == nil {
						t.Fatal("apply accepted unselected skills")
					}
					if _, err := ApplySync("all", root, options); err == nil {
						t.Fatal("sync accepted unselected skills")
					}
				}
				if err := ExportWithOptions(vendor, filepath.Join(root, ".agents"), root, WriteOptions{Force: true, Backup: true}); err == nil {
					t.Fatal("export accepted unselected skills")
				}
				if !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
					t.Fatal("refusal changed files or directories")
				}
			}
		})
	}
}

func TestCodexStdioReferencesRefuseRegardlessOfEnvironment(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "present"}[present], func(t *testing.T) {
			if present {
				t.Setenv("ODA_REQUIRED_TEST", "must-not-appear-in-diagnostics")
			}
			root := t.TempDir()
			writeRepositoryFixtureWithoutSecrets(t, root)
			writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"server","env":{"ODA_REQUIRED_TEST":"urn:open-dot-agents:env:ODA_REQUIRED_TEST"}}}}`)
			before := portabilitySnapshot(t, root)
			plan, err := PlanProjection("codex", root, ApplyOptions{})
			diagnostic := strings.Join(plan.Diagnostics, " ")
			if err != nil || plan.Applicable || !strings.Contains(diagnostic, "ODA-ADAPTER-0004") || strings.Contains(diagnostic, "must-not-appear") {
				t.Fatalf("%#v %v", plan, err)
			}
			if _, err := ApplyProjection("codex", root, ApplyOptions{Force: true, Backup: true}); err == nil {
				t.Fatal("apply accepted reference")
			}
			if _, err := ApplySync("all", root, ApplyOptions{}); err == nil {
				t.Fatal("sync accepted reference")
			}
			if err := ExportWithOptions("codex", filepath.Join(root, ".agents"), root, WriteOptions{Force: true, Backup: true}); err == nil {
				t.Fatal("export accepted reference")
			}
			if !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
				t.Fatal("refusal changed files")
			}
		})
	}
}

func TestUnselectedAbsentAndEmptySkillsAreAllowed(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		root := t.TempDir()
		writeRepositoryFixtureWithoutSecrets(t, root)
		for _, empty := range []bool{false, true} {
			if empty {
				if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestExportPreservesSupportedRemoteReferences(t *testing.T) {
	for _, vendor := range []string{"codex", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			source := t.TempDir()
			output := t.TempDir()
			writeFixture(t, filepath.Join(source, "AGENTS.md"), "# Instructions\n")
			writeFixture(t, filepath.Join(source, "manifest.json"), `{"version":"1.0.0","profiles":["tools"]}`)
			writeFixture(t, filepath.Join(source, "tools", "mcp.json"), `{"mcpServers":{"remote":{"type":"remote","url":"https://example.com/mcp","headers":{"Authorization":"urn:open-dot-agents:env:AUTH_TOKEN"}}}}`)
			if err := ExportWithOptions(vendor, source, output, WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(vendorMCPPath(vendor, output))
			if err != nil {
				t.Fatal(err)
			}
			expected := "env_http_headers"
			if vendor == "claude" {
				expected = "${AUTH_TOKEN}"
			}
			if !strings.Contains(string(data), expected) || strings.Contains(string(data), "urn:open-dot-agents") {
				t.Fatalf("reference not mapped: %s", data)
			}
		})
	}
}

func TestRepositoryContractRequiresManifest(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".agents", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	before := portabilitySnapshot(t, root)
	if err := ValidateRepository(filepath.Join(root, ".agents")); err == nil {
		t.Fatal("missing manifest accepted")
	}
	if _, err := ApplyProjection("copilot", root, ApplyOptions{}); err == nil {
		t.Fatal("apply accepted missing manifest")
	}
	if !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
		t.Fatal("refusal changed files")
	}
}

func TestExplicitUnsupportedCapabilityRefusesBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		root := t.TempDir()
		writeRepositoryFixtureWithoutSecrets(t, root)
		writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":[],"requires":["mcp.envRef"]}`)
		before := portabilitySnapshot(t, root)
		plan, err := PlanProjection(vendor, root, ApplyOptions{})
		if err != nil || plan.Applicable || !strings.Contains(strings.Join(plan.Diagnostics, " "), "ODA-ADAPTER-0005") {
			t.Fatalf("%#v %v", plan, err)
		}
		if _, err := ApplySync("all", root, ApplyOptions{Force: true, Backup: true}); err == nil {
			t.Fatal("sync ignored required capability")
		}
		if err := ExportWithOptions(vendor, filepath.Join(root, ".agents"), root, WriteOptions{Force: true, Backup: true}); err == nil {
			t.Fatal("export ignored required capability")
		}
		if !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
			t.Fatal("refusal changed files")
		}
	}
}
