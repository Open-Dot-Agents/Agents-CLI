package main

import (
	"bytes"
	"encoding/json"
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

func TestRunValidateUsesLocalDefault(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)

	if err := run([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("initialize canonical tree: %v", err)
	}
	if err := run([]string{"validate"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("validate canonical tree: %v", err)
	}
}

func TestRunCapabilitiesWritesJSON(t *testing.T) {
	for _, test := range []struct {
		vendor string
		status string
		mcp    string
	}{
		{vendor: "codex", status: "not-conformance-supported", mcp: ".codex/config.toml"},
		{vendor: "claude", status: "not-conformance-supported", mcp: ".mcp.json"},
	} {
		t.Run(test.vendor, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			if err := run([]string{"capabilities", "--vendor", test.vendor}, stdout, &bytes.Buffer{}); err != nil {
				t.Fatalf("read capabilities: %v", err)
			}
			var capabilities struct {
				Vendor        string            `json:"vendor"`
				Status        string            `json:"status"`
				Paths         map[string]string `json:"paths"`
				ProfileStatus map[string]string `json:"profile_status"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &capabilities); err != nil {
				t.Fatalf("capabilities output is not JSON: %v", err)
			}
			if capabilities.Vendor != test.vendor || capabilities.Status != test.status || capabilities.Paths["mcp"] != test.mcp {
				t.Fatalf("unexpected capabilities: %#v", capabilities)
			}
			if capabilities.ProfileStatus["mcp"] == "" {
				t.Fatalf("capabilities did not include profile status: %#v", capabilities)
			}
		})
	}
}

func TestRunVersionWritesVersion(t *testing.T) {
	previous := version
	version = "1.0.0-test"
	t.Cleanup(func() {
		version = previous
	})

	stdout := &bytes.Buffer{}
	if err := run([]string{"version"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if stdout.String() != "agents 1.0.0-test\n" {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}
}

func TestRunExportDiffIncludesInstructions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers": {}}`)
	writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "# First\n")
	withWorkingDirectory(t, root)

	if err := run([]string{"export", "--vendor", "copilot"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("initial export: %v", err)
	}
	writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "# Second\n")
	stdout := &bytes.Buffer{}
	if err := run([]string{"export", "--vendor", "copilot", "--force", "--diff"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("forced export with diff: %v", err)
	}
	if !strings.Contains(stdout.String(), "M  AGENTS.md") {
		t.Fatalf("expected instruction diff summary, got %q", stdout.String())
	}
}

func TestRunExperimentalExportDiffIncludesManagedPaths(t *testing.T) {
	tests := []struct {
		vendor string
		paths  []string
	}{
		{vendor: "claude", paths: []string{".mcp.json", "AGENTS.md", "CLAUDE.md"}},
		{vendor: "opencode", paths: []string{"opencode.json", "AGENTS.md"}},
	}
	for _, test := range tests {
		t.Run(test.vendor, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers": {}}`)
			writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "# Instructions\n")
			withWorkingDirectory(t, root)

			stdout := &bytes.Buffer{}
			if err := run([]string{"export", "--vendor", test.vendor, "--diff"}, stdout, &bytes.Buffer{}); err != nil {
				t.Fatalf("export %s: %v", test.vendor, err)
			}
			for _, path := range test.paths {
				if !strings.Contains(stdout.String(), "A  "+path) {
					t.Fatalf("expected %q in diff summary, got %q", path, stdout.String())
				}
			}
		})
	}
}

func TestRunExportDiffHonorsManifestProfiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["instructions"]}`)
	writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "# Instructions\n")
	writeFile(t, filepath.Join(root, ".github", "mcp.json"), `{"mcpServers":{"keep":{"command":"keep"}}}`)
	withWorkingDirectory(t, root)

	stdout := &bytes.Buffer{}
	if err := run([]string{"export", "--vendor", "copilot", "--diff"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("export selected manifest profile: %v", err)
	}
	if !strings.Contains(stdout.String(), "A  AGENTS.md") || strings.Contains(stdout.String(), ".github/mcp.json") {
		t.Fatalf("unexpected selected-profile diff: %q", stdout.String())
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
