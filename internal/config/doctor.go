package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DoctorCheck describes an inspection result, never a runtime permission grant.
type DoctorCheck struct {
	ID       string     `json:"id"`
	Layer    string     `json:"layer"`
	Status   string     `json:"status"`
	Message  string     `json:"message"`
	Paths    []string   `json:"paths,omitempty"`
	Commands [][]string `json:"commands,omitempty"`
	NextStep string     `json:"next_step,omitempty"`
}

// DoctorResult has its own version; it does not change the canonical contract.
type DoctorResult struct {
	SchemaVersion      string             `json:"schema_version"`
	Vendor             string             `json:"vendor"`
	Root               string             `json:"root"`
	Scope              string             `json:"scope"`
	Ready              bool               `json:"ready"`
	ConfigurationState string             `json:"configuration_state"`
	RequestedPolicy    *DevelopmentPolicy `json:"requested_policy,omitempty"`
	Checks             []DoctorCheck      `json:"checks"`
}

func (r *DoctorResult) check(id, layer, status, message string, paths []string, commands ...[]string) {
	r.Checks = append(r.Checks, DoctorCheck{ID: id, Layer: layer, Status: status, Message: message, Paths: paths, Commands: commands})
	if status == "action-required" {
		r.Ready = false
	}
}

func doctorCommand(command, root, vendor string, extra ...string) []string {
	args := []string{"agents", command, "--experimental", "--vendor", vendor, "--root", root}
	if vendor == "codex" {
		args = append(args, "--preset", "development")
	}
	return append(args, extra...)
}

// DoctorDevelopment only reads configuration and process mount metadata. It
// does not run the native binary, create a test file, or acquire a write lock.
func DoctorDevelopment(vendor, root string) (DoctorResult, error) {
	return doctorDevelopment(vendor, root, loadDoctorMounts)
}

func doctorDevelopment(vendor, root string, mounts func() ([]doctorMount, error)) (DoctorResult, error) {
	if vendor != "codex" && vendor != "copilot" {
		return DoctorResult{}, errors.New("doctor requires --vendor codex or copilot")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return DoctorResult{}, errors.New("cannot resolve the project root")
	}
	r := DoctorResult{SchemaVersion: "1.0.0", Vendor: vendor, Root: root, Scope: "development-only",
		Ready: true, ConfigurationState: "unknown", Checks: []DoctorCheck{}}
	if vendor == "copilot" {
		r.Scope = "full-project"
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		r.check("project.root", "configuration", "action-required", "The project root is not an accessible directory.", []string{root})
	} else {
		doctorConfiguration(&r)
		doctorFilesystem(&r, mounts)
	}
	doctorExecutable(&r)
	r.check("session.authority", "active-session", "unknown",
		"Active app, session, administrator, and tool permissions are not verified. Matching files do not prove runtime access.", nil)
	r.Checks[len(r.Checks)-1].NextStep = "After a reviewed apply, start a new trusted native session. If access is still blocked, inspect that app or session's permissions; project policy cannot remove host restrictions."
	return r, nil
}

