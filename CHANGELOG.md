# Changelog

All notable changes to the reference CLI are documented here.

## Unreleased

- Require the manifest for public repository validation and projection.
- Refuse explicitly required unsupported capabilities before writes.
- Preserve remote header environment indirection in legacy Codex and Claude
  exports instead of writing a literal URN.

- Refuse unselected canonical skill content for Codex and Copilot before
  plan/apply/sync/export writes. Force does not bypass the refusal.
- Refuse Codex stdio environment references whose missing runtime source does
  not prevent native server activation. Preserve remote header mapping.
- Require all three supported adapter rows and durable evidence identifiers
  when checking release readiness.

## [1.0.0] - 2026-08-12

### Added

- `agents init`, `validate`, `capabilities`, `import`, `export`, and `convert`.
- Projection support for instructions, tools, and skills across the current
  reference targets while keeping native harness support claims conservative.
- Manifest-profile-aware exports that leave unselected native configuration
  untouched.
- Overwrite protection, optional backups, symlink rejection, and managed-file
  diff summaries.
- `agents version` for release artifact verification.

### Notes

- Reference CLI projection tests are not native harness support evidence.
- `.agents/AGENTS.md` is the canonical instruction file; root compatibility
  links and nested `AGENTS.md` files preserve native discovery and scoping.
- `plan` and `apply` replace whole-file export and direct vendor conversion.
  They merge owned MCP entries, preserve unrelated configuration, detect
  drift, and record hashes under `.agents/.state/reference-cli`.
- Codex and Claude projections preserve portable environment references using
  native indirection. Copilot projections fail when this cannot be done safely.
- OpenCode is no longer exposed by the stable CLI and remains a Workbench
  experiment.
- All compatibility claims remain governed by `compatibility.json`
  and the root `docs/COMPATIBILITY.md` file.
