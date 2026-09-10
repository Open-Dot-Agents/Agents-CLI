package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pluginFixture(t *testing.T, vendor, scope string, values map[string]any) string {
	t.Helper()
	repo := t.TempDir()
	ns, err := nativeNamespace(vendor)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repo, ".agents")
	dir := filepath.Join(root, "plugins", ns)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path, format, err := nativeTargetPath(vendor, scope, repo, nativeArtifact{Kind: "config"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := nativeEncode(values, format)
	if err != nil {
		t.Fatal(err)
	}
	p := nativeProfile{Namespace: ns, HarnessVersion: "=" + nativePinnedVersion(vendor), Scope: scope, Required: true, Artifacts: []nativeArtifact{{Kind: "config", Source: filepath.Base(path)}}}
	metadata, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{filepath.Join(root, "AGENTS.md"): []byte("Fixture instructions.\n"), filepath.Join(root, "manifest.json"): []byte(`{"version":"1.1.0-draft.2","profiles":["plugins"]}`), filepath.Join(dir, "profile.json"): metadata, filepath.Join(dir, filepath.Base(path)): data}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func pluginValues(vendor, name string, enabled bool) map[string]any {
	if vendor == "codex" {
		return map[string]any{"plugins": map[string]any{name: map[string]any{"enabled": enabled}}}
	}
	return map[string]any{"enabledPlugins": map[string]any{name: enabled}}
}

func TestPluginSelectionImportApplyReimport(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		for _, scope := range []string{"project", "user"} {
			t.Run(vendor+"-"+scope, func(t *testing.T) {
				t.Setenv("XDG_STATE_HOME", t.TempDir())
				ns, _ := nativeNamespace(vendor)
				seed, home, repo := t.TempDir(), t.TempDir(), t.TempDir()
				path, format, _ := nativeTargetPath(vendor, scope, seed, nativeArtifact{Kind: "config"})
				values := pluginValues(vendor, "fixture@market", true)
				if vendor == "codex" {
					values["marketplaces"] = map[string]any{"market": map[string]any{"source_type": "local", "source": seed, "last_revision": "external-state", "last_updated": "external-clock"}}
				} else {
					values["extraKnownMarketplaces"] = map[string]any{"market": map[string]any{"source": map[string]any{"source": "directory", "path": seed}}}
				}
				data, _ := nativeEncode(values, format)
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, data, 0600)
				importRoot := seed
				write := WriteOptions{Experimental: true, Scope: scope}
				if scope == "user" {
					importRoot = repo
					write.NativeHome = seed
				}
				if err := ImportRepositoryWithOptions(vendor, importRoot, write); err != nil {
					t.Fatal(err)
				}
				selected := filepath.Join(importRoot, ".agents/plugins", ns, filepath.Base(path))
				if strings.Contains(readNativeTest(t, selected), "external-") {
					t.Fatal("marketplace runtime state imported")
				}
				if got := readNativeTest(t, path); got != string(data) {
					t.Fatal("native import changed source")
				}
				options := ApplyOptions{Experimental: true, Scope: scope}
				if scope == "user" {
					options.NativeHome = home
				} else {
					os.Remove(path)
				}
				if _, err := ApplyProjection(vendor, importRoot, options); err != nil {
					t.Fatal(err)
				}
				plan, err := PlanProjection(vendor, importRoot, options)
				if err != nil || len(plan.Actions) != 0 {
					t.Fatal("selection not idempotent", err, plan.Actions)
				}
				if !strings.Contains(strings.Join(plan.Native.RequiredActions, " "), "does not install") {
					t.Fatal("missing native installation boundary")
				}
				again := t.TempDir()
				dest := home
				if scope == "project" {
					dest = again
					out, _, _ := nativeTargetPath(vendor, scope, importRoot, nativeArtifact{Kind: "config"})
					copyTo, _, _ := nativeTargetPath(vendor, scope, again, nativeArtifact{Kind: "config"})
					os.MkdirAll(filepath.Dir(copyTo), 0700)
					os.WriteFile(copyTo, []byte(readNativeTest(t, out)), 0600)
				}
				write.NativeHome = ""
				if scope == "user" {
					write.NativeHome = dest
				}
				if err := ImportRepositoryWithOptions(vendor, again, write); err != nil {
					t.Fatal(err)
				}
				before, _ := parseNative([]byte(readNativeTest(t, selected)), format)
				after, _ := parseNative([]byte(readNativeTest(t, filepath.Join(again, ".agents/plugins", ns, filepath.Base(path)))), format)
				if nativeHash(before) != nativeHash(after) {
					t.Fatal("plugin selection changed during round trip")
				}
			})
		}
	}
}

