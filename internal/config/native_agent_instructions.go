package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

func nativeCopilotAgentInstructionName(name string) bool {
	switch name {
	case "AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md", "GEMINI.md":
		return true
	}
	return false
}

func nativeExistingAgentInstructionSources(root string) (map[string]string, error) {
	sources := map[string]string{}
	path := filepath.Join(root, "native/com.github.copilot/profile.json")
	data, err := nativeReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return sources, nil
	}
	if err != nil {
		return nil, err
	}
	var profile nativeProfile
	if err = nativeDecodePolicyData(path, data, &profile); err != nil {
		return nil, err
	}
	if profile.Scope != "project" {
		return sources, nil
	}
	for _, artifact := range profile.Artifacts {
		if artifact.Kind == "agent-instructions" && nativeCopilotAgentInstructionName(artifact.Name) {
			if sources[artifact.Name] != "" {
				return nil, fmt.Errorf("duplicate native agent instruction target %s", artifact.Name)
			}
			sources[artifact.Name] = artifact.Source
		}
	}
	return sources, nil
}

func nativeImportAgentInstruction(root, name, source string, data []byte) (nativeChange, nativeArtifact, error) {
	if !nativeCopilotAgentInstructionName(name) || !utf8.Valid(data) {
		return nativeChange{}, nativeArtifact{}, fmt.Errorf("native agent instructions require a registered path and UTF-8 Markdown")
	}
	if source == "" {
		source = filepath.ToSlash(filepath.Join("agent-instructions", name))
	}
	return nativeChange{path: filepath.Join(root, "native/com.github.copilot", source), data: data, mode: 0600},
		nativeArtifact{Kind: "agent-instructions", Name: name, Source: source}, nil
}

const nativeAgentInstructionReferenceAction = "Keep referenced project files at their native relative paths and start a new Copilot session. Apply preserves instruction file locations; it does not copy referenced project files, expand @ references, or change native trust. The tested .claude/CLAUDE.md file loads in a .claude working directory, not in repository-root sessions."

func nativeAgentInstructionFeature(feature NativeFeature) NativeFeature {
	feature.Disposition = "artifact-mapping"
	feature.NativeStatus = "bounded-agent-instruction-loading"
	feature.Evidence = []string{"docs/COPILOT_AGENT_INSTRUCTIONS.md", "WORKBENCH/conformance/verify_copilot_agent_instructions.py"}
	feature.Limitation = "Fixed Copilot project paths only: AGENTS.md, CLAUDE.md, .claude/CLAUDE.md, and GEMINI.md. Bytes and native reference bases are preserved. Referenced project files remain external. In the tested native sessions, Copilot loads .claude/CLAUDE.md from a .claude working directory but not from the repository root; it does not expand @ references in GEMINI.md. Native instruction loading does not prove model compliance. Other nested scopes, later file-triggered discovery, and live reload are not covered."
	return feature
}
