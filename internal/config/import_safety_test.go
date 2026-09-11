package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableCodexImportRejectsLiteralSecretsBeforeWrites(t *testing.T) {
	for name, native := range map[string]string{
		"literal-uri":    "[mcp_servers.demo]\ncommand = 'demo'\n[mcp_servers.demo.env]\nTOKEN = 'urn:open-dot-agents:env:TOKEN'\n",
		"literal-env":    "[mcp_servers.demo]\ncommand = 'demo'\n[mcp_servers.demo.env]\nTOKEN = 'oda-test-secret'\n",
		"mixed-env":      "[mcp_servers.demo]\ncommand = 'demo'\nenv_vars = ['PASSTHROUGH']\n[mcp_servers.demo.env]\nTOKEN = 'oda-test-secret'\n",
		"literal-header": "[mcp_servers.demo]\nurl = 'https://example.test/mcp'\n[mcp_servers.demo.http_headers]\nAuthorization = 'oda-test-secret'\n",
		"mixed-header":   "[mcp_servers.demo]\nurl = 'https://example.test/mcp'\n[mcp_servers.demo.http_headers]\nAuthorization = 'oda-test-secret'\n[mcp_servers.demo.env_http_headers]\nOther = 'PASSTHROUGH'\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
			writeFixture(t, filepath.Join(root, ".codex/config.toml"), native)
			before := instructionSnapshot(t, root)
			err := ImportRepositoryWithOptions("codex", root, WriteOptions{Force: true, Backup: true})
			if err == nil {
				t.Fatal("literal credential was accepted or discarded")
			}
			if strings.Contains(err.Error(), "oda-test-secret") {
				t.Fatal("diagnostic exposed a credential value")
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("refused import changed repository files")
			}
		})
	}
}

func TestStableImportValidatesRetainedProfilesBeforeWrites(t *testing.T) {
	root := t.TempDir()
	writeCanonicalFixture(t, root)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
	writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\n")
	writeFixture(t, filepath.Join(root, ".agents/manifest.json"), `{"version":"1.0.0","profiles":["hooks"],"requires":["hooks.command"]}`)
	writeFixture(t, filepath.Join(root, ".agents/hooks/hooks.json"), `{"hooks":{"UnknownEvent":[]}}`)
	before := instructionSnapshot(t, root)
	if err := ImportRepositoryWithOptions("codex", root, WriteOptions{Force: true, Backup: true}); err == nil {
		t.Fatal("invalid retained profile was accepted or deactivated")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("retained-profile validation failure changed files")
	}
}

func TestStableImportRefusesExternalInstructionLinkBeforeWrites(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFixture(t, outside, "oda-external-instruction-sentinel")
	writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\n")
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	before := instructionSnapshot(t, root)
	if err := ImportRepositoryWithOptions("codex", root, WriteOptions{Force: true, Backup: true}); err == nil {
		t.Fatal("external instructions were imported")
	}
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("instruction refusal changed files")
	}
}

func TestStableImportRollbackIncludesBackups(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
			writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\n")
			if existing {
				writeCanonicalFixture(t, root)
				if err := os.Chmod(filepath.Join(root, ".agents/AGENTS.md"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := instructionSnapshot(t, root)
			failed, count := false, 0
			write := func(path string, data []byte, mode fs.FileMode) error {
				count++
				if !failed && count == 3 {
					failed = true
					return errors.New("injected import write failure")
				}
				return atomicWrite(path, data, mode)
			}
			err := importStableRepository("codex", root, WriteOptions{Force: true, Backup: true}, write)
			if err == nil || !failed {
				t.Fatalf("failure not exercised: %v", err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("import rollback did not restore all files and backups")
			}
			if existing {
				info, err := os.Stat(filepath.Join(root, ".agents/AGENTS.md"))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Fatal("rollback changed permissions")
				}
			} else if _, err := os.Lstat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
				t.Fatalf("rollback left created directories: %v", err)
			}
		})
	}
}

