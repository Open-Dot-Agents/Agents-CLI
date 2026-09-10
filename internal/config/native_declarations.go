package config

import (
	"encoding/json"
	"sort"
	"strings"
)

// Declarations describe available value validators. They are not behavioral
// evidence, and do not establish that any concrete configuration can activate.
type NativeSettingDeclaration struct {
	Path         string   `json:"path"`
	Scopes       []string `json:"scopes"`
	Disposition  string   `json:"disposition"`
	Validation   string   `json:"validation_source"`
	NativeStatus string   `json:"native_status"`
}

func nativeSettingDeclarations(vendor string) []NativeSettingDeclaration {
	paths := map[string]bool{}
	validation := "native_types.go"
	if vendor == "codex" {
		validation = "native_schemas/codex-0.154.0.json"
		var document map[string]any
		if json.Unmarshal(nativeCodexSchema, &document) == nil {
			var visit func(any, string, map[string]bool)
			visit = func(raw any, path string, refs map[string]bool) {
				node, ok := raw.(map[string]any)
				if !ok {
					return
				}
				if path != "" {
					paths[path] = true
				}
				if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/definitions/") && !refs[ref] {
					next := map[string]bool{}
					for k, v := range refs {
						next[k] = v
					}
					next[ref] = true
					definitions, _ := document["definitions"].(map[string]any)
					visit(definitions[strings.TrimPrefix(ref, "#/definitions/")], path, next)
				}
				join := func(key string) string {
					if path == "" {
						return key
					}
					return path + "." + key
				}
				if properties, ok := node["properties"].(map[string]any); ok {
					for name, child := range properties {
						visit(child, join(name), refs)
					}
				}
				visit(node["additionalProperties"], join("<name>"), refs)
				visit(node["items"], path+"[]", refs)
				for _, kind := range []string{"allOf", "anyOf", "oneOf"} {
					if children, ok := node[kind].([]any); ok {
						for _, child := range children {
							visit(child, path, refs)
						}
					}
				}
			}
			visit(document, "", map[string]bool{})
		}
		for section, aliases := range nativeCodexAliasFields {
			for alias := range aliases {
				paths[section+"."+alias] = true
			}
		}
	} else {
		for path := range nativeSettingTypes[vendor] {
			paths[path] = true
			parts := nativePatternParts(path)
			for i := 1; i < len(parts); i++ {
				if nativeTypeAt(vendor, parts[:i]) != "" {
					paths[strings.Join(parts[:i], ".")] = true
				}
			}
		}
	}
	var result []NativeSettingDeclaration
	for path := range paths {
		parts := nativePatternParts(path)
		if len(parts) == 0 {
			continue
		}
		scopes := []string{}
		for _, scope := range []string{"project", "user"} {
			if vendor == "codex" && scope == "project" && len(parts) >= 2 && parts[0] == "skills" && parts[1] == "config" {
				continue
			}
			if nativeSettingRegistry[vendor][scope][parts[0]] {
				scopes = append(scopes, scope)
			}
		}
		disposition := "validator-declared"
		if len(scopes) == 0 {
			disposition = "mapping-pending"
		}
		for i, part := range parts {
			if !nativePolicyField(vendor, parts[:i+1]) {
				continue
			}
			if nativeForbidden(part) {
				disposition = "external"
				break
			}
			if nativeSecurityKey(part) {
				disposition = "security-evidence-required"
			}
		}
		fieldValidation := validation
		if vendor == "codex" && parts[0] == "skills" && len(parts) >= 2 && parts[1] == "config" {
			fieldValidation = "native_codex_skills.go"
		}
		if vendor == "copilot" {
			switch parts[0] {
			case "statusLine", "tabs", "commandHistoryMaxSize", "inlineImageLiveWindow":
				fieldValidation = "native_copilot_preferences.go"
			case "subagents":
				fieldValidation = "native_copilot_subagents.go"
			}
		}
		if nativePluginRoot(vendor, parts[0]) {
			fieldValidation = "native_plugins.go"
		}
		if nativePluginState(vendor, parts) {
			disposition = "external"
		}
		result = append(result, NativeSettingDeclaration{Path: path, Scopes: scopes, Disposition: disposition, Validation: fieldValidation, NativeStatus: "unverified"})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
