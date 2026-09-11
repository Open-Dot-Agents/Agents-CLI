package config

// Draft.2 native projections use a separate ownership registry. No draft.1
// ownership record can confer authority to change a user setting.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

const NativeVersion = "1.1.0-draft.2"

type nativeArtifact struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
}
type nativeProfile struct {
	directory      string
	Namespace      string           `json:"namespace"`
	HarnessVersion string           `json:"harness_version"`
	Scope          string           `json:"scope"`
	Required       bool             `json:"required"`
	Artifacts      []nativeArtifact `json:"artifacts"`
}
type NativeFeature struct {
	NativeStatus string   `json:"native_status,omitempty"`
	Evidence     []string `json:"evidence,omitempty"`
	Feature      string   `json:"feature"`
	Source       string   `json:"source"`
	Destination  string   `json:"destination,omitempty"`
	Scope        string   `json:"scope"`
	Disposition  string   `json:"disposition"`
	Activation   string   `json:"activation"`
	Ownership    string   `json:"ownership"`
	Authority    string   `json:"authority"`
	Limitation   string   `json:"limitation,omitempty"`
}
type NativePlan struct {
	StandardVersion string                     `json:"standard_version"`
	Platforms       []string                   `json:"platforms"`
	Scope           string                     `json:"scope"`
	NativeHome      string                     `json:"native_home,omitempty"`
	Versions        []string                   `json:"supported_versions"`
	Features        []NativeFeature            `json:"features"`
	RequiredActions []string                   `json:"required_native_actions"`
	SettingRegistry []NativeSettingDeclaration `json:"setting_registry,omitempty"`
}
type nativeOwned struct {
	Source string `json:"source_repository"`
	Hash   string `json:"sha256"`
}
type nativeRegistry struct {
	Version  string                 `json:"version"`
	Vendor   string                 `json:"vendor"`
	Home     string                 `json:"native_home"`
	Settings map[string]nativeOwned `json:"settings"`
}
type nativeChange struct {
	before *nativeSnapshot
	path   string
	data   []byte
	mode   fs.FileMode
	remove bool
}
type nativeTarget struct {
	path     string
	format   string
	settings map[string]any
	asset    []byte
	isAsset  bool
	sources  map[string]string
}
type nativeBuild struct {
	plan         PlanResult
	changes      []nativeChange
	registryPath string
}

