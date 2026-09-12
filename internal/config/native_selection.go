package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type nativeInactiveField struct {
	Path, Disposition, Reason string
}

// Select known object fields before value validation. Never modify the source.
// Arrays remain atomic: dropping a member can change order or selector meaning.
func nativeSelectConfig(vendor, scope string, values map[string]any) (map[string]any, []nativeInactiveField) {
	selected := map[string]any{}
	var inactive []nativeInactiveField
	for _, key := range nativeSortedKeys(values) {
		if nativePluginRoot(vendor, key) {
			fields, rejected := nativeSelectPlugins(vendor, scope, map[string]any{key: values[key]})
			inactive = append(inactive, rejected...)
			if value, exists := fields[key]; exists {
				selected[key] = value
			}
			continue
		}
		if vendor == "codex" && key == "hooks" {
			fields, rejected, err := nativeSelectCodexHooks(map[string]any{"hooks": values[key]}, true)
			if err != nil {
				rejected = append(rejected, nativeInactiveField{"/hooks", "inactive", err.Error()})
			}
			inactive = append(inactive, rejected...)
			if err == nil {
				if hooks, ok := fields["hooks"]; ok {
					selected[key] = hooks
				}
			}
			continue
		}
		if vendor == "copilot" && key == "hooks" {
			fields, rejected, err := nativeSelectCopilotHooks(map[string]any{"hooks": values[key]}, true)
			if err != nil {
				rejected = append(rejected, nativeInactiveField{"/hooks", "inactive", err.Error()})
			}
			inactive = append(inactive, rejected...)
			if err == nil {
				if hooks, ok := fields["hooks"]; ok {
					selected[key] = hooks
				}
			}
			continue
		}
		value, keep, rejected := nativeSelectField(vendor, scope, []string{key}, values[key])
		inactive = append(inactive, rejected...)
		if keep {
			selected[key] = value
		}
	}
	return selected, inactive
}

func nativeSortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeSelectField(vendor, scope string, path []string, value any) (any, bool, []nativeInactiveField) {
	reject := func(disposition, reason string) (any, bool, []nativeInactiveField) {
		pointer := ""
		for _, part := range path {
			pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
		}
		return nil, false, []nativeInactiveField{{pointer, disposition, reason}}
	}
	if vendor == "copilot" {
		if reason := nativeCopilotConfigConstraint(path, value); reason != "" {
			return reject("inactive", reason)
		}
	}
	if vendor == "codex" {
		if reason := nativeCodexKeymapConstraint(path, value); reason != "" {
			return reject("inactive", reason)
		}
		if reason := nativeCodexOtelConstraint(path, value); reason != "" {
			return reject("inactive", reason)
		}
		if scope == "project" {
			if reason := nativeCodexProjectRestriction(path); reason != "" {
				return reject("native-ignored", reason)
			}
		}
		if disposition, reason := nativeCodexOtelExporterProblem(path, value); reason != "" {
			_, _, inactive := reject(disposition, reason)
			return "none", true, inactive
		}
		if nativeCodexOtelExcluded(path, value) {
			return reject("external", "telemetry credential value or invalid collector URL is excluded")
		}
		if scope == "project" && len(path) == 2 && path[0] == "skills" && path[1] == "config" {
			return reject("inactive", "Codex 0.154.0 ignores project skill selectors in discovery and model context; use native user scope")
		}
		if reason := nativeCodexSkillConstraint(path, value); reason != "" {
			return reject("inactive", reason)
		}
	}
	key := path[len(path)-1]
	if nativePolicyField(vendor, path) {
		if nativeForbidden(key) {
			return reject("external", "authority or credential field is excluded")
		}
		if nativeSecurityKey(key) {
			return reject("blocked", "native security mapping needs combined native behavior evidence")
		}
	}
	if len(path) == 1 && !nativeSettingRegistry[vendor][scope][key] || !nativeKnownConfigPath(vendor, path) {
		return reject("inactive", "no field mapping for the pinned native version")
	}
	switch typed := value.(type) {
	case map[string]any:
		selected := map[string]any{}
		var inactive []nativeInactiveField
		for _, childKey := range nativeSortedKeys(typed) {
			childPath := append(append([]string(nil), path...), childKey)
			child, keep, rejected := nativeSelectField(vendor, scope, childPath, typed[childKey])
			inactive = append(inactive, rejected...)
			if keep {
				selected[childKey] = child
			}
		}
		return selected, len(typed) == 0 || len(selected) > 0, inactive
	case []any:
		var inactive []nativeInactiveField
		for i, child := range typed {
			childPath := append(append([]string(nil), path...), strconv.Itoa(i))
			_, _, rejected := nativeSelectField(vendor, scope, childPath, child)
			inactive = append(inactive, rejected...)
		}
		if len(inactive) > 0 {
			_, _, atomic := reject("inactive", "array remains inactive because a member is unmapped; array order and selectors are preserved")
			return nil, false, append(inactive, atomic...)
		}
	}
	return value, true, nil
}

