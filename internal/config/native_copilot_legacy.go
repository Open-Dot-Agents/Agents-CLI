package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func nativeCopilotLegacyRoot(key string) bool {
	if nativeForbidden(key) {
		return false
	}
	if nativeSettingRegistry["copilot"]["user"][key] {
		return true
	}
	// Documented preferences that do not yet have an activation mapping must
	// still survive import. They remain optional and inactive in the namespace.
	switch key {
	case "defaultMode", "defaultPermissionMode", "inlineImages", "inlineImageLiveWindow", "notifications":
		return true
	}
	return false
}

// Copilot 1.0.83 moves recognized preferences from config.json on startup.
// Legacy roots replace matching settings.json roots, including whole objects.
// Reading these preferences does not authorize writes to application state.
func nativeCopilotLegacyValues(home string) (map[string]any, bool, error) {
	path := filepath.Join(home, "config.json")
	if err := nativeNoSymlinks(path); err != nil {
		return nil, false, err
	}
	data, err := nativeReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	values, err := parseNative(data, "json")
	if err != nil {
		return nil, false, fmt.Errorf("cannot parse legacy Copilot config.json; no legacy state was changed")
	}
	selected := map[string]any{}
	for key, value := range values {
		if nativeCopilotLegacyRoot(key) {
			selected[key] = value
		}
	}
	return selected, len(values) != len(selected), nil
}

func nativeReadCopilotUserConfig(home string) ([]byte, []string, error) {
	data, err := nativeReadFile(filepath.Join(home, "settings.json"))
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	values := map[string]any{}
	if exists {
		values, err = parseNative(data, "json")
		if err != nil {
			return nil, nil, err
		}
	}
	legacy, omitted, err := nativeCopilotLegacyValues(home)
	if err != nil {
		return nil, nil, err
	}
	var excluded []string
	if omitted {
		excluded = append(excluded, "config.json application state and unrecognized legacy fields")
	}
	if !exists && len(legacy) == 0 {
		return nil, excluded, os.ErrNotExist
	}
	for key, value := range legacy {
		values[key] = value
	}
	data, err = nativeEncode(values, "json")
	return data, excluded, err
}

// Refuse a write or removal that native startup would undo. This check also
// runs when apply rebuilds its plan under the user target lock. It cannot be
// bypassed by force or adopt and never migrates the external file itself.
func nativeCopilotLegacyConflict(home string, document map[string]any, touched map[string]bool) error {
	legacy, _, err := nativeCopilotLegacyValues(home)
	if err != nil {
		return err
	}
	for pointer := range touched {
		parts, err := nativePointerParts(pointer)
		if err != nil || len(parts) == 0 {
			return fmt.Errorf("invalid native setting ownership path")
		}
		if value, present := legacy[parts[0]]; present && nativeHash(value) != nativeHash(document[parts[0]]) {
			return fmt.Errorf("legacy Copilot config.json would replace setting %s on startup; use the pinned native CLI to migrate preferences, then retry; apply does not write legacy application state", parts[0])
		}
	}
	return nil
}
