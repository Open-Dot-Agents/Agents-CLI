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
- Root and nested `AGENTS.md` are used directly instead of copying a second
  canonical instruction file under `.agents`.
- `plan` and `apply` replace whole-file export and direct vendor conversion.
  They merge owned MCP entries, preserve unrelated configuration, detect
  drift, and record hashes under `.agents/.state/reference-cli`.
- Codex and Claude projections preserve portable environment references using
  native indirection. Copilot projections fail when this cannot be done safely.
- OpenCode is no longer exposed by the stable CLI and remains a Workbench
  experiment.
- All compatibility claims remain governed by `compatibility.json`
  and the root `docs/COMPATIBILITY.md` file.
