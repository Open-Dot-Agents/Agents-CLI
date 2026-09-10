package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeCopilotAgentFixture = "---\nname: Fixture\ndescription: Fixture agent\ntools: [bash]\nmodel: fixture-model\nreasoningEffort: low\n---\nUse fixture data.\n"

func copilotAgentFixture(t *testing.T, scope, name, body string) string {
	t.Helper()
	root := nativeLSPFixture(t, scope, `{}`, true)
	base := filepath.Join(root, ".agents/native/com.github.copilot")
	data, _ := json.Marshal(nativeProfile{Namespace: "com.github.copilot", HarnessVersion: "=1.0.83", Scope: scope, Required: true, Artifacts: []nativeArtifact{{Kind: "agent", Source: "agent.md", Name: name}}})
	os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(base, "agent.md"), []byte(body), 0600)
	return root
}
func TestNativeCopilotAgentValidation(t *testing.T) {
	for _, body := range []string{"---\ndescription: x\n---", nativeCopilotAgentFixture, strings.ReplaceAll(nativeCopilotAgentFixture, "\n", "\r\n"), strings.Replace(nativeCopilotAgentFixture, "name: Fixture\n", "", 1), strings.Replace(nativeCopilotAgentFixture, "tools: [bash]", "tools: bash", 1), strings.Replace(nativeCopilotAgentFixture, "tools: [bash]", "tools: []\ninfer: false\nuser-invocable: false\ndisable-model-invocation: true\ntarget: github-copilot\nmetadata: {purpose: fixture}", 1)} {
		if _, err := nativeCopilotAgent([]byte(body), "fallback.agent.md"); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"name: !!binary Zml4dHVyZQ==", "name: !custom fixture", "name: null", "name: ''", "name: 3", "description: false", "tools: [false]", "tools: {}", "infer: 'true'", "reasoningEffort: []", "future: true", "target: vscode", "metadata: {purpose: 1}", "description: duplicate"} {
		body := strings.Replace(nativeCopilotAgentFixture, "---\nUse", field+"\n---\nUse", 1)
		if _, err := nativeCopilotAgent([]byte(body), "fixture.md"); err == nil {
			t.Fatalf("invalid field accepted: %s", field)
		}
	}
	for _, body := range []string{"Plain Markdown", "---\ndescription: x\n", "---\ndescription: x\n---\n" + strings.Repeat("x", 30001), "---\ndescription: [\n---\nx"} {
		if _, err := nativeCopilotAgent([]byte(body), "a.md"); err == nil {
			t.Fatal("invalid agent accepted")
		}
	}
}
func TestNativeCopilotAgentImportApplyRoundTrip(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			home := t.TempDir()
			repo := copilotAgentFixture(t, scope, "fixture.md", nativeCopilotAgentFixture)
			options := ApplyOptions{Experimental: true, Scope: scope}
			base := repo
			if scope == "user" {
				options.NativeHome = home
				base = home
			}
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			target, _, _ := nativeTargetPath("copilot", scope, base, nativeArtifact{Kind: "agent", Name: "fixture.md"})
			if plan, err := PlanProjection("copilot", repo, options); err != nil || len(plan.Actions) != 0 {
				t.Fatalf("agent projection not idempotent: %v", err)
			}
			if readNativeTest(t, target) != nativeCopilotAgentFixture {
				t.Fatal("agent bytes changed")
			}
			imported := repo
			if scope == "user" {
				imported = t.TempDir()
			}
			if err := ImportRepositoryWithOptions("copilot", imported, WriteOptions{Experimental: true, Scope: scope, NativeHome: options.NativeHome}); err != nil {
				t.Fatal(err)
			}
			if scope == "user" {
				second := t.TempDir()
				if _, err := ApplyProjection("copilot", imported, ApplyOptions{Experimental: true, Scope: "user", NativeHome: second}); err != nil {
					t.Fatal(err)
				}
				if readNativeTest(t, filepath.Join(second, "agents/fixture.md")) != nativeCopilotAgentFixture {
					t.Fatal("import apply changed agent")
				}
			}
		})
	}
}
func TestNativeCopilotAgentDuplicateFilenameAndDisplay(t *testing.T) {
	for _, existing := range []string{"other.agent.md", "fixture.agent.md"} {
		t.Run(existing, func(t *testing.T) {
			repo := copilotAgentFixture(t, "project", "fixture.md", nativeCopilotAgentFixture)
			dir := filepath.Join(repo, ".github/agents")
			os.MkdirAll(dir, 0700)
			body := nativeCopilotAgentFixture
			if existing == "fixture.agent.md" {
				body = strings.Replace(body, "name: Fixture", "name: Different", 1)
			}
			os.WriteFile(filepath.Join(dir, existing), []byte(body), 0600)
			if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Force: true}); err == nil {
				t.Fatal("duplicate accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "fixture.md")); !os.IsNotExist(err) {
				t.Fatal("duplicate wrote file")
			}
		})
	}
	repo := copilotAgentFixture(t, "project", "fixture.md", nativeCopilotAgentFixture)
	base := filepath.Join(repo, ".agents/native/com.github.copilot")
	var profile nativeProfile
	decodePolicy(filepath.Join(base, "profile.json"), &profile)
	profile.Artifacts = append(profile.Artifacts, nativeArtifact{Kind: "agent", Source: "second.md", Name: "fixture.agent.md"})
	data, _ := json.Marshal(profile)
	os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(base, "second.md"), []byte(strings.Replace(nativeCopilotAgentFixture, "name: Fixture", "name: Different", 1)), 0600)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err == nil {
		t.Fatal("new planned file identities collide")
	}
}

