package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func nativeExistingUserInstructionArtifact(root, namespace string, fallback nativeArtifact) (nativeArtifact, error) {
	path := filepath.Join(root, "native", namespace, "profile.json")
	data, err := nativeReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	var profile nativeProfile
	if err := nativeDecodePolicyData(path, data, &profile); err != nil {
		return fallback, err
	}
	if profile.Scope != "user" {
		return fallback, nil
	}
	seen := false
	for _, artifact := range profile.Artifacts {
		if artifact.Kind != "instructions" {
			continue
		}
		if seen {
			return fallback, fmt.Errorf("duplicate native user instruction target")
		}
		fallback, seen = artifact, true
	}
	return fallback, nil
}

const nativeUserInstructionReferenceAction = "Keep referenced user instruction files at their native paths under the selected native home. Apply does not copy referenced files or grant trust. Start a new Copilot session after changes."

func nativeCopilotUserInstructionFeature(feature NativeFeature) NativeFeature {
	feature.Disposition = "artifact-mapping"
	feature.NativeStatus = "bounded-user-instruction-loading"
	feature.Evidence = []string{"docs/COPILOT_USER_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_user_instructions.py"}
	feature.Limitation = "Preserves user copilot-instructions.md bytes and the declared namespace source. Native evidence covers default and explicit COPILOT_HOME locations, combined user and project bodies, relative references within the user instruction directory, relocation, updated sessions, and removal. Referenced files remain external. This does not establish arbitrary references, live reload, model compliance, or native trust grants."
	return feature
}