func TestPluginSelectionAuthorityAndAtomicEntries(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			values := pluginValues(vendor, "fixture@market", true)
			if vendor == "codex" {
				values["plugins"].(map[string]any)["fixture@market"].(map[string]any)["future"] = true
			} else {
				values["enabledPlugins"].(map[string]any)["fixture@market"] = "true"
			}
			repo := pluginFixture(t, vendor, "project", values)
			if _, err := ApplyProjection(vendor, repo, ApplyOptions{Experimental: true}); err == nil {
				t.Fatal("required unknown plugin entry activated")
			}
			path, _, _ := nativeTargetPath(vendor, "project", repo, nativeArtifact{Kind: "config"})
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal wrote native configuration")
			}
			for _, credential := range []string{"https://user:sentinel-secret@example.invalid/repo", "https://example.invalid/repo?token=sentinel-secret"} {
				values := pluginValues(vendor, credential, true)
				if err := nativeCheckPluginImport(vendor, values); err == nil || strings.Contains(err.Error(), "sentinel-secret") {
					t.Fatal("credential reference not safely refused", err)
				}
			}
			repo = pluginFixture(t, vendor, "project", map[string]any{"model": "unrelated"})
			if _, err := ApplyProjection(vendor, repo, ApplyOptions{Experimental: true}); err == nil {
				t.Fatal("plugin artifact wrote unrelated settings")
			}
		})
	}
}

