package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// This registered binding has one fixed core source. It is not a path escape
// from the namespace and does not create a second copy of canonical content.
func nativeArtifactSource(root string, profile nativeProfile, artifact nativeArtifact) (string, error) {
	if profile.directory == "native" && profile.Namespace == "com.github.copilot" && artifact.Kind == "canonical-instructions" {
		if artifact.Source != "AGENTS.md" || artifact.Name != "" {
			return "", fmt.Errorf("canonical-instructions requires source AGENTS.md and no name")
		}
		return filepath.Join(root, "AGENTS.md"), nil
	}
	return filepath.Join(root, profile.directory, profile.Namespace, artifact.Source), nil
}

type nativeInstructionBindings struct {
	Core   bool
	Native nativeArtifact
}

func nativeExistingInstructionBindings(root string) (nativeInstructionBindings, error) {
	bindings := nativeInstructionBindings{Native: nativeArtifact{Kind: "instructions", Source: "copilot-instructions.md"}}
	path := filepath.Join(root, "native/com.github.copilot/profile.json")
	data, err := nativeReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bindings, nil
	}
	if err != nil {
		return bindings, err
	}
	var profile nativeProfile
	if err = nativeDecodePolicyData(path, data, &profile); err != nil {
		return bindings, err
	}
	if profile.Scope != "project" {
		return bindings, nil
	}
	seenNative := false
	for _, artifact := range profile.Artifacts {
		switch artifact.Kind {
		case "canonical-instructions":
			if bindings.Core {
				return bindings, fmt.Errorf("duplicate canonical instruction binding")
			}
			bindings.Core = true
		case "instructions":
			if seenNative {
				return bindings, fmt.Errorf("duplicate Copilot instruction target")
			}
			bindings.Native, seenNative = artifact, true
		}
	}
	return bindings, nil
}

func nativeCanonicalInstructionFeature(feature NativeFeature) NativeFeature {
	feature.Disposition = "portable-mapping"
	feature.NativeStatus = "bounded-canonical-instruction-loading"
	feature.Evidence = []string{"docs/COPILOT_CANONICAL_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_canonical_instructions.py"}
	feature.Limitation = "Fixed project binding from canonical .agents/AGENTS.md to root AGENTS.md. A verified existing canonical link is preserved; relocation creates a managed regular root file. Separate native instructions can use .github/copilot-instructions.md. Referenced project files remain external. Native evidence covers root-relative references through the source link and relocated regular file, not arbitrary link discovery or live reload."
	return feature
}
