package config

// The first security mapping targets the direct Codex Linux sandbox command.
// It does not claim coverage for model tools, hooks, MCP, LSP, or delegation.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const codexSecurityVersion = "0.154.0"
const codexSecurityBinarySHA = "3188814c35471432d4123203e0eb38e5bddc60226e3d7ddf0e59e649ea140022"
const codexSecurityProfile = "open-dot-agents"
const securitySelectorsBegin = "# Open-Dot-Agents security selectors begin\n"
const securitySelectorsEnd = "# Open-Dot-Agents security selectors end\n"
const securityProfileBegin = "\n# Open-Dot-Agents security profile begin\n"
const securityProfileEnd = "# Open-Dot-Agents security profile end\n"

func securityError(message string) error { return errors.New("ODA-SECURITY-0006: " + message) }

func codexPolicySettings(policy SecurityPolicy, selected map[string]bool) (map[string]any, error) {
	p := policy.Sandbox
	if p == nil {
		return nil, securityError("the Codex mapping requires a sandbox profile")
	}
	if len(p.Coverage) != 1 || p.Coverage[0] != "shell" {
		return nil, securityError("the Codex mapping covers direct sandbox shell processes only")
	}
	if len(p.Filesystem.Runtime) != 1 || p.Filesystem.Runtime[0] != "process" {
		return nil, securityError("declare filesystem.runtime=[process] for native process resources")
	}
	if p.Filesystem.Default != "read" {
		return nil, securityError("the Codex mapping requires filesystem.default=read; default-deny and unrestricted host writes are not mapped")
	}
	if p.Credentials.Environment != "inherit" || p.Credentials.Files != "inherit" || len(p.Credentials.Allow) != 0 {
		return nil, securityError("credential isolation is not verified; the subset requires explicit inherit for environment and files")
	}
	if p.Network.Default != "deny" || p.Network.Local != "deny" || p.Network.Private != "deny" || len(p.Network.Rules) != 0 || p.Network.Web != "allow" || p.Network.RemoteMCP != "allow" {
		return nil, securityError("the subset requires subprocess network denial without host rules; web and remoteMCP must be unrestricted and remain outside coverage")
	}
	for _, profile := range []string{"tools", "hooks", "skills"} {
		if selected[profile] {
			return nil, securityError("the first Codex security mapping does not combine with selected " + profile)
		}
	}
	if permissions := policy.Permissions; permissions != nil {
		if len(permissions.Coverage) != 1 || permissions.Coverage[0] != "shell" || permissions.Default != "allow" || len(permissions.Rules) != 0 {
			return nil, securityError("only shell permissions with default=allow and no rules are mapped; ask, deny, and exact-command rules still refuse")
		}
	}
	for _, extensions := range []map[string]Extension{p.Extensions, func() map[string]Extension {
		if policy.Permissions != nil {
			return policy.Permissions.Extensions
		}
		return nil
	}()} {
		for _, extension := range extensions {
			if *extension.Required {
				return nil, securityError("unknown required extensions cannot activate")
			}
		}
	}
	paths := map[string]any{}
	rootAccess, _ := filesystemAccess(*p, ".")
	paths["."] = rootAccess
	for _, rule := range p.Filesystem.Rules {
		value, err := filesystemAccess(*p, rule.Path)
		if err != nil {
			return nil, err
		}
		paths[rule.Path] = value
	}
	// Native protected paths are read-only even inside a writable workspace.
	for _, path := range []string{".agents", ".codex", ".git"} {
		value, _ := filesystemAccess(*p, path)
		if value != "read" {
			return nil, securityError("declare read access for native protected path " + path)
		}
		paths[path] = "read"
	}
	return map[string]any{
		"approval_policy": "never", "default_permissions": codexSecurityProfile,
		"permissions": map[string]any{codexSecurityProfile: map[string]any{
			"filesystem": map[string]any{":root": "read", ":workspace_roots": paths},
			"network":    map[string]any{"enabled": false},
		}},
	}, nil
}

