package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCopilotSkillDiscoveryRefusal(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for name, body := range map[string]string{
			"plain":   "# Skill\nUse the fixture.\n",
			"yaml":    "---\nname: fixture\nuser-invocable: [\n---\nUse the fixture.\n",
			"boolean": "---\nname: fixture\ndescription: Fixture.\nuser-invocable: \"false\"\n---\nUse the fixture.\n",
		} {
			t.Run(scope+"/"+name, func(t *testing.T) {
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				repo, home := t.TempDir(), t.TempDir()
				writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Use fixture data.\n")
				writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["skills"]}`)
				path := filepath.Join(repo, ".agents/skills/fixture/SKILL.md")
				writeFixture(t, path, body)
				options := ApplyOptions{Experimental: true, Scope: scope, Force: true}
				if scope == "user" {
					options.NativeHome = home
				}
				if err := ValidateRepositoryWithOptions(filepath.Join(repo, ".agents"), true); err != nil {
					t.Fatal(err)
				}
				plan, err := PlanProjection("copilot", repo, options)
				if err != nil {
					t.Fatal(err)
				}
				if plan.Applicable || len(plan.Actions) != 0 || len(plan.Diagnostics) == 0 {
					t.Fatalf("ignored native skill was offered as applicable: %+v", plan)
				}
				if _, err := ApplyProjection("copilot", repo, options); err == nil {
					t.Fatal("ignored native skill applied")
				}
				if readNativeTest(t, path) != body {
					t.Fatal("source changed")
				}
				if _, err := os.Stat(filepath.Join(home, "skills")); !os.IsNotExist(err) {
					t.Fatal("refusal wrote user skills")
				}
			})
		}
	}
}

func TestNativeCopilotSkillMetadataPreservedAndReported(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			repo, home := t.TempDir(), t.TempDir()
			writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Fixture.\n")
			writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.1.0-draft.2","profiles":["skills"]}`)
			body := "---\r\nname: invalid_name\r\nargument-hint: '[fixture]'\r\nallowed-tools: ['*']\r\nuser-invocable: false\r\ndisable-model-invocation: true\r\nunknown: SECRET_FIXTURE_VALUE\r\n---\r\nFixture body.\r\n"
			path := filepath.Join(repo, ".agents/skills/fixture/SKILL.md")
			writeFixture(t, path, body)
			options := ApplyOptions{Experimental: true, Scope: scope}
			if scope == "user" {
				options.NativeHome = home
			}
			plan, err := PlanProjection("copilot", repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Applicable || len(plan.Warnings) != 3 {
				t.Fatalf("missing native loss reports: %+v", plan)
			}
			encoded, _ := json.Marshal(plan)
			if strings.Contains(string(encoded), "SECRET_FIXTURE_VALUE") {
				t.Fatal("plan exposed metadata values")
			}
			found := false
			for _, f := range plan.Native.Features {
				if f.Feature == "skill" && strings.Contains(f.Activation, "model invocation disabled; user invocation disabled") && f.Source == path {
					found = true
				}
			}
			if !found {
				t.Fatal("missing per-skill invocation controls")
			}
			applied, err := ApplyProjection("copilot", repo, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(applied.Warnings) != 3 {
				t.Fatal("apply lost warnings")
			}
			if readNativeTest(t, path) != body {
				t.Fatal("source rewritten")
			}
			if scope == "user" {
				if readNativeTest(t, filepath.Join(home, "skills/fixture/SKILL.md")) != body {
					t.Fatal("metadata changed on projection")
				}
				info, err := os.Stat(filepath.Join(home, "skills/fixture/SKILL.md"))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("new user skill is not private")
				}
			} else if _, err := os.Stat(filepath.Join(home, "skills")); !os.IsNotExist(err) {
				t.Fatal("project wrote user skills")
			}
		})
	}
}

func TestNativeCopilotSkillFrontmatterBounds(t *testing.T) {
	for _, text := range []string{
		"---\nname: first\nname: second\n---\nBody",
		"---\n- item\n---\nBody",
		"---\n42: value\n---\nBody",
		"---\nname: value\n",
		"---\n---\nBody",
		"---\nuser-invocable: !!bool invalid\n---\nBody",
		"---\nunknown: {duplicate: one, duplicate: two}\n---\nBody",
		"---\nname: *missing\n---\nBody",
	} {
		if _, err := nativeCopilotSkillFields([]byte(text)); err == nil {
			t.Fatalf("accepted ambiguous frontmatter %q", text)
		}
	}
	for _, field := range []string{"name: 42", "description: true", "argument-hint: []", "allowed-tools: [42]", "allowed-tools: {}", "disable-model-invocation: 'false'", "user-invocable: null"} {
		fields, err := nativeCopilotSkillFields([]byte("---\n" + field + "\n---\nBody"))
		if err != nil {
			t.Fatal(err)
		}
		for key, node := range fields {
			if nativeCopilotSkillFieldValid(key, node) {
				t.Fatalf("coerced %s", field)
			}
		}
	}
}

func TestStableCopilotPlainSkillStillAccepted(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, filepath.Join(repo, ".agents/AGENTS.md"), "Fixture.\n")
	writeFixture(t, filepath.Join(repo, ".agents/manifest.json"), `{"version":"1.0.0","profiles":["skills"]}`)
	writeFixture(t, filepath.Join(repo, ".agents/skills/fixture/SKILL.md"), "# Fixture\nPlain Markdown.\n")
	plan, err := PlanProjection("copilot", repo, ApplyOptions{})
	if err != nil || !plan.Applicable || len(plan.Warnings) != 0 {
		t.Fatalf("stable semantics changed: %+v %v", plan, err)
	}
}
