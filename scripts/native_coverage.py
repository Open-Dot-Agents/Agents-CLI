#!/usr/bin/env python3
"""Build semantic coverage from frozen evidence and compiled CLI declarations."""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import re
import subprocess
from native_coverage_semantics import annotate_records, setting_lookup_name

ROOT = Path(__file__).resolve().parents[2]
INVENTORY = ROOT / '.agents/features/codex-copilot.json'
OUTPUT = ROOT / '.agents/features/coverage.json'
SOURCE_SHA = 'f41f70b081e3920822c125f90a1c27b5bea920061f74510ed20dc49c419ed310'
MCP_SECTIONS = {'Local server configuration fields', 'Remote server configuration fields'}
ARTIFACTS = {'agents/', 'copilot-instructions.md', 'hooks/', 'instructions/',
             'lsp-config.json', 'mcp-config.json', 'settings.json', 'skills/'}
COPILOT_ARTIFACT_DECLARATIONS = {
    'agents/': ('agent', 'native_copilot_agent.go'),
    'hooks/': ('hooks', 'native_copilot_hooks.go'),
    'mcp-config.json': ('mcp', 'native_copilot_mcp.go'),
    'lsp-config.json': ('lsp', 'native_lsp.go'),
}


def normalized_path(path):
    return re.sub(r'<[^>]+>', '<name>', path).replace('."<name>"', '.<name>')


def setting_declaration(registry, name):
    """Resolve a concrete object key through the compiled schema declaration."""
    direct = registry.get(name)
    if isinstance(direct, dict):
        return direct
    parts = name.split('.')
    matches = []
    for pattern, declaration in registry.items():
        if not isinstance(declaration, dict) or '<name>' not in pattern:
            continue
        components = pattern.split('.')
        if len(components) != len(parts):
            continue
        if all(expected == actual or expected == '<name>' and actual and '[' not in actual and ']' not in actual
               for expected, actual in zip(components, parts)):
            matches.append((sum(component != '<name>' for component in components), pattern, declaration))
    if not matches:
        return None
    specificity = max(match[0] for match in matches)
    best = [match for match in matches if match[0] == specificity]
    if len(best) != 1:
        raise ValueError('ambiguous native setting declaration for ' + name)
    return best[0][2]


def classify(entry):
    """Context is part of identity; disposition and classification are not."""
    source, section, surface = entry['source'], entry['section'], entry['surface']
    vendor = source.split('-')[0]
    name = entry['name'].replace('`', '').strip()
    context, category, scope = 'settings', 'native-configuration', ['project', 'user']
    if source == 'codex-config' and entry['line'] >= 1761:
        # Boundary in the immutable source snapshot; not a line in current docs.
        context, category, scope = 'requirements', 'external-authority-state', ['managed']
    elif source == 'copilot-config':
        if section.startswith('User settings'):
            scope = ['user']
        elif section.startswith('Repository settings'):
            scope = ['project']
        elif section in ('Directory overview', 'What you can safely delete'):
            context, scope = 'artifact', ['user']
            category = 'native-configuration' if name in ARTIFACTS else 'external-authority-state'
        elif section in ('Schema', 'Approval kinds', 'Shell command matching'):
            context, category, scope = 'saved-permissions', 'external-authority-state', ['user']
        else:
            context, category, scope = 'managed-policy', 'external-authority-state', ['managed']
    elif source == 'copilot-native-config':
        scope = ['user']
        if name == 'trustedFolders':
            category = 'external-authority-state'
    elif source == 'copilot-commands' and surface == 'setting':
        if section in MCP_SECTIONS:
            context = 'mcp'
            if name in ('command', 'args', 'url', 'type'):
                category = 'portable-configuration'
        elif section == 'Custom agent frontmatter fields':
            context = 'agent-frontmatter'
        elif section == 'Skill frontmatter fields':
            context = 'skill-frontmatter'
        elif section == 'Sidekick configuration fields':
            context, scope = 'sidekick', ['user']
        elif section in ('Custom instructions locations', 'Skill locations'):
            context = 'discovery-location'
            if name in ('.github/skills/', '.claude/skills/', '.agents/skills/', 'Parent .github/skills/',
                        '.github/copilot-instructions.md', 'AGENTS.md', 'CLAUDE.md', 'GEMINI.md'):
                scope = ['project']
            if name == '.github/instructions/**/*.instructions.md':
                scope = ['project']
            if name in ('$HOME/.copilot/instructions/**/*.instructions.md', '$HOME/.copilot/copilot-instructions.md',
                        '~/.copilot/skills/', '~/.agents/skills/'):
                scope = ['user']
            if name in ('COPILOT_SKILLS_DIRS', 'COPILOT_CUSTOM_INSTRUCTIONS_DIRS', '--add-dir <path>'):
                category, scope = 'invocation-option', ['invocation']
        else:
            context, category, scope = 'runtime:' + section, 'runtime-operation', ['session']
    elif surface in ('command', 'option', 'environment', 'shortcut') or '-native-' in source:
        category = 'invocation-option' if surface in ('option', 'environment') or name.startswith('-') else 'runtime-operation'
        context, scope = 'invocation', ['invocation']
        if surface == 'environment':
            context = 'environment'
        elif surface == 'shortcut':
            context = 'shortcut:' + section
        elif name.startswith('/'):
            context, scope = 'slash-command', ['session']
        elif source.startswith('codex-native-') and source not in ('codex-native-help', 'codex-native-version'):
            context += ':' + source.removeprefix('codex-native-')
        elif section.startswith('`copilot '):
            context += ':' + section.split('`')[1].removeprefix('copilot ')
        flags = re.findall(r'--[a-z][a-z0-9-]*', name)
        if name.startswith('-') and flags:
            name = flags[0]
        name = re.sub(r'^(codex|copilot) ', '', name)
    if context == 'settings':
        if name.startswith('[') and name.endswith(']'):
            name = name.strip('[]')
        name = normalized_path(name)
        if vendor == 'codex':
            name = {'agents.max_threads': 'agents.max_concurrent_threads_per_session',
                    'memories.no_memories_if_mcp_or_web_search': 'memories.disable_on_external_context'}.get(name, name)
            if name in ('mcp_servers.<name>.command', 'mcp_servers.<name>.args',
                        'mcp_servers.<name>.url', 'mcp_servers.<name>.env_vars'):
                category = 'portable-configuration'
            if name.startswith('desktop.custom_file_handlers.'):
                scope = ['user']
            if name == 'windows_wsl_setup_acknowledged':
                category = 'external-authority-state'
    return vendor, context, name, category, scope


