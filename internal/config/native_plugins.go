package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Marketplace and installed-package state remain native. These roots only
// declare sources, selection, and per-plugin configuration.
func nativePluginRoot(vendor, key string) bool {
	return vendor == "codex" && (key == "plugins" || key == "marketplaces") ||
		vendor == "copilot" && (key == "enabledPlugins" || key == "extraKnownMarketplaces")
}

func nativePluginState(vendor string, path []string) bool {
	return vendor == "codex" && len(path) == 3 && path[0] == "marketplaces" &&
		(path[2] == "last_revision" || path[2] == "last_updated")
}

func nativePluginCredential(value string) bool {
	if !strings.Contains(value, "://") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return true
	}
	// SSH user names identify accounts; passwords and HTTP user information
	// are credentials. No source URL may carry query credentials.
	if parsed.User != nil {
		if _, password := parsed.User.Password(); password || parsed.Scheme != "ssh" {
			return true
		}
	}
	return parsed.RawQuery != ""
}

func nativeCheckPluginImport(vendor string, values map[string]any) error {
	var scan func(any) bool
	scan = func(value any) bool {
		switch value := value.(type) {
		case string:
			return nativePluginCredential(value)
		case map[string]any:
			for key, child := range value {
				if nativePluginCredential(key) || scan(child) {
					return true
				}
			}
		case []any:
			for _, child := range value {
				if scan(child) {
					return true
				}
			}
		}
		return false
	}
	var excluded func([]string, any) bool
	excluded = func(path []string, value any) bool {
		if nativePluginState(vendor, path) {
			return false
		}
		if nativeFieldExcluded(vendor, path, value) {
			return true
		}
		switch fields := value.(type) {
		case map[string]any:
			for key, child := range fields {
				if excluded(append(append([]string(nil), path...), key), child) {
					return true
				}
			}
		case []any:
			for i, child := range fields {
				if excluded(append(append([]string(nil), path...), fmt.Sprint(i)), child) {
					return true
				}
			}
		}
		return false
	}
	for key, value := range values {
		if nativePluginRoot(vendor, key) && (scan(value) || excluded([]string{key}, value)) {
			return fmt.Errorf("plugin source contains excluded authority, credential, or invalid URL data; import is refused")
		}
	}
	return nil
}

func nativePluginName(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsAny(name, "\x00\r\n") && !nativePluginCredential(name)
}

func nativeSelectPlugins(vendor, scope string, values map[string]any) (map[string]any, []nativeInactiveField) {
	selected := map[string]any{}
	var inactive []nativeInactiveField
	for _, key := range nativeSortedKeys(values) {
		root := "/" + nativePointer(key)
		entries, ok := values[key].(map[string]any)
		if !nativePluginRoot(vendor, key) || !nativeSettingRegistry[vendor][scope][key] || !ok {
			inactive = append(inactive, nativeInactiveField{root, "inactive", "plugin selection must be a registered map"})
			continue
		}
		output := map[string]any{}
		for _, name := range nativeSortedKeys(entries) {
			value := entries[name]
			path := root + "/" + nativePointer(name)
			if !nativePluginName(name) || nativeCheckPluginImport(vendor, map[string]any{key: map[string]any{name: value}}) != nil {
				inactive = append(inactive, nativeInactiveField{root, "inactive", "invalid or credential-bearing plugin source"})
				continue
			}
			if key == "enabledPlugins" {
				if _, ok := value.(bool); ok {
					output[name] = value
				} else {
					inactive = append(inactive, nativeInactiveField{path, "inactive", "plugin selection must be boolean"})
				}
				continue
			}
			fields, ok := value.(map[string]any)
			if !ok {
				inactive = append(inactive, nativeInactiveField{path, "inactive", "plugin configuration must be an object"})
				continue
			}
			if vendor == "codex" {
				candidate := map[string]any{}
				for field, child := range fields {
					if nativePluginState(vendor, []string{key, name, field}) {
						inactive = append(inactive, nativeInactiveField{path + "/" + field, "external", "marketplace refresh state is native-owned"})
						continue
					}
					candidate[field] = child
				}
				if len(candidate) == 0 && len(fields) != 0 {
					continue
				}
				if !nativeCodexValue(key, map[string]any{name: candidate}) || nativeHasPermissionControl(vendor, []string{key, name}, candidate) || nativeContainsExcluded(vendor, []string{key, name}, candidate) {
					inactive = append(inactive, nativeInactiveField{path, "inactive", "plugin entry contains invalid, unmapped, or authority fields; the entire entry remains inactive"})
					continue
				}
				if key == "marketplaces" {
					if candidate["source_type"] == "local" {
						source, _ := candidate["source"].(string)
						if !filepath.IsAbs(source) {
							inactive = append(inactive, nativeInactiveField{path, "inactive", "local marketplace source must be an absolute path"})
							continue
						}
					}
				}
				output[name] = candidate
				continue
			}
			source, ok := fields["source"].(map[string]any)
			valid := ok && (len(fields) == 1 || len(fields) == 2 && fields["autoUpdate"] != nil)
			if update, exists := fields["autoUpdate"]; exists {
				if scope != "user" {
					inactive = append(inactive, nativeInactiveField{path, "inactive", "Copilot ignores marketplace autoUpdate in project scope; the entire entry remains inactive"})
					continue
				}
				_, typed := update.(bool)
				valid = valid && typed
			}
			valid = valid && len(source) == 2
			switch source["source"] {
			case "directory":
				location, typed := source["path"].(string)
				valid = valid && typed && filepath.IsAbs(location) && !strings.ContainsAny(location, "\x00\r\n")
			case "git":
				location, typed := source["url"].(string)
				parsed, err := url.Parse(location)
				valid = valid && typed && err == nil && parsed.Scheme != "" && !strings.ContainsAny(location, "\x00\r\n")
			case "github":
				repository, typed := source["repo"].(string)
				valid = valid && typed && regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repository)
			default:
				valid = false
			}
			if !valid {
				inactive = append(inactive, nativeInactiveField{path, "inactive", "marketplace source mapping is invalid or unverified; the entire entry remains inactive"})
				continue
			}
			output[name] = fields
		}
		if len(output) > 0 || len(entries) == 0 {
			selected[key] = output
		}
	}
	return selected, inactive
}