func TestPluginSelectionSharedOwnershipAndRollback(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		t.Run(vendor, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			home := t.TempDir()
			a := pluginFixture(t, vendor, "user", pluginValues(vendor, "one@market", true))
			b := pluginFixture(t, vendor, "user", pluginValues(vendor, "two@market", false))
			options := ApplyOptions{Experimental: true, Scope: "user", NativeHome: home, Force: true}
			for _, repo := range []string{a, b} {
				if _, err := ApplyProjection(vendor, repo, options); err != nil {
					t.Fatal(err)
				}
			}
			path, _, _ := nativeTargetPath(vendor, "user", home, nativeArtifact{Kind: "config"})
			before := readNativeTest(t, path)
			if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
				t.Fatal("new user configuration is not private")
			}
			conflict := pluginFixture(t, vendor, "user", pluginValues(vendor, "one@market", false))
			if _, err := ApplyProjection(vendor, conflict, options); err == nil {
				t.Fatal("force replaced another repository's plugin")
			}
			os.WriteFile(filepath.Join(a, ".agents/manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":[]}`), 0600)
			options.Backup = true
			homeBefore := portabilitySnapshot(t, home)
			stateBefore := portabilitySnapshot(t, state)
			build, err := buildNativeProjection(vendor, a, options)
			if err != nil {
				t.Fatal(err)
			}
			err = nativeRunTransaction(build.changes, func(stage string, index int) error {
				if stage == "after-write" && index == len(build.changes)-1 {
					return fmt.Errorf("fixture rollback")
				}
				return nil
			})
			if err == nil || readNativeTest(t, path) != before {
				t.Fatal("plugin removal did not roll back", err)
			}
			if nativeHash(homeBefore) != nativeHash(portabilitySnapshot(t, home)) || nativeHash(stateBefore) != nativeHash(portabilitySnapshot(t, state)) {
				t.Fatal("rollback changed ownership or left a backup")
			}
			if _, err := ApplyProjection(vendor, a, options); err != nil {
				t.Fatal(err)
			}
			after := readNativeTest(t, path)
			if strings.Contains(after, "one@market") || !strings.Contains(after, "two@market") {
				t.Fatal("removal changed foreign selection")
			}
		})
	}
}

func TestPluginSelectionImportKeepsDeclaredSource(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		for _, directory := range []string{"native", "plugins"} {
			for _, custom := range []bool{false, true} {
				t.Run(fmt.Sprint(vendor, "-", directory, "-", custom), func(t *testing.T) {
					values := pluginValues(vendor, "fixture@market", true)
					repo := pluginFixture(t, vendor, "project", values)
					ns, _ := nativeNamespace(vendor)
					root := filepath.Join(repo, ".agents")
					if directory == "native" {
						if err := os.Rename(filepath.Join(root, "plugins"), filepath.Join(root, "native")); err != nil {
							t.Fatal(err)
						}
						os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"version":"1.1.0-draft.2","profiles":["native"]}`), 0600)
					}
					dir := filepath.Join(root, directory, ns)
					path, format, _ := nativeTargetPath(vendor, "project", repo, nativeArtifact{Kind: "config"})
					var profile nativeProfile
					if err := nativeDecodePolicy(filepath.Join(dir, "profile.json"), &profile); err != nil {
						t.Fatal(err)
					}
					if custom {
						if err := os.Rename(filepath.Join(dir, profile.Artifacts[0].Source), filepath.Join(dir, "custom.fragment")); err != nil {
							t.Fatal(err)
						}
						profile.Artifacts[0].Source = "custom.fragment"
						metadata, _ := json.Marshal(profile)
						os.WriteFile(filepath.Join(dir, "profile.json"), metadata, 0600)
					}
					// Add a marketplace root while keeping the prior selection. Both
					// roots can share a default destination without duplicate writes.
					if vendor == "codex" {
						values["marketplaces"] = map[string]any{"market": map[string]any{"source_type": "local", "source": repo}}
					} else {
						values["extraKnownMarketplaces"] = map[string]any{"market": map[string]any{"source": map[string]any{"source": "directory", "path": repo}}}
					}
					data, _ := nativeEncode(values, format)
					os.MkdirAll(filepath.Dir(path), 0700)
					os.WriteFile(path, data, 0600)
					for i := 0; i < 2; i++ {
						if err := ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true}); err != nil {
							t.Fatal(err)
						}
					}
					var after nativeProfile
					if err := nativeDecodePolicy(filepath.Join(dir, "profile.json"), &after); err != nil {
						t.Fatal(err)
					}
					if !after.Required || after.Scope != profile.Scope || after.HarnessVersion != profile.HarnessVersion || after.Artifacts[0] != profile.Artifacts[0] {
						t.Fatal("import changed the prior declaration or required status")
					}
					if _, err := PlanProjection(vendor, repo, ApplyOptions{Experimental: true}); err != nil {
						t.Fatal("import made conflicting projections", err)
					}
				})
			}
		}
	}
}

func TestPluginCredentialImportRefusesBeforeCanonicalWrites(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		for _, payload := range []any{
			[]any{map[string]any{"password": "sentinel-secret"}},
			map[string]any{"url": "https://user:sentinel-secret@example.invalid/plugin"},
		} {
			t.Run(vendor+fmt.Sprintf("-%T", payload), func(t *testing.T) {
				repo := pluginFixture(t, vendor, "project", pluginValues(vendor, "fixture@market", true))
				root := filepath.Join(repo, ".agents")
				before := portabilitySnapshot(t, root)
				key := "marketplaces"
				if vendor == "copilot" {
					key = "extraKnownMarketplaces"
				}
				path, format, _ := nativeTargetPath(vendor, "project", repo, nativeArtifact{Kind: "config"})
				data, err := nativeEncode(map[string]any{key: map[string]any{"fixture": map[string]any{"future": payload}}}, format)
				if err != nil {
					t.Fatal(err)
				}
				os.MkdirAll(filepath.Dir(path), 0700)
				os.WriteFile(path, data, 0600)
				err = ImportRepositoryWithOptions(vendor, repo, WriteOptions{Experimental: true, Force: true})
				if err == nil || strings.Contains(err.Error(), "sentinel-secret") {
					t.Fatal("credential import not safely refused", err)
				}
				if nativeHash(before) != nativeHash(portabilitySnapshot(t, root)) {
					t.Fatal("refused import changed canonical files")
				}
			})
		}
	}
}

