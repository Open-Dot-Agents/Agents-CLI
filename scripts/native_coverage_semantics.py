"""Source-backed syntax relationships and Linux CLI milestone boundaries."""


def annotate_records(groups):
    for item in groups.values():
        item['record_kind'] = 'feature'
        item['counted_feature'] = True
        item['milestone_scope'] = 'linux-cli'
        directories = {'instructions/': '$HOME/.copilot/instructions/**/*.instructions.md',
                       'skills/': '~/.copilot/skills/',
                       'copilot-instructions.md': '$HOME/.copilot/copilot-instructions.md'}
        if item['vendor'] == 'copilot' and item['context'] == 'artifact' and item['native'] in directories:
            target = groups[('copilot', 'discovery-location', directories[item['native']])]
            item.update(record_kind='alias', counted_feature=False,
                        describes=[target['id']], disposition='syntax-reference',
                        adapter=None, implemented_scopes=[], native_status='not-applicable',
                        limitations=['Directory overview for the linked user discovery location; not another configuration feature.'])
        if item['vendor'] == 'copilot' and item['context'] == 'discovery-location':
            aliases = {'COPILOT_SKILLS_DIRS': ('environment', 'COPILOT_SKILLS_DIRS'),
                       'COPILOT_CUSTOM_INSTRUCTIONS_DIRS': ('environment', 'COPILOT_CUSTOM_INSTRUCTIONS_DIRS'),
                       '--add-dir <path>': ('invocation', '--add-dir')}
            if item['native'] in aliases:
                context, name = aliases[item['native']]
                target = groups[('copilot', context, name)]
                item.update(record_kind='alias', counted_feature=False,
                            describes=[target['id']], disposition='syntax-reference',
                            adapter=None, implemented_scopes=[], native_status='not-applicable',
                            limitations=['Discovery use of the linked native invocation option, not a separate configuration setting. Apply does not set this option or grant directory trust.'])
        if item['vendor'] != 'codex':
            continue
        name = item['native']
        if name.startswith(('windows.', 'computer_use.windows.')) or name in ('windows', 'computer_use.windows', 'windows_wsl_setup_acknowledged'):
            item['milestone_scope'] = 'outside-linux-cli'
            item['milestone_reason'] = 'Windows-specific surface; this milestone covers Linux CLIs.'
        if name.startswith('desktop.custom_file_handlers.'):
            item['milestone_scope'] = 'outside-linux-cli'
            item['milestone_reason'] = 'The reference defines Open in targets for the desktop app, outside this CLI milestone.'
        if item['context'] != 'settings':
            continue
        if item['milestone_scope'] == 'outside-linux-cli' and item['category'] == 'native-configuration':
            item['disposition'] = 'outside-milestone'
            item['limitations'] = [item['milestone_reason'], 'No CLI mapping or native behavior is claimed.']
            item['native_status'] = 'unverified'
            item['adapter'] = None
            item['implemented_scopes'] = []

        kind, parent = None, None
        if any(e['source'] == 'codex-permissions' and e['section'] == 'Filesystem permissions' for e in item['evidence']):
            if name in ('read', 'write', 'deny'):
                kind = 'configuration-value'
            elif name in (':root', ':minimal', ':workspace_roots', ':tmpdir', ':slash_tmp', '/absolute/path', '~/path'):
                kind = 'configuration-selector'
            if kind:
                parent = 'permissions.<name>.filesystem.<name>'
        if name == 'tui.keymap.<name>.<name> = []':
            kind, parent = 'configuration-value', 'tui.keymap.<name>.<name>'
        if name.startswith('[permissions.') and '].' in name:
            closing = name.index(']')
            item['setting_path'] = name[1:closing] + name[closing + 1:]
            target = groups.get(('codex', 'settings', item['setting_path']))
            if target:
                kind, parent = 'alias', item['setting_path']
        if kind:
            target = groups[('codex', 'settings', parent)]
            item.update(record_kind=kind, counted_feature=False,
                        describes=[target['id']], disposition='syntax-reference',
                        adapter=None, implemented_scopes=[], native_status='not-applicable',
                        limitations=['Syntax or a value of the linked setting; not a separate setting, operation, or completed mapping.'])
            for field in ('validation_source', 'gate_scopes', 'native_evidence'):
                item.pop(field, None)


def setting_lookup_name(item):
    name = item['native']
    if item['vendor'] == 'codex' and item['context'] == 'settings' and name.startswith('[permissions.') and '].' in name:
        closing = name.index(']')
        return name[1:closing] + name[closing + 1:]
    return name
