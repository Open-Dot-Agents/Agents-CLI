package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DevelopmentExtension keeps the preset explicit and fail-closed for consumers
// that understand the security draft but do not implement this extension.
const DevelopmentExtension = "org.open-dot-agents.development"

const developmentPath = "permissions/development.json"

// DevelopmentPolicy is the small authoring surface for the development preset.
// It is a requested policy, not proof of native enforcement.
type DevelopmentPolicy struct {
	Enforcement     string   `json:"enforcement,omitempty"`
	Version         string   `json:"version"`
	Preset          string   `json:"preset"`
	ProjectWork     string   `json:"project_work"`
	LocalCommits    string   `json:"local_commits"`
	ExternalChanges string   `json:"external_changes"`
	DestructiveWork string   `json:"destructive_work"`
	ProtectedPaths  []string `json:"protected_paths"`
}

func defaultDevelopmentPolicy() DevelopmentPolicy {
	return DevelopmentPolicy{
		Version: "1", Preset: "development", ProjectWork: "allow",
		LocalCommits: "allow", ExternalChanges: "ask", DestructiveWork: "ask",
		ProtectedPaths: []string{".env", "secrets"},
	}
}

func readDevelopmentPolicy(root string, permissions *PermissionsPolicy) (*DevelopmentPolicy, error) {
	extension, selected := permissions.Extensions[DevelopmentExtension]
	if !selected {
		return nil, nil
	}
	if extension.Required == nil || !*extension.Required {
		return nil, errors.New("development preset must be a required extension")
	}
	var path string
	if len(extension.Data) != 1 || json.Unmarshal(extension.Data["path"], &path) != nil || path != developmentPath {
		return nil, errors.New("development extension path must be permissions/development.json")
	}
	// The extension is the only authoring source. Low-level permission rules
	// would create a second, potentially contradictory source of approval policy.
	if permissions.Default != "ask" || len(permissions.Rules) != 0 {
		return nil, errors.New("development preset requires permissions default=ask and an empty rules array")
	}
	if len(permissions.Coverage) != 2 || !nativeHasProfile(permissions.Coverage, "shell") || !nativeHasProfile(permissions.Coverage, "builtin-tools") {
		return nil, errors.New("development preset requires shell and builtin-tools coverage")
	}
	var policy DevelopmentPolicy
	if err := decodePolicy(filepath.Join(root, developmentPath), &policy); err != nil {
		return nil, err
	}
	if policy.Version != "1" || policy.Preset != "development" {
		return nil, errors.New("unsupported development preset or version")
	}
	if policy.Enforcement != "" && policy.Enforcement != "strict" && policy.Enforcement != "practical" {
		return nil, errors.New("development enforcement must be strict or practical")
	}
	for _, effect := range []string{policy.ProjectWork, policy.LocalCommits, policy.ExternalChanges, policy.DestructiveWork} {
		if !decision(effect) {
			return nil, errors.New("development decisions must be allow, ask, or deny")
		}
	}
	if policy.ProtectedPaths == nil {
		return nil, errors.New("development preset requires protected_paths (use [] for no project-specific paths)")
	}
	seen := map[string]bool{}
	for _, path := range policy.ProtectedPaths {
		if path == "." || !policyPath.MatchString(path) || seen[path] {
			return nil, fmt.Errorf("invalid or duplicate protected path %q", path)
		}
		for _, component := range strings.Split(path, "/") {
			if component == "." || component == ".." {
				return nil, fmt.Errorf("invalid protected path %q", path)
			}
		}
		seen[path] = true
	}
	if policy.Enforcement == "practical" {
		var manifest manifestDocument
		if err := decodePolicy(filepath.Join(root, "manifest.json"), &manifest); err != nil {
			return nil, err
		}
		if manifest.Version != NativeVersion {
			return nil, errors.New("practical development requires a draft.2 manifest")
		}
		path := filepath.Join(root, developmentGuardrailsPath)
		if err := rejectSymlinkPath(path); err != nil {
			return nil, err
		}
		if err := requireRegularFile(path, "development guardrails"); err != nil {
			return nil, err
		}
	}
	return &policy, nil
}

