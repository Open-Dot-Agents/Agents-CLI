//go:build linux

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeLockAndPrivateBackup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	repo := nativeFixture(t, "user", "model = \"a\"\n")
	o := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	b, e := buildNativeProjection("codex", repo, o)
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Dir(b.registryPath), 0700)
	unlock, e := lockNativeTarget(b.registryPath + ".lock")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := ApplyProjection("codex", repo, o); e == nil {
		unlock()
		t.Fatal("target lock bypassed")
	}
	unlock()
	if _, e := ApplyProjection("codex", repo, o); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, "config.toml")
	os.Chmod(path, 0640)
	os.WriteFile(filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"), []byte("model = \"b\"\n"), 0600)
	o.Backup = true
	if _, e := ApplyProjection("codex", repo, o); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("existing mode changed")
	}
	info, e = os.Stat(path + ".bak")
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("backup not private: %v", e)
	}
}