// Native keys are managed in two marked segments. Unrelated bytes stay intact.
// A semantic comparison detects markers inside strings or unrelated fields in
// the marked segments. Force never permits removal of unrelated configuration.
func stripCodexSecurity(data []byte) ([]byte, []byte, error) {
	var original map[string]any
	if len(bytes.TrimSpace(data)) > 0 {
		if err := toml.Unmarshal(data, &original); err != nil {
			return nil, nil, securityError("cannot parse project Codex configuration")
		}
	}
	clean := append([]byte(nil), data...)
	var owned []byte
	for _, markers := range [][2]string{{securitySelectorsBegin, securitySelectorsEnd}, {securityProfileBegin, securityProfileEnd}} {
		count := bytes.Count(clean, []byte(markers[0]))
		endCount := bytes.Count(clean, []byte(markers[1]))
		if count == 0 && endCount == 0 {
			continue
		}
		if count != 1 || endCount != 1 {
			return nil, nil, securityError("ambiguous native security markers")
		}
		start := bytes.Index(clean, []byte(markers[0]))
		end := bytes.Index(clean, []byte(markers[1])) + len(markers[1])
		if end < start || (start > 0 && markers[0][0] != '\n' && clean[start-1] != '\n') {
			return nil, nil, securityError("invalid native security markers")
		}
		owned = append(owned, clean[start:end]...)
		clean = append(clean[:start:start], clean[end:]...)
	}
	if len(owned) == 0 {
		return clean, nil, nil
	}
	if !bytes.Contains(owned, []byte(securitySelectorsBegin)) || !bytes.Contains(owned, []byte(securityProfileBegin)) {
		return nil, nil, securityError("incomplete native security segments")
	}
	var remaining map[string]any
	if err := toml.Unmarshal(clean, &remaining); err != nil {
		return nil, nil, securityError("native security segments do not preserve TOML structure")
	}
	delete(original, "approval_policy")
	delete(original, "default_permissions")
	if profiles, ok := original["permissions"].(map[string]any); ok {
		delete(profiles, codexSecurityProfile)
		if len(profiles) == 0 {
			delete(original, "permissions")
		}
	}
	if len(original) == 0 && len(remaining) == 0 {
		return clean, owned, nil
	}
	if !reflect.DeepEqual(original, remaining) {
		return nil, nil, securityError("native security markers contain unrelated settings or occur inside a value")
	}
	return clean, owned, nil
}

func mergeCodexSecurity(data []byte, settings map[string]any, oldHash string, options ApplyOptions) ([]byte, string, error) {
	clean, owned, err := stripCodexSecurity(data)
	if err != nil {
		return nil, "", err
	}
	var base map[string]any
	if err := toml.Unmarshal(clean, &base); err != nil {
		return nil, "", securityError("cannot parse native security base")
	}
	if _, ok := base["approval_policy"]; ok {
		return nil, "", securityError("unmarked approval_policy conflicts with managed security; move or remove it explicitly")
	}
	if _, ok := base["default_permissions"]; ok {
		return nil, "", securityError("unmarked default_permissions conflicts with managed security; move or remove it explicitly")
	}
	if profiles, ok := base["permissions"].(map[string]any); ok {
		if _, exists := profiles[codexSecurityProfile]; exists {
			return nil, "", securityError("unmarked open-dot-agents profile already exists")
		}
	}
	if len(owned) > 0 && oldHash == "" && !options.Adopt {
		return nil, "", securityError("native security segments are unowned; equivalent content requires --adopt")
	}
	if oldHash != "" && digest(owned) != oldHash && !options.Force {
		return nil, "", securityError("owned native security settings changed; use an explicit forced backup after review")
	}
	if settings == nil {
		if len(owned) > 0 && oldHash == "" {
			return nil, "", securityError("cannot remove unowned native security segments")
		}
		return clean, "", nil
	}
	selectors, err := toml.Marshal(map[string]any{"approval_policy": settings["approval_policy"], "default_permissions": settings["default_permissions"]})
	if err != nil {
		return nil, "", err
	}
	profile, err := toml.Marshal(map[string]any{"permissions": settings["permissions"]})
	if err != nil {
		return nil, "", err
	}
	// Do not own a generic [permissions] header: other named profiles can coexist.
	profile = bytes.TrimPrefix(profile, []byte("[permissions]\n"))
	header := []byte(securitySelectorsBegin + string(selectors) + securitySelectorsEnd)
	footer := []byte(securityProfileBegin + string(profile) + securityProfileEnd)
	desiredOwned := append(append([]byte(nil), header...), footer...)
	if len(owned) > 0 && oldHash == "" && !bytes.Equal(owned, desiredOwned) {
		return nil, "", securityError("--adopt requires equivalent native security content")
	}
	result := append(append(append([]byte(nil), header...), clean...), footer...)
	if _, _, err := stripCodexSecurity(result); err != nil {
		return nil, "", err
	}
	return result, digest(desiredOwned), nil
}

