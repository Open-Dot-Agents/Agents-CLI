#!/usr/bin/env python3
"""Check semantic context and frozen source coverage."""
import collections
import copy
import hashlib
import json
import unittest
from native_coverage import ROOT, INVENTORY, build, classify, compiled_registry, setting_declaration


class NativeCoverageTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.inventory = json.loads(INVENTORY.read_text())
        cls.registry = compiled_registry()
        cls.result = build(cls.inventory, cls.registry)

    def feature(self, vendor, context, name):
        return next(f for f in self.result['features'] if (f['vendor'], f['context'], f['native']) == (vendor, context, name))

    def test_original_rows_exactly_once(self):
        rows = [e['entry_id'] for f in self.result['features'] for e in f['evidence']]
        self.assertEqual(collections.Counter(rows), collections.Counter('source-entry-' + str(i) for i in range(1, 1680)))

    def test_native_artifact_paths_use_existing_scope_declarations(self):
        for name, kind in [('agents/', 'agent'), ('hooks/', 'hooks'), ('mcp-config.json', 'mcp'), ('lsp-config.json', 'lsp')]:
            feature = self.feature('copilot', 'artifact', name)
            declaration = next(f for f in self.registry['copilot']['__artifacts__'] if f['feature'] == 'artifact:'+kind and f['scope'] == 'user')
            self.assertEqual(feature['disposition'], declaration['disposition'])
            self.assertEqual(feature['implemented_scopes'], ['user'])
            self.assertEqual(feature['native_status'], declaration['native_status'])
            self.assertEqual(feature['native_evidence'], sorted(declaration['evidence']))
            self.assertIn(declaration['limitation'], feature['limitations'])
            self.assertEqual(feature['native_targets'], [declaration['destination']])

    def test_concrete_bedrock_names_link_to_declared_validators_only(self):
        for leaf in ('profile', 'region'):
            feature = self.feature('codex', 'settings', 'model_providers.amazon-bedrock.aws.'+leaf)
            self.assertEqual(feature['disposition'], 'validator-declared')
            self.assertEqual(feature['validation_paths'], ['model_providers.<name>.aws.'+leaf])
            self.assertEqual(feature['implemented_scopes'], ['user'])
            self.assertEqual(feature['native_status'], 'unverified')
            self.assertEqual(feature['scope_dispositions'], {'project': 'native-ignored'})
            self.assertNotIn('native_evidence', feature)

    def test_user_directory_aliases_do_not_complete_missing_mappings(self):
        for name, location in [('skills/', '~/.copilot/skills/'), ('copilot-instructions.md', '$HOME/.copilot/copilot-instructions.md')]:
            alias = self.feature('copilot', 'artifact', name)
            target = self.feature('copilot', 'discovery-location', location)
            self.assertFalse(alias['counted_feature'])
            self.assertEqual(alias['disposition'], 'syntax-reference')
            self.assertEqual(alias['describes'], [target['id']])
            self.assertEqual(target['scope'], ['user'])
            self.assertEqual(target['disposition'], 'mapping-pending' if name == 'skills/' else 'artifact-mapping')

    def test_user_instruction_discovery_has_bounded_native_evidence(self):
        feature = self.feature('copilot', 'discovery-location', '$HOME/.copilot/copilot-instructions.md')
        self.assertEqual(feature['implemented_scopes'], ['user'])
        self.assertEqual(feature['native_status'], 'bounded-user-instruction-loading')
        self.assertIn('declared namespace source', feature['limitations'][0])
        self.assertIn('Referenced files remain external', feature['limitations'][0])
        self.assertIn('WORKBENCH/conformance/verify_copilot_user_instructions.py', feature['native_evidence'])

    def test_declaration_lookup_requires_unambiguous_component_matches(self):
        broad = {'path': 'providers.<name>.region'}
        exact = {'path': 'providers.fixture.region'}
        registry = {'providers.<name>.region': broad, 'providers.fixture.region': exact}
        self.assertIs(setting_declaration(registry, 'providers.fixture.region'), exact)
        self.assertIs(setting_declaration(registry, 'providers.amazon-bedrock.region'), broad)
        for name in ('providers.nested.name.region', 'providers..region', 'providers.fixture[].region', 'providers.fixture.unknown'):
            self.assertIsNone(setting_declaration(registry, name))
        overlapping = {'providers.<name>.region': broad, 'providers.fixture.<name>': {'path': 'providers.fixture.<name>'}}
        with self.assertRaisesRegex(ValueError, 'ambiguous'):
            setting_declaration(overlapping, 'providers.fixture.region')

    def test_artifact_link_does_not_invent_a_missing_declaration(self):
        registry = copy.deepcopy(self.registry)
        registry['copilot']['__artifacts__'] = [f for f in registry['copilot']['__artifacts__'] if not (f['feature'] == 'artifact:lsp' and f['scope'] == 'user')]
        result = build(self.inventory, registry)
        feature = next(f for f in result['features'] if f['vendor'] == 'copilot' and f['native'] == 'lsp-config.json')
        self.assertEqual(feature['disposition'], 'mapping-pending')
        self.assertNotIn('native_evidence', feature)

    def test_native_link_fix_preserves_source_ids_and_failure_evidence(self):
        path = ROOT / 'CLI/scripts/testdata/native_coverage/native-links-regression.json'
        baseline = json.loads(path.read_text())
        self.assertEqual(baseline['source_sha256'], self.result['source_sha256'])
        by_id = {f['id']: f for f in self.result['features']}
        for old in baseline['features']:
            current = by_id[old['id']]
            self.assertEqual(current['native'], old['native'])
            self.assertEqual(current['evidence'], old['evidence'])

    def test_documented_discovery_paths_have_explicit_scopes(self):
        for name in ('.agents/skills/', '.github/copilot-instructions.md', 'AGENTS.md', 'CLAUDE.md', 'GEMINI.md', 'Parent .github/skills/'):
            self.assertEqual(self.feature('copilot', 'discovery-location', name)['scope'], ['project'])
        for name in ('~/.agents/skills/', '~/.copilot/skills/', '$HOME/.copilot/copilot-instructions.md'):
            self.assertEqual(self.feature('copilot', 'discovery-location', name)['scope'], ['user'])

    def test_role_references_have_relocation_evidence(self):
        feature = self.feature('codex', 'settings', 'agents.<name>.config_file')
        self.assertEqual(feature['native_status'], 'bounded-reference-relocation')
        self.assertEqual(feature['implemented_scopes'], ['project', 'user'])
        self.assertIn('remain external absolute references', feature['limitations'][0])
        self.assertEqual(len(feature['native_evidence']), 10)

    def test_skill_metadata_mappings_keep_native_limits(self):
        for name in ('name', 'description', 'argument-hint', 'allowed-tools', 'user-invocable', 'disable-model-invocation'):
            feature = self.feature('copilot', 'skill-frontmatter', name)
            self.assertEqual(feature['disposition'], 'artifact-field-mapping')
            self.assertEqual(feature['native_status'], 'bounded-skill-metadata')
            self.assertEqual(feature['implemented_scopes'], ['project', 'user'])
            self.assertTrue(feature['limitations'])
            self.assertIn('docs/COPILOT_SKILL_METADATA.md', feature['native_evidence'])
        self.assertIn('Other patterns and tools are unverified', self.feature('copilot', 'skill-frontmatter', 'allowed-tools')['limitations'][0])

    def test_discovery_invocation_references_are_not_duplicate_settings(self):
        for name, context, target_name in [('COPILOT_SKILLS_DIRS', 'environment', 'COPILOT_SKILLS_DIRS'),
                                            ('COPILOT_CUSTOM_INSTRUCTIONS_DIRS', 'environment', 'COPILOT_CUSTOM_INSTRUCTIONS_DIRS'),
                                            ('--add-dir <path>', 'invocation', '--add-dir')]:
            reference = self.feature('copilot', 'discovery-location', name)
            target = self.feature('copilot', context, target_name)
            self.assertEqual(reference['category'], 'invocation-option')
            self.assertEqual(reference['scope'], ['invocation'])
            self.assertEqual(reference['disposition'], 'syntax-reference')
            self.assertFalse(reference['counted_feature'])
            self.assertEqual(reference['describes'], [target['id']])
            self.assertEqual(target['disposition'], 'native-operation')
            self.assertTrue(reference['evidence'])

    def test_project_skill_import_does_not_claim_other_discovery_scopes(self):
        for name in ('.github/skills/', '.agents/skills/', '.claude/skills/'):
            feature = self.feature('copilot', 'discovery-location', name)
            self.assertEqual(feature['scope'], ['project'])
            self.assertEqual(feature['implemented_scopes'], ['project'])
            self.assertEqual(feature['disposition'], 'artifact-mapping')
            self.assertEqual(feature['native_status'], 'bounded-relocated-skill-execution')
            self.assertIn('Parent, user, plugin, and added-directory sources are not imported', feature['limitations'][0])
            self.assertIn('Existing .agents/skills packages are selected in place', feature['limitations'][0])

    def test_root_instruction_import_keeps_conversion_limits(self):
        feature = self.feature('copilot', 'discovery-location', 'AGENTS.md')
        self.assertEqual(feature['implemented_scopes'], ['project'])
        self.assertEqual(feature['adapter'], 'CLI/internal/config/native.go')
        self.assertEqual(feature['native_status'], 'bounded-root-instruction-loading')
        self.assertIn('preserve root @ references', ' '.join(feature['limitations']))
        self.assertIn('distinct root and Copilot instruction bodies', ' '.join(feature['limitations']))

    def test_recursive_instruction_paths_keep_scope_and_matching_limits(self):
        for name, scope in [('.github/instructions/**/*.instructions.md', 'project'),
                            ('$HOME/.copilot/instructions/**/*.instructions.md', 'user')]:
            feature = self.feature('copilot', 'discovery-location', name)
            self.assertEqual(feature['scope'], [scope])
            self.assertEqual(feature['implemented_scopes'], [scope])
            self.assertEqual(feature['disposition'], 'artifact-mapping')
            self.assertEqual(feature['native_status'], 'bounded-recursive-instruction-loading')
            self.assertIn('not automatic body injection or deterministic glob enforcement', feature['limitations'][0])
        alias = self.feature('copilot', 'artifact', 'instructions/')
        target = self.feature('copilot', 'discovery-location', '$HOME/.copilot/instructions/**/*.instructions.md')
        self.assertFalse(alias['counted_feature'])
        self.assertEqual(alias['describes'], [target['id']])
        self.assertEqual(alias['disposition'], 'syntax-reference')

    def test_agent_instruction_locations_keep_native_scope_limits(self):
        for name in ('CLAUDE.md', 'GEMINI.md'):
            feature = self.feature('copilot', 'discovery-location', name)
            self.assertEqual(feature['implemented_scopes'], ['project'])
            self.assertEqual(feature['adapter'], 'CLI/internal/config/native_agent_instructions.go')
            self.assertEqual(feature['native_status'], 'bounded-agent-instruction-loading')
            self.assertIn('not from the repository root', ' '.join(feature['limitations']))
            self.assertIn('does not expand @ references in GEMINI.md', ' '.join(feature['limitations']))

    def test_canonical_instruction_binding_keeps_one_semantic_feature(self):
        feature = self.feature('copilot', 'discovery-location', 'AGENTS.md')
        self.assertEqual(feature['implemented_scopes'], ['project'])
        self.assertEqual(feature['disposition'], 'portable-mapping')
        self.assertIn('fixed canonical-instructions binding', ' '.join(feature['limitations']))
        self.assertIn('docs/COPILOT_CANONICAL_INSTRUCTIONS.md', feature['native_evidence'])
        self.assertIn('WORKBENCH/conformance/verify_copilot_canonical_instructions.py', feature['native_evidence'])
        bindings = [f for f in self.result['native_artifacts']['copilot']
                    if f['feature'] == 'artifact:canonical-instructions']
        self.assertEqual(len(bindings), 1)
        binding = bindings[0]
        self.assertEqual(binding['scope'], 'project')
        self.assertEqual(binding['source'], '.agents/AGENTS.md')
        self.assertEqual(binding['destination'], '<project-root>/AGENTS.md')
        self.assertEqual(binding['native_status'], 'bounded-canonical-instruction-loading')
        self.assertFalse(any(f['native'] == 'canonical-instructions' for f in self.result['features']))

    def test_plugin_discovery_is_separate_from_execution(self):
        for vendor, names in [('codex', ['plugins.<name>.enabled', 'marketplaces.<name>.source', 'marketplaces.<name>.ref']),
                              ('copilot', ['enabledPlugins', 'extraKnownMarketplaces'])]:
            for name in names:
                feature = self.feature(vendor, 'settings', name)
                self.assertEqual(feature['native_status'], 'native-discovery-only')
                self.assertEqual(feature['disposition'], 'artifact-field-mapping')
                self.assertEqual(feature['implemented_scopes'], ['project', 'user'])
                self.assertTrue(any('selection-mcp-reviewed-' in path for path in feature['native_evidence']))
                self.assertTrue(any('git-mcp-reviewed-' in path for path in feature['native_evidence']))
            fields = [f for f in self.result['native_artifacts'][vendor] if f['feature'].startswith('artifact:plugins:')]
            self.assertTrue(fields)
            for field in fields:
                self.assertEqual(field['native_status'], 'native-discovery-only')
                self.assertIn('Public HTTPS/SSH services', field['limitation'])
        self.assertEqual(self.feature('codex', 'settings', 'plugins.<name>.mcp_servers.<name>.enabled')['native_status'], 'unverified')

    def test_plugin_mcp_execution_keeps_native_conformance_limits(self):
        for vendor in ('codex', 'copilot'):
            fields = [f for f in self.result['native_artifacts'][vendor] if f['feature'] == 'artifact:plugin-component:stdio']
            self.assertEqual(len(fields), 2)
            for field in fields:
                self.assertEqual(field['native_status'], 'bounded-fixture-execution')
                self.assertEqual(field['disposition'], 'native-operation')
                self.assertIn('not full package-format conformance', field['limitation'])
                if vendor == 'copilot': self.assertIn('violate the standard', field['limitation'])
                self.assertEqual(len(field['evidence']), 4 if vendor == 'codex' else 3)

    def test_copilot_preferences_do_not_share_permission_or_state_context(self):
        memory = self.feature('copilot', 'settings', 'memory')
        self.assertEqual(memory['disposition'], 'validator-declared')
        self.assertEqual(memory['implemented_scopes'], ['user'])
        self.assertEqual(self.feature('copilot', 'saved-permissions', 'memory')['disposition'], 'external')
        trust = self.feature('copilot', 'settings', 'trustedFolders')
        self.assertEqual(trust['category'], 'external-authority-state')
        self.assertEqual(trust['disposition'], 'external')
        for parent in ('ide', 'subagents', 'tabs'):
            self.assertIn(parent, self.registry['copilot'])

    def test_copilot_user_settings_are_not_state(self):
        for name in ('autoUpdate', 'banner'):
            feature = self.feature('copilot', 'settings', name)
            self.assertEqual(feature['category'], 'native-configuration')
            self.assertEqual(feature['disposition'], 'validator-declared')
            self.assertEqual(feature['implemented_scopes'], ['user'])

    def test_preference_evidence_is_scoped_and_limited(self):
        for name in ('defaultMode', 'tabs.enabled', 'tabs.sort', 'tabs.hide', 'statusLine'):
            feature = self.feature('copilot', 'settings', name)
            self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
            self.assertEqual(feature['implemented_scopes'], ['user'])
            self.assertEqual(feature['native_evidence'], ['WORKBENCH/evidence/native-draft2-debug/copilot-preferences-final.json'])
        self.assertIn('Autopilot execution', self.feature('copilot', 'settings', 'defaultMode')['limitations'][0])
        for name in ('inlineImages', 'inlineImageLiveWindow', 'notifications'):
            self.assertEqual(self.feature('copilot', 'settings', name)['native_status'], 'unverified')

    def test_subagent_mapping_distinguishes_account_prerequisites(self):
        for name in ('subagents.agents', 'subagents.agents.<name>', 'subagents.disabledSubagents'):
            feature = self.feature('copilot', 'settings', name)
            self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
            self.assertEqual(feature['implemented_scopes'], ['user'])
        for name in ('subagents.maxDepth', 'subagents.maxConcurrency'):
            feature = self.feature('copilot', 'settings', name)
            self.assertEqual(feature['native_status'], 'native-prerequisite-required')
            self.assertIn('usage-based billing', feature['limitations'][0])
            self.assertIn('two nested agents', feature['limitations'][0])

    def test_schema_array_indexes_resolve_without_new_ids(self):
        for leaf in ('path', 'enabled'):
            feature = self.feature('codex', 'settings', 'skills.config.<name>.' + leaf)
            self.assertEqual(feature['disposition'], 'artifact-field-mapping')
            self.assertEqual(feature['native_status'], 'bounded-fixture-context')
            self.assertEqual(feature['implemented_scopes'], ['user'])
            self.assertTrue(any(e['source'] == 'codex-config' for e in feature['evidence']))

    def test_otel_exporter_aliases_keep_finite_schema_choices(self):
        for exporter in ('exporter', 'trace_exporter'):
            for field in ('endpoint', 'headers', 'protocol', 'tls.ca-certificate', 'tls.client-certificate', 'tls.client-private-key'):
                feature = self.feature('codex', 'settings', 'otel.' + exporter + '.<name>.' + field)
                choices = ['otlp-http'] if field == 'protocol' else ['otlp-grpc', 'otlp-http']
                self.assertEqual(feature['validation_paths'], ['otel.' + exporter + '.' + choice + '.' + field for choice in choices])
                self.assertEqual(feature['implemented_scopes'], ['user'])
                identity = field.startswith('tls.client-')
                self.assertEqual(feature['native_status'], 'unverified' if identity else 'bounded-fixture-execution')
                self.assertEqual(feature['disposition'], 'validator-declared' if identity else 'artifact-field-mapping')
                if identity:
                    self.assertEqual(len(feature['native_variant_limitations']), 1)
                    variant = feature['native_variant_limitations'][0]
                    self.assertEqual(variant['native_status'], 'native-exporter-failure')
                    self.assertEqual(variant['activation'], 'inactive')
                    self.assertIn('/otlp-http/', variant['feature'])
        self.assertNotIn('otel', [f['feature'] for f in self.registry['codex']['__roots__'] if f['scope'] == 'project'])
        metrics = self.feature('codex', 'settings', 'otel.metrics_exporter')
        self.assertEqual(metrics['native_status'], 'bounded-fixture-execution')
        self.assertTrue(any('codex-otel-grpc-final.json' in path for path in metrics['native_evidence']))
        identities = [f for f in self.result['native_artifacts']['codex'] if '/otlp-grpc/tls/client-private-key' in f['feature']]
        self.assertEqual(len(identities), 3)
        for feature in identities:
            self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
            self.assertTrue(any('codex-otel-grpc-mutual-final.json' in path for path in feature['evidence']))

    def test_frontmatter_does_not_reuse_root_setting_validator(self):
        feature = self.feature('copilot', 'agent-frontmatter', 'model')
        self.assertEqual(feature['disposition'], 'validator-declared')
        self.assertEqual(feature['native_status'], 'unverified')
        self.assertTrue(feature['validation_source'].endswith('native_copilot_agent.go'))
        self.assertNotEqual(feature['id'], self.feature('copilot', 'settings', 'model')['id'])

    def test_managed_model_has_separate_authority(self):
        feature = self.feature('copilot', 'managed-policy', 'model')
        self.assertEqual(feature['disposition'], 'external')
        self.assertEqual(feature['scope'], ['managed'])

    def test_mcp_fields_are_not_commands(self):
        self.assertEqual(self.feature('copilot', 'mcp', 'command')['category'], 'portable-configuration')
        self.assertEqual(self.feature('copilot', 'mcp', 'headers')['category'], 'native-configuration')

    def test_mcp_fields_use_the_native_artifact_validator(self):
        for name in ('command', 'headers', 'tools'):
            feature = self.feature('copilot', 'mcp', name)
            self.assertTrue(feature['validation_source'].endswith('native_copilot_mcp.go'))
            self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
        self.assertEqual(self.feature('copilot', 'mcp', 'timeout')['native_status'], 'unverified')

    def test_agent_local_mcp_has_separate_evidence(self):
        feature = self.feature('copilot', 'agent-frontmatter', 'mcp-servers')
        self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
        self.assertTrue(feature['validation_source'].endswith('native_copilot_agent.go'))
        fields = [f for f in self.result['native_artifacts']['copilot'] if f['feature'] == 'artifact:agent:/mcp-servers']
        self.assertEqual({f['scope'] for f in fields}, {'project', 'user'})
        for field in fields:
            self.assertTrue(any('agent-mcp-final-' + field['scope'] in path for path in field['evidence']))
            self.assertTrue(any('agent-mcp-override-' + field['scope'] in path for path in field['evidence']))

    def test_hook_files_and_inline_settings_have_separate_evidence(self):
        feature = self.feature('copilot', 'settings', 'hooks')
        self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
        self.assertTrue(feature['validation_source'].endswith('native_copilot_hooks.go'))
        self.assertTrue(all('inline' in path for path in feature['native_evidence']))
        self.assertEqual(self.feature('copilot', 'settings', 'disableAllHooks')['disposition'], 'security-evidence-required')
        fields = [f for f in self.result['native_artifacts']['copilot'] if f['feature'].startswith('artifact:hooks:/')]
        self.assertEqual({f['scope'] for f in fields}, {'project', 'user'})
        for field in fields:
            if field['feature'].endswith('/prompt'):
                self.assertEqual(field['native_status'], 'unverified')
            if field['feature'].endswith('/timeout'):
                self.assertTrue(any('timeout-alias-' + field['scope'] in p for p in field['evidence']))

    def test_codex_hooks_use_native_registry_and_scope_evidence(self):
        root = self.feature('codex', 'settings', 'hooks')
        self.assertEqual(root['native_status'], 'bounded-fixture-execution')
        self.assertTrue(all('inline-' in path for path in root['native_evidence']))
        for name in ('hooks.<name>', 'hooks.<name>[].hooks', 'hooks.<name>[].hooks[].commandWindows'):
            feature = self.feature('codex', 'settings', name)
            self.assertEqual(feature['disposition'], 'validator-declared')
            self.assertEqual(feature['native_status'], 'unverified')
            self.assertTrue(feature['validation_source'].endswith('native_codex_hooks.go'))

        for name in ('hooks.<name>[].hooks[].async', 'hooks.<name>[].hooks[].additionalContextLimit'):
            feature = self.feature('codex', 'settings', name)
            self.assertEqual(feature['disposition'], 'artifact-field-mapping')
            self.assertEqual(feature['native_status'], 'bounded-fixture-execution')
            self.assertTrue(any('codex-background-final-' in path for path in feature['native_evidence']))

    def test_codex_mcp_hooks_have_separate_evidence(self):
        fields = [f for f in self.result['native_artifacts']['codex']
                  if '/hooks/<mcp_tool>/' in f['feature']]
        self.assertEqual(len(fields), 12)
        for field in fields:
            self.assertEqual(field['native_status'], 'bounded-fixture-execution')
            self.assertTrue(any('codex-mcp-hooks-final-' in path and
                                field['scope'] in path for path in field['evidence']))
            if field['feature'].endswith('/timeout'):
                self.assertTrue(all('timeout-' in path for path in field['evidence']))
                self.assertIn('does not prove MCP tool termination', field['limitation'])
            if field['feature'].endswith('/input'):
                self.assertIn('Null input values are refused', field['limitation'])

    def test_codex_background_and_context_limits_have_execution_evidence(self):
        for setting, required in [('async', ['background-next', 'background-active', 'background-deny', 'background-timeout', 'background-detached-timeout', 'background-unsubscribe', 'background-shutdown', 'background-archive']),
                                  ('additionalContextLimit', ['spill', 'spill-tiny', 'unlimited', 'spill-default', 'spill-mixed', 'background-spill', 'background-unlimited'])]:
            fields = [f for f in self.result['native_artifacts']['codex']
                      if f['feature'].endswith('/hooks/<command>/' + setting)]
            self.assertEqual(len(fields), 2)
            for field in fields:
                self.assertEqual(field['native_status'], 'bounded-fixture-execution')
                for scenario in required:
                    self.assertTrue(any('codex-background-final-' + scenario + '-' + field['scope'] in path
                                        for path in field['evidence']))
                if setting == 'async':
                    self.assertIn('a detached child can outlive the timeout', field['limitation'])
                else:
                    self.assertIn('spill-write failures need separate tests', field['limitation'])

    def test_alias_does_not_inflate_count(self):
        feature = self.feature('codex', 'settings', 'agents.max_concurrent_threads_per_session')
        self.assertIn('agents.max_threads', {e['native_name'] for e in feature['evidence']})

    def test_identity_does_not_depend_on_implementation(self):
        other = build(self.inventory, {'codex': {}, 'copilot': {}})
        self.assertEqual([f['id'] for f in self.result['features']], [f['id'] for f in other['features']])
        self.assertFalse(self.result['complete'])
        self.assertFalse(self.result['counts_are_completion'])

    def test_classification_retains_existing_ids_and_source_assignments(self):
        baseline = json.loads((ROOT / 'CLI/scripts/testdata/native_coverage/classification-baseline.json').read_text())
        current = {f['id']: sorted(e['entry_id'] for e in f['evidence']) for f in self.result['features']}
        self.assertEqual(current, baseline['identities'])
        self.assertEqual(self.result['source_sha256'], baseline['source_sha256'])

    def test_permission_values_and_selectors_are_linked_syntax(self):
        parent = self.feature('codex', 'settings', 'permissions.<name>.filesystem.<name>')
        for name in ('read', 'write', 'deny', ':root', ':minimal', ':workspace_roots', ':tmpdir', ':slash_tmp', '/absolute/path', '~/path'):
            feature = self.feature('codex', 'settings', name)
            self.assertEqual(feature['disposition'], 'syntax-reference')
            self.assertFalse(feature['counted_feature'])
            self.assertEqual(feature['describes'], [parent['id']])
            self.assertEqual(feature['native_status'], 'not-applicable')
            self.assertIsNone(feature['adapter'])
        self.assertEqual(parent['disposition'], 'security-evidence-required')
        self.assertEqual(parent['implemented_scopes'], [])
        self.assertEqual(parent['gate_scopes'], ['project', 'user'])

    def test_table_notation_and_empty_binding_do_not_inflate_counts(self):
        alias = self.feature('codex', 'settings', '[permissions.<name>.filesystem].<name>')
        parent = self.feature('codex', 'settings', 'permissions.<name>.filesystem.<name>')
        self.assertEqual(alias['record_kind'], 'alias')
        self.assertEqual(alias['describes'], [parent['id']])
        self.assertFalse(alias['counted_feature'])
        nested = self.feature('codex', 'settings', '[permissions.<name>.filesystem.<name>].<name>')
        self.assertTrue(nested['counted_feature'])
        self.assertEqual(nested['setting_path'], 'permissions.<name>.filesystem.<name>.<name>')
        self.assertEqual(nested['disposition'], 'security-evidence-required')
        unbind = self.feature('codex', 'settings', 'tui.keymap.<name>.<name> = []')
        self.assertEqual(unbind['describes'], [self.feature('codex', 'settings', 'tui.keymap.<name>.<name>')['id']])
        self.assertFalse(unbind['counted_feature'])

    def test_desktop_scope_and_platform_boundaries_have_explicit_dispositions(self):
        desktop = self.feature('codex', 'settings', 'desktop.custom_file_handlers.<name>.command')
        self.assertEqual(desktop['scope'], ['user'])
        self.assertEqual(desktop['disposition'], 'outside-milestone')
        self.assertEqual(desktop['milestone_scope'], 'outside-linux-cli')
        windows = self.feature('codex', 'settings', 'computer_use.windows.aumids')
        self.assertEqual(windows['disposition'], 'outside-milestone')
        managed = self.feature('codex', 'requirements', 'computer_use.windows.aumids')
        self.assertEqual(managed['disposition'], 'external')
        state = self.feature('codex', 'settings', 'windows_wsl_setup_acknowledged')
        self.assertEqual(state['category'], 'external-authority-state')
        self.assertEqual(state['disposition'], 'external')

    def test_counts_separate_records_features_and_milestone(self):
        records = self.result['features']
        features = [f for f in records if f['counted_feature']]
        milestone = [f for f in features if f['milestone_scope'] == 'linux-cli']
        for name, rows in [('record_counts', records), ('counts', features), ('milestone_counts', milestone)]:
            self.assertEqual(self.result[name], dict(collections.Counter(f['disposition'] for f in rows)))
        self.assertEqual(self.result['coverage_records'], len(records))
        self.assertEqual(self.result['semantic_features'], len(features))
        self.assertEqual(self.result['milestone_features'], len(milestone))
        self.assertGreater(len(records), len(features))
        self.assertGreater(len(features), len(milestone))
        self.assertFalse(any(not f['counted_feature'] for f in records if f['disposition'] in ('mapping-pending', 'validator-declared')))

    def test_provider_token_commands_keep_native_authentication_limit(self):
        for suffix in ('', '.command', '.args', '.cwd', '.timeout_ms', '.refresh_interval_ms'):
            feature = self.feature('codex', 'settings', 'model_providers.<name>.auth' + suffix)
            self.assertEqual(feature['disposition'], 'value-mapping')
            self.assertEqual(feature['implemented_scopes'], ['user'])
            self.assertEqual(feature['native_status'], 'native-authentication-fallback')
            self.assertIn('unauthenticated model request', ' '.join(feature['limitations']))
            self.assertEqual(len(feature['native_evidence']), 9)

    def test_native_project_scope_is_not_a_user_scope_support_claim(self):
        for name in ('model_provider', 'model_providers.<name>.auth.command', 'notify', 'openai_base_url'):
            feature = self.feature('codex', 'settings', name)
            self.assertEqual(feature['scope_dispositions'], {'project': 'native-ignored'})
            self.assertIn('WORKBENCH/evidence/native-draft2-debug/codex-project-allkeys-verified.json', feature['scope_evidence']['project'])
            self.assertNotIn('project', feature.get('implemented_scopes', []))
        self.assertEqual(self.feature('codex', 'settings', 'model_providers.<name>.auth.command')['native_status'], 'native-authentication-fallback')
        proxy = [f for f in self.result['native_artifacts']['codex'] if f['feature'] == 'artifact:project-scope:/features/respect_system_proxy']
        self.assertEqual(len(proxy), 1)
        self.assertEqual(proxy[0]['disposition'], 'native-ignored')

    def test_lsp_fields_have_separate_artifact_evidence(self):
        artifacts = self.result['native_artifacts']['copilot']
        fields = [f for f in artifacts if f['feature'].startswith('artifact:lsp:/')]
        self.assertEqual(len(fields), 14)
        for field in fields:
            self.assertIn(field['scope'], ('project', 'user'))
            self.assertEqual(field['native_status'], 'bounded-fixture-execution')
            if field['feature'].endswith('/requestTimeoutMs'):
                self.assertTrue(any('timeout' in path for path in field['evidence']))
            self.assertTrue(field['evidence'])
        self.assertFalse(any('lspServers' in f['path'] for f in self.registry['copilot'].values() if isinstance(f, dict)))

    def test_generated_file_is_current(self):
        self.assertEqual(self.result, json.loads((ROOT / '.agents/features/coverage.json').read_text()))


if __name__ == '__main__':
    unittest.main()
