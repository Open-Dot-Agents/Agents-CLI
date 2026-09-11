package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Only the registered instruction directory has recursive discovery. Keep its
// relative layout; equal basenames in separate subdirectories are distinct.
func nativeImportScopedInstructions(scope, base, root, namespace string) ([]nativeChange, []nativeArtifact, []string, error) {
	example, _, err := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "scoped-instructions", Name: "fixture.instructions.md"})
	if err != nil {
		return nil, nil, nil, err
	}
	directory := filepath.Dir(example)
	if err := nativeNoSymlinks(directory); err != nil {
		return nil, nil, nil, err
	}
	if info, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	} else if err != nil {
		return nil, nil, nil, err
	} else if !info.IsDir() {
		return nil, nil, nil, fmt.Errorf("native instruction location must be a directory")
	}
	var changes []nativeChange
	var artifacts []nativeArtifact
	var excluded []string
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := nativeNoSymlinks(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("native instruction asset is not a regular file")
		}
		if !strings.HasSuffix(entry.Name(), ".instructions.md") {
			relative, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			excluded = append(excluded, "unrecognized artifact: "+filepath.ToSlash(relative))
			return nil
		}
		name, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		artifact := nativeArtifact{Kind: "scoped-instructions", Name: filepath.ToSlash(name), Source: filepath.ToSlash(filepath.Join("scoped-instructions", name))}
		target, _, err := nativeTargetPath("copilot", scope, base, artifact)
		if err != nil {
			return err
		}
		if target != path {
			return fmt.Errorf("native instruction source does not match registry")
		}
		data, err := nativeReadFile(path)
		if err != nil {
			return err
		}
		changes = append(changes, nativeChange{path: filepath.Join(root, "native", namespace, artifact.Source), data: data, mode: 0600})
		artifacts = append(artifacts, artifact)
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return changes, artifacts, excluded, nil
}

func nativeCopilotRecursiveInstructionFeature(feature NativeFeature) NativeFeature {
	feature.Disposition = "artifact-mapping"
	feature.NativeStatus = "bounded-recursive-instruction-loading"
	feature.Evidence = []string{"docs/COPILOT_RECURSIVE_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_recursive_instructions.py"}
	feature.Limitation = "Preserves complete relative paths under the registered instruction directory. Native evidence covers direct loading with applyTo '**' and model-requested reads of '**/*.go' instruction paths from the native catalog in fresh sessions. Path-specific discovery provides model guidance, not automatic body injection or deterministic glob enforcement. The model can skip instruction reads. User instruction reads can require native permission when the native home is outside trusted directories. Other metadata, imports, and live reload remain unverified. Apply does not write native trust or account state."
	return feature
}

const nativeCopilotInstructionReadAction = "Copilot can request native read permission for path-specific user instructions outside trusted directories. Approve or deny these reads in the native client; apply does not grant directory trust."

func nativeCopilotInstructionDiscoveryCapability(scope string) NativeFeature {
	location := ".github/instructions/**/*.instructions.md"
	if scope == "user" {
		location = "$HOME/.copilot/instructions/**/*.instructions.md"
	}
	return nativeCopilotRecursiveInstructionFeature(NativeFeature{
		Feature: "artifact:instruction-discovery:/" + location, Source: location,
		Destination: ".agents/native/com.github.copilot/scoped-instructions/<relative-path>.instructions.md", Scope: scope,
		Activation: "import then project; start a new native session", Ownership: "file", Authority: "registry instruction directory only",
	})
}
