package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Open-Dot-Agents/Agents-CLI/internal/config"
)

func TestDoctorArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--vendor", "codex"}, {"--experimental"},
		{"--experimental", "--vendor", "all"}, {"--experimental", "--vendor", "claude"},
		{"--experimental", "--vendor", "codex", "--format", "yaml"},
		{"--experimental", "--vendor", "codex", "extra"},
		{"--experimental", "--vendor", "codex", "--repair"},
		{"--experimental", "--vendor", "codex", "--global"},
	} {
		var output bytes.Buffer
		if err := run(append([]string{"doctor"}, args...), &output, &bytes.Buffer{}); err == nil || output.Len() != 0 {
			t.Fatalf("invalid arguments accepted: %v: %s", args, &output)
		}
	}
	var help bytes.Buffer
	if err := run([]string{"doctor", "--help"}, &bytes.Buffer{}, &help); err != nil || !strings.Contains(help.String(), "-vendor") {
		t.Fatal(err, &help)
	}
}

func TestDoctorOutputAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	binary := filepath.Join(home, "native")
	writeFile(t, binary, "#!/bin/sh\necho launched > \"$HOME/accidental-launch\"\nexit 99\n")
	if err := os.Chmod(binary, 0755); err != nil {
		t.Fatal(err)
	}
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			t.Setenv(strings.ToUpper(vendor)+"_BIN", binary)
			root := filepath.Join(t.TempDir(), "project 'quoted' $literal")
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
				t.Fatal(err)
			}
			withWorkingDirectory(t, root)
			args := []string{"doctor", "--experimental", "--vendor", vendor}
			inspect := func(state string, ready bool) {
				t.Helper()
				var encoded, plain bytes.Buffer
				errJSON := run(append(args, "--format", "json"), &encoded, &bytes.Buffer{})
				errText := run(args, &plain, &bytes.Buffer{})
				var report config.DoctorResult
				if err := json.Unmarshal(encoded.Bytes(), &report); err != nil {
					t.Fatal(err, &encoded)
				}
				if (errJSON == nil) != ready || (errText == nil) != ready || report.Ready != ready || report.ConfigurationState != state || report.Root != root || report.SchemaVersion != "1.0.0" {
					t.Fatal(errJSON, errText, &encoded, &plain)
				}
				if (report.Scope == "development-only") != (vendor == "codex") || !strings.Contains(plain.String(), report.Scope) {
					t.Fatal("projection scope differs", report, &plain)
				}
				for _, check := range report.Checks {
					if !strings.Contains(plain.String(), check.Status+"\t"+check.ID+"\t"+check.Message) {
						t.Fatal("text and JSON checks differ", check, &plain)
					}
					if check.Layer == "active-session" && check.Status != "unknown" {
						t.Fatal("inspection claimed runtime authority", check)
					}
				}
				if state == "missing" && !strings.Contains(plain.String(), "'project") && !strings.Contains(plain.String(), "'\"'\"'quoted") {
					t.Fatal("suggested command did not quote the root", &plain)
				}
			}
			inspect("missing", false)
			if err := run([]string{"init", "--preset", "development", "--experimental"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			inspect("needs-apply", false)
			apply := []string{"apply", "--experimental", "--vendor", vendor}
			if vendor == "codex" {
				apply = append(apply, "--preset", "development")
			}
			if err := run(apply, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			inspect("current", true)
			t.Setenv(strings.ToUpper(vendor)+"_BIN", filepath.Join(home, "absent"))
			inspect("current", false)
		})
	}
	if _, err := os.Stat(filepath.Join(home, "accidental-launch")); !os.IsNotExist(err) {
		t.Fatal("doctor launched a native executable")
	}
}

func TestDoctorCommandQuoting(t *testing.T) {
	for input, expected := range map[string]string{
		"agents": "agents", "": "''", "a b": "'a b'", "a'b": "'a'\"'\"'b'",
		"$(command)": "'$(command)'", "a\nb": "'a\nb'",
	} {
		if actual := doctorShellArgument(input); actual != expected {
			t.Fatal(input, actual, expected)
		}
	}
}
