# Changelog

All notable changes to the reference CLI are documented here.

## [1.0.0] - 2026-08-12

### Added

- `agents init`, `validate`, `capabilities`, `import`, `export`, and `convert`.
- Projection support for instructions, MCP, and skills across the current
  reference targets while keeping native harness support claims conservative.
- Manifest-profile-aware exports that leave unselected native configuration
  untouched.
- Overwrite protection, optional backups, symlink rejection, and managed-file
  diff summaries.
- `agents version` for release artifact verification.

### Notes

- Reference CLI projection tests are not native harness support evidence.
- All compatibility claims remain governed by the root `compatibility.json`
  and `COMPATIBILITY.md` files.
