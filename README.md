# Agents CLI

`agents` is the Go reference implementation of Open-Dot-Agents 1.0. It
validates repository configuration and projects the portable tools, hooks, and
skills profiles into native harness files without treating those files as a
second source of truth.

No adapter is conformance-supported yet. `capabilities` reports the same
conservative claims as the public compatibility registry.

## Portable repository

```text
.agents/
  AGENTS.md
  manifest.json
  tools/mcp.json
  hooks/hooks.json
  skills/<skill>/SKILL.md
AGENTS.md -> .agents/AGENTS.md
packages/api/AGENTS.md
```

Canonical instructions live in `.agents/AGENTS.md`; a root compatibility link
and nested `AGENTS.md` files provide native scoped discovery. Copilot CLI and
Codex also use `.agents/skills` directly. Hook catalogues project to
`.github/hooks/open-dot-agents.json`, `.codex/hooks.json`, or the `hooks`
field in `.claude/settings.json`. Claude Code receives an owned `CLAUDE.md`
import bridge and an owned `.claude/skills` projection.

## Build and test

```sh
go build ./cmd/agents
go test ./...
```

Source builds report `agents dev`. Release artifacts embed their version with
Go linker flags.

## Workflow

```sh
agents init --root .
agents validate --root . --format json
agents import --vendor codex --root .
agents capabilities --vendor codex
agents plan --vendor codex --root . --format json
agents apply --vendor codex --root .
agents plan --vendor codex --root . --check
agents sync --vendor all --root .
agents sync --vendor all --root . --check
```

`plan` is read-only. `apply` merges only managed MCP entries, preserves
unrelated JSON/TOML content, and records generated-entry hashes under
`.agents/.state/reference-cli/<vendor>.json`. A new unowned name collision or
a user-modified managed entry fails before any write.

`sync` uses the same projection rules for one vendor or all three stable
vendors. It plans every selected vendor before it writes a file. If one plan
fails, it writes no vendor output. If a write fails, it restores all managed
files to their state before the sync. `sync --check` is read-only and fails
when a managed projection is stale. Import remains an explicit operation for
one vendor because native formats can lose portable data and have no safe
multi-vendor merge order.

Use `--adopt` only for semantically equivalent existing content. Use
`--force --backup` for an intentional replacement. Writes reject symlink paths and use
same-directory temporary files plus atomic rename.

Remove the `tools` profile and run `apply` or `sync` to remove owned MCP
servers. Unowned servers and unrelated native settings remain. A modified
owned entry blocks removal unless you use `--force`; `--force --backup`
saves the previous file. Removal does not need the canonical MCP catalogue.
If the native file is absent, apply clears stale ownership without creating it.

Earlier CLI builds could clear ownership without removing the native servers.
For these repositories, review the canonical catalogue and native entries,
select `tools` again, and review `plan`. Use `--adopt` only when the existing
entries are equivalent to the reviewed catalogue. Apply to restore ownership,
then remove `tools` and apply again. Do not infer ownership from server names.

Codex environment URNs become `env_vars` or `env_http_headers`. Claude Code
uses `${VARIABLE}` expansion. Copilot CLI projections containing portable
environment references are refused because its current documented project
configuration exposes literal environment and header values.

## Commands

```text
agents init [--root <directory>] [--force]
agents validate [--root <directory>] [--format text|json]
agents capabilities --vendor <copilot|codex|claude>
agents import --vendor <copilot|codex|claude> [--root <directory>] [--force] [--backup]
agents plan --vendor <copilot|codex|claude> [--root <directory>] [--format text|json] [--check] [--adopt|--force]
agents apply --vendor <copilot|codex|claude> [--root <directory>] [--format text|json] [--adopt|--force] [--backup]
agents sync --vendor <all|copilot|codex|claude> [--root <directory>] [--format text|json] [--check] [--adopt|--force] [--backup]
agents version
```

OpenCode remains a Workbench experiment and is not part of the stable CLI
surface.

## Command hook workflow

See the [complete hook example](https://github.com/Open-Dot-Agents/Agents-Spec/tree/main/examples/hooks)
for validation, preview, application, and removal. Hook-only native repositories
can be imported without an MCP file.

The CLI refuses ignored matchers with `ODA-HOOK-0002`. Omitted or zero
`timeoutSec` uses the native default. Native matcher engines and event payloads
are not normalized.

`disableAllHooks: true` is projected for Copilot only. Codex and Claude fail
before writes with `ODA-HOOK-0001`; a failed apply leaves any earlier native
hooks active. To remove owned hooks, remove `hooks` from the manifest profiles
and apply. Claude keeps unrelated settings and tracks only the owned `hooks`
field. Changing unrelated settings does not cause an ownership conflict.
