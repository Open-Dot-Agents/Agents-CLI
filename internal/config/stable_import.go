package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Decoding into a struct ignores unknown fields. Inspect the native MCP object
// first so activation, filtering, and authentication controls cannot disappear.
func validateStableMCPFields(vendor string, data []byte) error {
	var document map[string]any
	key := "mcpServers"
	fields := []string{"type", "command", "args", "env", "url", "headers", "startup_timeout_sec", "tool_timeout_sec", "default_tools_approval_mode"}
	if vendor == "codex" {
		key = "mcp_servers"
		fields = []string{"command", "args", "env", "env_vars", "url", "http_headers", "env_http_headers", "startup_timeout_sec", "tool_timeout_sec", "default_tools_approval_mode"}
		if err := toml.Unmarshal(data, &document); err != nil {
			return err
		}
	} else {
		if err := nativeUniqueJSON(data); err != nil {
			return err
		}
		if err := json.Unmarshal(data, &document); err != nil {
			return err
		}
	}
	if document == nil {
		return fmt.Errorf("native MCP configuration must be an object")
	}
	value, exists := document[key]
	if !exists {
		return nil
	}
	servers, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("native MCP configuration must be an object")
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	for _, name := range nativeSortedKeys(servers) {
		server, ok := servers[name].(map[string]any)
		if !ok {
			return fmt.Errorf("native MCP server %q must be an object", name)
		}
		for _, field := range nativeSortedKeys(server) {
			if !allowed[field] {
				return fmt.Errorf("ODA-IMPORT-0004: %s MCP server %q field %q has no stable mapping; use native import for supported native fields", vendor, name, field)
			}
		}
	}
	return nil
}

// Validate the prospective selected tree in private staging. Unselected native
// packages, account files, and runtime state are neither copied nor inspected.
func validateStableImportTree(root string, writes map[string][]byte) error {
	var manifest manifestDocument
	if err := json.Unmarshal(writes[filepath.Join(root, "manifest.json")], &manifest); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "agents-import-validation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	stage := filepath.Join(temporary, ".agents")
	for _, profile := range manifest.Profiles {
		if profile == "skills" {
			source := filepath.Join(root, "skills")
			if _, err := os.Lstat(source); err == nil {
				if err := copyDirectory(source, filepath.Join(stage, "skills")); err != nil {
					return err
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		file := ""
		switch profile {
		case "tools":
			file = filepath.Join("tools", "mcp.json")
		case "hooks":
			file = filepath.Join("hooks", "hooks.json")
		}
		if file == "" {
			continue // Manifest validation reports unsupported stable profiles.
		}
		if _, replaced := writes[filepath.Join(root, file)]; replaced {
			continue
		}
		retained, err := snapshotManagedFile(filepath.Join(root, file))
		if err != nil {
			return err
		}
		if !retained.existed {
			return fmt.Errorf("retained profile %s configuration is missing", profile)
		}
		if err := atomicWrite(filepath.Join(stage, file), retained.data, 0600); err != nil {
			return err
		}
	}
	for path, data := range writes {
		relative, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("import output is outside the canonical root")
		}
		if err := atomicWrite(filepath.Join(stage, relative), data, 0600); err != nil {
			return err
		}
	}
	return Validate(stage)
}

// Import has no projection ownership record. All canonical files and private
// backups participate in the same rollback, using the stable managed writer.
func commitStableImport(writes map[string][]byte, modes map[string]fs.FileMode, preconditions map[string]managedSnapshot, options WriteOptions, write managedWriter) error {
	paths := make([]string, 0, len(writes))
	for path := range writes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	snapshots := map[string]managedSnapshot{}
	createdDirectories := map[string]struct{}{}
	for _, path := range paths {
		snapshot, err := snapshotManagedFile(path)
		if err != nil {
			return err
		}
		if expected, ok := preconditions[path]; ok && !sameManagedImportInput(expected, snapshot) {
			return fmt.Errorf("import output changed during validation: %s", path)
		}
		if snapshot.existed && !options.Force {
			return fmt.Errorf("refusing to overwrite %q without --force", path)
		}
		snapshots[path] = snapshot
	}
	if modes == nil {
		modes = map[string]fs.FileMode{}
	}
	if options.Backup {
		timestamp := time.Now().UTC().Format("20060102T150405.000000000Z")
		for _, path := range append([]string(nil), paths...) {
			before := snapshots[path]
			if !before.existed {
				continue
			}
			backup := path + ".backup-" + timestamp
			for index := 1; ; index++ {
				snapshot, err := snapshotManagedFile(backup)
				if err != nil {
					return err
				}
				_, reserved := writes[backup]
				if !snapshot.existed && !reserved {
					snapshots[backup] = snapshot
					break
				}
				backup = fmt.Sprintf("%s.backup-%s-%d", path, timestamp, index)
			}
			writes[backup], modes[backup] = before.data, 0600
			paths = append(paths, backup)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		for _, directory := range missingParentDirectories(path) {
			createdDirectories[directory] = struct{}{}
		}
	}
	var attempted []managedSnapshot
	for _, path := range paths {
		before := snapshots[path]
		current, err := snapshotManagedFile(path)
		if err == nil && !sameManagedImportInput(current, before) {
			err = fmt.Errorf("import output changed before write: %s", path)
		}
		if err != nil {
			return rollbackManagedFiles(attempted, createdDirectories, write, err)
		}
		mode := modes[path]
		if before.existed {
			mode = before.permissions
		} else if mode == 0 {
			mode = 0644
		}
		attempted = append(attempted, before)
		if err := write(path, writes[path], mode); err != nil {
			return rollbackManagedFiles(attempted, createdDirectories, write, fmt.Errorf("write imported output %q: %w", path, err))
		}
	}
	return nil
}

func sameManagedImportInput(a, b managedSnapshot) bool {
	return a.existed == b.existed && a.permissions == b.permissions && bytes.Equal(a.data, b.data)
}
