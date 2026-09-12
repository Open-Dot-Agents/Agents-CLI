package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexKeybindingGrammarMatchesPinnedLoader(t *testing.T) {
	valid := []any{"ctrl-y", " Control--X  OPTION--PgDn ", "f24", "f+1", "F01", "ctrl-x ctrl-s", "ctrl-+", "-a-", "shift-enter", "minus", []any{}, []any{"ctrl-x ctrl-s", "alt-minus"}}
	invalid := []any{"ctrl+y", "", "  ", "ctrl-control-x", "x-ctrl", "ctrl", "---", "f25", "f0", "f++1", "super-a", "é", "K", "ctrl-x ctrl-s ctrl-y", []any{"ctrl-a", "ctrl+y"}, []any{1}}
	for _, scope := range []string{"project", "user"} {
		for _, tc := range []struct {
			values []any
			want   bool
		}{{valid, true}, {invalid, false}} {
			for _, value := range tc.values {
				input := map[string]any{"tui": map[string]any{"keymap": map[string]any{"global": map[string]any{"open_external_editor": value}}}}
				selected, inactive := nativeSelectConfig("codex", scope, input)
				if (len(inactive) == 0 && nativeHash(input) == nativeHash(selected)) != tc.want {
					t.Errorf("scope=%s value=%#v selected=%#v inactive=%#v", scope, value, selected, inactive)
				}
				if nativeCodexValue("tui", input["tui"]) != tc.want {
					t.Errorf("direct schema guard disagrees for %#v", value)
				}
			}
		}
	}
}

func TestCodexKeymapRequiredInvalidRefusesBeforeWrites(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			root := nativeFixture(t, scope, "[tui.keymap.global]\nopen_external_editor='ctrl+y'\n")
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			options := ApplyOptions{Experimental: true, Scope: scope, Force: true, Backup: true}
			if scope == "user" {
				options.NativeHome = home
			}
			if _, err := ApplyProjection("codex", root, options); err == nil {
				t.Fatal("invalid native keybinding activated")
			}
			for _, path := range []string{filepath.Join(root, ".codex/config.toml"), filepath.Join(home, "config.toml")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("refusal wrote native file", path, err)
				}
			}
		})
	}
}
