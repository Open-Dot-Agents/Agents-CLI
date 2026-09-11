#!/usr/bin/env python3
"""Build an auditable inventory from saved official references and native help.

Input files are public documentation and help text, never user config or secrets.
The inventory classifies scope; it does not claim native implementation support.
"""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import re


def category(name, section, surface):
    text = (name + ' ' + section).lower()
    security = r'permission|sandbox|approv|allow|deny|denied|trust|credential|secret|auth|network|proxy|shell_environment|writable|filesystem|read.only|security|bypass'
    if re.search(security, text):
        if surface in ('command', 'option', 'shortcut', 'environment'):
            return 'runtime-operation', 'security'
        if re.search(r'credential|secret|auth|trust', text):
            return 'vendor-extension', 'security'
        return 'proposed-portable', 'security'
    areas = [
        ('instructions-skills-hooks-mcp', r'instruct|skill|hook|mcp'),
        ('models-agents', r'model|reason|agent|delegat|sidekick'),
        ('planning-context-sessions', r'plan|context|compact|session|resume|memory|history|fork'),
        ('plugins-lsp-automation', r'plugin|lsp|extension|automat|schedule|every|after'),
        ('remote-protocols', r'remote|server|protocol|acp|realtime'),
    ]
    area = next((area for area, pattern in areas if re.search(pattern, text)), 'runtime-ui-operations')
    if surface in ('command', 'option', 'shortcut', 'environment'):
        return 'runtime-operation', area
    # Only the existing core fields are portable now. Native discovery paths,
    # selector options, caches, and lifecycle operations retain native scope.
    existing = {'command', 'args', 'url', 'env', 'headers', 'type', 'name', 'description', 'timeoutSec'}
    if area == 'instructions-skills-hooks-mcp' and name.strip('`') in existing:
        return 'existing-portable', area
    return 'vendor-extension', area


def build(source_dir):
    sources = json.loads((source_dir / 'indexes.json').read_text()) + json.loads((source_dir / 'pages.json').read_text())
    for path in sorted(source_dir.glob('*-native-*.txt')):
        vendor = path.name.split('-')[0]
        sources.append(dict(name=path.stem, url='native-help:' + vendor,
                            sha256=hashlib.sha256(path.read_bytes()).hexdigest(), bytes=path.stat().st_size,
                            version={'codex': '0.154.0', 'copilot': '1.0.83'}[vendor]))
    entries = []
    def add(source, line, name, details, section, surface):
        classification, area = category(name, section, surface)
        entries.append(dict(source=source, line=line, name=name, details=details,
                            section=section, surface=surface, classification=classification, area=area))
    # ConfigTable data in the Codex config Markdown contains every documented key.
    text = (source_dir / 'codex-config.md').read_text()
    for match in re.finditer(r'key:\s*"([^"]+)"', text):
        end = text.find('key:', match.end())
        block = text[match.end():end if end != -1 else len(text)]
        description = re.search(r'description:\s*"((?:[^"\\]|\\.)*)"', block)
        add('codex-config', text.count('\n', 0, match.start()) + 1, match[1],
            description[1] if description else block.strip()[:1500], 'config.toml', 'setting')
    # The command Markdown omits imported table data. Use the rendered official
    # page's tables to account for those commands and flags as well.
    for index, row in enumerate(json.loads((source_dir / 'codex-command-tables.json').read_text()), 1):
        if len(row) < 2 or row[0] in ('Key', 'Command', 'Option', 'Shortcut'):
            continue
        surface = 'option' if row[0].startswith('-') else 'command'
        add('codex-commands-rendered', index, row[0], ' | '.join(row[1:]), 'rendered table row', surface)
    for name in ['copilot-commands', 'copilot-config', 'codex-permissions', 'copilot-sandbox']:
        section = ''
        for line_number, line in enumerate((source_dir / (name + '.md')).read_text().splitlines(), 1):
            if line.startswith('#'):
                section = line.lstrip('# ').strip()
            if not line.startswith('|'):
                continue
            # Most source cells protect identifiers with backticks. Keep the
            # complete cell so aliases and syntax variants remain visible.
            cells = re.split(r'(?<!\\)\|', line.strip('|'))
            if len(cells) < 2 or '`' not in cells[0]:
                continue
            key = cells[0].strip()
            lower = section.lower()
            surface = 'setting'
            if 'shortcut' in lower:
                surface = 'shortcut'
            elif 'environment' in lower:
                surface = 'environment'
            elif 'option' in lower:
                surface = 'option'
            elif 'command' in lower and 'configuration' not in lower:
                surface = 'command'
            add(name, line_number, key, ' | '.join(cell.strip() for cell in cells[1:]), section, surface)
    for path in sorted(source_dir.glob('*-native-*.txt')):
        if '-version' in path.stem:
            continue
        lines = path.read_text().splitlines()
        section = ''
        for index, line in enumerate(lines):
            if line and not line.startswith(' ') and line.endswith(':'):
                section = line.rstrip(':')
            key = None
            if '-config' in path.stem:
                match = re.match(r'\s+`([^`]+)`:', line)
                if match:
                    key = match[1]
            elif re.match(r'\s+(?:-[A-Za-z?], )?--[a-z]', line):
                key = line.strip().split('  ')[0]
            elif section == 'Commands' and re.match(r'  [a-z]', line):
                key = line.strip().split('  ')[0]
            if key:
                details = [line.strip()]
                for next_line in lines[index+1:index+15]:
                    if not next_line.strip() or re.match(r'  (?:--|`)', next_line):
                        break
                    details.append(next_line.strip())
                surface = 'setting' if '-config' in path.stem else ('command' if section == 'Commands' else 'option')
                add(path.stem, index+1, key, ' '.join(details), section, surface)
    if not entries:
        raise ValueError('No inventory entries were extracted')
    return dict(schema_version='1.0.0', candidate_version='1.1.0-draft.1',
                observed_on='2026-09-10', harnesses={'codex': '0.154.0', 'copilot': '1.0.83'},
                bounds='All structured reference rows and native help keys in the listed corpus. Prose-only behavior is summarized in FEATURE_INVENTORY.md. Docs can be newer than the pinned binaries.',
                record_defaults={
                    'scope': 'CLI first; consult the quoted source for user/project/managed scope; IDE and cloud are separate',
                    'precedence': 'Native precedence applies; no equality across vendors is assumed',
                    'lifecycle': 'Native lifecycle applies; startup, live reload, persisted changes, and runtime operations differ',
                    'status': 'inventory-only; classifications are design targets, not adapter support',
                    'limits': 'This inventory alone establishes no native security, IDE, or cloud support; consult implementation.json for current evidence',
                    'source_version': 'Live documentation unless the source record has a native version',
                }, sources=sources, entries=entries)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-dir', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = build(args.source_dir)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(dict(entries=len(result['entries']), sources=len(result['sources']),
                         classes=dict(collections.Counter(row['classification'] for row in result['entries'])))))


if __name__ == '__main__':
    main()
