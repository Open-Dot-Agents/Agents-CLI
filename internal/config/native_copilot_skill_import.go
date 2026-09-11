package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A native shared skill tree need not have an Open-Dot-Agents manifest. Only
// these recognized sources can establish a new draft.2 tree. Other partial
// configuration has no version contract and must not be activated by import.
func nativeImportSharedSkillTree(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "skills":
		case "AGENTS.md":
			if err := requireRegularFile(filepath.Join(root, entry.Name()), "canonical instructions"); err != nil {
				return false, err
			}
		default:
			return false, fmt.Errorf("native shared skill import requires a manifest for other canonical content: %s", entry.Name())
		}
	}
	assets, excluded, err := nativeImportSkills(filepath.Join(root, "skills"), filepath.Join(root, "skills"))
	if err != nil {
		return false, err
	}
	if len(assets) == 0 || len(excluded) != 0 {
		return false, fmt.Errorf("native shared skill import requires recognized project skill packages")
	}
	return true, nil
}

// Only these supplied-root locations are source configuration. Parent skills,
// user homes, plugins, and added-directory trust remain outside this import.
func nativeImportCopilotProjectSkills(base, root string) ([]nativeChange, []string, error) {
	destination := filepath.Join(root, "skills")
	packages := map[string][]nativeChange{}
	identities := map[string]string{}
	var result []nativeChange
	var sources []string
	for _, origin := range []string{".agents/skills", ".github/skills", ".claude/skills"} {
		assets, excluded, err := nativeImportSkills(filepath.Join(base, filepath.FromSlash(origin)), destination)
		if err != nil {
			return nil, nil, err
		}
		if len(excluded) > 0 {
			return nil, nil, fmt.Errorf("project skill location %q contains a nonportable system package", origin)
		}
		grouped := map[string][]nativeChange{}
		for _, asset := range assets {
			relative, err := filepath.Rel(destination, asset.path)
			if err != nil {
				return nil, nil, err
			}
			name := strings.Split(filepath.ToSlash(relative), "/")[0]
			grouped[name] = append(grouped[name], asset)
		}
		keys := map[string]any{}
		for name := range grouped {
			keys[name] = true
		}
		for _, name := range nativeSortedKeys(keys) {
			incoming := grouped[name]
			sources = append(sources, origin+"/"+name)
			if previous, exists := packages[name]; exists {
				if !nativeSameSkillPackage(previous, incoming) {
					return nil, nil, fmt.Errorf("conflicting complete skill package %q; import cannot combine or replace its assets", name)
				}
				continue
			}
			for _, asset := range incoming {
				if asset.path != filepath.Join(destination, name, "SKILL.md") {
					continue
				}
				// Malformed metadata remains importable as source. The separate
				// projection check refuses it before native writes.
				fields, err := nativeCopilotSkillFields(asset.data)
				if err != nil {
					continue
				}
				identity, ok := fields["name"].(string)
				if !ok || identity == "" {
					identity = name
				}
				if previous, exists := identities[identity]; exists && previous != name {
					return nil, nil, fmt.Errorf("duplicate native skill identity in packages %q and %q", previous, name)
				}
				identities[identity] = name
			}
			packages[name] = incoming
			if origin != ".agents/skills" {
				result = append(result, incoming...)
			}
		}
	}
	if len(sources) > 0 {
		marker := filepath.Join(destination, ".gitkeep")
		snapshot, err := nativeReadSnapshot(marker)
		if err != nil {
			return nil, nil, err
		}
		if snapshot.exists && len(snapshot.data) == 0 {
			result = append(result, nativeChange{path: marker, remove: true, before: &snapshot})
		}
	}
	return result, sources, nil
}

func nativeSameSkillPackage(a, b []nativeChange) bool {
	if len(a) != len(b) {
		return false
	}
	byPath := map[string]nativeChange{}
	for _, file := range a {
		byPath[file.path] = file
	}
	for _, file := range b {
		previous, exists := byPath[file.path]
		if !exists || !bytes.Equal(previous.data, file.data) || previous.mode&0111 != file.mode&0111 {
			return false
		}
	}
	return true
}

func nativeCopilotProjectSkillImportCapabilities() []NativeFeature {
	var features []NativeFeature
	for _, origin := range []string{".github/skills/", ".agents/skills/", ".claude/skills/"} {
		features = append(features, NativeFeature{
			Feature: "artifact:skill-discovery:/" + origin, Source: origin, Destination: ".agents/skills/<name>/", Scope: "project",
			Disposition: "artifact-mapping", Activation: "import then project through the portable skills profile",
			Ownership: "canonical package; source native package remains unchanged", Authority: "supplied project root only; no added-directory or parent trust grant",
			NativeStatus: "bounded-relocated-skill-execution",
			Evidence:     []string{"docs/COPILOT_SKILL_IMPORT.md", "WORKBENCH/conformance/verify_copilot_skill_import.py"},
			Limitation:   "Imports complete packages from the supplied root. Existing .agents/skills packages are selected in place; a bare shared skill tree can establish draft.2 without replacing source files. Other unversioned canonical content refuses. Conflicting packages or duplicate native names refuse before writes, including --force. Identical packages are deduplicated. Parent, user, plugin, and added-directory sources are not imported. Stable import behavior is unchanged.",
		})
	}
	return features
}
