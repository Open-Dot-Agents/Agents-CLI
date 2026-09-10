package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const draftPermissions = `{"version":"1.1.0-draft.1","coverage":["builtin-tools","shell"],"default":"ask","rules":[{"kind":"process","name":"git","args":["status"],"effect":"allow"}]}`
const draftSandbox = `{"version":"1.1.0-draft.1","coverage":["shell"],"filesystem":{"default":"deny","rules":[{"path":".","access":"write"},{"path":"private","access":"deny"}]},"network":{"default":"deny","rules":[],"local":"deny","private":"deny","web":"deny","remoteMCP":"deny"},"credentials":{"environment":"none","allow":[],"files":"deny"}}`

func securityFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, ".agents", "AGENTS.md"), "Use the policy.\n")
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.1.0-draft.1","profiles":["permissions","sandbox"]}`)
	writeFixture(t, filepath.Join(root, ".agents", "permissions", "permissions.json"), draftPermissions)
	writeFixture(t, filepath.Join(root, ".agents", "sandbox", "sandbox.json"), draftSandbox)
	return root
}
func TestSecurityDraftGateAndRefusal(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot", "claude"} {
		t.Run(vendor, func(t *testing.T) {
			root := securityFixture(t)
			canonical := filepath.Join(root, ".agents")
			if err := ValidateRepository(canonical); err == nil || !strings.Contains(err.Error(), "ODA-SECURITY-0001") {
				t.Fatalf("gate: %v", err)
			}
			if err := ValidateRepositoryWithOptions(canonical, true); err != nil {
				t.Fatal(err)
			}
			// A native conflict and bypass request cannot force draft activation.
			writeFixture(t, filepath.Join(root, ".codex", "config.toml"), "sandbox_mode = 'danger-full-access'\napproval_policy = 'never'\n")
			t.Setenv("COPILOT_SANDBOX_ALLOW_UNSANDBOXED_PROCESSES", "true")
			t.Setenv("ODA_TEST_SECRET", "do-not-copy-this-value")
			before := portabilitySnapshot(t, root)
			for _, options := range []ApplyOptions{{Experimental: true}, {Experimental: true, Force: true, Backup: true, Adopt: true}} {
				for i := 0; i < 2; i++ {
					plan, err := PlanProjection(vendor, root, options)
					if err != nil || plan.Applicable || plan.Security == nil || len(plan.Actions) != 0 {
						t.Fatalf("plan: %#v %v", plan, err)
					}
					if plan.Security.Status != "refused" || len(plan.Security.ProjectedSettings) != 0 || len(plan.Security.UnresolvedControls) == 0 {
						t.Fatalf("invalid security plan: %#v", plan.Security)
					}
					data, _ := json.Marshal(plan)
					if strings.Contains(string(data), "do-not-copy-this-value") {
						t.Fatal("secret in report")
					}
					if _, err := ApplyProjection(vendor, root, options); err == nil {
						t.Fatal("apply accepted security")
					}
					if _, err := ApplySync("all", root, options); err == nil {
						t.Fatal("sync accepted security")
					}
				}
			}
			for _, experimental := range []bool{false, true} {
				options := WriteOptions{Experimental: experimental, Force: true, Backup: true}
				if err := ExportWithOptions(vendor, canonical, root, options); err == nil {
					t.Fatal("export dropped policy")
				}
				if err := ImportRepositoryWithOptions(vendor, root, options); err == nil {
					t.Fatal("import replaced draft")
				}
				if err := ImportWithOptions(vendor, root, canonical, options); err == nil {
					t.Fatal("legacy import replaced draft")
				}
			}
			if !reflect.DeepEqual(before, portabilitySnapshot(t, root)) {
				t.Fatal("refusal changed files, backups, or ownership")
			}
		})
	}
}
func TestSecurityValidationRejectsAmbiguity(t *testing.T) {
	cases := map[string]string{
		"unknown":      strings.Replace(draftPermissions, `"default":"ask"`, `"default":"ask","native_allow_all":true`, 1),
		"duplicate":    strings.Replace(draftPermissions, `"default":"ask"`, `"default":"ask","default":"allow"`, 1),
		"null":         strings.Replace(draftPermissions, `"rules":[{"kind":"process","name":"git","args":["status"],"effect":"allow"}]`, `"rules":null`, 1),
		"missing-args": strings.Replace(draftPermissions, `,"args":["status"]`, "", 1),
		"tool-args":    strings.Replace(draftPermissions, `"kind":"process"`, `"kind":"tool"`, 1),
		"coverage":     strings.Replace(draftPermissions, `"shell"`, `"unverified-scope"`, 1),
		"effect":       strings.Replace(draftPermissions, `"effect":"allow"`, `"effect":"bypass"`, 1),
		"extension":    strings.TrimSuffix(draftPermissions, "}") + `,"extensions":{"codex":{"required":true,"data":{}}}}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			root := securityFixture(t)
			writeFixture(t, filepath.Join(root, ".agents", "permissions", "permissions.json"), data)
			if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	for _, path := range []string{"../escape", "/etc", "a/../b", "a//b", "./a", "a\\b", "a/", "a/*", "a/.."} {
		t.Run(path, func(t *testing.T) {
			root := securityFixture(t)
			data := strings.Replace(draftSandbox, `"path":"private"`, `"path":`+strconvJSON(path), 1)
			writeFixture(t, filepath.Join(root, ".agents", "sandbox", "sandbox.json"), data)
			if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
				t.Fatal("invalid path accepted")
			}
		})
	}
}
func strconvJSON(value string) string { data, _ := json.Marshal(value); return string(data) }
func TestSecurityPolicySymlinkRefused(t *testing.T) {
	root := securityFixture(t)
	path := filepath.Join(root, ".agents", "sandbox", "sandbox.json")
	target := filepath.Join(t.TempDir(), "policy.json")
	writeFixture(t, target, draftSandbox)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip(err)
	}
	if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err == nil {
		t.Fatal("policy symlink accepted")
	}
}
func TestSecurityRuleOrderAndApproval(t *testing.T) {
	var p PermissionsPolicy
	if err := json.Unmarshal([]byte(draftPermissions), &p); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		args    []string
		channel bool
		want    string
	}{
		{"git", []string{"status"}, false, "allow"},
		{"git", []string{"status", "--other"}, false, "deny"},
		{"git", []string{"status; touch marker"}, false, "deny"},
		{"sh", []string{"-c", "git status"}, true, "ask"},
		{"sh", []string{"-c", "git status"}, false, "deny"},
	}
	for _, test := range tests {
		if got := permissionDecision(p, "process", test.name, test.args, test.channel); got != test.want {
			t.Errorf("%v: %s", test, got)
		}
	}
	p.Rules = append(p.Rules, PermissionRule{Kind: "process", Name: "*", Args: []string{"status"}, Effect: "deny"})
	if got := permissionDecision(p, "process", "git", []string{"status"}, true); got != "deny" {
		t.Fatal(got)
	}
	p.Rules[0], p.Rules[1] = p.Rules[1], p.Rules[0]
	if got := permissionDecision(p, "process", "git", []string{"status"}, true); got != "deny" {
		t.Fatal(got)
	}
}
func TestSecurityFilesystemOverlaps(t *testing.T) {
	var p SandboxPolicy
	_ = json.Unmarshal([]byte(draftSandbox), &p)
	p.Filesystem.Rules = append(p.Filesystem.Rules, PathRule{Path: "private/open", Access: "write"}, PathRule{Path: "docs", Access: "read"}, PathRule{Path: "docs/edit", Access: "write"})
	for path, want := range map[string]string{"private": "deny", "private/open/file": "deny", "private-other": "write", "docs/edit/file": "read", "src/file": "write", ".env": "write", ".git/config": "write"} {
		got, err := filesystemAccess(p, path)
		if err != nil || got != want {
			t.Errorf("%s: %s %v", path, got, err)
		}
	}
	if _, err := filesystemAccess(p, "private/../src"); err == nil {
		t.Fatal("traversal accepted")
	}
}
func TestSecurityNormalizationAndExtensions(t *testing.T) {
	root := securityFixture(t)
	data := strings.TrimSuffix(draftPermissions, "}") + `,"extensions":{"com.example.policy":{"required":true,"data":{"enabled":true}}}}`
	writeFixture(t, filepath.Join(root, ".agents", "permissions", "permissions.json"), data)
	plan, err := PlanProjection("codex", root, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Diagnostics, " "), "ODA-SECURITY-0003") {
		t.Fatal(plan.Diagnostics)
	}
	if !reflect.DeepEqual(plan.Security.Declared.Permissions.Extensions, plan.Security.Normalized.Permissions.Extensions) {
		t.Fatal("extension changed")
	}
	p := plan.Security.Declared
	p.Permissions.Rules = append(p.Permissions.Rules, PermissionRule{Kind: "process", Name: "git", Args: []string{"status"}, Effect: "ask"}, PermissionRule{Kind: "process", Name: "true", Args: []string{}, Effect: "allow"})
	normalized := normalizeSecurityPolicy(p)
	if len(normalized.Permissions.Rules) != 2 || !reflect.DeepEqual(normalized, normalizeSecurityPolicy(normalized)) {
		t.Fatal("normalization is not idempotent")
	}
	if got := permissionDecision(*normalized.Permissions, "process", "git", []string{"status"}, true); got != "ask" {
		t.Fatal(got)
	}
	encoded, _ := json.Marshal(normalized.Permissions)
	writeFixture(t, filepath.Join(root, ".agents", "permissions", "permissions.json"), string(encoded))
	if err := ValidateRepositoryWithOptions(filepath.Join(root, ".agents"), true); err != nil {
		t.Fatalf("normalized policy invalid: %v", err)
	}
}
func TestSecurityRemovalPreservesStableProjection(t *testing.T) {
	root := securityFixture(t)
	// No security settings have been owned. Removing selection must leave native
	// security settings unchanged and allow ordinary 1.0 content projection.
	writeFixture(t, filepath.Join(root, ".agents", "manifest.json"), `{"version":"1.1.0-draft.1","profiles":[]}`)
	path := filepath.Join(root, ".codex", "config.toml")
	writeFixture(t, path, "sandbox_mode = 'read-only'\n")
	for i := 0; i < 2; i++ {
		if _, err := ApplyProjection("codex", root, ApplyOptions{Experimental: true}); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != "sandbox_mode = 'read-only'\n" {
		t.Fatal("unowned security changed")
	}
	if err := ValidateRepository(filepath.Join(root, ".agents")); err == nil {
		t.Fatal("draft bypassed stable gate")
	}
}
func TestSecurityCapabilitiesDoNotMutateStableState(t *testing.T) {
	before, _ := VendorCapabilities("codex")
	draft, _ := VendorCapabilitiesWithOptions("codex", true)
	after, _ := VendorCapabilities("codex")
	if draft.Experimental == nil || draft.Experimental.Status != "subset-available" || !reflect.DeepEqual(before, after) {
		t.Fatal("capability status widened")
	}
}
