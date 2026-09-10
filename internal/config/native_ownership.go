package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var nativeHashSyntax = regexp.MustCompile(`^(?:(?:json|toml)-v2:)?[0-9a-f]{64}$`)

func nativeTargetForPath(vendor, scope, base, path string) (*nativeTarget, bool) {
	// Retain the old draft.2 destination only to remove owned settings safely.
	if vendor == "copilot" && scope == "project" && path == filepath.Join(base, ".github/mcp.json") {
		return &nativeTarget{path: path, format: "json", settings: map[string]any{}, sources: map[string]string{}}, true
	}

	var artifacts []nativeArtifact
	for _, kind := range []string{"config", "instructions", "mcp", "lsp"} {
		artifacts = append(artifacts, nativeArtifact{Kind: kind})
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || !safeNativeRelative(rel) {
		return nil, false
	}
	for _, kind := range []string{"skill", "scoped-instructions", "hooks", "agent"} {
		parts := strings.Split(rel, string(filepath.Separator))
		for i := range parts {
			artifacts = append(artifacts, nativeArtifact{Kind: kind, Name: filepath.Join(parts[i:]...)})
		}
	}
	for _, artifact := range artifacts {
		target, format, err := nativeTargetPath(vendor, scope, base, artifact)
		if err == nil && target == path {
			return &nativeTarget{path: path, format: format, isAsset: format == "", settings: map[string]any{}, sources: map[string]string{}}, true
		}
	}
	return nil, false
}
func nativePointerParts(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("native ownership field must be a JSON pointer")
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		decoded := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if nativePointer(decoded) != part || decoded == "" {
			return nil, fmt.Errorf("invalid native ownership pointer")
		}
		parts[i] = decoded
	}
	return parts, nil
}
func nativeValidateRegistry(vendor, scope, base string, state nativeRegistry) error {
	byPath := map[string][]string{}
	for encoded, owner := range state.Settings {
		var fields []string
		if json.Unmarshal([]byte(encoded), &fields) != nil || len(fields) != 2 || encoded != nativeKey(fields[0], fields[1]) {
			return fmt.Errorf("invalid or noncanonical native ownership key")
		}
		path, pointer := fields[0], fields[1]
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("native ownership target must be a canonical absolute path")
		}
		target, ok := nativeTargetForPath(vendor, scope, base, path)
		if !ok {
			return fmt.Errorf("native ownership target is outside adapter registry")
		}
		if target.isAsset != (pointer == "") {
			return fmt.Errorf("native ownership kind does not match the adapter target")
		}
		if pointer != "" {
			parts, err := nativePointerParts(pointer)
			if err != nil {
				return err
			}
			if filepath.Base(path) == "config.toml" || filepath.Base(path) == "settings.json" {
				for i, part := range parts {
					if nativePolicyField(vendor, parts[:i+1]) && nativeForbidden(part) {
						return fmt.Errorf("native ownership cannot claim external authority or credential fields")
					}
				}
			}
		}
		if !filepath.IsAbs(owner.Source) || filepath.Clean(owner.Source) != owner.Source || strings.ContainsRune(owner.Source, 0) {
			return fmt.Errorf("native ownership source must be a canonical absolute path")
		}
		if !nativeHashSyntax.MatchString(owner.Hash) {
			return fmt.Errorf("invalid native ownership hash")
		}
		byPath[path] = append(byPath[path], pointer)
	}
	for _, pointers := range byPath {
		sort.Strings(pointers)
		for i, pointer := range pointers {
			for _, prior := range pointers[:i] {
				if pointer == prior || strings.HasPrefix(pointer, prior+"/") {
					return fmt.Errorf("native ownership has overlapping parent and child fields")
				}
			}
		}
	}
	return nil
}
