package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillDefinitionMustBeUTF8(t *testing.T) {
	for _, version := range []string{manifestVersion, ExperimentalVersion, NativeVersion} {
		for _, vendor := range []string{"codex", "copilot"} {
			t.Run(version+"/"+vendor, func(t *testing.T) {
				root := t.TempDir()
				writeCanonicalFixture(t, root)
				agents := filepath.Join(root, ".agents")
				if err := os.WriteFile(filepath.Join(agents, "manifest.json"), []byte(`{"version":"`+version+`","profiles":["skills"]}`), 0600); err != nil {
					t.Fatal(err)
				}
				definition := filepath.Join(agents, "skills", "review", "SKILL.md")
				if err := os.WriteFile(definition, []byte{'#', ' ', 0xff, '\n'}, 0600); err != nil {
					t.Fatal(err)
				}
				options := ApplyOptions{Experimental: version != manifestVersion}
				if _, err := ApplyProjection(vendor, root, options); err == nil || !strings.Contains(err.Error(), "UTF-8") {
					t.Fatalf("invalid skill encoding was not refused: %v", err)
				}
				for _, path := range []string{"AGENTS.md", ".codex/config.toml", ".github/copilot-instructions.md"} {
					if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Fatalf("refusal wrote %s: %v", path, err)
					}
				}
				if err := os.WriteFile(definition, []byte("# Review\nRésumé — 日本語.\n"), 0600); err != nil {
					t.Fatal(err)
				}
				// Binary supporting assets are valid. Only SKILL.md is Markdown.
				if err := os.WriteFile(filepath.Join(filepath.Dir(definition), "asset.bin"), []byte{0xff, 0xfe}, 0600); err != nil {
					t.Fatal(err)
				}
				if err := ValidateRepositoryWithOptions(agents, options.Experimental); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestStableSkillImportRefusesInvalidEncodingBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root := t.TempDir()
			writeCanonicalFixture(t, root)
			if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Native instructions.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			path, data := ".codex/config.toml", "[mcp_servers.example]\ncommand = 'example'\n"
			if vendor == "copilot" {
				path, data = ".github/mcp.json", `{"mcpServers":{"example":{"type":"stdio","command":"example"}}}`
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".agents/skills/review/SKILL.md"), []byte{0xff}, 0600); err != nil {
				t.Fatal(err)
			}
			before := instructionSnapshot(t, root)
			err := ImportRepositoryWithOptions(vendor, root, WriteOptions{Force: true, Backup: true})
			if err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid skill import was not refused: %v", err)
			}
			after := instructionSnapshot(t, root)
			if nativeHash(before) != nativeHash(after) {
				t.Fatal("failed import changed files or created backups")
			}
		})
	}
}

func TestNativeUserSkillImportRefusesInvalidEncodingBeforeWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			definition := filepath.Join(home, "skills/review/SKILL.md")
			writeFixture(t, definition, "# Review\n")
			options := WriteOptions{Experimental: true, Scope: "user", NativeHome: home}
			if err := ImportRepositoryWithOptions(vendor, root, options); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(definition, []byte{0xff}, 0600); err != nil {
				t.Fatal(err)
			}
			before := instructionSnapshot(t, root)
			options.Force, options.Backup = true, true
			if err := ImportRepositoryWithOptions(vendor, root, options); err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid skill import was not refused: %v", err)
			}
			if nativeHash(before) != nativeHash(instructionSnapshot(t, root)) {
				t.Fatal("failed native import changed files or created backups")
			}
		})
	}
}
