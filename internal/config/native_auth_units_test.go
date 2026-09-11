package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAuthenticationRefusalIsAtomic(t *testing.T) {
	for _, tc := range []struct{ name, vendor, kind, content string }{
		{"codex-mcp", "codex", "config", "[mcp_servers.demo]\nurl='https://example.test/mcp'\nhttp_headers.Authorization='Bearer ODA_AUTH_TEST'\n"},
		{"codex-provider", "codex", "config", "[model_providers.demo]\nname='Demo'\nhttp_headers.Authorization='Bearer ODA_AUTH_TEST'\n"},
		{"codex-provider-type", "codex", "config", "[model_providers.demo]\nname='Demo'\nrequires_openai_auth='true'\n"},
		{"codex-url", "codex", "config", "[model_providers.demo]\nname='Demo'\nbase_url='https://user:ODA_AUTH_TEST@example.test/v1'\n"},
		{"codex-query", "codex", "config", "[mcp_servers.demo]\nurl='https://example.test/mcp?access_token=ODA_AUTH_TEST'\n"},
		{"codex-bearer-type", "codex", "config", "[mcp_servers.demo]\nurl='https://example.test/mcp'\nbearer_token_env_var=false\n"},
		{"codex-empty-env-key", "codex", "config", "[model_providers.demo]\nname='Demo'\nenv_key=''\n"},
		{"codex-header-type", "codex", "config", "[model_providers.demo]\nname='Demo'\nhttp_headers=false\n"},
		{"codex-env-header-type", "codex", "config", "[model_providers.demo]\nname='Demo'\nenv_http_headers.Authorization=false\n"},
		{"codex-env-type", "codex", "config", "[mcp_servers.demo]\ncommand='demo'\nenv.DEMO=false\n"},
		{"codex-url-type", "codex", "config", "[mcp_servers.demo]\nurl=false\n"},
		{"codex-malformed-query", "codex", "config", "[mcp_servers.demo]\nurl='https://example.test/mcp?field=%zz'\n"},
		{"copilot-mcp", "copilot", "mcp", `{"mcpServers":{"demo":{"type":"http","url":"https://example.test/mcp","headers":{"Authorization":"Bearer ODA_AUTH_TEST"}}}}`},
		{"copilot-lsp", "copilot", "lsp", `{"lspServers":{"demo":{"command":"demo","fileExtensions":{".go":"go"},"env":{"API_KEY":"ODA_AUTH_TEST"}}}}`},
		{"copilot-lsp-options", "copilot", "lsp", `{"lspServers":{"demo":{"command":"demo","fileExtensions":{".go":"go"},"initializationOptions":{"nested":{"access_token":"ODA_AUTH_TEST"}}}}}`},
	} {
		for _, scope := range []string{"project", "user"} {
			t.Run(tc.name+"/"+scope, func(t *testing.T) {
				repo, home, state := t.TempDir(), t.TempDir(), t.TempDir()
				t.Setenv("XDG_STATE_HOME", state)
				base, nativeHome := repo, ""
				if scope == "user" {
					base, nativeHome = home, home
				}
				a := nativeArtifact{Kind: tc.kind}
				target, format, err := nativeTargetPath(tc.vendor, scope, base, a)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, target, tc.content)
				// Existing canonical policy and all files must survive a forced import.
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Existing instructions.\n")
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"],"requires":["mcp.envRef"]}`)
				if err := os.MkdirAll(filepath.Join(repo, ".agents/native"), 0700); err != nil {
					t.Fatal(err)
				}
				before, beforeHome := instructionSnapshot(t, repo), instructionSnapshot(t, home)
				err = ImportRepositoryWithOptions(tc.vendor, repo, WriteOptions{Experimental: true, Scope: scope, NativeHome: nativeHome, Force: true, Backup: true})
				if err == nil || !strings.Contains(err.Error(), "native authentication") {
					t.Fatalf("expected authentication refusal: %v", err)
				}
				if strings.Contains(err.Error(), "ODA_AUTH_TEST") {
					t.Fatal("credential in diagnostic")
				}
				// Native import keeps a coordination lock outside the canonical tree.
				after := instructionSnapshot(t, repo)
				for path := range after {
					if filepath.Base(path) == ".agents-import.lock" {
						delete(after, path)
					}
				}
				if nativeHash(before) != nativeHash(after) || nativeHash(beforeHome) != nativeHash(instructionSnapshot(t, home)) {
					t.Fatal("import refusal changed files or backups")
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				// An optional native profile must not permit a lossy forced apply.
				ns := "com.openai.codex"
				if tc.vendor == "copilot" {
					ns = "com.github.copilot"
				}
				dir := filepath.Join(repo, ".agents", "native", ns)
				source := "source." + format
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native"]}`)
				writeFixture(t, filepath.Join(dir, "profile.json"), fmt.Sprintf(`{"namespace":%q,"harness_version":%q,"scope":%q,"required":false,"artifacts":[{"kind":%q,"source":%q}]}`, ns, "="+nativePinnedVersion(tc.vendor), scope, tc.kind, source))
				writeFixture(t, filepath.Join(dir, source), tc.content)
				before, beforeHome = instructionSnapshot(t, repo), instructionSnapshot(t, home)
				beforeState := instructionSnapshot(t, state)
				_, err = ApplyProjection(tc.vendor, repo, ApplyOptions{Experimental: true, Scope: scope, NativeHome: nativeHome, Force: true, Backup: true})
				if err == nil || !strings.Contains(err.Error(), "native authentication") {
					t.Fatalf("optional apply did not refuse authentication loss: %v", err)
				}
				if strings.Contains(err.Error(), "ODA_AUTH_TEST") {
					t.Fatal("credential in diagnostic")
				}
				if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) || nativeHash(beforeHome) != nativeHash(instructionSnapshot(t, home)) || nativeHash(beforeState) != nativeHash(instructionSnapshot(t, state)) {
					t.Fatal("apply refusal changed files, ownership, or backups")
				}
			})
		}
	}
}

