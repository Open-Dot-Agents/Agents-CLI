package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportAndImportCopilotPreservesMCPAndSkills(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	copilot := t.TempDir()

	if err := Export("copilot", filepath.Join(source, ".agents"), copilot, false); err != nil {
		t.Fatalf("export Copilot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(copilot, ".github", "mcp.json")); err != nil {
		t.Fatalf("Copilot MCP configuration was not created: %v", err)
	}
	skill, err := os.ReadFile(filepath.Join(copilot, ".agents", "skills", "review", "SKILL.md"))
	if err != nil {
		t.Fatalf("read exported skill: %v", err)
	}
	if string(skill) != "# Review\n" {
		t.Fatalf("unexpected skill content: %q", skill)
	}
	instructions, err := os.ReadFile(filepath.Join(copilot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read exported instructions: %v", err)
	}
	if string(instructions) != "# Shared instructions\n" {
		t.Fatalf("unexpected exported instructions: %q", instructions)
	}

	imported := t.TempDir()
	if err := Import("copilot", copilot, filepath.Join(imported, ".agents"), false); err != nil {
		t.Fatalf("import Copilot: %v", err)
	}
	got, err := readCanonicalMCP(filepath.Join(imported, ".agents"))
	if err != nil {
		t.Fatalf("read imported canonical MCP configuration: %v", err)
	}
	if got["local"].Command != "example-mcp" || got["remote"].URL != "https://example.test/mcp" {
		t.Fatalf("unexpected imported servers: %#v", got)
	}
	instructions, err = os.ReadFile(filepath.Join(imported, ".agents", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read imported instructions: %v", err)
	}
	if string(instructions) != "# Shared instructions\n" {
		t.Fatalf("unexpected imported instructions: %q", instructions)
	}
}

func TestConvertCodexToCopilot(t *testing.T) {
	codex := t.TempDir()
	config := `[mcp_servers.local]
command = "example-mcp"
args = ["serve"]
env = { TOKEN = "token" }

[mcp_servers.remote]
url = "https://example.test/mcp"
http_headers = { Authorization = "Bearer token" }
`
	writeFixture(t, filepath.Join(codex, ".codex", "config.toml"), config)
	writeFixture(t, filepath.Join(codex, ".agents", "skills", "review", "SKILL.md"), "# Review\n")
	writeFixture(t, filepath.Join(codex, "AGENTS.md"), "# Codex instructions\n")

	copilot := t.TempDir()
	if err := Convert("codex", "copilot", codex, copilot, false); err != nil {
		t.Fatalf("convert Codex to Copilot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(copilot, ".github", "mcp.json"))
	if err != nil {
		t.Fatalf("read converted Copilot configuration: %v", err)
	}
	if !strings.Contains(string(data), `"type": "stdio"`) || !strings.Contains(string(data), `"url": "https://example.test/mcp"`) || !strings.Contains(string(data), `"Authorization": "Bearer token"`) {
		t.Fatalf("converted MCP configuration lost server fields: %s", data)
	}
	instructions, err := os.ReadFile(filepath.Join(copilot, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read converted instructions: %v", err)
	}
	if string(instructions) != "# Codex instructions\n" {
		t.Fatalf("unexpected converted instructions: %q", instructions)
	}
}

func TestExportRefusesExistingOutputWithoutForce(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".github", "mcp.json"), "{}")

	err := Export("copilot", filepath.Join(source, ".agents"), output, false)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected overwrite protection, got %v", err)
	}
}

func TestExportRefusesSymlinkedManagedDestination(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source")
	writeCanonicalFixture(t, source)
	output := filepath.Join(workspace, "output")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(workspace, "external-instructions.md")
	writeFixture(t, external, "# External\n")
	if err := os.Symlink(external, filepath.Join(output, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	err := ExportWithOptions("copilot", filepath.Join(source, ".agents"), output, WriteOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "# External\n" {
		t.Fatalf("external file was modified: %q (%v)", data, err)
	}
}

func TestCopyOperationsRejectSymlinkedDestinations(t *testing.T) {
	workspace := t.TempDir()
	sourceSkills := filepath.Join(workspace, "source-skills")
	writeFixture(t, filepath.Join(sourceSkills, "review", "SKILL.md"), "# Review\n")
	externalSkills := filepath.Join(workspace, "external-skills")
	if err := os.MkdirAll(externalSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkedSkills := filepath.Join(workspace, "symlinked-skills")
	if err := os.Symlink(externalSkills, symlinkedSkills); err != nil {
		t.Fatal(err)
	}
	if err := copySkills(sourceSkills, symlinkedSkills, true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked skills rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(externalSkills, "review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("external skills directory was modified: %v", err)
	}

	sourceDirectory := filepath.Join(workspace, "source-directory")
	writeFixture(t, filepath.Join(sourceDirectory, "file.txt"), "source\n")
	externalDirectory := filepath.Join(workspace, "external-directory")
	if err := os.MkdirAll(externalDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkedDirectory := filepath.Join(workspace, "symlinked-directory")
	if err := os.Symlink(externalDirectory, symlinkedDirectory); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(sourceDirectory, symlinkedDirectory); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked directory rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(externalDirectory, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("external directory was modified: %v", err)
	}
}

func TestBackupRejectsSymlinkedPath(t *testing.T) {
	workspace := t.TempDir()
	external := filepath.Join(workspace, "external.txt")
	writeFixture(t, external, "external\n")
	managed := filepath.Join(workspace, "managed.txt")
	if err := os.Symlink(external, managed); err != nil {
		t.Fatal(err)
	}

	if err := backupPath(managed); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked backup rejection, got %v", err)
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "external\n" {
		t.Fatalf("external backup source was modified: %q (%v)", data, err)
	}
}

func TestExportHonorsSelectedManifestProfiles(t *testing.T) {
	source := t.TempDir()
	agentsRoot := filepath.Join(source, ".agents")
	writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":["instructions"]}`)
	writeFixture(t, filepath.Join(agentsRoot, "AGENTS.md"), "# Instructions only\n")
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".github", "mcp.json"), `{"mcpServers":{"keep":{"command":"keep"}}}`)
	writeFixture(t, filepath.Join(output, ".agents", "skills", "keep", "SKILL.md"), "# Keep\n")

	if err := ExportWithOptions("copilot", agentsRoot, output, WriteOptions{Force: true, Backup: true}); err != nil {
		t.Fatalf("export instructions-only manifest: %v", err)
	}
	instructions, err := os.ReadFile(filepath.Join(output, "AGENTS.md"))
	if err != nil || string(instructions) != "# Instructions only\n" {
		t.Fatalf("unexpected exported instructions: %q (%v)", instructions, err)
	}
	mcp, err := os.ReadFile(filepath.Join(output, ".github", "mcp.json"))
	if err != nil || !strings.Contains(string(mcp), `"keep"`) {
		t.Fatalf("unselected MCP was projected: %q (%v)", mcp, err)
	}
	if _, err := os.Stat(filepath.Join(output, ".agents", "skills", "keep", "SKILL.md")); err != nil {
		t.Fatalf("unselected skills were changed: %v", err)
	}
	for _, path := range []string{".github.backup-*", ".agents.backup-*"} {
		backups, err := filepath.Glob(filepath.Join(output, path))
		if err != nil || len(backups) != 0 {
			t.Fatalf("unselected profile was backed up at %q: %v (%v)", path, backups, err)
		}
	}
}

func TestExportBacksUpExistingConfigurationAndSkills(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".github", "mcp.json"), `{"mcpServers":{"old":{"command":"old"}}}`)
	writeFixture(t, filepath.Join(output, ".agents", "skills", "old", "SKILL.md"), "# Old\n")
	writeFixture(t, filepath.Join(output, "AGENTS.md"), "# Old instructions\n")

	err := ExportWithOptions("copilot", filepath.Join(source, ".agents"), output, WriteOptions{
		Force:  true,
		Backup: true,
	})
	if err != nil {
		t.Fatalf("export with backup: %v", err)
	}
	configBackups, err := filepath.Glob(filepath.Join(output, ".github.backup-*", "mcp.json"))
	if err != nil || len(configBackups) != 1 {
		t.Fatalf("expected one MCP backup, got %v (%v)", configBackups, err)
	}
	data, err := os.ReadFile(configBackups[0])
	if err != nil || !strings.Contains(string(data), `"old"`) {
		t.Fatalf("backup does not preserve original MCP configuration: %q (%v)", data, err)
	}
	skillBackups, err := filepath.Glob(filepath.Join(output, ".agents.backup-*", "skills", "old", "SKILL.md"))
	if err != nil || len(skillBackups) != 1 {
		t.Fatalf("expected one skill directory backup, got %v (%v)", skillBackups, err)
	}
	if _, err := os.Stat(skillBackups[0]); err != nil {
		t.Fatalf("expected skill directory backup: %v", err)
	}
	instructionBackups, err := filepath.Glob(filepath.Join(output, "AGENTS.md.backup-*"))
	if err != nil || len(instructionBackups) != 1 {
		t.Fatalf("expected one instruction backup, got %v (%v)", instructionBackups, err)
	}
	data, err = os.ReadFile(instructionBackups[0])
	if err != nil || string(data) != "# Old instructions\n" {
		t.Fatalf("instruction backup does not preserve original content: %q (%v)", data, err)
	}
}

func TestBackupRequiresForce(t *testing.T) {
	err := ExportWithOptions("copilot", t.TempDir(), t.TempDir(), WriteOptions{Backup: true})
	if err == nil || !strings.Contains(err.Error(), "--backup requires --force") {
		t.Fatalf("expected backup validation error, got %v", err)
	}
}

func TestBackupCreatesDistinctDirectorySnapshots(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".github", "mcp.json"), "{}")

	options := WriteOptions{
		Force:  true,
		Backup: true,
	}
	if err := ExportWithOptions("copilot", filepath.Join(source, ".agents"), output, options); err != nil {
		t.Fatalf("create first backup: %v", err)
	}
	if err := ExportWithOptions("copilot", filepath.Join(source, ".agents"), output, options); err != nil {
		t.Fatalf("create second backup: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(output, ".github.backup-*"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("expected two distinct backups, got %v (%v)", backups, err)
	}
}

func TestBackupPreservesRegularFilePermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	writeFixture(t, path, "# Private\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backupPath(path); err != nil {
		t.Fatalf("back up regular file: %v", err)
	}
	backups, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one backup, got %v (%v)", backups, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestInitCreatesExportableStarterTree(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, false); err != nil {
		t.Fatalf("initialize starter tree: %v", err)
	}
	agentsRoot := filepath.Join(root, ".agents")
	for _, path := range []string{
		"AGENTS.md",
		"manifest.json",
		"tools/mcp.json",
		"skills/example/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(agentsRoot, path)); err != nil {
			t.Fatalf("starter file %q was not created: %v", path, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(agentsRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read starter manifest: %v", err)
	}
	if !strings.Contains(string(manifest), `"version": "1.0.0"`) ||
		!strings.Contains(string(manifest), `"profiles": ["instructions", "mcp", "skills"]`) {
		t.Fatalf("starter manifest is not interoperable: %s", manifest)
	}
	if _, err := readCanonicalMCP(agentsRoot); err != nil {
		t.Fatalf("read starter MCP configuration: %v", err)
	}
	if err := Validate(agentsRoot); err != nil {
		t.Fatalf("validate starter tree: %v", err)
	}
	if err := Export("copilot", agentsRoot, t.TempDir(), false); err != nil {
		t.Fatalf("export starter tree: %v", err)
	}
}

func TestInitRefusesExistingTreeWithoutForce(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, false); err != nil {
		t.Fatalf("initialize starter tree: %v", err)
	}
	err := Init(root, false)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected overwrite protection, got %v", err)
	}
}

func TestValidateRejectsInvalidCanonicalStructure(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *testing.T, agentsRoot string)
		wantErr string
	}{
		{
			name: "unsupported manifest",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"2.0.0","profiles":[]}`)
			},
			wantErr: "unsupported version",
		},
		{
			name: "extra MCP root",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{},"other":{}}`)
			},
			wantErr: "unsupported root field",
		},
		{
			name: "remote without URL",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"remote":{"type":"remote"}}}`)
			},
			wantErr: "must define url",
		},
		{
			name: "invalid environment map",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"run","env":["TOKEN"]}}}`)
			},
			wantErr: "must be a map of strings",
		},
		{
			name: "invalid argument list",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"run","args":"serve"}}}`)
			},
			wantErr: "must be a list of strings",
		},
		{
			name: "invalid command type",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":1}}}`)
			},
			wantErr: "must be a string",
		},
		{
			name: "unsupported canonical MCP type",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"remote":{"type":"http","url":"https://example.test/mcp"}}}`)
			},
			wantErr: "unsupported canonical type",
		},
		{
			name: "extra versioned MCP server field",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"run","startup_timeout_sec":1}}}`)
			},
			wantErr: "unsupported field",
		},
		{
			name: "non HTTPS remote URL",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"remote":{"type":"remote","url":"http://example.test/mcp"}}}`)
			},
			wantErr: "HTTPS url",
		},
		{
			name: "non URN environment reference",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"run","env":{"TOKEN":"token"}}}}`)
			},
			wantErr: "urn:open-dot-agents:env",
		},
		{
			name: "non URN header reference",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{"remote":{"type":"remote","url":"https://example.test/mcp","headers":{"Authorization":"token"}}}}`)
			},
			wantErr: "urn:open-dot-agents:env",
		},
		{
			name: "empty manifest profiles",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":[]}`)
			},
			wantErr: "at least one profile",
		},
		{
			name: "duplicate manifest profile",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":["mcp","mcp"]}`)
			},
			wantErr: "duplicate profile",
		},
		{
			name: "invalid manifest profile name",
			mutate: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":["Bad"]}`)
			},
			wantErr: "invalid profile",
		},
		{
			name: "unsafe skill name",
			mutate: func(t *testing.T, agentsRoot string) {
				if err := os.Rename(filepath.Join(agentsRoot, "skills", "example"), filepath.Join(agentsRoot, "skills", ".unsafe")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "unsafe skill name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Init(root, false); err != nil {
				t.Fatalf("initialize starter tree: %v", err)
			}
			agentsRoot := filepath.Join(root, ".agents")
			test.mutate(t, agentsRoot)
			err := Validate(agentsRoot)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected %q validation error, got %v", test.wantErr, err)
			}
		})
	}
}