func nativeKnownConfigPath(vendor string, path []string) bool {
	if vendor != "codex" {
		if nativeTypeAt(vendor, path) != "" {
			return true
		}
		if len(path) > 1 {
			parent := nativeTypeAt(vendor, path[:len(path)-1])
			if strings.HasPrefix(parent, "map<") {
				return true
			}
			if strings.HasPrefix(parent, "array<") || strings.HasSuffix(parent, "[]") {
				index, err := strconv.Atoi(path[len(path)-1])
				return err == nil && index >= 0
			}
		}
		return false
	}
	schema, err := loadNativeCodexSchema()
	if err != nil {
		return false
	}
	parts := append([]string(nil), path...)
	if len(parts) >= 2 {
		if canonical := nativeCodexAliasFields[parts[0]][parts[1]]; canonical != "" {
			parts[1] = canonical
		}
	}
	candidates := []*jsonschema.Schema{schema}
	for _, part := range parts {
		var next []*jsonschema.Schema
		for _, candidate := range candidates {
			next = append(next, nativeSchemaChildren(candidate, part, map[*jsonschema.Schema]bool{})...)
		}
		if len(next) == 0 {
			return false
		}
		candidates = next
	}
	return len(candidates) != 0
}

func nativeSchemaChildren(schema *jsonschema.Schema, name string, visited map[*jsonschema.Schema]bool) []*jsonschema.Schema {
	if schema == nil || visited[schema] {
		return nil
	}
	visited[schema] = true
	var children []*jsonschema.Schema
	if child := schema.Properties[name]; child != nil {
		children = append(children, child)
	} else if child, ok := schema.AdditionalProperties.(*jsonschema.Schema); ok {
		children = append(children, child)
	}
	if _, err := strconv.Atoi(name); err == nil {
		if item, ok := schema.Items.(*jsonschema.Schema); ok {
			children = append(children, item)
		}
	}
	for _, child := range append(append(append([]*jsonschema.Schema{schema.Ref}, schema.AllOf...), schema.AnyOf...), schema.OneOf...) {
		children = append(children, nativeSchemaChildren(child, name, visited)...)
	}
	return children
}

func nativeInactiveFeatures(base NativeFeature, kind string, inactive []nativeInactiveField) []NativeFeature {
	var features []NativeFeature
	for _, field := range inactive {
		feature := base
		feature.Feature = fmt.Sprintf("%s:%s", kind, field.Path)
		feature.Disposition, feature.Limitation = field.Disposition, field.Reason
		feature.Activation, feature.NativeStatus, feature.Evidence = "inactive", "unverified", nil
		if field.Disposition == "native-ignored" && base.Scope == "project" && kind == "config" {
			feature.NativeStatus, feature.Evidence = "native-effective-configuration", nativeCodexProjectScopeEvidence()
		}
		features = append(features, feature)
	}
	return features
}
