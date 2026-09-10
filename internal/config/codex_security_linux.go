//go:build linux

package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func checkCodexPolicyPaths(root string, policy SandboxPolicy) error {
	// Git worktree/submodule indirection and filesystem mount changes need a
	// separate mapping. This subset requires a real, local Git directory.
	git, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil || !git.IsDir() {
		return securityError("the subset requires a local .git directory")
	}
	// A writable workspace entry can alias a read-only file outside the
	// workspace. Inspect writable trees too: path masks cannot separate two
	// existing names for one inode. Native enforcement blocks new aliases.
	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return securityError("cannot inspect native mount boundaries")
	}
	if err := rejectPolicyMountAliases(root, string(mounts)); err != nil {
		return err
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return securityError("cannot inspect workspace aliases")
		}
		info, err := entry.Info()
		if err != nil {
			return securityError("cannot inspect workspace entry")
		}
		if info.Mode().IsRegular() {
			native, ok := info.Sys().(*syscall.Stat_t)
			if !ok || native.Nlink != 1 {
				return securityError("workspace files cannot have hard-link aliases")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, relative := range codexPolicyPathList(policy) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := rejectSymlinkPath(path); err != nil {
			return securityError("policy paths cannot contain symlinks: " + relative)
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && relative == ".codex" {
			continue
		}
		if err != nil {
			return securityError("policy paths must exist before activation: " + relative)
		}
		access, _ := filesystemAccess(policy, relative)
		if access == "write" {
			continue
		}
		if err := rejectPolicyMountAliases(path, string(mounts)); err != nil {
			return err
		}
		err = filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return securityError("cannot inspect restricted tree: " + relative)
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return securityError("restricted trees cannot contain symlinks: " + relative)
			}
			stat, err := entry.Info()
			if err != nil {
				return securityError("cannot inspect restricted entry")
			}
			if stat.Mode().IsRegular() {
				native, ok := stat.Sys().(*syscall.Stat_t)
				if !ok || native.Nlink != 1 {
					return securityError("restricted files cannot have hard-link aliases")
				}
			}
			if native, ok := stat.Sys().(*syscall.Stat_t); ok {
				if origin, ok := info.Sys().(*syscall.Stat_t); ok && native.Dev != origin.Dev {
					return securityError("restricted trees cannot cross mounts")
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func pathContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, strings.TrimSuffix(parent, string(filepath.Separator))+string(filepath.Separator))
}

// Bind mounts do not increase a file's hard-link count. Refuse alternate mount
// views of a restricted tree rather than claim inode-wide isolation from path
// masks. Mount changes by outside actors invalidate this preflight.
func rejectPolicyMountAliases(target, source string) error {
	type mount struct{ device, origin, path string }
	var mounts []mount
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(source, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			return securityError("cannot parse native mount boundaries")
		}
		mounts = append(mounts, mount{fields[2], decode.Replace(fields[3]), decode.Replace(fields[4])})
	}
	selected := -1
	for i, entry := range mounts {
		if pathContains(entry.path, target) && (selected < 0 || len(entry.path) > len(mounts[selected].path)) {
			selected = i
		}
	}
	if selected < 0 {
		return securityError("policy path has no known native mount")
	}
	origin := mounts[selected]
	relative, err := filepath.Rel(origin.path, target)
	if err != nil {
		return err
	}
	targetOrigin := filepath.Join(origin.origin, relative)
	for i, entry := range mounts {
		if i == selected {
			continue
		}
		if pathContains(target, entry.path) {
			return securityError("restricted trees cannot contain mounts")
		}
		if entry.device == origin.device && (pathContains(entry.origin, targetOrigin) || pathContains(targetOrigin, entry.origin)) {
			return securityError("restricted tree has an alternate mount view")
		}
	}
	return nil
}
