package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeProviderCommandAuthenticationRoundTrip(t *testing.T) {
	home, target, repo, again := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	commandDir := t.TempDir()
	marker := filepath.Join(commandDir, "executed")
	writeFixture(t, filepath.Join(commandDir, "command.sh"), "touch executed\n")
	config := fmt.Sprintf("model_provider='demo'\n[model_providers.demo]\nname='Demo'\nbase_url='https://example.test/v1'\n[model_providers.demo.auth]\ncommand='/bin/sh'\nargs=['command.sh', 'literal;argument']\ncwd=%q\ntimeout_ms=1000\nrefresh_interval_ms=0\n", commandDir)
	writeFixture(t, filepath.Join(home, "config.toml"), config)
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatalf("valid token-command configuration was rejected: %v", err)
	}
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	if err := ImportRepositoryWithOptions("codex", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	name := ".agents/native/com.openai.codex/config.toml"
	want, err := parseNative([]byte(config), "toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(repo, name), filepath.Join(target, "config.toml"), filepath.Join(again, name)} {
		got, err := parseNative([]byte(readNativeTest(t, path)), "toml")
		if err != nil || nativeHash(want) != nativeHash(got) {
			t.Fatalf("token-command configuration changed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("configuration operation executed the token command")
	}
	if readNativeTest(t, filepath.Join(home, "config.toml")) != config {
		t.Fatal("native source changed")
	}
}

func TestNativeProviderCommandAuthenticationRejectsIncompleteUnits(t *testing.T) {
	for name, data := range map[string]string{
		"not-object":         "auth=false\n",
		"missing-command":    "auth.args=['argument']\n",
		"empty-command":      "auth.command=' '\n",
		"invalid-args":       "auth.command='demo'\nauth.args=[false]\n",
		"zero-timeout":       "auth.command='demo'\nauth.timeout_ms=0\n",
		"fractional-timeout": "auth.command='demo'\nauth.timeout_ms=1.5\n",
		"negative-refresh":   "auth.command='demo'\nauth.refresh_interval_ms=-1\n",
		"invalid-cwd":        "auth.command='demo'\nauth.cwd=false\n",
		"unknown-field":      "auth.command='demo'\nauth.future_mode='ODA_AUTH_TEST'\n",
		"credential-field":   "auth.command='demo'\nauth.api_key='ODA_AUTH_TEST'\n",
		"env-conflict":       "auth.command='demo'\nenv_key='MODEL_KEY'\n",
		"account-conflict":   "auth.command='demo'\nrequires_openai_auth=true\n",
		"aws-conflict":       "auth.command='demo'\naws.region='us-east-1'\n",
	} {
		t.Run(name, func(t *testing.T) {
			config := "[model_providers.demo]\nname='Demo'\n" + data
			for _, scope := range []string{"project", "user"} {
				t.Run(scope, func(t *testing.T) {
					home, root := t.TempDir(), t.TempDir()
					base, nativeHome := root, ""
					if scope == "user" {
						base, nativeHome = home, home
					}
					target, _, err := nativeTargetPath("codex", scope, base, nativeArtifact{Kind: "config"})
					if err != nil {
						t.Fatal(err)
					}
					writeFixture(t, target, config)
					err = ImportRepositoryWithOptions("codex", root, WriteOptions{Experimental: true, Scope: scope, NativeHome: nativeHome, Force: true, Backup: true})
					if err == nil || !strings.Contains(err.Error(), "native authentication") || strings.Contains(err.Error(), "ODA_AUTH_TEST") {
						t.Fatalf("expected redacted complete-unit refusal: %v", err)
					}
					if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
						t.Fatal("failed import wrote canonical files")
					}
					if readNativeTest(t, target) != config {
						t.Fatal("failed import changed source")
					}
					repo := nativeFixture(t, scope, config)
					profile := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
					writeFixture(t, profile, strings.ReplaceAll(readNativeTest(t, profile), `"required":true`, `"required":false`))
					before := instructionSnapshot(t, repo)
					_, err = ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: scope, NativeHome: nativeHome, Force: true, Backup: true})
					if err == nil || !strings.Contains(err.Error(), "native authentication") || strings.Contains(err.Error(), "ODA_AUTH_TEST") {
						t.Fatalf("optional apply did not refuse invalid auth: %v", err)
					}
					if nativeHash(before) != nativeHash(instructionSnapshot(t, repo)) {
						t.Fatal("failed apply changed canonical or native files")
					}
				})
			}
		})
	}
}

func TestNativeProviderCommandAuthDoesNotAllowCredentialStores(t *testing.T) {
	for _, path := range [][]string{{"auth"}, {"model_providers", "demo", "auth", "api_key"}, {"model_providers", "demo", "requires_openai_auth", "auth"}, {"projects", "demo", "auth"}} {
		if !nativeFieldExcluded("codex", path, "ODA_AUTH_TEST") {
			t.Fatalf("credential field no longer excluded: %v", path)
		}
	}
	values, err := parseNative([]byte("[model_providers.demo]\nname='Demo'\nrequires_openai_auth=false\nauth.command='demo'\n"), "toml")
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeCheckRuntimeAuthentication("codex", values); err != nil {
		t.Fatalf("false account selector incorrectly conflicts with token command: %v", err)
	}
	declarations := 0
	for _, declaration := range nativeSettingDeclarations("codex") {
		if strings.HasPrefix(declaration.Path, "model_providers.<name>.auth") {
			declarations++
			if declaration.Disposition != "validator-declared" {
				t.Fatalf("token-command field is still external: %+v", declaration)
			}
		}
	}
	if declarations < 6 {
		t.Fatalf("token-command declarations missing: %d", declarations)
	}
}
