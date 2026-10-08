# Harness plugins

Status: proposal for GEN-27, researched 2026-10-08. No plugin has been installed,
published or tested with a live agent. This changes packaging and setup; the server
and delivery protocol stay the same.

## Recommendation

Ship one integration package per supported harness, generated from the skill and
profiles we already maintain. Start with optional plugins and instructions in
`aboard init`; keep direct setup as the default until migration and upgrade proofs
pass. A plugin still needs the aboard binary installed separately.

Packaging alone is small. Automatically replacing someone's working hooks is the
larger part: direct and plugin hooks must never run together, edited files must stay
safe, and a marketplace update must not put the plugin ahead of its binary.

## What the harnesses support

### Claude Code

A plugin has `.claude-plugin/plugin.json`, root `skills/`, and `hooks/hooks.json`.
It can also carry commands, agents and MCP configuration. Skill commands are
namespaced, such as `/aboard:aboard`. We need the skill and hooks, without new agents
or MCP services. [Plugin format](https://code.claude.com/docs/en/plugins).

A repository catalog lives at `.claude-plugin/marketplace.json`; an entry can point
to a plugin subdirectory. Git sources can be pinned to a revision. This lets the
existing repository carry the catalog and packages.
[Marketplace format](https://code.claude.com/docs/en/plugin-marketplaces).

Users add the marketplace, then install a named entry. The interactive install opens
a review panel; the shell command supports user, project and local scopes.
Third-party marketplaces do not auto-update by default. Refreshing a marketplace
does not itself update every installed plugin.
[Discovery and updates](https://code.claude.com/docs/en/discover-plugins).

The shell supports `claude plugin install`, `update` and `validate`. Updated hooks
load on the next session or through the documented reload flow.
[Plugin CLI](https://code.claude.com/docs/en/plugins/cli-reference).

The manifest's dependencies are other plugins. I found no documented field that
installs an arbitrary system binary, so aboard must remain a separate prerequisite.
[Dependency rules](https://code.claude.com/docs/en/plugins/dependencies).

### Codex

The portable layout uses root `plugin.json`, `skills/`, and optional hooks and MCP
configuration. OpenAI settings can live under `extensions.com.openai`; a compatibility
manifest also exists. Repository catalogs use `.agents/plugins/marketplace.json`.
The CLI can add and upgrade Git marketplaces; desktop users can install their entries.
Marketplace refresh can refresh configured plugins. This is distinct from public
directory publication. The packaging schema gives us no documented system-binary
installer. [Packaging and local distribution](https://developers.openai.com/plugins/build/plugins).

Hooks require separate trust. Changed, untrusted hooks are skipped until reviewed;
enabling a plugin must not be reported as working delivery before that trust step.
[Hook trust](https://learn.chatgpt.com/docs/hooks).

The public directory submission rules require lifecycle hooks to be removed. Our
delivery depends on them, so distribute the full integration through a repository
marketplace. A public skills-only listing would have different capabilities and is
outside this first package.
[Submission limits](https://developers.openai.com/plugins/guides/submit-claude-plugin).

Read-only help probes in an isolated home found `codex-cli 0.160.0` supports
`codex plugin add PLUGIN@MARKETPLACE`, `list`, `remove`, and marketplace
`add|list|upgrade|remove`. It has no separate `plugin update` command. This supplements
the docs; no installation or update behavior has been proved. Detect actual CLI
capabilities and keep direct setup for older versions.

### omp and Pi

omp loads extension packages using `omp.extensions` (or the legacy `pi.extensions`
key), alongside configured extension paths. Package the existing `adapters/omp/aboard.ts`
and skill; do not create a second extension implementation. Direct and packaged
copies can otherwise both load.
[omp loading](https://github.com/can1357/oh-my-pi/blob/main/docs/extension-loading.md),
[omp CLI](https://github.com/can1357/oh-my-pi/blob/main/docs/cli-reference.md).

Pi packages can carry extensions, skills, prompts and themes, installed from Git,
npm or local paths. Pinned versions do not float with an update. Pi is still a
baseline skill integration in our roadmap: do not assume omp's extension is a
certified Pi adapter. A Pi skill package is possible; new automatic delivery needs
separate approval and proofs.
[Pi packages](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/packages.md).

## Package and setup design

Proposed repository layout:

```text
.claude-plugin/marketplace.json     # aboard-claude catalog
.agents/plugins/marketplace.json   # aboard-codex catalog
plugins/claude-code/               # manifest, generated skill, hooks, wrapper
plugins/codex/                     # manifest, generated skill, hooks, wrapper
plugins/omp/                       # package manifest, existing extension, skill
```

Use distinct catalog identities and the plugin name `aboard` in each. Validate which
catalog Codex chooses when both formats are present before documenting that flow.
Generate package content from `skills/aboard` and the harness profiles, with a check
that rejects drift. No keys, board credentials or machine paths go into a package.

Hook wrappers preserve stdin, output, exit codes, session identity and the existing
hook options. They locate the installed binary and refuse a missing or incompatible
one with a clear next step. Custom binary and home selection remain machine-local.
Claude's existing profile has version-dependent events and Stop behavior; a static
plugin must declare a supported minimum for its complete hook set. Older harnesses
keep the profile's current direct setup and fallbacks.

`aboard init` initially recommends the matching plugin, with an explicit plugin
choice and a direct fallback. Do not silently change existing installations. After
successful installation, disable only aboard-owned, unchanged direct entries;
refuse an ambiguous migration with an actionable hint. Preserve other hooks and
edited files. Inspect both project and global scopes before enabling delivery.
Keep command allow rules an explicit choice: packaging does not grant sandbox or
network permissions, and setup must not bypass hook trust.

Record only setup ownership, backend, scope, package revision and compatibility in
the local install manifest. `doctor` checks binary/package compatibility, enabled
hooks, trust where observable, and duplicate direct/plugin setups. Uninstall touches
only the integration aboard owns; it does not remove shared catalogs or caches.

Today `aboard upgrade` refreshes remembered global direct installs by running the
new binary's `init`. Extend that path to the selected backend. Update only owned
packages to a revision matching the binary; verify the result. Keep third-party
automatic updates off in our recommended flow. If a harness cannot perform a pinned
update, give the exact manual action and report setup as incomplete. Never silently
fall back to direct hooks after a partial plugin installation.

## How the docs would present it

These are proposed commands, not working aboard catalog entries today. Install the
signed aboard binary first, then add the catalog and plugin:

```text
# Claude Code, inside the session
/plugin marketplace add leonidas1712/aboard
/plugin install aboard@aboard-claude

# Codex CLI, where these commands are supported
codex plugin marketplace add leonidas1712/aboard --ref <matching-release-tag>
codex plugin add aboard@aboard-codex
```

Then complete the harness's trust/reload steps and run `aboard doctor`. The docs must
show the direct fallback for unsupported clients and migration instructions for an
existing installation. Do not promise a one-command binary installation or public
marketplace discovery. omp's final package install line needs a package-resolution
test before publication; no npm publishing is part of this proposal.

## Contracts, size and proof

Before code, add optional plugin metadata to the harness profile schema and specify
setup selection, JSON outcomes, ownership and partial failures in `spec/cli.yaml`.
Preserve existing outputs and profile compatibility. No API or event change is needed.

Rough effort: generated packages, validation and recommendation docs take 1–2
engineering days. Managed migration, upgrade, doctor and failure tests add 2–3 days;
native proofs need a further reserved test window. These are estimates, not launch
commitments. Recommendation-only packaging can ship first; automatic switching
should not be a launch dependency.

Required acceptance before managed setup ships:

- Fresh install, repeated init, and direct-to-plugin migration produce one active
  integration. Edited files, unrelated hooks and other scopes survive.
- Missing binary, unsupported harness, untrusted hooks, failed download/update and
  interrupted migration give clear errors without duplicate hooks or false success.
- Package versions match the selected binary; upgrade and uninstall preserve unrelated
  setup. Pinning and update semantics are tested against the installed harness CLI.
- In isolated homes, each supported harness proves cold start, resume, bidirectional
  delivery, tool/turn behavior and multi-board routing with unchanged ack semantics.
  Run the affected native suite after bounded plugin proofs. Request the laptop slot
  before any native run; record protected-file checks before and after.

## Choices for the maintainer

1. Approve optional packages and recommendations first, keeping direct setup as the
   default; or make automatic migration part of the launch. Recommend the first.
2. Approve repository distribution for Claude Code, Codex and omp. Keep Pi skill-only
   and public-directory submissions separate. Recommend this bounded scope.

Implementation starts after these choices, with contracts first.
