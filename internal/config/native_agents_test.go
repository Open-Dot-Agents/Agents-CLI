package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeAgentFixture = "name = 'fixture'\ndescription = 'Fixture agent.'\ndeveloper_instructions = 'Review the fixture.'\nmodel = 'fixture-model'\n"

func TestNativeCodexAgentValidation(t *testing.T) {
	for name, data := range map[string]string{
		"missing-name":         "description='Fixture'\ndeveloper_instructions='Fixture'\n",
		"blank-description":    "name='fixture'\ndescription=' '\ndeveloper_instructions='Fixture'\n",
		"missing-instructions": "name='fixture'\ndescription='Fixture'\n",
		"permission":           nativeAgentFixture + "sandbox_mode='danger-full-access'\n",
		"unknown":              nativeAgentFixture + "future=true\n",
		"wrong-type":           nativeAgentFixture + "model_context_window='large'\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := nativeCodexAgent([]byte(data), "user"); err == nil {
				t.Fatal("invalid agent activated")
			}
		})
	}
	if name, err := nativeCodexAgent([]byte(nativeAgentFixture), "user"); err != nil || name != "fixture" {
		t.Fatal(name, err)
	}
}

func TestNativeAgentImportApplyImportRoundTrip(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			repo := t.TempDir()
			home := repo
			options := WriteOptions{Experimental: true, Scope: scope}
			apply := ApplyOptions{Experimental: true, Scope: scope, Adopt: true}
			if scope == "user" {
				home = t.TempDir()
				options.NativeHome = home
				apply.NativeHome = t.TempDir()
			}
			source, _, err := nativeTargetPath("codex", scope, home, nativeArtifact{Kind: "agent", Name: "fixture.toml"})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(source), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(source, []byte(nativeAgentFixture), 0600); err != nil {
				t.Fatal(err)
			}
			if err = ImportRepositoryWithOptions("codex", repo, options); err != nil {
				t.Fatal(err)
			}
			if _, err = ApplyProjection("codex", repo, apply); err != nil {
				t.Fatal(err)
			}
			targetBase := repo
			if scope == "user" {
				targetBase = apply.NativeHome
			}
			target, _, _ := nativeTargetPath("codex", scope, targetBase, nativeArtifact{Kind: "agent", Name: "fixture.toml"})
			if readNativeTest(t, target) != nativeAgentFixture {
				t.Fatal("agent content changed")
			}
			if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("agent is not private")
			}
			if scope == "user" {
				other := t.TempDir()
				options.NativeHome = apply.NativeHome
				if err = ImportRepositoryWithOptions("codex", other, options); err != nil {
					t.Fatal(err)
				}
				if readNativeTest(t, filepath.Join(other, ".agents/native/com.openai.codex/agent/fixture.toml")) != nativeAgentFixture {
					t.Fatal("agent round trip changed the source")
				}
			}
		})
	}
}

func TestNativeAgentDuplicateIdentityRefused(t *testing.T) {
	repo := nativeFixture(t, "user", "model='fixture'\n")
	base := filepath.Join(repo, ".agents/native/com.openai.codex")
	profile := nativeProfile{Namespace: "com.openai.codex", HarnessVersion: "=0.154.0", Scope: "user", Required: true, Artifacts: []nativeArtifact{{Kind: "agent", Source: "a.toml", Name: "a.toml"}, {Kind: "agent", Source: "b.toml", Name: "b.toml"}}}
	data, _ := json.Marshal(profile)
	os.WriteFile(filepath.Join(base, "profile.json"), data, 0600)
	os.WriteFile(filepath.Join(base, "a.toml"), []byte(nativeAgentFixture), 0600)
	os.WriteFile(filepath.Join(base, "b.toml"), []byte(strings.Replace(nativeAgentFixture, "'fixture'", "' fixture '", 1)), 0600)
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err == nil || !strings.Contains(err.Error(), "duplicate native agent identity") {
		t.Fatal("normalized agent collision accepted", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents")); !os.IsNotExist(err) {
		t.Fatal("refused duplicate wrote an agent")
	}
}

func TestNativeImportPreservesUnmappedArtifacts(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	files := map[string]string{
		"agents/fixture.agent.md":            "---\nname: fixture\ndescription: Fixture agent.\nunknownFutureField: true\n---\nReview the fixture.\n",
		"instructions/style.instructions.md": "---\napplyTo: '**/*.go'\n---\nUse clear names.\n",
		"hooks/fixture.json":                 `{"hooks":{"sessionStart":[{"type":"command","bash":"true"}]}}`,
	}
	for relative, data := range files {
		path := filepath.Join(home, relative)
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(data), 0600)
	}
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
		t.Fatal(err)
	}
	var profile nativeProfile
	if err := decodePolicy(filepath.Join(repo, ".agents/native/com.github.copilot/profile.json"), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.Artifacts) != 3 {
		t.Fatal("recognized assets were dropped", profile.Artifacts)
	}
	target := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	plan, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"agent", "hooks"} {
		found := false
		for _, feature := range plan.Native.Features {
			if strings.HasPrefix(feature.Feature, kind) && feature.Activation == "inactive" {
				found = true
			}
		}
		if !found {
			t.Fatal("unmapped artifact was not explicitly inactive", kind)
		}
	}
	if readNativeTest(t, filepath.Join(target, "instructions/style.instructions.md")) != files["instructions/style.instructions.md"] {
		t.Fatal("scoped instructions changed")
	}
}

func TestNativeAgentMoveUsesOwnedRemoval(t *testing.T) {
	repo := nativeFixture(t, "user", "model='fixture'\n")
	base := filepath.Join(repo, ".agents/native/com.openai.codex")
	writeProfile := func(name string) {
		profile := nativeProfile{Namespace: "com.openai.codex", HarnessVersion: "=0.154.0", Scope: "user", Required: true, Artifacts: []nativeArtifact{{Kind: "agent", Source: "fixture.toml", Name: name}}}
		data, _ := json.Marshal(profile)
		if err := os.WriteFile(filepath.Join(base, "profile.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(base, "fixture.toml"), []byte(nativeAgentFixture), 0600)
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}
	writeProfile("a.toml")
	if _, err := ApplyProjection("codex", repo, options); err != nil {
		t.Fatal(err)
	}
	writeProfile("b.toml")
	if _, err := ApplyProjection("codex", repo, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents/a.toml")); !os.IsNotExist(err) {
		t.Fatal("old owned agent was not removed")
	}
	if readNativeTest(t, filepath.Join(home, "agents/b.toml")) != nativeAgentFixture {
		t.Fatal("agent move changed content")
	}
	// A second unowned file with the same normalized native identity cannot be replaced.
	os.WriteFile(filepath.Join(home, "agents/foreign.toml"), []byte(nativeAgentFixture), 0600)
	if _, err := ApplyProjection("codex", repo, options); err == nil {
		t.Fatal("foreign duplicate agent identity was accepted")
	}
}