func TestNativeCopilotAgentPreservesUnmappedNeighbour(t *testing.T) {
	repo := copilotAgentFixture(t, "project", "fixture.md", nativeCopilotAgentFixture)
	dir := filepath.Join(repo, ".github/agents")
	os.MkdirAll(dir, 0700)
	body := "---\nname: Neighbour\ndescription: Existing agent\nfutureField: true\n---\nExisting prompt.\n"
	path := filepath.Join(dir, "neighbour.md")
	os.WriteFile(path, []byte(body), 0600)
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, path) != body {
		t.Fatal("unowned agent changed")
	}
}

func nativeCopilotAgentMCPFixture(definition string) string {
	return "---\nname: Fixture\ndescription: Fixture agent\ntools: ['fixture/*']\nmcp-servers: " + definition + "\n---\nUse the isolated fixture.\n"
}

func TestNativeCopilotAgentMCPValidation(t *testing.T) {
	for _, definition := range []string{
		`{}`, `{fixture: {command: server, args: [], tools: [record], timeout: 3000}}`,
		`{credentials: {type: local, command: server, env: {API_KEY: '${KEY}'}}}`,
		`{fixture: {type: http, url: 'https://fixture.invalid/mcp', headers: {Authorization: 'Bearer ${KEY}'}}}`,
	} {
		body := []byte(nativeCopilotAgentMCPFixture(definition))
		if _, err := nativeCopilotAgent(body, "fixture.md"); err != nil {
			t.Fatal(err)
		}
		if err := nativeCheckCopilotAgentImport(body); err != nil {
			t.Fatal(err)
		}
	}
	for _, definition := range []string{
		`[]`, `{fixture: false}`, `{fixture: {future: true}}`,
		`{fixture: {command: server, future: true}}`,
		`{fixture: {command: server, args: [false]}}`,
		`{fixture: {command: server, timeout: .inf}}`,
		`{fixture: {command: server, command: second}}`,
		`{fixture: {command: server, env: {1: {API_KEY: secret}}}}`,
		`{fixture: {command: server, env: {API_KEY: literal-secret}}}`,
		`{fixture: {type: http, url: 'https://user:literal-secret@fixture.invalid'}}`,
		`{fixture: {type: http, command: server}}`,
		`{fixture: {type: local, url: 'https://fixture.invalid'}}`,
		`{fixture: {command: server, url: 'https://fixture.invalid'}}`,
	} {
		if _, err := nativeCopilotAgent([]byte(nativeCopilotAgentMCPFixture(definition)), "fixture.md"); err == nil {
			t.Fatalf("accepted invalid definition: %s", definition)
		} else if strings.Contains(err.Error(), "literal-secret") {
			t.Fatal("credential in diagnostic")
		}
	}
}

func TestNativeCopilotAgentMCPRoundTrip(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			body := nativeCopilotAgentMCPFixture(`{fixture: {type: local, command: server, args: [], env: {API_KEY: '${KEY}'}, tools: [record], timeout: 3000, deferTools: never, disableToolCache: true}}`)
			source := t.TempDir()
			base := source
			if scope == "project" {
				base = filepath.Join(source, ".github")
			}
			os.MkdirAll(filepath.Join(base, "agents"), 0700)
			os.WriteFile(filepath.Join(base, "agents/fixture.md"), []byte(body), 0600)
			repo := source
			nativeHome := ""
			if scope == "user" {
				repo = t.TempDir()
				nativeHome = source
			}
			if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: scope, NativeHome: nativeHome}); err != nil {
				t.Fatal(err)
			}
			if readNativeTest(t, filepath.Join(repo, ".agents/native/com.github.copilot/agent/fixture.md")) != body {
				t.Fatal("import changed agent bytes")
			}
			if scope == "project" {
				target := t.TempDir()
				if err := os.CopyFS(filepath.Join(target, ".agents"), os.DirFS(filepath.Join(repo, ".agents"))); err != nil {
					t.Fatal(err)
				}
				repo = target
			}
			options := ApplyOptions{Experimental: true, Scope: scope}
			targetBase := repo
			if scope == "user" {
				options.NativeHome = t.TempDir()
				targetBase = options.NativeHome
			}
			if _, err := ApplyProjection("copilot", repo, options); err != nil {
				t.Fatal(err)
			}
			path, _, _ := nativeTargetPath("copilot", scope, targetBase, nativeArtifact{Kind: "agent", Name: "fixture.md"})
			if readNativeTest(t, path) != body {
				t.Fatal("projection changed agent bytes")
			}
			if plan, err := PlanProjection("copilot", repo, options); err != nil || len(plan.Actions) != 0 {
				t.Fatalf("not idempotent: %v", err)
			}
			imported := repo
			if scope == "user" {
				imported = t.TempDir()
			}
			if err := ImportRepositoryWithOptions("copilot", imported, WriteOptions{Experimental: true, Scope: scope, NativeHome: options.NativeHome}); err != nil {
				t.Fatal(err)
			}
			if readNativeTest(t, filepath.Join(imported, ".agents/native/com.github.copilot/agent/fixture.md")) != body {
				t.Fatal("second import changed agent bytes")
			}
		})
	}
}