def compiled_registry():
    result = {}
    for vendor in ('codex', 'copilot'):
        run = subprocess.run(['go', 'run', './cmd/agents', 'capabilities', '--experimental', '--vendor', vendor],
                             cwd=ROOT / 'CLI', capture_output=True, text=True, check=True)
        native = json.loads(run.stdout)['native']
        result[vendor] = {}
        for item in native['setting_registry']:
            result[vendor][normalized_path(item['path'])] = item
            # Schema array items and documentation <index> paths identify the
            # same setting. Add a lookup alias without changing semantic IDs.
            if '[]' in item['path']:
                result[vendor][normalized_path(item['path'].replace('[]', '.<index>'))] = item
        result[vendor]['__artifacts__'] = [item for item in native['features'] if item['feature'].startswith('artifact:')]
        result[vendor]['__roots__'] = [item for item in native['features'] if not item['feature'].startswith('artifact:')]
        if vendor == 'codex':
            # The reference uses <id> for a finite exporter choice. Retain the
            # concrete schema paths; do not declare arbitrary exporter names.
            exporters = collections.defaultdict(list)
            for path, declaration in list(result[vendor].items()):
                if re.match(r'^otel\.(exporter|trace_exporter|metrics_exporter)\.otlp-(http|grpc)\.', path):
                    alias = re.sub(r'\.otlp-(http|grpc)\.', '.<name>.', path, count=1)
                    exporters[alias].append(declaration)
            for alias, declarations in exporters.items():
                declaration = dict(declarations[0])
                assert all(d['scopes'] == declaration['scopes'] and d['disposition'] == declaration['disposition'] for d in declarations)
                declaration['validation_paths'] = sorted(d['path'] for d in declarations)
                result[vendor][alias] = declaration
    return result