func doctorConfiguration(r *DoctorResult) {
	canonical := filepath.Join(r.Root, ".agents")
	manifestPath := filepath.Join(canonical, "manifest.json")
	policyPath := filepath.Join(canonical, developmentPath)
	guardrails := filepath.Join(canonical, developmentGuardrailsPath)
	registryPath := filepath.Join(canonical, "state/native-"+r.Vendor+".json")
	planCommand := doctorCommand("plan", r.Root, r.Vendor)
	applyCommand := doctorCommand("apply", r.Root, r.Vendor)
	initCommand := []string{"agents", "init", "--preset", "development", "--experimental", "--root", r.Root}
	if err := rejectSymlinkPath(canonical); err != nil {
		r.ConfigurationState = "invalid"
		r.check("setup.path", "configuration", "action-required", "The canonical path contains a link or cannot be inspected safely.", []string{canonical})
		return
	}
	if _, err := os.Lstat(canonical); os.IsNotExist(err) {
		r.ConfigurationState = "missing"
		r.check("setup.missing", "requested-policy", "action-required", "The development preset is not initialized.", []string{canonical}, initCommand)
		return
	}
	var manifest manifestDocument
	if err := decodePolicy(manifestPath, &manifest); err != nil {
		r.ConfigurationState = "invalid"
		r.check("setup.manifest", "configuration", "action-required", "The canonical manifest is missing, invalid, or unsafe to read.", []string{manifestPath})
		return
	}
	selected := map[string]bool{}
	for _, name := range manifest.Profiles {
		selected[name] = true
	}
	security, err := readSecurityPolicy(canonical, selected)
	if err != nil {
		r.ConfigurationState = "invalid"
		r.check("policy.invalid", "requested-policy", "action-required", "The selected policy or its guardrails are missing, invalid, or unsafe to inspect.", []string{manifestPath, policyPath, guardrails})
		return
	}
	if security.Development != nil {
		copy := *security.Development
		if copy.Enforcement == "" {
			copy.Enforcement = "strict"
		}
		r.RequestedPolicy = &copy
		if copy.Enforcement == "strict" || security.Sandbox != nil || len(security.Permissions.Extensions) != 1 {
			r.ConfigurationState = "refused"
			r.check("policy.refused", "requested-policy", "action-required", "The selected security contract has no equivalent development mapping. Activation remains refused.", []string{policyPath})
			return
		}
		// Planning can read selected assets anywhere in these configuration
		// trees. Refuse overlapping protected paths before inspecting assets.
		for _, protected := range copy.ProtectedPaths {
			protected = filepath.FromSlash(protected)
			for _, directory := range []string{".agents", ".codex", ".github", "AGENTS.md"} {
				if doctorContains(protected, directory) || doctorContains(directory, protected) {
					r.ConfigurationState = "unknown"
					r.check("inspection.protected", "configuration", "action-required", "A protected path overlaps configuration needed for planning. Its contents were not inspected.", []string{filepath.Join(r.Root, protected)})
					return
				}
			}
		}
		r.check("policy.selected", "requested-policy", "ok", "The practical development decisions are selected. Operation approval remains agent guidance.", []string{policyPath, guardrails})
	} else if selected["permissions"] || selected["sandbox"] {
		r.ConfigurationState = "refused"
		r.check("policy.other", "requested-policy", "action-required", "Another security policy is selected. Doctor does not replace it with development guidance.", []string{manifestPath})
		return
	} else if doctorPathAbsent(policyPath) && doctorPathAbsent(registryPath) {
		r.ConfigurationState = "missing"
		if err := ValidateRepositoryWithOptions(canonical, true); err != nil {
			r.check("setup.invalid", "configuration", "action-required", "The existing canonical tree must be corrected before adoption.", []string{canonical})
		} else {
			r.check("setup.adopt", "requested-policy", "action-required", "This canonical tree has no development preset. Adoption preserves existing instructions and backs up changed canonical files.", []string{canonical}, append(initCommand, "--adopt"))
		}
		return
	} else {
		r.check("policy.deselected", "requested-policy", "ok", "Development permissions are deselected. Check for pending native removal.", []string{manifestPath})
	}
	if err := ValidateRepositoryWithOptions(canonical, true); err != nil {
		r.ConfigurationState = "invalid"
		r.check("setup.invalid", "configuration", "action-required", "Canonical configuration failed validation. Configuration values are omitted from this report.", []string{canonical})
		return
	}
	if r.RequestedPolicy != nil && r.Vendor == "codex" {
		path := filepath.Join(r.Root, ".codex/config.toml")
		if data, err := nativeReadFile(path); err == nil {
			values, err := parseNative(data, "toml")
			if err == nil && (values["sandbox_mode"] != nil || values["sandbox_workspace_write"] != nil) {
				r.ConfigurationState = "legacy-settings"
				r.check("configuration.legacy", "configuration", "action-required", "Legacy sandbox keys need an explicit migration plan and backup before apply.", []string{path}, doctorCommand("plan", r.Root, r.Vendor, "--force", "--backup"))
				return
			}
		}
	}
	plan, err := PlanProjection(r.Vendor, r.Root, ApplyOptions{Experimental: true, DevelopmentOnly: r.Vendor == "codex"})
	if err != nil {
		r.ConfigurationState = "blocked"
		r.check("configuration.blocked", "configuration", "action-required", "Planning refused the current configuration, ownership, or required capabilities. Inspect the plan before changing files.", []string{canonical}, planCommand)
		return
	}
	if !plan.Applicable {
		r.ConfigurationState = "conflict"
		r.check("configuration.conflict", "configuration", "action-required", "The planner reports conflicting or unsafe assignments. Review ownership and local changes before apply.", []string{canonical, filepath.Join(canonical, "state/native-"+r.Vendor+".json")}, planCommand)
		return
	}
	paths := []string{}
	for _, action := range plan.Actions {
		if action.Operation != "unchanged" {
			paths = append(paths, action.Path)
		}
	}
	if len(paths) != 0 {
		sort.Strings(paths)
		r.ConfigurationState = "needs-apply"
		message := "Generated configuration differs from the requested policy. Review the plan, then apply it."
		if r.RequestedPolicy == nil {
			r.ConfigurationState = "removal-pending"
			message = "Development permissions are deselected; managed projection changes remain. Review removal before apply."
		}
		r.check("configuration."+r.ConfigurationState, "configuration", "action-required", message, paths, planCommand, applyCommand)
		return
	}
	r.ConfigurationState = "current"
	if r.RequestedPolicy == nil {
		r.ConfigurationState = "not-selected"
	}
	r.check("configuration."+r.ConfigurationState, "configuration", "ok", "No managed changes are required in the reported projection scope. Active session permissions are not verified.", nil)
}

func doctorPathAbsent(path string) bool {
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func doctorExecutable(r *DoctorResult) {
	variable := strings.ToUpper(r.Vendor) + "_BIN"
	candidate := os.Getenv(variable)
	if candidate == "" {
		candidate = r.Vendor
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		r.check("executable.location", "configuration", "action-required", "Native executable unavailable. Set "+variable+" to an installed executable, or add it to PATH.", nil)
		return
	}
	path, err = filepath.Abs(path)
	if err != nil {
		r.check("executable.location", "configuration", "unknown", "Cannot resolve the native executable location.", nil)
		return
	}
	r.check("executable.location", "configuration", "ok", "Executable found but not run. Its version, behavior, and support status were not verified.", []string{path})
}

func doctorContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, strings.TrimSuffix(parent, string(filepath.Separator))+string(filepath.Separator))
}
