# Agents CLI

`agents` is the Go command-line implementation of Open-Dot-Agents. It imports,
exports, and converts repository-scoped agent configuration without coupling a
project to one harness.

The first release supports:

- MCP server configuration for GitHub Copilot CLI and OpenAI Codex.
- Portable skills in `.agents/skills`.

The portable `.agents/` tree is the canonical representation:

```text
.agents/
  tools/mcp.json
  skills/<skill>/SKILL.md
```

Copilot uses `.github/mcp.json`; Codex uses `.codex/config.toml` under
`[mcp_servers]`. Both use `.agents/skills`.

## Build

```sh
go build ./cmd/agents
```

Or use the included Taskfile:

```sh
task build
task test
task install
```

`task build` writes `bin/agents`; `task install` uses Go's configured binary
directory.

## Commands

```sh
# Create an editable portable starter tree in the current directory.
agents init

# Export the local .agents tree to Copilot.
agents export --vendor copilot

# Import local Codex configuration into .agents.
agents import --vendor codex

# Convert an existing Copilot configuration directly to Codex.
agents convert --from copilot --to codex --target ../codex-project

# Overwrite Codex configuration while showing a summary and retaining a backup.
agents export --vendor codex --force --backup --diff
```

All commands refuse to overwrite MCP configuration or skills unless `--force`
is supplied. The command preserves standard MCP command, arguments,
environment, URL, header, and timeout fields shared by the two formats.

Import defaults to `--source . --target .agents`; export defaults to
`--source .agents --target .`. Use `--source` and `--target` to operate on
other repositories.

Use `--force --backup` to preserve whole destination directories before an
overwrite. Backups are timestamped sibling directories, such as
`.github.backup-20260812T143000.000000000Z`,
`.codex.backup-20260812T143000.000000000Z`, and
`.agents.backup-20260812T143000.000000000Z`; existing snapshots are retained.

Use `--diff` to print a Git-style summary of added, modified, and deleted
managed configuration files, including added and removed line counts.

`agents init` creates `.agents/AGENTS.md`, an empty
`.agents/tools/mcp.json`, and `.agents/skills/example/SKILL.md`. Edit or
replace the stubs before exporting the configuration.

## Scope

This first implementation supports Copilot CLI and Codex only. It translates
MCP server definitions and copies skills; shared agent instructions and
provider-specific settings are outside the current scope.

OpenCode and Claude Code are intentionally deferred to a later release.
