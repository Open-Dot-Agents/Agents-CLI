package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func toolsProfile(t *testing.T, root string, enabled bool) {
	t.Helper()
	profiles := `[]`
	if enabled {
		profiles = `["tools"]`
	}
	writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.0.0","profiles":`+profiles+`}`)
}

func toolsSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[path] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestToolsProfileLifecycle(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			writeRepositoryFixtureWithoutSecrets(t, root)
			path := vendorMCPPath(vendor, root)
			toolsProfile(t, root, false)
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("initial unselected file: %v", err)
			}
			original := `{"editor":{"keep":true},"mcpServers":{"external":{"type":"stdio","command":"external-server"}}}`
			if vendor == "codex" {
				original = "# keep\nmodel = 'gpt-test'\n[mcp_servers.external]\ncommand = 'external-server'\n[features]\nkeep = true\n"
			}
			writeFixture(t, path, original)
			toolsProfile(t, root, true)
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			toolsProfile(t, root, false)
			catalogue := filepath.Join(root, ".agents/tools/mcp.json")
			canonical, err := os.ReadFile(catalogue)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(catalogue); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			servers, err := readVendorMCP(vendor, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(servers) != 1 || servers["external"].Command != "external-server" {
				t.Fatalf("wrong remaining servers: %#v", servers)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expectedSettings := []string{`"editor":`, `"keep": true`}
			if vendor == "codex" {
				expectedSettings = []string{"model = 'gpt-test'", "keep = true"}
			}
			for _, setting := range expectedSettings {
				if !strings.Contains(string(data), setting) {
					t.Fatalf("lost unrelated setting %q: %s", setting, data)
				}
			}
			if vendor == "codex" && (!strings.Contains(string(data), "# keep") || !strings.Contains(string(data), "[features]")) {
				t.Fatalf("lost TOML content: %s", data)
			}
			state, err := loadOwnership(root, vendor)
			if err != nil || len(state.Entries) != 0 {
				t.Fatalf("stale ownership: %#v %v", state, err)
			}
			before := toolsSnapshot(t, root)
			plan, err := ApplyProjection(vendor, root, ApplyOptions{})
			if err != nil {
				t.Fatalf("repeat: %#v %v", plan, err)
			}
			if !reflect.DeepEqual(before, toolsSnapshot(t, root)) {
				t.Fatal("repeat changed files")
			}
			writeFixture(t, catalogue, string(canonical))
			toolsProfile(t, root, true)
			if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			servers, err = readVendorMCP(vendor, root)
			if err != nil || len(servers) != 2 || servers["local"].Command != "server" {
				t.Fatalf("reenable: %#v %v", servers, err)
			}
		})
	}
}

func TestToolsRemovalConflictAndMissingFile(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		for _, missing := range []bool{false, true} {
			name := "modified"
			if missing {
				name = "missing"
			}
			t.Run(vendor+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				writeRepositoryFixtureWithoutSecrets(t, root)
				if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
					t.Fatal(err)
				}
				path := vendorMCPPath(vendor, root)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				modified := strings.Replace(string(data), "server'", "changed'", 1)
				modified = strings.Replace(modified, `"server"`, `"changed"`, 1)
				if missing {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					writeFixture(t, path, modified)
				}
				toolsProfile(t, root, false)
				before := toolsSnapshot(t, root)
				options := ApplyOptions{}
				if !missing {
					if _, err := ApplyProjection(vendor, root, options); err == nil {
						t.Fatal("modified entry removal accepted")
					}
					if !reflect.DeepEqual(before, toolsSnapshot(t, root)) {
						t.Fatal("refusal wrote files")
					}
					options = ApplyOptions{Force: true, Backup: true}
				}
				if _, err := ApplyProjection(vendor, root, options); err != nil {
					t.Fatal(err)
				}
				state, err := loadOwnership(root, vendor)
				if err != nil || len(state.Entries) != 0 {
					t.Fatalf("ownership: %#v %v", state, err)
				}
				if missing {
					if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("created absent file: %v", err)
					}
				} else {
					backups, err := filepath.Glob(path + ".backup-*")
					if err != nil || len(backups) != 1 {
						t.Fatalf("backups: %v %v", backups, err)
					}
					saved, err := os.ReadFile(backups[0])
					if err != nil || string(saved) != modified {
						t.Fatalf("backup differs: %v", err)
					}
					servers, err := readVendorMCP(vendor, root)
					if err != nil || len(servers) != 0 {
						t.Fatalf("forced removal: %#v %v", servers, err)
					}
				}
				before = toolsSnapshot(t, root)
				if _, err := ApplyProjection(vendor, root, ApplyOptions{}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, toolsSnapshot(t, root)) {
					t.Fatal("repeat changed files")
				}
			})
		}
	}
}

func TestToolsRemovalSyncRollback(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	if _, err := ApplySync("all", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	toolsProfile(t, root, false)
	before := toolsSnapshot(t, root)
	prepared, result, err := prepareSync("all", root, ApplyOptions{})
	if err != nil || !result.Applicable {
		t.Fatalf("prepare: %#v %v", result, err)
	}
	writes := 0
	err = applyPreparedProjections(prepared, ApplyOptions{}, func(path string, data []byte, mode fs.FileMode) error {
		writes++
		if writes == 6 {
			return errors.New("injected removal failure")
		}
		return atomicWrite(path, data, mode)
	})
	if err == nil || !strings.Contains(err.Error(), "injected removal failure") {
		t.Fatalf("wrong failure: %v", err)
	}
	if !reflect.DeepEqual(before, toolsSnapshot(t, root)) {
		t.Fatal("rollback did not restore configurations and ownership")
	}
	// A conflict in the last vendor must block every vendor before writes.
	path := vendorMCPPath("claude", root)
	writeFixture(t, path, `{"mcpServers":{"local":{"type":"stdio","command":"changed"}}}`)
	before = toolsSnapshot(t, root)
	if _, err := ApplySync("all", root, ApplyOptions{}); err == nil {
		t.Fatal("sync accepted drift")
	}
	if !reflect.DeepEqual(before, toolsSnapshot(t, root)) {
		t.Fatal("sync refusal wrote files")
	}
}
