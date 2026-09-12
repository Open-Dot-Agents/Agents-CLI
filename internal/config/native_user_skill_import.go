package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Add disjoint packages or retain an identical package. File-by-file import
// must not create a new package from assets that never occurred together.
func nativeCheckUserSkillImport(vendor, destination string, incoming []nativeChange) error {
	existing, excluded, err := nativeImportSkills(destination, destination)
	if err != nil {
		return err
	}
	if len(excluded) != 0 {
		return fmt.Errorf("existing canonical skills contain a nonportable system package; import cannot select it")
	}
	previous, err := nativeGroupSkillChanges(destination, existing)
	if err != nil {
		return err
	}
	packages, err := nativeGroupSkillChanges(destination, incoming)
	if err != nil {
		return err
	}
	for name, files := range packages {
		if old, exists := previous[name]; exists && !nativeSameSkillPackage(old, files) {
			return fmt.Errorf("conflicting complete user skill package %q; import cannot combine or replace its assets", name)
		}
		previous[name] = files
	}
	if vendor == "copilot" {
		return nativeCheckUserSkillNames(destination, previous)
	}
	return nil
}

func nativeCheckUserSkillNames(destination string, packages map[string][]nativeChange) error {
	identities := map[string]string{}
	for name, files := range packages {
		for _, file := range files {
			if file.path != filepath.Join(destination, name, "SKILL.md") {
				continue
			}
			fields, err := nativeCopilotSkillFields(file.data)
			if err != nil {
				continue // Malformed source is preserved; projection validates it separately.
			}
			identity, _ := fields["name"].(string)
			if identity == "" {
				identity = name
			}
			if prior, exists := identities[identity]; exists && prior != name {
				return fmt.Errorf("duplicate native user skill identity in packages %q and %q", prior, name)
			}
			identities[identity] = name
		}
	}
	return nil
}

func nativeGroupSkillChanges(destination string, changes []nativeChange) (map[string][]nativeChange, error) {
	packages := map[string][]nativeChange{}
	for _, change := range changes {
		relative, err := filepath.Rel(destination, change.path)
		if err != nil || !safeNativeRelative(relative) {
			return nil, fmt.Errorf("skill asset escapes its package destination")
		}
		name := strings.Split(filepath.ToSlash(relative), "/")[0]
		packages[name] = append(packages[name], change)
	}
	return packages, nil
}

func nativeCheckUserSkillProjection(vendor, root, home string, state nativeRegistry) error {
	destination := filepath.Join(home, "skills")
	assets, _, err := nativeImportSkills(filepath.Join(root, "skills"), destination)
	if err != nil {
		return err
	}
	packages, err := nativeGroupSkillChanges(destination, assets)
	if err != nil {
		return err
	}
	if vendor == "copilot" {
		identities := map[string][]nativeChange{}
		for name, files := range packages {
			identities[name] = files
		}
		entries, err := os.ReadDir(destination)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == ".system" || packages[entry.Name()] != nil {
				continue
			}
			path := filepath.Join(destination, entry.Name(), "SKILL.md")
			if owner, exists := state.Settings[nativeKey(path, "")]; exists && owner.Source == root {
				continue // This source's removed definition is reconciled by file ownership.
			}
			data, err := nativeReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			identities[entry.Name()] = []nativeChange{{path: path, data: data}}
		}
		if err := nativeCheckUserSkillNames(destination, identities); err != nil {
			return err
		}
	}
	for name, desired := range packages {
		path := filepath.Join(destination, name)
		if err := nativeNoSymlinks(path); err != nil {
			return err
		}
		var current []nativeChange
		managed := true
		err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) && entry == nil {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			snapshot, err := nativeReadSnapshot(path)
			if err != nil {
				return err
			}
			mode := fs.FileMode(0600)
			if snapshot.mode.Perm()&0111 != 0 {
				mode = 0700
			}
			current = append(current, nativeChange{path: path, data: snapshot.data, mode: mode})
			if owner, exists := state.Settings[nativeKey(path, "")]; !exists || owner.Source != root {
				managed = false
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !managed && !nativeSameSkillPackage(current, desired) {
			return fmt.Errorf("conflicting complete user skill package %q; adoption cannot combine owned and unowned assets", name)
		}
	}
	return nil
}

func nativeCopilotUserSkillFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "bounded-user-skill-execution"
	feature.Evidence = []string{"docs/COPILOT_USER_SKILLS.md", "WORKBENCH/conformance/verify_copilot_user_skills.py"}
	feature.Limitation = "Import reads skills only from the explicitly selected --native-home: ~/.copilot for personal skills or ~/.agents for shared skills. Apply targets the explicitly selected Copilot home. Native evidence covers both source locations, user discovery from a separate workspace, asset execution, updates, and removal. Source bytes, executable assets, and portable policy are preserved. Complete-package and native-name conflicts refuse; backups stay in private state outside active packages. Other homes, inherited directories, project precedence, and live reload are not established."
	return feature
}

func nativeCopilotUserSkillCapabilities() []NativeFeature {
	var features []NativeFeature
	for _, source := range []string{"~/.copilot/skills/", "~/.agents/skills/"} {
		features = append(features, nativeCopilotUserSkillFeature(NativeFeature{
			Feature: "artifact:skill-discovery:/" + source, Source: source, Destination: "<native-home>/skills/<name>/", Scope: "user",
			Disposition: "artifact-mapping", Activation: "explicit user import and apply; start a new native session",
			Ownership: "file owned by source repository", Authority: "explicit selected source and target homes; no implicit home scan or trust grant",
		}))
	}
	return features
}
