//go:build linux

package config

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDoctorDoesNotOpenProtectedOrCredentialFiles(t *testing.T) {
	home := doctorFixtureEnvironment(t)
	root := practicalFixture(t)
	for _, vendor := range []string{"codex", "copilot"} {
		if _, err := ApplyProjection(vendor, root, ApplyOptions{Experimental: true, DevelopmentOnly: vendor == "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	fd, err := syscall.InotifyInit1(syscall.IN_NONBLOCK | syscall.IN_CLOEXEC)
	if err != nil {
		t.Skipf("file access observation unavailable: %v", err)
	}
	defer syscall.Close(fd)
	for _, path := range []string{
		filepath.Join(root, ".env"), filepath.Join(root, "secrets/private.txt"),
		filepath.Join(home, ".codex/auth.json"), filepath.Join(home, ".copilot/config.json"),
		filepath.Join(home, ".copilot/permissions-config.json"),
	} {
		writeFixture(t, path, "DOCTOR_PRIVATE_BODY")
		if _, err := syscall.InotifyAddWatch(fd, path, syscall.IN_OPEN|syscall.IN_ACCESS); err != nil {
			t.Fatal(err)
		}
	}
	for _, vendor := range []string{"codex", "copilot"} {
		if result := doctorTestResult(t, root, vendor); !result.Ready {
			t.Fatal(result)
		}
	}
	if count, err := syscall.Read(fd, make([]byte, 4096)); count > 0 || !errors.Is(err, syscall.EAGAIN) {
		t.Fatalf("doctor opened a protected file or account store: events=%d error=%v", count, err)
	}
}
