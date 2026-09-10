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

Codex remote header URNs become `env_http_headers`. Codex stdio environment
references are refused because missing runtime variables do not prevent server
activation. Claude Code
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

## Portability refusals

`plan`, `apply`, and `sync` refuse an unselected skills profile for Codex and
Copilot when the target `.agents/skills` directory contains content.
`export` applies the same check to its output repository. The check leaves
canonical content, owned native files, unrelated settings, and backups
unchanged. `--force` does not bypass it. Empty or absent skills directories
remain valid.

`ODA-ADAPTER-0003` identifies this refusal. It does not disable native skill
discovery: direct use of the harness can still read the canonical skills.
Select the profile only when you intend to expose those skills. No automatic
move or deletion of canonical content is performed.

`ODA-ADAPTER-0004` refuses Codex stdio environment references even when the
variable exists during apply. Apply cannot establish that the variable will
exist at runtime. Remote header references keep their existing mapping.
Existing native configuration from an earlier apply remains in place after
refusal. Remove the tools profile and apply to remove owned MCP entries;
review the resulting plan first. Do not replace a reference with a secret value.

These refusals do not make an adapter conformance-supported.

The public `validate`, `plan`, `apply`, and `sync` commands require the
canonical manifest. Legacy internal import/export helpers retain their
unversioned input handling. `ODA-ADAPTER-0005` refuses an explicitly required
capability that the adapter declares unsupported, even if no selected server
currently uses it. Keep `requires` aligned with the intended configuration.
Another refusal can block profile cleanup; never assume that a failed apply
removed existing native configuration.

## Experimental security draft

The `1.1.0-draft.1` contract adds permissions and sandbox profiles behind
`--experimental`. The Codex 0.154.0 Linux amd64 direct-shell subset can project
native security settings through plan/apply/sync with `--codex-home`. The native
home must already trust the workspace. Use the exact invocation and replacement
environment from a fresh plan. No launcher or trust grant is installed.

Other security requests and native security import remain refused before
writes. Force does not override these refusals. The legacy export API also
refuses selected security policy. See [usage and limits](../docs/SECURITY_PROFILES.md)
and the [draft specification](../SPEC/spec/1.1-draft/SPECIFICATION.md).

## Experimental native configuration

Copilot draft.2 user settings include initial mode, tabs, status-line commands,
inline-image preferences, and notifications. Value validation includes history
and refresh bounds and tab identifiers. The pinned terminal tests cover mode,
tabs, and status-line execution; other preference behavior remains unverified.
See [native settings and limits](../docs/NATIVE_CONFIGURATION.md).

Named Copilot subagents can also select a native model, effort, and context tier,
or inherit the parent choice. Pinned tests cover model/effort dispatch and custom
agent disablement. Depth and concurrency overrides require native usage-based
billing; plan output reports this prerequisite. Apply does not change account
state or enforce those limits.

Both Codex and Copilot already load project skills from `.agents/skills`.
Codex native user selectors can enable or disable skills. Import preserves those
selectors when it copies user skills to a new native home. Pinned Codex ignores
project selectors, so required draft.2 project selectors refuse before writes.
This restriction does not affect ordinary project skill discovery.

Draft.2 also accepts the `plugins` profile. Store existing Codex or Copilot
selection configuration in `.agents/plugins/<namespace>/`, with `profile.json`
and a native `config` artifact. Import reads the native selection; it does not
copy or rewrite the installed package. Plan and apply use the same scope,
ownership, and transaction checks as the native profile. Installation, updates,
and trust remain native operations. A later native startup can fetch enabled
packages. See the [plugin guide](../docs/PLUGIN_STANDARD.md) and
[example](../SPEC/examples/plugins-draft/.agents/manifest.json).

The `1.1.0-draft.2` native profile adds `--scope project|user` to import, plan,
apply, and sync. Project is the default. User scope requires an absolute
`--native-home`. Use `--experimental` and an explicit Codex or Copilot vendor.
The existing `--codex-home` option retains draft.1 security semantics.

Native ownership is per setting or asset. A different source repository cannot
replace or remove a setting with `--force`. `--adopt` accepts equal unowned
values. New user files and backups are private. Import can add disjoint content
to an existing draft.2 root; conflicts refuse, even with force. It preserves
portable policy and required native status. It does not migrate stable or
draft.1 roots, or import trust, account, or credential stores.

Codex value validation uses its pinned 0.154.0 schema and verified aliases.
Capabilities expose validator declarations separately from native behavior.
User skill import preserves executable assets and excludes system packages.
Plan includes private backup operations before apply starts.
Native Linux transactions use pinned directory handles and snapshot preconditions.
They refuse stale plans and backup-creation races, and keep rollback from following
a replacement symlink. A concurrent edit during rollback is reported and retained.
Unknown optional object fields have separate diagnostics and do not suppress
known siblings. Import reads recognized native agents, hooks, and scoped
instructions. The Codex standalone-agent mapping has bounded native execution
evidence; Copilot agents and unmapped hook fields remain inactive.

This implementation is incomplete. See the parent repository's
`docs/NATIVE_CONFIGURATION.md` and `.agents/features/coverage.json` for exact
boundaries. A configuration mapping does not establish native support.

### Draft.2 Codex telemetry scope

Codex `otel` settings require `--scope user --native-home /absolute/path`.
Pinned Codex `0.154.0` ignores project telemetry. Required project settings
refuse before writes; optional settings remain inactive. Collector credentials
remain external. Certificate and private-key paths are references; apply does
not copy these files. Import preserves relative reference targets. CA-only
HTTP TLS has bounded native evidence; HTTP client identities fail in the pin
and keep the complete exporter inactive. These tests do not establish gRPC,
metrics delivery, or live reload support. See the
[native configuration guide](../docs/NATIVE_CONFIGURATION.md).

### Scoped instruction compatibility links

The reference CLI accepts a nested `AGENTS.md` link to that directory's own
`.agents/AGENTS.md`. The canonical directory and target file must be real
entries. Links to external files, ancestor instructions, broken targets,
cycles, or indirect canonical sources are refused before projection writes.
Ordinary scoped instruction files retain their scope. The Claude file adapter
uses the same checks when it creates a scoped `CLAUDE.md` bridge. These CLI
checks do not establish native harness behavior.

When a stable project has no root `AGENTS.md`, plan reports a `create-link`
action and apply creates `AGENTS.md -> .agents/AGENTS.md`. All-vendor sync creates
one shared link and records ownership for each selected adapter. A failed file
transaction removes the newly created link. Rollback preserves a link path
that another writer has replaced. Existing user instruction files remain
unchanged, including with `--force`. The Claude file adapter also creates its
initial `CLAUDE.md` bridge. An empty regular `.agents/skills/.gitkeep` is a
placeholder; other unselected skill content still refuses Codex/Copilot apply.
