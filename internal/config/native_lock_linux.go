//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireNativeTargetLock(path string) (*nativeTargetLock, error) {
	set := &nativeFileSet{}
	dir, err := set.directory(filepath.Dir(path), true)
	if err != nil {
		set.close(true)
		return nil, err
	}
	fd, err := syscall.Openat(int(dir.file.Fd()), filepath.Base(path), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		set.close(true)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		set.close(false)
		return nil, fmt.Errorf("native lock must be a regular file")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		set.close(false)
		return nil, fmt.Errorf("native target is locked: %w", err)
	}
	if err = set.stable(); err != nil {
		file.Close()
		set.close(false)
		return nil, err
	}
	validate := func() error {
		if err := set.stable(); err != nil {
			return err
		}
		currentFD, err := syscall.Openat(int(dir.file.Fd()), filepath.Base(path), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fmt.Errorf("native lock identity changed: %w", err)
		}
		current := os.NewFile(uintptr(currentFD), path)
		defer current.Close()
		latest, err := current.Stat()
		if err != nil || !os.SameFile(info, latest) {
			return fmt.Errorf("native lock identity changed: %s", path)
		}
		return nil
	}
	return &nativeTargetLock{release: func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = file.Close(); set.close(false) }, validate: validate}, nil
}
