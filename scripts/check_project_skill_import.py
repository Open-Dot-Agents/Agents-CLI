#!/usr/bin/env python3
"""Check project skill import, marker backups, and overlap refusal through the CLI."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import traceback

ROOT = Path(__file__).resolve().parents[2]


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def snapshot(root):
    return {str(p.relative_to(root)): [p.stat().st_mode & 0o777, sha(p)]
            for p in root.rglob('*') if p.is_file() and p.name != '.agents-import.lock'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    assert not output.exists() and not output.with_suffix('.runner.py').exists()
    base = Path(tempfile.mkdtemp(prefix='oda-project-skill-cli-'))
    binary = base / 'agents'
    result = {'passed': False, 'fixture': str(base), 'commands': [], 'cases': [],
              'native_harness_execution': False, 'runner_sha256': sha(__file__),
              'source_sha256': {str(p.relative_to(ROOT)): sha(p) for directory in ('CLI/internal/config', 'CLI/cmd')
                               for p in sorted((ROOT / directory).rglob('*.go'))}}

    def command(argv, failed=False):
        run = subprocess.run([str(a) for a in argv], cwd=ROOT / 'CLI', capture_output=True, text=True, timeout=60,
                             env={**os.environ, 'XDG_STATE_HOME': str(base / 'state')})
        row = {'command': [str(a) for a in argv], 'exit_code': run.returncode, 'stdout': run.stdout, 'stderr': run.stderr}
        result['commands'].append(row)
        assert (run.returncode != 0) if failed else (run.returncode == 0), row
        return row

    def write(path, content):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    try:
        command(['go', 'build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/agents'])
        for origin in ('github', 'claude'):
            root = base / origin
            write(root / '.agents/manifest.json', '{"version":"1.1.0-draft.2","profiles":[]}')
            write(root / '.agents/AGENTS.md', 'Keep canonical instructions.\n')
            write(root / '.agents/skills/.gitkeep', '')
            native = root / ('.'+origin) / 'skills/fixture/SKILL.md'
            write(native, '---\nname: fixture\ndescription: Fixture.\n---\nUse fixture data.\n')
            original = sha(native)
            flags = ['--experimental', '--vendor', 'copilot', '--root', root, '--force', '--backup']
            command([binary, 'import', *flags])
            validation = json.loads(command([binary, 'validate', '--experimental', '--root', root, '--format', 'json'])['stdout'])
            assert validation['passed']
            canonical = root / '.agents/skills/fixture/SKILL.md'
            backup = root / '.agents/state/import-backups/skills.gitkeep.bak'
            assert backup.is_file() and backup.stat().st_size == 0 and backup.stat().st_mode & 0o777 == 0o600
            assert not (root / '.agents/skills/.gitkeep').exists() and not (root / '.agents/skills/.gitkeep.bak').exists()
            assert sha(native) == sha(canonical) == original
            canonical.write_text(canonical.read_text()+'Changed canonical body.\n')
            before = snapshot(root)
            refusal = command([binary, 'apply', *flags, '--format', 'json'], failed=True)
            plan = json.loads(refusal['stdout'])
            assert not plan['applicable'] and plan['actions'] == []
            assert any('project skill discovery conflict' in message for message in plan['diagnostics'])
            after = snapshot(root)
            assert after == before and sha(native) == original
            result['cases'].append({'origin': origin, 'validated_after_backup': True, 'backup_private': True,
                                    'source_unchanged': True, 'overlap_refused': True, 'refusal_unchanged': True,
                                    'before_refusal': before, 'after_refusal': after,
                                    'backup_mode': backup.stat().st_mode & 0o777, 'backup_bytes': backup.stat().st_size})
        result['passed'] = True
    except Exception:
        result['error'] = traceback.format_exc()
    output.parent.mkdir(parents=True, exist_ok=True)
    output.with_suffix('.runner.py').write_bytes(Path(__file__).read_bytes())
    output.write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps({'passed': result['passed'], 'error': result.get('error')}))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