// InitDevelopment creates a new experimental tree. It never replaces an
// existing canonical tree or native configuration, including with --force.
func InitDevelopment(root string) error {
	return initDevelopment(root, atomicWrite)
}

// AdoptDevelopment adds the preset to a valid existing tree, preserving all
// selected profiles and requirements. The original manifest is backed up.
func AdoptDevelopment(root string) error {
	return adoptDevelopment(root, atomicWrite)
}

// InitPracticalDevelopment explicitly separates guidance from native controls.
func InitPracticalDevelopment(root string) error {
	return initDevelopmentMode(root, atomicWrite, true)
}

func AdoptPracticalDevelopment(root string) error {
	return adoptDevelopmentMode(root, atomicWrite, true)
}

func adoptDevelopment(root string, write managedWriter) error {
	return adoptDevelopmentMode(root, write, false)
}

func adoptDevelopmentMode(root string, write managedWriter, practical bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	canonical := filepath.Join(root, ".agents")
	if err := rejectSymlinkPath(canonical); err != nil {
		return err
	}
	if err := ValidateRepositoryWithOptions(canonical, true); err != nil {
		return err
	}
	manifestPath := filepath.Join(canonical, "manifest.json")
	before, err := snapshotManagedFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest manifestDocument
	if err := decodePolicy(manifestPath, &manifest); err != nil {
		return err
	}
	if nativeHasProfile(manifest.Profiles, "permissions") || nativeHasProfile(manifest.Profiles, "sandbox") {
		return errors.New("development adoption cannot replace an existing permissions or sandbox profile; review policy migration explicitly")
	}
	// Refuse even unselected policy files, rather than overwriting user work.
	for _, name := range []string{"permissions/permissions.json", developmentPath} {
		if _, err := os.Lstat(filepath.Join(canonical, name)); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("development adoption requires an absent %s", name)
		}
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(before.data, &document); err != nil {
		return err
	}
	if manifest.Version == manifestVersion {
		manifest.Version = ExperimentalVersion
		delete(document, "$schema") // A stable schema cannot describe the draft.
	}
	if practical {
		manifest.Version = NativeVersion
		delete(document, "$schema")
	}
	manifest.Profiles = append(manifest.Profiles, "permissions")
	if !nativeHasProfile(manifest.Requires, "permissions") {
		manifest.Requires = append(manifest.Requires, "permissions")
	}
	for name, value := range map[string]any{"version": manifest.Version, "profiles": manifest.Profiles, "requires": manifest.Requires} {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		document[name] = data
	}
	manifestBytes, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	writes, err := developmentDocuments(canonical, manifest.Version)
	if err != nil {
		return err
	}
	if practical {
		if err := practicalDevelopmentDocuments(canonical, writes); err != nil {
			return err
		}
	}
	writes[manifestPath] = append(manifestBytes, '\n')
	preconditions := map[string]managedSnapshot{manifestPath: before}
	for path := range writes {
		if path != manifestPath {
			before, err := snapshotManagedFile(path)
			if err != nil {
				return err
			}
			if before.existed && !(practical && path == filepath.Join(canonical, "AGENTS.md")) {
				return fmt.Errorf("development policy appeared during migration: %s", path)
			}
			preconditions[path] = before
		}
	}
	return commitStableImport(writes, nil, preconditions, WriteOptions{Force: true, Backup: true}, write)
}

func developmentDocuments(canonical, version string) (map[string][]byte, error) {
	required := true
	permissions := PermissionsPolicy{
		Version: version, Coverage: []string{"shell", "builtin-tools"}, Default: "ask", Rules: []PermissionRule{},
		Extensions: map[string]Extension{DevelopmentExtension: {Required: &required, Data: map[string]json.RawMessage{"path": json.RawMessage(`"` + developmentPath + `"`)}}},
	}
	writes := map[string][]byte{}
	for name, value := range map[string]any{"permissions/permissions.json": permissions, developmentPath: defaultDevelopmentPolicy()} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return nil, err
		}
		writes[filepath.Join(canonical, name)] = append(data, '\n')
	}
	return writes, nil
}

func initDevelopment(root string, write managedWriter) error {
	return initDevelopmentMode(root, write, false)
}

