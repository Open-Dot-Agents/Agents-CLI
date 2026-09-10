package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Native discovery can read this directory without consulting the manifest.
// Do not modify canonical content or claim that refusal disables a harness.
func checkUnselectedSkills(vendor, root string, selected map[string]bool) error {
	if selected["skills"] || (vendor != "codex" && vendor != "copilot") {
		return nil
	}
	path := filepath.Join(root, ".agents", "skills")
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect unselected canonical skills: %w", err)
	}
	content := false
	for _, entry := range entries {
		if entry.Name() == ".gitkeep" && entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() == 0 {
				continue
			}
		}
		content = true
	}
	if content {
		return fmt.Errorf("ODA-ADAPTER-0003: %s cannot preserve an unselected skills profile while .agents/skills contains content; projection refused; direct harness use can still discover these skills", vendor)
	}
	return nil
}

// Refuse references before any writes, regardless of the current environment.
func referenceDiagnostics(vendor string, servers map[string]MCPServer) []string {
	var diagnostics []string
	for name, server := range servers {
		if vendor == "copilot" && (hasEnvironmentReferences(server.Env) || hasEnvironmentReferences(server.Headers)) {
			diagnostics = append(diagnostics, fmt.Sprintf("ODA-ADAPTER-0001: Copilot cannot safely represent environment reference for MCP server %q", name))
		}
		if vendor == "codex" && hasEnvironmentReferences(server.Env) {
			diagnostics = append(diagnostics, fmt.Sprintf("ODA-ADAPTER-0004: Codex cannot guarantee runtime failure for missing stdio environment references in MCP server %q; projection refused", name))
		}
	}
	sort.Strings(diagnostics)
	return diagnostics
}

func hasEnvironmentReferences(values map[string]string) bool {
	for _, value := range values {
		if isEnvironmentReference(value) {
			return true
		}
	}
	return false
}

// ValidateRepository enforces the versioned contract used by the public CLI.
// Validate remains available to legacy import/export callers.
func ValidateRepository(root string) error {
	if err := requireRegularFile(filepath.Join(root, "manifest.json"), "canonical manifest"); err != nil {
		return err
	}
	return Validate(root)
}

func requiredCapabilityDiagnostics(vendor, source string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(source, "manifest.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest manifestDocument
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	var diagnostics []string
	for _, capability := range manifest.Requires {
		status := vendorCompatibility[vendor].CapabilityStatus[capability]
		if status == "" || status == "unsupported" {
			diagnostics = append(diagnostics, fmt.Sprintf("ODA-ADAPTER-0005: %s cannot preserve explicitly required capability %q; projection refused", vendor, capability))
		}
	}
	sort.Strings(diagnostics)
	return diagnostics, nil
}

func requiredCapabilityDiagnosticsForSecurity(vendor, root string, security *SecurityPlan) ([]string, error) {
	diagnostics, err := requiredCapabilityDiagnostics(vendor, root)
	if err != nil || security == nil || security.Status != "native-subset" {
		return diagnostics, err
	}
	result := []string{}
	for _, diagnostic := range diagnostics {
		if security.Declared.Sandbox != nil && strings.Contains(diagnostic, `capability "sandbox"`) {
			continue
		}
		if security.Declared.Permissions != nil && strings.Contains(diagnostic, `capability "permissions"`) {
			continue
		}
		result = append(result, diagnostic)
	}
	return result, nil
}
