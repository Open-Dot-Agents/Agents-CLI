package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCodexRoleReferenceImportRelocation(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, location := range []string{"external", "managed", "absolute", "declared-only", "managed-skill", "external-skill"} {
			t.Run(scope+"/"+location, func(t *testing.T) {
				source, target, canonical := t.TempDir(), t.TempDir(), t.TempDir()
				if scope == "project" && location == "managed-skill" {
					source = nativeFixture(t, scope, "")
					writeFixture(t, filepath.Join(source, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["native","skills"]}`)
				}
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				configDir := source
				if scope == "project" {
					configDir = filepath.Join(source, ".codex")
					canonical = source
				}
				folder := "role-library"
				if location == "managed" || location == "declared-only" || strings.HasSuffix(location, "-skill") {
					folder = "agents"
				}
				role := filepath.Join(configDir, folder, "fixture.toml")
				body := nativeAgentFixture
				if location == "declared-only" {
					body = "developer_instructions='Fixture.'\nmodel='child-model'\n"
				}
				selector := ""
				if strings.HasSuffix(location, "-skill") {
					skill := filepath.Join(t.TempDir(), "fixture/SKILL.md")
					if location == "managed-skill" {
						skill = filepath.Join(source, "skills/fixture/SKILL.md")
						if scope == "project" {
							skill = filepath.Join(source, ".agents/skills/fixture/SKILL.md")
						}
					}
					writeFixture(t, skill, "---\nname: fixture\ndescription: Fixture skill.\n---\nUse the fixture.\n")
					relative, err := filepath.Rel(filepath.Dir(role), skill)
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(relative)
					body += "skills.config=[{path=" + string(encoded) + ",enabled=false}]\n"
					selector = skill
					if location == "managed-skill" {
						selector = filepath.ToSlash(relative)
					}
				}
				writeFixture(t, role, body)
				reference := filepath.ToSlash(filepath.Join(folder, "fixture.toml"))
				if location == "absolute" {
					reference = role
				}
				content := "[agents.fixture]\ndescription='Fixture.'\nconfig_file='" + reference + "'\n"
				writeFixture(t, filepath.Join(configDir, "config.toml"), content)
				options := WriteOptions{Experimental: true, Scope: scope}
				apply := ApplyOptions{Experimental: true, Scope: scope}
				if scope == "user" {
					options.NativeHome, apply.NativeHome = source, target
				}
				if err := ImportRepositoryWithOptions("codex", canonical, options); err != nil {
					t.Fatal(err)
				}
				imported := filepath.Join(canonical, ".agents/native/com.openai.codex/config.toml")
				values, err := parseNative([]byte(readNativeTest(t, imported)), "toml")
				if err != nil {
					t.Fatal(err)
				}
				expected := role
				if location == "managed" || strings.HasSuffix(location, "-skill") {
					expected = "agents/fixture.toml"
				}
				got := values["agents"].(map[string]any)["fixture"].(map[string]any)["config_file"]
				if got != expected {
					t.Fatalf("reference changed location: got %v, want %s", got, expected)
				}
				if selector != "" {
					importedRole, err := parseNative([]byte(readNativeTest(t, filepath.Join(canonical, ".agents/native/com.openai.codex/agent/fixture.toml"))), "toml")
					if err != nil {
						t.Fatal(err)
					}
					got := importedRole["skills"].(map[string]any)["config"].([]any)[0].(map[string]any)["path"]
					if got != selector {
						t.Fatalf("skill reference changed location: got %v, want %s", got, selector)
					}
				}
				if scope == "project" {
					// Copy only canonical configuration into a separate target project.
					if err := filepath.WalkDir(filepath.Join(canonical, ".agents"), func(path string, entry os.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if entry.IsDir() {
							return nil
						}
						relative, err := filepath.Rel(canonical, path)
						if err != nil {
							return err
						}
						writeFixture(t, filepath.Join(target, relative), readNativeTest(t, path))
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					canonical = target
				}
				if _, err := ApplyProjection("codex", canonical, apply); err != nil {
					t.Fatal(err)
				}
				if readNativeTest(t, role) != body || readNativeTest(t, filepath.Join(configDir, "config.toml")) != content {
					t.Fatal("source changed")
				}
			})
		}
	}
}

func TestNativeCodexAgentSkillImportDoesNotRepairInvalidSelectors(t *testing.T) {
	for _, selector := range []string{"../skills/fixture", "../skills/fixture/SKILL.md/", "", "~/.agents/skills/fixture/SKILL.md"} {
		repo, home := t.TempDir(), t.TempDir()
		encoded, _ := json.Marshal(selector)
		body := nativeAgentFixture + "skills.config=[{path=" + string(encoded) + ",enabled=false}]\n"
		writeFixture(t, filepath.Join(home, "agents/fixture.toml"), body)
		if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: home}); err != nil {
			t.Fatal(err)
		}
		if got := readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/agent/fixture.toml")); got != body {
			t.Fatalf("selector was changed: %q", selector)
		}
	}
}