func readNativeConfig(path string) (map[string]any, string, error) {
	if err := rejectSymlinkPath(path); err != nil {
		return nil, "", securityError("native configuration path contains a symlink")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", securityError("cannot read native configuration " + path)
	}
	var value map[string]any
	if err := toml.Unmarshal(data, &value); err != nil {
		return nil, "", securityError("cannot parse native configuration " + path)
	}
	return value, digest(data), nil
}

func codexAuthority(root, home string) (map[string]string, error) {
	if !filepath.IsAbs(home) {
		return nil, securityError("--codex-home must be an absolute native configuration directory")
	}
	home = filepath.Clean(home)
	if home == root || strings.HasPrefix(home, root+string(filepath.Separator)) {
		return nil, securityError("native configuration home must be outside the workspace")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, securityError("cannot inspect the selected native home")
	}
	for _, entry := range entries {
		if entry.Name() != "config.toml" && entry.Name() != "tmp" {
			return nil, securityError("use a configuration-only native home; account and cached policy state are outside this subset")
		}
	}
	hashes := map[string]string{}
	for _, path := range []string{"/etc/codex/config.toml", "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml", filepath.Join(home, "requirements.toml"), filepath.Join(home, "managed_config.toml")} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			return nil, securityError("managed or system configuration is outside this subset: " + path)
		}
		hashes[path] = "absent"
	}
	configPath := filepath.Join(home, "config.toml")
	user, hash, err := readNativeConfig(configPath)
	if err != nil {
		return nil, err
	}
	hashes[configPath] = hash
	// Restrict the first invocation contract to a small, known user config surface.
	for key := range user {
		if key != "projects" && key != "model" && key != "model_reasoning_effort" && key != "model_verbosity" && key != "personality" {
			return nil, securityError("native user setting requires an authority mapping: " + key)
		}
	}
	projects, _ := user["projects"].(map[string]any)
	project, _ := projects[root].(map[string]any)
	if project["trust_level"] != "trusted" {
		return nil, securityError("the exact workspace must already be trusted in the selected native home; the adapter does not grant trust")
	}
	for parent := filepath.Dir(root); ; parent = filepath.Dir(parent) {
		path := filepath.Join(parent, ".codex", "config.toml")
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			return nil, securityError("ancestor native configuration is outside this subset: " + path)
		}
		hashes[path] = "absent"
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return hashes, nil
}

