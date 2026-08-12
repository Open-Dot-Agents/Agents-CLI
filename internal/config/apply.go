package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const ownershipVersion = "1.0.0"

// ApplyOptions controls conflict adoption and backups for an adapter projection.
type ApplyOptions struct {
	Adopt  bool
	Force  bool
	Backup bool
}

// Action is one planned managed-file operation.
type Action struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Detail    string `json:"detail,omitempty"`
}

// PlanResult is the stable plan/apply output contract.
type PlanResult struct {
	SchemaVersion string   `json:"schema_version"`
	Vendor        string   `json:"vendor"`
	Root          string   `json:"root"`
	Applicable    bool     `json:"applicable"`
	Actions       []Action `json:"actions"`
	Diagnostics   []string `json:"diagnostics,omitempty"`
}

type ownershipState struct {
	Version string            `json:"version"`
	Vendor  string            `json:"vendor"`
	Entries map[string]string `json:"entries"`
	Files   map[string]string `json:"files,omitempty"`
}

type preparedProjection struct {
	result  PlanResult
	writes  map[string][]byte
	deletes []string
	state   ownershipState
}

// ImportRepository imports portable native configuration into root without
// duplicating root AGENTS.md.
func ImportRepository(vendor, root string, force, backup bool) error {
	vendor, err := normalizeStableVendor(vendor)
	if err != nil {
		return err
	}
	if backup && !force {
		return errors.New("--backup requires --force")
	}
	servers, err := readVendorMCP(vendor, root)
	if err != nil {
		return err
	}
	if vendor == "copilot" {
		for name, server := range servers {
			if len(server.Env) > 0 || len(server.Headers) > 0 {
				return fmt.Errorf("ODA-IMPORT-0001: Copilot MCP server %q contains literal environment or header values", name)
			}
		}
	}
	if vendor == "claude" {
		for name, server := range servers {
			converted, err := portableizeClaude(server)
			if err != nil {
				return fmt.Errorf("ODA-IMPORT-0002: Claude MCP server %q: %w", name, err)
			}
			servers[name] = converted
		}
	}
	agentsRoot := filepath.Join(root, ".agents")
	managed := []string{filepath.Join(agentsRoot, "manifest.json"), filepath.Join(agentsRoot, "tools", "mcp.json")}
	if !force {
		for _, path := range managed {
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("refusing to overwrite %q without --force", path)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	if backup {
		for _, path := range managed {
			if err := backupPath(path); err != nil {
				return err
			}
		}
	}
	profiles := []string{"mcp"}
	if info, err := os.Lstat(filepath.Join(root, "AGENTS.md")); err == nil && info.Mode().IsRegular() {
		profiles = append([]string{"instructions"}, profiles...)
	}
	if info, err := os.Lstat(vendorSkillsPath(vendor, root)); err == nil && info.IsDir() {
		profiles = append(profiles, "skills")
		if vendor == "claude" {
			if err := copySkills(vendorSkillsPath(vendor, root), filepath.Join(agentsRoot, "skills"), force); err != nil {
				return err
			}
		}
	}
	manifest, _ := json.MarshalIndent(map[string]any{"version": manifestVersion, "profiles": profiles}, "", "  ")
	if err := atomicWrite(filepath.Join(agentsRoot, "manifest.json"), append(manifest, '\n'), 0o644); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(mcpDocument{Servers: servers}, "", "  ")
	if err := atomicWrite(filepath.Join(agentsRoot, "tools", "mcp.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	return Validate(agentsRoot)
}

func portableizeClaude(server MCPServer) (MCPServer, error) {
	convert := func(values map[string]string) (map[string]string, error) {
		if len(values) == 0 {
			return nil, nil
		}
		result := map[string]string{}
		for key, value := range values {
			if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") || strings.Contains(value, ":-") {
				return nil, errors.New("literal or defaulted values are not portable secret references")
			}
			name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
			if !isEnvironmentName(name) {
				return nil, fmt.Errorf("invalid environment variable %q", name)
			}
			result[key] = "urn:open-dot-agents:env:" + name
		}
		return result, nil
	}
	var err error
	server.Env, err = convert(server.Env)
	if err != nil {
		return server, err
	}
	server.Headers, err = convert(server.Headers)
	return server, err
}

// PlanProjection computes an adapter projection without changing the repository.
func PlanProjection(vendor, root string, options ApplyOptions) (PlanResult, error) {
	prepared, err := prepareProjection(vendor, root, options)
	if err != nil {
		return PlanResult{}, err
	}
	return prepared.result, nil
}

// ApplyProjection applies a previously describable projection using atomic writes.
func ApplyProjection(vendor, root string, options ApplyOptions) (PlanResult, error) {
	if options.Backup && !options.Force {
		return PlanResult{}, errors.New("--backup requires --force")
	}
	prepared, err := prepareProjection(vendor, root, options)
	if err != nil {
		return PlanResult{}, err
	}
	if !prepared.result.Applicable {
		return prepared.result, errors.New(strings.Join(prepared.result.Diagnostics, "; "))
	}
	paths := make([]string, 0, len(prepared.writes))
	for path := range prepared.writes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if options.Backup {
		for _, path := range paths {
			if err := backupPath(path); err != nil {
				return PlanResult{}, err
			}
		}
	}
	for _, path := range paths {
		if err := atomicWrite(path, prepared.writes[path], 0o644); err != nil {
			return PlanResult{}, err
		}
	}
	for _, path := range prepared.deletes {
		if options.Backup {
			if err := backupPath(path); err != nil {
				return PlanResult{}, err
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return PlanResult{}, fmt.Errorf("delete managed output %q: %w", path, err)
		}
	}
	stateData, err := json.MarshalIndent(prepared.state, "", "  ")
	if err != nil {
		return PlanResult{}, fmt.Errorf("encode ownership state: %w", err)
	}
	statePath := ownershipPath(root, vendor)
	if err := atomicWrite(statePath, append(stateData, '\n'), 0o644); err != nil {
		return PlanResult{}, err
	}
	return prepared.result, nil
}

func prepareProjection(vendor, root string, options ApplyOptions) (preparedProjection, error) {
	vendor, err := normalizeStableVendor(vendor)
	if err != nil {
		return preparedProjection{}, err
	}
	agentsRoot := filepath.Join(root, ".agents")
	if err := Validate(agentsRoot); err != nil {
		return preparedProjection{}, err
	}
	profiles, _, err := validateManifest(filepath.Join(agentsRoot, "manifest.json"))
	if err != nil {
		return preparedProjection{}, err
	}
	selected := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		selected[profile] = true
	}
	state, err := loadOwnership(root, vendor)
	if err != nil {
		return preparedProjection{}, err
	}
	result := PlanResult{SchemaVersion: ownershipVersion, Vendor: vendor, Root: root, Applicable: true, Actions: []Action{}}
	writes := map[string][]byte{}
	next := ownershipState{Version: ownershipVersion, Vendor: vendor, Entries: map[string]string{}, Files: map[string]string{}}
	deletes := []string{}

	if selected["mcp"] {
		servers, err := readCanonicalMCP(agentsRoot)
		if err != nil {
			return preparedProjection{}, err
		}
		for name, server := range servers {
			if vendor == "copilot" && (len(server.Env) > 0 || len(server.Headers) > 0) {
				result.Applicable = false
				result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("ODA-ADAPTER-0001: Copilot cannot safely represent environment reference for MCP server %q", name))
			}
			if vendor == "codex" {
				for target, reference := range server.Env {
					if target != environmentReferenceName(reference) {
						result.Applicable = false
						result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("ODA-ADAPTER-0002: Codex cannot rename environment variable %q for MCP server %q", target, name))
					}
				}
			}
		}
		if result.Applicable {
			path := vendorMCPPath(vendor, root)
			var data []byte
			var entryHashes map[string]string
			if vendor == "codex" {
				data, entryHashes, err = mergeCodex(path, servers, state, options)
			} else {
				data, entryHashes, err = mergeJSONVendor(vendor, path, servers, state, options)
			}
			if err != nil {
				result.Applicable = false
				result.Diagnostics = append(result.Diagnostics, err.Error())
			} else {
				next.Entries = entryHashes
				addWrite(&result, writes, root, path, data)
			}
		}
	}

	if selected["instructions"] && vendor == "claude" {
		bridgeWrites, hashes, err := prepareClaudeBridges(root, state, options)
		if err != nil {
			result.Applicable = false
			result.Diagnostics = append(result.Diagnostics, err.Error())
		} else {
			for path, data := range bridgeWrites {
				addWrite(&result, writes, root, path, data)
			}
			for path, hash := range hashes {
				next.Files[path] = hash
			}
		}
	}
	if selected["skills"] && vendor == "claude" {
		skillWrites, hashes, err := prepareSkillWrites(root, state, options)
		if err != nil {
			result.Applicable = false
			result.Diagnostics = append(result.Diagnostics, err.Error())
		} else {
			for path, data := range skillWrites {
				addWrite(&result, writes, root, path, data)
			}
			for path, hash := range hashes {
				next.Files[path] = hash
			}
		}
	}
	for relative, oldHash := range state.Files {
		if _, remains := next.Files[relative]; remains {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			result.Applicable = false
			result.Diagnostics = append(result.Diagnostics, err.Error())
			continue
		}
		if digest(data) != oldHash && !options.Force {
			result.Applicable = false
			result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("ODA-MERGE-0002: managed file %q was modified", relative))
			continue
		}
		result.Actions = append(result.Actions, Action{Operation: "delete", Path: relative})
		deletes = append(deletes, path)
	}
	return preparedProjection{result: result, writes: writes, deletes: deletes, state: next}, nil
}

