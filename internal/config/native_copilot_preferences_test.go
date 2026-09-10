package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilotPreferenceBoundsAndAtomicTabs(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
	}{
		{"zero-history", map[string]any{"commandHistoryMaxSize": json.Number("0")}},
		{"fractional-history", map[string]any{"commandHistoryMaxSize": json.Number("1.00000000000000000000000000001")}},
		{"large-history", map[string]any{"commandHistoryMaxSize": json.Number("1001")}},
		{"negative-images", map[string]any{"inlineImageLiveWindow": json.Number("-1")}},
		{"inexact-images", map[string]any{"inlineImageLiveWindow": json.Number("9007199254740993")}},
		{"zero-refresh", map[string]any{"statusLine": map[string]any{"refreshInterval": json.Number("0")}}},
		{"large-refresh", map[string]any{"statusLine": map[string]any{"refreshInterval": json.Number("2147484")}}},
		{"fractional-padding", map[string]any{"statusLine": map[string]any{"padding": json.Number("0.5")}}},
		{"empty-command", map[string]any{"statusLine": map[string]any{"command": "  "}}},
		{"unknown-tab", map[string]any{"tabs": map[string]any{"sort": []any{"agents", "unknown"}}}},
		{"hidden-session", map[string]any{"tabs": map[string]any{"hide": []any{"gists", "COPILOT"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := nativeHash(tc.values)
			selected, inactive := nativeSelectConfig("copilot", "user", tc.values)
			if len(selected) != 0 || len(inactive) == 0 || nativeHash(tc.values) != before {
				t.Fatal("invalid preference activated or source mutated", selected, inactive)
			}
			for key, value := range tc.values {
				if nativeMappedValue("copilot", "user", key, value) {
					t.Fatal("direct value mapping accepted invalid preference", key)
				}
			}
			repo := copilotLegacyFixture(t, "user", tc.values)
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err == nil {
				t.Fatal("required invalid preference activated")
			}
			if _, err := os.Stat(filepath.Join(home, "settings.json")); !os.IsNotExist(err) {
				t.Fatal("refusal wrote native settings")
			}
		})
	}
}

func TestCopilotPreferenceValidShapesAndVersionedScope(t *testing.T) {
	values := map[string]any{"commandHistoryMaxSize": json.Number("1000"), "defaultMode": "plan", "inlineImages": false, "inlineImageLiveWindow": json.Number("0"), "notifications": true,
		"statusLine": map[string]any{"type": "command", "command": "/fixture/status", "padding": json.Number("0"), "refreshInterval": json.Number("2147483")},
		"tabs":       map[string]any{"enabled": true, "sort": []any{"Agents", "CoPiLoT"}, "hide": []any{"GISTS"}}}
	selected, inactive := nativeSelectConfig("copilot", "user", values)
	if len(inactive) != 0 || nativeHash(values) != nativeHash(selected) {
		t.Fatal("known preferences inactive", inactive)
	}
	for key, value := range values {
		if !nativeMappedValue("copilot", "user", key, value) {
			t.Fatal("known preference fails direct value mapping", key)
		}
	}
	selected, inactive = nativeSelectConfig("copilot", "project", values)
	if len(selected) != 0 || len(inactive) == 0 {
		t.Fatal("user-only preferences activated in project scope")
	}
	selected, inactive = nativeSelectConfig("copilot", "user", map[string]any{"defaultPermissionMode": "allow-all"})
	if len(selected) != 0 || len(inactive) != 1 || inactive[0].Disposition != "blocked" {
		t.Fatal("permission mode bypassed security gate", selected, inactive)
	}
}

func TestCopilotPreferenceImportApplyReimport(t *testing.T) {
	home, repo, target := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	values := map[string]any{"defaultMode": "plan", "inlineImages": false, "inlineImageLiveWindow": json.Number("12"), "notifications": false, "statusLine": map[string]any{"type": "command", "command": "/fixture/status", "refreshInterval": json.Number("2")}, "tabs": map[string]any{"sort": []any{"AGENTS", "copilot"}, "hide": []any{"gists"}}}
	os.WriteFile(filepath.Join(home, "settings.json"), mustNativeJSON(t, values), 0600)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	projected, err := parseNative([]byte(readNativeTest(t, filepath.Join(target, "settings.json"))), "json")
	if err != nil {
		t.Fatal(err)
	}
	if nativeHash(values) != nativeHash(projected) {
		t.Fatal("preference roundtrip changed native values", projected)
	}
	again := t.TempDir()
	if err := ImportRepositoryWithOptions("copilot", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	data := readNativeTest(t, filepath.Join(again, ".agents/native/com.github.copilot/settings.json"))
	if !strings.Contains(data, "AGENTS") {
		t.Fatal("case-insensitive native selector spelling changed")
	}
}
