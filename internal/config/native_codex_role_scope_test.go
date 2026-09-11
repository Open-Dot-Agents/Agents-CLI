package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCodexRoleIgnoredOverrides(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, field := range []string{
			"model_provider='child'", "model_context_window=12345", "model_auto_compact_token_limit=1000",
			"model_providers.child.name='Child'", "mcp_servers.demo.command='demo'", "notify=['demo']",
			"agents.max_depth=3", "features.shell_tool=true", "features.multi_agent=false",
			"skills.include_instructions=true", "skills.max_context_tokens=1000",
			"skills.config=[{path='/fixture/SKILL.md',enabled=true}]", "skills.bundled.enabled=true",
		} {
			t.Run(scope+"/"+field, func(t *testing.T) {
				if _, err := nativeCodexAgent([]byte(nativeAgentFixture+field+"\n"), scope); err == nil {
					t.Fatal("ignored role override activated")
				}
			})
		}
	}
}

func TestNativeCodexRoleReductionValidation(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, field := range []string{
			"model_reasoning_effort='low'", "model_reasoning_summary='concise'", "model_verbosity='low'", "personality='pragmatic'", "service_tier='flex'",
			"features.shell_tool=false", "features.apps=false", "features.personality=false", "features.plugins=false", "features.memories=false", "features.request_permissions_tool=false",
			"skills.include_instructions=false", "skills.bundled.enabled=false", "skills.config=[{path='/fixture/SKILL.md',enabled=false}]",
		} {
			t.Run(scope+"/"+field, func(t *testing.T) {
				if _, err := nativeCodexAgent([]byte(nativeAgentFixture+field+"\n"), scope); err != nil {
					t.Fatal(err)
				}
			})
		}
		for _, field := range []string{
			"model_reasoning_effort=''", "features.shell_tool='false'", "features.shell_tool=[]", "features=[]",
			"skills=[]", "skills.bundled={enabled=false,future=true}", "skills.config=[{path='/fixture/SKILL.md',enabled=false,future=true}]",
			"skills.config=[{path='/fixture/SKILL.md',enabled=false},{path='/other/SKILL.md',enabled=true}]",
		} {
			if _, err := nativeCodexAgent([]byte(nativeAgentFixture+field+"\n"), scope); err == nil {
				t.Fatalf("invalid role activated: %s", field)
			}
		}
	}
}

func TestNativeCodexRoleRefusalIsAtomic(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			repo := nativeFixture(t, scope, "")
			home, state := t.TempDir(), t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			base := filepath.Join(repo, ".agents/native/com.openai.codex")
			profile := nativeProfile{Namespace: "com.openai.codex", HarnessVersion: "=0.154.0", Scope: scope, Required: true,
				Artifacts: []nativeArtifact{{Kind: "agent", Source: "fixture.toml", Name: "fixture.toml"}}}
			writeProfile := func() {
				data, err := json.Marshal(profile)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, filepath.Join(base, "profile.json"), string(data))
			}
			writeProfile()
			content := nativeAgentFixture + "model_provider='child'\n"
			writeFixture(t, filepath.Join(base, "fixture.toml"), content)
			options := ApplyOptions{Experimental: true, Scope: scope, Force: true, Backup: true}
			targetBase := repo
			if scope == "user" {
				options.NativeHome, targetBase = home, home
			}
			before := nativeHash(instructionSnapshot(t, repo))
			if _, err := ApplyProjection("codex", repo, options); err == nil || !strings.Contains(err.Error(), "model_provider") {
				t.Fatalf("required ignored override accepted: %v", err)
			}
			if nativeHash(instructionSnapshot(t, repo)) != before {
				t.Fatal("required refusal changed repository")
			}
			for _, dir := range []string{home, state} {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatal("required refusal changed user state", entries, err)
				}
			}
			profile.Required = false
			writeProfile()
			plan, err := ApplyProjection("codex", repo, options)
			if err != nil {
				t.Fatal(err)
			}
			target, _, err := nativeTargetPath("codex", scope, targetBase, profile.Artifacts[0])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("optional role partially activated", err)
			}
			if readNativeTest(t, filepath.Join(base, "fixture.toml")) != content {
				t.Fatal("canonical role changed")
			}
			found := false
			for _, feature := range plan.Native.Features {
				if feature.Activation == "inactive" && strings.Contains(feature.Limitation, "model_provider") {
					found = true
				}
			}
			if !found {
				t.Fatal("plan does not explain ignored role field")
			}
		})
	}
}
