package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitValidateAndPlanApply(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	if err := run([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := run([]string{"validate", "--format", "json"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	stdout := &bytes.Buffer{}
	if err := run([]string{"plan", "--vendor", "codex", "--format", "json"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	var plan struct {
		Applicable bool `json:"applicable"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil || !plan.Applicable {
		t.Fatalf("unexpected plan: %s (%v)", stdout.String(), err)
	}
	if err := run([]string{"apply", "--vendor", "codex"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, path := range []string{".codex/config.toml", ".agents/.state/reference-cli/codex.json"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("missing applied file %s: %v", path, err)
		}
	}
	if err := run([]string{"plan", "--vendor", "codex", "--check"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("idempotent check: %v", err)
	}
}

func TestRunApplyRefusesUnsupportedCopilotSecretReference(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["mcp"]}`)
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"secret":{"type":"stdio","command":"server","env":{"TOKEN":"urn:open-dot-agents:env:TOKEN"}}}}`)
	withWorkingDirectory(t, root)
	stdout := &bytes.Buffer{}
	err := run([]string{"apply", "--vendor", "copilot"}, stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot safely represent") {
		t.Fatalf("expected loss diagnostic, got %v (%s)", err, stdout.String())
	}
	if _, statErr := os.Stat(filepath.Join(root, ".github", "mcp.json")); !os.IsNotExist(statErr) {
		t.Fatalf("lossy apply wrote native configuration: %v", statErr)
	}
}

func TestRunSyncAllAndReadOnlyCheck(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	if err := run([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout := &bytes.Buffer{}
	if err := run([]string{"sync", "--vendor", "all", "--format", "json"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sync all: %v (%s)", err, stdout.String())
	}
	var result struct {
		Applicable bool `json:"applicable"`
		Vendors    []struct {
			Vendor string `json:"vendor"`
		} `json:"vendors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Applicable || len(result.Vendors) != 3 {
		t.Fatalf("unexpected sync result: %s (%v)", stdout.String(), err)
	}
	for _, path := range []string{
		".github/mcp.json",
		".codex/config.toml",
		".mcp.json",
		".agents/.state/reference-cli/copilot.json",
		".agents/.state/reference-cli/codex.json",
		".agents/.state/reference-cli/claude.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing synchronized file %q: %v", path, err)
		}
	}
	if err := run([]string{"sync", "--vendor", "all", "--check"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("idempotent sync check: %v", err)
	}
}

func TestRunApplyCheckDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	withWorkingDirectory(t, root)
	if err := run([]string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	err := run([]string{"apply", "--vendor", "codex", "--check"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "changes are required") {
		t.Fatalf("expected stale projection check, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("apply --check changed the repository: %v", err)
	}
}

func TestRunSyncCheckFailsForNonApplicableVendorWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["mcp"]}`)
	writeFile(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"secret":{"type":"stdio","command":"server","env":{"TOKEN":"urn:open-dot-agents:env:TOKEN"}}}}`)
	withWorkingDirectory(t, root)
	stdout := &bytes.Buffer{}
	err := run([]string{"sync", "--vendor", "all", "--check"}, stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not applicable") || !strings.Contains(stdout.String(), "copilot") {
		t.Fatalf("expected non-applicable sync check, got %v (%s)", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("sync --check changed the repository: %v", err)
	}
}

func TestRunCapabilitiesWritesJSON(t *testing.T) {
	for _, vendor := range []string{"copilot", "codex", "claude"} {
		stdout := &bytes.Buffer{}
		if err := run([]string{"capabilities", "--vendor", vendor}, stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("capabilities %s: %v", vendor, err)
		}
		var capabilities struct {
			Vendor string `json:"vendor"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &capabilities); err != nil || capabilities.Vendor != vendor || capabilities.Status == "" {
			t.Fatalf("unexpected capabilities: %s (%v)", stdout.String(), err)
		}
	}
}

func TestRemovedCommandsAreRejected(t *testing.T) {
	for _, command := range []string{"export", "convert"} {
		if err := run([]string{command}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("removed command %s was accepted: %v", command, err)
		}
	}
}

func TestRunVersionWritesVersion(t *testing.T) {
	previous := version
	version = "1.0.0-test"
	t.Cleanup(func() { version = previous })
	stdout := &bytes.Buffer{}
	if err := run([]string{"version"}, stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "agents 1.0.0-test\n" {
		t.Fatalf("unexpected version output: %q", stdout.String())
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
