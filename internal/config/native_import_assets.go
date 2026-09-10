package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Read only recognized skill packages. Built-in system packages and unrelated
// native-home state are not source configuration owned by a repository.
func nativeImportSkills(source, destination string) ([]nativeChange, []string, error) {
	if err := nativeNoSymlinks(source); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var changes []nativeChange
	var excluded []string
	for _, entry := range entries {
		if entry.Name() == ".system" {
			excluded = append(excluded, "skills/.system")
			continue
		}
		if !entry.IsDir() || !isSafeSkillName(entry.Name()) {
			return nil, nil, fmt.Errorf("unrecognized native skill package %s", entry.Name())
		}
		skillRoot := filepath.Join(source, entry.Name())
		definition := filepath.Join(skillRoot, "SKILL.md")
		if err = nativeNoSymlinks(definition); err != nil {
			return nil, nil, err
		}
		if err = requireRegularFile(definition, "native skill definition"); err != nil {
			return nil, nil, err
		}
		err = filepath.WalkDir(skillRoot, func(path string, child fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if child.IsDir() {
				return nil
			}
			if !child.Type().IsRegular() {
				return fmt.Errorf("native skill contains a non-regular asset")
			}
			if err := nativeNoSymlinks(path); err != nil {
				return err
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			if !safeNativeRelative(relative) {
				return fmt.Errorf("native skill asset path is not portable")
			}
			data, err := nativeReadFile(path)
			if err != nil {
				return err
			}
			info, err := child.Info()
			if err != nil {
				return err
			}
			mode := fs.FileMode(0600)
			if info.Mode().Perm()&0111 != 0 {
				mode = 0700
			}
			changes = append(changes, nativeChange{path: filepath.Join(destination, relative), data: data, mode: mode})
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return changes, excluded, nil
}

// Read only registry-defined, flat native artifact directories. Do not traverse
// the native home or import account, session, managed-policy, or plugin stores.
func nativeImportArtifactFiles(vendor, scope, base, root, namespace string) ([]nativeChange, []nativeArtifact, []string, error) {
	type artifactDirectory struct{ kind, suffix string }
	directories := []artifactDirectory{{"agent", ".toml"}}
	if vendor == "copilot" {
		directories = []artifactDirectory{{"agent", ".md"}, {"scoped-instructions", ".instructions.md"}, {"hooks", ".json"}}
	}
	var changes []nativeChange
	var artifacts []nativeArtifact
	var exclusions []string
	for _, directory := range directories {
		example, _, err := nativeTargetPath(vendor, scope, base, nativeArtifact{Kind: directory.kind, Name: "fixture" + directory.suffix})
		if err != nil {
			return nil, nil, nil, err
		}
		sourceDir := filepath.Dir(example)
		if err = nativeNoSymlinks(sourceDir); err != nil {
			return nil, nil, nil, err
		}
		entries, err := os.ReadDir(sourceDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		for _, entry := range entries {
			source := filepath.Join(sourceDir, entry.Name())
			if err = nativeNoSymlinks(source); err != nil {
				return nil, nil, nil, err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), directory.suffix) {
				relative, _ := filepath.Rel(base, source)
				exclusions = append(exclusions, "unrecognized artifact: "+filepath.ToSlash(relative))
				continue
			}
			artifact := nativeArtifact{Kind: directory.kind, Name: entry.Name(), Source: filepath.ToSlash(filepath.Join(directory.kind, entry.Name()))}
			target, format, err := nativeTargetPath(vendor, scope, base, artifact)
			if err != nil {
				return nil, nil, nil, err
			}
			if target != source {
				return nil, nil, nil, fmt.Errorf("native artifact source does not match registry")
			}
			data, err := nativeReadFile(source)
			if err != nil {
				return nil, nil, nil, err
			}
			if vendor == "codex" && directory.kind == "agent" {
				format = "toml"
			}
			if vendor == "copilot" && directory.kind == "agent" {
				if err := nativeCheckCopilotAgentImport(data); err != nil {
					return nil, nil, nil, fmt.Errorf("cannot import native agent %s: %w", entry.Name(), err)
				}
			}
			if format != "" {
				values, err := parseNative(data, format)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("malformed native artifact %s: %w", entry.Name(), err)
				}
				if vendor == "copilot" && directory.kind == "hooks" {
					if err := nativeCheckCopilotHookCredentials(values); err != nil {
						return nil, nil, nil, err
					}
				}
				filtered, excluded := nativeImportFilter(vendor, values, nil)
				for _, path := range excluded {
					exclusions = append(exclusions, artifact.Source+":"+path)
				}
				if len(excluded) > 0 {
					data, err = nativeEncode(filtered, format)
					if err != nil {
						return nil, nil, nil, err
					}
				}
			}
			changes = append(changes, nativeChange{path: filepath.Join(root, "native", namespace, artifact.Source), data: data, mode: 0600})
			artifacts = append(artifacts, artifact)
		}
	}
	return changes, artifacts, exclusions, nil
}