func normalizeStableVendor(vendor string) (string, error) {
	switch strings.ToLower(vendor) {
	case "copilot", "codex", "claude":
		return strings.ToLower(vendor), nil
	default:
		return "", fmt.Errorf("unsupported stable vendor %q (supported: copilot, codex, claude)", vendor)
	}
}

func mergeJSONVendor(vendor, path string, desired map[string]MCPServer, state ownershipState, options ApplyOptions) ([]byte, map[string]string, error) {
	root := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, nil, fmt.Errorf("ODA-MERGE-0001: parse existing %s: %w", path, err)
		}
		if raw := root["mcpServers"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &servers); err != nil {
				return nil, nil, fmt.Errorf("ODA-MERGE-0001: parse existing mcpServers: %w", err)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	next := map[string]string{}
	for name, server := range desired {
		native := nativeJSONServer(vendor, server)
		raw, err := json.Marshal(native)
		if err != nil {
			return nil, nil, err
		}
		if existing, ok := servers[name]; ok {
			if err := resolveEntryConflict(name, existing, raw, state.Entries[name], options); err != nil {
				return nil, nil, err
			}
		}
		servers[name] = raw
		next[name] = semanticDigest(raw)
	}
	for name, oldHash := range state.Entries {
		if _, wanted := desired[name]; wanted {
			continue
		}
		if existing, ok := servers[name]; ok {
			if semanticDigest(existing) != oldHash && !options.Force {
				return nil, nil, fmt.Errorf("ODA-MERGE-0002: managed MCP server %q was modified", name)
			}
			delete(servers, name)
		}
	}
	serverData, _ := json.Marshal(servers)
	root["mcpServers"] = serverData
	data, err := json.MarshalIndent(root, "", "  ")
	return append(data, '\n'), next, err
}

