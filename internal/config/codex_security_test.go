package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func codexSubsetPolicy(t *testing.T) SecurityPolicy {
	t.Helper()
	var sandbox SandboxPolicy
	if err := json.Unmarshal([]byte(draftSandbox), &sandbox); err != nil {
		t.Fatal(err)
	}
	sandbox.Coverage = []string{"shell"}
	sandbox.Filesystem.Default = "read"
	sandbox.Filesystem.Runtime = []string{"process"}
	sandbox.Filesystem.Rules = []PathRule{{".", "write"}, {".agents", "read"}, {".codex", "read"}, {".git", "read"}, {"private", "deny"}, {"docs", "read"}, {"docs/edit", "write"}}
	sandbox.Network.Web = "allow"
	sandbox.Network.RemoteMCP = "allow"
	sandbox.Credentials.Environment = "inherit"
	sandbox.Credentials.Files = "inherit"
	return SecurityPolicy{Sandbox: &sandbox, Permissions: &PermissionsPolicy{Version: ExperimentalVersion, Coverage: []string{"shell"}, Default: "allow", Rules: []PermissionRule{}}}
}

func TestCodexSubsetPreservesRestrictions(t *testing.T) {
	policy := codexSubsetPolicy(t)
	settings, err := codexPolicySettings(policy, map[string]bool{"sandbox": true, "permissions": true})
	if err != nil {
		t.Fatal(err)
	}
	profiles := settings["permissions"].(map[string]any)
	profile := profiles[codexSecurityProfile].(map[string]any)
	filesystem := profile["filesystem"].(map[string]any)
	paths := filesystem[":workspace_roots"].(map[string]any)
	if paths["docs/edit"] != "read" || paths["private"] != "deny" || paths["."] != "write" {
		t.Fatal(paths)
	}
	if profile["network"].(map[string]any)["enabled"] != false || settings["approval_policy"] != "never" {
		t.Fatal(settings)
	}
	for name, change := range map[string]func(*SecurityPolicy){
		"read-isolation":            func(p *SecurityPolicy) { p.Sandbox.Filesystem.Default = "deny" },
		"missing-runtime-grant":     func(p *SecurityPolicy) { p.Sandbox.Filesystem.Runtime = nil },
		"credential-env-isolation":  func(p *SecurityPolicy) { p.Sandbox.Credentials.Environment = "none" },
		"credential-file-isolation": func(p *SecurityPolicy) { p.Sandbox.Credentials.Files = "deny" },
		"scope-delegation":          func(p *SecurityPolicy) { p.Sandbox.Coverage = append(p.Sandbox.Coverage, "delegation") },
		"domain-allow": func(p *SecurityPolicy) {
			p.Sandbox.Network.Rules = []NetworkRule{{Host: "example.com", Ports: []int{443}, Effect: "allow"}}
		},
		"web-isolation": func(p *SecurityPolicy) { p.Sandbox.Network.Web = "deny" },
		"approval-ask":  func(p *SecurityPolicy) { p.Permissions.Default = "ask" },
		"exact-command": func(p *SecurityPolicy) {
			p.Permissions.Rules = []PermissionRule{{Kind: "process", Name: "git", Args: []string{"status"}, Effect: "allow"}}
		},
		"writable-policy": func(p *SecurityPolicy) { p.Sandbox.Filesystem.Rules = []PathRule{{".", "write"}} },
	} {
		t.Run(name, func(t *testing.T) {
			p := codexSubsetPolicy(t)
			change(&p)
			if _, err := codexPolicySettings(p, map[string]bool{}); err == nil {
				t.Fatal("unsupported control was accepted")
			}
		})
	}
	for _, selected := range []string{"tools", "hooks", "skills"} {
		if _, err := codexPolicySettings(policy, map[string]bool{selected: true}); err == nil {
			t.Fatal("combined profile accepted")
		}
	}
}