func TestValidateAllowsCompatibleManifestAndEnvironmentReferences(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, false); err != nil {
		t.Fatalf("initialize starter tree: %v", err)
	}
	agentsRoot := filepath.Join(root, ".agents")
	writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{
  "version": "1.0.0",
  "profiles": ["mcp"],
  "future_field": {"enabled": true}
}`)
	writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{
  "$schema": "schemas/mcp.schema.json",
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "example-mcp",
      "env": {"TOKEN": "urn:open-dot-agents:env:MCP_TOKEN"}
    }
  }
}`)
	if err := Validate(agentsRoot); err != nil {
		t.Fatalf("validate environment reference: %v", err)
	}
}

func TestValidateRequiresOnlyManifestProfiles(t *testing.T) {
	tests := []struct {
		name     string
		profiles string
		setup    func(t *testing.T, agentsRoot string)
	}{
		{
			name:     "MCP only",
			profiles: `["mcp", "future-profile"]`,
			setup: func(t *testing.T, agentsRoot string) {
				writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{}}`)
			},
		},
		{
			name:     "instructions only",
			profiles: `["instructions"]`,
			setup:    func(t *testing.T, agentsRoot string) {},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agentsRoot := t.TempDir()
			writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":`+test.profiles+`}`)
			test.setup(t, agentsRoot)
			if err := Validate(agentsRoot); err != nil {
				t.Fatalf("validate selected profiles: %v", err)
			}
		})
	}
}