func nativeJSONServer(vendor string, server MCPServer) map[string]any {
	native := map[string]any{}
	if server.Type == "stdio" {
		native["type"] = "stdio"
		native["command"] = server.Command
		if len(server.Args) > 0 {
			native["args"] = server.Args
		}
		if vendor == "claude" && len(server.Env) > 0 {
			env := map[string]string{}
			for key, reference := range server.Env {
				env[key] = "${" + environmentReferenceName(reference) + "}"
			}
			native["env"] = env
		}
	} else {
		native["type"] = "http"
		native["url"] = server.URL
		if vendor == "claude" && len(server.Headers) > 0 {
			headers := map[string]string{}
			for key, reference := range server.Headers {
				headers[key] = "${" + environmentReferenceName(reference) + "}"
			}
			native["headers"] = headers
		}
	}
	return native
}

func mergeCodex(path string, desired map[string]MCPServer, state ownershipState, options ApplyOptions) ([]byte, map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data = nil
	} else if err != nil {
		return nil, nil, err
	}
	blocks, prefix, suffixes, err := splitCodexBlocks(data)
	if err != nil {
		return nil, nil, err
	}
	next := map[string]string{}
	for name, server := range desired {
		block, err := renderCodexBlock(name, server)
		if err != nil {
			return nil, nil, err
		}
		if existing, ok := blocks[name]; ok {
			if err := resolveEntryConflict(name, existing, block, state.Entries[name], options); err != nil {
				return nil, nil, err
			}
		}
		blocks[name] = block
		next[name] = semanticDigest(block)
	}
	for name, oldHash := range state.Entries {
		if _, wanted := desired[name]; wanted {
			continue
		}
		if existing, ok := blocks[name]; ok {
			if semanticDigest(existing) != oldHash && !options.Force {
				return nil, nil, fmt.Errorf("ODA-MERGE-0002: managed MCP server %q was modified", name)
			}
			delete(blocks, name)
		}
	}
	var output bytes.Buffer
	output.Write(prefix)
	if len(blocks) > 0 && !bytes.Contains(prefix, []byte("[mcp_servers]")) {
		if output.Len() > 0 && !bytes.HasSuffix(output.Bytes(), []byte("\n")) {
			output.WriteByte('\n')
		}
		output.WriteString("[mcp_servers]\n")
	}
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if output.Len() > 0 && !bytes.HasSuffix(output.Bytes(), []byte("\n\n")) {
			output.WriteByte('\n')
		}
		output.Write(blocks[name])
	}
	for _, suffix := range suffixes {
		if output.Len() > 0 && !bytes.HasSuffix(output.Bytes(), []byte("\n")) {
			output.WriteByte('\n')
		}
		output.Write(suffix)
	}
	return output.Bytes(), next, nil
}

