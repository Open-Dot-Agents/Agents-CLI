package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type mcpDocument struct {
	Servers map[string]MCPServer `json:"mcpServers"`
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
	return copySkills(filepath.Join(source, ".agents", "skills"), filepath.Join(output, "skills"), options.Force)
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
	servers, err := readCanonicalMCP(source)
	if err != nil {
		return err
	}
	if err := prepareOverwrite(options,
		filepath.Dir(vendorMCPPath(vendor, output)),
		filepath.Join(output, ".agents"),
	); err != nil {
		return err
	}
	if err := writeVendorMCP(vendor, output, servers, options.Force); err != nil {
		return err
	}
	return copySkills(filepath.Join(source, "skills"), filepath.Join(output, ".agents", "skills"), options.Force)
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
	if err := prepareOverwrite(options,
		filepath.Dir(vendorMCPPath(to, output)),
		filepath.Join(output, ".agents"),
	); err != nil {
		return err
	}
	if err := writeVendorMCP(to, output, servers, options.Force); err != nil {
		return err
	}
	return copySkills(filepath.Join(source, ".agents", "skills"), filepath.Join(output, ".agents", "skills"), options.Force)
}

func normalizeVendor(vendor string) (string, error) {
	switch strings.ToLower(vendor) {
	case "copilot":
		return "copilot", nil
	case "codex":
		return "codex", nil
	default:
		return "", fmt.Errorf("unsupported vendor %q (supported: copilot, codex)", vendor)
	}
}

func readCanonicalMCP(root string) (map[string]MCPServer, error) {
	path := filepath.Join(root, "tools", "mcp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canonical MCP configuration %q: %w", path, err)
	}
	var document mcpDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse canonical MCP configuration %q: %w", path, err)
	}
	return validateServers(document.Servers)
}

func writeCanonicalMCP(root string, servers map[string]MCPServer, force bool) error {
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
		return validateServers(document.Servers)
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
	return validateServers(servers)
}

func writeVendorMCP(vendor, root string, servers map[string]MCPServer, force bool) error {
	path := vendorMCPPath(vendor, root)
	if vendor == "copilot" {
		data, err := json.MarshalIndent(mcpDocument{Servers: servers}, "", "  ")
		if err != nil {
			return fmt.Errorf("encode Copilot MCP configuration: %w", err)
		}
		return writeFile(path, append(data, '\n'), force)
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
	if vendor == "copilot" {
		return filepath.Join(root, ".github", "mcp.json")
	}
	return filepath.Join(root, ".codex", "config.toml")
}

func validateServers(servers map[string]MCPServer) (map[string]MCPServer, error) {
	if servers == nil {
		servers = make(map[string]MCPServer)
	}
	for name, server := range servers {
		if name == "" {
			return nil, errors.New("MCP server name cannot be empty")
		}
		if server.Command == "" && server.URL == "" {
			return nil, fmt.Errorf("MCP server %q must define command or url", name)
		}
		if server.Command != "" && server.URL != "" {
			return nil, fmt.Errorf("MCP server %q cannot define both command and url", name)
		}
		if server.Type == "" {
			if server.Command != "" {
				server.Type = "stdio"
			} else {
				server.Type = "http"
			}
			servers[name] = server
		}
	}
	return servers, nil
}

func writeFile(path string, data []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("refusing to overwrite %q without --force", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect output %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory for %q: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write output %q: %w", path, err)
	}
	return nil
}

func prepareOverwrite(options WriteOptions, paths ...string) error {
	if err := validateWriteOptions(options); err != nil {
		return err
	}
	if !options.Backup {
		return nil
	}

	for _, path := range paths {
		if err := backupDirectory(path); err != nil {
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

func backupDirectory(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup source %q: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("backup source %q is not a directory", path)
	}
	timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := path + ".backup-" + timestamp
	for index := 1; ; index++ {
		if _, err := os.Stat(backupPath); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return fmt.Errorf("inspect backup destination %q: %w", backupPath, err)
		}
		backupPath = fmt.Sprintf("%s.backup-%s-%d", path, timestamp, index)
	}
	if err := copyDirectory(path, backupPath); err != nil {
		return fmt.Errorf("back up directory %q: %w", path, err)
	}
	return nil
}

func copyDirectory(source, output string) error {
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
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular file %q", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
}

func copySkills(source, output string, force bool) error {
	info, err := os.Stat(source)
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
	if _, err := os.Stat(output); err == nil && !force {
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
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular skill file %q", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
}
