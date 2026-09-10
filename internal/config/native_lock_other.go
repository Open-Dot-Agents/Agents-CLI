//go:build !linux

package config

import "fmt"

func acquireNativeTargetLock(path string) (*nativeTargetLock, error) {
	return nil, fmt.Errorf("native projection requires Linux")
}
