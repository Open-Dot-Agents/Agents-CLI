package config

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type doctorMount struct {
	path     string
	readOnly bool
}

func parseDoctorMounts(data string) ([]doctorMount, error) {
	var mounts []doctorMount
	for _, line := range strings.Split(data, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			return nil, errors.New("invalid mount metadata")
		}
		var point strings.Builder
		for i := 0; i < len(fields[4]); i++ {
			if fields[4][i] != '\\' {
				point.WriteByte(fields[4][i])
				continue
			}
			if i+3 >= len(fields[4]) {
				return nil, errors.New("invalid mount path escape")
			}
			value, err := strconv.ParseUint(fields[4][i+1:i+4], 8, 8)
			if err != nil || value == 0 {
				return nil, errors.New("invalid mount path escape")
			}
			point.WriteByte(byte(value))
			i += 3
		}
		if !filepath.IsAbs(point.String()) {
			return nil, errors.New("mount point is not absolute")
		}
		readonly := false
		for _, options := range []string{fields[5], fields[separator+3]} {
			for _, option := range strings.Split(options, ",") {
				readonly = readonly || option == "ro"
			}
		}
		mounts = append(mounts, doctorMount{filepath.Clean(point.String()), readonly})
	}
	if len(mounts) == 0 {
		return nil, errors.New("mount metadata is empty")
	}
	return mounts, nil
}

func doctorMountReadOnly(path string, mounts []doctorMount) (bool, bool) {
	selected := -1
	ambiguous := false
	for i, mount := range mounts {
		if !doctorContains(mount.path, path) {
			continue
		}
		if selected < 0 || len(mount.path) > len(mounts[selected].path) {
			selected, ambiguous = i, false
		} else if mount.path == mounts[selected].path && mount.readOnly != mounts[selected].readOnly {
			ambiguous = true
		}
	}
	if selected < 0 || ambiguous {
		return false, false
	}
	return mounts[selected].readOnly, true
}

func doctorReadMetadata(path string) (string, error) {
	if err := rejectSymlinkPath(path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return "", errors.New("invalid Git metadata file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return "", errors.New("Git metadata changed during inspection")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return "", errors.New("cannot read Git metadata")
	}
	line := strings.TrimSpace(string(data))
	if line == "" || strings.ContainsAny(line, "\x00\r\n") {
		return "", errors.New("invalid Git metadata line")
	}
	return line, nil
}

func doctorProtected(path, root string, protected []string) bool {
	for _, name := range protected {
		if doctorContains(filepath.Join(root, filepath.FromSlash(name)), path) {
			return true
		}
	}
	return false
}

func doctorGitDirs(root string, protected []string) ([]string, error) {
	directory := root
	var gitdir string
	for {
		path := filepath.Join(directory, ".git")
		if doctorProtected(path, root, protected) {
			return nil, errors.New("Git metadata is protected")
		}
		info, err := os.Lstat(path)
		if err == nil {
			if info.IsDir() {
				gitdir = path
			} else {
				data, err := doctorReadMetadata(path)
				if err != nil || !strings.HasPrefix(data, "gitdir: ") {
					return nil, errors.New("invalid Git directory reference")
				}
				gitdir = strings.TrimPrefix(data, "gitdir: ")
				if !filepath.IsAbs(gitdir) {
					gitdir = filepath.Join(directory, gitdir)
				}
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil, errors.New("Git metadata was not found")
		}
		directory = parent
	}
	gitdir, err := filepath.EvalSymlinks(gitdir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(gitdir)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Git metadata directory is unavailable")
	}
	paths := []string{gitdir}
	common := filepath.Join(gitdir, "commondir")
	if doctorProtected(common, root, protected) {
		return nil, errors.New("Git metadata is protected")
	}
	data, err := doctorReadMetadata(common)
	if os.IsNotExist(err) {
		return paths, nil
	}
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(data) {
		data = filepath.Join(gitdir, data)
	}
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		return nil, err
	}
	info, err = os.Stat(data)
	if err != nil || !info.IsDir() {
		return nil, errors.New("common Git directory is unavailable")
	}
	if data != gitdir {
		paths = append(paths, data)
	}
	return paths, nil
}

func doctorFilesystem(r *DoctorResult, load func() ([]doctorMount, error)) {
	mounts, err := load()
	if err != nil {
		r.check("filesystem.mounts", "process-filesystem", "unknown", "Mount metadata is unavailable. Write access was not tested.", nil)
		return
	}
	inspect := func(id, path string, writesRequested bool) {
		resolved, err := filepath.EvalSymlinks(path)
		readonly, known := doctorMountReadOnly(resolved, mounts)
		if err != nil || !known {
			r.check(id, "process-filesystem", "unknown", "The current mount restriction could not be determined. Write access was not tested.", []string{path})
		} else if readonly {
			status := "unknown"
			if writesRequested {
				status = "action-required"
			}
			r.check(id, "process-filesystem", status, "This path is on a read-only mount in the current process. Project allow decisions cannot make this mount writable.", []string{resolved})
			r.Checks[len(r.Checks)-1].NextStep = "Inspect the current app or session permission profile. Do not weaken the project policy to bypass this restriction."
		} else {
			r.check(id, "process-filesystem", "unknown", "No read-only mount was detected here. Other permissions can still prevent writes; no write was attempted.", []string{resolved})
		}
	}
	projectWrites, gitWrites := false, false
	var protected []string
	if r.RequestedPolicy != nil {
		projectWrites = r.RequestedPolicy.ProjectWork == "allow"
		gitWrites = r.RequestedPolicy.LocalCommits == "allow"
		protected = r.RequestedPolicy.ProtectedPaths
	}
	inspect("filesystem.workspace", r.Root, projectWrites)
	roots, walkErr := doctorGitRoots(r.Root, protected)
	if walkErr != nil {
		r.check("filesystem.git-discovery", "process-filesystem", "unknown", "Some nested Git directories could not be inspected. Their write access is unknown.", nil)
	}
	seen := map[string]bool{}
	for _, root := range roots {
		paths, err := doctorGitDirs(root, protectedForRoot(r.Root, root, protected))
		if err != nil {
			r.check("filesystem.git", "process-filesystem", "unknown", "Git metadata is missing, protected, or unsafe to resolve. Git write access was not tested.", []string{root})
			continue
		}
		for _, path := range paths {
			if !seen[path] {
				inspect("filesystem.git", path, gitWrites)
				seen[path] = true
			}
		}
	}
}

// Discover nested repositories without executing Git or opening their working
// files. WalkDir does not follow links; protected directories are not entered.
func doctorGitRoots(root string, protected []string) ([]string, error) {
	roots := []string{root}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || path == root {
			return nil
		}
		if doctorProtected(path, root, protected) || entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			roots = append(roots, path)
		} else if !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	return roots, err
}

func protectedForRoot(project, root string, protected []string) []string {
	var paths []string
	for _, path := range protected {
		if relative, err := filepath.Rel(root, filepath.Join(project, filepath.FromSlash(path))); err == nil {
			paths = append(paths, filepath.ToSlash(relative))
		}
	}
	return paths
}
