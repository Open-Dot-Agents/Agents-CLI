package config

// This file implements an experimental policy contract. It does not enforce
// policy in a native process. Activation is refused until an adapter can prove
// the complete requested scope and configuration authority.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const ExperimentalVersion = "1.1.0-draft.1"

type Extension struct {
	Required *bool                      `json:"required"`
	Data     map[string]json.RawMessage `json:"data"`
}
type PermissionRule struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Args   []string `json:"args,omitempty"`
	Effect string   `json:"effect"`
}
type PermissionsPolicy struct {
	Version    string               `json:"version"`
	Coverage   []string             `json:"coverage"`
	Default    string               `json:"default"`
	Rules      []PermissionRule     `json:"rules"`
	Extensions map[string]Extension `json:"extensions,omitempty"`
}
type PathRule struct {
	Path   string `json:"path"`
	Access string `json:"access"`
}
type NetworkRule struct {
	Host   string `json:"host"`
	Ports  []int  `json:"ports"`
	Effect string `json:"effect"`
}
type SandboxPolicy struct {
	Version    string   `json:"version"`
	Coverage   []string `json:"coverage"`
	Filesystem struct {
		Default string     `json:"default"`
		Rules   []PathRule `json:"rules"`
		Runtime []string   `json:"runtime,omitempty"`
	} `json:"filesystem"`
	Network struct {
		Default   string        `json:"default"`
		Rules     []NetworkRule `json:"rules"`
		Local     string        `json:"local"`
		Private   string        `json:"private"`
		Web       string        `json:"web"`
		RemoteMCP string        `json:"remoteMCP"`
	} `json:"network"`
	Credentials struct {
		Environment string   `json:"environment"`
		Allow       []string `json:"allow"`
		Files       string   `json:"files"`
	} `json:"credentials"`
	Extensions map[string]Extension `json:"extensions,omitempty"`
}

type SecurityPolicy struct {
	Permissions *PermissionsPolicy `json:"permissions,omitempty"`
	Sandbox     *SandboxPolicy     `json:"sandbox,omitempty"`
}

type SecurityPlan struct {
	NativeEnvironmentMode string            `json:"native_environment_mode,omitempty"`
	NativeInvocation      []string          `json:"native_invocation,omitempty"`
	NativeEnvironment     map[string]string `json:"native_environment,omitempty"`
	Authority             map[string]string `json:"authority,omitempty"`
	Coverage              map[string]string `json:"coverage,omitempty"`
	StandardVersion       string            `json:"standard_version"`
	Status                string            `json:"status"`
	Declared              SecurityPolicy    `json:"declared"`
	Normalized            SecurityPolicy    `json:"normalized"`
	ProjectedSettings     map[string]any    `json:"projected_settings"`
	AutomaticGrants       []string          `json:"automatic_grants"`
	UnresolvedControls    []string          `json:"unresolved_controls"`
	EvidenceScope         string            `json:"evidence_scope"`
}

var policyPath = regexp.MustCompile(`^(\.|\.?[A-Za-z0-9_@+-][A-Za-z0-9_.@+-]*(/\.?[A-Za-z0-9_@+-][A-Za-z0-9_.@+-]*)*)$`)
var extensionName = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
var policyName = regexp.MustCompile(`^[^\s\x00-\x1f]+$`)
var policyHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func decision(value string) bool { return value == "deny" || value == "ask" || value == "allow" }
func access(value string) bool   { return value == "deny" || value == "read" || value == "write" }

