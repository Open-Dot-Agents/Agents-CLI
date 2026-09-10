package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSkillImportRehomesOwnedReferences(t *testing.T) {
	source, target, repo := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	definition := filepath.Join(source, "skills", "fixture", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(definition), 0700); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: fixture\ndescription: Isolated fixture\n---\nUse this skill.\n"
	if err := os.WriteFile(definition, []byte(skill), 0600); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("[[skills.config]]\npath = %q\nenabled = false\n", definition)
	if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte(data), 0640); err != nil {
		t.Fatal(err)
	}
	before := portabilitySnapshot(t, source)
	if err := ImportRepositoryWithOptions("codex", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(portabilitySnapshot(t, source)) {
		t.Fatal("import changed native source")
	}
	canonicalPath := filepath.Join(repo, ".agents/native/com.openai.codex/config.toml")
	values, err := parseNative([]byte(readNativeTest(t, canonicalPath)), "toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"skills": map[string]any{"config": []any{map[string]any{"path": "skills/fixture/SKILL.md", "enabled": false}}}}
	if nativeHash(values) != nativeHash(want) {
		t.Fatal("owned skill reference was not made relative", values)
	}
	if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	if readNativeTest(t, filepath.Join(target, "skills/fixture/SKILL.md")) != skill {
		t.Fatal("skill bytes changed")
	}
	again := t.TempDir()
	if err := ImportRepositoryWithOptions("codex", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	second, err := parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents/native/com.openai.codex/config.toml"))), "toml")
	if err != nil || nativeHash(second) != nativeHash(want) {
		t.Fatal("reimport changed rebased skill references", second, err)
	}
}

func TestCodexSkillImportKeepsExternalReferences(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"fixture", ".system"} {
		path := filepath.Join(home, "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"~/skills/fixture/SKILL.md":                    "~/skills/fixture/SKILL.md",
		"skills/fixture/SKILL.md":                      "skills/fixture/SKILL.md",
		filepath.Join(home, "skills/fixture/SKILL.md"): "skills/fixture/SKILL.md",
		"../external/SKILL.md":                         filepath.Join(filepath.Dir(home), "external/SKILL.md"),
		"skills/missing/SKILL.md":                      filepath.Join(home, "skills/missing/SKILL.md"),
		"skills/.system/SKILL.md":                      filepath.Join(home, "skills/.system/SKILL.md"),
		"skills/fixture":                               "skills/fixture", // Ignored folder selectors stay inactive.
	}
	for input, want := range cases {
		entry := map[string]any{"path": input, "enabled": false}
		values := map[string]any{"skills": map[string]any{"config": []any{entry}}}
		nativeRebaseCodexSkillImport(values, home)
		if entry["path"] != want {
			t.Errorf("%q became %q, want %q", input, entry["path"], want)
		}
	}
}

func TestCodexSkillFolderSelectorRefusesAtomically(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			values := map[string]any{"skills": map[string]any{"max_context_tokens": int64(100), "config": []any{
				map[string]any{"path": "skills/valid/SKILL.md", "enabled": false},
				map[string]any{"path": "skills/ignored", "enabled": false},
			}}}
			before := nativeHash(values)
			selected, inactive := nativeSelectConfig("codex", scope, values)
			want := map[string]any{"skills": map[string]any{"max_context_tokens": int64(100)}}
			if len(inactive) == 0 || nativeHash(selected) != nativeHash(want) || nativeHash(values) != before {
				t.Fatal("skill selector array lost atomicity or changed source", selected, inactive)
			}
			if nativeMappedValue("codex", scope, "skills", values["skills"]) {
				t.Fatal("direct mapping bypassed skill selector check")
			}
			repo := nativeFixture(t, scope, "[[skills.config]]\npath = 'skills/ignored'\nenabled = false\n")
			options := ApplyOptions{Experimental: true, Scope: scope}
			if scope == "user" {
				options.NativeHome = t.TempDir()
				t.Setenv("XDG_STATE_HOME", t.TempDir())
			}
			if _, err := ApplyProjection("codex", repo, options); err == nil {
				t.Fatal("required ignored selector activated")
			}
		})
	}
}

func TestCodexSkillProjectSelectorsInactive(t *testing.T) {
	value := map[string]any{"config": []any{map[string]any{"path": "/fixture/SKILL.md", "enabled": false}}}
	if nativeMappedValue("codex", "project", "skills", value) || !nativeMappedValue("codex", "user", "skills", value) {
		t.Fatal("skill selector scope does not match native evidence")
	}
	for _, declaration := range nativeSettingDeclarations("codex") {
		if declaration.Path == "skills.config[].path" && nativeHasProfile(declaration.Scopes, "project") {
			t.Fatal("ignored project selectors advertised as mapped")
		}
	}
}