func TestNativeAuthenticationReferencesSurviveSelection(t *testing.T) {
	for _, tc := range []struct{ vendor, kind, source string }{
		{"codex", "config", `{"model_providers":{"demo":{"name":"Demo","env_key":"MODEL_TOKEN","requires_openai_auth":true,"env_http_headers":{"Authorization":"HEADER_TOKEN"}}}}`},
		{"codex", "config", `{"mcp_servers":{"demo":{"url":"https://example.test/mcp","bearer_token_env_var":"MCP_TOKEN","env_http_headers":{"Authorization":"HEADER_TOKEN"}}}}`},
		{"copilot", "mcp", `{"mcpServers":{"demo":{"type":"http","url":"https://example.test/mcp","headers":{"Authorization":"${HEADER_TOKEN}"},"env":{"API_KEY":"$API_KEY"}}}}`},
		{"copilot", "lsp", `{"lspServers":{"demo":{"command":"demo","fileExtensions":{".go":"go"},"env":{"API_KEY":"${API_KEY}"},"initializationOptions":{"authenticationMode":"native","tokenTypes":["string"]}}}}`},
	} {
		t.Run(tc.vendor+"/"+tc.kind, func(t *testing.T) {
			var values map[string]any
			if err := json.Unmarshal([]byte(tc.source), &values); err != nil {
				t.Fatal(err)
			}
			if err := nativeCheckRuntimeAuthentication(tc.vendor, values); err != nil {
				t.Fatal(err)
			}
			filtered, excluded := nativeImportFilter(tc.vendor, values, nil)
			if len(excluded) != 0 {
				t.Fatalf("references excluded: %v", excluded)
			}
			var selected map[string]any
			var inactive []nativeInactiveField
			switch tc.kind {
			case "lsp":
				selected, inactive = nativeSelectLSP(filtered)
			case "mcp":
				var err error
				selected, inactive, err = nativeSelectCopilotMCP(filtered)
				if err != nil {
					t.Fatal(err)
				}
			default:
				selected, inactive = nativeSelectConfig(tc.vendor, "user", filtered)
			}
			if len(inactive) != 0 || nativeHash(values) != nativeHash(selected) {
				t.Fatalf("authentication references changed: %v", inactive)
			}
		})
	}
}

func TestNativeAgentImportAuthenticationRefusal(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	writeFixture(t, filepath.Join(home, "config.toml"), "model='fixture'\n")
	writeFixture(t, filepath.Join(home, "agents/demo.toml"), "name='Demo'\ndescription='Demo'\ndeveloper_instructions='Demo'\n[model_providers.demo]\nname='Demo'\nhttp_headers.Authorization='ODA_AUTH_TEST'\n")
	err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true, Backup: true})
	if err == nil || !strings.Contains(err.Error(), "native authentication") || strings.Contains(err.Error(), "ODA_AUTH_TEST") {
		t.Fatalf("expected redacted agent refusal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
		t.Fatal("refusal created canonical content")
	}
}

func TestNativeImportMustNotRemoveRuntimeAuthentication(t *testing.T) {
	for _, tc := range []struct{ name, vendor, filename, content string }{
		{"codex-mcp", "codex", "config.toml", "[mcp_servers.demo]\nurl='https://example.test/mcp'\nhttp_headers.Authorization='Bearer ODA_AUTH_TEST'\n"},
		{"codex-provider", "codex", "config.toml", "model_provider='demo'\n[model_providers.demo]\nname='Demo'\nbase_url='https://example.test/v1'\nhttp_headers.Authorization='Bearer ODA_AUTH_TEST'\n"},
		{"copilot-mcp", "copilot", "mcp-config.json", `{"mcpServers":{"demo":{"type":"http","url":"https://example.test/mcp","headers":{"Authorization":"Bearer ODA_AUTH_TEST"}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			writeFixture(t, filepath.Join(home, tc.filename), tc.content)
			err := ImportRepositoryWithOptions(tc.vendor, root, WriteOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true, Backup: true})
			if err == nil {
				t.Fatal("import removed runtime authentication and reported success")
			}
			if strings.Contains(err.Error(), "ODA_AUTH_TEST") {
				t.Fatal("credential value in diagnostic")
			}
			if _, err := os.Lstat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
				t.Fatalf("refusal wrote canonical content: %v", err)
			}
			if readNativeTest(t, filepath.Join(home, tc.filename)) != tc.content {
				t.Fatal("source changed")
			}
		})
	}
}

func TestNativeProviderAuthRequirementIsConfiguration(t *testing.T) {
	for _, value := range []bool{false, true} {
		values := map[string]any{"model_provider": "demo", "model_providers": map[string]any{"demo": map[string]any{"name": "Demo", "base_url": "https://example.test/v1", "requires_openai_auth": value}}}
		filtered, excluded := nativeImportFilter("codex", values, nil)
		if len(excluded) != 0 {
			t.Fatalf("authentication requirement was treated as a credential: %v", excluded)
		}
		selected, inactive := nativeSelectConfig("codex", "user", filtered)
		if len(inactive) != 0 {
			t.Fatalf("authentication requirement was not mapped: %v", inactive)
		}
		if selected["model_providers"].(map[string]any)["demo"].(map[string]any)["requires_openai_auth"] != value {
			t.Fatal("provider authentication requirement was removed")
		}
	}
}
