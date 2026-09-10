package config

import (
	"path/filepath"
	"strings"
)

func nativeCodexSkillConstraint(path []string, value any) string {
	if len(path) < 2 || path[0] != "skills" {
		return ""
	}
	if len(path) == 4 && path[1] == "config" && path[3] == "path" {
		text, ok := value.(string)
		if !ok || strings.ContainsRune(text, '\x00') || filepath.Base(text) != "SKILL.md" || strings.HasSuffix(text, "/") {
			return "Codex skill path selectors must name SKILL.md; native ignores folder selectors"
		}
	}
	return ""
}

// Resolve imported references against their native config origin. A reference
// to a copied skill stays relative to the new native home. External references
// keep their original absolute target; no external assets are copied or owned.
func nativeRebaseCodexSkillImport(values map[string]any, home string) bool {
	skills, _ := values["skills"].(map[string]any)
	entries, _ := skills["config"].([]any)
	changed := false
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path, ok := entry["path"].(string)
		if !ok || nativeCodexSkillConstraint([]string{"skills", "config", "0", "path"}, path) != "" {
			continue
		}
		if strings.HasPrefix(path, "~") {
			// Codex expands home-relative selectors at runtime. The source
			// CODEX_HOME does not establish the native process's HOME value.
			continue
		}
		resolved := path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(home, resolved)
		}
		rebased := filepath.Clean(resolved)
		if relative, err := filepath.Rel(home, resolved); err == nil {
			parts := strings.Split(filepath.ToSlash(relative), "/")
			if len(parts) == 3 && parts[0] == "skills" && isSafeSkillName(parts[1]) && parts[2] == "SKILL.md" {
				// Asset import will validate this package before the transaction.
				// Missing and system packages are not imported; keep their targets.
				if requireRegularFile(resolved, "native skill definition") == nil && nativeNoSymlinks(resolved) == nil {
					rebased = filepath.ToSlash(relative)
				}
			}
		}
		if path != rebased {
			entry["path"] = rebased
			changed = true
		}
	}
	return changed
}

func nativeCodexSkillFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-fixture-context"
	feature.Limitation = "Codex 0.154.0 user selectors control discovery and the initial model catalog after restart. Paths must name SKILL.md; folder selectors are ignored. Project selectors appear in config/read but do not control discovery or model context and remain inactive. Import makes references to copied user skill packages relative to the target native home; external and HOME-dependent references remain external. Skill execution, token budgets, and other skill controls need separate evidence."
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-skills-verified-user.json", "WORKBENCH/evidence/native-draft2-debug/codex-skills-verified-project.json"}
	return feature
}

func nativeCodexSkillCapabilities(scope, destination string) []NativeFeature {
	if scope != "user" {
		return nil
	}
	var fields []NativeFeature
	for _, path := range []string{"skills.config", "skills.config.<name>.path", "skills.config.<name>.enabled"} {
		fields = append(fields, nativeCodexSkillFeature(NativeFeature{
			Feature: "artifact:skill-controls:/" + strings.ReplaceAll(path, ".", "/"),
			Source:  "native_codex_skills.go", Destination: destination, Scope: scope,
			Disposition: "artifact-field-mapping", Activation: "requires value validation and native reload",
			Ownership: "setting", Authority: "native user skill preference; no account or trust write",
		}))
	}
	return fields
}
