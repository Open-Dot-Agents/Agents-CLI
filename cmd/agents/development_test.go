package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDevelopmentPreset(t *testing.T) {
	root := t.TempDir()
	var out, stderr bytes.Buffer
	if err := run([]string{"init", "--preset", "development", "--enforcement", "strict", "--experimental", "--root", root}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Native settings are not active") {
		t.Fatal(out.String())
	}
	if err := run([]string{"validate", "--experimental", "--root", root}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"plan", "--experimental", "--vendor", "codex", "--root", root}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Local commits: allow", "Push, publish, deploy, other external changes: ask", "ODA-DEVELOPMENT-0001"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %q in %s", expected, out.String())
		}
	}
	for _, command := range []string{"plan", "apply"} {
		args := []string{command, "--experimental", "--vendor", "codex", "--root", root}
		if command == "plan" {
			args = append(args, "--check")
		}
		if err := run(args, &out, &stderr); err == nil {
			t.Fatal("unverified preset returned success", command)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".codex")); !os.IsNotExist(err) {
		t.Fatal("refusal created native configuration", err)
	}
}

func TestRunDevelopmentInitFlags(t *testing.T) {
	for _, args := range [][]string{
		{"init", "--adopt"},
		{"init", "--enforcement", "practical"},
		{"init", "--preset", "development"},
		{"init", "--preset", "development", "--experimental", "--force"},
		{"init", "--preset", "development", "--experimental", "--global"},
		{"init", "--preset", "unknown", "--experimental"},
		{"init", "--preset", "development", "--experimental", "--enforcement", "automatic"},
	} {
		root := t.TempDir()
		var out bytes.Buffer
		if err := run(append(args, "--root", root), &out, &out); err == nil {
			t.Fatal("invalid flags accepted", args)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("invalid flags caused writes", err)
		}
	}
}

func TestRunPracticalDevelopment(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	for _, arguments := range [][]string{
		{"init", "--preset", "development"},
		{"validate"},
		{"plan", "--vendor", "codex"},
		{"apply", "--vendor", "codex"},
		{"plan", "--vendor", "codex", "--check"},
	} {
		if err := run(append(arguments, "--experimental", "--root", root), &out, &out); err != nil {
			t.Fatal(err, out.String())
		}
	}
	if !strings.Contains(out.String(), "agent guidance") {
		t.Fatal("plan hid guidance limits", out.String())
	}
}

func TestRunDevelopmentAdopt(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"init", "--root", root}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"init", "--preset", "development", "--experimental", "--adopt", "--root", root}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", "--experimental", "--root", root}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", "--root", root}, &out, &out); err == nil {
		t.Fatal("migration silently retained stable activation")
	}
}
