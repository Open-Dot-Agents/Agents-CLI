# Changelog

All notable changes to the reference CLI are documented here.

## Unreleased

- Add experimental global configuration with explicit user destinations and
  fixed core instruction bindings. Preserve complete user skill packages,
  private backups, and inherited skill ownership. Validate Codex keybindings
  and keep MCP output limits separate from approval controls.

- Create a missing root instruction compatibility link during stable apply or
  sync, record its ownership, and roll it back with failed writes. Create the
  initial Claude file bridge. Ignore only empty regular `.gitkeep` skill markers
  when checking an unselected skills profile.

- Accept a scoped `AGENTS.md` compatibility link to its own canonical
  `.agents/AGENTS.md`. Keep external, cross-scope, broken, cyclic, and indirect
  canonical targets refused before writes. Use the same check for Claude file
  bridges; this does not establish native harness support.

- Preserve source-relative Codex TLS file references during user import.
  Keep failing HTTP client-identity exporters inactive without removing their
  authentication. Record CA-only TLS and independent identity-failure tests.

- Restrict draft.2 Codex telemetry to user scope after native scope tests.
  Exclude credentials in collector URLs and headers from activation and import.
  Preserve external certificate and private-key references without copying files.

- Refuse native filesystem redirection, stale plans, late-created backups, and
  replaced lock identities. Use pinned directories for native writes and rollback.

- Add draft.2 scoped native configuration with private ownership and import.
  Fix exact JSON number preservation, ownership validation, alias conflicts,
  skill assets, and backup planning. Use the pinned Codex schema and preserve
  existing draft.2 policy during additive imports. Full native coverage remains
  incomplete.

- Record native isolation and model-tool approval tests. Preserve the Copilot
  local-network and Codex mandatory-ask refusals; do not extend coverage.

- Add explicit opt-in for the 1.1 security draft, strict policy validation,
  normalized refusal plans, and import/export protection against policy loss.
  Add the pinned Codex Linux direct-shell subset, explicit native-home checks,
  ownership, removal, and alias refusal. Other security policies remain refused.
  Stable behavior stays at 1.0.

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
