package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilotSubagentPreferencesRoundtrip(t *testing.T) {
	values := map[string]any{"subagents": map[string]any{
		"agents": map[string]any{
			"explore":     map[string]any{"model": "native-model", "effortLevel": "high", "contextTier": "long_context"},
			"permissions": map[string]any{"model": "inherit", "effortLevel": "inherit", "contextTier": "inherit"},
			"token":       map[string]any{"model": "inherit"},
		},
		"disabledSubagents": []any{"task", "Custom Agent"},
		"maxConcurrency":    json.Number("32"), "maxDepth": json.Number("256"),
	}}
	selected, inactive := nativeSelectConfig("copilot", "user", values)
	if len(inactive) != 0 || nativeHash(selected) != nativeHash(values) || !nativeMappedValue("copilot", "user", "subagents", values["subagents"]) {
		t.Fatal("native subagent selection changed or inactive", selected, inactive)
	}
	selected, inactive = nativeSelectConfig("copilot", "project", values)
	if len(selected) != 0 || len(inactive) == 0 {
		t.Fatal("user subagent preference activated in project scope")
	}
	source, repo, target := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(source, "settings.json"), mustNativeJSON(t, values), 0600); err != nil {
		t.Fatal(err)
	}
	before := portabilitySnapshot(t, source)
	if err := ImportRepositoryWithOptions("copilot", repo, WriteOptions{Experimental: true, Scope: "user", NativeHome: source}); err != nil {
		t.Fatal(err)
	}
	if nativeHash(before) != nativeHash(portabilitySnapshot(t, source)) {
		t.Fatal("import changed native source")
	}
	if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	got, err := parseNative([]byte(readNativeTest(t, filepath.Join(target, "settings.json"))), "json")
	if err != nil || nativeHash(got) != nativeHash(values) {
		t.Fatal("subagent roundtrip changed values", got, err)
	}
	again := t.TempDir()
	if err := ImportRepositoryWithOptions("copilot", again, WriteOptions{Experimental: true, Scope: "user", NativeHome: target}); err != nil {
		t.Fatal(err)
	}
	got, err = parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents/native/com.github.copilot/settings.json"))), "json")
	if err != nil || nativeHash(got) != nativeHash(values) {
		t.Fatal("reimport changed values", got, err)
	}
}

func TestCopilotSubagentPlanReportsAccountPrerequisite(t *testing.T) {
	repo := copilotLegacyFixture(t, "user", map[string]any{"subagents": map[string]any{"maxDepth": json.Number("1")}})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	plan, err := PlanProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Native.RequiredActions, " "), "usage-based billing") {
		t.Fatal("native billing prerequisite missing from plan")
	}
	found := false
	for _, feature := range plan.Native.Features {
		if feature.Feature == "config:subagents" {
			found = true
			if feature.NativeStatus != "native-prerequisite-required" || feature.Activation != "requires native usage-based billing and reload" {
				t.Fatal("account-dependent limit reported as effective", feature)
			}
		}
	}
	if !found {
		t.Fatal("subagent feature missing")
	}
}

func TestCopilotSubagentPreferencesRefuseLossyValues(t *testing.T) {
	cases := []map[string]any{
		{"maxConcurrency": json.Number("33")},
		{"maxConcurrency": json.Number("1.0000000000000000000001")},
		{"maxDepth": json.Number("257")},
		{"maxDepth": json.Number("0")},
		{"maxDepth": json.Number("-1")},
		{"disabledSubagents": []any{"task", "rubber-duck"}},
		{"disabledSubagents": []any{""}},
		{"agents": map[string]any{"": map[string]any{"model": "inherit"}}},
		{"agents": map[string]any{"explore": map[string]any{"model": ""}}},
		{"agents": map[string]any{"explore": map[string]any{"model": "bad\x00name"}}},
		{"agents": map[string]any{"explore": map[string]any{"effortLevel": "future-effort"}}},
		{"agents": map[string]any{"explore": map[string]any{"contextTier": "future-context"}}},
		{"agents": map[string]any{"explore": map[string]any{"unknown": true}}},
		{"agents": map[string]any{"explore": map[string]any{"permissions": "allow-all"}}},
	}
	for _, value := range cases {
		t.Run(nativeHash(value), func(t *testing.T) {
			if nativeMappedValue("copilot", "user", "subagents", value) {
				t.Fatal("invalid or unmapped dispatch settings accepted", value)
			}
			repo := copilotLegacyFixture(t, "user", map[string]any{"subagents": value})
			home := t.TempDir()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true, Scope: "user", NativeHome: home}); err == nil {
				t.Fatal("required invalid subagent setting activated")
			}
			if _, err := os.Stat(filepath.Join(home, "settings.json")); !os.IsNotExist(err) {
				t.Fatal("refusal wrote native settings")
			}
		})
	}
}

func TestCopilotSubagentOptionalFieldsAndAtomicDisable(t *testing.T) {
	values := map[string]any{"subagents": map[string]any{
		"agents":            map[string]any{"explore": map[string]any{"model": "inherit", "unknown": true}},
		"disabledSubagents": []any{"task", "rubber-duck"},
	}}
	before := nativeHash(values)
	selected, inactive := nativeSelectConfig("copilot", "user", values)
	want := map[string]any{"subagents": map[string]any{"agents": map[string]any{"explore": map[string]any{"model": "inherit"}}}}
	if len(inactive) == 0 || nativeHash(selected) != nativeHash(want) || nativeHash(values) != before {
		t.Fatal("optional subagent fields lost atomicity or changed source", selected, inactive)
	}
}