func TestCodexSecurityMergeLifecycle(t *testing.T) {
	settings, err := codexPolicySettings(codexSubsetPolicy(t), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("# Keep user comments\nmodel = 'example-model'\n\n[mcp_servers.example]\ncommand = 'example'\n")
	merged, hash, err := mergeCodexSecurity(original, settings, "", ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(merged, original) {
		t.Fatal("unrelated bytes changed")
	}
	again, nextHash, err := mergeCodexSecurity(merged, settings, hash, ApplyOptions{})
	if err != nil || !bytes.Equal(again, merged) || nextHash != hash {
		t.Fatalf("idempotence: %v", err)
	}
	if _, _, err := mergeCodexSecurity(merged, settings, "", ApplyOptions{}); err == nil {
		t.Fatal("unowned native settings adopted")
	}
	if _, _, err := mergeCodexSecurity(merged, settings, "", ApplyOptions{Adopt: true}); err != nil {
		t.Fatal(err)
	}
	modified := bytes.Replace(merged, []byte("enabled = false"), []byte("enabled = true"), 1)
	if _, _, err := mergeCodexSecurity(modified, settings, hash, ApplyOptions{}); err == nil {
		t.Fatal("modified security overwritten")
	}
	forced, _, err := mergeCodexSecurity(modified, settings, hash, ApplyOptions{Force: true, Backup: true})
	if err != nil || !bytes.Equal(forced, merged) {
		t.Fatalf("forced restoration: %v", err)
	}
	restored, nextHash, err := mergeCodexSecurity(merged, nil, hash, ApplyOptions{})
	if err != nil || nextHash != "" || !bytes.Equal(restored, original) {
		t.Fatalf("removal changed unowned content: %v", err)
	}
	if _, _, err := mergeCodexSecurity([]byte("approval_policy = 'never'\n"), settings, "", ApplyOptions{Force: true, Backup: true}); err == nil {
		t.Fatal("force replaced unmarked security")
	}
	injected := bytes.Replace(merged, []byte(securitySelectorsEnd), []byte("personality = 'injected'\n"+securitySelectorsEnd), 1)
	if _, _, err := mergeCodexSecurity(injected, settings, hash, ApplyOptions{Force: true, Backup: true}); err == nil {
		t.Fatal("force removed unrelated native setting")
	}
	// Header-like text in a multiline value is not a real managed segment.
	fake := []byte("model = '''\n" + securitySelectorsBegin + "approval_policy='never'\n" + securitySelectorsEnd + securityProfileBegin + "[permissions.open-dot-agents]\n" + securityProfileEnd + "'''\n")
	if _, _, err := stripCodexSecurity(fake); err == nil {
		t.Fatal("markers inside a string accepted")
	}
}

func TestCodexSecurityTransactionRollback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".codex", "config.toml")
	writeFixture(t, path, "model = 'original'\n")
	before := portabilitySnapshot(t, root)
	state := ownershipState{Version: ownershipVersion, Vendor: "codex", Entries: map[string]string{}, Files: map[string]string{}, SecurityHash: "new-hash"}
	projection := preparedProjection{result: PlanResult{Root: root, Vendor: "codex", Applicable: true}, writes: map[string][]byte{path: []byte("model = 'changed'\n")}, state: state}
	err := applyPreparedProjections([]preparedProjection{projection}, ApplyOptions{}, func(target string, data []byte, mode fs.FileMode) error {
		if err := atomicWrite(target, data, mode); err != nil {
			return err
		}
		if target == path {
			return errors.New("injected failure after native write")
		}
		return nil
	})
	if err == nil || !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
		t.Fatal("security transaction did not restore native configuration and ownership")
	}
}

func TestCodexPolicyRejectsMissingPathsAndAliases(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux path enforcement preflight")
	}
	root := t.TempDir()
	policy := codexSubsetPolicy(t)
	for _, name := range []string{".git", ".agents", "docs/edit", "private"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(root, "private", "marker")
	writeFixture(t, secret, "marker")
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Link(secret, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("hard-link alias accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "readonly-outside")
	writeFixture(t, outside, "outside marker")
	if err := os.Link(outside, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("writable alias of an outside read-only file accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "private", "link")); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("denied tree symlink accepted")
	}
	policy.Sandbox.Filesystem.Rules = append(policy.Sandbox.Filesystem.Rules, PathRule{"future", "deny"})
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("future deny path accepted")
	}
}

func TestCodexAuthorityDoesNotGrantTrust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	writeFixture(t, path, "")
	if _, err := codexAuthority(root, home); err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("untrusted home: %v", err)
	}
	data, _ := json.Marshal(root)
	writeFixture(t, path, "[projects."+string(data)+"]\ntrust_level='trusted'\n")
	if _, err := codexAuthority(root, home); err != nil {
		t.Fatal(err)
	}
	before := portabilitySnapshot(t, home)
	if _, err := codexAuthority(root, home); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, portabilitySnapshot(t, home)) {
		t.Fatal("authority preflight changed native trust")
	}
	writeFixture(t, path, "sandbox_mode='danger-full-access'\n[projects."+string(data)+"]\ntrust_level='trusted'\n")
	if _, err := codexAuthority(root, home); err == nil {
		t.Fatal("legacy sandbox authority accepted")
	}
	writeFixture(t, filepath.Join(home, "auth.json"), "{}")
	if _, err := codexAuthority(root, home); err == nil {
		t.Fatal("account-bearing home accepted")
	}
}
