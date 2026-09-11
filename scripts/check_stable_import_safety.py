#!/usr/bin/env python3
"""Record public CLI import refusals, policy preservation, and private backups."""
import argparse
import hashlib
import json
from pathlib import Path
import stat
import subprocess
import tempfile
import traceback

ROOT = Path(__file__).resolve().parents[2]


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def snapshot(root):
    return {str(p.relative_to(root)): (stat.S_IMODE(p.stat().st_mode), sha(p) if p.is_file() else 'directory')
            for p in root.rglob('*')}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    assert not output.exists() and not output.with_suffix('.runner.py').exists(), 'refuse evidence replacement'
    root = Path(tempfile.mkdtemp(prefix='oda-stable-import-auth-', dir='/mnt/DATA/tmp'))
    binary = root / 'agents'
    result = {'passed': False, 'fixture': str(root), 'results': [], 'native_harness_execution': False,
              'runtime_support_promoted': False, 'runner_sha256': sha(__file__),
              'source_sha256': {str(p.relative_to(ROOT)): sha(p) for p in sorted((ROOT / 'CLI/internal/config').glob('*.go'))}}

    def write(path, text, mode=0o600):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        path.chmod(mode)

    def command(case, argv):
        run = subprocess.run([str(a) for a in argv], capture_output=True, text=True, timeout=60, cwd=ROOT / 'CLI')
        row = {'case': case, 'command': [str(a) for a in argv], 'exit_code': run.returncode, 'stdout': run.stdout, 'stderr': run.stderr}
        result['results'].append(row)
        return row

    try:
        build = command('build', ['go', 'build', '-trimpath', '-buildvcs=false', '-o', binary, './cmd/agents'])
        assert build['exit_code'] == 0, build
        result['binary_sha256'] = sha(binary)
        sources = {
            'literal': "[mcp_servers.demo]\ncommand='demo'\nenv.TOKEN='oda-test-secret'\n",
            'mixed': "[mcp_servers.demo]\ncommand='demo'\nenv_vars=['FROM_ENV']\nenv.TOKEN='oda-test-secret'\n",
            'disabled': "[mcp_servers.demo]\ncommand='demo'\nenabled=false\n",
            'bearer': "[mcp_servers.demo]\nurl='https://example.test/mcp'\nbearer_token_env_var='TOKEN'\n",
        }
        for name, source in sources.items():
            repo = root / name
            write(repo / 'AGENTS.md', '# Instructions\n')
            write(repo / '.codex/config.toml', source)
            before = snapshot(repo)
            row = command(name, [binary, 'import', '--vendor', 'codex', '--root', repo, '--force', '--backup'])
            row['unchanged'] = snapshot(repo) == before
            assert row['exit_code'] != 0 and row['unchanged'], name
            assert 'oda-test-secret' not in row['stdout'] + row['stderr'], 'credential in diagnostic'
        repo = root / 'policy'
        write(repo / 'AGENTS.md', '# Instructions\n')
        write(repo / '.codex/config.toml', "[mcp_servers.demo]\ncommand='demo'\n")
        write(repo / '.agents/AGENTS.md', '# Old\n', 0o640)
        write(repo / '.agents/manifest.json', json.dumps({'version': '1.0.0', 'profiles': [], 'requires': ['mcp.envRef']}), 0o664)
        row = command('policy', [binary, 'import', '--vendor', 'codex', '--root', repo, '--force', '--backup'])
        assert row['exit_code'] == 0, row
        row['manifest'] = json.loads((repo / '.agents/manifest.json').read_text())
        row['existing_mode_preserved'] = all(stat.S_IMODE((repo / path).stat().st_mode) == mode for path, mode in [('.agents/AGENTS.md', 0o640), ('.agents/manifest.json', 0o664)])
        backups = list(repo.rglob('*.backup-*'))
        row['backups_private'] = len(backups) >= 2 and all(stat.S_IMODE(p.stat().st_mode) == 0o600 for p in backups)
        assert row['manifest']['requires'] == ['mcp.envRef'] and row['existing_mode_preserved'] and row['backups_private']
        row = command('preserved-policy-refusal', [binary, 'plan', '--vendor', 'codex', '--root', repo, '--format', 'json'])
        row['plan'] = json.loads(row['stdout'])
        assert not row['plan']['applicable'] and row['plan']['actions'] == []
        assert any('mcp.envRef' in d for d in row['plan']['diagnostics'])
        result['passed'] = True
    except Exception:
        result['error'] = traceback.format_exc()
    finally:
        output.parent.mkdir(parents=True, exist_ok=True)
        output.with_suffix('.runner.py').write_bytes(Path(__file__).read_bytes())
        output.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps({'passed': result['passed'], 'output': str(output), 'error': result.get('error')}))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
