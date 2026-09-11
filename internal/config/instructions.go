package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func validateInstructionDiscovery(root string) error {
	repositoryRoot := filepath.Dir(root)
	return filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".state" || path == root) {
			return filepath.SkipDir
		}
		if entry.IsDir() && path != repositoryRoot {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if entry.Name() != "AGENTS.md" {
			return nil
		}
		canonical := filepath.Join(filepath.Dir(path), ".agents", "AGENTS.md")
		if path == filepath.Join(repositoryRoot, "AGENTS.md") {
			canonical = filepath.Join(root, "AGENTS.md")
		}
		return validateInstructionFile(path, canonical)
	})
}

func prepareRootInstructionLink(root string, state ownershipState, options ApplyOptions) (map[string]string, map[string]string, error) {
	links, owned := map[string]string{}, map[string]string{}
	path := filepath.Join(root, "AGENTS.md")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		links[path], owned["AGENTS.md"] = ".agents/AGENTS.md", ".agents/AGENTS.md"
		return links, owned, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := validateInstructionFile(path, filepath.Join(root, ".agents/AGENTS.md")); err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if state.Links["AGENTS.md"] != "" || options.Adopt {
			owned["AGENTS.md"] = ".agents/AGENTS.md"
		}
	} else if state.Links["AGENTS.md"] != "" {
		return nil, nil, fmt.Errorf("managed instruction link %q was replaced by a regular file; restore the canonical link before apply", path)
	}
	return links, owned, nil
}

// A compatibility link names the canonical instructions in its own scope.
// The source directory and file must be real entries, not indirect links.
func validateInstructionFile(path, canonical string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		return nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("canonical instructions %q are not a regular file", path)
	}
	if err := requireDirectory(filepath.Dir(canonical), "canonical instruction directory"); err != nil {
		return err
	}
	if err := requireRegularFile(canonical, "canonical instructions"); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve instruction link %q: %w", path, err)
	}
	expected, err := filepath.EvalSymlinks(canonical)
	if err != nil {
		return err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return err
	}
	expected, err = filepath.Abs(expected)
	if err != nil {
		return err
	}
	if resolved != expected {
		return fmt.Errorf("instruction link %q must refer to its own canonical instructions %q", path, canonical)
	}
	return nil
}
