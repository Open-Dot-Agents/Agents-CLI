package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InitGlobal creates a private draft.2 source tree. It does not replace an
// existing configuration format or create instructions in the parent home.
func InitGlobal(home string) error {
	return initGlobal(home, nil)
}

func initGlobal(home string, hook nativeTransactionHook) error {
	if !filepath.IsAbs(home) {
		return fmt.Errorf("global configuration requires an absolute user home")
	}
	root := filepath.Join(home, ".agents")
	if err := nativeNoSymlinks(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("global configuration already exists at %s; migrate other formats explicitly", root)
	}
	files := map[string][]byte{
		"manifest.json": []byte("{\"version\":\"1.1.0-draft.2\",\"profiles\":[\"native\"]}\n"),
		"AGENTS.md":     []byte("# User Agent Instructions\n\nAdd shared instructions for your projects.\n"),
	}
	for _, vendor := range []string{"codex", "copilot"} {
		namespace, err := nativeNamespace(vendor)
		if err != nil {
			return err
		}
		profile := nativeProfile{Namespace: namespace, HarnessVersion: "=" + nativePinnedVersion(vendor), Scope: "user", Required: false,
			Artifacts: []nativeArtifact{{Kind: "canonical-instructions", Source: "AGENTS.md"}}}
		data, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return err
		}
		files[filepath.Join("native", namespace, "profile.json")] = append(data, '\n')
	}
	var changes []nativeChange
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		changes = append(changes, nativeChange{path: filepath.Join(root, name), data: files[name], mode: 0600, before: &nativeSnapshot{}})
	}
	return nativeRunTransaction(changes, hook)
}

// User defaults are applied separately to native user destinations. A project
// cannot drop their portable requirements merely by selecting a local tree.
func nativeGlobalSource(vendor, project string, scope string) (string, error) {
	if scope == "user" {
		return "", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("global discovery requires an absolute user home")
	}
	root := filepath.Join(home, ".agents")
	if root == project {
		return "", nil
	}
	data, err := nativeReadFile(filepath.Join(root, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var manifest manifestDocument
	if err := nativeDecodePolicyData(filepath.Join(root, "manifest.json"), data, &manifest); err != nil {
		return "", err
	}
	if manifest.Version != NativeVersion {
		return "", fmt.Errorf("global defaults require a draft.2 manifest; migrate other versions explicitly")
	}
	if err := ValidateRepositoryWithOptions(root, true); err != nil {
		return "", fmt.Errorf("global defaults: %w", err)
	}
	if nativeHasProfile(manifest.Profiles, "permissions") || nativeHasProfile(manifest.Profiles, "sandbox") {
		return "", fmt.Errorf("global portable security requirements cannot be dropped or weakened by project overrides; combined draft.2 enforcement is not verified")
	}
	diagnostics, err := requiredCapabilityDiagnostics(vendor, root)
	if err != nil {
		return "", err
	}
	if len(diagnostics) > 0 {
		return "", fmt.Errorf("global portable requirements: %s", strings.Join(diagnostics, "; "))
	}
	return root, nil
}

func nativeSharedGlobalSkills(vendor, root, nativeHome string) bool {
	home, err := os.UserHomeDir()
	return err == nil && root == filepath.Join(home, ".agents") && nativeHome == filepath.Join(home, "."+vendor)
}

func nativeUserCoreFeature(feature NativeFeature) NativeFeature {
	feature.Disposition, feature.NativeStatus = "portable-mapping", "unverified"
	feature.Limitation = "Fixed user binding from canonical AGENTS.md to native user instructions. Reference-bearing content requires an explicit native artifact. Projection does not establish model compliance or combined project behavior."
	return feature
}
