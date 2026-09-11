package config

// Dynamic resource names and UI action names do not confer configuration
// authority. Their nested configuration fields still pass the policy checks.
func nativePolicyField(vendor string, path []string) bool {
	if len(path) < 2 {
		return true
	}
	if vendor == "copilot" && len(path) == 3 && path[0] == "subagents" && path[1] == "agents" {
		return false
	}
	if vendor == "codex" {
		if len(path) == 3 && path[0] == "model_providers" {
			switch path[2] {
			case "requires_openai_auth":
				// This Boolean selects native account authentication. Dropping
				// true would restore the false default; it is not a credential.
				return false
			case "auth":
				// This table configures a token command. Its fields must pass
				// the complete-unit check; returned tokens remain external.
				return false
			}
		}
		if path[0] == "tui" && path[1] == "keymap" {
			return false
		}
		if len(path) == 2 {
			switch path[0] {
			case "agents", "model_providers", "mcp_servers", "plugins", "marketplaces", "apps":
				return false
			}
		}
		if len(path) == 4 && path[0] == "plugins" && path[2] == "mcp_servers" {
			return false
		}
		if len(path) == 6 && path[0] == "plugins" && path[2] == "mcp_servers" && path[4] == "tools" {
			return false
		}
		// These map values are environment variable names, not header values.
		if len(path) == 4 && (path[0] == "model_providers" || path[0] == "mcp_servers") && path[2] == "env_http_headers" {
			return false
		}
	} else if vendor == "copilot" && len(path) == 4 && path[0] == "lspServers" && path[2] == "fileExtensions" {
		return false
	} else if vendor == "copilot" && len(path) == 2 {
		switch path[0] {
		case "lspServers", "mcpServers", "enabledPlugins", "extraKnownMarketplaces":
			return false
		}
	}
	return true
}
