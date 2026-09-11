package config

import (
	"fmt"
	"net/url"
	"strings"
)

// These definitions have no common disabled value that avoids native fallback
// or connection attempts. Refuse a lossy import/projection before filtering.
func nativeCheckRuntimeAuthentication(vendor string, values map[string]any) error {
	groups := []string{"mcp_servers", "model_providers"}
	if vendor == "copilot" {
		groups = []string{"mcpServers", "lspServers"}
	}
	for _, group := range groups {
		units, _ := values[group].(map[string]any)
		for _, name := range nativeSortedKeys(units) {
			unit, ok := units[name].(map[string]any)
			if !ok {
				continue
			} // The native schema reports malformed definitions.
			path := []string{group, name}
			fail := func(reason string) error {
				return fmt.Errorf("native authentication in /%s/%s cannot be preserved: %s; source remains unchanged", nativePointer(group), nativePointer(name), reason)
			}
			if nativeContainsExcluded(vendor, path, unit) {
				return fail("the definition contains an excluded credential or authority field")
			}
			if vendor == "codex" && group == "model_providers" {
				if auth, exists := unit["auth"]; exists {
					fields, object := auth.(map[string]any)
					// Validate this object before optional-field selection. A partial
					// token command can change authentication or restore a default.
					if !object || !nativeCodexValue("model_providers", map[string]any{"fixture": map[string]any{"auth": auth}}) {
						return fail("auth must be a complete token-command configuration for Codex " + nativePinnedVersion(vendor))
					}
					if strings.TrimSpace(fields["command"].(string)) == "" {
						return fail("auth.command must not be empty")
					}
					for _, key := range []string{"env_key", "experimental_bearer_token", "aws"} {
						if _, exists := unit[key]; exists {
							return fail("auth cannot be combined with " + key)
						}
					}
					if unit["requires_openai_auth"] == true {
						return fail("auth cannot be combined with requires_openai_auth=true")
					}
				}
				if v, exists := unit["requires_openai_auth"]; exists {
					if _, ok := v.(bool); !ok {
						return fail("requires_openai_auth must be a Boolean")
					}
				}
			}
			if group == "lspServers" {
				continue
			}
			for _, key := range []string{"env_key", "bearer_token_env_var"} {
				if v, exists := unit[key]; exists {
					if text, ok := v.(string); !ok || text == "" {
						return fail(key + " must be a non-empty environment variable name")
					}
				}
			}
			for _, key := range []string{"headers", "http_headers", "env_http_headers", "env"} {
				if v, exists := unit[key]; exists {
					fields, ok := v.(map[string]any)
					if !ok {
						return fail(key + " must be an object of strings")
					}
					for _, value := range fields {
						if _, ok := value.(string); !ok {
							return fail(key + " must contain only strings")
						}
					}
				}
			}
			for _, key := range []string{"url", "base_url"} {
				if v, exists := unit[key]; exists {
					text, ok := v.(string)
					if !ok {
						return fail(key + " must be a string")
					}
					parsed, err := url.Parse(text)
					if err != nil || parsed.User != nil {
						return fail(key + " is malformed or contains credentials")
					}
					query, err := url.ParseQuery(parsed.RawQuery)
					if err != nil {
						return fail(key + " has a malformed query")
					}
					for field := range query {
						if nativeForbidden(field) || strings.Contains(strings.ToLower(field), "token") {
							return fail(key + " contains a credential query field")
						}
					}
				}
			}
		}
	}
	return nil
}

func nativeCodexCommandAuthFeature(feature NativeFeature) NativeFeature {
	feature.NativeStatus = "native-authentication-fallback"
	feature.Limitation = "Codex 0.154.0 user-scope token commands preserve command, arguments, working directory, timeout, caching, and refresh configuration. Success, timed refresh, and 401 retry have local native evidence. Timeout, empty output, nonzero exit, invalid UTF-8, and a missing executable log an error but still send an unauthenticated model request. This is native configuration, not authentication enforcement. Install and check the token command separately; apply does not execute it or copy its returned token. Project behavior, remote services, and background descendants are not verified."
	for _, name := range []string{"success", "cache", "refresh", "retry", "timeout", "empty", "exit", "invalid-utf8", "missing"} {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-command-auth-"+name+"-verified.json")
	}
	return feature
}
