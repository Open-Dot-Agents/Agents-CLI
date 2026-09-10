//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNativeAtomicWriteDoesNotFollowDirectoryReplacement(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "native")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{parent, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(parent, "config.toml")
	sentinel := filepath.Join(outside, "config.toml")
	if err := os.WriteFile(sentinel, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := nativeNoSymlinks(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if err := nativeAtomicWrite(target, []byte("projected"), 0600); err == nil {
		t.Fatal("native write followed a replaced parent directory")
	}
	if got := readNativeTest(t, sentinel); got != "outside" {
		t.Fatal("outside configuration changed")
	}
}

func TestNativePinnedRollbackAfterDirectorySwap(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "native")
	outside := filepath.Join(base, "outside")
	for _, path := range []string{parent, outside} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(parent, "config.toml")
	external := filepath.Join(outside, "config.toml")
	os.WriteFile(target, []byte("original"), 0640)
	os.WriteFile(external, []byte("outside"), 0600)
	ownership := filepath.Join(base, "state/ownership.json")
	swapped := false
	err := nativeRunTransaction([]nativeChange{{path: target, data: []byte("new"), mode: 0640}, {path: ownership, data: []byte("state"), mode: 0600}}, func(stage string, index int) error {
		if stage == "after-write" && index == 0 {
			if err := os.Rename(parent, parent+"-saved"); err != nil {
				return err
			}
			if err := os.Symlink(outside, parent); err != nil {
				return err
			}
			swapped = true
		}
		return nil
	})
	if err == nil || !swapped {
		t.Fatal("directory replacement was not detected", err)
	}
	if readNativeTest(t, external) != "outside" || readNativeTest(t, filepath.Join(parent+"-saved", "config.toml")) != "original" {
		t.Fatal("rollback did not use the pinned original directory")
	}
	if info, err := os.Stat(filepath.Join(parent+"-saved", "config.toml")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("rollback changed permissions")
	}
	if _, err := os.Stat(filepath.Dir(ownership)); !os.IsNotExist(err) {
		t.Fatal("failed transaction left its new state directory")
	}
}

func TestNativeRollbackCoversWritesRemovalOwnershipAndBackup(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "config")
	removed := filepath.Join(base, "old-agent")
	state := filepath.Join(base, "state.json")
	backup := filepath.Join(base, "backups/config.bak")
	original := map[string]string{config: "old config", removed: "old agent", state: "old state"}
	for path, data := range original {
		if err := os.WriteFile(path, []byte(data), 0640); err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	err := nativeRunTransaction([]nativeChange{{path: backup, data: []byte("old config"), mode: 0600}, {path: config, data: []byte("new config"), mode: 0600}, {path: removed, remove: true}, {path: state, data: []byte("new state"), mode: 0600}}, func(stage string, index int) error {
		if stage == "after-write" {
			applied++
			if index == 3 {
				return fmt.Errorf("injected commit failure")
			}
		}
		return nil
	})
	if err == nil || applied != 4 {
		t.Fatal("test did not fail after all transaction operation kinds", err, applied)
	}
	for path, want := range original {
		if readNativeTest(t, path) != want {
			t.Fatal("rollback lost content", path)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("rollback lost mode", path)
		}
	}
	if _, err := os.Stat(filepath.Dir(backup)); !os.IsNotExist(err) {
		t.Fatal("new backup directory survived rollback")
	}
}

func TestNativeTransactionRefusesChangedPlanInput(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := nativeFixture(t, "user", "model='a'\n")
	home := t.TempDir()
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	if _, err := ApplyProjection("codex", repo, options); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, ".agents/native/com.openai.codex/config.toml")
	os.WriteFile(source, []byte("model='b'\n"), 0600)
	plan, err := buildNativeProjection("codex", repo, options)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore := readNativeTest(t, plan.registryPath)
	target := filepath.Join(home, "config.toml")
	concurrent := "model='a'\neditor_setting='keep'\n"
	os.WriteFile(target, []byte(concurrent), 0600)
	if err = nativeTransaction(plan.changes); err == nil {
		t.Fatal("a stale plan overwrote a concurrent native edit")
	}
	if readNativeTest(t, target) != concurrent || readNativeTest(t, plan.registryPath) != stateBefore {
		t.Fatal("refused stale plan changed config or ownership")
	}
}

