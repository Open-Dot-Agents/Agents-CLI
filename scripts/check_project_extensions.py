#!/usr/bin/env python3
"""Check pinned project skills and the GitHub plugin without installing them.

This is a source-integrity check. Native behavior and full adapter support have
separate evidence and release gates.
"""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re


ROOT = Path(__file__).resolve().parents[2]


def require(condition, message):
    if not condition:
        raise ValueError(message)


def no_links(path):
    for component in (path, *path.parents):
        require(not component.is_symlink(), 'source path contains a symlink')


def verify_source(package, metadata):
    no_links(package)
    no_links(metadata)
    provenance = json.loads(metadata.read_text(encoding='utf-8'))
    revision = provenance.get('revision', provenance.get('commit'))
    require(isinstance(revision, str) and re.fullmatch(r'[0-9a-f]{40}', revision), 'source requires a full Git revision')
    files = provenance.get('files', provenance.get('sha256'))
    require(isinstance(files, dict) and files, 'source requires file hashes')
    for name, digest in files.items():
        relative = PurePosixPath(name)
        require(name and not relative.is_absolute() and '..' not in relative.parts and '\\' not in name
                and str(relative) == name, 'unsafe provenance file path')
        require(isinstance(digest, str) and re.fullmatch(r'[0-9a-f]{64}', digest), 'invalid source digest')
        path = package / name
        no_links(path)
        require(path.is_file(), 'source file is missing')
        require(hashlib.sha256(path.read_bytes()).hexdigest() == digest, 'source file differs from its pin: '+name)
    observed = set()
    for path in package.rglob('*'):
        no_links(path)
        if path.is_dir():
            continue
        require(path.is_file(), 'source contains a non-regular file')
        if path != metadata:
            observed.add(path.relative_to(package).as_posix())
    require(observed == set(files), 'package contains files absent from its provenance')
    return {'revision': revision, 'files': len(files)}


def check(root):
    sources = {}
    for name in ['property-based-testing', 'diagnosing-bugs']:
        package = root / '.agents/skills' / name
        sources[name] = verify_source(package, package / 'provenance.json')
        (package / 'SKILL.md').read_bytes().decode('utf-8')
        require((package / 'LICENSE').is_file(), 'shared skill license is missing')
    native = root / '.agents/plugins/com.openai.codex'
    package = native / 'plugins/github'
    sources['github'] = verify_source(package, native / 'provenance.json')
    provenance = json.loads((native / 'provenance.json').read_text())
    require(provenance.get('transformations') == [{
        'path': '.mcp.json',
        'reason': 'Add the Copilot-compatible Authorization environment reference while retaining the Codex bearer field.',
        'upstream_sha256': '730ebd45944d5f46aeded73c8fa8a2e5765726c626a8605671809d6159d31edd',
    }], 'unreviewed GitHub compatibility transformation')
    plugin = json.loads((package / '.codex-plugin/plugin.json').read_text())
    require(plugin['name'] == 'github' and plugin['version'] == '0.1.11', 'unexpected GitHub package identity')
    for key in ('composerIcon', 'logo', 'logoDark'):
        path = package / plugin['interface'][key]
        no_links(path)
        require(path.is_file() and path.resolve().is_relative_to(package.resolve()), 'invalid plugin asset')
    catalog = json.loads((native / '.agents/plugins/marketplace.json').read_text())
    require(catalog['name'] == 'open-dot-agents', 'unexpected project marketplace')
    require(len(catalog['plugins']) == 1 and catalog['plugins'][0]['name'] == 'github', 'unexpected project plugin selection')
    require(catalog['plugins'][0]['source'] == {'source': 'local', 'path': './plugins/github'}, 'marketplace points outside the pinned package')
    require(catalog['plugins'][0]['policy'] == {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'unexpected marketplace policy')
    mcp = json.loads((package / '.mcp.json').read_text())
    require(mcp == {'mcpServers': {'github': {'type': 'http', 'url': 'https://api.githubcopilot.com/mcp/',
                                           'bearer_token_env_var': 'GITHUB_PAT_TOKEN',
                                           'headers': {'Authorization': 'Bearer ${GITHUB_PAT_TOKEN}'}}}},
            'unreviewed GitHub MCP configuration')
    return {'passed': True, 'check': 'pinned-source-integrity', 'sources': sources,
            'native_execution_checked': False, 'adapter_support_promoted': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    args = parser.parse_args()
    try:
        result = check(args.root.absolute())
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(json.dumps({'passed': False, 'error': str(error)}))
        return 1
    print(json.dumps(result, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
