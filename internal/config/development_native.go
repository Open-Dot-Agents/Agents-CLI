package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func developmentInstructions(root string, p *DevelopmentPolicy) (string, error) {
	path := filepath.Join(root, developmentGuardrailsPath)
	if err := requireRegularFile(path, "development guardrails"); err != nil {
		return "", err
	}
	data, err := nativeReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n\nDevelopment decisions (agent guidance):\n- Project work: %s\n- Local commits: %s\n- External changes: %s\n- Destructive work: %s\n- Protected paths: %s\n", data, p.ProjectWork, p.LocalCommits, p.ExternalChanges, p.DestructiveWork, strings.Join(p.ProtectedPaths, ", ")), nil
}

func addPracticalDevelopment(vendor, root, base string, security *SecurityPlan, plan *PlanResult, migrateLegacy bool, add func(string, string, string, any, string, bool) error) error {
	p := security.Declared.Development
	guidance, err := developmentInstructions(root, p)
	if err != nil {
		return err
	}
	plan.Warnings = append(plan.Warnings, "Development decisions are agent guidance. Native permissions cannot enforce the meaning of every script or tool action.")
	plan.Native.RequiredActions = append(plan.Native.RequiredActions, "Start a new trusted native session after apply. Session and administrator restrictions can override this project configuration. No trust or credentials are granted.")
	plan.Warnings = append(plan.Warnings, "Existing native allow rules and approved escalations use separate authority; project settings do not revoke those grants.")
	security.Coverage = map[string]string{"operation-decisions": "agent-guidance", "host-authority": "unchanged", "credential-isolation": "not-provided"}
	if vendor == "copilot" {
		security.Coverage["filesystem-and-network"] = "existing-native-settings"
		plan.Warnings = append(plan.Warnings, "Copilot receives instructions only. Its existing tool approvals, filesystem, and network settings are unchanged; automatic local execution is not guaranteed.")
		return nil
	}
	path := filepath.Join(base, ".codex", "config.toml")
	if data, err := os.ReadFile(path); err == nil {
		values, err := parseNative(data, "toml")
		if err != nil {
			return err
		}
		if !migrateLegacy && (values["sandbox_mode"] != nil || values["sandbox_workspace_write"] != nil) {
			return fmt.Errorf("remove legacy sandbox_mode and sandbox_workspace_write from %s before practical development apply; keep a backup", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	workspaceAccess, gitAccess := "read", "read"
	if p.ProjectWork == "allow" {
		workspaceAccess = "write"
	}
	if p.LocalCommits == "allow" {
		gitAccess = "write"
	}
	paths := map[string]any{".": workspaceAccess, ".git": gitAccess}
	for _, protected := range p.ProtectedPaths {
		paths[protected] = "deny"
	}
	settings := map[string]any{
		"approval_policy": "on-request", "approvals_reviewer": "user",
		"default_permissions":    "agents-development",
		"developer_instructions": guidance,
		"permissions": map[string]any{"agents-development": map[string]any{
			"extends":    ":workspace",
			"filesystem": map[string]any{":workspace_roots": paths},
			"network":    map[string]any{"enabled": false},
		}},
	}
	for _, key := range nativeSortedKeys(settings) {
		if err := add(path, "toml", key, settings[key], filepath.Join(root, developmentPath), false); err != nil {
			return err
		}
	}
	security.ProjectedSettings = settings
	security.Coverage["filesystem-and-network"] = "native-project-configuration"
	security.AutomaticGrants = []string{"Codex sandboxed project writes and local .git writes when allow is selected", "native runtime paths and inherited readable host paths remain available"}
	plan.Warnings = append(plan.Warnings, "Codex command network access is disabled until native approval. Workspace write access includes deletion; .git write access includes history changes. Protected path behavior depends on native platform support. Other tools use separate controls.")
	return nil
}

func developmentLegacyKey(pointer string) bool {
	return pointer == "/sandbox_mode" || pointer == "/sandbox_workspace_write" || strings.HasPrefix(pointer, "/sandbox_workspace_write/")
}

func developmentNativeKey(base, path, pointer string) bool {
	if path != filepath.Join(base, ".codex", "config.toml") {
		return false
	}
	return developmentLegacyKey(pointer) || pointer == "/approval_policy" || pointer == "/approvals_reviewer" ||
		pointer == "/default_permissions" || pointer == "/developer_instructions" ||
		strings.HasPrefix(pointer, "/permissions/agents-development/")
}

func developmentRootReference(canonical, root []byte) bool {
	return strings.HasSuffix(string(canonical), developmentReference) && string(root) == strings.TrimSuffix(string(canonical), developmentReference) ||
		string(root) == "# Repository Agent Instructions\n\nRead and follow the canonical instructions in `.agents/AGENTS.md`.\n"
}
