package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copilotLegacyFixture(t *testing.T, scope string, values map[string]any) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".agents/native/com.github.copilot")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	profile := nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: true, Artifacts: []nativeArtifact{{Kind: "config", Source: "settings.json"}}}
	metadata, _ := json.Marshal(profile)
	for path, data := range map[string][]byte{
		filepath.Join(repo, ".agents/manifest.json"): []byte(`{"version":"1.1.0-draft.2","profiles":["native"]}`),
		filepath.Join(repo, ".agents/AGENTS.md"):     []byte("Fixture instructions.\n"),
		filepath.Join(dir, "profile.json"):           metadata,
		filepath.Join(dir, "settings.json"):          mustNativeJSON(t, values),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestCopilotLegacyImportPrecedenceAndState(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-only", true: "root-replacement"}[modern], func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			legacy := `// Native application file with old preferences.
{"theme":"dim","memory":false,"bannerStyle":"classic","ide":{"autoConnect":false},"trustedFolders":["/external/trusted"],"firstLaunchAt":1234,"staff":false,"loggedInUsers":[{"token":"sentinel-account-secret"}],"unknownFutureState":"do-not-copy"}`
			os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0640)
			if modern {
				os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"theme":"github","memory":true,"ide":{"openDiffOnEdit":true},"companyAnnouncements":["fixture"]}`), 0600)
			}
			before := portabilitySnapshot(t, home)
			if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
				t.Fatal(err)
			}
			data := readNativeTest(t, filepath.Join(repo, ".agents/native/com.github.copilot/settings.json"))
			values, err := parseNative([]byte(data), "json")
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"theme": "dim", "memory": false, "bannerStyle": "classic", "ide": map[string]any{"autoConnect": false}}
			if modern {
				want["companyAnnouncements"] = []any{"fixture"}
			}
			if nativeHash(values) != nativeHash(want) {
				t.Fatal("legacy precedence or exclusion differs", values)
			}
			if strings.Contains(data, "sentinel") || strings.Contains(data, "trustedFolders") || strings.Contains(data, "unknownFutureState") {
				t.Fatal("external application state copied")
			}
			if nativeHash(before) != nativeHash(portabilitySnapshot(t, home)) {
				t.Fatal("import changed legacy state or preferences")
			}
			target := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
				t.Fatal(err)
			}
			projected, err := parseNative([]byte(readNativeTest(t, filepath.Join(target, "settings.json"))), "json")
			if err != nil {
				t.Fatal(err)
			}
			if nativeHash(projected) != nativeHash(want) {
				t.Fatal("legacy preferences were not projected", projected)
			}
		})
	}
}

func TestCopilotLegacyBlocksOverwrittenSettingsAndRemoval(t *testing.T) {
	for _, mode := range []string{"write", "remove", "object-child"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			values := map[string]any{"memory": false}
			legacy := `{"memory":true,"trustedFolders":["/native/trust"]}`
			if mode == "object-child" {
				values = map[string]any{"ide": map[string]any{"openDiffOnEdit": false}}
				legacy = `{"ide":{"autoConnect":false}}`
			}
			repo := copilotLegacyFixture(t, "user", values)
			if mode == "remove" {
				if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
			}
			os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0600)
			before := portabilitySnapshot(t, home)
			_, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true, Adopt: true, Backup: true})
			if err == nil || !strings.Contains(err.Error(), "would replace setting") {
				t.Fatal("legacy override not refused", err)
			}
			if nativeHash(before) != nativeHash(portabilitySnapshot(t, home)) {
				t.Fatal("refusal changed native files")
			}
		})
	}
}

func TestCopilotLegacyEqualValuesAndProjectIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	legacy := `{"memory":false,"trustedFolders":["/native/trust"]}`
	os.WriteFile(filepath.Join(home, "config.json"), []byte(legacy), 0600)
	repo := copilotLegacyFixture(t, "user", map[string]any{"memory": false})
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(home, "config.json")) != legacy {
		t.Fatal("equal projection migrated native state")
	}
	// Project operations do not parse an unrelated malformed user state file.
	os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"malformed"`), 0600)
	t.Setenv("HOME", home)
	t.Setenv("COPILOT_HOME", home)
	repo = copilotLegacyFixture(t, "project", map[string]any{"companyAnnouncements": []any{"one", "two"}})
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCopilotLegacyMalformedRefusesBeforeImportWrites(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"memory":false}`), 0600)
	os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"sentinel-secret"`), 0600)
	err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home})
	if err == nil || strings.Contains(err.Error(), "sentinel-secret") {
		t.Fatal("malformed legacy data was not safely refused", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
		t.Fatal("refused import wrote a canonical tree")
	}
}

func TestCopilotKnownContainersAndStringArrays(t *testing.T) {
	values := map[string]any{"ide": map[string]any{"autoConnect": false}, "subagents": map[string]any{"maxDepth": json.Number("2")}, "tabs": map[string]any{"sort": []any{"agents", "copilot"}}, "companyAnnouncements": []any{"one", "two"}}
	selected, inactive := nativeSelectConfig("copilot", "user", values)
	if len(inactive) != 0 || nativeHash(values) != nativeHash(selected) {
		t.Fatal("known containers or string arrays are inactive", inactive)
	}
	for key, value := range selected {
		if !nativeMappedValue("copilot", "user", key, value) {
			t.Fatal("selected field fails value mapping", key)
		}
	}
	for _, values := range []map[string]any{
		{"ide": map[string]any{"unknown": true}},
		{"tabs": map[string]any{"sort": []any{map[string]any{"secret": "sentinel"}}}},
	} {
		if selected, inactive := nativeSelectConfig("copilot", "user", values); len(inactive) == 0 || len(selected) != 0 {
			t.Fatal("unknown descendants were activated", selected, inactive)
		}
	}
	if nativeKnownConfigPath("copilot", []string{"companyAnnouncements", "named"}) {
		t.Fatal("array accepts a named field")
	}
}
