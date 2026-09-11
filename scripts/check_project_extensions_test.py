#!/usr/bin/env python3
"""Test refusal of altered or unsafe pinned source packages."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from check_project_extensions import verify_source


class PinnedSources(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.package = Path(self.temporary.name) / 'package'
        self.package.mkdir()
        self.skill = self.package / 'SKILL.md'
        self.skill.write_text('# Review\n', encoding='utf-8')
        self.metadata = self.package / 'provenance.json'
        self.record = {'revision': 'a' * 40, 'files': {'SKILL.md': hashlib.sha256(self.skill.read_bytes()).hexdigest()}}
        self.save()

    def save(self):
        self.metadata.write_text(json.dumps(self.record), encoding='utf-8')

    def test_preserved_package(self):
        self.assertEqual(verify_source(self.package, self.metadata)['files'], 1)

    def test_modified_file(self):
        self.skill.write_text('Changed instructions.\n', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'differs'):
            verify_source(self.package, self.metadata)

    def test_added_executable(self):
        (self.package / 'hooks.json').write_text('{}', encoding='utf-8')
        with self.assertRaisesRegex(ValueError, 'absent'):
            verify_source(self.package, self.metadata)

    def test_escape_in_provenance(self):
        for name in ['../outside', '/etc/passwd', 'nested/../../outside', 'a\\b']:
            with self.subTest(name=name):
                self.record['files'] = {name: 'b' * 64}
                self.save()
                with self.assertRaisesRegex(ValueError, 'unsafe'):
                    verify_source(self.package, self.metadata)

    def test_symlink_even_to_identical_content(self):
        outside = self.package.parent / 'outside'
        outside.write_bytes(self.skill.read_bytes())
        self.skill.unlink()
        self.skill.symlink_to(outside)
        with self.assertRaisesRegex(ValueError, 'symlink'):
            verify_source(self.package, self.metadata)

    def test_unpinned_revision(self):
        self.record['revision'] = 'main'
        self.save()
        with self.assertRaisesRegex(ValueError, 'full Git revision'):
            verify_source(self.package, self.metadata)


if __name__ == '__main__':
    unittest.main()