func useNativeProjection(root string, options ApplyOptions) bool {
	var m manifestDocument
	return options.Scope == "user" || options.NativeHome != "" || (nativeDecodePolicy(filepath.Join(root, ".agents", "manifest.json"), &m) == nil && m.Version == NativeVersion)
}
func validateNativeScope(scope, home string, experimental bool) error {
	if scope != "" && scope != "project" && scope != "user" {
		return fmt.Errorf("scope must be project or user")
	}
	if scope == "user" {
		if !experimental {
			return fmt.Errorf("user scope requires --experimental")
		}
		if !filepath.IsAbs(home) {
			return fmt.Errorf("user scope requires an absolute --native-home")
		}
	} else if home != "" {
		return fmt.Errorf("--native-home requires --scope user")
	}
	return nil
}
func nativeNamespace(vendor string) (string, error) {
	switch vendor {
	case "codex":
		return "com.openai.codex", nil
	case "copilot":
		return "com.github.copilot", nil
	}
	return "", fmt.Errorf("native profile adapter is unavailable for %s", vendor)
}
func safeNativeRelative(name string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9_.@+/-]+$`).MatchString(name) && !filepath.IsAbs(name) && filepath.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, ".."+string(filepath.Separator)) && !strings.ContainsAny(name, "\\\x00")
}

// Check each existing component. This also rejects a symlink in a parent of a
// not-yet-created destination. User ownership must never follow a redirected path.
func nativeNoSymlinks(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(abs, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("native path contains a symlink: %s", current)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("native path is not a regular file or directory: %s", current)
		}
	}
	return nil
}
func readNativeProfiles(root string) ([]nativeProfile, error) {
	return readScopedNativeProfiles(root, "native")
}

func readScopedNativeProfiles(root, directory string) ([]nativeProfile, error) {
	base := filepath.Join(root, directory)
	if err := nativeNoSymlinks(base); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var result []nativeProfile
	for _, entry := range entries {
		if !entry.IsDir() {
			return nil, fmt.Errorf("native namespace must be a directory: %s", entry.Name())
		}
		path := filepath.Join(base, entry.Name(), "profile.json")
		if err := nativeNoSymlinks(path); err != nil {
			return nil, err
		}
		var p nativeProfile
		if data, err := nativeReadFile(path); err != nil {
			return nil, err
		} else if err = nativeUniqueJSON(data); err != nil {
			return nil, err
		}
		if err := nativeDecodePolicy(path, &p); err != nil {
			return nil, err
		}
		// Decode required fields separately: false and missing have different meanings.
		var raw map[string]json.RawMessage
		data, _ := nativeReadFile(path)
		_ = json.Unmarshal(data, &raw)
		if p.Namespace != entry.Name() || !regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`).MatchString(p.Namespace) || p.HarnessVersion == "" || (p.Scope != "project" && p.Scope != "user") || raw["required"] == nil || p.Artifacts == nil {
			return nil, fmt.Errorf("invalid native profile: %s", path)
		}
		p.directory = directory
		var artifactFields []map[string]json.RawMessage
		if err := json.Unmarshal(raw["artifacts"], &artifactFields); err != nil {
			return nil, err
		}
		for i, a := range p.Artifacts {
			if artifactFields[i]["name"] != nil && a.Name == "" {
				return nil, fmt.Errorf("native artifact name must not be empty")
			}
			if a.Kind == "" || !safeNativeRelative(a.Source) || (a.Name != "" && !safeNativeRelative(a.Name)) {
				return nil, fmt.Errorf("invalid native artifact in %s", path)
			}
			src, err := nativeArtifactSource(root, p, a)
			if err != nil {
				return nil, err
			}
			if err := nativeNoSymlinks(src); err != nil {
				return nil, err
			}
			info, err := os.Stat(src)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("native artifact must be a regular file: %s", src)
			}
		}
		p.directory = directory
		result = append(result, p)
	}
	return result, nil
}
func nativeTargetPath(vendor, scope, base string, a nativeArtifact) (string, string, error) {
	var relative, format string
	if a.Kind == "config" {
		if vendor == "codex" {
			relative = "config.toml"
			format = "toml"
			if scope == "project" {
				relative = ".codex/config.toml"
			}
		} else {
			relative = "settings.json"
			format = "json"
			if scope == "project" {
				relative = ".github/copilot/settings.json"
			}
		}
	} else if a.Kind == "lsp" && vendor == "copilot" {
		relative, format = "lsp-config.json", "json"
		if scope == "project" {
			relative = ".github/lsp.json"
		}
	} else if a.Kind == "mcp" && vendor == "copilot" {
		relative = "mcp-config.json"
		format = "json"
		if scope == "project" {
			relative = ".mcp.json"
		}
	} else {
		name := a.Name
		switch a.Kind {
		case "instructions":
			if vendor == "codex" {
				relative = "AGENTS.md"
			} else {
				relative = "copilot-instructions.md"
				if scope == "project" {
					relative = ".github/copilot-instructions.md"
				}
			}
		case "agent-instructions":
			if vendor != "copilot" || scope != "project" || !nativeCopilotAgentInstructionName(name) {
				return "", "", fmt.Errorf("agent instructions require a registered Copilot project path")
			}
			relative = name
		case "canonical-instructions":
			if vendor != "copilot" || scope != "project" || a.Source != "AGENTS.md" || name != "" {
				return "", "", fmt.Errorf("canonical instructions require the fixed Copilot project core binding")
			}
			relative = "AGENTS.md"
		case "agent":
			if !safeNativeRelative(name) || strings.Contains(name, "/") {
				return "", "", fmt.Errorf("agent requires a file name")
			}
			if vendor == "codex" {
				if !strings.HasSuffix(name, ".toml") {
					return "", "", fmt.Errorf("Codex agent must be TOML")
				}
				relative = filepath.Join("agents", name)
				if scope == "project" {
					relative = filepath.Join(".codex", relative)
				}
			} else {
				if !strings.HasSuffix(name, ".md") {
					return "", "", fmt.Errorf("Copilot agent must be Markdown")
				}
				relative = filepath.Join("agents", name)
				if scope == "project" {
					relative = filepath.Join(".github", relative)
				}
			}
		case "skill":
			if !safeNativeRelative(name) || !strings.Contains(name, "/") {
				return "", "", fmt.Errorf("skill requires a skill directory and file name")
			}
			relative = filepath.Join("skills", name)
			if scope == "project" {
				return "", "", fmt.Errorf("use the portable skills profile for project skills")
			}
		case "scoped-instructions":
			if vendor != "copilot" || !safeNativeRelative(name) || !strings.HasSuffix(name, ".instructions.md") {
				return "", "", fmt.Errorf("invalid scoped instruction artifact")
			}
			relative = filepath.Join("instructions", name)
			if scope == "project" {
				relative = filepath.Join(".github", relative)
			}
		case "hooks":
			if vendor == "codex" {
				relative = "hooks.json"
				format = "json"
				if scope == "project" {
					relative = ".codex/hooks.json"
				}
				break
			}
			if vendor != "copilot" || !safeNativeRelative(name) || strings.Contains(name, "/") || !strings.HasSuffix(name, ".json") {
				return "", "", fmt.Errorf("invalid hook artifact")
			}
			relative = filepath.Join("hooks", name)
			format = "json"
			if scope == "project" {
				relative = filepath.Join(".github", relative)
			}
		default:
			return "", "", fmt.Errorf("unknown native artifact kind %s", a.Kind)
		}
	}
	return filepath.Join(base, relative), format, nil
}
func parseNative(data []byte, format string) (map[string]any, error) {
	var out map[string]any
	var err error
	if format == "toml" {
		err = toml.Unmarshal(data, &out)
	} else {
		var clean []byte
		clean, err = stripJSONC(data)
		if err == nil {
			if err = nativeUniqueJSON(clean); err == nil {
				decoder := json.NewDecoder(bytes.NewReader(clean))
				decoder.UseNumber()
				err = decoder.Decode(&out)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if out == nil && format == "toml" {
		out = map[string]any{}
	}
	if out == nil {
		return nil, fmt.Errorf("native configuration must be an object")
	}
	return out, nil
}

// JSONC accepts comments and trailing commas, but never changes string content.
func stripJSONC(data []byte) ([]byte, error) {
	out := append([]byte(nil), data...)
	inString, escape := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == '/' && i+1 < len(out) {
			if out[i+1] == '/' {
				out[i] = ' '
				i++
				for i < len(out) && out[i] != '\n' {
					out[i] = ' '
					i++
				}
			} else if out[i+1] == '*' {
				out[i] = ' '
				i++
				out[i] = ' '
				closed := false
				for i++; i < len(out); i++ {
					if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
						out[i] = ' '
						i++
						out[i] = ' '
						closed = true
						break
					}
					if out[i] != '\n' {
						out[i] = ' '
					}
				}
				if !closed {
					return nil, fmt.Errorf("unterminated JSONC comment")
				}
			}
		}
	}
	inString, escape = false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inString {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && strings.ContainsRune(" \r\n\t", rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
	}
	return out, nil
}
func nativeEncode(value map[string]any, format string) ([]byte, error) {
	if format == "toml" {
		return toml.Marshal(value)
	}
	b, e := json.MarshalIndent(value, "", "  ")
	return append(b, '\n'), e
}

func nativeKey(path, key string) string { b, _ := json.Marshal([]string{path, key}); return string(b) }
func nativeStatePath(vendor, scope, root, home string) (string, error) {
	if scope == "project" {
		return filepath.Join(root, "state", "native-"+vendor+".json"), nil
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(h, ".local", "state")
	}
	if !filepath.IsAbs(state) {
		return "", fmt.Errorf("XDG_STATE_HOME must be absolute")
	}
	sum := sha256.Sum256([]byte(home))
	return filepath.Join(state, "open-dot-agents", "native", vendor, hex.EncodeToString(sum[:])+".json"), nil
}
func nativePinnedVersion(vendor string) string {
	if vendor == "codex" {
		return "0.154.0"
	}
	return "1.0.83"
}
func nativeForbidden(key string) bool {
	key = strings.ToLower(key)
	if key == "includecoauthoredby" || key == "bearer_token_env_var" {
		return false
	}
	if key == "projects" {
		return true
	}
	for _, part := range []string{"trust", "credential", "password", "secret", "api_key", "apikey", "access_token", "refresh_token", "bearer_token", "auth", "account", "organization", "managed", "enterprise"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
func nativeSecurityKey(key string) bool {
	key = strings.ToLower(key)
	if strings.Contains(key, "approval") || key == "defaultpermissionmode" || key == "default_permissions" || key == "shell_environment_policy" || key == "auto_review" {
		return true
	}
	for _, p := range []string{"approval", "sandbox", "permissions", "tools", "allow", "deny", "execution", "rules", "network", "filesystem", "experimental_use_freeform_apply_patch"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return key == "disableallhooks" || key == "enableallgithubmcptools"
}
func nativeSettingDisposition(vendor, scope, key string, value any) (string, string) {
	if nativeForbidden(key) {
		return "external", "authority or credential setting is excluded"
	}
	if nativeSecurityKey(key) {
		return "blocked", "native security mapping needs combined native behavior evidence"
	}
	if !nativeMappedValue(vendor, scope, key, value) {
		return "inactive", "no setting mapping for the pinned native version"
	}
	if nativeContainsExcluded(vendor, []string{key}, value) {
		return "external", "nested authority or credential setting is excluded"
	}
	return "configuration", "configuration write only; native reload and behavior require native evidence"
}
func nativeContainsExcluded(vendor string, path []string, value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			next := append(append([]string(nil), path...), k)
			if nativeFieldExcluded(vendor, next, x) || nativeContainsExcluded(vendor, next, x) {
				return true
			}
		}
	case []any:
		for i, x := range v {
			if nativeContainsExcluded(vendor, append(append([]string(nil), path...), fmt.Sprint(i)), x) {
				return true
			}
		}
	}
	return false
}
func prepareNativeProjection(vendor, root string, options ApplyOptions) (preparedProjection, error) {
	b, e := buildNativeProjection(vendor, root, options)
	if e != nil {
		return preparedProjection{}, e
	}
	return preparedProjection{result: b.plan}, nil
}
func buildNativeProjection(vendor, root string, options ApplyOptions) (nativeBuild, error) {
	var b nativeBuild
	if err := validateNativeScope(options.Scope, options.NativeHome, options.Experimental); err != nil {
		return b, err
	}
	if !options.Experimental {
		return b, fmt.Errorf("draft.2 native projection requires --experimental")
	}
	if options.CodexHome != "" {
		return b, fmt.Errorf("--codex-home retains draft.1 semantics; use --native-home for draft.2 user scope")
	}
	ns, err := nativeNamespace(vendor)
	if err != nil {
		return b, err
	}
	root, err = filepath.Abs(filepath.Join(root, ".agents"))
	if err != nil {
		return b, err
	}
	if err = nativeNoSymlinks(root); err != nil {
		return b, err
	}
	if err = ValidateRepositoryWithOptions(root, true); err != nil {
		return b, err
	}
	var m manifestDocument
	if err = nativeDecodePolicy(filepath.Join(root, "manifest.json"), &m); err != nil {
		return b, err
	}
	if m.Version != NativeVersion {
		return b, fmt.Errorf("native projection requires %s", NativeVersion)
	}
	if diagnostics, e := requiredCapabilityDiagnostics(vendor, root); e != nil {
		return b, e
	} else if len(diagnostics) > 0 {
		return b, fmt.Errorf("%s", strings.Join(diagnostics, "; "))
	}
	if options.Scope != "user" {
		selected := map[string]bool{}
		for _, profile := range m.Profiles {
			selected[profile] = true
		}
		if e := checkUnselectedSkills(vendor, filepath.Dir(root), selected); e != nil {
			return b, e
		}
	}
	scope := options.Scope
	if scope == "" {
		scope = "project"
	}
	base := filepath.Dir(root)
	home := ""
	if scope == "user" {
		home = filepath.Clean(options.NativeHome)
		base = home
	}
	if err = nativeNoSymlinks(base); err != nil {
		return b, err
	}
	statePath, err := nativeStatePath(vendor, scope, root, home)
	if err != nil {
		return b, err
	}
	b.registryPath = statePath
	if err = nativeNoSymlinks(statePath); err != nil {
		return b, err
	}
	state := nativeRegistry{Version: NativeVersion, Vendor: vendor, Home: home, Settings: map[string]nativeOwned{}}
	stateSnapshot, err := nativeReadSnapshot(statePath)
	if err != nil {
		return b, err
	}
	if stateSnapshot.exists {
		if err = nativeDecodePolicyData(statePath, stateSnapshot.data, &state); err != nil {
			return b, err
		}
	}
	if state.Version != NativeVersion || state.Vendor != vendor || state.Home != home || state.Settings == nil {
		return b, fmt.Errorf("native ownership registry identity mismatch")
	}
	if err = nativeValidateRegistry(vendor, scope, base, state); err != nil {
		return b, err
	}
	b.plan = PlanResult{SchemaVersion: NativeVersion, Vendor: vendor, Root: filepath.Dir(root), Actions: []Action{}, Native: &NativePlan{StandardVersion: NativeVersion, Platforms: []string{"linux"}, Scope: scope, NativeHome: home, Versions: []string{nativePinnedVersion(vendor)}, Features: []NativeFeature{}, RequiredActions: []string{"Use the pinned native CLI version.", "Reload native configuration. Login, plugin installation, scheduling, and execution are separate native operations."}}}
	if vendor == "copilot" && nativeHasProfile(m.Profiles, "skills") {
		if err = nativePlanCopilotSkills(&b.plan, root, base, scope); err != nil {
			return b, err
		}
		if len(b.plan.Diagnostics) > 0 {
			return b, nil
		}
	}
	targets := map[string]*nativeTarget{}
	canonicalBinding := false
	agentNames := map[string]string{}
	agentStems := map[string]string{}
	agentTargets := map[string]string{}
	add := func(path, format, key string, value any, source string, asset bool) error {
		t := targets[path]
		if t == nil {
			t = &nativeTarget{path: path, format: format, settings: map[string]any{}, sources: map[string]string{}, isAsset: asset}
			targets[path] = t
		}
		if t.isAsset != asset || t.format != format {
			return fmt.Errorf("conflicting artifact formats for %s", path)
		}

		assignments := map[string]any{"": value}
		if !asset {
			assignments = nativeFlatten(map[string]any{key: value})
		}
		for field, child := range assignments {
			for prior := range t.sources {
				if field == prior || strings.HasPrefix(field, prior+"/") || strings.HasPrefix(prior, field+"/") {
					return fmt.Errorf("duplicate native assignment for %s %s", path, field)
				}
			}
			t.sources[field] = source
			if asset {
				t.asset = child.([]byte)
			} else {
				t.settings[field] = child
			}
		}

		return nil
	}
	security := nativeHasProfile(m.Profiles, "permissions") || nativeHasProfile(m.Profiles, "sandbox")
	// A new projection path must not bypass portable mandatory policy.
	if security {
		return b, fmt.Errorf("native profiles cannot replace portable security enforcement; combined draft.2 security projection is not verified")
	}
	for _, directory := range []string{"native", "plugins"} {
		if !nativeHasProfile(m.Profiles, directory) {
			continue
		}
		profiles, err := readScopedNativeProfiles(root, directory)
		if err != nil {
			return b, err
		}
		for _, p := range profiles {
			if p.Scope != scope {
				continue
			}
			if p.Namespace != ns {
				if p.Required {
					return b, fmt.Errorf("required native namespace %s cannot activate for %s", p.Namespace, vendor)
				}
				continue
			}
			versionOK := strings.TrimPrefix(p.HarnessVersion, "=") == nativePinnedVersion(vendor)
			if !versionOK && p.Required {
				return b, fmt.Errorf("native version constraint %s is not supported", p.HarnessVersion)
			}
			for _, a := range p.Artifacts {
				src, sourceErr := nativeArtifactSource(root, p, a)
				if sourceErr != nil {
					return b, sourceErr
				}
				path, format, e := nativeTargetPath(vendor, scope, base, a)
				feature := NativeFeature{Feature: a.Kind, Source: src, Destination: path, Scope: scope, Ownership: "requested by source repository", Authority: "registry target", Activation: "inactive"}
				if p.directory == "plugins" && a.Kind != "config" {
					e = fmt.Errorf("plugin selection profiles only project configuration")
				}
				if e != nil || !versionOK {
					if scope == "project" && p.directory == "native" && a.Kind == "canonical-instructions" {
						return b, fmt.Errorf("cannot choose a portable instruction target from an unmapped canonical binding")
					}
					feature.Disposition = "inactive"
					feature.Limitation = "unknown artifact or unsupported native version"
					b.plan.Native.Features = append(b.plan.Native.Features, feature)
					if p.Required {
						return b, fmt.Errorf("required native artifact %s is unmapped: %v", src, e)
					}
					continue
				}
				if a.Kind == "canonical-instructions" {
					if canonicalBinding {
						return b, fmt.Errorf("duplicate canonical instruction binding")
					}
					canonicalBinding = true
					feature = nativeCanonicalInstructionFeature(feature)
					feature.Activation = "portable core projected at the registered root path"
					b.plan.Native.Features = append(b.plan.Native.Features, feature)
					b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, nativeAgentInstructionReferenceAction)
					continue
				}
				data, e := nativeReadFile(src)
				if e != nil {
					return b, e
				}
				if format == "" {
					if vendor == "copilot" && scope == "user" && a.Kind == "instructions" {
						feature = nativeCopilotUserInstructionFeature(feature)
						b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, nativeUserInstructionReferenceAction)
					}
					if a.Kind == "agent-instructions" {
						if !utf8.Valid(data) {
							return b, fmt.Errorf("native agent instructions must be UTF-8 Markdown")
						}
						feature = nativeAgentInstructionFeature(feature)
						b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, nativeAgentInstructionReferenceAction)
					}
					if a.Kind == "scoped-instructions" {
						feature = nativeCopilotRecursiveInstructionFeature(feature)
						if scope == "user" {
							b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, nativeCopilotInstructionReadAction)
						}
					}
					if a.Kind == "agent" {
						name := ""
						if vendor == "codex" {
							name, e = nativeCodexAgent(data, scope)
							feature = nativeCodexRoleFeature(feature)
						} else {
							name, e = nativeCopilotAgent(data, path)
							feature = nativeCopilotAgentFeature(feature)
						}
						if e != nil {
							feature.Disposition, feature.Activation, feature.Limitation = "inactive", "inactive", e.Error()
							feature.NativeStatus, feature.Evidence = "unverified", nil
							b.plan.Native.Features = append(b.plan.Native.Features, feature)
							if p.Required {
								return b, fmt.Errorf("required native agent cannot activate: %w", e)
							}
							continue
						}
						if prior, duplicate := agentNames[name]; duplicate {
							return b, fmt.Errorf("duplicate native agent identity in %s and %s", prior, src)
						}
						if vendor == "copilot" {
							stem := filepath.Join(filepath.Dir(path), nativeCopilotAgentStem(path))
							if prior, exists := agentStems[stem]; exists {
								return b, fmt.Errorf("duplicate native agent file identity in %s and %s", prior, src)
							}
							agentStems[stem] = src
						}
						agentNames[name] = src
						agentTargets[path] = name
					}
					if e = add(path, format, "", data, src, true); e != nil {
						return b, e
					}
					feature.Disposition = "configuration"
					feature.Activation = "pending native reload"
					b.plan.Native.Features = append(b.plan.Native.Features, feature)
					continue
				}
				values, e := parseNative(data, format)
				if e != nil {
					return b, fmt.Errorf("%s: %w", src, e)
				}
				if a.Kind == "mcp" && vendor == "copilot" {
					values = nativeCopilotMCPValues(values)
				}
				if e = nativeCheckRuntimeAuthentication(vendor, values); e != nil {
					return b, e
				}
				if a.Kind == "mcp" && vendor == "copilot" {
					feature = nativeCopilotMCPFeature(feature)
					var inactive []nativeInactiveField
					values, inactive, e = nativeSelectCopilotMCP(values)
					if e != nil {
						return b, e
					}
					b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, inactive)...)
					if p.Required && len(inactive) > 0 {
						return b, fmt.Errorf("required native MCP field %s cannot activate", inactive[0].Path)
					}
					b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Use native MCP status and authentication commands as needed. Project MCP requires native folder trust; apply does not write trust state.")
				}
				if a.Kind == "lsp" {
					feature = nativeLSPFeature(feature)
					b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Install the configured LSP server separately and start a new Copilot session.")
					var inactive []nativeInactiveField
					values, inactive = nativeSelectLSP(values)
					b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, inactive)...)
					if p.Required {
						if e = nativeLSPRequired(inactive); e != nil {
							return b, e
						}
					}
				}
				if a.Kind == "hooks" && vendor == "copilot" {
					feature = nativeCopilotHookFeature(feature)
					var inactive []nativeInactiveField
					values, inactive, e = nativeSelectCopilotHooks(values, false)
					if e != nil {
						return b, e
					}
					b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, inactive)...)
					if p.Required && len(inactive) > 0 {
						return b, fmt.Errorf("required native hook field %s cannot activate: %s", inactive[0].Path, inactive[0].Reason)
					}
					b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Hook files run commands when the native CLI loads them. Project hooks require native folder trust and a supported native session interface; apply does not grant trust. Local HTTP hooks require the native COPILOT_HOOK_ALLOW_LOCALHOST=1 opt-in; permission responses require HTTPS.")
				}
				if a.Kind == "hooks" && vendor == "codex" {
					feature = nativeCodexHookFeature(feature)
					var inactive []nativeInactiveField
					values, inactive, e = nativeSelectCodexHooks(values, false)
					if e != nil {
						return b, e
					}
					b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, inactive)...)
					if p.Required && len(inactive) > 0 {
						return b, fmt.Errorf("required native Codex hook field %s cannot activate: %s", inactive[0].Path, inactive[0].Reason)
					}
				}
				if p.directory == "plugins" {
					for key := range values {
						if !nativePluginRoot(vendor, key) {
							if p.Required {
								return b, fmt.Errorf("required plugin selection artifact contains a non-plugin field: %s", key)
							}
							b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, []nativeInactiveField{{"/" + nativePointer(key), "inactive", "not a plugin selection field"}})...)
							delete(values, key)
						}
					}
				}
				if a.Kind == "config" {
					if vendor == "codex" {
						if e = nativeCodexAliases(values, false); e != nil {
							return b, e
						}
					}
					var inactive []nativeInactiveField
					values, inactive = nativeSelectConfig(vendor, scope, values)
					b.plan.Native.Features = append(b.plan.Native.Features, nativeInactiveFeatures(feature, a.Kind, inactive)...)
					if p.Required && len(inactive) != 0 {
						return b, fmt.Errorf("required native field %s cannot activate: %s", inactive[0].Path, inactive[0].Reason)
					}
					if vendor == "codex" && scope == "user" {
						providers, _ := values["model_providers"].(map[string]any)
						for _, name := range nativeSortedKeys(providers) {
							provider, _ := providers[name].(map[string]any)
							if provider["auth"] != nil {
								authFeature := feature
								authFeature.Feature = "artifact:config:/model_providers/" + nativePointer(name) + "/auth"
								authFeature.Disposition, authFeature.Activation = "configuration", "pending native reload"
								b.plan.Native.Features = append(b.plan.Native.Features, nativeCodexCommandAuthFeature(authFeature))
								b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Install and check the provider token command separately. Codex token-command failures can send unauthenticated model requests; apply does not execute the command or grant native authority.")
							}
						}
					}
				}
				keys := make([]string, 0, len(values))
				for key := range values {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				if len(keys) == 0 {
					feature.Activation, feature.Disposition = "inactive", "inactive"
					feature.NativeStatus, feature.Evidence = "unverified", nil
					feature.Limitation = "No mapped settings are selected from this artifact."
					b.plan.Native.Features = append(b.plan.Native.Features, feature)
					continue
				}
				for _, key := range keys {
					f := feature
					f.Feature = a.Kind + ":" + key
					f.Disposition, f.Limitation = nativeSettingDisposition(vendor, scope, key, values[key])
					// Separate JSON artifacts need their own field registry.
					if a.Kind == "lsp" {
						f.Disposition, f.Limitation = "configuration", "LSP configuration only; install the server and reload Copilot before use"
					} else if a.Kind == "mcp" && vendor == "copilot" {
						f.Disposition, f.Limitation = "configuration", "Native MCP fields; project loading requires native folder trust. Apply does not install servers or authenticate."
					} else if a.Kind == "hooks" && vendor == "copilot" {
						f.Disposition, f.Limitation = "configuration", feature.Limitation
					} else if a.Kind == "hooks" && vendor == "codex" {
						f.Disposition, f.Limitation = "configuration", feature.Limitation
					} else if a.Kind == "config" && vendor == "codex" && key == "hooks" && f.Disposition == "configuration" {
						f = nativeCodexHookFeature(f)
						f.Disposition = "configuration"
						f.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-hooks-inline-" + scope + ".json"}
					} else if a.Kind == "config" && vendor == "codex" && key == "otel" && f.Disposition == "configuration" {
						f = nativeCodexOtelFeature(f)
					} else if a.Kind == "config" && vendor == "copilot" && key == "hooks" && f.Disposition == "configuration" {
						f = nativeCopilotHookFeature(f)
						f.Disposition = "configuration"
						f.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-inline-" + scope + ".json"}
					} else if a.Kind != "config" {
						f.Disposition = "inactive"
						f.Limitation = "artifact field mapping is not verified"
					}
					if f.Disposition == "configuration" {
						if a.Kind == "config" && vendor == "copilot" {
							f = nativeCopilotPreferenceFeature(f, key)
						}
						if a.Kind == "config" && nativePluginRoot(vendor, key) {
							f = nativePluginFeature(vendor, f)
							b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Use native plugin and marketplace commands to inspect availability and installation. The native client can fetch enabled packages on startup or refresh. Apply only writes selection configuration; it does not install, update, authenticate, or grant trust.")
							if vendor == "codex" && key == "marketplaces" {
								b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "For a Git marketplace without a native cache, run codex plugin marketplace add SOURCE with the configured --ref and --sparse options before codex plugin add NAME@MARKETPLACE. Selection configuration alone does not populate the marketplace cache.")
							}
						}
						f.Activation = "pending native reload"
						if vendor == "codex" && a.Kind == "config" && key == "agents" {
							roles, _ := values[key].(map[string]any)
							hasReference := false
							for _, name := range nativeSortedKeys(roles) {
								role, _ := roles[name].(map[string]any)
								if _, ok := role["config_file"].(string); !ok {
									continue
								}
								reference := f
								hasReference = true
								reference.Feature = "artifact:role-reference:/agents/" + nativePointer(name) + "/config_file"
								b.plan.Native.Features = append(b.plan.Native.Features, nativeCodexRoleReferenceFeature(reference))
							}
							if hasReference {
								b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Keep external agent config_file references and skill selectors available at their resolved paths. Apply owns only selected native assets; it does not copy external role or skill libraries. Reload Codex to check discovery.")
							}
						}
						if vendor == "codex" && a.Kind == "config" && key == "skills" {
							if preferences, ok := values[key].(map[string]any); ok && preferences["config"] != nil {
								f = nativeCodexSkillFeature(f)
							}
						}
						if vendor == "copilot" && a.Kind == "config" && key == "subagents" {
							f = nativeCopilotSubagentFeature(f)
							b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Check native model availability and /subagents dispatch settings. Depth and concurrency overrides require native usage-based billing and are ignored on other plans. Apply does not change account state or enforce runtime limits.")
							if preferences, ok := values[key].(map[string]any); ok {
								_, depth := preferences["maxDepth"]
								_, concurrency := preferences["maxConcurrency"]
								if depth || concurrency {
									f.Activation = "requires native usage-based billing and reload"
									f.NativeStatus = "native-prerequisite-required"
								}
							}
						}
						if vendor == "codex" && (a.Kind == "hooks" || a.Kind == "config" && key == "hooks") {
							f.Activation = "requires native hook trust and reload"
							b.plan.Native.RequiredActions = append(b.plan.Native.RequiredActions, "Review and trust the exact Codex hook definitions through native hook controls before execution. Apply does not write hooks.state or grant trust. Changed definitions require new native review. Project hook sources also require native project trust. Referenced MCP servers must be configured and connected separately. MCP hook errors and timeouts do not establish operation denial or tool termination. Background command hooks cannot control the triggering operation. Native context spill files are runtime output; apply does not manage them.")
						}
						if e = add(path, format, key, values[key], src, false); e != nil {
							return b, e
						}
					} else if p.Required {
						return b, fmt.Errorf("required native setting %s cannot activate: %s", key, f.Limitation)
					}
					b.plan.Native.Features = append(b.plan.Native.Features, f)
				}
			}
		}
	}
	// Portable instructions map to the same target registry. This detects a
	// duplicate native artifact before a write.
	if scope == "project" {
		src := filepath.Join(root, "AGENTS.md")
		data, e := nativeReadFile(src)
		if e != nil {
			return b, e
		}
		path, format, e := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "instructions"})
		if e != nil {
			return b, e
		}
		if canonicalBinding {
			path, format, e = nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "canonical-instructions", Source: "AGENTS.md"})
			if e != nil {
				return b, e
			}
		}
		link := filepath.Join(base, "AGENTS.md")
		info, statErr := os.Lstat(link)
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return b, statErr
		}
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			if e = validateInstructionFile(link, src); e != nil {
				return b, e
			}
			if targets[link] != nil || !canonicalBinding && targets[path] != nil {
				return b, fmt.Errorf("duplicate native assignment for %s", path)
			}
			if path == link {
				key := nativeKey(path, "")
				if owner, owned := state.Settings[key]; owned && owner.Source != root {
					return b, fmt.Errorf("instruction path is owned by another source repository")
				}
				// A verified canonical link is not a managed native asset. If a
				// copied instruction file was converted to this link, relinquish
				// that file ownership without deleting or following the link.
				delete(state.Settings, key)
			}
			b.plan.Native.Features = append(b.plan.Native.Features, NativeFeature{Feature: "instructions:canonical-link", Source: src,
				Destination: link, Scope: scope, Disposition: "portable-mapping", Activation: "existing canonical compatibility link",
				Ownership: "not owned by native projection", Authority: "verified link to this project's canonical instructions"})
		} else {
			rootOwner, rootOwned := state.Settings[nativeKey(link, "")]
			if vendor == "copilot" && path != link && statErr == nil && targets[link] == nil && rootOwned && rootOwner.Source == root && bytes.Contains(data, []byte("@")) {
				rootData, err := nativeReadFile(link)
				if err != nil {
					return b, err
				}
				if bytes.Equal(rootData, data) {
					return b, fmt.Errorf("moving owned root instructions would change a native reference base; retain the canonical binding or update the references")
				}
			}
			if vendor == "copilot" && path != link && statErr == nil && targets[link] == nil && !(rootOwned && rootOwner.Source == root) {
				rootData, err := nativeReadFile(link)
				if err != nil {
					return b, err
				}
				if !bytes.Equal(rootData, data) {
					return b, fmt.Errorf("native projection refuses distinct project instructions in root AGENTS.md and canonical AGENTS.md")
				}
				if bytes.Contains(rootData, []byte("@")) {
					return b, fmt.Errorf("Copilot root AGENTS.md contains a potential file reference; projection cannot preserve its reference base at .github/copilot-instructions.md")
				}
			}
			if e = add(path, format, "", data, src, true); e != nil {
				return b, e
			}
		}
	}

	if nativeHasProfile(m.Profiles, "tools") {
		servers, e := readCanonicalMCP(root)
		if e != nil {
			return b, e
		}
		if diagnostics := referenceDiagnostics(vendor, servers); len(diagnostics) > 0 {
			return b, fmt.Errorf("%s", strings.Join(diagnostics, "; "))
		}
		kind := "config"
		if vendor == "copilot" {
			kind = "mcp"
		}
		path, format, e := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: kind})
		if e != nil {
			return b, e
		}
		for name, server := range servers {
			var values map[string]any
			if vendor == "codex" {
				data, e := renderCodexBlock(name, server)
				if e != nil {
					return b, e
				}
				values, e = parseNative(data, "toml")
				if e != nil {
					return b, e
				}
			} else {
				mapped, mapErr := nativePortableCopilotMCP(name, server, targets[path])
				if mapErr != nil {
					return b, mapErr
				}
				values = map[string]any{"mcpServers": map[string]any{name: mapped}}
			}
			for key, value := range values {
				if e = add(path, format, key, value, filepath.Join(root, "tools/mcp.json"), false); e != nil {
					return b, e
				}
			}
		}
	}
	if nativeHasProfile(m.Profiles, "hooks") {
		hooks, e := readCanonicalHooks(root)
		if e != nil {
			return b, e
		}
		if vendor == "codex" && hooks.DisableAllHooks {
			return b, fmt.Errorf("Codex hooks.json rejects disableAllHooks; a global native feature flag is not an equivalent portable mapping")
		}
		values, e := nativeHooks(vendor, hooks)
		if e != nil {
			return b, e
		}
		path, format, e := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "hooks", Name: "open-dot-agents.json"})
		if e != nil {
			return b, e
		}
		if vendor == "copilot" {
			if target := targets[path]; target != nil {
				if target.settings["/disableAllHooks"] == true && !hooks.DisableAllHooks && len(hooks.Hooks) > 0 {
					return b, fmt.Errorf("native hook control conflicts with selected portable hooks")
				}
				if version, exists := target.settings["/version"]; exists && nativeHash(version) == nativeHash(1) {
					delete(values, "version") // Preserve the explicit native file format version.
				}
			}
		}
		for key, value := range values {
			if e = add(path, format, key, value, filepath.Join(root, "hooks/hooks.json"), false); e != nil {
				return b, e
			}
		}
	}
	if scope == "user" && nativeHasProfile(m.Profiles, "skills") {
		skillRoot := filepath.Join(root, "skills")
		err = filepath.WalkDir(skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if e := nativeNoSymlinks(path); e != nil {
				return e
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("skill asset must be a regular file")
			}
			relative, e := filepath.Rel(skillRoot, path)
			if e != nil {
				return e
			}
			dest, format, e := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "skill", Name: relative})
			if e != nil {
				return e
			}
			data, e := nativeReadFile(path)
			if e != nil {
				return e
			}
			return add(dest, format, "", data, path, true)
		})
		if err != nil {
			return b, err
		}
	}

	// Include old targets to remove only this repository's stale assignments.
	for encoded, owned := range state.Settings {
		var k []string
		if json.Unmarshal([]byte(encoded), &k) != nil || len(k) != 2 {
			return b, fmt.Errorf("invalid ownership key")
		}
		if owned.Source != root {
			continue
		}
		rel, e := filepath.Rel(base, k[0])
		if e != nil || !safeNativeRelative(rel) {
			return b, fmt.Errorf("ownership target escapes native scope")
		}
		// Registry entries cannot introduce arbitrary removal destinations.
		if !nativeOwnedDestination(vendor, scope, base, k[0]) {
			return b, fmt.Errorf("ownership target is outside adapter registry")
		}
		if targets[k[0]] == nil {
			target, ok := nativeTargetForPath(vendor, scope, base, k[0])
			if !ok {
				return b, fmt.Errorf("ownership target is outside adapter registry")
			}
			targets[k[0]] = target
		}
	}
	paths := make([]string, 0, len(targets))
	for path, name := range agentTargets {
		if err = nativeAgentNameConflict(vendor, path, name, agentTargets, targets, state, root); err != nil {
			return b, err
		}
	}
	for path := range targets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		t := targets[path]
		if err = nativeNoSymlinks(path); err != nil {
			return b, err
		}
		before, readErr := nativeReadSnapshot(path)
		if readErr != nil {
			return b, readErr
		}
		old, exists := before.data, before.exists
		mode := fs.FileMode(0644)
		if scope == "user" {
			mode = 0600
		}
		if t.isAsset {
			if info, e := os.Stat(t.sources[""]); e == nil && info.Mode().Perm()&0111 != 0 {
				if scope == "user" {
					mode = 0700
				} else {
					mode = 0755
				}
			}
		}
		if before.exists {
			mode = before.mode
		}
		values := map[string]any{}
		if exists && !t.isAsset {
			values, err = parseNative(old, t.format)
			if err != nil {
				return b, err
			}
			mcpPath, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "mcp"})
			if vendor == "copilot" && path == mcpPath {
				values = nativeCopilotMCPValues(values)
			}
			values = nativeFlatten(values)
		}
		if vendor == "copilot" && !t.isAsset {
			hookPath, _, hookErr := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "hooks", Name: filepath.Base(path)})
			if err := nativeCopilotHookControlOwnership(path, root, t, values, state, hookErr == nil && hookPath == path); err != nil {
				return b, err
			}
		}
		remove := false
		keys := map[string]bool{}
		for key := range t.sources {
			keys[key] = true
		}
		for encoded, owned := range state.Settings {
			var k []string
			_ = json.Unmarshal([]byte(encoded), &k)
			if len(k) == 2 && k[0] == path && owned.Source == root {
				keys[k[1]] = true
			}
		}
		for key := range keys {
			encoded := nativeKey(path, key)
			owner, owned := state.Settings[encoded]
			_, desired := t.sources[key]
			var current any
			present := exists
			if t.isAsset {
				current = old
			} else {
				current, present = values[key]
			}
			conflict := ""
			if desired && !t.isAsset {
				for currentKey := range values {
					if currentKey != key && (strings.HasPrefix(currentKey, key+"/") || strings.HasPrefix(key, currentKey+"/")) {
						conflict = "setting conflicts with an existing parent or child"
					}
				}
				for encodedOwner, otherOwner := range state.Settings {
					var other []string
					_ = json.Unmarshal([]byte(encodedOwner), &other)
					if len(other) == 2 && other[0] == path && otherOwner.Source != root && (other[1] == key || strings.HasPrefix(other[1], key+"/") || strings.HasPrefix(key, other[1]+"/")) {
						conflict = "setting overlaps ownership from another source repository"
					}
				}
			}
			if owned && owner.Source != root {
				conflict = "setting is owned by another source repository"
			} else if owned && present && !nativeHashMatches(current, owner.Hash) {
				conflict = "owned setting was modified outside the adapter"
			} else if !owned && present {
				var desiredValue any
				if t.isAsset {
					desiredValue = t.asset
				} else {
					desiredValue = t.settings[key]
				}
				if !options.Adopt || !desired || nativeHash(current) != nativeHash(desiredValue) {
					conflict = "setting has no ownership record"
				}
			}
			if conflict != "" {
				b.plan.Diagnostics = append(b.plan.Diagnostics, path+": "+conflict)
				continue
			}
			if desired {
				var desiredValue any
				if t.isAsset {
					desiredValue = t.asset
				} else {
					desiredValue = t.settings[key]
					values[key] = desiredValue
				}
				state.Settings[encoded] = nativeOwned{Source: root, Hash: nativeHash(desiredValue)}
			} else {
				delete(state.Settings, encoded)
				if t.isAsset {
					remove = true
				} else {
					delete(values, key)
				}
			}
		}
		var data []byte
		if t.isAsset {
			data = t.asset
		} else {
			document := nativeUnflatten(values)
			if vendor == "copilot" && scope == "user" && path == filepath.Join(home, "settings.json") {
				if err = nativeCopilotLegacyConflict(home, document, keys); err != nil {
					return b, err
				}
			}
			if vendor == "copilot" || vendor == "codex" {
				hookPath, _, hookErr := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: "hooks", Name: filepath.Base(path)})
				if hookErr == nil && hookPath == path {
					if len(values) == 0 {
						remove = exists
					}
					if hooks, ok := document["hooks"].(map[string]any); vendor == "copilot" && ok && len(hooks) > 0 && nativeHash(document["version"]) != nativeHash(1) {
						return b, fmt.Errorf("native hook removal would leave events without version 1 in %s", path)
					}
				}
			}
			if vendor == "codex" && t.format == "toml" {
				if err = nativeCodexAliases(document, false); err != nil {
					return b, err
				}
			}
			mcpPath, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "mcp"})
			if vendor == "copilot" && path == mcpPath {
				servers, _ := document["mcpServers"].(map[string]any)
				checked := map[string]bool{}
				for key := range t.settings {
					parts, parseErr := nativePointerParts(key)
					if parseErr != nil || len(parts) < 2 || parts[0] != "mcpServers" || checked[parts[1]] {
						continue
					}
					checked[parts[1]] = true
					server, ok := servers[parts[1]].(map[string]any)
					if !ok {
						return b, fmt.Errorf("native MCP server has no complete definition")
					}
					if err = nativeValidateCopilotMCPServer(server); err != nil {
						return b, err
					}
				}
			}
			data, err = nativeEncode(document, t.format)
			if err != nil {
				return b, err
			}
		}
		if len(b.plan.Diagnostics) > 0 {
			continue
		}
		if remove && !exists {
			continue
		}
		if !remove && bytes.Equal(data, old) {
			continue
		}
		// Preserve formatting and comments when no semantic value changed.
		if !remove && exists && !t.isAsset {
			before, _ := parseNative(old, t.format)
			mcpPath, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "mcp"})
			if vendor == "copilot" && path == mcpPath {
				before = nativeCopilotMCPValues(before)
			}
			if reflect.DeepEqual(nativeFlatten(before), values) {
				continue
			}
		}
		action := "create"
		if remove {
			action = "remove"
		} else if exists {
			action = "update"
		}
		b.plan.Actions = append(b.plan.Actions, Action{Path: path, Operation: action, Detail: "native"})
		b.changes = append(b.changes, nativeChange{path: path, data: data, mode: mode, remove: remove, before: &before})
	}
	b.plan.Applicable = len(b.plan.Diagnostics) == 0
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return b, err
	}
	stateBytes = append(stateBytes, '\n')
	oldState := stateSnapshot.data
	if !bytes.Equal(oldState, stateBytes) {
		b.changes = append(b.changes, nativeChange{path: statePath, data: stateBytes, mode: 0600, before: &stateSnapshot})
		b.plan.Actions = append(b.plan.Actions, Action{Path: statePath, Operation: "update", Detail: "ownership"})
	}
	if options.Backup && b.plan.Applicable {
		if err = nativePlanBackups(&b); err != nil {
			return b, err
		}
	}

	return b, nil
}
func nativeOwnedDestination(vendor, scope, base, path string) bool {
	_, ok := nativeTargetForPath(vendor, scope, base, path)
	return ok
}

