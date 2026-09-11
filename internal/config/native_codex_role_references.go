package config

import (
	"path/filepath"
	"strings"
)

// A role reference is relative to its native config directory, not CODEX_HOME
// in all scopes and not the canonical artifact directory. Keep references to
// mapped flat agent assets portable. Other role files remain external; do not
// copy arbitrary trees or silently change the referenced file on relocation.
func nativeRebaseCodexRoleImport(values map[string]any, configDir, scope string) bool {
	agents, _ := values["agents"].(map[string]any)
	changed := false
	for _, raw := range agents {
		role, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reference, ok := role["config_file"].(string)
		if !ok || reference == "" || strings.ContainsRune(reference, '\x00') || strings.HasPrefix(reference, "~") {
			continue
		}
		resolved := reference
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(configDir, resolved)
		}
		rebased := resolved
		relative, err := filepath.Rel(filepath.Join(configDir, "agents"), resolved)
		if err == nil && filepath.Base(relative) == relative && safeNativeRelative(relative) && strings.HasSuffix(relative, ".toml") && nativeNoSymlinks(resolved) == nil {
			// Declared roles can omit standalone metadata. Such files are imported
			// for preservation but do not activate as standalone assets. Keep the
			// declaration linked to the original external file in that case.
			if data, err := nativeReadFile(resolved); err == nil {
				if _, err = nativeCodexAgent(data, scope); err == nil {
					rebased = filepath.ToSlash(filepath.Join("agents", relative))
				}
			}
		}
		if reference != rebased {
			role["config_file"], changed = rebased, true
		}
	}
	return changed
}

// Selectors inside an agent file are relative to that file. References to
// copied user skills or the project's canonical skills keep that relationship.
// External selectors retain the source path without copying external files.
func nativeRebaseCodexAgentSkillImport(values map[string]any, base, agentDir, scope string) bool {
	skills, _ := values["skills"].(map[string]any)
	entries, _ := skills["config"].([]any)
	skillRoot := filepath.Join(base, "skills")
	if scope == "project" {
		skillRoot = filepath.Join(base, ".agents", "skills")
	}
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reference, ok := entry["path"].(string)
		if !ok || nativeCodexSkillConstraint([]string{"skills", "config", "0", "path"}, reference) != "" || strings.HasPrefix(reference, "~") {
			continue
		}
		resolved := reference
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(agentDir, resolved)
		}
		rebased := resolved
		if relative, err := filepath.Rel(skillRoot, resolved); err == nil {
			parts := strings.Split(filepath.ToSlash(relative), "/")
			if len(parts) == 2 && isSafeSkillName(parts[0]) && parts[1] == "SKILL.md" && nativeNoSymlinks(resolved) == nil && requireRegularFile(resolved, "native skill definition") == nil {
				if local, err := filepath.Rel(agentDir, resolved); err == nil {
					rebased = filepath.ToSlash(local)
				}
			}
		}
		if reference != rebased {
			entry["path"], changed = rebased, true
		}
	}
	return changed
}

func nativeCodexRoleReferenceFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-reference-relocation"
	feature.Limitation = "Import resolves role config_file and agent-file skill selectors against their source config directory. References to mapped agent files and owned skills remain relative; other files remain external absolute references. HOME-relative references remain native-dependent. Apply does not copy external libraries, check their later availability, or grant runtime authority."
	feature.Evidence = nil
	for _, name := range []string{"external", "managed", "absolute", "declared-only", "managed-skill"} {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-role-reference-"+name+"-"+feature.Scope+"-verified.json")
	}
	return feature
}
