# Architecture

## Decision

`slack-agent-cli` is a standalone Go binary with a fixed, typed Slack Web API
route matrix. It does not wrap the official `slack api` command and does not
use Slack MCP as its credential or policy boundary.

The official Slack CLI remains appropriate for app development and
administration. Slack MCP remains useful when client-managed OAuth and its
broader tool surface match the user's intent. Neither exposes this project's
provider-neutral per-call profile selection, stable envelope, target policy,
bounded transport, or one-shot write recovery contract.

## Official Slack authentication nuance

The official [`slack auth list`](https://docs.slack.dev/tools/slack-cli/reference/commands/slack_auth_list/)
enumerates multiple developer authorizations, and the global
[`--team`](https://docs.slack.dev/tools/slack-cli/reference/commands/slack/)
flag selects a workspace or organization for CLI development operations.

The separate [`slack api`](https://docs.slack.dev/tools/slack-cli/reference/commands/slack_api/)
command resolves a Web API token through `--token`, an installed `--app`, a
token environment variable, or an app prompt. Its
[implementation](https://github.com/slackapi/slack-cli/blob/v4.7.0/cmd/api/api.go)
does not make `--team` a Web API token source. A developer authorization in
`slack auth list` therefore must not be treated as the token selected by
`slack api --team`.

`slack api` is intentionally raw: it accepts method names, HTTP methods,
headers, form or JSON bodies. Its
[raw request path](https://github.com/slackapi/slack-cli/blob/v4.7.0/internal/api/raw_request.go)
also has general retry and body behavior that cannot distinguish a safe read
from an ambiguous write. Hiding its flags would still leave this project to
replace credential selection, bounds, typed decoding, policy, output, and
recovery. Direct fixed-route HTTP is the smaller auditable boundary.

The official [Slack MCP server](https://docs.slack.dev/ai/slack-mcp-server/)
documents one OAuth connection in its client examples and a broad tool set.
Its public contract does not document named profile switching on every tool
call. A client may configure multiple server entries, but that is not a
portable per-call account-selection protocol for Codex and Claude Code.

## Local CLI precedent

The design follows the strongest shared properties of the operator's
`jira-cli`, `confluence-cli`, and `redmine-cli`: a single Go binary,
explicit profiles, native credentials, bounded typed data, a versioned machine
envelope, recovery-oriented exits, and one installable Skill. It deliberately
does not inherit `trello-cli`'s historical environment/default-account
fallbacks.

## Package boundaries

```text
cmd/slack-agent-cli -> internal/cli
internal/cli -> profile, auth, policy, slack, output, intent, skills
internal/slack -> contract, errx
all packages -> errx (leaf)
```

`internal/slack` receives an already selected credential and may not import
`auth` or `profile`. `internal/cli` may not import `net/http`. Production
Slack origin and route paths are constants, and no raw API escape hatch exists.

The supported network matrix is:

```text
POST https://slack.com/api/auth.test
GET  https://slack.com/api/conversations.list
GET  https://slack.com/api/conversations.info
GET  https://slack.com/api/conversations.history
GET  https://slack.com/api/conversations.replies
GET  https://slack.com/api/users.info
POST https://slack.com/api/chat.postMessage
```

Every response has compressed and decompressed byte bounds, a deadline, and a
typed DTO. Redirects are disabled. Upstream bodies, request URLs, headers,
tokens, and outbound message text do not enter errors or logs. HTTP success is
not API success until the bounded JSON object has `ok: true`.

## Profiles and credentials

Every network invocation names a profile. One profile binds its name,
workspace/team ID, workspace URL and display name, authenticated user and
optional bot identity, enterprise identity when present, token kind, declared
capabilities, and credential generation.

The token enters through bounded stdin or a bounded hidden read from the
process's controlling terminal. The interactive `--token-tty` path disables
echo, intercepts interrupt, termination, hangup, and terminal-suspend requests,
restores terminal state before cancelling the prompt, and never places the
token in argv or the environment. The token is stored as a versioned Keychain
generic-password payload bound to that full non-secret identity. The atomic
locked registry contains no token. Re-login changes the generation,
invalidating old policies and write receipts.

`auth login` imports and verifies an already-issued credential; it is not an
OAuth client and does not mint tokens or grant scopes. Human-to-human direct
message reads therefore use a separate user-only Slack app and profile. Its
canonical scopes are `im:read`, `im:history`, and `users:read`; the last scope
serves the fixed exact-ID `users.info` route used for participant-workspace
verification and explicit `users get` lookups of any valid user ID. No user
listing or search route exists. The app has no bot or write grant, and each
`D...` target remains behind the identity- and generation-bound read policy.

Slack's [documented one-to-one DM shape](https://docs.slack.dev/reference/methods/conversations.info/)
may omit `is_shared` and `is_ext_shared`. The relaxed classifier applies only
to a valid, non-contradictory `D...`/`is_im` object with `is_org_shared`
present. It validates the other participant through the fixed `users.info`
route and requires a valid exact user ID and Team ID. A different Team ID is
treated conservatively as externally shared. The lookup is repeated during
every content preflight, so participant-workspace drift invalidates the stored
shared-state policy before history or replies are read. Channels and group DMs
retain the complete three-flag requirement.

## Target and content boundary

Conversation content has two exact-ID policies: reads and writes. Both are
bound to profile identity and credential generation; writes must be a subset
of reads. Slack Connect conversations are denied unless their shared status is
explicitly accepted and recorded. A later shared-state mismatch fails closed.

Slack messages, names, topics, purposes, profiles, links, file metadata, and
previews are untrusted. Content-bearing envelopes include
`meta.content_trust: "untrusted"`. The v0.1 surface does not download files or
follow returned URLs.

## Write boundary

`messages send --dry-run` reads only bounded stdin and non-secret local
profile/policy metadata. It emits an identity-bound digest receipt without
message text. A real send must reproduce the exact intent and pass `--yes`.
The CLI then revalidates the credential, workspace, target, shared state, and
optional parent thread before one `chat.postMessage` dispatch.

Transport, timeout, server, or malformed-success conditions that might have
applied the write become `WRITE_OUTCOME_UNKNOWN` at exit 9. One bounded read
may prove the exact message; the CLI never sends it again automatically.

## Agent Skills

The installer exposes a closed set of two embedded Skills and writes their
same canonical bytes to an explicit Codex or Claude Code user/project
destination. It tracks ownership hashes and refuses symlinked, modified, or
unowned targets.

`assets/skills/slack` is the runtime Skill. It may invoke only this fixed CLI
contract and must never fall back to Slack MCP, `slack api`, an SDK, browser
automation, or raw HTTP for an unsupported operation.

`assets/skills/slack-app-provisioning` is a separate operator-setup Skill. It
pins an official Slack CLI version, scaffold, lockfile, and four exact
least-privilege manifests: three bot variants plus a dedicated read-only user
DM variant. Agents may perform bounded local inspection and validation in a
minimal clean environment with Slack CLI telemetry explicitly disabled. The
clean environment derives its home from the operating-system account record,
not caller environment state, and agent invocations use direct argv vectors.
Slack login, app installation, and runtime token entry remain human-only
terminal handoffs. Slack's
[manifest schema](https://docs.slack.dev/reference/app-manifest/) supports user
scopes without a bot, but Slack CLI 4.7.0 does not document that install path.
Its pinned source passes only
[bot scopes into `DeveloperAppInstall`](https://github.com/slackapi/slack-cli/blob/v4.7.0/internal/pkg/apps/install.go)
and its
[approval request](https://github.com/slackapi/slack-cli/blob/v4.7.0/internal/api/app.go)
while the successful environment setup does not expose the returned user
token. The user-only variant therefore requires a fail-closed live human
acceptance gate before release. This administrative surface is not available
to the runtime Slack Skill.