func initDevelopmentMode(root string, write managedWriter, practical bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := rejectSymlinkPath(root); err != nil {
		return err
	}
	agentsRoot := filepath.Join(root, ".agents")
	if _, err := os.Lstat(agentsRoot); err == nil {
		return errors.New("development init requires a new .agents tree; existing configuration must be migrated explicitly")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	rootInstructions := filepath.Join(root, "AGENTS.md")
	instructions := "# Repository Agent Instructions\n\nUse the declared development policy. Native activation requires a successful plan and apply.\n"
	if _, err := os.Lstat(rootInstructions); err == nil {
		if err := requireRegularFile(rootInstructions, "existing instructions"); err != nil {
			return err
		}
		data, err := os.ReadFile(rootInstructions)
		if err != nil {
			return err
		}
		instructions = string(data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	writes, err := developmentDocuments(agentsRoot, ExperimentalVersion)
	if err != nil {
		return err
	}
	writes[filepath.Join(agentsRoot, "manifest.json")] = []byte("{\n  \"version\": \"" + ExperimentalVersion + "\",\n  \"profiles\": [\"permissions\"],\n  \"requires\": [\"permissions\"]\n}\n")
	writes[filepath.Join(agentsRoot, "AGENTS.md")] = []byte(instructions)
	if practical {
		writes[filepath.Join(agentsRoot, "manifest.json")] = []byte("{\"version\":\"" + NativeVersion + "\",\"profiles\":[\"permissions\"],\"requires\":[\"permissions\"]}\n")
		if err := practicalDevelopmentDocuments(agentsRoot, writes); err != nil {
			return err
		}
	}
	// A regular discovery file participates in the same rollback as the policy.
	// Existing repository instructions are preserved, never replaced.
	if _, err := os.Lstat(rootInstructions); errors.Is(err, fs.ErrNotExist) {
		writes[rootInstructions] = []byte("# Repository Agent Instructions\n\nRead and follow the canonical instructions in `.agents/AGENTS.md`.\n")
	}
	return commitStableImport(writes, nil, nil, WriteOptions{}, write)
}

const developmentGuardrailsPath = "guardrails/development.md"
const developmentReference = "\n\nIf .agents/manifest.json selects permissions and .agents/permissions/development.json selects practical enforcement, read .agents/guardrails/development.md and follow the development decisions before work.\n"

func practicalDevelopmentDocuments(root string, writes map[string][]byte) error {
	p := defaultDevelopmentPolicy()
	p.Enforcement = "practical"
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	writes[filepath.Join(root, developmentPath)] = append(data, '\n')
	var permissions PermissionsPolicy
	if err := json.Unmarshal(writes[filepath.Join(root, "permissions/permissions.json")], &permissions); err != nil {
		return err
	}
	permissions.Version = NativeVersion
	data, err = json.MarshalIndent(permissions, "", "  ")
	if err != nil {
		return err
	}
	writes[filepath.Join(root, "permissions/permissions.json")] = append(data, '\n')
	writes[filepath.Join(root, developmentGuardrailsPath)] = []byte(developmentGuardrails)
	path := filepath.Join(root, "AGENTS.md")
	data, exists := writes[path]
	if !exists {
		data, err = os.ReadFile(path)
		if err != nil {
			return err
		}
	}
	writes[path] = append(data, []byte(developmentReference)...)
	return nil
}

const developmentGuardrails = `# Development guardrails

Use the decisions in .agents/permissions/development.json. These decisions
are instructions to the agent. They are not a security boundary.

Allow means proceed within the user's task. Ask means obtain approval for
the specific action. Deny means do not perform the action. If more than one
decision applies, use deny before ask before allow. Ask for unclassified work.

Project work includes edits, tests, builds, and formatting. Local commits
include staging and new commits for reviewed task changes. External changes
include push, publish, deployment, messages, and changes to external systems.
Destructive work includes history rewrites and loss of unrelated work.

Check the effects of scripts, hooks, subprocesses, and tools before execution.
An allowed test or commit does not authorize a hidden deployment or push.
Keep secrets out of output. Do not read or change the protected paths.
Do not change host security, native trust, or active permissions to avoid an
approval. Existing approval for the same action remains valid in the session.
Native session and administrator restrictions always remain in effect.
`
