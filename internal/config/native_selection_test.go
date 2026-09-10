package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeOptionalUnknownChildKeepsKnownFields(t *testing.T) {
	repo := nativeFixture(t, "project", "[tui]\nstatus_line = ['model-name']\nfuture_preference = true\n")
	profilePath := filepath.Join(repo, ".agents/native/com.openai.codex/profile.json")
	profile := strings.Replace(readNativeTest(t, profilePath), `"required":true`, `"required":false`, 1)
	if err := os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status_line") || strings.Contains(string(data), "future_preference") {
		t.Fatal("unknown optional child suppressed or entered the known configuration")
	}
	found := false
	for _, feature := range plan.Native.Features {
		if feature.Feature == "config:/tui/future_preference" && feature.Activation == "inactive" {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown child has no field-level diagnostic")
	}
	if !strings.Contains(readNativeTest(t, filepath.Join(repo, ".agents/native/com.openai.codex/config.toml")), "future_preference") {
		t.Fatal("unknown source content was removed")
	}
}

func TestNativeOptionalSelectionKeepsStructuredValuesAtomic(t *testing.T) {
	values, err := parseNative([]byte("[skills]\n[[skills.config]]\nname = 'fixture'\nenabled = false\nfuture = true\n[tui]\nstatus_line = ['model-name']\n"), "toml")
	if err != nil {
		t.Fatal(err)
	}
	selected, inactive := nativeSelectConfig("codex", "user", values)
	if _, exists := selected["skills"]; exists {
		t.Fatal("an unknown array member was partly activated")
	}
	if _, exists := selected["tui"]; !exists {
		t.Fatal("valid sibling section was suppressed")
	}
	if len(inactive) != 2 {
		t.Fatalf("expected child and atomic-array diagnostics, got %v", inactive)
	}
	if !strings.Contains(inactive[0].Path, "/skills/config/0/future") {
		t.Fatal("array diagnostic lost its element path")
	}
}

func TestNativeRequiredUnknownChildRefusesWithoutWrites(t *testing.T) {
	repo := nativeFixture(t, "project", "[tui]\nstatus_line = ['model-name']\nfuture = true\n")
	_, err := ApplyProjection("codex", repo, ApplyOptions{Experimental: true})
	if err == nil || !strings.Contains(err.Error(), "/tui/future") {
		t.Fatal("required unknown child was not refused with its path", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex/config.toml")); !os.IsNotExist(err) {
		t.Fatal("refused required content wrote configuration")
	}
}

func TestNativeSelectionKeepsPermissionControlsInactive(t *testing.T) {
	values, err := parseNative([]byte("[apps._default]\nenabled = false\ndefault_tools_approval_mode = 'approve'\n"), "toml")
	if err != nil {
		t.Fatal(err)
	}
	selected, inactive := nativeSelectConfig("codex", "user", values)
	if len(inactive) != 1 || inactive[0].Disposition != "blocked" {
		t.Fatal("permission control lost its independent check", inactive)
	}
	data, err := nativeEncode(selected, "toml")
	if err != nil || strings.Contains(string(data), "approval") || !strings.Contains(string(data), "enabled = false") {
		t.Fatal("permission filter dropped a known sibling or kept an approval grant")
	}
}
