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

The inventory and semantic coverage tools live in `scripts/` in this CLI
repository. They use the parent Open-Dot-Agents checkout for canonical data
and Workbench evidence. From the parent checkout, run:

```sh
python3 CLI/scripts/native_coverage.py --check
python3 CLI/scripts/native_coverage_test.py
```

Use `CLI/scripts/build-feature-inventory.py --help` for inventory generation.
Keep generated configuration and coverage data under `.agents/`; keep
maintenance programs in this submodule.

## Workflow

Draft.2 also supports a global source at `~/.agents`. Use
`agents init --global --experimental` for an empty location, then
`agents validate --global --experimental`. Global initialization refuses
existing content, including another tool's configuration format.

Use `agents plan --global --experimental --vendor codex --native-home "$HOME/.codex"`
to inspect user projection. `apply`, `sync`, and `import` accept the same
source selector. Copilot uses an explicit native home such as `$HOME/.copilot`.
`--global` implies user scope and cannot be combined with `--root`.
Project commands keep their existing source and destination rules. Native
precedence combines applied user defaults with project values; the CLI does
not automatically merge two canonical trees. Security and ownership checks
remain enforced. Global instructions use the fixed user core binding, with
reference-bearing content subject to explicit native mapping.

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

Draft.2 project apply and import accept the verified root canonical link for
Codex and Copilot. Apply preserves that link. When an old Codex instruction
copy has been replaced with the link, apply releases its old file ownership.
For Copilot, apply removes an unchanged owned `.github/copilot-instructions.md`
copy. Modified or foreign files remain protected, including with `--force`.
Import reads the canonical file directly. Copilot records a fixed
`canonical-instructions` binding with `source: "AGENTS.md"` and no `name`.
This source refers to `.agents/AGENTS.md`, and its destination is root
`AGENTS.md`. After relocation without the link, apply creates a managed root
file. A separate native `instructions` artifact preserves a distinct
`.github/copilot-instructions.md` body. Referenced files remain external;
removal refuses a known change to the reference base. See the
[canonical instruction report](../docs/COPILOT_CANONICAL_INSTRUCTIONS.md).
This does not migrate stable manifests to draft.2.