// Reject duplicate keys and null values before typed decoding. Different JSON
// parsers must not activate different policies from the same document.
func uniqueJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func() error
	value = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		if token == nil {
			return errors.New("null is not allowed in a draft policy")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return errors.New("invalid JSON key")
				}
				if seen[name] {
					return fmt.Errorf("duplicate JSON key %q", name)
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("expected one JSON document")
	}
	return nil
}
func decodePolicy(path string, target any) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	if err := requireRegularFile(path, "draft policy"); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := uniqueJSON(data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := exactPolicyFields(data, reflect.TypeOf(target)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
func validatePolicyCommon(version string, coverage []string, extensions map[string]Extension) error {
	if version != ExperimentalVersion && version != NativeVersion {
		return fmt.Errorf("policy version must be %s", ExperimentalVersion)
	}
	known := map[string]bool{"builtin-tools": true, "shell": true, "hooks": true, "mcp-local": true, "mcp-remote": true, "lsp": true, "delegation": true}
	if len(coverage) == 0 {
		return errors.New("coverage must contain at least one scope")
	}
	seen := map[string]bool{}
	for _, scope := range coverage {
		if !known[scope] || seen[scope] {
			return fmt.Errorf("invalid or duplicate coverage %q", scope)
		}
		seen[scope] = true
	}
	for name, extension := range extensions {
		if !extensionName.MatchString(name) || extension.Required == nil || extension.Data == nil {
			return fmt.Errorf("invalid extension %q: use a namespace, required boolean, and data object", name)
		}
	}
	return nil
}
func readSecurityPolicy(root string, selected map[string]bool) (SecurityPolicy, error) {
	var policy SecurityPolicy
	expectedVersion := ExperimentalVersion
	var manifest manifestDocument
	if decodePolicy(filepath.Join(root, "manifest.json"), &manifest) == nil && manifest.Version == NativeVersion {
		expectedVersion = NativeVersion
	}
	if selected["permissions"] {
		p := new(PermissionsPolicy)
		if err := decodePolicy(filepath.Join(root, "permissions", "permissions.json"), p); err != nil {
			return policy, err
		}
		if p.Version != expectedVersion {
			return policy, fmt.Errorf("policy version must match manifest version %s", expectedVersion)
		}
		if err := validatePolicyCommon(p.Version, p.Coverage, p.Extensions); err != nil {
			return policy, err
		}
		if !decision(p.Default) || p.Rules == nil {
			return policy, errors.New("permissions require a default decision and rules array")
		}
		for _, rule := range p.Rules {
			if !decision(rule.Effect) || (!policyName.MatchString(rule.Name) || strings.IndexFunc(rule.Name, unicode.IsSpace) >= 0) || (rule.Kind != "tool" && rule.Kind != "process") {
				return policy, errors.New("invalid permission rule")
			}
			if (rule.Kind == "process" && rule.Args == nil) || (rule.Kind == "tool" && rule.Args != nil) {
				return policy, errors.New("only process rules require args")
			}
			for _, arg := range rule.Args {
				if strings.ContainsRune(arg, 0) {
					return policy, errors.New("NUL is not allowed in args")
				}
			}
		}
		policy.Permissions = p
	}
	if selected["sandbox"] {
		p := new(SandboxPolicy)
		if err := decodePolicy(filepath.Join(root, "sandbox", "sandbox.json"), p); err != nil {
			return policy, err
		}
		if p.Version != expectedVersion {
			return policy, fmt.Errorf("policy version must match manifest version %s", expectedVersion)
		}
		if err := validatePolicyCommon(p.Version, p.Coverage, p.Extensions); err != nil {
			return policy, err
		}
		if !access(p.Filesystem.Default) || p.Filesystem.Rules == nil {
			return policy, errors.New("filesystem requires a default access and rules array")
		}
		if len(p.Filesystem.Runtime) > 1 || (len(p.Filesystem.Runtime) == 1 && p.Filesystem.Runtime[0] != "process") {
			return policy, errors.New("filesystem.runtime accepts only one process grant")
		}
		for _, rule := range p.Filesystem.Rules {
			if !policyPath.MatchString(rule.Path) || !access(rule.Access) {
				return policy, fmt.Errorf("invalid filesystem rule %q", rule.Path)
			}
		}
		for _, effect := range []string{p.Network.Default, p.Network.Local, p.Network.Private, p.Network.Web, p.Network.RemoteMCP} {
			if !decision(effect) {
				return policy, errors.New("network requires default, local, private, web, and remoteMCP decisions")
			}
		}
		if p.Network.Rules == nil {
			return policy, errors.New("network rules must be an array")
		}
		for _, rule := range p.Network.Rules {
			if !policyHost.MatchString(rule.Host) || len(rule.Ports) == 0 || !decision(rule.Effect) {
				return policy, errors.New("invalid network rule")
			}
			seen := map[int]bool{}
			for _, port := range rule.Ports {
				if port < 1 || port > 65535 || seen[port] {
					return policy, errors.New("invalid or duplicate network port")
				}
				seen[port] = true
			}
		}
		if (p.Credentials.Environment != "none" && p.Credentials.Environment != "inherit") || (p.Credentials.Files != "deny" && p.Credentials.Files != "inherit") || p.Credentials.Allow == nil {
			return policy, errors.New("credentials require environment, allow, and files")
		}
		seen := map[string]bool{}
		for _, name := range p.Credentials.Allow {
			if !isEnvironmentName(name) || seen[name] {
				return policy, errors.New("invalid or duplicate credential environment name")
			}
			seen[name] = true
		}
		policy.Sandbox = p
	}
	return policy, nil
}

func draftManifest(root string) (bool, error) {
	path := filepath.Join(root, "manifest.json")
	if err := rejectSymlinkPath(path); err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	}
	if err := requireRegularFile(path, "canonical manifest"); err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var m manifestDocument
	if err := json.Unmarshal(data, &m); err != nil {
		return false, err
	}
	if m.Version != ExperimentalVersion && m.Version != NativeVersion {
		return false, nil
	}
	if err := decodePolicy(path, &m); err != nil {
		return true, err
	}
	return true, nil
}
func experimentalGate(root string, experimental bool) error {
	draft, err := draftManifest(root)
	if err != nil {
		return err
	}
	if draft && !experimental {
		return errors.New("ODA-SECURITY-0001: draft policy requires --experimental")
	}
	return nil
}

func ValidateRepositoryWithOptions(root string, experimental bool) error {
	if err := requireRegularFile(filepath.Join(root, "manifest.json"), "canonical manifest"); err != nil {
		return err
	}
	return validateWithOptions(root, experimental)
}

// normalizeSecurityPolicy keeps all overlapping restrictions. It only combines
// identical permission selectors; deny > ask > allow. It does not resolve paths
// or execute native configuration.
func normalizeSecurityPolicy(policy SecurityPolicy) SecurityPolicy {
	data, _ := json.Marshal(policy)
	var normalized SecurityPolicy
	_ = json.Unmarshal(data, &normalized)
	if p := normalized.Permissions; p != nil {
		sort.Strings(p.Coverage)
		rules := map[string]PermissionRule{}
		for _, rule := range p.Rules {
			keyData, _ := json.Marshal([]any{rule.Kind, rule.Name, rule.Args})
			key := string(keyData)
			old, ok := rules[key]
			if !ok || decisionRank(rule.Effect) > decisionRank(old.Effect) {
				rules[key] = rule
			}
		}
		keys := make([]string, 0, len(rules))
		for key := range rules {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		p.Rules = []PermissionRule{}
		for _, key := range keys {
			p.Rules = append(p.Rules, rules[key])
		}
	}
	if p := normalized.Sandbox; p != nil {
		sort.Strings(p.Coverage)
		sort.Strings(p.Credentials.Allow)
		sort.SliceStable(p.Filesystem.Rules, func(i, j int) bool {
			a, b := p.Filesystem.Rules[i], p.Filesystem.Rules[j]
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			return a.Access < b.Access
		})
		for i := range p.Network.Rules {
			sort.Ints(p.Network.Rules[i].Ports)
		}
		sort.SliceStable(p.Network.Rules, func(i, j int) bool {
			a, _ := json.Marshal(p.Network.Rules[i])
			b, _ := json.Marshal(p.Network.Rules[j])
			return string(a) < string(b)
		})
	}
	return normalized
}
func decisionRank(value string) int {
	switch value {
	case "deny":
		return 3
	case "ask":
		return 2
	case "allow":
		return 1
	}
	return 0
}

func securityPreflight(vendor, root string, selected map[string]bool) (*SecurityPlan, []string, error) {
	policy, err := readSecurityPolicy(root, selected)
	if err != nil {
		return nil, nil, err
	}
	if policy.Permissions == nil && policy.Sandbox == nil {
		return nil, nil, nil
	}
	plan := &SecurityPlan{StandardVersion: ExperimentalVersion, Status: "refused", Declared: policy, Normalized: normalizeSecurityPolicy(policy), ProjectedSettings: map[string]any{}, AutomaticGrants: []string{}, EvidenceScope: "policy validation and adapter refusal only; native enforcement is not verified"}
	plan.UnresolvedControls = []string{
		"native version and host prerequisites have no verified security adapter range",
		"effective user, project, command-line, environment, and administrator authority is not attested",
		"runtime filesystem grants and credential exposure are unknown",
		"requested tool, subprocess, hook, MCP, LSP, and delegation coverage is not verified",
	}
	if vendor == "codex" {
		plan.UnresolvedControls = append(plan.UnresolvedControls, "legacy sandbox settings can supersede permission profiles; domain restrictions require an active native proxy")
	}
	if vendor == "copilot" {
		plan.UnresolvedControls = append(plan.UnresolvedControls, "native permission flags and bypass environment can change policy; a cooperative proxy is not universal process network enforcement")
	}
	diagnostics := []string{"ODA-SECURITY-0002: " + vendor + " requested security policy has no verified native mapping in this context; no files will be changed"}
	extensionSets := []map[string]Extension{}
	if policy.Permissions != nil {
		extensionSets = append(extensionSets, policy.Permissions.Extensions)
	}
	if policy.Sandbox != nil {
		extensionSets = append(extensionSets, policy.Sandbox.Extensions)
	}
	for _, set := range extensionSets {
		for name, ext := range set {
			if *ext.Required {
				diagnostics = append(diagnostics, "ODA-SECURITY-0003: required extension "+name+" is unknown; activation refused")
			} else {
				diagnostics = append(diagnostics, "ODA-SECURITY-0005: optional extension "+name+" is preserved and inactive")
			}
		}
	}
	sort.Strings(diagnostics)
	return plan, diagnostics, nil
}

func guardSecurityImport(root string, experimental bool) error {
	if experimental {
		return errors.New("ODA-SECURITY-0004: native security import is not implemented; no policy or credential stores will be read or changed")
	}
	draft, err := draftManifest(root)
	if err != nil {
		return err
	}
	if draft {
		return errors.New("ODA-SECURITY-0004: import cannot replace a draft security manifest, even with --force")
	}
	return nil
}

func VendorCapabilitiesWithOptions(vendor string, experimental bool) (Capabilities, error) {
	result, err := VendorCapabilities(vendor)
	if err != nil || !experimental {
		return result, err
	}
	if result.Vendor == "codex" || result.Vendor == "copilot" {
		result.Native = nativeCapabilities(result.Vendor)
	}
	result.Experimental = &ExperimentalCapabilities{StandardVersion: ExperimentalVersion, Profiles: []string{"permissions", "sandbox"}, Status: "refused", EvidenceScope: "validation and refusal only", Paths: map[string]string{"permissions": ".agents/permissions/permissions.json", "sandbox": ".agents/sandbox/sandbox.json"}}
	if result.Vendor == "codex" {
		result.Experimental.Status = "subset-available"
		result.Experimental.EvidenceScope = "Codex 0.154.0 Linux amd64 direct sandbox shell subset; full adapter support remains unverified"
		result.Experimental.Subset = map[string]string{"invocation": "codex sandbox", "coverage": "shell", "filesystem": "default read; explicit process runtime grant", "network": "deny all subprocess connections", "credentials": "inherit only", "authority": "explicit configuration-only native home; pre-existing trust", "binary_sha256": codexSecurityBinarySHA}
	}
	return result, nil
}

type ExperimentalCapabilities struct {
	Subset          map[string]string `json:"subset,omitempty"`
	StandardVersion string            `json:"standard_version"`
	Profiles        []string          `json:"profiles"`
	Status          string            `json:"status"`
	EvidenceScope   string            `json:"evidence_scope"`
	Paths           map[string]string `json:"paths"`
}

// MarshalJSON preserves an explicit empty process argument vector.
func (r PermissionRule) MarshalJSON() ([]byte, error) {
	type rule PermissionRule
	if r.Kind == "process" {
		return json.Marshal(struct {
			Kind   string   `json:"kind"`
			Name   string   `json:"name"`
			Args   []string `json:"args"`
			Effect string   `json:"effect"`
		}{r.Kind, r.Name, r.Args, r.Effect})
	}
	return json.Marshal(rule(r))
}

// permissionDecision is a reference evaluator, not a native enforcement layer.
// A process rule matches the complete argument vector, never a shell prefix.
func permissionDecision(p PermissionsPolicy, kind, name string, args []string, approvalChannel bool) string {
	result := ""
	for _, rule := range p.Rules {
		if rule.Kind != kind || (rule.Name != name && rule.Name != "*") {
			continue
		}
		if kind == "process" {
			if len(rule.Args) != len(args) {
				continue
			}
			match := true
			for i := range args {
				if args[i] != rule.Args[i] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		if decisionRank(rule.Effect) > decisionRank(result) {
			result = rule.Effect
		}
	}
	if result == "" {
		result = p.Default
	}
	if result == "ask" && !approvalChannel {
		return "deny"
	}
	return result
}

// filesystemAccess evaluates canonical workspace-relative paths. An adapter
// must also check the resolved target and prevent symlink and mount races.
func filesystemAccess(p SandboxPolicy, path string) (string, error) {
	if !policyPath.MatchString(path) {
		return "", errors.New("path is not a canonical workspace path")
	}
	rank := map[string]int{"deny": 3, "read": 2, "write": 1}
	result := ""
	for _, rule := range p.Filesystem.Rules {
		if rule.Path == "." || path == rule.Path || strings.HasPrefix(path, rule.Path+"/") {
			if rank[rule.Access] > rank[result] {
				result = rule.Access
			}
		}
	}
	if result == "" {
		result = p.Filesystem.Default
	}
	return result, nil
}

// ValidationStandardVersion identifies the validator contract for result output.
// Inspect only a safe regular manifest; never follow a link to infer metadata.
func ValidationStandardVersion(root string, experimental bool) string {
	if experimental {
		draft, _ := draftManifest(root)
		if draft {
			var m manifestDocument
			if decodePolicy(filepath.Join(root, "manifest.json"), &m) == nil {
				return m.Version
			}
			return ExperimentalVersion
		}
	}
	return manifestVersion
}

// JSON Schema integer values can use a decimal or exponent spelling.
func (r *NetworkRule) UnmarshalJSON(data []byte) error {
	var raw struct {
		Host   string            `json:"host"`
		Ports  []json.RawMessage `json:"ports"`
		Effect string            `json:"effect"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	r.Host, r.Effect = raw.Host, raw.Effect
	if raw.Ports != nil {
		r.Ports = make([]int, 0, len(raw.Ports))
	}
	for _, number := range raw.Ports {
		value, err := strconv.ParseFloat(strings.TrimSpace(string(number)), 64)
		if err != nil || value < 1 || value > 65535 || math.Trunc(value) != value {
			return errors.New("port must be an integer from 1 to 65535")
		}
		r.Ports = append(r.Ports, int(value))
	}
	return nil
}

// encoding/json accepts case-insensitive field names. Policy schemas do not.
func exactPolicyFields(data []byte, typ reflect.Type) error {
	if typ == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	if typ.Kind() == reflect.Pointer {
		return exactPolicyFields(data, typ.Elem())
	}
	switch typ.Kind() {
	case reflect.Struct, reflect.Map:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		allowed := map[string]reflect.Type{}
		if typ.Kind() == reflect.Struct {
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				key := strings.Split(field.Tag.Get("json"), ",")[0]
				if key != "" && key != "-" {
					allowed[key] = field.Type
				}
			}
		}
		for key, value := range fields {
			var child reflect.Type
			if typ.Kind() == reflect.Map {
				child = typ.Elem()
			} else {
				var ok bool
				child, ok = allowed[key]
				if !ok {
					return fmt.Errorf("unknown policy field %q", key)
				}
			}
			if err := exactPolicyFields(value, child); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactPolicyFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