func TestStableImportPreservesModesAndMakesPrivateBackups(t *testing.T) {
	root := t.TempDir()
	writeCanonicalFixture(t, root)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
	writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\n")
	canonical := filepath.Join(root, ".agents/AGENTS.md")
	if err := os.Chmod(canonical, 0640); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("codex", root, WriteOptions{Force: true, Backup: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatal("import changed existing permissions")
	}
	backups, err := filepath.Glob(canonical + ".backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup missing: %v", err)
	}
	info, err = os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
}

func TestStableImportRefusesUnmappedMCPControlsBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		for _, field := range []string{"enabled", "required", "disabled_tools", "bearer_token_env_var", "unknown_optional"} {
			t.Run(vendor+"/"+field, func(t *testing.T) {
				root := t.TempDir()
				writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
				data := `{"mcpServers":{"demo":{"command":"demo","` + field + `":false}}}`
				if vendor == "codex" {
					data = "[mcp_servers.demo]\ncommand = 'demo'\n" + field + " = false\n"
				}
				writeFixture(t, vendorMCPPath(vendor, root), data)
				before := instructionSnapshot(t, root)
				err := ImportRepositoryWithOptions(vendor, root, WriteOptions{Force: true, Backup: true})
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("unmapped control not reported: %v", err)
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
					t.Fatal("unmapped control refusal changed files")
				}
			})
		}
	}
}

func TestStableImportKeepsConcurrentPolicyEdit(t *testing.T) {
	root := t.TempDir()
	writeCanonicalFixture(t, root)
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
	writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\n")
	manifest := filepath.Join(root, ".agents/manifest.json")
	edited := `{"version":"1.0.0","profiles":["tools","skills"],"requires":["mcp.envRef"]}`
	before := instructionSnapshot(t, root)
	didEdit := false
	write := func(path string, data []byte, mode fs.FileMode) error {
		if !didEdit {
			didEdit = true
			if err := os.WriteFile(manifest, []byte(edited), 0644); err != nil {
				return err
			}
		}
		return atomicWrite(path, data, mode)
	}
	err := importStableRepository("codex", root, WriteOptions{Force: true, Backup: true}, write)
	if err == nil || !strings.Contains(err.Error(), "changed before write") {
		t.Fatalf("concurrent policy edit not refused: %v", err)
	}
	before[manifest] = "file:" + edited
	if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
		t.Fatal("rollback did not retain the concurrent policy edit")
	}
}

func TestStableImportRefusesChangedValidatedTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	writeFixture(t, path, "original")
	expected, err := snapshotManagedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, "concurrent edit")
	err = commitStableImport(map[string][]byte{path: []byte("imported")}, nil, map[string]managedSnapshot{path: expected}, WriteOptions{Force: true, Backup: true}, atomicWrite)
	if err == nil || !strings.Contains(err.Error(), "changed during validation") {
		t.Fatalf("stale import input not refused: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "concurrent edit" {
		t.Fatal("stale input refusal changed output")
	}
	backups, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(backups) != 0 {
		t.Fatal("stale input refusal left a backup")
	}
}

func TestStableClaudeImportPreservesSkillAssets(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
	writeFixture(t, vendorMCPPath("claude", root), `{"mcpServers":{"demo":{"command":"demo"}}}`)
	source := vendorSkillsPath("claude", root)
	writeFixture(t, filepath.Join(source, "review/SKILL.md"), "# Review\n")
	writeFixture(t, filepath.Join(source, "review/check.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(source, "review/check.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("claude", root, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, ".agents/skills/review/check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatal("import lost executable skill asset mode")
	}
	if err := Validate(filepath.Join(root, ".agents")); err != nil {
		t.Fatal(err)
	}
}

func TestStableImportPreservesRequiredCapabilities(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Native instructions\n")
	writeFixture(t, filepath.Join(root, ".codex/config.toml"), "[mcp_servers.demo]\ncommand = 'demo'\nenv_vars = ['TOKEN']\n")
	writeCanonicalFixture(t, root)
	manifest := `{"version":"1.0.0","profiles":["tools","skills"],"requires":["mcp.envRef"]}`
	writeFixture(t, filepath.Join(root, ".agents/manifest.json"), manifest)
	if err := ImportRepositoryWithOptions("codex", root, WriteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".agents/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mcp.envRef") {
		t.Fatal("forced import removed a required portable capability")
	}
}
