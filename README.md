# Agents CLI

`agents` is the Go command-line implementation of Open-Dot-Agents. It imports,
exports, and converts repository-scoped agent configuration without coupling a
project to one harness.

The first release supports:

- MCP server configuration for GitHub Copilot CLI and OpenAI Codex.
- Shared repository instructions.
- Portable skills in `.agents/skills`.

Experimental projections are also available for Claude Code and OpenCode. They
are not conformance-supported adapters; validate their generated files with the
native harness before relying on them.

No adapter has version-pinned native-harness conformance evidence. Capability
statuses describe this explicitly: every adapter currently reports
`not-conformance-supported`, with profile-level details such as
`cli-projection-only`, `workbench-projection-only`, or `planned`.

The portable `.agents/` tree is the canonical representation:

```text
.agents/
  AGENTS.md
  manifest.json
  tools/mcp.json
  skills/<skill>/SKILL.md
```

Copilot uses `.github/mcp.json`; Codex uses `.codex/config.toml` under
`[mcp_servers]`. Both use root `AGENTS.md` for instructions and
`.agents/skills` for skills.

Experimental Claude projection uses root `.mcp.json`, root `AGENTS.md`, a
generated root `CLAUDE.md` bridge containing `@AGENTS.md`, and
`.claude/skills`. Experimental OpenCode projection uses root `opencode.json`
with an `mcp` object, root `AGENTS.md`, and `.agents/skills`.

## Build

```sh
go build ./cmd/agents
```

Or use the included Taskfile:

```sh
task build
task test
task verify
task install
```

`task build` writes `bin/agents`; `task install` uses Go's configured binary
directory.

## Commands

```sh
# Create an editable portable starter tree in the current directory.
agents init

# Check the structural validity of the local portable tree.
agents validate

# Print Codex implementation details and native paths.
agents capabilities --vendor codex

# Print the build version embedded by release artifacts.
agents version

# Inspect an experimental adapter before using it with its native harness.
agents capabilities --vendor claude

# Export the local .agents tree to Copilot.
agents export --vendor copilot

# Import local Codex configuration into .agents.
agents import --vendor codex

# Convert an existing Copilot configuration directly to Codex.
agents convert --from copilot --to codex --target ../codex-project

# Overwrite Codex configuration while showing a summary and retaining a backup.
agents export --vendor codex --force --backup --diff
```

All transfer commands refuse to overwrite MCP configuration, instructions, or
skills unless `--force` is supplied. The command preserves standard MCP
command, arguments, environment, URL, header, and timeout fields shared by
the two formats.

Import defaults to `--source . --target .agents`; export defaults to
`--source .agents --target .`. Use `--source` and `--target` to operate on
other repositories.

Use `--force --backup` to preserve whole destination directories before an
overwrite. Backups are timestamped sibling directories, such as
`.github.backup-20260812T143000.000000000Z`,
`.codex.backup-20260812T143000.000000000Z`, and
`.agents.backup-20260812T143000.000000000Z`; existing snapshots are retained.
Managed writes, copies, directory creation, and backups reject symlinks at the
target or in existing path components rather than following them.

Use `--diff` to print a Git-style summary of added, modified, and deleted
managed configuration files, including added and removed line counts.

`agents init` creates `.agents/manifest.json` with schema
`manifest-1.0`, version `1.0.0`, and the `instructions`, `mcp`, and `skills`
profiles; it also creates `.agents/AGENTS.md`, an empty `.agents/tools/mcp.json`, and
`.agents/skills/example/SKILL.md`. Edit or replace the stubs before exporting
the configuration.

`agents validate` checks a canonical tree's supported manifest version (when
present), instruction and skill files, safe skill names, and MCP shape. The
manifest accepts extra fields and an optional `$schema`. Profiles must be
non-empty, duplicate-free portable names without requiring every profile. When
a manifest is present, validation checks only the selected known profiles;
unknown profile names remain
forward-compatible. Without a manifest, it requires all three canonical paths.
With a manifest, the `instructions` profile permits an absent `AGENTS.md`;
when present, that path must be a regular file.
MCP uses a root containing `mcpServers` and may include an optional string
`$schema`; local servers need a command and remote servers need an HTTPS URL. In
versioned trees, MCP servers must use `stdio` or `remote`, have only the
schema-defined fields, and use
`urn:open-dot-agents:env:VARIABLE_NAME` environment and header references.
Legacy trees without a manifest retain permissive MCP parsing for backward
compatible import/export.

Exports honor the selected known manifest profiles and leave unselected vendor
configuration untouched. Without a manifest, exports retain the legacy
all-profile behavior.

`agents capabilities --vendor <copilot|codex|claude|opencode>` writes JSON
with the implemented profiles (`instructions`, `mcp`, and `skills`) and that
vendor's native repository paths. The status, profile-level status, evidence,
and limitations match `../compatibility.json`; this command does not create a
native-harness conformance claim.

`agents version` prints `agents <version>`. Source builds default to `dev`.
Release artifacts embed the release version through Go linker flags.

## Scope

Copilot CLI and Codex have reference CLI projections. Claude Code and OpenCode
remain planned or Workbench projection-only at the compatibility layer. No
adapter is native-harness conformance-verified. The CLI translates MCP server
definitions and copies shared instructions and skills; provider-specific
settings are outside the current scope.

OpenCode export rejects canonical MCP fields it cannot represent, such as
timeouts and approval modes. It preserves unrelated top-level
`opencode.json` fields while replacing its managed `mcp` object.