Pinned Linux checks cover both migration paths, repeated apply/import, and
updated instructions in fresh native model contexts. They do not establish
live reload or all native instruction precedence cases. See the
[instruction-link debug record](../docs/NATIVE_DEBUG_RESEARCH.md#draft2-instruction-links-and-re-import).

### User instruction sources

Draft.2 user instruction import preserves an existing artifact source such as
`policy/local.md` for the fixed native instruction file. Reimport refuses
duplicate targets and conflicting policy, including with force. Copilot plans
report external user references and session restart requirements. Use explicit
`--scope user --native-home /absolute/path`; project apply leaves that user
file unchanged. See the [user instruction report](../docs/COPILOT_USER_INSTRUCTIONS.md).

### Skill encoding and project source checks

Draft.2 Copilot plans report each selected skill, model and user invocation
controls, and known metadata losses. Malformed discovery metadata refuses
projection before writes. The `warnings` array is also printed by text plan,
apply, and sync. Valid definitions retain their bytes through user import,
apply, and reimport. Stable Markdown rules remain unchanged. See the
[skill metadata report](../docs/COPILOT_SKILL_METADATA.md) for native limits.

Draft.2 project import reads complete Copilot packages from `.github/skills/`
and `.claude/skills/` under the supplied root. It stores them in `.agents/skills/`,
selects `skills`, and preserves original packages. Existing `.agents/skills/`
packages are selected in place. A bare shared skill tree can establish draft.2
metadata without replacing its files or modes. Only recognized skill packages
and an optional regular `.agents/AGENTS.md` are accepted without a manifest.
Malformed manifests and other unversioned canonical content refuse. Import, plan, and apply
refuse different packages with overlapping native identities, even with
`--force`. Import removes an empty canonical skill marker transactionally;
`--backup` stores it under `.agents/state/import-backups/`, outside the skill
discovery tree. See the [project skill import report](../docs/COPILOT_SKILL_IMPORT.md).

Draft.2 Copilot import also reads nested `*.instructions.md` files from the
registered project or user instruction directory. Relative paths and file
contents survive relocation. Unsafe names and symlinks refuse before writes;
nested assets use the existing file ownership and rollback rules. Native
evidence covers direct unconditional loading and explicit model reads from the
native instruction catalog. Plan reports that user instructions outside trusted
directories can need native read permission. Apply does not grant trust. See
the [instruction report](../docs/COPILOT_RECURSIVE_INSTRUCTIONS.md) for model-guided
selection and approval limits.

Draft.2 project import reads a regular root `AGENTS.md`. Native
`agent-instructions` artifacts preserve root reference syntax and distinct
agent instruction bodies at fixed project paths. Other registered files are
`CLAUDE.md`, `.claude/CLAUDE.md`, and `GEMINI.md`. Referenced project files remain
external. In the tested Copilot version, `.claude/CLAUDE.md` loads from a
`.claude` working directory but not from repository-root sessions. See the
[native agent instruction report](../docs/COPILOT_AGENT_INSTRUCTIONS.md).

Selected `SKILL.md` definitions must be UTF-8 Markdown. Projection rejects
invalid encoding in stable, draft.1, and draft.2 trees. Stable repository import
and native user-scope skill import check definitions before content writes and
backups. Supporting skill assets can contain binary data and are preserved.

Project maintenance checks for pinned external skills and the requested GitHub
package are under `scripts/check_project_extensions.py`. Run its companion
`scripts/check_project_extensions_test.py` to check changed files, extra files,
symlinks, escaping paths, and unpinned revisions. These checks do not install
packages, convert native plugin formats, or establish native runtime support.
See the [project readiness report](../docs/PROJECT_EXTENSIONS_READINESS.md).

### Stable import safety

Stable repository import checks MCP fields and the prospective canonical tree
before it writes content or backups. Codex literal `env` and `http_headers`
values are refused, including strings that resemble portable reference URIs.
Mixed literal and reference inputs cannot silently discard a value. Unknown
server fields, including activation, authentication, and tool-filter controls,
are refused when no stable mapping exists. Diagnostics do not include values.

`--force` preserves existing manifest requirements, metadata, and selected
profiles. It does not deactivate a retained profile that is absent from the
native source. Retained selected content must still validate. Native import has
separate draft.2 support and migration rules; stable import does not implicitly
enable that profile or migrate an existing manifest.

An import failure rolls back imported files, new directories, and backups.
Existing file modes are preserved; new backups use `0600`. New skill assets
retain their source mode. Changes observed after validation or before a target
write cause refusal. This does not claim the stronger native user-scope lock
and authority guarantees for stable project import.

See the [stable import evidence](../docs/NATIVE_DEBUG_RESEARCH.md#stable-import-credentials-policy-and-rollback).

### Native telemetry authentication

Native MCP, model-provider, and LSP definitions refuse import or projection
when credential exclusion would remove authentication. This includes optional
profiles and forced writes. Supported environment references remain intact.
Codex `requires_openai_auth` remains a Boolean setting; account files stay
external. See the [authentication tests and limits](../docs/NATIVE_AUTHENTICATION.md).

Codex native telemetry exporters with excluded credentials use the explicit
value `none`. Import reports identify each disabled exporter. Optional apply
uses the same value for unsupported HTTP client identities; required content
still blocks apply. The adapter does not remove authentication and leave a
weaker exporter active, or omit a setting that restores a native default.

The pinned user-scope native evidence now covers gRPC and HTTP binary logs,
traces, and metrics, including CA trust and gRPC client certificates. Metrics
require analytics to be enabled. See the
[transport and authentication report](../docs/CODEX_OTEL_TRANSPORTS.md) for
source pins, native events, regression evidence, and limits.

### Native provider token commands

Draft.2 preserves Codex `model_providers.<id>.auth` as configuration. The whole
token-command table is validated before import or projection. Invalid fields
and conflicting authentication modes refuse before writes. Apply does not run
the helper or copy its returned token. Codex `0.154.0` can send unauthenticated
requests after helper failure; this native setting is not authentication
enforcement. See the [native evidence and limits](../docs/CODEX_COMMAND_AUTHENTICATION.md).

### Codex project scope

Draft.2 refuses required project fields that Codex `0.154.0` ignores. Optional
source content stays preserved and inactive. This includes provider selection
and definitions, endpoint overrides, notifications, profiles, telemetry,
host metadata, and the system-proxy flag. User files remain unchanged. See the
[scope table, tests, and limits](../docs/CODEX_PROJECT_SCOPE.md).

### Codex child-role files

Draft.2 validates agent files against the bounded native role contract.
Ignored provider, MCP, context, or other session settings refuse required
activation before writes. Optional files stay intact and inactive as a whole.
Model and instruction overrides and selected feature or skill reductions use
the same role layer in project and user scope. See the
[role mapping and native evidence](../docs/CODEX_ROLE_OVERRIDES.md).

Import resolves relative role-file paths and role skill selectors against their
native source directory. References to mapped assets remain relative; external
libraries retain absolute source references and are not copied. See the
[relocation tests and limits](../docs/CODEX_ROLE_REFERENCES.md).
