package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCodexProjectRestrictionsRefuseOrStayInactive(t *testing.T) {
	for key, field := range map[string]string{
		"openai_base_url":        "openai_base_url='https://example.test/v1'\n",
		"chatgpt_base_url":       "chatgpt_base_url='https://example.test'\n",
		"apps_mcp_product_sku":   "apps_mcp_product_sku='fixture'\n",
		"responses_api_metadata": "responses_api_metadata.fixture='project'\n",
		"model_provider":         "model_provider='fixture'\n",
		"model_providers":        "model_providers.fixture.name='Fixture'\n",
		"notify":                 "notify=['/bin/true']\n",
		"profile":                "profile='fixture'\n",
		"profiles":               "profiles.fixture.model='fixture'\n",
		"experimental_realtime_webrtc_call_base_url": "experimental_realtime_webrtc_call_base_url='https://example.test'\n",
		"experimental_realtime_ws_base_url":          "experimental_realtime_ws_base_url='wss://example.test'\n",
		"otel":                                       "otel.environment='fixture'\n",
		"features.respect_system_proxy":              "features.respect_system_proxy=true\n",
	} {
		t.Run(key, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			writeFixture(t, filepath.Join(home, "config.toml"), "model='user-owned'\n")
			data := "model='project-model'\n" + field
			repo := nativeFixture(t, "project", data)
			before := instructionSnapshot(t, repo)
			options := ApplyOptions{Experimental: true, Force: true, Backup: true}
			if _, err := ApplyProjection("codex", repo, options); err == nil || !strings.Contains(err.Error(), "ignores project") {
				t.Fatalf("required ignored key was not refused: %v", err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
				t.Fatal("required refusal wrote files or backups")
			}
			profile := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
			writeFixture(t, profile, strings.ReplaceAll(readNativeTest(t, profile), `"required":true`, `"required":false`))
			plan, err := ApplyProjection("codex", repo, options)
			if err != nil {
				t.Fatal(err)
			}
			values, err := parseNative([]byte(readNativeTest(t, filepath.Join(repo, ".codex/config.toml"))), "toml")
			if err != nil || len(values) != 1 || values["model"] != "project-model" {
				t.Fatal("optional ignored field became active", values, err)
			}
			found := false
			for _, feature := range plan.Native.Features {
				if feature.Activation == "inactive" && strings.Contains(feature.Limitation, "ignores project") {
					found = true
				}
			}
			if !found {
				t.Fatal("native scope limit missing from plan")
			}
			if readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/config.toml")) != data {
				t.Fatal("optional canonical field was lost")
			}
			if readNativeTest(t, filepath.Join(home, "config.toml")) != "model='user-owned'\n" {
				t.Fatal("project operation changed user config")
			}
		})
	}
}

func TestNativeCodexIgnoredProjectOwnershipCleanup(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "modified"}[modified], func(t *testing.T) {
			repo := nativeFixture(t, "project", "model_provider='legacy'\n")
			root := filepath.Join(repo, ".agents")
			profile := filepath.Join(root, "native/com.openai.codex/profile.json")
			writeFixture(t, profile, strings.ReplaceAll(readNativeTest(t, profile), `"required":true`, `"required":false`))
			path := filepath.Join(repo, ".codex/config.toml")
			value := "legacy"
			if modified {
				value = "external-edit"
			}
			writeFixture(t, path, "model='unowned-model'\nmodel_provider='"+value+"'\n")
			if err := os.Chmod(path, 0640); err != nil {
				t.Fatal(err)
			}
			state := nativeRegistry{Version: NativeVersion, Vendor: "codex", Home: "", Settings: map[string]nativeOwned{nativeKey(path, "/model_provider"): {Source: root, Hash: nativeHash("legacy")}}}
			statePath := filepath.Join(root, "state/native-codex.json")
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, statePath, string(encoded))
			before := instructionSnapshot(t, repo)
			options := ApplyOptions{Experimental: true}
			if modified {
				if _, err := ApplyProjection("codex", repo, options); err == nil {
					t.Fatal("changed owned setting was removed without force")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("conflict refusal wrote files")
				}
				options.Force, options.Backup = true, true
				if _, err := ApplyProjection("codex", repo, options); err == nil {
					t.Fatal("force bypassed a changed owned setting")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
					t.Fatal("forced conflict refusal wrote files or backups")
				}
				return
			}
			if _, err := ApplyProjection("codex", repo, options); err != nil {
				t.Fatal(err)
			}
			got, err := parseNative([]byte(readNativeTest(t, path)), "toml")
			if err != nil || len(got) != 1 || got["model"] != "unowned-model" {
				t.Fatal("cleanup changed unowned configuration", got, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal("cleanup changed file permissions")
			}
			state = nativeRegistry{}
			if err := nativeDecodePolicy(statePath, &state); err != nil {
				t.Fatal(err)
			}
			if _, stale := state.Settings[nativeKey(path, "/model_provider")]; stale {
				t.Fatal("stale provider ownership remains")
			}
			if _, claimed := state.Settings[nativeKey(path, "/model")]; claimed {
				t.Fatal("cleanup claimed the unowned model")
			}
		})
	}
}

func TestNativeCodexScopeDeclarations(t *testing.T) {
	seen := map[string]bool{}
	for _, declaration := range nativeSettingDeclarations("codex") {
		parts := nativePatternParts(declaration.Path)
		if nativeCodexProjectRestriction(parts) == "" {
			continue
		}
		for _, scope := range declaration.Scopes {
			if scope == "project" {
				t.Fatalf("ignored field advertises project scope: %s", declaration.Path)
			}
		}
		seen[declaration.Path] = true
	}
	for key := range nativeCodexProjectIgnored {
		if !seen[key] {
			t.Fatalf("scope declaration not checked: %s", key)
		}
	}
}