def build(inventory, registry):
    groups = {}
    sources = {source['name']: source for source in inventory['sources']}
    for index, entry in enumerate(inventory['entries']):
        vendor, context, name, category, scope = classify(entry)
        key = vendor, context, name
        item = groups.setdefault(key, {
            'id': vendor + '.' + hashlib.sha256((context + '\0' + name).encode()).hexdigest()[:20],
            'vendor': vendor, 'context': context, 'native': name,
            'category': category, 'scope': [], 'adapter': None,
            'version_constraint': '=' + ('0.154.0' if vendor == 'codex' else '1.0.83'),
            'disposition': 'mapping-pending', 'native_status': 'unverified',
            'limitations': [], 'evidence': [],
        })
        if item['category'] != category:
            raise ValueError('inconsistent semantic classification: ' + repr(key))
        item['scope'] = sorted(set(item['scope'] + scope))
        item['evidence'].append({'entry_id': 'source-entry-' + str(index + 1),
                                 'source': entry['source'], 'native_name': entry['name'],
                                 'url': sources[entry['source']]['url'], 'line': entry['line'],
                                 'section': entry['section']})
    for item in groups.values():
        category = item['category']
        if item['vendor'] == 'copilot' and item['context'] == 'artifact' and item['native'] in COPILOT_ARTIFACT_DECLARATIONS:
            kind, validator = COPILOT_ARTIFACT_DECLARATIONS[item['native']]
            fields = [f for f in registry['copilot'].get('__artifacts__', [])
                      if f['feature'] == 'artifact:'+kind and f['scope'] in item['scope']]
            if fields:
                item.update(adapter='CLI/internal/config/native.go',
                            validation_source='CLI/internal/config/'+validator,
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields if f['activation'] != 'inactive'}),
                            native_targets=sorted({f['destination'] for f in fields}),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if item['vendor'] == 'copilot' and item['context'] == 'discovery-location':
            instructions = [f for f in registry['copilot'].get('__artifacts__', []) if f['feature'] == 'artifact:instruction-discovery:/' + item['native']]
            if instructions:
                implementation = 'native.go' if item['native'] == 'AGENTS.md' else 'native_agent_instructions.go' if item['native'] in ('CLAUDE.md', '.claude/CLAUDE.md', 'GEMINI.md') else 'native_recursive_instructions.go'
                item.update(adapter='CLI/internal/config/'+implementation,
                            validation_source='CLI/internal/config/'+implementation,
                            disposition=instructions[0]['disposition'], native_status=instructions[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in instructions}),
                            native_evidence=sorted({p for f in instructions for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in instructions}))
                continue
            fields = [f for f in registry['copilot'].get('__artifacts__', []) if f['feature'] == 'artifact:skill-discovery:/' + item['native']]
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_skill_import.go',
                            validation_source='CLI/internal/config/native_copilot_skill_import.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields}),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if item['vendor'] == 'copilot' and item['context'] == 'skill-frontmatter':
            fields = [f for f in registry['copilot'].get('__artifacts__', []) if f['feature'] == 'artifact:skill:/' + item['native']]
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_skill_metadata.go',
                            validation_source='CLI/internal/config/native_copilot_skill_metadata.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields}),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['vendor'] == 'codex' and item['context'] == 'settings' and item['native'] == 'agents.<name>.config_file':
            fields = [f for f in registry['codex'].get('__artifacts__', []) if f['feature'] == 'artifact:role-reference:/agents/<name>/config_file']
            if fields:
                item.update(adapter='CLI/internal/config/native_codex_role_references.go',
                            validation_source='CLI/internal/config/native_codex_schema.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields}),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['context'] == 'settings' and item['vendor'] == 'codex' and item['native'].startswith('otel.'):
            lookup = registry['codex'].get(item['native'])
            if lookup and 'validation_paths' in lookup:
                item['validation_paths'] = lookup['validation_paths']
            path = '/'.join(item['native'].split('.'))
            limited = [f for f in registry['codex'].get('__artifacts__', [])
                       if f['feature'] == 'artifact:telemetry:/' + path.replace('<name>', 'otlp-http') and f['activation'] == 'inactive']
            if limited:
                item['native_variant_limitations'] = limited
            fields = [f for f in registry['codex'].get('__artifacts__', []) if f['feature'] == 'artifact:telemetry:/' + path]
            if fields:
                item.update(adapter='CLI/internal/config/native_codex_otel.go',
                            validation_source='CLI/internal/config/native_schemas/codex-0.154.0.json',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['context'] == 'settings' and item['vendor'] == 'codex':
            path = '/'.join(item['native'].split('.'))
            fields = [f for f in registry['codex'].get('__artifacts__', [])
                      if f['feature'] == 'artifact:skill-controls:/' + path]
            if fields:
                item.update(adapter='CLI/internal/config/native_codex_skills.go',
                            validation_source='CLI/internal/config/native_codex_skills.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['context'] == 'settings' and item['vendor'] == 'codex':
            prefix = 'model_providers.<name>.auth'
            if item['native'] in [prefix + suffix for suffix in ('', '.command', '.args', '.args[]', '.cwd', '.timeout_ms', '.refresh_interval_ms')]:
                fields = [f for f in registry['codex'].get('__artifacts__', [])
                          if f['feature'] == 'artifact:config:/model_providers/<name>/auth']
                if fields:
                    item.update(adapter='CLI/internal/config/native_auth_units.go',
                                validation_source='CLI/internal/config/native_auth_units.go',
                                disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                                implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                                native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                                limitations=sorted({f['limitation'] for f in fields}))
                    continue
        if category == 'native-configuration' and item['context'] == 'settings' and item['vendor'] == 'copilot':
            path = '/'.join(item['native'].split('.'))
            fields = [f for f in registry['copilot'].get('__artifacts__', [])
                      if f['feature'] == 'artifact:subagents:/' + path]
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_subagents.go',
                            validation_source='CLI/internal/config/native_copilot_subagents.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['context'] == 'settings' and item['vendor'] == 'copilot':
            path = '/'.join(item['native'].split('.'))
            fields = [f for f in registry['copilot'].get('__artifacts__', [])
                      if f['feature'] == 'artifact:preferences:/' + path]
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_preferences.go',
                            validation_source='CLI/internal/config/native_copilot_preferences.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['context'] == 'settings':
            path = '/'.join(item['native'].split('.'))
            fields = [f for f in registry[item['vendor']].get('__artifacts__', [])
                      if f['feature'] == 'artifact:plugins:/' + path]
            if fields:
                item.update(adapter='CLI/internal/config/native_plugins.go',
                            validation_source='CLI/internal/config/native_plugins.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category in ('native-configuration', 'portable-configuration') and item['context'] == 'mcp':
            fields = [f for f in registry[item['vendor']].get('__artifacts__', [])
                      if f['feature'] == 'artifact:mcp:/mcpServers/<name>/' + item['native']]
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_mcp.go',
                            validation_source='CLI/internal/config/native_copilot_mcp.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields}),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['vendor'] == 'codex' and item['context'] == 'settings' and item['native'].startswith('hooks'):
            name = item['native']
            feature_name = {'hooks': 'artifact:config:/hooks',
                            'hooks.<name>': 'artifact:hooks:/hooks/<event>',
                            'hooks.<name>[].hooks': 'artifact:hooks:/hooks/<event>[]/hooks'}.get(name)
            if name.startswith('hooks.<name>[].hooks[].'):
                feature_name = 'artifact:hooks:/hooks/<event>[]/hooks/<command>/' + name.rsplit('.', 1)[1]
            fields = [f for f in registry['codex'].get('__artifacts__', []) if f['feature'] == feature_name]
            if fields:
                item.update(adapter='CLI/internal/config/native_codex_hooks.go',
                            validation_source='CLI/internal/config/native_codex_hooks.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration' and item['vendor'] == 'copilot' and item['context'] == 'settings' and item['native'] == 'hooks':
            fields = [f for f in registry['copilot'].get('__artifacts__', []) if f['feature'] == 'artifact:config:/hooks']
            if fields:
                item.update(adapter='CLI/internal/config/native_copilot_hooks.go',
                            validation_source='CLI/internal/config/native_copilot_hooks.go',
                            disposition=fields[0]['disposition'], native_status=fields[0]['native_status'],
                            implemented_scopes=sorted({f['scope'] for f in fields} & set(item['scope'])),
                            native_evidence=sorted({p for f in fields for p in f.get('evidence', [])}),
                            limitations=sorted({f['limitation'] for f in fields}))
                continue
        if category == 'native-configuration':
            artifact_fields = [f for f in registry[item['vendor']].get('__artifacts__', [])
                               if item['context'] == 'agent-frontmatter' and f['feature'] == 'artifact:agent:/' + item['native']]
            if artifact_fields:
                item['adapter'] = 'CLI/internal/config/native_copilot_agent.go'
                item['validation_source'] = 'CLI/internal/config/native_copilot_agent.go'
                item['disposition'] = artifact_fields[0]['disposition']
                item['native_status'] = artifact_fields[0]['native_status']
                item['implemented_scopes'] = sorted({f['scope'] for f in artifact_fields if f['activation'] != 'inactive'})
                item['native_evidence'] = sorted({p for f in artifact_fields for p in f.get('evidence', [])})
                item['limitations'] = sorted({f['limitation'] for f in artifact_fields})
                continue
            lookup = setting_lookup_name(item)
            declaration = setting_declaration(registry[item['vendor']], lookup) if item['context'] == 'settings' else None
            if declaration:
                if declaration['path'] != lookup:
                    item.setdefault('validation_paths', declaration.get('validation_paths', [declaration['path']]))
                item['disposition'] = declaration['disposition']
                item['adapter'] = 'CLI/internal/config/native.go'
                item['validation_source'] = 'CLI/internal/config/' + declaration['validation_source']
                item['implemented_scopes'] = sorted(set(item['scope']) & set(declaration['scopes']))
                if not item['implemented_scopes'] and item['disposition'] == 'validator-declared':
                    item['disposition'] = 'mapping-pending'
                item['limitations'] = ['A declared validator is not a tested field mapping.',
                                       'Concrete values, scope, authority, and native behavior still require checks.']
            else:
                item['limitations'] = ['Artifact or setting mapping remains incomplete; this is implementation work, not a native limitation.']
                roots = [f for f in registry[item['vendor']].get('__roots__', [])
                         if item['context'] == 'settings' and f['feature'] == lookup.split('.')[0]
                         and f['scope'] in item['scope'] and f['disposition'] == 'blocked']
                if roots:
                    item.update(disposition='security-evidence-required',
                                adapter='CLI/internal/config/native_selection.go',
                                validation_source='CLI/internal/config/native.go', implemented_scopes=[],
                                gate_scopes=sorted({f['scope'] for f in roots}),
                                limitations=['The compiled adapter refuses the parent setting in these scopes.',
                                             'This is refusal coverage, not a field mapping or native enforcement claim.'])
        elif category == 'portable-configuration':
            item['adapter'], item['disposition'] = 'CLI/internal/config/config.go', 'portable-mapping'
            item['limitations'] = ['Existing portable mapping; native extras remain separate. Per-feature native behavior is not established by this map.']
        elif category == 'external-authority-state':
            item['disposition'] = 'external'
            item['limitations'] = ['Apply does not write authority, credentials, managed policy, or runtime state.']
        else:
            item['disposition'] = 'native-operation'
            item['limitations'] = ['Use the native interface. Apply does not invoke the operation or set invocation options.']
    for item in groups.values():
        if item['vendor'] != 'codex' or item['context'] != 'settings':
            continue
        path = '/'.join(item['native'].split('.'))
        for field in registry['codex'].get('__artifacts__', []):
            prefix = 'artifact:project-scope:/'
            if not field['feature'].startswith(prefix):
                continue
            setting = field['feature'].removeprefix(prefix)
            if path == setting or path.startswith(setting + '/'):
                item['scope_dispositions'] = {'project': 'native-ignored'}
                item['scope_evidence'] = {'project': field['evidence']}
                item['limitations'].append('Project scope: ' + field['limitation'])
                break
    annotate_records(groups)
    features = sorted(groups.values(), key=lambda item: item['id'])
    counted = [f for f in features if f['counted_feature']]
    milestone = [f for f in counted if f['milestone_scope'] == 'linux-cli']
    return {
        'schema_version': 3, 'standard_version': '1.1.0-draft.2',
        'identity_rule': 'vendor plus semantic context plus canonical name; independent of disposition and category',
        'source_inventory': '.agents/features/codex-copilot.json',
        'source_sha256': SOURCE_SHA, 'source_entries': len(inventory['entries']),
        'coverage_records': len(features), 'semantic_features': len(counted),
        'milestone_features': len(milestone), 'complete': False,
        'counts_are_completion': False,
        'native_artifacts': {vendor: registry[vendor].get('__artifacts__', []) for vendor in ('codex', 'copilot')},
        'counts': dict(sorted(collections.Counter(f['disposition'] for f in counted).items())),
        'record_counts': dict(sorted(collections.Counter(f['disposition'] for f in features).items())),
        'milestone_counts': dict(sorted(collections.Counter(f['disposition'] for f in milestone).items())),
        'features': features,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Check reproducibility without writes.')
    args = parser.parse_args()
    raw = INVENTORY.read_bytes()
    if hashlib.sha256(raw).hexdigest() != SOURCE_SHA:
        raise SystemExit('The frozen source inventory changed.')
    inventory = json.loads(raw)
    if len(inventory['entries']) != 1679:
        raise SystemExit('The frozen source row count changed.')
    result = build(inventory, compiled_registry())
    content = json.dumps(result, indent=2) + '\n'
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != content:
            raise SystemExit('Native coverage is stale. Run CLI/scripts/native_coverage.py.')
    else:
        OUTPUT.write_text(content)


if __name__ == '__main__':
    main()
