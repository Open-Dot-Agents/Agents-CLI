//go:build linux

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type nativeDirectory struct {
	file    *os.File
	parent  *nativeDirectory
	name    string
	created bool
}

type nativeFileSet struct {
	directories map[string]*nativeDirectory
	order       []*nativeDirectory
}

func (set *nativeFileSet) directory(path string, create bool) (*nativeDirectory, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if set.directories == nil {
		set.directories = map[string]*nativeDirectory{}
	}
	if dir := set.directories[absolute]; dir != nil {
		return dir, nil
	}
	dir := &nativeDirectory{name: filepath.Base(absolute)}
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd := -1
	if absolute == "/" {
		fd, err = syscall.Open("/", flags, 0)
	} else {
		dir.parent, err = set.directory(filepath.Dir(absolute), create)
		if err != nil {
			return nil, err
		}
		fd, err = syscall.Openat(int(dir.parent.file.Fd()), dir.name, flags, 0)
		if errors.Is(err, syscall.ENOENT) && create {
			if err = syscall.Mkdirat(int(dir.parent.file.Fd()), dir.name, 0700); err != nil {
				return nil, err
			}
			dir.created = true
			fd, err = syscall.Openat(int(dir.parent.file.Fd()), dir.name, flags, 0)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open native directory %s without symlinks: %w", absolute, err)
	}
	dir.file = os.NewFile(uintptr(fd), absolute)
	set.directories[absolute] = dir
	set.order = append(set.order, dir)
	return dir, nil
}

func (set *nativeFileSet) close(cleanup bool) {
	for i := len(set.order) - 1; i >= 0; i-- {
		dir := set.order[i]
		if cleanup && dir.created && dir.parent != nil {
			// Only remove the same directory, and only if it is still empty.
			fd, err := syscall.Openat(int(dir.parent.file.Fd()), dir.name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			if err == nil {
				current := os.NewFile(uintptr(fd), dir.name)
				want, e1 := dir.file.Stat()
				got, e2 := current.Stat()
				if e1 == nil && e2 == nil && os.SameFile(want, got) {
					_ = nativeUnlinkDirectory(int(dir.parent.file.Fd()), dir.name)
				}
				current.Close()
			}
		}
		dir.file.Close()
	}
}

func nativeUnlinkDirectory(fd int, name string) error {
	pointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	// Linux unlinkat AT_REMOVEDIR; syscall.Unlinkat exposes only flags=0.
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(fd), uintptr(unsafe.Pointer(pointer)), 0x200)
	if errno != 0 {
		return errno
	}
	return nil
}

func (set *nativeFileSet) stable() error {
	check := &nativeFileSet{}
	defer check.close(false)
	for path, dir := range set.directories {
		current, err := check.directory(path, false)
		if err != nil {
			return fmt.Errorf("native directory changed during transaction: %w", err)
		}
		before, e1 := dir.file.Stat()
		after, e2 := current.file.Stat()
		if e1 != nil || e2 != nil || !os.SameFile(before, after) {
			return fmt.Errorf("native directory changed during transaction: %s", path)
		}
	}
	return nil
}

func nativeReadAt(dir *nativeDirectory, name string) (nativeSnapshot, error) {
	fd, err := syscall.Openat(int(dir.file.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nativeSnapshot{}, nil
	}
	if err != nil {
		return nativeSnapshot{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nativeSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return nativeSnapshot{}, fmt.Errorf("native target must be a regular file: %s", name)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nativeSnapshot{}, err
	}
	after, err := file.Stat()
	if err != nil {
		return nativeSnapshot{}, err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
		return nativeSnapshot{}, fmt.Errorf("native file changed while reading: %s", name)
	}
	ownership := info.Sys().(*syscall.Stat_t)
	return nativeSnapshot{data: data, mode: info.Mode().Perm(), exists: true, uid: int(ownership.Uid), gid: int(ownership.Gid), ownerKnown: true}, nil
}

func nativeReadSnapshot(path string) (nativeSnapshot, error) {
	set := &nativeFileSet{}
	defer set.close(false)
	dir, err := set.directory(filepath.Dir(path), false)
	if errors.Is(err, syscall.ENOENT) {
		return nativeSnapshot{}, nil
	}
	if err != nil {
		return nativeSnapshot{}, err
	}
	return nativeReadAt(dir, filepath.Base(path))
}

func nativeWriteAt(dir *nativeDirectory, name string, data []byte, mode fs.FileMode, owner nativeSnapshot) (bool, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return false, err
	}
	temporary := ".agents-native-" + hex.EncodeToString(token)
	fd, err := syscall.Openat(int(dir.file.Fd()), temporary, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return false, err
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer syscall.Unlinkat(int(dir.file.Fd()), temporary)
	if owner.exists && owner.ownerKnown {
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return false, statErr
		}
		actual := info.Sys().(*syscall.Stat_t)
		if int(actual.Uid) != owner.uid || int(actual.Gid) != owner.gid {
			if err = file.Chown(owner.uid, owner.gid); err != nil {
				file.Close()
				return false, err
			}
		}
	}
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	if err = syscall.Renameat(int(dir.file.Fd()), temporary, int(dir.file.Fd()), name); err != nil {
		return false, err
	}
	return true, dir.file.Sync()
}

func nativeRunTransaction(changes []nativeChange, hook nativeTransactionHook) error {
	set := &nativeFileSet{}
	success := false
	defer func() { set.close(!success) }()
	type operation struct {
		change        nativeChange
		dir           *nativeDirectory
		name          string
		before, after nativeSnapshot
	}
	operations := make([]operation, 0, len(changes))
	seen := map[string]bool{}
	// Reject duplicates before creating any directories.
	for _, change := range changes {
		path, err := filepath.Abs(change.path)
		if err != nil {
			return err
		}
		if seen[path] {
			return fmt.Errorf("duplicate native transaction target: %s", path)
		}
		seen[path] = true
	}
	for _, change := range changes {
		dir, err := set.directory(filepath.Dir(change.path), true)
		if err != nil {
			return err
		}
		name := filepath.Base(change.path)
		before, err := nativeReadAt(dir, name)
		if err != nil {
			return err
		}
		if change.before != nil && !change.before.equal(before) {
			return fmt.Errorf("native target changed after planning: %s", change.path)
		}
		operations = append(operations, operation{change: change, dir: dir, name: name, before: before})
	}
	var done []int
	rollback := func(cause error) error {
		failures := []error{cause}
		for i := len(done) - 1; i >= 0; i-- {
			op := operations[done[i]]
			current, err := nativeReadAt(op.dir, op.name)
			if err != nil || !current.equal(op.after) {
				failures = append(failures, fmt.Errorf("native rollback target changed: %s", op.change.path))
				continue
			}
			if op.before.exists {
				_, err = nativeWriteAt(op.dir, op.name, op.before.data, op.before.mode, op.before)
			} else {
				err = syscall.Unlinkat(int(op.dir.file.Fd()), op.name)
			}
			if err != nil && !errors.Is(err, syscall.ENOENT) {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	if hook != nil {
		if err := hook("prepared", -1); err != nil {
			return err
		}
	}
	for i := range operations {
		op := &operations[i]
		if hook != nil {
			if err := hook("before-write", i); err != nil {
				return rollback(err)
			}
		}
		if err := set.stable(); err != nil {
			return rollback(err)
		}
		current, err := nativeReadAt(op.dir, op.name)
		if err != nil {
			return rollback(err)
		}
		if !current.equal(op.before) {
			return rollback(fmt.Errorf("native target changed before write: %s", op.change.path))
		}
		changed := false
		if op.change.remove {
			if current.exists {
				err = syscall.Unlinkat(int(op.dir.file.Fd()), op.name)
				changed = err == nil
			}
			op.after = nativeSnapshot{}
		} else {
			changed, err = nativeWriteAt(op.dir, op.name, op.change.data, op.change.mode, op.before)
			op.after = nativeSnapshot{data: op.change.data, mode: op.change.mode.Perm(), exists: true, uid: op.before.uid, gid: op.before.gid, ownerKnown: op.before.ownerKnown}
		}
		if changed {
			done = append(done, i)
		}
		if err != nil {
			return rollback(err)
		}
		if hook != nil {
			if err := hook("after-write", i); err != nil {
				return rollback(err)
			}
		}
	}
	if err := set.stable(); err != nil {
		return rollback(err)
	}
	for _, op := range operations {
		current, err := nativeReadAt(op.dir, op.name)
		if err != nil {
			return rollback(err)
		}
		if !current.equal(op.after) {
			return rollback(fmt.Errorf("native target changed before transaction completed: %s", op.change.path))
		}
	}
	success = true
	return nil
}

func nativeMkdirAll(path string) error {
	set := &nativeFileSet{}
	success := false
	defer func() { set.close(!success) }()
	if _, err := set.directory(path, true); err != nil {
		return err
	}
	if err := set.stable(); err != nil {
		return err
	}
	success = true
	return nil
}
