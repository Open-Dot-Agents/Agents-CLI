package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeLSPFixture(t *testing.T, scope, content string, required bool) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, ".agents/native/com.github.copilot")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	profile, _ := json.Marshal(nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: required, Artifacts: []nativeArtifact{{Kind: "lsp", Source: "lsp.json"}}})
	for path, data := range map[string][]byte{filepath.Join(repo, ".agents/AGENTS.md"): []byte("Fixture.\n"), filepath.Join(repo, ".agents/manifest.json"): []byte(`{"version":"1.1.0-draft.2","profiles":["native"]}`), filepath.Join(dir, "profile.json"): profile, filepath.Join(dir, "lsp.json"): []byte(content)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

const nativeLSPExample = `{"lspServers":{"fixture":{"command":"fixture-server","args":["--stdio","${ARG}"],"fileExtensions":{".oda":"fixture"},"env":{"MODE":"test","API_KEY":"${LSP_API_KEY}"},"rootUri":"packages/frontend","initializationOptions":{"tokenTypes":["function"],"authenticationMode":"external","preferences":{"includeCompletions":true},"nested":[null,9007199254740993]},"requestTimeoutMs":30000}}}`

func TestNativeLSPRoundTripAndScope(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			home := t.TempDir()
			t.Setenv("COPILOT_HOME", home)
			sentinel := filepath.Join(home, "settings.json")
			os.WriteFile(sentinel, []byte(`{"theme":"dark"}`), 0640)
			repo := nativeLSPFixture(t, scope, nativeLSPExample, true)
			options := ApplyOptions{Experimental: true, Scope: scope}
			base := repo
			if scope == "user" {
				options.NativeHome = home
				base = home
			}
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			target, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "lsp"})
			got, err := parseNative([]byte(readNativeTest(t, target)), "json")
			if err != nil {
				t.Fatal(err)
			}
			want, _ := parseNative([]byte(nativeLSPExample), "json")
			if nativeHash(got) != nativeHash(want) {
				t.Fatal("LSP values changed")
			}
			if readNativeTest(t, sentinel) != `{"theme":"dark"}` {
				t.Fatal("user settings changed")
			}
			if scope == "project" {
				if _, err := os.Stat(filepath.Join(home, "lsp-config.json")); !os.IsNotExist(err) {
					t.Fatal("project apply wrote user LSP")
				}
			}
			imported := repo
			if scope == "user" {
				imported = t.TempDir()
			}
			if err := ImportRepositoryWithOptions("copilot", imported, WriteOptions{Experimental: true, Scope: scope, NativeHome: options.NativeHome}); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(imported, ".agents/native/com.github.copilot", filepath.Base(target))
			if scope == "project" {
				source = filepath.Join(repo, ".agents/native/com.github.copilot/lsp.json")
			}
			round, err := parseNative([]byte(readNativeTest(t, source)), "json")
			if err != nil || nativeHash(round) != nativeHash(want) {
				t.Fatalf("round trip changed: %v", err)
			}
			if scope == "user" {
				roundHome := t.TempDir()
				if _, err := ApplyProjection("copilot", imported, ApplyOptions{Experimental: true, Scope: "user", NativeHome: roundHome}); err != nil {
					t.Fatal(err)
				}
				if err := ImportRepositoryWithOptions("copilot", imported, WriteOptions{Experimental: true, Scope: "user", NativeHome: roundHome}); err != nil {
					t.Fatal(err)
				}
				again, err := parseNative([]byte(readNativeTest(t, source)), "json")
				if err != nil || nativeHash(again) != nativeHash(want) {
					t.Fatalf("import apply import changed LSP: %v", err)
				}
			}
			plan, err := PlanProjection("copilot", repo, options)
			if err != nil || len(plan.Actions) != 0 {
				t.Fatalf("not idempotent: %v %+v", err, plan.Actions)
			}
		})
	}
}
func TestNativeLSPUnknownMalformedAndPolicy(t *testing.T) {
	for _, content := range []string{`{"lspServers":{"fixture":{"command":"x"}}}`, `{"lspServers":{"bad.name":{"command":"x","fileExtensions":{}}}}`, `{"lspServers":{"fixture":{"command":"x","fileExtensions":{},"initializationOptions":{"apiKey":"literal-secret"}}}}`,
		`{"lspServers":{"fixture":{"command":"x","fileExtensions":{},"args":1}}}`, `{"lspServers":{"fixture":{"command":"x","fileExtensions":{},"requestTimeoutMs":"slow"}}}`, `{"lspServers":{"fixture":{"command":"x","fileExtensions":{},"env":{"API_KEY":"literal-secret"}}}}`, `{"lspServers":{"fixture":{"command":"x","fileExtensions":{},"futureFlag":true}}}`} {
		t.Run(content, func(t *testing.T) {
			repo := nativeLSPFixture(t, "project", content, true)
			_, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
			if err == nil {
				t.Fatal("invalid required LSP activated")
			}
			if strings.Contains(err.Error(), "literal-secret") {
				t.Fatal("secret in diagnostic")
			}
		})
	}
	repo := nativeLSPFixture(t, "project", strings.Replace(nativeLSPExample, `"command":`, `"futureFlag":true,"command":`, 1), false)
	plan, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	data := readNativeTest(t, filepath.Join(repo, ".github/lsp.json"))
	if strings.Contains(data, "futureFlag") || !strings.Contains(data, "fixture-server") {
		t.Fatal(data)
	}
	found := false
	for _, f := range plan.Native.Features {
		if f.Feature == "lsp:/lspServers/fixture/futureFlag" && f.Activation == "inactive" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing unknown field diagnostic")
	}
	os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["native","permissions"]}`), 0600)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err == nil {
		t.Fatal("portable policy bypassed")
	}
}
func TestNativeLSPSharedOwnershipRemoval(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	a := nativeLSPFixture(t, "user", nativeLSPExample, true)
	b := nativeLSPFixture(t, "user", strings.ReplaceAll(nativeLSPExample, "fixture", "second"), true)
	opt := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}
	for _, repo := range []string{a, b} {
		if _, err := ApplyProjection("copilot", repo, opt); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(home, "lsp-config.json")
	before := readNativeTest(t, target)
	source := filepath.Join(b, ".agents/native/com.github.copilot/lsp.json")
	os.WriteFile(source, []byte(nativeLSPExample), 0600)
	if _, err := ApplyProjection("copilot", b, opt); err == nil {
		t.Fatal("force stole LSP settings")
	}
	if readNativeTest(t, target) != before {
		t.Fatal("conflict wrote LSP")
	}
	os.WriteFile(source, []byte(`{}`), 0600)
	if _, err := ApplyProjection("copilot", b, opt); err != nil {
		t.Fatal(err)
	}
	got := readNativeTest(t, target)
	if strings.Contains(got, "second") || !strings.Contains(got, "fixture-server") {
		t.Fatal(got)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0600 {
		t.Fatal("new LSP config is not private")
	}
}
