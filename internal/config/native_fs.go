package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"reflect"
)

type nativeSnapshot struct {
	uid, gid   int
	ownerKnown bool
	data       []byte
	mode       fs.FileMode
	exists     bool
}

func (s nativeSnapshot) equal(other nativeSnapshot) bool {
	return s.exists == other.exists && (!s.exists || s.mode == other.mode && bytes.Equal(s.data, other.data) && (!s.ownerKnown || !other.ownerKnown || s.uid == other.uid && s.gid == other.gid))
}

// Hooks are passed explicitly by filesystem race tests. Production uses nil.
// No package-global injection point is shared between concurrent projections.
type nativeTransactionHook func(stage string, index int) error

func nativeReadFile(path string) ([]byte, error) {
	snapshot, err := nativeReadSnapshot(path)
	if err != nil {
		return nil, err
	}
	if !snapshot.exists {
		return nil, &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return snapshot.data, nil
}
func nativeDecodePolicyData(path string, data []byte, target any) error {
	if err := nativeUniqueJSON(data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := exactPolicyFields(data, reflect.TypeOf(target)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
func nativeDecodePolicy(path string, target any) error {
	data, err := nativeReadFile(path)
	if err != nil {
		return err
	}
	return nativeDecodePolicyData(path, data, target)
}

// Keep the lock identity bound to the transaction, not only its pathname.
type nativeTargetLock struct {
	release  func()
	validate func() error
}

func lockNativeTarget(path string) (func(), error) {
	lock, err := acquireNativeTargetLock(path)
	if err != nil {
		return nil, err
	}
	return lock.release, nil
}
func nativeLockedTransaction(changes []nativeChange, lock *nativeTargetLock) error {
	return nativeRunTransaction(changes, func(stage string, index int) error { return lock.validate() })
}
