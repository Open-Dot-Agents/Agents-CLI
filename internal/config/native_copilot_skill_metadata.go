package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// This is a native projection check, not canonical Markdown validation. Never
// rewrite the skill to make a native parser accept it. Unknown annotations stay
// byte-preserved and are reported without a native behavior claim.
func nativeCopilotSkillFields(data []byte) (map[string]any, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("Copilot skill discovery requires YAML frontmatter")
	}
	lines := strings.Split(text[4:], "\n")
	end := -1
	for i, line := range lines {
		if line == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("Copilot skill frontmatter has no closing delimiter")
	}
	decoder := yaml.NewDecoder(strings.NewReader(strings.Join(lines[:end], "\n")))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("Copilot skill has malformed or empty YAML frontmatter")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("Copilot skill frontmatter must be an object")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("Copilot skill requires one frontmatter document")
	}
	fields := map[string]any{}
	if err := document.Content[0].Decode(&fields); err != nil {
		return nil, fmt.Errorf("Copilot skill has invalid YAML values or duplicate fields")
	}
	children := document.Content[0].Content
	seen := map[string]bool{}
	for i := 0; i < len(children); i += 2 {
		key := children[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("Copilot skill frontmatter requires string keys; YAML merge keys have no verified mapping")
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("Copilot skill has duplicate frontmatter fields")
		}
		seen[key.Value] = true
	}
	return fields, nil
}

func nativeCopilotSkillFieldValid(key string, value any) bool {
	switch key {
	case "name", "description", "argument-hint":
		_, ok := value.(string)
		return ok
	case "user-invocable", "disable-model-invocation":
		_, ok := value.(bool)
		return ok
	case "allowed-tools":
		if _, ok := value.(string); ok {
			return true
		}
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, child := range items {
			if _, ok := child.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

func nativeCopilotSkillFieldFeature(feature NativeFeature, key string) NativeFeature {
	feature.Feature = "artifact:skill:/" + key
	feature.Disposition = "artifact-field-mapping"
	feature.NativeStatus = "bounded-skill-metadata"
	feature.Evidence = []string{"docs/COPILOT_SKILL_METADATA.md", "WORKBENCH/conformance/verify_copilot_skill_metadata.py"}
	feature.Activation = "pending native discovery"
	switch key {
	case "name", "description":
		feature.Limitation = "Copilot 1.0.83 uses native fallback values when the field is absent. No shared name identity or description fallback is imposed."
	case "user-invocable":
		feature.Limitation = "False hides the skill from ACP slash commands and prevents tested explicit slash expansion; it does not disable model invocation."
	case "disable-model-invocation":
		feature.Limitation = "True removes the skill from the tested model catalog and skill tool; it does not disable explicit user invocation."
	case "argument-hint":
		feature.Activation = "native limitation"
		feature.Limitation = "The tested custom hint is absent from Copilot 1.0.83 ACP and terminal completion. Bytes are preserved; display behavior is not guaranteed."
	case "allowed-tools":
		feature.Activation = "native permission behavior not guaranteed"
		feature.Limitation = "Copilot 1.0.83 records the wildcard in skill invocation events but still requests approval for the tested shell built-in. Other patterns and tools are unverified. This is not a portable permission mapping."
	}
	return feature
}

func nativeCopilotSkillCapabilities(scope, destination string) []NativeFeature {
	var features []NativeFeature
	for _, key := range []string{"name", "description", "argument-hint", "allowed-tools", "user-invocable", "disable-model-invocation"} {
		features = append(features, nativeCopilotSkillFieldFeature(NativeFeature{
			Source: "native_copilot_skill_metadata.go", Destination: destination, Scope: scope,
			Ownership: "project canonical file or user-owned skill file", Authority: "native discovery; no trust or permission grant",
		}, key))
	}
	return features
}

func nativePlanCopilotSkills(plan *PlanResult, root, base, scope string) error {
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		return err
	}
	known := map[string]bool{"name": true, "description": true, "argument-hint": true, "allowed-tools": true, "user-invocable": true, "disable-model-invocation": true}
	for _, entry := range entries {
		source := filepath.Join(root, "skills", entry.Name(), "SKILL.md")
		destination, ownership := source, "canonical file; not owned by native projection"
		if scope == "user" {
			destination, ownership = filepath.Join(base, "skills", entry.Name(), "SKILL.md"), "file owned by source repository"
		}
		data, err := nativeReadFile(source)
		if err != nil {
			return err
		}
		feature := NativeFeature{Feature: "skill", Source: source, Destination: destination, Scope: scope,
			Ownership: ownership, Authority: "native discovery; no trust or permission grant",
			Disposition: "portable-mapping", Activation: "pending native discovery", NativeStatus: "bounded-skill-metadata"}
		fields, err := nativeCopilotSkillFields(data)
		if err != nil {
			feature.Activation, feature.Disposition, feature.NativeStatus = "projection refused", "unmapped", "unverified"
			feature.Limitation = err.Error()
			plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("skill %q: %s; source stays unchanged; existing project discovery is outside apply", source, err))
			plan.Native.Features = append(plan.Native.Features, feature)
			continue
		}
		keys := make(map[string]any, len(fields))
		for key := range fields {
			keys[key] = true
		}
		model, user := "enabled", "enabled"
		if disabled, _ := fields["disable-model-invocation"].(bool); disabled {
			model = "disabled"
		}
		if enabled, ok := fields["user-invocable"].(bool); ok && !enabled {
			user = "disabled"
		}
		feature.Activation = "pending native discovery; model invocation " + model + "; user invocation " + user
		for _, key := range nativeSortedKeys(keys) {
			field := feature
			if !known[key] {
				field.Feature, field.Disposition, field.Activation, field.NativeStatus = "artifact:skill:/"+nativePointer(key), "preserved-annotation", "not mapped by adapter", "unverified"
				field.Limitation = "Unknown Markdown annotation is preserved without a native behavior claim. It is not an activated native-profile setting."
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("skill %q field %q: %s", source, key, field.Limitation))
			} else if !nativeCopilotSkillFieldValid(key, fields[key]) {
				field.Feature, field.Disposition, field.Activation = "artifact:skill:/"+key, "unmapped", "projection refused"
				field.NativeStatus, field.Limitation = "unverified", "Skill field type has no verified Copilot mapping; do not coerce it into a native control."
				feature.Activation, feature.Disposition = "projection refused", "unmapped"
				plan.Diagnostics = append(plan.Diagnostics, fmt.Sprintf("skill %q field %q: %s", source, key, field.Limitation))
			} else {
				field = nativeCopilotSkillFieldFeature(field, key)
				if key == "argument-hint" || key == "allowed-tools" {
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("skill %q field %q: %s", source, key, field.Limitation))
				}
			}
			plan.Native.Features = append(plan.Native.Features, field)
		}
		plan.Native.Features = append(plan.Native.Features, feature)
	}
	if scope == "project" {
		if _, _, err := nativeImportCopilotProjectSkills(base, root); err != nil {
			plan.Diagnostics = append(plan.Diagnostics, "project skill discovery conflict: "+err.Error()+"; resolve the unowned native source before apply")
		}
	}
	return nil
}