func TestNativeBackupCreationRaceRefused(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := nativeFixture(t, "user", "model='a'\n")
	home := t.TempDir()
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	if _, err := ApplyProjection("codex", repo, options); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, ".agents/native/com.openai.codex/config.toml"), []byte("model='b'\n"), 0600)
	options.Backup = true
	plan, err := buildNativeProjection("codex", repo, options)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "config.toml")
	before := readNativeTest(t, target)
	os.WriteFile(target+".bak", []byte("concurrent backup"), 0600)
	if err = nativeTransaction(plan.changes); err == nil {
		t.Fatal("backup creation race overwrote an existing backup")
	}
	if readNativeTest(t, target+".bak") != "concurrent backup" || readNativeTest(t, target) != before {
		t.Fatal("refused backup race changed files")
	}
}

func TestNativeFilesystemRejectsSpecialFilesAndSymlinkLocks(t *testing.T) {
	base := t.TempDir()
	fifo := filepath.Join(base, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeReadSnapshot(fifo); err == nil {
		t.Fatal("FIFO accepted as configuration")
	}
	if unlock, err := lockNativeTarget(fifo); err == nil {
		unlock()
		t.Fatal("FIFO accepted as lock")
	}
	external := t.TempDir()
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(external, linked); err != nil {
		t.Fatal(err)
	}
	if unlock, err := lockNativeTarget(filepath.Join(linked, "state.lock")); err == nil {
		unlock()
		t.Fatal("lock followed a symlink parent")
	}
	if _, err := os.Stat(filepath.Join(external, "state.lock")); !os.IsNotExist(err) {
		t.Fatal("lock created an external file")
	}
}

func TestNativeRollbackDoesNotOverwriteConcurrentWriter(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "config")
	state := filepath.Join(base, "state")
	os.WriteFile(config, []byte("original config"), 0600)
	os.WriteFile(state, []byte("original state"), 0600)
	err := nativeRunTransaction([]nativeChange{{path: config, data: []byte("new config"), mode: 0600}, {path: state, data: []byte("new state"), mode: 0600}}, func(stage string, index int) error {
		if stage == "after-write" && index == 1 {
			return os.WriteFile(config, []byte("concurrent editor"), 0600)
		}
		return nil
	})
	if err == nil {
		t.Fatal("concurrent modification was reported as a completed transaction")
	}
	if readNativeTest(t, config) != "concurrent editor" {
		t.Fatal("rollback overwrote the concurrent writer")
	}
	if readNativeTest(t, state) != "original state" {
		t.Fatal("ownership state was not restored")
	}
}

func TestNativeImportMergeCarriesSnapshotPreconditions(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agents")
	dir := filepath.Join(root, "native/com.openai.codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("model='a'\n"), 0640)
	merged, err := nativeMergeImport(root, []nativeChange{{path: path, data: []byte("model_reasoning_effort='low'\n"), mode: 0600}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].before == nil {
		t.Fatal("import merge lost its source snapshot")
	}
	os.WriteFile(path, []byte("model='editor-change'\n"), 0640)
	if err = nativeTransaction(merged); err == nil {
		t.Fatal("stale import merge overwrote a newer canonical file")
	}
	if readNativeTest(t, path) != "model='editor-change'\n" {
		t.Fatal("refused import changed the editor's value")
	}
}

func TestNativeTransactionRemainsBoundToLockIdentity(t *testing.T) {
	for _, swap := range []string{"directory", "file"} {
		t.Run(swap, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "state")
			path := filepath.Join(dir, "target.lock")
			lock, err := acquireNativeTargetLock(path)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.release()
			if swap == "directory" {
				if err = os.Rename(dir, dir+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Rename(path, path+"-saved"); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(path, []byte("replacement lock"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(base, "config")
			if err = nativeLockedTransaction([]nativeChange{{path: target, data: []byte("new"), mode: 0600}}, lock); err == nil {
				t.Fatal("transaction used a replaced lock")
			}
			if _, err = os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("replaced lock allowed a write")
			}
		})
	}
}
