package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Source selection and native destination scope are separate. A global
// command must never infer permission to write a native home.
func resolveGlobalRoot(flags *flag.FlagSet, global, experimental bool, root, scope *string) error {
	if !global {
		return nil
	}
	if !experimental {
		return fmt.Errorf("--global requires --experimental and the draft.2 contract")
	}
	explicitRoot := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "root" {
			explicitRoot = true
		}
	})
	if explicitRoot {
		return fmt.Errorf("--global and --root are separate source selectors; choose one")
	}
	if scope != nil {
		if *scope != "" && *scope != "user" {
			return fmt.Errorf("--global requires user scope")
		}
		*scope = "user"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("--global requires an absolute user home")
	}
	*root = filepath.Clean(home)
	return nil
}
