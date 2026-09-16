package config

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

func TestDoctorMountMetadata(t *testing.T) {
	data := "1 0 8:1 / / rw,relatime - ext4 /dev/root rw\n" +
		"2 1 8:1 /git /work\\040space/.git ro,relatime shared:4 - ext4 /dev/root rw\n" +
		"3 1 8:2 / /other rw - ext4 /dev/other ro\n"
	mounts, err := parseDoctorMounts(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		ro   bool
	}{{"/work space/.git/objects", true}, {"/work space/.github", false}, {"/other/path", true}} {
		ro, known := doctorMountReadOnly(c.path, mounts)
		if !known || ro != c.ro {
			t.Fatal(c, ro, known)
		}
	}
	mounts = append(mounts, doctorMount{path: "/other"})
	if _, known := doctorMountReadOnly("/other/path", mounts); known {
		t.Fatal("ambiguous stacked mounts claimed a restriction")
	}
	for _, bad := range []string{"", "invalid", "1 0 8:1 / / rw - ext4", strings.ReplaceAll(data, `\040`, `\xyz`)} {
		if _, err := parseDoctorMounts(bad); err == nil {
			t.Fatal("accepted invalid mount metadata")
		}
	}
}

func TestDoctorMountPathRoundTrip(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	// A separate encoder supplies the mountinfo octal format. Include arbitrary
	// bytes, whitespace and backslashes; exclude NUL and path separators.
	property := func(input []byte, readonly bool) bool {
		var name, encoded strings.Builder
		name.WriteString("mount-")
		encoded.WriteString("mount-")
		for _, b := range input {
			if b == 0 || b == '/' || b == '\\' && filepath.Separator == '\\' {
				b = '_'
			}
			name.WriteByte(b)
			fmt.Fprintf(&encoded, `\%03o`, b)
		}
		options := "rw"
		if readonly {
			options = "ro"
		}
		mounts, err := parseDoctorMounts("1 0 8:1 / " + root + encoded.String() + " " + options + " - ext4 /dev/root rw\n")
		return err == nil && len(mounts) == 1 && mounts[0].path == filepath.Join(root, name.String()) && mounts[0].readOnly == readonly
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 250, Rand: rand.New(rand.NewSource(1))}); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorGitMetadataLayouts(t *testing.T) {
	for _, layout := range []string{"regular", "submodule", "worktree", "ancestor", "protected", "malformed", "link"} {
		t.Run(layout, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "project")
			os.Mkdir(root, 0755)
			var expected []string
			var protected []string
			switch layout {
			case "regular", "ancestor", "protected":
				os.Mkdir(filepath.Join(root, ".git"), 0755)
				expected = []string{filepath.Join(root, ".git")}
				if layout == "ancestor" {
					root = filepath.Join(root, "package")
					os.Mkdir(root, 0755)
				}
				if layout == "protected" {
					protected = []string{".git"}
				}
			case "submodule", "worktree":
				directory := filepath.Join(base, "metadata")
				os.Mkdir(directory, 0755)
				writeFixture(t, filepath.Join(root, ".git"), "gitdir: ../metadata\n")
				expected = []string{directory}
				if layout == "worktree" {
					common := filepath.Join(base, "common")
					os.Mkdir(common, 0755)
					writeFixture(t, filepath.Join(directory, "commondir"), "../common\n")
					expected = append(expected, common)
				}
			case "malformed":
				writeFixture(t, filepath.Join(root, ".git"), "not a gitdir\n")
			case "link":
				writeFixture(t, filepath.Join(base, "private"), "DOCTOR_PRIVATE_BODY\n")
				os.Symlink(filepath.Join(base, "private"), filepath.Join(root, ".git"))
			}
			paths, err := doctorGitDirs(root, protected)
			if layout == "protected" || layout == "malformed" || layout == "link" {
				if err == nil {
					t.Fatal("unsafe or protected metadata was inspected")
				}
			} else if err != nil || !reflect.DeepEqual(paths, expected) {
				t.Fatal(paths, expected, err)
			}
		})
	}
}

func TestDoctorGitDiscoveryIncludesDotDirectoriesAndSkipsProtectedTrees(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{".github/actions/helper", ".agents/skills/helper", "secrets/private"} {
		writeFixture(t, filepath.Join(root, path, ".git"), "gitdir: /not-opened-by-discovery\n")
	}
	roots, err := doctorGitRoots(root, []string{"secrets"})
	expected := []string{root, filepath.Join(root, ".agents/skills/helper"), filepath.Join(root, ".github/actions/helper")}
	if err != nil || !reflect.DeepEqual(roots, expected) {
		t.Fatal(roots, expected, err)
	}
}
