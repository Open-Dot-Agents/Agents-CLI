package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNativeModelSelectionCodexAndCopilot proves that the existing draft.2
// native profile (no new schema or manifest field) already round-trips a
// project-scope model selection into each harness's own native configuration
// file: Codex's config.toml `model` scalar and Copilot's settings.json
// `model` scalar. This mirrors SPEC/examples/native-model-selection.
func TestNativeModelSelectionCodexAndCopilot(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		repo := t.TempDir()
		root := filepath.Join(repo, ".agents")
		dir := filepath.Join(root, "native", "com.openai.codex")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			filepath.Join(root, "AGENTS.md"):     "Model selection example.\n",
			filepath.Join(root, "manifest.json"): `{"version":"1.1.0-draft.2","profiles":["native"]}`,
			filepath.Join(dir, "profile.json"):   `{"namespace":"com.openai.codex","harness_version":"=0.154.0","scope":"project","required":true,"artifacts":[{"kind":"config","source":"config.toml"}]}`,
			filepath.Join(dir, "config.toml"):    "model = \"gpt-5.5\"\n",
		}
		for path, data := range files {
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true}); err != nil {
			t.Fatal(err)
		}
		got := readNativeTest(t, filepath.Join(repo, ".codex", "config.toml"))
		if !strings.Contains(got, `model = "gpt-5.5"`) && !strings.Contains(got, `model = 'gpt-5.5'`) {
			t.Fatalf("Codex config.toml missing projected model: %q", got)
		}
	})

	t.Run("copilot", func(t *testing.T) {
		repo := t.TempDir()
		root := filepath.Join(repo, ".agents")
		dir := filepath.Join(root, "native", "com.github.copilot")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			filepath.Join(root, "AGENTS.md"):     "Model selection example.\n",
			filepath.Join(root, "manifest.json"): `{"version":"1.1.0-draft.2","profiles":["native"]}`,
			filepath.Join(dir, "profile.json"):   `{"namespace":"com.github.copilot","harness_version":"=1.0.84-9","scope":"project","required":true,"artifacts":[{"kind":"config","source":"settings.json"}]}`,
			filepath.Join(dir, "settings.json"):  `{"model":"gpt-5.5"}`,
		}
		for path, data := range files {
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err != nil {
			t.Fatal(err)
		}
		got := readNativeTest(t, filepath.Join(repo, ".github", "copilot", "settings.json"))
		if !strings.Contains(got, `"model": "gpt-5.5"`) && !strings.Contains(got, `"model":"gpt-5.5"`) {
			t.Fatalf("Copilot settings.json missing projected model: %q", got)
		}
	})
}
