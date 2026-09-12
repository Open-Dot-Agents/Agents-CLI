package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalInitAndScopedProjection(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			home, project, native := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			withWorkingDirectory(t, project)
			invoke := func(args ...string) string {
				t.Helper()
				var out bytes.Buffer
				if err := run(args, &out, &bytes.Buffer{}); err != nil {
					t.Fatal(args, err, out.String())
				}
				return out.String()
			}
			invoke("init", "--global", "--experimental")
			invoke("validate", "--global", "--experimental")
			for _, path := range []string{filepath.Join(project, ".agents"), filepath.Join(home, "AGENTS.md")} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("global init created a project path", path, err)
				}
			}
			body := "Use global fixture policy.\n"
			if err := os.WriteFile(filepath.Join(home, ".agents/AGENTS.md"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"plan", "--global", "--experimental", "--vendor", vendor, "--native-home", native, "--format", "json"}
			var result struct {
				Applicable bool
				Native     struct{ Scope string }
			}
			if err := json.Unmarshal([]byte(invoke(args...)), &result); err != nil || !result.Applicable || result.Native.Scope != "user" {
				t.Fatal(result, err)
			}
			args[0] = "apply"
			invoke(args...)
			name := "AGENTS.md"
			if vendor == "copilot" {
				name = "copilot-instructions.md"
			}
			path := filepath.Join(native, name)
			data, err := os.ReadFile(path)
			if err != nil || string(data) != body {
				t.Fatal("global core was not projected", string(data), err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("global instructions not private", info, err)
			}
			invoke("import", "--global", "--experimental", "--vendor", vendor, "--native-home", native)
			args[0] = "sync"
			invoke(append(args, "--check")...)
			if _, err := os.Lstat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
				t.Fatal("global command wrote project", err)
			}
		})
	}
}

func TestGlobalFlagsRefuseAmbiguousOrUnsafeScope(t *testing.T) {
	for _, command := range []string{"init", "validate", "import", "plan", "apply", "sync"} {
		for _, extra := range [][]string{nil, {"--experimental", "--root", "."}} {
			t.Run(command+strings.Join(extra, "_"), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				args := append([]string{command, "--global"}, extra...)
				if err := run(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
					t.Fatal("ambiguous global flags accepted", args)
				}
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 0 {
					t.Fatal("refusal changed home", entries, err)
				}
			})
		}
	}
	for _, extra := range [][]string{{"--scope", "project"}, {"--scope", "user"}} {
		err := run(append([]string{"apply", "--global", "--experimental", "--vendor", "codex"}, extra...), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("global write without native home accepted")
		}
	}
}

func TestGlobalInitPreservesOtherConfigurationFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".agents/config.json")
	writeFile(t, path, `{"version":1,"projects":{}}`)
	for _, extra := range [][]string{nil, {"--force"}} {
		err := run(append([]string{"init", "--global", "--experimental"}, extra...), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("legacy global content overwritten")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != `{"version":1,"projects":{}}` {
			t.Fatal("legacy data changed", readErr)
		}
		if _, statErr := os.Stat(filepath.Join(home, ".agents/manifest.json")); !os.IsNotExist(statErr) {
			t.Fatal("partial global init", statErr)
		}
	}
}
