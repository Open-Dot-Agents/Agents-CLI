package config

import (
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

func TestExportBacksUpExistingConfigurationAndSkills(t *testing.T) {
	source := t.TempDir()
	writeCanonicalFixture(t, source)
	output := t.TempDir()
	writeFixture(t, filepath.Join(output, ".github", "mcp.json"), `{"mcpServers":{"old":{"command":"old"}}}`)
	writeFixture(t, filepath.Join(output, ".agents", "skills", "old", "SKILL.md"), "# Old\n")

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

func TestInitCreatesExportableStarterTree(t *testing.T) {
	root := t.TempDir()
	if err := Init(root, false); err != nil {
		t.Fatalf("initialize starter tree: %v", err)
	}
	agentsRoot := filepath.Join(root, ".agents")
	for _, path := range []string{
		"AGENTS.md",
		"tools/mcp.json",
		"skills/example/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(agentsRoot, path)); err != nil {
			t.Fatalf("starter file %q was not created: %v", path, err)
		}
	}
	if _, err := readCanonicalMCP(agentsRoot); err != nil {
		t.Fatalf("read starter MCP configuration: %v", err)
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

func writeCanonicalFixture(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "example-mcp",
      "args": ["serve"],
      "env": {"TOKEN": "token"}
    },
    "remote": {
      "type": "http",
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
