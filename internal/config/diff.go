package config

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileSnapshot captures the regular files under managed configuration directories.
type FileSnapshot map[string][]byte

// CaptureFiles records the managed files below paths. Missing directories are empty snapshots.
func CaptureFiles(paths ...string) (FileSnapshot, error) {
	snapshot := make(FileSnapshot)
	for _, root := range paths {
		if _, err := os.Stat(root); errorsIsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect diff path %q: %w", root, err)
		}
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("cannot summarize non-regular file %q", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[path] = data
			return nil
		}); err != nil {
			return nil, fmt.Errorf("capture diff path %q: %w", root, err)
		}
	}
	return snapshot, nil
}

// RenderChangeSummary returns a Git-style summary of added, modified, and deleted files.
func RenderChangeSummary(before, after FileSnapshot) string {
	paths := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		paths[path] = struct{}{}
	}
	for path := range after {
		paths[path] = struct{}{}
	}
	sortedPaths := make([]string, 0, len(paths))
	for path := range paths {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	var builder strings.Builder
	for _, path := range sortedPaths {
		beforeData, beforeExists := before[path]
		afterData, afterExists := after[path]
		if beforeExists && afterExists && bytes.Equal(beforeData, afterData) {
			continue
		}
		added, removed := changedLines(beforeData, afterData)
		displayPath := filepath.ToSlash(path)
		switch {
		case !beforeExists:
			fmt.Fprintf(&builder, "A  %s (+%d)\n", displayPath, added)
		case !afterExists:
			fmt.Fprintf(&builder, "D  %s (-%d)\n", displayPath, removed)
		default:
			fmt.Fprintf(&builder, "M  %s (+%d -%d)\n", displayPath, added, removed)
		}
	}
	if builder.Len() == 0 {
		return "No managed configuration changes.\n"
	}
	return "Configuration changes:\n" + builder.String()
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func changedLines(before, after []byte) (added, removed int) {
	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && bytes.Equal(beforeLines[prefix], afterLines[prefix]) {
		prefix++
	}
	beforeEnd, afterEnd := len(beforeLines), len(afterLines)
	for beforeEnd > prefix && afterEnd > prefix && bytes.Equal(beforeLines[beforeEnd-1], afterLines[afterEnd-1]) {
		beforeEnd--
		afterEnd--
	}
	return afterEnd - prefix, beforeEnd - prefix
}

func splitLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		return lines[:len(lines)-1]
	}
	return lines
}