func TestValidateLegacyTreeRequiresAllProfiles(t *testing.T) {
	agentsRoot := t.TempDir()
	writeFixture(t, filepath.Join(agentsRoot, "tools", "mcp.json"), `{"mcpServers":{}}`)
	writeFixture(t, filepath.Join(agentsRoot, "skills", "review", "SKILL.md"), "# Review\n")

	err := Validate(agentsRoot)
	if err == nil || !strings.Contains(err.Error(), "canonical instructions") {
		t.Fatalf("expected legacy instructions requirement, got %v", err)
	}
	writeFixture(t, filepath.Join(agentsRoot, "AGENTS.md"), "# Instructions\n")
	if err := Validate(agentsRoot); err != nil {
		t.Fatalf("validate complete legacy tree: %v", err)
	}
}

func TestValidateRejectsNonRegularOptionalInstructions(t *testing.T) {
	agentsRoot := t.TempDir()
	writeFixture(t, filepath.Join(agentsRoot, "manifest.json"), `{"version":"1.0.0","profiles":["instructions"]}`)
	if err := os.Mkdir(filepath.Join(agentsRoot, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Validate(agentsRoot)
	if err == nil || !strings.Contains(err.Error(), "canonical instructions") {
		t.Fatalf("expected non-regular instructions error, got %v", err)
	}
}

func TestExportAndImportTransferInstructionsForBothVendors(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex"} {
		t.Run(vendor, func(t *testing.T) {
			source := t.TempDir()
			writeCanonicalFixture(t, source)
			vendorRoot := t.TempDir()
			if err := Export(vendor, filepath.Join(source, ".agents"), vendorRoot, false); err != nil {
				t.Fatalf("export %s: %v", vendor, err)
			}
			data, err := os.ReadFile(filepath.Join(vendorRoot, "AGENTS.md"))
			if err != nil || string(data) != "# Shared instructions\n" {
				t.Fatalf("unexpected exported instructions: %q (%v)", data, err)
			}

			importRoot := t.TempDir()
			if err := Import(vendor, vendorRoot, filepath.Join(importRoot, ".agents"), false); err != nil {
				t.Fatalf("import %s: %v", vendor, err)
			}
			data, err = os.ReadFile(filepath.Join(importRoot, ".agents", "AGENTS.md"))
			if err != nil || string(data) != "# Shared instructions\n" {
				t.Fatalf("unexpected imported instructions: %q (%v)", data, err)
			}
		})
	}
}

func TestExportAndImportClaudeProjectsBridgeAndSkills(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	claude := t.TempDir()

	if err := Export("claude", filepath.Join(source, ".agents"), claude, false); err != nil {
		t.Fatalf("export Claude: %v", err)
	}
	for _, path := range []string{
		".mcp.json",
		"AGENTS.md",
		"CLAUDE.md",
		".claude/skills/review/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(claude, path)); err != nil {
			t.Fatalf("Claude projection %q was not created: %v", path, err)
		}
	}
	bridge, err := os.ReadFile(filepath.Join(claude, "CLAUDE.md"))
	if err != nil || string(bridge) != "@AGENTS.md\n" {
		t.Fatalf("unexpected Claude bridge: %q (%v)", bridge, err)
	}

	imported := t.TempDir()
	if err := Import("claude", claude, filepath.Join(imported, ".agents"), false); err != nil {
		t.Fatalf("import Claude: %v", err)
	}
	instructions, err := os.ReadFile(filepath.Join(imported, ".agents", "AGENTS.md"))
	if err != nil || string(instructions) != "# Shared instructions\n" {
		t.Fatalf("unexpected imported Claude instructions: %q (%v)", instructions, err)
	}
	if _, err := os.Stat(filepath.Join(imported, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("Claude skill was not imported: %v", err)
	}
}

func TestImportClaudeDoesNotParseCLAUDEAsInstructions(t *testing.T) {
	claude := t.TempDir()
	writeFixture(t, filepath.Join(claude, ".mcp.json"), `{"mcpServers":{}}`)
	writeFixture(t, filepath.Join(claude, "CLAUDE.md"), "arbitrary Claude content\n")
	imported := t.TempDir()

	if err := Import("claude", claude, filepath.Join(imported, ".agents"), false); err != nil {
		t.Fatalf("import Claude without AGENTS.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(imported, ".agents", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md was imported as canonical instructions: %v", err)
	}
}

func TestClaudeBridgeUsesForceAndBackup(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".mcp.json"), `{"mcpServers":{}}`)
	writeFixture(t, filepath.Join(output, "AGENTS.md"), "# Old instructions\n")
	writeFixture(t, filepath.Join(output, "CLAUDE.md"), "# Old bridge\n")

	if err := Export("claude", filepath.Join(source, ".agents"), output, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected bridge overwrite protection, got %v", err)
	}
	if err := ExportWithOptions("claude", filepath.Join(source, ".agents"), output, WriteOptions{Force: true, Backup: true}); err != nil {
		t.Fatalf("export Claude with backup: %v", err)
	}
	for _, path := range []string{"AGENTS.md.backup-*", "CLAUDE.md.backup-*", ".mcp.json.backup-*"} {
		backups, err := filepath.Glob(filepath.Join(output, path))
		if err != nil || len(backups) != 1 {
			t.Fatalf("expected backup for %q, got %v (%v)", path, backups, err)
		}
	}
}