func codexNativeProbe(root, home string) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() == 0 {
		return "", securityError("the verified subset requires unprivileged Linux amd64")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		return "", securityError("Codex is not installed")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return "", err
	}
	if binary == root || strings.HasPrefix(binary, root+string(filepath.Separator)) {
		return "", securityError("native binary must be outside the writable workspace")
	}
	handle, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, handle)
	handle.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if hex.EncodeToString(hasher.Sum(nil)) != codexSecurityBinarySHA {
		return "", securityError("Codex binary is outside the exact 0.154.0 Linux pin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "sandbox", "--permission-profile", ":read-only", "--include-managed-config", "--cd", root, "--", "/usr/bin/true")
	// Only the selected home and a fixed system PATH enter this startup probe.
	command.Env = []string{"PATH=/usr/bin:/bin", "CODEX_HOME=" + home}
	if err := command.Run(); err != nil {
		return "", securityError("native read-only sandbox startup failed; check Linux sandbox prerequisites")
	}
	return binary, nil
}

func prepareCodexSecurity(root string, options ApplyOptions, selected map[string]bool, state ownershipState, plan *SecurityPlan) ([]byte, string, error) {
	if options.CodexHome == "" {
		return nil, "", securityError("the Codex subset requires an explicit --codex-home")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	root = filepath.Clean(root)
	if err := rejectSymlinkPath(root); err != nil {
		return nil, "", err
	}
	if options.Force && !options.Backup {
		return nil, "", securityError("forced native security changes require --backup")
	}
	path := filepath.Join(root, ".codex", "config.toml")
	if err := rejectSymlinkPath(path); err != nil {
		return nil, "", err
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	base, _, err := stripCodexSecurity(current)
	if err != nil {
		return nil, "", err
	}
	var existing map[string]any
	if err := toml.Unmarshal(base, &existing); err != nil {
		return nil, "", securityError("cannot parse project configuration")
	}
	for key := range existing {
		if key != "model" && key != "model_reasoning_effort" && key != "model_verbosity" && key != "personality" {
			return nil, "", securityError("project setting is outside the first native security subset: " + key)
		}
	}
	var settings map[string]any
	if plan != nil {
		settings, err = codexPolicySettings(plan.Normalized, selected)
		if err != nil {
			return nil, "", err
		}
		if len(state.Entries) != 0 || len(state.Files) != 0 {
			return nil, "", securityError("remove other owned adapter profiles before selecting the first security subset")
		}
		if err := checkCodexPolicyPaths(root, *plan.Normalized.Sandbox); err != nil {
			return nil, "", err
		}
	}
	hashes, err := codexAuthority(root, options.CodexHome)
	if err != nil {
		return nil, "", err
	}
	binary, err := codexNativeProbe(root, options.CodexHome)
	if err != nil {
		return nil, "", err
	}
	rendered, hash, err := mergeCodexSecurity(current, settings, state.SecurityHash, options)
	if err != nil {
		return nil, "", err
	}
	if plan != nil {
		plan.Status = "native-subset"
		plan.ProjectedSettings = settings
		plan.UnresolvedControls = []string{}
		plan.AutomaticGrants = []string{"filesystem.runtime=process: native private device and proc mounts plus caller-supplied standard streams", "environment and readable credential files are inherited; no credential isolation", "approval_policy=never prevents sandbox elevation"}
		plan.EvidenceScope = "Codex 0.154.0 Linux amd64 direct sandbox command; shell descendants only; full adapter support is not established"
		plan.NativeInvocation = []string{binary, "sandbox", "--permission-profile", codexSecurityProfile, "--include-managed-config", "--cd", root, "--", "<executable>", "<args...>"}
		plan.NativeEnvironment = map[string]string{"CODEX_HOME": options.CodexHome, "PATH": "/usr/bin:/bin"}
		plan.NativeEnvironmentMode = "replace"
		plan.Authority = hashes
		plan.Coverage = map[string]string{"shell": "native-subset", "builtin-tools": "unverified", "hooks": "unverified", "mcp-local": "unverified", "mcp-remote": "unverified", "lsp": "unverified", "delegation": "unverified"}
	}
	return rendered, hash, nil
}

func codexPolicyPathList(p SandboxPolicy) []string {
	paths := map[string]bool{}
	for _, rule := range p.Filesystem.Rules {
		paths[rule.Path] = true
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
