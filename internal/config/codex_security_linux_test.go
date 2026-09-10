//go:build linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexMountAliasesRefuse(t *testing.T) {
	base := "1 0 8:1 / / rw - ext4 /dev/root rw\n"
	for name, extra := range map[string]string{
		"filesystem-alias": "2 1 8:1 / /alias rw - ext4 /dev/root rw\n",
		"subtree-alias":    "2 1 8:1 /work/private /alias rw - ext4 /dev/root rw\n",
		"ancestor-alias":   "2 1 8:1 /work /alias rw - ext4 /dev/root rw\n",
		"child-mount":      "2 1 8:2 / /work/private/child rw - ext4 /dev/other rw\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := rejectPolicyMountAliases("/work/private", base+extra); err == nil {
				t.Fatal("mount alias accepted")
			}
		})
	}
	if err := rejectPolicyMountAliases("/work/private", base+"2 1 8:1 /unrelated /other rw - ext4 /dev/root rw\n"); err != nil {
		t.Fatal(err)
	}
	if err := rejectPolicyMountAliases("/work/private", "invalid\n"); err == nil {
		t.Fatal("invalid mount inventory accepted")
	}
}

func TestCodexReadOnlyAliasesRefuse(t *testing.T) {
	root := t.TempDir()
	policy := codexSubsetPolicy(t)
	for _, name := range []string{".git", ".agents", "docs/edit", "private"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "docs", "read.txt")
	writeFixture(t, source, "readonly marker")
	alias := filepath.Join(root, "alias")
	if err := os.Link(source, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("read-only hard-link alias accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "private"), filepath.Join(root, "docs", "link")); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexPolicyPaths(root, *policy.Sandbox); err == nil {
		t.Fatal("read-only symlink accepted")
	}
}