func TestOpenCodeProjectionPreservesTopLevelFields(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	openCode := t.TempDir()
	writeFixture(t, filepath.Join(openCode, "opencode.json"), `{"theme":"dark","mcp":{"old":{"type":"local","command":["old"]}}}`)

	if err := Export("opencode", filepath.Join(source, ".agents"), openCode, true); err != nil {
		t.Fatalf("export OpenCode: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(openCode, "opencode.json"))
	if err != nil {
		t.Fatalf("read OpenCode configuration: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenCode configuration: %v", err)
	}
	if string(document["theme"]) != `"dark"` {
		t.Fatalf("unrelated OpenCode field was not preserved: %s", data)
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(document["mcp"], &servers); err != nil {
		t.Fatalf("parse projected OpenCode servers: %v", err)
	}
	var localType string
	var localCommand []string
	var localEnvironment map[string]string
	if err := json.Unmarshal(servers["local"]["type"], &localType); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(servers["local"]["command"], &localCommand); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(servers["local"]["environment"], &localEnvironment); err != nil {
		t.Fatal(err)
	}
	if localType != "local" || strings.Join(localCommand, ",") != "example-mcp,serve" || localEnvironment["TOKEN"] != "token" {
		t.Fatalf("unexpected local OpenCode server: %v", servers["local"])
	}
	var remoteType, remoteURL string
	var remoteHeaders map[string]string
	if err := json.Unmarshal(servers["remote"]["type"], &remoteType); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(servers["remote"]["url"], &remoteURL); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(servers["remote"]["headers"], &remoteHeaders); err != nil {
		t.Fatal(err)
	}
	if remoteType != "remote" || remoteURL != "https://example.test/mcp" || remoteHeaders["Authorization"] == "" {
		t.Fatalf("unexpected remote OpenCode server: %v", servers["remote"])
	}

	imported := t.TempDir()
	if err := Import("opencode", openCode, filepath.Join(imported, ".agents"), false); err != nil {
		t.Fatalf("import OpenCode: %v", err)
	}
	serversAfterImport, err := readCanonicalMCP(filepath.Join(imported, ".agents"))
	if err != nil {
		t.Fatalf("read imported OpenCode MCP: %v", err)
	}
	if serversAfterImport["local"].Command != "example-mcp" || serversAfterImport["local"].Env["TOKEN"] != "token" ||
		serversAfterImport["remote"].Type != "remote" || serversAfterImport["remote"].URL != "https://example.test/mcp" || serversAfterImport["remote"].Headers["Authorization"] == "" {
		t.Fatalf("unexpected imported OpenCode servers: %#v", serversAfterImport)
	}
}

func TestOpenCodeEnabledServerIsRepresentable(t *testing.T) {
	openCode := t.TempDir()
	writeFixture(t, filepath.Join(openCode, "opencode.json"), `{"mcp":{"local":{"type":"local","command":["run"],"enabled":true}}}`)
	imported := t.TempDir()
	if err := Import("opencode", openCode, filepath.Join(imported, ".agents"), false); err != nil {
		t.Fatalf("import enabled OpenCode server: %v", err)
	}

	writeFixture(t, filepath.Join(openCode, "opencode.json"), `{"mcp":{"local":{"type":"local","command":["run"],"enabled":false}}}`)
	if err := Import("opencode", openCode, filepath.Join(t.TempDir(), ".agents"), false); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled OpenCode server error, got %v", err)
	}
	writeFixture(t, filepath.Join(openCode, "opencode.json"), `{"mcp":{"local":{"type":"local","command":["run"],"enabled":"true"}}}`)
	if err := Import("opencode", openCode, filepath.Join(t.TempDir(), ".agents"), false); err == nil || !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatalf("expected non-boolean enabled error, got %v", err)
	}
}

func TestOpenCodeRejectsMalformedAndUnrepresentableServers(t *testing.T) {
	openCode := t.TempDir()
	writeFixture(t, filepath.Join(openCode, "opencode.json"), `{"mcp":{"bad":{"type":"local","command":"not-a-list"}}}`)
	if err := Import("opencode", openCode, filepath.Join(t.TempDir(), ".agents"), false); err == nil || !strings.Contains(err.Error(), "command as a list") {
		t.Fatalf("expected malformed OpenCode error, got %v", err)
	}

	source := t.TempDir()
	writeFixture(t, filepath.Join(source, ".agents", "tools", "mcp.json"), `{"mcpServers":{"local":{"command":"run","startup_timeout_sec":1}}}`)
	if err := Export("opencode", filepath.Join(source, ".agents"), t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "cannot project unsupported fields") {
		t.Fatalf("expected unrepresentable OpenCode error, got %v", err)
	}
}

func TestConvertOpenCodeToItselfDoesNotCopySkillsOntoThemselves(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "opencode.json"), `{"mcp":{"local":{"type":"local","command":["run"],"environment":{}}}}`)
	writeFixture(t, filepath.Join(root, ".agents", "skills", "review", "SKILL.md"), "# Review\n")
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Instructions\n")

	if err := Convert("opencode", "opencode", root, root, true); err != nil {
		t.Fatalf("convert OpenCode to itself: %v", err)
	}
}