// Keep existing plugin selections in their declared artifact. Moving a root
// between profiles can change its required status. If a root has more than one
// source, import cannot choose its owner and must refuse before writes.
func nativeExistingPluginSources(root, vendor, namespace, scope, format string) (map[string]string, error) {
	owners := map[string]string{}
	for _, directory := range []string{"native", "plugins"} {
		if _, err := os.Lstat(filepath.Join(root, directory)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		profiles, err := readScopedNativeProfiles(root, directory)
		if err != nil {
			return nil, err
		}
		for _, profile := range profiles {
			if profile.Namespace != namespace || profile.Scope != scope {
				continue
			}
			for _, artifact := range profile.Artifacts {
				if artifact.Kind != "config" {
					continue
				}
				path := filepath.Join(root, directory, namespace, artifact.Source)
				data, err := nativeReadFile(path)
				if err != nil {
					return nil, err
				}
				values, err := parseNative(data, format)
				if err != nil {
					return nil, fmt.Errorf("cannot parse existing plugin selection artifact")
				}
				for key := range values {
					if nativePluginRoot(vendor, key) {
						if previous, ok := owners[key]; ok && previous != path {
							return nil, fmt.Errorf("plugin selection root %s has multiple source artifacts; import is ambiguous", key)
						}
						owners[key] = path
					}
				}
			}
		}
	}
	return owners, nil
}

func nativePluginFeature(vendor string, feature NativeFeature) NativeFeature {
	feature.NativeStatus = "native-discovery-only"
	feature.Limitation = "The pinned native CLI installs the unchanged Agent Plugins 1.0.0 skill fixture from a local marketplace. Enable, reload, disable, and package preservation are verified. Git fetching is tested separately over loopback HTTP, including a pinned Codex tag and Copilot default branch. Stdio MCP execution and native approvals have separate component evidence. Public HTTPS/SSH services, authentication, automatic updates, other package components, per-server overlays, and arbitrary third-party packages need separate evidence. Native trust and package installation remain external."
	if vendor == "copilot" {
		feature.Limitation += " Copilot 1.0.83 does not preserve unknown environment placeholders and leaves PLUGIN_DATA literal in the tested session environment value; full Agent Plugins environment conformance is not verified."
	}
	feature.Evidence = []string{"WORKBENCH/evidence/plugin-standard/" + vendor + "-selection-mcp-reviewed-" + feature.Scope + ".json", "WORKBENCH/evidence/plugin-standard/" + vendor + "-git-mcp-reviewed-" + feature.Scope + ".json"}
	return feature
}

func nativePluginCapabilities(vendor, scope, destination string) []NativeFeature {
	paths := []string{"/plugins/<name>/enabled", "/marketplaces/<name>/source_type", "/marketplaces/<name>/source", "/marketplaces/<name>/ref"}
	if vendor == "copilot" {
		paths = []string{"/enabledPlugins", "/extraKnownMarketplaces"}
	}
	var features []NativeFeature
	for _, path := range paths {
		features = append(features, nativePluginFeature(vendor, NativeFeature{
			Feature: "artifact:plugins:" + path, Source: "native_plugins.go", Destination: destination,
			Scope: scope, Disposition: "artifact-field-mapping", Activation: "requires native installation, trust, and reload",
			Ownership: "setting", Authority: "registry target; native trust remains external",
		}))
	}
	return append(features, nativePluginMCPFeature(vendor, scope))
}

func nativePluginMCPFeature(vendor, scope string) NativeFeature {
	feature := NativeFeature{Feature: "artifact:plugin-component:stdio", Source: "native package mcp.json", Scope: scope,
		Destination: "native plugin installation and data directories", Disposition: "native-operation",
		NativeStatus: "bounded-fixture-execution", Activation: "native installation, approval, and reload",
		Ownership: "external package and runtime data", Authority: "native trust and tool approval; no portable security mapping",
		Limitation: "The shared local Agent Plugins 1.0.0 stdio fixture executes through a deterministic local provider. Exact MCP requests, native completion, model input, data effects, explicit approval and denial, disable, and retained package/data bytes are verified. This is not full package-format conformance. Other transports, native extensions, and portable security combinations need separate evidence."}
	if vendor == "copilot" {
		feature.Limitation += " Copilot expands the unknown ODA_AMBIENT environment placeholder and leaves the PLUGIN_DATA placeholder literal in an environment value on session startup; these violate the standard environment requirements."
	} else {
		feature.Limitation += " Codex approval_policy=never refuses the plugin tool unless native tool policy already approves it."
	}
	for _, mode := range []string{"approve-allow", "prompt-allow", "prompt-deny"} {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/plugin-standard/"+vendor+"-mcp-final-"+mode+"-"+scope+".json")
	}
	if vendor == "codex" {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/plugin-standard/codex-mcp-final-never-allow-"+scope+".json")
	}
	return feature
}
