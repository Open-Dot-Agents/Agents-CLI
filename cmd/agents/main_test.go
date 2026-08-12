package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExportUsesLocalDefaults(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers": {}}`)
	withWorkingDirectory(t, root)

	stdout := &bytes.Buffer{}
	if err := run([]string{"export", "--vendor", "copilot", "--diff"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("export with defaults: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "mcp.json")); err != nil {
		t.Fatalf("default target was not written: %v", err)
	}
	if !strings.Contains(stdout.String(), "A  .github/mcp.json") {
		t.Fatalf("expected diff summary, got %q", stdout.String())
	}
}

func TestRunExportDiffShowsForcedModification(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers": {}}`)
	withWorkingDirectory(t, root)

	if err := run([]string{"export", "--vendor", "copilot"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("initial export: %v", err)
	}
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"example":{"command":"example-mcp"}}}`)
	stdout := &bytes.Buffer{}
	if err := run([]string{"export", "--vendor", "copilot", "--force", "--diff"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("forced export with diff: %v", err)
	}
	if !strings.Contains(stdout.String(), "M  .github/mcp.json") {
		t.Fatalf("expected modified MCP summary, got %q", stdout.String())
	}
}

func TestRunImportUsesLocalDefaults(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "mcp.json"), `{"mcpServers": {}}`)
	withWorkingDirectory(t, root)

	if err := run([]string{"import", "--vendor", "copilot"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("import with defaults: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "tools", "mcp.json")); err != nil {
		t.Fatalf("default target was not written: %v", err)
	}
}

func withWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatal(err)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