func TestNativeCopilotAgentMCPUnknownInactive(t *testing.T) {
	body := nativeCopilotAgentMCPFixture(`{fixture: {command: server, future: true}}`)
	for _, required := range []bool{false, true} {
		repo := copilotAgentFixture(t, "project", "fixture.md", body)
		path := filepath.Join(repo, ".agents/native/com.github.copilot/profile.json")
		var profile nativeProfile
		decodePolicy(path, &profile)
		profile.Required = required
		data, _ := json.Marshal(profile)
		os.WriteFile(path, data, 0600)
		plan, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true})
		if required && err == nil {
			t.Fatal("required unknown field accepted")
		}
		if !required {
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, feature := range plan.Native.Features {
				if feature.Feature == "agent" && feature.Activation == "inactive" {
					found = true
				}
			}
			if !found {
				t.Fatal("unknown agent not reported inactive")
			}
		}
		if _, err := os.Stat(filepath.Join(repo, ".github/agents/fixture.md")); !os.IsNotExist(err) {
			t.Fatal("unknown nested field activated")
		}
	}
}

func TestNativeCopilotAgentCredentialImportIsTransactional(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, definition := range []string{
			`{fixture: {command: server, env: {API_KEY: literal-secret}}}`,
			`{fixture: {type: http, url: 'https://user:literal-secret@fixture.invalid'}}`,
			`{fixture: {type: http, url: 'https://fixture.invalid', headers: {Authorization: literal-secret}}}`,
			`{fixture: {command: server, future: {credentials: literal-secret}}}`,
		} {
			source := t.TempDir()
			base := source
			if scope == "project" {
				base = filepath.Join(source, ".github")
			}
			os.MkdirAll(filepath.Join(base, "agents"), 0700)
			os.WriteFile(filepath.Join(base, "agents/fixture.md"), []byte(nativeCopilotAgentMCPFixture(definition)), 0600)
			repo := source
			nativeHome := ""
			if scope == "user" {
				repo = t.TempDir()
				nativeHome = source
			}
			canonical := filepath.Join(repo, ".agents")
			os.MkdirAll(canonical, 0700)
			sentinel := filepath.Join(canonical, "AGENTS.md")
			os.WriteFile(sentinel, []byte("Existing instructions.\n"), 0600)
			err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: scope, NativeHome: nativeHome})
			if err == nil {
				t.Fatal("credential-bearing agent imported")
			}
			if strings.Contains(err.Error(), "literal-secret") {
				t.Fatal("credential in error")
			}
			entries, _ := os.ReadDir(canonical)
			if len(entries) != 1 || readNativeTest(t, sentinel) != "Existing instructions.\n" {
				t.Fatal("failed import changed canonical tree")
			}
		}
	}
}

func TestNativeCopilotAgentCredentialFieldContexts(t *testing.T) {
	for _, extra := range []string{
		"metadata: {apiKey: literal-secret}\n",
		"mcpServers: {fixture: {env: {API_KEY: literal-secret}}}\n",
	} {
		body := strings.Replace(nativeCopilotAgentMCPFixture(`{}`), "---\nUse", extra+"---\nUse", 1)
		if err := nativeCheckCopilotAgentImport([]byte(body)); err == nil {
			t.Fatal("credential check bypassed by field context")
		}
		if _, err := nativeCopilotAgent([]byte(body), "fixture.md"); err == nil {
			t.Fatal("credential-bearing agent projected")
		}
	}
	// Unknown optional configuration is retained during import. Do not treat a
	// server name as a credential field, or use successful import as activation.
	body := nativeCopilotAgentMCPFixture(`{credentials: {command: server, future: true}}`)
	if err := nativeCheckCopilotAgentImport([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeCopilotAgent([]byte(body), "fixture.md"); err == nil {
		t.Fatal("unknown field activated")
	}
}