func TestVendorCapabilities(t *testing.T) {
	for _, test := range []struct {
		vendor    string
		mcpPath   string
		skills    string
		mcpStatus string
	}{
		{vendor: "copilot", mcpPath: ".github/mcp.json", skills: ".agents/skills", mcpStatus: "cli-projection-only"},
		{vendor: "codex", mcpPath: ".codex/config.toml", skills: ".agents/skills", mcpStatus: "cli-projection-only"},
		{vendor: "claude", mcpPath: ".mcp.json", skills: ".claude/skills", mcpStatus: "planned"},
		{vendor: "opencode", mcpPath: "opencode.json", skills: ".agents/skills", mcpStatus: "workbench-projection-only"},
	} {
		t.Run(test.vendor, func(t *testing.T) {
			capabilities, err := VendorCapabilities(test.vendor)
			if err != nil {
				t.Fatalf("read capabilities: %v", err)
			}
			if capabilities.Vendor != test.vendor || capabilities.Status != "not-conformance-supported" || capabilities.Paths["mcp"] != test.mcpPath ||
				capabilities.Paths["instructions"] != "AGENTS.md" || capabilities.Paths["skills"] != test.skills {
				t.Fatalf("unexpected capabilities: %#v", capabilities)
			}
			if capabilities.ProfileStatus["mcp"] != test.mcpStatus || capabilities.Evidence == "" {
				t.Fatalf("unexpected compatibility summary: %#v", capabilities)
			}
			if strings.Join(capabilities.Profiles, ",") != "instructions,mcp,skills" {
				t.Fatalf("unexpected profiles: %#v", capabilities.Profiles)
			}
			if test.vendor == "claude" && capabilities.Paths["instructions_bridge"] != "CLAUDE.md" {
				t.Fatalf("unexpected Claude bridge path: %#v", capabilities.Paths)
			}
		})
	}
	if _, err := VendorCapabilities("unknown"); err == nil || !strings.Contains(err.Error(), "unsupported vendor") {
		t.Fatalf("expected unsupported vendor error, got %v", err)
	}
}

func writeCanonicalFixture(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".agents", "AGENTS.md"), "# Shared instructions\n")
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "example-mcp",
      "args": ["serve"],
      "env": {"TOKEN": "token"}
    },
    "remote": {
      "type": "remote",
      "url": "https://example.test/mcp",
      "headers": {"Authorization": "Bearer token"}
    }
  }
}
`)
	writeFixture(t, filepath.Join(root, ".agents", "skills", "review", "SKILL.md"), "# Review\n")
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