func renderCodexBlock(name string, server MCPServer) ([]byte, error) {
	native := codexServer{Command: server.Command, Args: server.Args, URL: server.URL}
	for _, reference := range server.Env {
		native.EnvVars = append(native.EnvVars, environmentReferenceName(reference))
	}
	sort.Strings(native.EnvVars)
	if len(server.Headers) > 0 {
		native.EnvHTTPHeaders = map[string]string{}
		for header, reference := range server.Headers {
			native.EnvHTTPHeaders[header] = environmentReferenceName(reference)
		}
	}
	data, err := toml.Marshal(codexDocument{Servers: map[string]codexServer{name: native}})
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("[mcp_servers]\n"))
	return data, nil
}

func splitCodexBlocks(data []byte) (map[string][]byte, []byte, [][]byte, error) {
	var parsed map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := toml.Unmarshal(data, &parsed); err != nil {
			return nil, nil, nil, fmt.Errorf("ODA-MERGE-0001: parse existing Codex TOML: %w", err)
		}
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	blocks := map[string][]byte{}
	var prefix bytes.Buffer
	var suffixes [][]byte
	var current string
	var currentData bytes.Buffer
	flush := func() {
		if current != "" {
			blocks[current] = append([]byte(nil), currentData.Bytes()...)
		} else if currentData.Len() > 0 {
			if len(blocks) == 0 {
				prefix.Write(currentData.Bytes())
			} else {
				suffixes = append(suffixes, append([]byte(nil), currentData.Bytes()...))
			}
		}
		currentData.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := codexTableServer(trimmed)
			if name != "" && name != current {
				flush()
				current = name
			} else if name == "" {
				flush()
				current = ""
			}
		}
		currentData.Write(line)
	}
	flush()
	return blocks, prefix.Bytes(), suffixes, nil
}

func codexTableServer(header string) string {
	header = strings.TrimSuffix(strings.TrimPrefix(header, "["), "]")
	if !strings.HasPrefix(header, "mcp_servers.") {
		return ""
	}
	rest := strings.TrimPrefix(header, "mcp_servers.")
	return strings.Split(rest, ".")[0]
}

func resolveEntryConflict(name string, existing, desired []byte, oldHash string, options ApplyOptions) error {
	if semanticDigest(existing) == semanticDigest(desired) {
		if oldHash == "" && !options.Adopt && !options.Force {
			return fmt.Errorf("ODA-MERGE-0003: MCP server %q already exists; use --adopt for equivalent content", name)
		}
		return nil
	}
	if oldHash == "" {
		if !options.Force {
			return fmt.Errorf("ODA-MERGE-0004: unowned MCP server %q conflicts with the canonical catalogue", name)
		}
		return nil
	}
	if semanticDigest(existing) != oldHash && !options.Force {
		return fmt.Errorf("ODA-MERGE-0002: managed MCP server %q was modified", name)
	}
	return nil
}