func TestPluginStateOnlyAndDynamicNames(t *testing.T) {
	values := map[string]any{"marketplaces": map[string]any{"fixture": map[string]any{"last_revision": "native-revision", "last_updated": "native-clock"}}}
	filtered, _ := nativeImportFilter("codex", values, nil)
	if len(filtered) != 0 {
		t.Fatal("state-only marketplace imported", filtered)
	}
	values = map[string]any{"plugins": map[string]any{"auth@market": map[string]any{"enabled": true, "mcp_servers": map[string]any{"auth": map[string]any{"enabled": true}}}}}
	if err := nativeCheckPluginImport("codex", values); err != nil {
		t.Fatal("dynamic server name treated as authority", err)
	}
	selected, inactive := nativeSelectPlugins("codex", "project", values)
	if len(inactive) != 0 || nativeHash(values) != nativeHash(selected) {
		t.Fatal("dynamic server name was not preserved", inactive)
	}
}

func TestPluginProfileVersionGate(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.1.0-draft.1"} {
		repo := pluginFixture(t, "codex", "project", pluginValues("codex", "fixture@market", false))
		os.WriteFile(filepath.Join(repo, ".agents/manifest.json"), []byte(fmt.Sprintf(`{"version":%q,"profiles":["plugins"]}`, version)), 0600)
		if err := ValidateRepositoryWithOptions(repo, true); err == nil {
			t.Fatal("plugins widened prior version", version)
		}
	}
}

func TestPluginMarketplaceEntriesStayAtomic(t *testing.T) {
	for _, vendor := range []string{"codex", "copilot"} {
		key := "marketplaces"
		valid := map[string]any{"source_type": "local", "source": "/fixture"}
		if vendor == "copilot" {
			key = "extraKnownMarketplaces"
			valid = map[string]any{"source": map[string]any{"source": "directory", "path": "/fixture"}}
		}
		unknown := map[string]any{"future": true}
		for k, v := range valid {
			unknown[k] = v
		}
		values := map[string]any{key: map[string]any{"good": valid, "future": unknown, "malformed": []any{true}}}
		before := nativeHash(values)
		selected, inactive := nativeSelectPlugins(vendor, "project", values)
		entries, _ := selected[key].(map[string]any)
		if len(entries) != 1 || entries["good"] == nil || len(inactive) != 2 || nativeHash(values) != before {
			t.Fatal("plugin selection lost source content or partially activated an entry", selected, inactive)
		}
	}
}

func TestPluginMarketplaceAutoUpdateScope(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		values := map[string]any{"extraKnownMarketplaces": map[string]any{"market": map[string]any{
			"source": map[string]any{"source": "git", "url": "https://example.invalid/market.git"}, "autoUpdate": enabled,
		}}}
		before := nativeHash(values)
		project, inactive := nativeSelectPlugins("copilot", "project", values)
		if len(project) != 0 || len(inactive) != 1 || !strings.Contains(inactive[0].Reason, "ignores") {
			t.Fatal("ignored project auto-update was activated", project, inactive)
		}
		user, inactive := nativeSelectPlugins("copilot", "user", values)
		if len(inactive) != 0 || nativeHash(user) != before || nativeHash(values) != before {
			t.Fatal("user auto-update setting was lost or source was changed", user, inactive)
		}
		repo := pluginFixture(t, "copilot", "project", values)
		if _, err := ApplyProjection("copilot", repo, ApplyOptions{Experimental: true}); err == nil {
			t.Fatal("required ignored project setting did not refuse")
		}
	}
}