func applyNativeProjection(vendor, root string, options ApplyOptions) (PlanResult, error) {
	first, err := buildNativeProjection(vendor, root, options)
	if err != nil {
		return PlanResult{}, err
	}
	if !first.plan.Applicable {
		return first.plan, fmt.Errorf("native projection has conflicts; --force cannot override setting ownership")
	}
	if err = nativeNoSymlinks(first.registryPath); err != nil {
		return first.plan, err
	}
	if err = nativeMkdirAll(filepath.Dir(first.registryPath)); err != nil {
		return first.plan, err
	}
	lockPath := first.registryPath + ".lock"
	if err = nativeNoSymlinks(lockPath); err != nil {
		return first.plan, err
	}
	lock, err := acquireNativeTargetLock(lockPath)
	if err != nil {
		return first.plan, err
	}
	defer lock.release()

	// Re-read authority, source files, and ownership under the target lock.
	b, err := buildNativeProjection(vendor, root, options)
	if err != nil {
		return first.plan, err
	}
	if !b.plan.Applicable {
		return b.plan, fmt.Errorf("native projection conflicts after locking")
	}
	if err = nativeLockedTransaction(b.changes, lock); err != nil {
		return b.plan, err
	}
	return b.plan, nil
}

func nativePlanBackups(b *nativeBuild) error {
	var backups []nativeChange
	for _, c := range b.changes {
		snapshot, err := nativeReadSnapshot(c.path)
		if err != nil {
			return err
		}
		if c.before != nil && !c.before.equal(snapshot) {
			return fmt.Errorf("native backup source changed after planning: %s", c.path)
		}
		if !snapshot.exists {
			continue
		}
		old := snapshot.data
		backup := c.path + ".bak"
		// A root-level marker backup is not a skill package. Keep it out of
		// selected skills while retaining the ordinary private backup and
		// rollback rules. Only the canonical empty-marker removal uses this.
		skillsDir := filepath.Dir(c.path)
		canonical := filepath.Dir(skillsDir)
		if c.remove && len(old) == 0 && filepath.Base(c.path) == ".gitkeep" && filepath.Base(skillsDir) == "skills" && filepath.Base(canonical) == ".agents" {
			backup = filepath.Join(canonical, "state/import-backups/skills.gitkeep.bak")
		}
		if err = nativeNoSymlinks(backup); err != nil {
			return err
		}
		if _, err = os.Lstat(backup); err == nil {
			return fmt.Errorf("backup already exists: %s", backup)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		backups = append(backups, nativeChange{path: backup, data: old, mode: 0600, before: &nativeSnapshot{}})
		b.plan.Actions = append(b.plan.Actions, Action{Path: backup, Operation: "create", Detail: "backup"})
	}
	b.changes = append(backups, b.changes...)
	return nil
}

func nativeTransaction(changes []nativeChange) error {
	return nativeRunTransaction(changes, nil)
}
func nativeAtomicWrite(path string, data []byte, mode fs.FileMode) error {
	return nativeTransaction([]nativeChange{{path: path, data: data, mode: mode}})
}
func applyNativeSync(vendor, root string, options ApplyOptions) (SyncResult, error) {
	plan, err := applyNativeProjection(vendor, root, options)
	result := SyncResult{SchemaVersion: NativeVersion, Root: root, Applicable: plan.Applicable, Vendors: []PlanResult{plan}}
	return result, err
}
func importNativeRepository(vendor, root string, options WriteOptions) error {
	if err := validateNativeScope(options.Scope, options.NativeHome, options.Experimental); err != nil {
		return err
	}
	if !options.Experimental {
		return fmt.Errorf("native import requires --experimental")
	}
	ns, err := nativeNamespace(vendor)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(filepath.Join(root, ".agents"))
	if err != nil {
		return err
	}
	if err = nativeNoSymlinks(root); err != nil {
		return err
	}
	sharedProjectSkills := vendor == "copilot" && (options.Scope == "" || options.Scope == "project")
	if _, err = nativeImportExisting(root, sharedProjectSkills); err != nil {
		return err
	}

	if err = nativeMkdirAll(filepath.Dir(root)); err != nil {
		return err
	}
	lockPath := filepath.Join(filepath.Dir(root), ".agents-import.lock")
	if err = nativeNoSymlinks(lockPath); err != nil {
		return err
	}
	lock, err := acquireNativeTargetLock(lockPath)
	if err != nil {
		return err
	}
	defer lock.release()
	existing, err := nativeImportExisting(root, sharedProjectSkills)
	if err != nil {
		return err
	}
	scope := options.Scope
	if scope == "" {
		scope = "project"
	}
	base := filepath.Dir(root)
	if scope == "user" {
		base = filepath.Clean(options.NativeHome)
	}
	p := nativeProfile{Namespace: ns, HarnessVersion: "=" + nativePinnedVersion(vendor), Scope: scope, Required: false, Artifacts: []nativeArtifact{}}
	var changes []nativeChange
	excluded := []string{}
	var importedProjectSkills []string
	foundSource := false
	profiles := []string{"native"}
	instructions := []byte("Use the selected native profile.\n")
	instructionsProvided := false
	var rootInstructions []byte
	rootRegular := false
	agentInstructionSources := map[string]string{}
	bindings := nativeInstructionBindings{}
	if vendor == "copilot" && scope == "project" {
		bindings, err = nativeExistingInstructionBindings(root)
		if err != nil {
			return err
		}
		agentInstructionSources, err = nativeExistingAgentInstructionSources(root)
		if err != nil {
			return err
		}
	}
	canonicalLink := ""
	if scope == "project" {
		link := filepath.Join(base, "AGENTS.md")
		info, statErr := os.Lstat(link)
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			source := filepath.Join(root, "AGENTS.md")
			if err := validateInstructionFile(link, source); err != nil {
				return err
			}
			// Read the verified canonical file directly. Do not make native
			// file reads follow symlinks or create a second instruction asset.
			instructions, err = nativeReadFile(source)
			if err != nil {
				return err
			}
			canonicalLink = link
			instructionsProvided, foundSource = true, true
			if vendor == "copilot" {
				bindings.Core = true
			}
		} else if statErr == nil && vendor == "copilot" {
			rootInstructions, err = nativeReadFile(link)
			if err != nil {
				return err
			}
			rootRegular, foundSource = true, true
			if bindings.Core {
				instructions, instructionsProvided = rootInstructions, true
			}
		}
	}
	if vendor == "copilot" && scope == "project" && bindings.Core {
		p.Artifacts = append(p.Artifacts, nativeArtifact{Kind: "canonical-instructions", Source: "AGENTS.md"})
	}
	kinds := []string{"config", "instructions"}
	if vendor == "copilot" {
		kinds = append(kinds, "mcp", "lsp")
	} else {
		kinds = append(kinds, "hooks")
	}
	for _, kind := range kinds {
		path, format, e := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: kind})
		if e != nil {
			return e
		}
		if kind == "instructions" && path == canonicalLink {
			continue
		}
		if e = nativeNoSymlinks(path); e != nil {
			return e
		}
		data, e := nativeReadFile(path)
		if vendor == "copilot" && scope == "user" && kind == "config" {
			var legacyExcluded []string
			data, legacyExcluded, e = nativeReadCopilotUserConfig(base)
			excluded = append(excluded, legacyExcluded...)
		}
		if vendor == "copilot" && scope == "project" && kind == "mcp" {
			data, e = nativeReadCopilotProjectMCP(base)
		}
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		foundSource = true
		if format != "" {
			values, e := parseNative(data, format)
			if e != nil {
				return e
			}
			if vendor == "copilot" && kind == "mcp" {
				values = nativeCopilotMCPValues(values)
			}
			if vendor == "copilot" {
				if err := nativeCheckCopilotHookCredentials(values); err != nil {
					return err
				}
			}
			if vendor == "codex" {
				if err := nativeCheckCodexHookImport(values); err != nil {
					return err
				}
			}
			if kind == "config" {
				if err := nativeCheckPluginImport(vendor, values); err != nil {
					return err
				}
			}
			if err := nativeCheckRuntimeAuthentication(vendor, values); err != nil {
				return err
			}
			filtered, paths := nativeImportFilter(vendor, values, nil)
			values = filtered
			excluded = append(excluded, paths...)
			rebasedSkills := vendor == "codex" && kind == "config" && scope == "user" && nativeRebaseCodexSkillImport(values, base)
			rebasedTLS := vendor == "codex" && kind == "config" && scope == "user" && nativeRebaseCodexOtelImport(values, base)
			rebasedRoles := vendor == "codex" && kind == "config" && nativeRebaseCodexRoleImport(values, filepath.Dir(path), scope)
			if len(paths) > 0 || rebasedSkills || rebasedTLS || rebasedRoles {
				data, e = nativeEncode(values, format)
				if e != nil {
					return e
				}
			}

			if kind == "config" {
				selection := map[string]any{}
				owners, err := nativeExistingPluginSources(root, vendor, ns, scope, format)
				if err != nil {
					return err
				}
				routed := map[string]map[string]any{}
				for key, value := range values {
					if nativePluginRoot(vendor, key) {
						if owner := owners[key]; owner != "" {
							if owner == filepath.Join(root, "native", ns, filepath.Base(path)) {
								continue
							}
							if owner == filepath.Join(root, "plugins", ns, filepath.Base(path)) {
								selection[key] = value
								delete(values, key)
								continue
							}
							if routed[owner] == nil {
								routed[owner] = map[string]any{}
							}
							routed[owner][key] = value
							delete(values, key)
							continue
						}
						selection[key] = value
						delete(values, key)
					}
				}
				for owner, fields := range routed {
					data, err := nativeEncode(fields, format)
					if err != nil {
						return err
					}
					changes = append(changes, nativeChange{path: owner, data: data, mode: 0600})
				}
				if len(selection) > 0 {
					selectionData, err := nativeEncode(selection, format)
					if err != nil {
						return err
					}
					selectionProfile := p
					selectionProfile.Artifacts = []nativeArtifact{{Kind: "config", Source: filepath.Base(path)}}
					metadata, err := json.MarshalIndent(selectionProfile, "", "  ")
					if err != nil {
						return err
					}
					changes = append(changes, nativeChange{path: filepath.Join(root, "plugins", ns, filepath.Base(path)), data: selectionData, mode: 0600}, nativeChange{path: filepath.Join(root, "plugins", ns, "profile.json"), data: append(metadata, '\n'), mode: 0600})
					profiles = append(profiles, "plugins")
				}
				data, e = nativeEncode(values, format)
				if e != nil {
					return e
				}
			}

			servers := map[string]MCPServer{}
			if vendor == "codex" && kind == "config" || vendor == "copilot" && kind == "mcp" {
				servers, e = nativeExtractMCP(vendor, values)
			}
			if e != nil {
				return e
			}
			if len(servers) > 0 {
				portable, e := json.MarshalIndent(mcpDocument{Servers: servers}, "", "  ")
				if e != nil {
					return e
				}
				changes = append(changes, nativeChange{path: filepath.Join(root, "tools/mcp.json"), data: append(portable, '\n'), mode: 0600})
				profiles = append(profiles, "tools")
				data, e = nativeEncode(values, format)
				if e != nil {
					return e
				}
			}
		}
		if kind == "instructions" && scope == "project" {
			if vendor == "copilot" && bindings.Core {
				p.Artifacts = append(p.Artifacts, bindings.Native)
				changes = append(changes, nativeChange{path: filepath.Join(root, "native", ns, bindings.Native.Source), data: data, mode: 0600})
				continue
			}
			if instructionsProvided && !bytes.Equal(instructions, data) {
				return fmt.Errorf("native import refuses distinct project instructions in AGENTS.md and %s", path)
			}
			instructions = data
			instructionsProvided = true
			continue
		}
		source := filepath.Base(path)
		artifact := nativeArtifact{Kind: kind, Source: source}
		if scope == "user" && kind == "instructions" {
			artifact, err = nativeExistingUserInstructionArtifact(root, ns, artifact)
			if err != nil {
				return err
			}
			source = artifact.Source
		}
		p.Artifacts = append(p.Artifacts, artifact)
		changes = append(changes, nativeChange{path: filepath.Join(root, "native", ns, source), data: data, mode: 0600})
	}
	if vendor == "copilot" && scope == "project" {
		if rootRegular && !bindings.Core {
			if agentInstructionSources["AGENTS.md"] != "" || bytes.Contains(rootInstructions, []byte("@")) || instructionsProvided && !bytes.Equal(instructions, rootInstructions) {
				change, artifact, err := nativeImportAgentInstruction(root, "AGENTS.md", agentInstructionSources["AGENTS.md"], rootInstructions)
				if err != nil {
					return err
				}
				changes = append(changes, change)
				p.Artifacts = append(p.Artifacts, artifact)
			} else {
				instructions, instructionsProvided = rootInstructions, true
			}
		}
		for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md"} {
			data, err := nativeReadFile(filepath.Join(base, name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			change, artifact, err := nativeImportAgentInstruction(root, name, agentInstructionSources[name], data)
			if err != nil {
				return err
			}
			changes = append(changes, change)
			p.Artifacts = append(p.Artifacts, artifact)
			foundSource = true
		}
	}
	artifactChanges, artifacts, artifactExclusions, err := nativeImportArtifactFiles(vendor, scope, base, root, ns)
	if err != nil {
		return err
	}
	changes = append(changes, artifactChanges...)
	p.Artifacts = append(p.Artifacts, artifacts...)
	excluded = append(excluded, artifactExclusions...)
	foundSource = foundSource || len(artifacts) > 0
	if scope == "user" {
		assets, skipped, e := nativeImportSkills(filepath.Join(base, "skills"), filepath.Join(root, "skills"))
		if e != nil {
			return e
		}
		excluded = append(excluded, skipped...)
		if len(assets) > 0 {
			changes = append(changes, assets...)
			profiles = append(profiles, "skills")
			foundSource = true
		}
	} else if vendor == "copilot" {
		assets, sources, e := nativeImportCopilotProjectSkills(base, root)
		if e != nil {
			return e
		}
		if len(sources) > 0 {
			changes = append(changes, assets...)
			profiles = append(profiles, "skills")
			foundSource = true
			importedProjectSkills = sources
		}
	}
	if !foundSource {
		return fmt.Errorf("no recognized native configuration artifacts")
	}
	changes = append(changes, nativeChange{path: filepath.Join(root, "AGENTS.md"), data: instructions, mode: 0600})
	if len(excluded) > 0 || len(importedProjectSkills) > 0 {
		sort.Strings(excluded)
		details := map[string]any{"external_fields": excluded, "disposition": "not imported; source remains unchanged"}
		if len(importedProjectSkills) > 0 {
			imports := map[string]string{}
			for _, source := range importedProjectSkills {
				imports[source] = "skills/" + filepath.Base(source)
			}
			details["imported_project_skills"] = imports
		}
		var disabled []string
		if vendor == "codex" {
			for _, path := range excluded {
				if path == "otel.exporter" || path == "otel.trace_exporter" || path == "otel.metrics_exporter" {
					disabled = append(disabled, path)
				}
			}
		}
		if len(disabled) > 0 {
			details["disabled_exporters"] = disabled
			details["disabled_exporter_reason"] = "explicit none prevents unauthenticated telemetry and native default exporters after credential exclusion"
		}
		report, _ := json.MarshalIndent(details, "", "  ")
		changes = append(changes, nativeChange{path: filepath.Join(root, "native", ns, "import-report.json"), data: append(report, '\n'), mode: 0600})
	}
	profileData, _ := json.MarshalIndent(p, "", "  ")
	manifestData, _ := json.MarshalIndent(map[string]any{"version": NativeVersion, "profiles": profiles}, "", "  ")
	changes = append(changes, nativeChange{path: filepath.Join(root, "native", ns, "profile.json"), data: append(profileData, '\n'), mode: 0600}, nativeChange{path: filepath.Join(root, "manifest.json"), data: append(manifestData, '\n'), mode: 0600})
	if existing {
		changes, err = nativeMergeImport(root, changes, instructionsProvided)
		if err != nil {
			return err
		}
	}
	for i := range changes {
		if changes[i].before == nil {
			changes[i].before = &nativeSnapshot{}
		}
	}
	if options.Backup {
		build := nativeBuild{changes: changes}
		if err = nativePlanBackups(&build); err != nil {
			return err
		}
		changes = build.changes
	}
	return nativeLockedTransaction(changes, lock)
}

