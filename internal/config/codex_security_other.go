//go:build !linux

package config

func checkCodexPolicyPaths(root string, policy SandboxPolicy) error {
	return securityError("native security mapping is Linux-only")
}
