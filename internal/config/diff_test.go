package config

import (
	"strings"
	"testing"
)

func TestRenderChangeSummary(t *testing.T) {
	before := FileSnapshot{
		".agents/removed.md": []byte("old\n"),
		".github/mcp.json":   []byte("unchanged\n"),
		".codex/config.toml": []byte("before\nshared\n"),
	}
	after := FileSnapshot{
		".agents/added.md":   []byte("new\n"),
		".github/mcp.json":   []byte("unchanged\n"),
		".codex/config.toml": []byte("after\nshared\n"),
	}

	summary := RenderChangeSummary(before, after)
	for _, expected := range []string{
		"A  .agents/added.md (+1)",
		"M  .codex/config.toml (+1 -1)",
		"D  .agents/removed.md (-1)",
	} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("summary %q does not contain %q", summary, expected)
		}
	}
	if strings.Contains(summary, ".github/mcp.json") {
		t.Fatalf("summary includes unchanged file: %q", summary)
	}
}
