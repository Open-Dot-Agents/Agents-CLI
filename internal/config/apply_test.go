package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexApplyPreservesUnrelatedTOMLAndTranslatesReferences(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixture(t, root)
	writeFixture(t, filepath.Join(root, ".codex", "config.toml"), "# keep this comment\nmodel = 'gpt-test'\n")
	result, err := ApplyProjection("codex", root, ApplyOptions{})
	if err != nil || !result.Applicable {
		t.Fatalf("apply Codex: %#v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"# keep this comment", "model = 'gpt-test'", "env_vars = ['TOKEN']", "[mcp_servers.remote.env_http_headers]", "Authorization = 'AUTH_TOKEN'"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in:\n%s", expected, text)
		}
	}
	if _, err := PlanProjection("codex", root, ApplyOptions{}); err != nil {
		t.Fatalf("idempotent plan: %v", err)
	}
}

func TestCodexRefusesEnvironmentRename(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["mcp"]}`)
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"server","env":{"TARGET":"urn:open-dot-agents:env:SOURCE"}}}}`)
	plan, err := PlanProjection("codex", root, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable || len(plan.Diagnostics) == 0 {
		t.Fatalf("lossy rename was accepted: %#v", plan)
	}
}

func TestJSONMergePreservesUnrelatedFieldsAndRefusesCollision(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	path := filepath.Join(root, ".github", "mcp.json")
	writeFixture(t, path, `{"editor":{"keep":true},"mcpServers":{"local":{"type":"stdio","command":"other"}}}`)
	plan, err := PlanProjection("copilot", root, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable || len(plan.Diagnostics) == 0 {
		t.Fatalf("expected collision diagnostic: %#v", plan)
	}
	if _, err := ApplyProjection("copilot", root, ApplyOptions{Force: true}); err != nil {
		t.Fatalf("forced apply: %v", err)
	}
	data, _ := os.ReadFile(path)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["editor"]; !ok {
		t.Fatalf("unrelated JSON field was removed: %s", data)
	}
}

func TestApplyAdoptsSemanticallyEquivalentJSON(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	writeFixture(t, filepath.Join(root, ".github", "mcp.json"), `{
  "mcpServers": {
    "local": { "command": "server", "type": "stdio" }
  }
}`)
	plan, err := PlanProjection("copilot", root, ApplyOptions{Adopt: true})
	if err != nil || !plan.Applicable {
		t.Fatalf("equivalent content was not adoptable: %#v, %v", plan, err)
	}
}

func TestApplyDetectsManagedDriftAndBacksUpForcedReplacement(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	if _, err := ApplyProjection("copilot", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".github", "mcp.json")
	writeFixture(t, path, `{"mcpServers":{"local":{"type":"stdio","command":"changed"}}}`)
	plan, err := PlanProjection("copilot", root, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable {
		t.Fatalf("managed drift was accepted: %#v", plan)
	}
	if _, err := ApplyProjection("copilot", root, ApplyOptions{Force: true, Backup: true}); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".backup-*")
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}
}

func TestClaudeProjectionUsesExpansionAndOwnedBridge(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixture(t, root)
	writeFixture(t, filepath.Join(root, "packages", "api", "AGENTS.md"), "# Scoped\n")
	if _, err := ApplyProjection("claude", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	mcp, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if !strings.Contains(string(mcp), "${TOKEN}") || !strings.Contains(string(mcp), "${AUTH_TOKEN}") {
		t.Fatalf("Claude references were not expanded safely: %s", mcp)
	}
	bridge, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(bridge) != "@AGENTS.md\n" {
		t.Fatalf("unexpected bridge: %q", bridge)
	}
	nestedBridge, _ := os.ReadFile(filepath.Join(root, "packages", "api", "CLAUDE.md"))
	if string(nestedBridge) != "@AGENTS.md\n" {
		t.Fatalf("unexpected nested bridge: %q", nestedBridge)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("Claude skill was not projected: %v", err)
	}
}

func TestClaudeApplyDeletesOnlyUnmodifiedOwnedSkill(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixture(t, root)
	if _, err := ApplyProjection("claude", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".agents", "skills", "review")); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("claude", root, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("removed canonical skill left an owned projection: %v", err)
	}
}

