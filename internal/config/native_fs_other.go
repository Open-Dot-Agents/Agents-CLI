//go:build !linux

package config

import (
	"fmt"
	"os"
)

func nativeRunTransaction(changes []nativeChange, hook nativeTransactionHook) error {
	return fmt.Errorf("native transactions require Linux")
}
func nativeReadSnapshot(path string) (nativeSnapshot, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nativeSnapshot{}, nil
	}
	if err != nil {
		return nativeSnapshot{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nativeSnapshot{}, err
	}
	return nativeSnapshot{data: data, mode: info.Mode().Perm(), exists: true}, nil
}
func nativeMkdirAll(path string) error { return fmt.Errorf("native transactions require Linux") }
