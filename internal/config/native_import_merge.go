package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func nativeImportExisting(root string, sharedProjectSkills bool) (bool, error) {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var manifest manifestDocument
	if err := nativeDecodePolicy(filepath.Join(root, "manifest.json"), &manifest); err != nil {
		if sharedProjectSkills && errors.Is(err, os.ErrNotExist) {
			return nativeImportSharedSkillTree(root)
		}
		return false, err
	}
	if manifest.Version != NativeVersion {
		return false, fmt.Errorf("native import cannot migrate an existing stable or draft.1 canonical tree")
	}
	if err := ValidateRepositoryWithOptions(root, true); err != nil {
		return false, err
	}
	return true, nil
}

// Import can add disjoint configuration to an existing canonical tree. It has
// no authority to replace conflicting values, even with --force. Keep existing
// portable policy files and manifest fields without re-encoding those files.
func nativeMergeImport(root string, changes []nativeChange, instructionsProvided bool) ([]nativeChange, error) {
	var merged []nativeChange
	for _, change := range changes {
		if err := nativeNoSymlinks(change.path); err != nil {
			return nil, err
		}
		snapshot, err := nativeReadSnapshot(change.path)
		if err != nil {
			return nil, err
		}
		if change.remove {
			if change.path != filepath.Join(root, "skills/.gitkeep") || change.before == nil || !change.before.equal(snapshot) || len(snapshot.data) != 0 {
				return nil, fmt.Errorf("native import refuses removal of changed or non-placeholder content")
			}
			if snapshot.exists {
				merged = append(merged, change)
			}
			continue
		}
		change.before = &snapshot
		if !snapshot.exists {
			merged = append(merged, change)
			continue
		}
		old := snapshot.data
		if change.path == filepath.Join(root, "AGENTS.md") && !instructionsProvided || bytes.Equal(old, change.data) {
			continue
		}
		relative, err := filepath.Rel(root, change.path)
		if err != nil {
			return nil, err
		}
		format := ""
		if relative == "manifest.json" || relative == "tools/mcp.json" || (strings.HasPrefix(relative, "native/") || strings.HasPrefix(relative, "plugins/")) && strings.HasSuffix(relative, ".json") {
			format = "json"
		} else if (strings.HasPrefix(relative, "native/") || strings.HasPrefix(relative, "plugins/")) && strings.HasSuffix(relative, ".toml") {
			format = "toml"
		}
		// The adapter defines a config artifact's format, not its source suffix.
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) >= 3 && (parts[0] == "native" || parts[0] == "plugins") && parts[2] != "profile.json" {
			var profile nativeProfile
			if err := nativeDecodePolicy(filepath.Join(root, parts[0], parts[1], "profile.json"), &profile); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			for _, artifact := range profile.Artifacts {
				if artifact.Kind == "config" && artifact.Source == strings.Join(parts[2:], "/") {
					switch profile.Namespace {
					case "com.openai.codex":
						format = "toml"
					case "com.github.copilot":
						format = "json"
					}
				}
			}
		}
		if format == "" {
			return nil, fmt.Errorf("native import conflicts with existing canonical file: %s", relative)
		}
		before, err := parseNative(old, format)
		if err != nil {
			return nil, fmt.Errorf("cannot parse existing canonical file: %s", relative)
		}
		after, err := parseNative(change.data, format)
		if err != nil {
			return nil, fmt.Errorf("cannot parse imported canonical file: %s", relative)
		}
		if relative == "manifest.json" {
			before["version"] = NativeVersion
			profiles, _ := before["profiles"].([]any)
			incoming, _ := after["profiles"].([]any)
			before["profiles"] = nativeUnionValues(profiles, incoming)
			delete(after, "version")
			delete(after, "profiles")
		} else if (strings.HasPrefix(relative, "native/") || strings.HasPrefix(relative, "plugins/")) && filepath.Base(relative) == "profile.json" {
			// Existing required status is an existing requirement, not input for
			// this importer to relax. Namespace, version, and scope must agree.
			delete(after, "required")
			prior, _ := before["artifacts"].([]any)
			incoming, _ := after["artifacts"].([]any)
			for _, artifact := range incoming {
				fields, _ := artifact.(map[string]any)
				for _, previous := range prior {
					previousFields, _ := previous.(map[string]any)
					sameSource := fields["source"] == previousFields["source"]
					if relative == "native/com.github.copilot/profile.json" && (fields["kind"] == "canonical-instructions") != (previousFields["kind"] == "canonical-instructions") {
						sameSource = false // Fixed core source and namespace file are different sources.
					}
					if sameSource && nativeHash(artifact) != nativeHash(previous) {
						return nil, fmt.Errorf("native import conflicts with an existing artifact declaration")
					}
				}
			}
			before["artifacts"] = nativeUnionValues(prior, incoming)
			delete(after, "artifacts")
		}
		if err = nativeMergeImportValues(before, after); err != nil {
			return nil, fmt.Errorf("native import conflicts with existing canonical file %s: %w", relative, err)
		}
		if strings.HasPrefix(relative, "native/com.openai.codex/") && format == "toml" {
			if err = nativeCodexAliases(before, false); err != nil {
				return nil, err
			}
		}
		original, _ := parseNative(old, format)
		if nativeHash(original) == nativeHash(before) {
			continue
		}
		change.data, err = nativeEncode(before, format)
		if err != nil {
			return nil, err
		}
		change.mode = snapshot.mode
		merged = append(merged, change)
	}
	return merged, nil
}

func nativeMergeImportValues(existing, incoming map[string]any) error {
	for key, value := range incoming {
		previous, found := existing[key]
		if !found {
			existing[key] = value
			continue
		}
		if nativeHash(previous) == nativeHash(value) {
			continue
		}
		oldMap, oldOK := previous.(map[string]any)
		newMap, newOK := value.(map[string]any)
		if !oldOK || !newOK {
			// Do not include values in a diagnostic.
			return fmt.Errorf("different assignments for field %s", key)
		}
		if err := nativeMergeImportValues(oldMap, newMap); err != nil {
			return err
		}
	}
	return nil
}

func nativeUnionValues(existing, incoming []any) []any {
	for _, item := range incoming {
		found := false
		for _, previous := range existing {
			if nativeHash(previous) == nativeHash(item) {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, item)
		}
	}
	return existing
}
