package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type mcpDocument struct {
	Servers map[string]MCPServer `json:"mcpServers"`
}

const (
	manifestVersion = "1.0.0"
	manifestStarter = `{
  "version": "` + manifestVersion + `",
  "profiles": ["instructions", "mcp", "skills"]
}
`
)

type manifestDocument struct {
	Schema   string   `json:"$schema"`
	Version  string   `json:"version"`
	Profiles []string `json:"profiles"`
}

// MCPServer contains the fields shared by the supported repository-scoped MCP formats.
type MCPServer struct {
	Type                     string            `json:"type,omitempty"`
	Command                  string            `json:"command,omitempty"`
	Args                     []string          `json:"args,omitempty"`
	Env                      map[string]string `json:"env,omitempty"`
	URL                      string            `json:"url,omitempty"`
	Headers                  map[string]string `json:"headers,omitempty"`
	StartupTimeoutSec        int               `json:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec           int               `json:"tool_timeout_sec,omitempty"`
	DefaultToolsApprovalMode string            `json:"default_tools_approval_mode,omitempty"`
}

type codexDocument struct {
	Servers map[string]codexServer `toml:"mcp_servers"`
}

type codexServer struct {
	Command                  string            `toml:"command,omitempty"`
	Args                     []string          `toml:"args,omitempty"`
	Env                      map[string]string `toml:"env,omitempty"`
	URL                      string            `toml:"url,omitempty"`
	HTTPHeaders              map[string]string `toml:"http_headers,omitempty"`
	StartupTimeoutSec        int               `toml:"startup_timeout_sec,omitempty"`
	ToolTimeoutSec           int               `toml:"tool_timeout_sec,omitempty"`
	DefaultToolsApprovalMode string            `toml:"default_tools_approval_mode,omitempty"`
}

// WriteOptions controls destructive configuration writes.
type WriteOptions struct {
	Force  bool
	Backup bool
}

// Capabilities describes the repository-scoped configuration supported for a vendor.
type Capabilities struct {
	Vendor         string            `json:"vendor"`
	Name           string            `json:"name"`
	Harness        string            `json:"harness"`
	HarnessVersion string            `json:"harness_version,omitempty"`
	Status         string            `json:"status"`
	Profiles       []string          `json:"profiles"`
	ProfileStatus  map[string]string `json:"profile_status"`
	Paths          map[string]string `json:"paths"`
	Evidence       string            `json:"evidence"`
	Limitations    []string          `json:"limitations,omitempty"`
}

type compatibilitySummary struct {
	Name           string
	Harness        string
	HarnessVersion string
	Status         string
	ProfileStatus  map[string]string
	Evidence       string
	Limitations    []string
}

var vendorCompatibility = map[string]compatibilitySummary{
	"copilot": {
		Name:    "Reference CLI: Copilot",
		Harness: "GitHub Copilot CLI",
		Status:  "not-conformance-supported",
		ProfileStatus: map[string]string{
			"instructions": "cli-projection-only",
			"mcp":          "cli-projection-only",
			"skills":       "cli-projection-only",
		},
		Evidence: "CLI unit tests only; no version-pinned native-harness black-box run",
		Limitations: []string{
			"No immutable native harness version is pinned",
			"No native black-box run covers instruction discovery, MCP startup, or skill discovery",
		},
	},
	"codex": {
		Name:    "Reference CLI: Codex",
		Harness: "OpenAI Codex CLI",
		Status:  "not-conformance-supported",
		ProfileStatus: map[string]string{
			"instructions": "cli-projection-only",
			"mcp":          "cli-projection-only",
			"skills":       "cli-projection-only",
		},
		Evidence: "CLI unit tests only; no version-pinned native-harness black-box run",
		Limitations: []string{
			"No immutable native harness version is pinned",
			"No native black-box run covers instruction discovery, MCP startup, or skill discovery",
		},
	},
	"claude": {
		Name:    "Reference CLI: Claude Code",
		Harness: "Claude Code",
		Status:  "not-conformance-supported",
		ProfileStatus: map[string]string{
			"instructions": "planned",
			"mcp":          "planned",
			"skills":       "planned",
		},
		Evidence: "No native adapter evidence or black-box run",
		Limitations: []string{
			"No native adapter evidence is recorded",
			"No immutable native harness version is pinned",
		},
	},
	"opencode": {
		Name:    "Reference CLI: OpenCode",
		Harness: "OpenCode",
		Status:  "not-conformance-supported",
		ProfileStatus: map[string]string{
			"instructions": "planned",
			"mcp":          "workbench-projection-only",
			"skills":       "planned",
		},
		Evidence: "Workbench projection tests only; no version-pinned native-harness black-box run",
		Limitations: []string{
			"No immutable native harness version is pinned",
			"No native black-box run covers instruction discovery, MCP startup, or skill discovery",
		},
	},
}

// Init creates a canonical .agents tree containing editable instruction, MCP, and skill stubs.
func Init(root string, force bool) error {
	agentsRoot := filepath.Join(root, ".agents")
	info, err := os.Stat(agentsRoot)
	if err == nil && !force {
		return fmt.Errorf("refusing to overwrite %q without --force", agentsRoot)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect output %q: %w", agentsRoot, err)
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("output %q is not a directory", agentsRoot)
	}

	stubs := map[string]string{
		"AGENTS.md": `# Repository Agent Instructions

Add shared instructions for every agent working in this repository.
`,
		"manifest.json": manifestStarter,
		"tools/mcp.json": `{
  "mcpServers": {}
}
`,
		"skills/example/SKILL.md": `# Example Skill

Describe when this skill applies and the steps an agent should follow.
`,
	}
	for relativePath, content := range stubs {
		if err := writeFile(filepath.Join(agentsRoot, relativePath), []byte(content), force); err != nil {
			return err
		}
	}
	return nil
}

// VendorCapabilities reports the configuration profiles and paths supported for vendor.
func VendorCapabilities(vendor string) (Capabilities, error) {
	vendor, err := normalizeVendor(vendor)
	if err != nil {
		return Capabilities{}, err
	}
	summary := vendorCompatibility[vendor]
	paths := map[string]string{
		"instructions": "AGENTS.md",
		"mcp":          slashPath(vendorMCPPath(vendor, ".")),
		"skills":       slashPath(vendorSkillsPath(vendor, ".")),
	}
	if vendor == "claude" {
		paths["instructions_bridge"] = "CLAUDE.md"
	}
	return Capabilities{
		Vendor:         vendor,
		Name:           summary.Name,
		Harness:        summary.Harness,
		HarnessVersion: summary.HarnessVersion,
		Status:         summary.Status,
		Profiles:       []string{"instructions", "mcp", "skills"},
		ProfileStatus:  summary.ProfileStatus,
		Paths:          paths,
		Evidence:       summary.Evidence,
		Limitations:    summary.Limitations,
	}, nil
}

// ExportTargetPaths returns the vendor files managed by an export from source.
func ExportTargetPaths(vendor, source, output string) ([]string, error) {
	vendor, err := normalizeVendor(vendor)
	if err != nil {
		return nil, err
	}
	profiles, err := canonicalProfiles(source)
	if err != nil {
		return nil, err
	}
	return vendorManagedPathsForProfiles(vendor, output, profiles), nil
}

// Validate verifies the structure and supported contents of a canonical .agents tree.
func Validate(root string) error {
	if err := requireDirectory(root, "canonical root"); err != nil {
		return err
	}
	profiles, hasManifest, err := validateManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		return err
	}
	selected := make(map[string]bool, len(profiles))
	if hasManifest {
		for _, profile := range profiles {
			selected[profile] = true
		}
	} else {
		selected["instructions"] = true
		selected["mcp"] = true
		selected["skills"] = true
	}
	if selected["instructions"] {
		instructionsPath := filepath.Join(root, "AGENTS.md")
		if hasManifest {
			if err := validateOptionalRegularFile(instructionsPath, "canonical instructions"); err != nil {
				return err
			}
		} else if err := requireRegularFile(instructionsPath, "canonical instructions"); err != nil {
			return err
		}
	}
	if selected["mcp"] {
		if err := validateCanonicalMCPFile(filepath.Join(root, "tools", "mcp.json"), hasManifest); err != nil {
			return err
		}
	}
	if selected["skills"] {
		return validateSkills(filepath.Join(root, "skills"))
	}
	return nil
}

func Import(vendor, source, output string, force bool) error {
	return ImportWithOptions(vendor, source, output, WriteOptions{Force: force})
}

// ImportWithOptions imports a vendor configuration into the portable model.
func ImportWithOptions(vendor, source, output string, options WriteOptions) error {
	if err := validateWriteOptions(options); err != nil {
		return err
	}
	vendor, err := normalizeVendor(vendor)
	if err != nil {
		return err
	}
	servers, err := readVendorMCP(vendor, source)
	if err != nil {
		return err
	}
	if err := prepareOverwrite(options,
		output,
	); err != nil {
		return err
	}
	if err := writeCanonicalMCP(output, servers, options.Force); err != nil {
		return err
	}
	if err := copySkills(vendorSkillsPath(vendor, source), filepath.Join(output, "skills"), options.Force); err != nil {
		return err
	}
	return copyInstructions(filepath.Join(source, "AGENTS.md"), filepath.Join(output, "AGENTS.md"), options.Force)
}

func Export(vendor, source, output string, force bool) error {
	return ExportWithOptions(vendor, source, output, WriteOptions{Force: force})
}

// ExportWithOptions exports the portable model to a vendor configuration.
func ExportWithOptions(vendor, source, output string, options WriteOptions) error {
	if err := validateWriteOptions(options); err != nil {
		return err
	}
	vendor, err := normalizeVendor(vendor)
	if err != nil {
		return err
	}
	profiles, err := canonicalProfiles(source)
	if err != nil {
		return err
	}
	var servers map[string]MCPServer
	if profiles["mcp"] {
		servers, err = readCanonicalMCP(source)
		if err != nil {
			return err
		}
	}
	if err := prepareOverwrite(options, vendorManagedPathsForProfiles(vendor, output, profiles)...); err != nil {
		return err
	}
	if profiles["mcp"] {
		if err := writeVendorMCP(vendor, output, servers, options.Force); err != nil {
			return err
		}
	}
	if profiles["skills"] {
		if err := copySkills(filepath.Join(source, "skills"), vendorSkillsPath(vendor, output), options.Force); err != nil {
			return err
		}
	}
	if profiles["instructions"] {
		if err := copyInstructions(filepath.Join(source, "AGENTS.md"), filepath.Join(output, "AGENTS.md"), options.Force); err != nil {
			return err
		}
		return writeClaudeBridge(vendor, filepath.Join(source, "AGENTS.md"), output, options.Force)
	}
	return nil
}

func Convert(from, to, source, output string, force bool) error {
	return ConvertWithOptions(from, to, source, output, WriteOptions{Force: force})
}

// ConvertWithOptions converts one vendor configuration directly to another.
func ConvertWithOptions(from, to, source, output string, options WriteOptions) error {
	if err := validateWriteOptions(options); err != nil {
		return err
	}
	from, err := normalizeVendor(from)
	if err != nil {
		return err
	}
	to, err = normalizeVendor(to)
	if err != nil {
		return err
	}
	servers, err := readVendorMCP(from, source)
	if err != nil {
		return err
	}
	if err := prepareOverwrite(options, vendorManagedPaths(to, output)...); err != nil {
		return err
	}
	if err := writeVendorMCP(to, output, servers, options.Force); err != nil {
		return err
	}
	if err := copySkills(vendorSkillsPath(from, source), vendorSkillsPath(to, output), options.Force); err != nil {
		return err
	}
	if err := copyInstructions(filepath.Join(source, "AGENTS.md"), filepath.Join(output, "AGENTS.md"), options.Force); err != nil {
		return err
	}
	return writeClaudeBridge(to, filepath.Join(source, "AGENTS.md"), output, options.Force)
}

func normalizeVendor(vendor string) (string, error) {
	switch strings.ToLower(vendor) {
	case "copilot":
		return "copilot", nil
	case "codex":
		return "codex", nil
	case "claude":
		return "claude", nil
	case "opencode":
		return "opencode", nil
	default:
		return "", fmt.Errorf("unsupported vendor %q (supported: copilot, codex, claude, opencode)", vendor)
	}
}

func readCanonicalMCP(root string) (map[string]MCPServer, error) {
	path := filepath.Join(root, "tools", "mcp.json")
	_, hasManifest, err := validateManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if err := validateCanonicalMCPFile(path, hasManifest); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canonical MCP configuration %q: %w", path, err)
	}
	var document mcpDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse canonical MCP configuration %q: %w", path, err)
	}
	if hasManifest {
		return validateCanonicalServers(document.Servers)
	}
	return canonicalizeVendorServers(document.Servers)
}

func writeCanonicalMCP(root string, servers map[string]MCPServer, force bool) error {
	servers, err := canonicalizeVendorServers(servers)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(mcpDocument{Servers: servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode canonical MCP configuration: %w", err)
	}
	return writeFile(filepath.Join(root, "tools", "mcp.json"), append(data, '\n'), force)
}

func readVendorMCP(vendor, root string) (map[string]MCPServer, error) {
	path := vendorMCPPath(vendor, root)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s MCP configuration %q: %w", vendor, path, err)
	}

	if vendor == "copilot" {
		var document mcpDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse Copilot MCP configuration %q: %w", path, err)
		}
		return canonicalizeVendorServers(document.Servers)
	}
	if vendor == "claude" {
		var document mcpDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return nil, fmt.Errorf("parse Claude MCP configuration %q: %w", path, err)
		}
		return canonicalizeVendorServers(document.Servers)
	}
	if vendor == "opencode" {
		return readOpenCodeMCP(path, data)
	}

	var document codexDocument
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse Codex MCP configuration %q: %w", path, err)
	}
	servers := make(map[string]MCPServer, len(document.Servers))
	for name, server := range document.Servers {
		servers[name] = MCPServer{
			Command:                  server.Command,
			Args:                     server.Args,
			Env:                      server.Env,
			URL:                      server.URL,
			Headers:                  server.HTTPHeaders,
			StartupTimeoutSec:        server.StartupTimeoutSec,
			ToolTimeoutSec:           server.ToolTimeoutSec,
			DefaultToolsApprovalMode: server.DefaultToolsApprovalMode,
		}
	}
	return canonicalizeVendorServers(servers)
}

func writeVendorMCP(vendor, root string, servers map[string]MCPServer, force bool) error {
	servers, err := validateCanonicalServers(servers)
	if err != nil {
		return err
	}
	path := vendorMCPPath(vendor, root)
	if vendor == "copilot" {
		data, err := json.MarshalIndent(mcpDocument{Servers: vendorMCPServers(servers)}, "", "  ")
		if err != nil {
			return fmt.Errorf("encode Copilot MCP configuration: %w", err)
		}
		return writeFile(path, append(data, '\n'), force)
	}
	if vendor == "claude" {
		data, err := json.MarshalIndent(mcpDocument{Servers: vendorMCPServers(servers)}, "", "  ")
		if err != nil {
			return fmt.Errorf("encode Claude MCP configuration: %w", err)
		}
		return writeFile(path, append(data, '\n'), force)
	}
	if vendor == "opencode" {
		return writeOpenCodeMCP(path, servers, force)
	}

	codexServers := make(map[string]codexServer, len(servers))
	for name, server := range servers {
		codexServers[name] = codexServer{
			Command:                  server.Command,
			Args:                     server.Args,
			Env:                      server.Env,
			URL:                      server.URL,
			HTTPHeaders:              server.Headers,
			StartupTimeoutSec:        server.StartupTimeoutSec,
			ToolTimeoutSec:           server.ToolTimeoutSec,
			DefaultToolsApprovalMode: server.DefaultToolsApprovalMode,
		}
	}
	data, err := toml.Marshal(codexDocument{Servers: codexServers})
	if err != nil {
		return fmt.Errorf("encode Codex MCP configuration: %w", err)
	}
	return writeFile(path, data, force)
}

func vendorMCPPath(vendor, root string) string {
	switch vendor {
	case "copilot":
		return filepath.Join(root, ".github", "mcp.json")
	case "codex":
		return filepath.Join(root, ".codex", "config.toml")
	case "claude":
		return filepath.Join(root, ".mcp.json")
	case "opencode":
		return filepath.Join(root, "opencode.json")
	default:
		return ""
	}
}

func vendorSkillsPath(vendor, root string) string {
	if vendor == "claude" {
		return filepath.Join(root, ".claude", "skills")
	}
	return filepath.Join(root, ".agents", "skills")
}

func vendorManagedPaths(vendor, root string) []string {
	return vendorManagedPathsForProfiles(vendor, root, map[string]bool{
		"instructions": true,
		"mcp":          true,
		"skills":       true,
	})
}

func vendorManagedPathsForProfiles(vendor, root string, profiles map[string]bool) []string {
	paths := make([]string, 0, 4)
	if profiles["mcp"] {
		switch vendor {
		case "copilot":
			paths = append(paths, filepath.Join(root, ".github"))
		case "codex":
			paths = append(paths, filepath.Join(root, ".codex"))
		case "claude":
			paths = append(paths, filepath.Join(root, ".mcp.json"))
		case "opencode":
			paths = append(paths, filepath.Join(root, "opencode.json"))
		}
	}
	if profiles["skills"] {
		if vendor == "claude" {
			paths = append(paths, filepath.Join(root, ".claude"))
		} else {
			paths = append(paths, filepath.Join(root, ".agents"))
		}
	}
	if profiles["instructions"] {
		paths = append(paths, filepath.Join(root, "AGENTS.md"))
		if vendor == "claude" {
			paths = append(paths, filepath.Join(root, "CLAUDE.md"))
		}
	}
	return paths
}

func canonicalProfiles(root string) (map[string]bool, error) {
	profiles, hasManifest, err := validateManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(profiles))
	if !hasManifest {
		selected["instructions"] = true
		selected["mcp"] = true
		selected["skills"] = true
		return selected, nil
	}
	for _, profile := range profiles {
		switch profile {
		case "instructions", "mcp", "skills":
			selected[profile] = true
		}
	}
	return selected, nil
}

func writeClaudeBridge(vendor, instructionsSource, output string, force bool) error {
	if vendor != "claude" {
		return nil
	}
	if err := rejectSymlinkPath(instructionsSource); err != nil {
		return err
	}
	info, err := os.Lstat(instructionsSource)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Claude instructions source %q: %w", instructionsSource, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Claude instructions source %q is not a regular file", instructionsSource)
	}
	return writeFile(filepath.Join(output, "CLAUDE.md"), []byte("@AGENTS.md\n"), force)
}

func readOpenCodeMCP(path string, data []byte) (map[string]MCPServer, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil || document == nil {
		if err == nil {
			err = errors.New("root must be an object")
		}
		return nil, fmt.Errorf("parse OpenCode configuration %q: %w", path, err)
	}
	rawMCP, ok := document["mcp"]
	if !ok || isJSONNull(rawMCP) {
		return nil, fmt.Errorf("OpenCode configuration %q must contain an mcp object", path)
	}
	var rawServers map[string]json.RawMessage
	if err := json.Unmarshal(rawMCP, &rawServers); err != nil || rawServers == nil {
		if err == nil {
			err = errors.New("mcp must be an object")
		}
		return nil, fmt.Errorf("parse OpenCode MCP configuration %q: %w", path, err)
	}
	servers := make(map[string]MCPServer, len(rawServers))
	for name, rawServer := range rawServers {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("OpenCode MCP server name cannot be empty")
		}
		server, err := parseOpenCodeServer(name, rawServer)
		if err != nil {
			return nil, err
		}
		servers[name] = server
	}
	return canonicalizeVendorServers(servers)
}

func parseOpenCodeServer(name string, rawServer json.RawMessage) (MCPServer, error) {
	if isJSONNull(rawServer) {
		return MCPServer{}, fmt.Errorf("OpenCode MCP server %q must be an object", name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawServer, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("server must be an object")
		}
		return MCPServer{}, fmt.Errorf("parse OpenCode MCP server %q: %w", name, err)
	}
	rawType, ok := fields["type"]
	if !ok || validateJSONString(rawType) != nil {
		return MCPServer{}, fmt.Errorf("OpenCode MCP server %q must define a string type", name)
	}
	var typeName string
	_ = json.Unmarshal(rawType, &typeName)

	switch typeName {
	case "local":
		if err := requireOpenCodeFields(name, fields, "type", "command", "environment", "enabled"); err != nil {
			return MCPServer{}, err
		}
		if err := validateOpenCodeEnabled(name, fields); err != nil {
			return MCPServer{}, err
		}
		rawCommand, ok := fields["command"]
		if !ok || validateJSONStringList(rawCommand) != nil {
			return MCPServer{}, fmt.Errorf("OpenCode local MCP server %q must define command as a list of strings", name)
		}
		var command []string
		_ = json.Unmarshal(rawCommand, &command)
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return MCPServer{}, fmt.Errorf("OpenCode local MCP server %q must define a non-empty command", name)
		}
		environment, err := parseOpenCodeStringMap(name, fields, "environment")
		if err != nil {
			return MCPServer{}, err
		}
		return MCPServer{Type: "stdio", Command: command[0], Args: command[1:], Env: environment}, nil
	case "remote":
		if err := requireOpenCodeFields(name, fields, "type", "url", "headers", "enabled"); err != nil {
			return MCPServer{}, err
		}
		if err := validateOpenCodeEnabled(name, fields); err != nil {
			return MCPServer{}, err
		}
		rawURL, ok := fields["url"]
		if !ok || validateJSONString(rawURL) != nil {
			return MCPServer{}, fmt.Errorf("OpenCode remote MCP server %q must define url as a string", name)
		}
		var url string
		_ = json.Unmarshal(rawURL, &url)
		if strings.TrimSpace(url) == "" {
			return MCPServer{}, fmt.Errorf("OpenCode remote MCP server %q must define a non-empty url", name)
		}
		headers, err := parseOpenCodeStringMap(name, fields, "headers")
		if err != nil {
			return MCPServer{}, err
		}
		return MCPServer{Type: "remote", URL: url, Headers: headers}, nil
	default:
		return MCPServer{}, fmt.Errorf("OpenCode MCP server %q has unsupported type %q", name, typeName)
	}
}

func requireOpenCodeFields(name string, fields map[string]json.RawMessage, allowed ...string) error {
	allowedFields := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	for field := range fields {
		if _, ok := allowedFields[field]; !ok {
			return fmt.Errorf("OpenCode MCP server %q has unsupported field %q", name, field)
		}
	}
	return nil
}

func parseOpenCodeStringMap(name string, fields map[string]json.RawMessage, field string) (map[string]string, error) {
	rawValue, ok := fields[field]
	if !ok {
		return nil, nil
	}
	if err := validateStringMap(rawValue); err != nil {
		return nil, fmt.Errorf("OpenCode MCP server %q field %q must be a map of strings", name, field)
	}
	var values map[string]string
	_ = json.Unmarshal(rawValue, &values)
	return values, nil
}

func validateOpenCodeEnabled(name string, fields map[string]json.RawMessage) error {
	rawEnabled, ok := fields["enabled"]
	if !ok {
		return nil
	}
	if isJSONNull(rawEnabled) {
		return fmt.Errorf("OpenCode MCP server %q field enabled must be a boolean", name)
	}
	var enabled bool
	if err := json.Unmarshal(rawEnabled, &enabled); err != nil {
		return fmt.Errorf("OpenCode MCP server %q field enabled must be a boolean", name)
	}
	if !enabled {
		return fmt.Errorf("OpenCode MCP server %q is disabled and cannot be represented canonically", name)
	}
	return nil
}

func writeOpenCodeMCP(path string, servers map[string]MCPServer, force bool) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	document := make(map[string]json.RawMessage)
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &document); err != nil || document == nil {
			if err == nil {
				err = errors.New("root must be an object")
			}
			return fmt.Errorf("parse existing OpenCode configuration %q: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read existing OpenCode configuration %q: %w", path, err)
	}

	openCodeServers := make(map[string]any, len(servers))
	for name, server := range servers {
		projected, err := projectOpenCodeServer(name, server)
		if err != nil {
			return err
		}
		openCodeServers[name] = projected
	}
	mcpData, err := json.Marshal(openCodeServers)
	if err != nil {
		return fmt.Errorf("encode OpenCode MCP configuration: %w", err)
	}
	document["mcp"] = mcpData
	output, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OpenCode configuration: %w", err)
	}
	return writeFile(path, append(output, '\n'), force)
}

func projectOpenCodeServer(name string, server MCPServer) (map[string]any, error) {
	if server.Command != "" {
		if server.URL != "" {
			return nil, fmt.Errorf("cannot project OpenCode MCP server %q with both command and url", name)
		}
		if server.Type != "" && server.Type != "stdio" {
			return nil, fmt.Errorf("cannot project OpenCode MCP server %q type %q as local", name, server.Type)
		}
		if len(server.Headers) != 0 || server.StartupTimeoutSec != 0 || server.ToolTimeoutSec != 0 || server.DefaultToolsApprovalMode != "" {
			return nil, fmt.Errorf("cannot project unsupported fields for OpenCode local MCP server %q", name)
		}
		command := append([]string{server.Command}, server.Args...)
		environment := server.Env
		if environment == nil {
			environment = map[string]string{}
		}
		return map[string]any{"type": "local", "command": command, "environment": environment}, nil
	}
	if server.URL != "" {
		if server.Type == "stdio" {
			return nil, fmt.Errorf("cannot project OpenCode MCP server %q type stdio as remote", name)
		}
		if len(server.Args) != 0 || len(server.Env) != 0 || server.StartupTimeoutSec != 0 || server.ToolTimeoutSec != 0 || server.DefaultToolsApprovalMode != "" {
			return nil, fmt.Errorf("cannot project unsupported fields for OpenCode remote MCP server %q", name)
		}
		headers := server.Headers
		if headers == nil {
			headers = map[string]string{}
		}
		return map[string]any{"type": "remote", "url": server.URL, "headers": headers}, nil
	}
	return nil, fmt.Errorf("cannot project OpenCode MCP server %q without command or url", name)
}

func canonicalizeVendorServers(servers map[string]MCPServer) (map[string]MCPServer, error) {
	canonical := make(map[string]MCPServer, len(servers))
	for name, server := range servers {
		switch {
		case server.Command != "":
			if server.URL != "" {
				return nil, fmt.Errorf("MCP server %q cannot define both command and url", name)
			}
			if server.Type != "" && server.Type != "stdio" {
				return nil, fmt.Errorf("MCP server %q has unsupported local type %q", name, server.Type)
			}
			server.Type = "stdio"
		case server.URL != "":
			switch server.Type {
			case "", "http", "sse", "remote":
				server.Type = "remote"
			default:
				return nil, fmt.Errorf("MCP server %q has unsupported remote type %q", name, server.Type)
			}
		default:
			return nil, fmt.Errorf("MCP server %q must define command or url", name)
		}
		canonical[name] = server
	}
	return validateCanonicalServers(canonical)
}

func validateCanonicalServers(servers map[string]MCPServer) (map[string]MCPServer, error) {
	if servers == nil {
		return make(map[string]MCPServer), nil
	}
	for name, server := range servers {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("MCP server name cannot be empty")
		}
		if server.Type == "" {
			if server.Command != "" {
				server.Type = "stdio"
			} else if server.URL != "" {
				server.Type = "remote"
			}
		}
		switch server.Type {
		case "stdio":
			if strings.TrimSpace(server.Command) == "" || server.URL != "" {
				return nil, fmt.Errorf("MCP server %q with type stdio must define command and no url", name)
			}
		case "remote":
			if strings.TrimSpace(server.URL) == "" || server.Command != "" {
				return nil, fmt.Errorf("MCP server %q with type remote must define url and no command", name)
			}
		default:
			return nil, fmt.Errorf("MCP server %q has unsupported canonical type %q", name, server.Type)
		}
		servers[name] = server
	}
	return servers, nil
}

func vendorMCPServers(servers map[string]MCPServer) map[string]MCPServer {
	vendorServers := make(map[string]MCPServer, len(servers))
	for name, server := range servers {
		if server.Type == "remote" {
			server.Type = "http"
		}
		vendorServers[name] = server
	}
	return vendorServers
}

func writeFile(path string, data []byte, force bool) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil && !force {
		return fmt.Errorf("refusing to overwrite %q without --force", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect output %q: %w", path, err)
	}
	return writeManagedFile(path, data, 0o644)
}

func writeManagedFile(path string, data []byte, permissions fs.FileMode) error {
	if err := safeMkdirAll(filepath.Dir(path)); err != nil {
		return fmt.Errorf("create output directory for %q: %w", path, err)
	}
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, permissions); err != nil {
		return fmt.Errorf("write output %q: %w", path, err)
	}
	return nil
}

func safeMkdirAll(path string) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return rejectSymlinkPath(path)
}

func rejectSymlinkPath(path string) error {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve managed path %q: %w", path, err)
	}
	volume := filepath.VolumeName(absolutePath)
	remaining := strings.TrimPrefix(absolutePath, volume)
	root := volume + string(filepath.Separator)
	components := strings.FieldsFunc(remaining, func(character rune) bool {
		return character == filepath.Separator
	})
	current := root
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect managed path %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if allowedSystemSymlinkAncestor(current) {
				continue
			}
			return fmt.Errorf("refusing managed path %q because %q is a symlink", path, current)
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("managed path component %q is not a directory", current)
		}
	}
	return nil
}

func allowedSystemSymlinkAncestor(path string) bool {
	tempDirectory, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(path, tempDirectory)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func slashPath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

func prepareOverwrite(options WriteOptions, paths ...string) error {
	if err := validateWriteOptions(options); err != nil {
		return err
	}
	if !options.Backup {
		return nil
	}

	for _, path := range paths {
		if err := backupPath(path); err != nil {
			return err
		}
	}
	return nil
}

func validateWriteOptions(options WriteOptions) error {
	if options.Backup && !options.Force {
		return errors.New("--backup requires --force")
	}
	return nil
}

func backupPath(path string) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup source %q: %w", path, err)
	}

	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := path + ".backup-" + timestamp
	for index := 1; ; index++ {
		if err := rejectSymlinkPath(backupPath); err != nil {
			return err
		}
		if _, err := os.Lstat(backupPath); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return fmt.Errorf("inspect backup destination %q: %w", backupPath, err)
		}
		backupPath = fmt.Sprintf("%s.backup-%s-%d", path, timestamp, index)
	}
	if info.IsDir() {
		if err := copyDirectory(path, backupPath); err != nil {
			return fmt.Errorf("back up directory %q: %w", path, err)
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source %q is not a regular file or directory", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read backup source %q: %w", path, err)
	}
	if err := writeManagedFile(backupPath, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write backup destination %q: %w", backupPath, err)
	}
	if err := os.Chmod(backupPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set backup permissions %q: %w", backupPath, err)
	}
	return nil
}

func copyDirectory(source, output string) error {
	if err := rejectSymlinkPath(source); err != nil {
		return err
	}
	if err := rejectSymlinkPath(output); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(output, relative)
		if entry.IsDir() {
			return safeMkdirAll(destination)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular file %q", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeManagedFile(destination, data, 0o644)
	})
}

func copySkills(source, output string, force bool) error {
	if err := rejectSymlinkPath(source); err != nil {
		return err
	}
	if err := rejectSymlinkPath(output); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect skills source %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("skills source %q is not a directory", source)
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve skills source %q: %w", source, err)
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve skills output %q: %w", output, err)
	}
	if sourceAbs == outputAbs {
		return nil
	}
	if _, err := os.Lstat(output); err == nil && !force {
		return fmt.Errorf("refusing to overwrite skills at %q without --force", output)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect skills output %q: %w", output, err)
	}

	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(output, relative)
		if entry.IsDir() {
			return safeMkdirAll(destination)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular skill file %q", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeManagedFile(destination, data, 0o644)
	})
}

func copyInstructions(source, output string, force bool) error {
	if err := rejectSymlinkPath(source); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect instructions source %q: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("instructions source %q is not a regular file", source)
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve instructions source %q: %w", source, err)
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve instructions output %q: %w", output, err)
	}
	if sourceAbs == outputAbs {
		return nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read instructions source %q: %w", source, err)
	}
	return writeFile(output, data, force)
}

func validateManifest(path string) ([]string, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err := requireRegularFile(path, "manifest"); err != nil {
		return nil, true, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, true, fmt.Errorf("read manifest %q: %w", path, err)
	}
	var manifest manifestDocument
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, true, fmt.Errorf("parse manifest %q: %w", path, err)
	}
	if manifest.Version != manifestVersion {
		return nil, true, fmt.Errorf("manifest %q has unsupported version %q (supported: %s)", path, manifest.Version, manifestVersion)
	}
	if manifest.Profiles == nil {
		return nil, true, fmt.Errorf("manifest %q must define profiles", path)
	}
	if len(manifest.Profiles) == 0 {
		return nil, true, fmt.Errorf("manifest %q must define at least one profile", path)
	}
	seenProfiles := make(map[string]struct{}, len(manifest.Profiles))
	for _, profile := range manifest.Profiles {
		if !isPortableProfileName(profile) {
			return nil, true, fmt.Errorf("manifest %q has invalid profile %q", path, profile)
		}
		if _, seen := seenProfiles[profile]; seen {
			return nil, true, fmt.Errorf("manifest %q has duplicate profile %q", path, profile)
		}
		seenProfiles[profile] = struct{}{}
	}
	return manifest.Profiles, true, nil
}

func isPortableProfileName(profile string) bool {
	if len(profile) == 0 || len(profile) > 64 {
		return false
	}
	for index, character := range profile {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			continue
		}
		if character == '-' && index > 0 && index < len(profile)-1 {
			continue
		}
		return false
	}
	return true
}

func validateCanonicalMCPFile(path string, strict bool) error {
	if err := requireDirectory(filepath.Dir(path), "canonical tools directory"); err != nil {
		return err
	}
	if err := requireRegularFile(path, "canonical MCP configuration"); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read canonical MCP configuration %q: %w", path, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse canonical MCP configuration %q: %w", path, err)
	}
	serversData, ok := root["mcpServers"]
	if !ok || len(root) > 2 {
		return fmt.Errorf("canonical MCP configuration %q must contain only $schema and mcpServers at the root", path)
	}
	for field, value := range root {
		if field != "mcpServers" && field != "$schema" {
			return fmt.Errorf("canonical MCP configuration %q has unsupported root field %q", path, field)
		}
		if field == "$schema" {
			if err := validateJSONString(value); err != nil {
				return fmt.Errorf("canonical MCP configuration %q field $schema must be a string", path)
			}
		}
	}
	if isJSONNull(serversData) {
		return fmt.Errorf("canonical MCP configuration %q mcpServers must be an object", path)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversData, &servers); err != nil {
		return fmt.Errorf("canonical MCP configuration %q mcpServers must be an object: %w", path, err)
	}
	for name, rawServer := range servers {
		if strings.TrimSpace(name) == "" {
			return errors.New("MCP server name cannot be empty")
		}
		if err := validateCanonicalServer(name, rawServer, strict); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalServer(name string, rawServer json.RawMessage, strict bool) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawServer, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("server must be an object")
		}
		return fmt.Errorf("MCP server %q must be an object: %w", name, err)
	}
	if !strict {
		return nil
	}
	rawType, ok := fields["type"]
	if !ok || validateJSONString(rawType) != nil {
		return fmt.Errorf("MCP server %q must define type as a string", name)
	}
	var typeName string
	_ = json.Unmarshal(rawType, &typeName)
	switch typeName {
	case "stdio":
		return validateCanonicalStdioServer(name, fields)
	case "remote":
		return validateCanonicalRemoteServer(name, fields)
	default:
		return fmt.Errorf("MCP server %q has unsupported canonical type %q", name, typeName)
	}
}

func validateCanonicalStdioServer(name string, fields map[string]json.RawMessage) error {
	allowed := map[string]func(json.RawMessage) error{
		"type":    validateJSONString,
		"command": validateJSONString,
		"args":    validateJSONStringList,
		"env":     validateEnvironmentReferences,
	}
	for field, value := range fields {
		validate, ok := allowed[field]
		if !ok {
			return fmt.Errorf("MCP server %q has unsupported field %q", name, field)
		}
		if err := validate(value); err != nil {
			return fmt.Errorf("MCP server %q field %q is invalid: %w", name, field, err)
		}
	}
	rawCommand, ok := fields["command"]
	if !ok || validateJSONString(rawCommand) != nil {
		return fmt.Errorf("MCP server %q with type stdio must define command as a string", name)
	}
	var command string
	_ = json.Unmarshal(rawCommand, &command)
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("MCP server %q with type stdio must define a non-empty command", name)
	}
	return nil
}

func validateCanonicalRemoteServer(name string, fields map[string]json.RawMessage) error {
	allowed := map[string]func(json.RawMessage) error{
		"type":    validateJSONString,
		"url":     validateJSONString,
		"headers": validateHeaderReferences,
	}
	for field, value := range fields {
		validate, ok := allowed[field]
		if !ok {
			return fmt.Errorf("MCP server %q has unsupported field %q", name, field)
		}
		if err := validate(value); err != nil {
			return fmt.Errorf("MCP server %q field %q is invalid: %w", name, field, err)
		}
	}
	rawURL, ok := fields["url"]
	if !ok || validateJSONString(rawURL) != nil {
		return fmt.Errorf("MCP server %q with type remote must define url as a string", name)
	}
	var rawURLString string
	_ = json.Unmarshal(rawURL, &rawURLString)
	parsedURL, err := url.Parse(rawURLString)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("MCP server %q with type remote must define an HTTPS url", name)
	}
	return nil
}

func validateJSONString(value json.RawMessage) error {
	if isJSONNull(value) {
		return errors.New("must be a string")
	}
	var stringValue string
	if err := json.Unmarshal(value, &stringValue); err != nil {
		return errors.New("must be a string")
	}
	return nil
}

func validateJSONStringList(value json.RawMessage) error {
	if isJSONNull(value) {
		return errors.New("must be a list of strings")
	}
	var values []string
	if err := json.Unmarshal(value, &values); err != nil {
		return errors.New("must be a list of strings")
	}
	return nil
}

func validateStringMap(value json.RawMessage) error {
	if isJSONNull(value) {
		return errors.New("must be a map of strings")
	}
	var values map[string]string
	if err := json.Unmarshal(value, &values); err != nil {
		return errors.New("must be a map of strings")
	}
	return nil
}

func validateEnvironmentReferences(value json.RawMessage) error {
	values, err := decodeStringMap(value)
	if err != nil {
		return err
	}
	for name, reference := range values {
		if !isEnvironmentName(name) || !isEnvironmentReference(reference) {
			return errors.New("must be environment references in urn:open-dot-agents:env:VARIABLE form")
		}
	}
	return nil
}

func validateHeaderReferences(value json.RawMessage) error {
	values, err := decodeStringMap(value)
	if err != nil {
		return err
	}
	for _, reference := range values {
		if !isEnvironmentReference(reference) {
			return errors.New("must be header references in urn:open-dot-agents:env:VARIABLE form")
		}
	}
	return nil
}

func decodeStringMap(value json.RawMessage) (map[string]string, error) {
	if err := validateStringMap(value); err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal(value, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func isEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '_' {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func isEnvironmentReference(reference string) bool {
	const prefix = "urn:open-dot-agents:env:"
	return strings.HasPrefix(reference, prefix) && isEnvironmentName(strings.TrimPrefix(reference, prefix))
}

func validateNonNegativeInt(value json.RawMessage) error {
	if isJSONNull(value) {
		return errors.New("must be a non-negative integer")
	}
	var number int
	if err := json.Unmarshal(value, &number); err != nil || number < 0 {
		return errors.New("must be a non-negative integer")
	}
	return nil
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func validateSkills(path string) error {
	if err := requireDirectory(path, "canonical skills directory"); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read canonical skills directory %q: %w", path, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("canonical skill %q must be a directory", entry.Name())
		}
		if !isSafeSkillName(entry.Name()) {
			return fmt.Errorf("unsafe skill name %q", entry.Name())
		}
		skillRoot := filepath.Join(path, entry.Name())
		if err := requireRegularFile(filepath.Join(skillRoot, "SKILL.md"), "skill definition"); err != nil {
			return err
		}
		if err := filepath.WalkDir(skillRoot, func(filePath string, file fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if file.IsDir() || file.Type().IsRegular() {
				return nil
			}
			return fmt.Errorf("canonical skill %q contains non-regular file %q", entry.Name(), filePath)
		}); err != nil {
			return err
		}
	}
	return nil
}

func isSafeSkillName(name string) bool {
	if name == "" || name == "." || name == ".." || name[0] == '.' {
		return false
	}
	for _, character := range name {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}

func requireDirectory(path, description string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %q: %w", description, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", description, path)
	}
	return nil
}

func requireRegularFile(path, description string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s %q: %w", description, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q is not a regular file", description, path)
	}
	return nil
}

func validateOptionalRegularFile(path, description string) error {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return requireRegularFile(path, description)
}