func prepareSkillWrites(root string, state ownershipState, options ApplyOptions) (map[string][]byte, map[string]string, error) {
	source := filepath.Join(root, ".agents", "skills")
	target := filepath.Join(root, ".claude", "skills")
	writes := map[string][]byte{}
	hashes := map[string]string{}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular skill file %q", path)
		}
		relative, _ := filepath.Rel(source, path)
		destination := filepath.Join(target, relative)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		stateKey := filepath.ToSlash(filepath.Join(".claude", "skills", relative))
		if err := checkOwnedFile(destination, state.Files[stateKey], data, options); err != nil {
			return err
		}
		writes[destination] = data
		hashes[stateKey] = digest(data)
		return nil
	})
	return writes, hashes, err
}

func prepareClaudeBridges(root string, state ownershipState, options ApplyOptions) (map[string][]byte, map[string]string, error) {
	writes := map[string][]byte{}
	hashes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".agents" || entry.Name() == ".claude") {
			return filepath.SkipDir
		}
		if entry.IsDir() && path != root {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if entry.Name() != "AGENTS.md" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular instruction file %q", path)
		}
		destination := filepath.Join(filepath.Dir(path), "CLAUDE.md")
		relative, _ := filepath.Rel(root, destination)
		stateKey := filepath.ToSlash(relative)
		data := []byte("@AGENTS.md\n")
		if err := checkOwnedFile(destination, state.Files[stateKey], data, options); err != nil {
			return err
		}
		writes[destination] = data
		hashes[stateKey] = digest(data)
		return nil
	})
	return writes, hashes, err
}

func checkOwnedFile(path, oldHash string, desired []byte, options ApplyOptions) error {
	existing, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if digest(existing) == digest(desired) {
		if oldHash == "" && !options.Adopt && !options.Force {
			return fmt.Errorf("ODA-MERGE-0005: file %q already exists; use --adopt for equivalent content", path)
		}
		return nil
	}
	if oldHash == "" || digest(existing) != oldHash {
		if !options.Force {
			return fmt.Errorf("ODA-MERGE-0006: unowned or modified file %q conflicts with the projection", path)
		}
	}
	return nil
}

func addWrite(result *PlanResult, writes map[string][]byte, root, path string, data []byte) {
	operation := "create"
	detail := ""
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			operation = "unchanged"
		} else {
			operation = "update"
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		detail = err.Error()
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = path
	}
	result.Actions = append(result.Actions, Action{Operation: operation, Path: filepath.ToSlash(relative), Detail: detail})
	writes[path] = data
}

func ownershipPath(root, vendor string) string {
	return filepath.Join(root, ".agents", ".state", "reference-cli", vendor+".json")
}

func loadOwnership(root, vendor string) (ownershipState, error) {
	state := ownershipState{Version: ownershipVersion, Vendor: vendor, Entries: map[string]string{}, Files: map[string]string{}}
	data, err := os.ReadFile(ownershipPath(root, vendor))
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("parse ownership state: %w", err)
	}
	if state.Version != ownershipVersion || state.Vendor != vendor {
		return state, fmt.Errorf("unsupported ownership state for %s", vendor)
	}
	if state.Entries == nil {
		state.Entries = map[string]string{}
	}
	if state.Files == nil {
		state.Files = map[string]string{}
	}
	return state, nil
}

func atomicWrite(path string, data []byte, permissions fs.FileMode) error {
	if err := safeMkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".agents-write-*")
	if err != nil {
		return fmt.Errorf("create temporary output for %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(permissions); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace output %q: %w", path, err)
	}
	return nil
}

func environmentReferenceName(reference string) string {
	return strings.TrimPrefix(reference, "urn:open-dot-agents:env:")
}

func digest(data []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(data))
	return hex.EncodeToString(sum[:])
}

func semanticDigest(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) == nil {
		if normalized, err := json.Marshal(value); err == nil {
			return digest(normalized)
		}
	}
	if toml.Unmarshal(data, &value) == nil {
		if normalized, err := json.Marshal(value); err == nil {
			return digest(normalized)
		}
	}
	return digest(data)
}
