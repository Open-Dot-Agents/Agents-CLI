package config

import (
	"strconv"
	"strings"
)

// The pinned loader uses a custom Serde key parser that its JSON schema
// represents only as a string. Validate that grammar without rewriting source
// spellings, chord order, alternatives, or explicit empty-list unbindings.
func nativeCodexKeybinding(value any) bool {
	switch value := value.(type) {
	case string:
		strokes := strings.Fields(value)
		if len(strokes) == 0 || len(strokes) > 2 {
			return false
		}
		for _, stroke := range strokes {
			if !nativeCodexKeyStroke(stroke) {
				return false
			}
		}
		return true
	case []any:
		for _, alternative := range value {
			if _, ok := alternative.(string); !ok || !nativeCodexKeybinding(alternative) {
				return false
			}
		}
		return true
	}
	return false
}

func nativeCodexKeyModifier(value string) string {
	switch value {
	case "ctrl", "control":
		return "ctrl"
	case "alt", "option":
		return "alt"
	case "shift":
		return "shift"
	}
	return ""
}

func nativeCodexKeyStroke(value string) bool {
	// Rust to_ascii_lowercase does not fold Unicode letters such as Kelvin
	// sign into ASCII keys. Preserve those bytes so they remain invalid.
	ascii := []byte(value)
	for i, c := range ascii {
		if c >= 'A' && c <= 'Z' {
			ascii[i] += 'a' - 'A'
		}
	}
	value = string(ascii)
	modifiers := map[string]bool{}
	var keyParts []string
	for _, segment := range strings.Split(value, "-") {
		if segment == "" {
			continue
		}
		if modifier := nativeCodexKeyModifier(segment); modifier != "" {
			if len(keyParts) != 0 || modifiers[modifier] {
				return false
			}
			modifiers[modifier] = true
		} else {
			keyParts = append(keyParts, segment)
		}
	}
	key := strings.Join(keyParts, "-")
	if len(key) == 1 && key[0] >= 0x20 && key[0] < 0x7f && key != "-" {
		return true
	}
	switch key {
	case "enter", "return", "tab", "backspace", "esc", "escape", "delete", "del", "up", "down", "left", "right", "home", "end", "page-up", "pageup", "pgup", "page-down", "pagedown", "pgdn", "space", "spacebar", "minus":
		return true
	}
	if strings.HasPrefix(key, "f") {
		number, err := strconv.ParseUint(strings.TrimPrefix(key[1:], "+"), 10, 8)
		return err == nil && number >= 1 && number <= 24
	}
	return false
}

func nativeCodexKeymapConstraint(path []string, value any) string {
	if len(path) == 4 && path[0] == "tui" && path[1] == "keymap" && !nativeCodexKeybinding(value) {
		return "invalid Codex keybinding: use one or two native key strokes, an array of valid alternatives, or [] to unbind"
	}
	return ""
}

func nativeCodexKeymapDocument(key string, value any) bool {
	if key != "tui" {
		return true
	}
	tui, _ := value.(map[string]any)
	keymap, _ := tui["keymap"].(map[string]any)
	for _, context := range keymap {
		actions, _ := context.(map[string]any)
		for _, binding := range actions {
			if !nativeCodexKeybinding(binding) {
				return false
			}
		}
	}
	return true
}