func nativeHasProfile(profiles []string, name string) bool {
	for _, p := range profiles {
		if p == name {
			return true
		}
	}
	return false
}

func nativeUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			if delim == '{' {
				seen := map[string]bool{}
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return fmt.Errorf("duplicate or invalid JSON object key")
					}
					seen[name] = true
					if err = visit(); err != nil {
						return err
					}
				}
			} else if delim == '[' {
				for decoder.More() {
					if err = visit(); err != nil {
						return err
					}
				}
			} else {
				return fmt.Errorf("invalid JSON delimiter")
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := visit(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func nativeCapabilities(vendor string) *NativePlan {
	p := &NativePlan{StandardVersion: NativeVersion, Platforms: []string{"linux"}, Scope: "project|user", Versions: []string{nativePinnedVersion(vendor)}, Features: []NativeFeature{}, RequiredActions: []string{"Use --experimental and a draft.2 manifest.", "User scope requires an absolute --native-home.", "Native discovery and execution are not verified by configuration writes."}}
	p.SettingRegistry = nativeSettingDeclarations(vendor)
	p.RequiredActions = append(p.RequiredActions, "Keep runtime credentials in native external stores or supported environment references. Import and projection refuse MCP, provider, or LSP definitions when filtering would remove authentication, including optional profiles and forced writes.")
	for _, scope := range []string{"project", "user"} {
		keys := []string{}
		for key := range nativeSettingRegistry[vendor][scope] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			disposition := "value-mapping"
			if nativeForbidden(key) {
				disposition = "external"
			} else if nativeSecurityKey(key) {
				disposition = "blocked"
			}
			path, _, _ := nativeTargetPath(vendor, scope, "<scope-root>", nativeArtifact{Kind: "config"})
			p.Features = append(p.Features, NativeFeature{Feature: key, Source: "native profile config", Destination: path, Scope: scope, Disposition: disposition, Activation: "requires value validation and native reload", Ownership: "setting", Authority: "registry target; native precedence still applies"})
			if vendor == "codex" && key == "hooks" {
				feature := nativeCodexHookFeature(NativeFeature{Feature: "artifact:config:/hooks", Source: "native_codex_hooks.go", Destination: path, Scope: scope, Ownership: "event array", Authority: "registry target; native hook trust remains external"})
				feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-hooks-inline-" + scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-mcp-hooks-final-inline-" + scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-background-final-inline-background-next-" + scope + ".json", "WORKBENCH/evidence/native-draft2-debug/codex-background-final-inline-spill-" + scope + ".json"}
				p.Features = append(p.Features, feature)
			}
			if vendor == "copilot" && key == "hooks" {
				feature := nativeCopilotHookFeature(NativeFeature{Feature: "artifact:config:/hooks", Source: "native_copilot_hooks.go", Destination: path, Scope: scope, Ownership: "event array", Authority: "registry target; native folder trust remains external"})
				feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/copilot-hooks-inline-" + scope + ".json"}
				p.Features = append(p.Features, feature)
			}
		}
		pluginTarget, _, _ := nativeTargetPath(vendor, scope, "<scope-root>", nativeArtifact{Kind: "config"})
		p.Features = append(p.Features, nativePluginCapabilities(vendor, scope, pluginTarget)...)
		if vendor == "codex" {
			p.Features = append(p.Features, nativeCodexRoleReferenceFeature(NativeFeature{Feature: "artifact:role-reference:/agents/<name>/config_file", Source: "native_codex_role_references.go", Destination: pluginTarget, Scope: scope, Disposition: "artifact-field-mapping", Activation: "requires referenced files and native reload", Ownership: "setting; referenced external files are not owned", Authority: "native discovery; no external file or authority writes"}))
			if scope == "project" {
				p.Features = append(p.Features, nativeCodexProjectScopeCapabilities(pluginTarget)...)
			}
			p.Features = append(p.Features, nativeCodexSkillCapabilities(scope, pluginTarget)...)
			p.Features = append(p.Features, nativeCodexOtelCapabilities(scope, pluginTarget)...)
			if scope == "user" {
				p.Features = append(p.Features, nativeCodexCommandAuthFeature(NativeFeature{Feature: "artifact:config:/model_providers/<name>/auth", Source: "native_auth_units.go", Destination: pluginTarget, Scope: scope, Disposition: "value-mapping", Activation: "requires complete token-command validation and native reload", Ownership: "setting", Authority: "native token command and returned credentials remain external"}))
			}
		}
		if vendor == "copilot" {
			p.Features = append(p.Features, nativeCopilotPreferenceCapabilities(scope, pluginTarget)...)
			p.Features = append(p.Features, nativeCopilotSubagentCapabilities(scope, pluginTarget)...)
			if scope == "user" {
				feature := nativeCopilotUserInstructionFeature(NativeFeature{
					Feature: "artifact:instructions", Source: "native profile artifact", Destination: "<native-home>/copilot-instructions.md", Scope: scope,
					Activation: "import then project; start a new native session", Ownership: "file", Authority: "explicit native home; no trust or account writes",
				})
				p.Features = append(p.Features, feature)
				feature.Feature = "artifact:instruction-discovery:/$HOME/.copilot/copilot-instructions.md"
				p.Features = append(p.Features, feature)
			}
			skillDestination := ".agents/skills/<name>/SKILL.md"
			if scope == "user" {
				skillDestination = "<native-home>/skills/<name>/SKILL.md"
			}
			p.Features = append(p.Features, nativeCopilotSkillCapabilities(scope, skillDestination)...)
			p.Features = append(p.Features, nativeCopilotInstructionDiscoveryCapability(scope))
			if scope == "project" {
				p.Features = append(p.Features, nativeCopilotProjectSkillImportCapabilities()...)
				p.Features = append(p.Features, nativeCanonicalInstructionFeature(NativeFeature{
					Feature: "artifact:canonical-instructions", Source: ".agents/AGENTS.md", Destination: "<project-root>/AGENTS.md",
					Scope: scope, Activation: "fixed core binding; start a new native session", Ownership: "file, except existing verified canonical link",
					Authority: "fixed canonical source and registered project root target",
				}))
				for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md"} {
					feature := nativeAgentInstructionFeature(NativeFeature{
						Feature: "artifact:agent-instructions:/" + name, Source: name,
						Destination: "<project-root>/" + name, Scope: scope,
						Activation: "import then project; start a new native session", Ownership: "file", Authority: "fixed registry project path",
					})
					p.Features = append(p.Features, feature)
					if name != "AGENTS.md" {
						feature.Feature = "artifact:instruction-discovery:/" + name
						p.Features = append(p.Features, feature)
					}
				}
				p.Features = append(p.Features, NativeFeature{
					Feature: "artifact:instruction-discovery:/AGENTS.md", Source: "AGENTS.md", Destination: ".agents/AGENTS.md",
					Scope: scope, Disposition: "portable-mapping", NativeStatus: "bounded-root-instruction-loading",
					Activation: "import then project; start a new native session", Ownership: "canonical file; source remains unchanged",
					Authority:  "supplied project root only",
					Evidence:   []string{"docs/COPILOT_ROOT_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_root_instructions.py", "docs/COPILOT_CANONICAL_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_canonical_instructions.py"},
					Limitation: "Imports plain root AGENTS.md into the portable core. Native agent-instructions artifacts preserve root @ references and distinct root and Copilot instruction bodies at their original paths. Projection refuses stale regular root instructions that differ from the canonical body. Projection uses .github/copilot-instructions.md unless a fixed canonical-instructions binding or verified canonical compatibility link selects the root path. Referenced project files remain external. Nested discovery and live reload are not covered.",
				})
			}
		}
		for _, artifact := range []nativeArtifact{{Kind: "mcp"}, {Kind: "lsp"}, {Kind: "agent", Name: "<name>.toml"}, {Kind: "scoped-instructions", Name: "<name>.instructions.md"}, {Kind: "hooks", Name: "<name>.json"}} {
			// Resolve a safe sample through the same target registry used by apply.
			if vendor == "copilot" && artifact.Kind == "agent" {
				artifact.Name = "<name>.agent.md"
			}
			sample := artifact
			sample.Name = strings.ReplaceAll(sample.Name, "<name>", "fixture")
			path, format, err := nativeTargetPath(vendor, scope, "<scope-root>", sample)
			if err != nil {
				continue
			}
			path = strings.ReplaceAll(path, "fixture", "<name>")
			feature := NativeFeature{Feature: "artifact:" + artifact.Kind, Source: "native profile artifact", Destination: path, Scope: scope, Disposition: "import-only", Activation: "inactive", Ownership: "file", Authority: "registry target", NativeStatus: "unverified", Limitation: "Recognized import path; activation requires an artifact field mapping."}
			if format != "" {
				feature.Ownership = "setting"
			}
			if artifact.Kind == "lsp" {
				feature.Disposition, feature.Activation = "artifact-mapping", "requires field validation and native reload"
				feature = nativeLSPFeature(feature)
				fields := make([]string, 0, len(nativeLSPFields))
				for field := range nativeLSPFields {
					fields = append(fields, field)
				}
				sort.Strings(fields)
				for _, field := range fields {
					child := feature
					child.Feature = "artifact:lsp:/lspServers/<name>/" + field
					child.Source = "native_lsp.go: " + nativeLSPFields[field]

					p.Features = append(p.Features, child)
				}
			}
			if artifact.Kind == "mcp" && vendor == "copilot" {
				feature = nativeCopilotMCPFeature(feature)
				p.Features = append(p.Features, nativeCopilotMCPFields(feature)...)
			}
			if artifact.Kind == "hooks" && vendor == "copilot" {
				feature = nativeCopilotHookFeature(feature)
				p.Features = append(p.Features, nativeCopilotHookFieldFeatures(feature)...)
			}
			if artifact.Kind == "hooks" && vendor == "codex" {
				feature = nativeCodexHookFeature(feature)
				p.Features = append(p.Features, nativeCodexHookFieldFeatures(feature)...)
			}
			if artifact.Kind == "scoped-instructions" {
				feature.Disposition, feature.Activation = "artifact-mapping", "pending native reload"
				feature = nativeCopilotRecursiveInstructionFeature(feature)
			}
			if vendor == "copilot" && artifact.Kind == "agent" {
				feature = nativeCopilotAgentFeature(feature)
				p.Features = append(p.Features, nativeCopilotAgentFields(feature)...)
			}
			if vendor == "codex" && artifact.Kind == "agent" {
				feature.Disposition, feature.Activation = "artifact-mapping", "requires field validation and native reload"
				feature = nativeCodexRoleFeature(feature)
			}
			p.Features = append(p.Features, feature)
		}
	}
	return p
}

func nativePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}
func nativeFlatten(values map[string]any) map[string]any {
	out := map[string]any{}
	var visit func(string, any)
	visit = func(path string, value any) {
		if m, ok := value.(map[string]any); ok && len(m) > 0 {
			for key, v := range m {
				visit(path+"/"+nativePointer(key), v)
			}
		} else {
			out[path] = value
		}
	}
	for key, value := range values {
		visit("/"+nativePointer(key), value)
	}
	return out
}
func nativeUnflatten(values map[string]any) map[string]any {
	out := map[string]any{}
	for pointer, value := range values {
		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		current := out
		for i, part := range parts {
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			if i == len(parts)-1 {
				current[key] = value
				break
			}
			next, ok := current[key].(map[string]any)
			if !ok {
				next = map[string]any{}
				current[key] = next
			}
			current = next
		}
	}
	return out
}

// Move only lossless, established MCP fields into the portable core. Keep all
// remaining server fields in their native namespace.
func nativeExtractMCP(vendor string, values map[string]any) (map[string]MCPServer, error) {
	if vendor == "copilot" {
		return nativeExtractCopilotMCP(values)
	}
	key := "mcp_servers"
	if vendor == "copilot" {
		key = "mcpServers"
	}
	raw, ok := values[key]
	if !ok {
		return nil, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("MCP servers must be an object")
	}
	portable := map[string]MCPServer{}
	remaining := map[string]any{}
	for name, item := range servers {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("MCP server must be an object")
		}
		core := map[string]any{}
		extra := map[string]any{}
		for field, value := range fields {
			switch field {
			case "command", "args", "url":
				core[field] = value
			case "type":
				if vendor == "copilot" {
					core[field] = value
				} else {
					extra[field] = value
				}
			case "env_vars":
				if vendor == "codex" {
					vars, ok := value.([]any)
					if !ok {
						return nil, fmt.Errorf("env_vars must be an array")
					}
					env := map[string]string{}
					for _, v := range vars {
						variable, ok := v.(string)
						if !ok || !isEnvironmentName(variable) {
							return nil, fmt.Errorf("invalid MCP environment reference")
						}
						env[variable] = "urn:open-dot-agents:env:" + variable
					}
					core["env"] = env
				} else {
					extra[field] = value
				}
			default:
				extra[field] = value
			}
		}
		if _, hasCommand := core["command"]; !hasCommand {
			if _, hasURL := core["url"]; !hasURL {
				remaining[name] = fields
				continue
			}
		}
		data, err := json.Marshal(core)
		if err != nil {
			return nil, err
		}
		var server MCPServer
		if err = json.Unmarshal(data, &server); err != nil {
			return nil, err
		}
		portable[name] = server
		if len(extra) > 0 {
			remaining[name] = extra
		}
	}
	if len(portable) == 0 {
		return nil, nil
	}
	portable, err := canonicalizeVendorServers(portable)
	if err != nil {
		return nil, err
	}
	if len(remaining) == 0 {
		delete(values, key)
	} else {
		values[key] = remaining
	}
	return portable, nil
}

func nativeImportFilter(vendor string, values map[string]any, prefix []string) (map[string]any, []string) {
	out := map[string]any{}
	var excluded []string
	for key, value := range values {
		path := append(append([]string(nil), prefix...), key)
		if vendor == "codex" {
			if disposition, _ := nativeCodexOtelExporterProblem(path, value); disposition == "external" {
				out[key] = "none"
				excluded = append(excluded, strings.Join(path, "."))
				continue
			}
		}
		if nativeFieldExcluded(vendor, path, value) {
			excluded = append(excluded, strings.Join(path, "."))
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			filtered, paths := nativeImportFilter(vendor, nested, path)
			excluded = append(excluded, paths...)
			if vendor == "codex" && path[0] == "marketplaces" && len(nested) > 0 && len(filtered) == 0 {
				continue
			}
			out[key] = filtered
		} else if nativeContainsExcluded(vendor, path, value) {
			excluded = append(excluded, strings.Join(path, "."))
		} else {
			out[key] = value
		}
	}
	return out, excluded
}
