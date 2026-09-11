package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Standalone roles use bounded child overrides, not general config layers.
// Source: rust-v0.154.0 core/src/agent/role.rs and the pinned native role probe.
// Keep unknown or blocked content inactive as a whole standalone artifact.
func nativeCodexAgent(data []byte, scope string) (string, error) {
	values, err := parseNative(data, "toml")
	if err != nil {
		return "", fmt.Errorf("malformed native agent TOML")
	}
	for _, key := range []string{"name", "description", "developer_instructions"} {
		value, ok := values[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("native agent requires a non-empty %s string", key)
		}
	}
	name := strings.TrimSpace(values["name"].(string))
	delete(values, "name")
	delete(values, "description")
	if err = nativeCheckRuntimeAuthentication("codex", values); err != nil {
		return "", err
	}
	if err = nativeCodexAliases(values, false); err != nil {
		return "", err
	}
	if err = nativeCodexRoleOverrides(values); err != nil {
		return "", err
	}
	// Role files use the same bounded session overrides in both discovery scopes.
	// General project-config stripping does not apply to this native layer.
	selected, inactive := nativeSelectConfig("codex", "user", values)
	if len(inactive) > 0 {
		return "", fmt.Errorf("native agent field %s cannot activate: %s", inactive[0].Path, inactive[0].Reason)
	}
	for _, key := range nativeSortedKeys(selected) {
		disposition, reason := nativeSettingDisposition("codex", "user", key, selected[key])
		if disposition != "configuration" {
			return "", fmt.Errorf("native agent setting %s cannot activate: %s", key, reason)
		}
	}
	return name, nil
}

func nativeAgentNameConflict(vendor, path, name string, planned map[string]string, targets map[string]*nativeTarget, state nativeRegistry, root string) error {
	entries, err := os.ReadDir(filepath.Dir(path))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		other := filepath.Join(filepath.Dir(path), entry.Name())
		suffix := ".toml"
		if vendor == "copilot" {
			suffix = ".md"
		}
		if other == path || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		// Other selected files are checked against their desired identities.
		// An unchanged file owned by this repository can be removed by this
		// same transaction when an agent moves to a new registered file name.
		if plannedName, exists := planned[other]; exists {
			if plannedName == name || vendor == "copilot" && nativeCopilotAgentStem(other) == nativeCopilotAgentStem(path) {
				return fmt.Errorf("duplicate native agent identity")
			}
			continue
		}
		if err = nativeNoSymlinks(other); err != nil {
			return err
		}
		data, err := nativeReadFile(other)
		if err != nil {
			return err
		}
		otherTarget := targets[other]
		stale := otherTarget == nil || len(otherTarget.sources) == 0
		if owner, exists := state.Settings[nativeKey(other, "")]; exists && owner.Source == root && stale && nativeHashMatches(data, owner.Hash) {
			continue
		}
		if vendor == "copilot" {
			otherName, err := nativeParseCopilotAgent(data, other, false)
			if err != nil {
				return fmt.Errorf("existing native agent is malformed: %s", entry.Name())
			}
			if otherName == name || nativeCopilotAgentStem(other) == nativeCopilotAgentStem(path) {
				return fmt.Errorf("duplicate native Copilot agent identity")
			}
			continue
		}
		values, err := parseNative(data, "toml")
		if err != nil {
			return fmt.Errorf("existing native agent is malformed: %s", entry.Name())
		}
		otherName, _ := values["name"].(string)
		if strings.TrimSpace(otherName) == name {
			return fmt.Errorf("duplicate native agent identity in %s and %s", filepath.Base(path), entry.Name())
		}
	}
	return nil
}