func TestSyncAllAppliesEveryStableVendorAndBecomesIdempotent(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)

	result, err := ApplySync("all", root, ApplyOptions{})
	if err != nil || !result.Applicable || len(result.Vendors) != 3 {
		t.Fatalf("sync all: %#v, %v", result, err)
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

	plan, err := PlanSync("all", root, ApplyOptions{})
	if err != nil || !plan.Applicable {
		t.Fatalf("plan synchronized repository: %#v, %v", plan, err)
	}
	for _, vendor := range plan.Vendors {
		for _, action := range vendor.Actions {
			if action.Operation != "unchanged" {
				t.Fatalf("sync was not idempotent: %#v", plan)
			}
		}
	}
}

func TestSyncAllPreflightFailureDoesNotWriteAnotherVendor(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	writeFixture(t, filepath.Join(root, ".codex", "config.toml"), "[mcp_servers.local]\ncommand = 'different'\n")

	result, err := ApplySync("all", root, ApplyOptions{})
	if err == nil || result.Applicable || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("expected Codex preflight conflict: %#v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("Copilot output was written before all-vendor preflight completed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("Claude output was written before all-vendor preflight completed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", ".state", "reference-cli", "copilot.json")); !os.IsNotExist(err) {
		t.Fatalf("ownership state was written after failed preflight: %v", err)
	}
}

func TestSyncTransactionRollsBackAfterWriteFailure(t *testing.T) {
	root := t.TempDir()
	writeRepositoryFixtureWithoutSecrets(t, root)
	originalCodex := "# keep\nmodel = 'gpt-test'\n"
	writeFixture(t, filepath.Join(root, ".codex", "config.toml"), originalCodex)
	prepared, result, err := prepareSync("all", root, ApplyOptions{})
	if err != nil || !result.Applicable {
		t.Fatalf("prepare sync: %#v, %v", result, err)
	}

	writes := 0
	failingWriter := func(path string, data []byte, permissions fs.FileMode) error {
		writes++
		if writes == 5 {
			return errors.New("injected write failure")
		}
		return atomicWrite(path, data, permissions)
	}
	err = applyPreparedProjections(prepared, ApplyOptions{}, failingWriter)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("expected injected transaction failure, got %v", err)
	}
	for _, path := range []string{
		".github/mcp.json",
		".mcp.json",
		".agents/.state/reference-cli/copilot.json",
		".agents/.state/reference-cli/codex.json",
		".agents/.state/reference-cli/claude.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("transaction left %q after rollback: %v", path, err)
		}
	}
	codex, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil || string(codex) != originalCodex {
		t.Fatalf("transaction did not restore Codex configuration: %q (%v)", codex, err)
	}
	for _, path := range []string{".github", ".agents/.state"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("transaction left directory %q after rollback: %v", path, err)
		}
	}
}

func writeRepositoryFixture(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "AGENTS.md"), "# Instructions\n")
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["instructions","mcp","skills"]}`)
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{
  "mcpServers": {
    "local": {"type":"stdio","command":"server","env":{"TOKEN":"urn:open-dot-agents:env:TOKEN"}},
    "remote": {"type":"remote","url":"https://example.test/mcp","headers":{"Authorization":"urn:open-dot-agents:env:AUTH_TOKEN"}}
  }
}`)
	writeFixture(t, filepath.Join(root, ".agents", "skills", "review", "SKILL.md"), "# Review\n")
}

func writeRepositoryFixtureWithoutSecrets(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.0.0","profiles":["mcp"]}`)
	writeFixture(t, filepath.Join(root, ".agents", "tools", "mcp.json"), `{"mcpServers":{"local":{"type":"stdio","command":"server"}}}`)
}
