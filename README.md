# slack-agent-cli

`slack-agent-cli` is a provider-neutral, machine-oriented Slack boundary for
Codex and Claude Code. It is deliberately smaller than a general Slack client:
every network call selects one exact profile, every Slack operation is typed
and bounded, content targets are allowlisted, and message writes use a local
receipt plus exact confirmation.

The [architecture](docs/architecture.md) explains why the official Slack CLI
and Slack MCP remain useful developer tools but are not the enforcement
boundary. The stable JSON and recovery contract is in
[docs/contract.md](docs/contract.md).

## Security model

- No default, active, environment-selected, or inferred account exists. Every
  network command requires `--profile NAME`.
- Multiple named profiles may represent different workspaces or different
  accounts in the same workspace.
- Tokens enter through bounded stdin or a hidden bounded controlling-terminal
  prompt and are stored only in macOS Keychain.
  They never appear in argv, environment variables, registry files, logs,
  errors, or output.
- Only fixed typed Slack Web API operations exist. There is no `api`,
  `request`, `raw`, custom origin, arbitrary header, or arbitrary body
  command.
- Conversation reads and writes require exact ID policies bound to profile
  identity and credential generation. Write targets must also be readable.
- Slack-controlled names, topics, profiles, links, and messages are marked
  `content_trust: "untrusted"`.
- Message sends are dispatched once. An ambiguous outcome exits 9 and must be
  reconciled, never automatically retried.

See [SECURITY.md](SECURITY.md) for the local credential-boundary limitations.

## Platform support

The v0.1 runtime target is macOS because the credential store calls
Security.framework directly. Linux is supported for source compilation and
tests but has no credential backend. Windows support is deferred until it has a
native credential store and lock boundary.

## Install

### Homebrew

On macOS, install the source-building Formula from the public tap:

```sh
brew install abigotado/tap/slack-agent-cli
slack-agent-cli version
```

The Formula pins the immutable source release by SHA-256, stages checksummed Go
modules, and builds locally with CGO enabled so credentials remain in the
native Security.framework Keychain backend. It does not install an unsigned
prebuilt executable or invoke `/usr/bin/security`.

### Go

Go 1.25.14 or newer:

```sh
go install github.com/abigotado/slack-agent-cli/cmd/slack-agent-cli@v0.2.2
slack-agent-cli version
```

Or build a checkout:

```sh
git clone https://github.com/abigotado/slack-agent-cli.git
cd slack-agent-cli
go build -o bin/slack-agent-cli ./cmd/slack-agent-cli
./bin/slack-agent-cli contract
```

Published releases include a deterministic source bundle,
`release-manifest.json`, and `SHA256SUMS`. No unsigned prebuilt macOS binary
is distributed. Binaries built from the release source bundle retain the exact
release tag and commit in `slack-agent-cli version`.

## Create explicit profiles

For one operator's workspace, use one separate user-only Slack app/token and
one named profile. The canonical `user-workspace-message-write` manifest has
user scopes `channels:history`, `channels:read`, `groups:history`,
`groups:read`, `im:history`, `im:read`, `mpim:history`, `mpim:read`,
`users:read`, and `chat:write`. It acts as the authorizing user for exact
allowlisted public channels, private channels, one-to-one DMs, group DMs, and
confirmed sends. It does not include a bot, search, files, reactions, admin,
events, redirects, or `chat:write.public`.

The runtime remains smaller than a general Slack connector: it cannot search
or read arbitrary content, every target needs explicit exact-ID policy, and
writes still need a local dry-run receipt and exact confirmation. Private
channels, DMs, and group DMs must be accessible to the authorizing user.

The CLI verifies the token with `auth.test` before committing the profile.
Enter it directly into the hidden controlling-terminal prompt:

```sh
slack-agent-cli auth login \
  --profile bangr \
  --token-kind user \
  --capability read \
  --capability message-write \
  --token-tty
```

`--token-stdin` remains available for a bounded secret pipe. The CLI never
reads a token environment variable. For another account or workspace, repeat
with another explicit profile name.

```sh
slack-agent-cli auth list
slack-agent-cli auth status --profile bangr
slack-agent-cli auth status --profile bangr --check
```

### Narrow direct-message-only profile

For a workflow that needs only human one-to-one DMs, the embedded
`slack-app-provisioning` Skill also provides the narrower
`user-direct-message-read-only` manifest with user scopes `im:read`,
`im:history`, and `users:read`. The last scope verifies the other DM
participant's workspace and also permits the fixed `users get` command to
inspect any exact valid user ID. The CLI exposes no user listing or search
operation.

After the human-only Slack installation has issued a User OAuth Token, import
it directly through the hidden terminal prompt. This command does not create a
token or grant scopes:

```sh
slack-agent-cli auth login \
  --profile bangr-user \
  --token-kind user \
  --capability read \
  --token-tty
```

Then explicitly approve each exact `D...` conversation in the read allowlist
before reading it. The canonical user-only app has no bot, write, channel,
group-DM, search, event, or redirect permissions. Slack CLI 4.7.0 does not
document this user-only install path, so the provisioning Skill requires a
one-shot human acceptance check and fails closed on admin approval or an
uncertain result.

## Install Agent Skills

The runtime Skill and the separate app-provisioning Skill install from the
same binary for either Codex or Claude Code. Review with `--dry-run`, then
repeat with `--yes`:

```sh
slack-agent-cli skill install --skill slack --provider codex --scope user --dry-run
slack-agent-cli skill install --skill slack --provider codex --scope user --yes

slack-agent-cli skill install --skill slack-app-provisioning --provider codex --scope user --dry-run
slack-agent-cli skill install --skill slack-app-provisioning --provider codex --scope user --yes
```

## Allow exact targets

Policy changes replace the complete exact-ID set. Review the dry-run before
applying the same set. Slack Connect targets require a separate explicit flag.

```sh
slack-agent-cli auth allow-reads set \
  --profile work \
  --conversation-id C0123456789 \
  --dry-run

slack-agent-cli auth allow-reads set \
  --profile work \
  --conversation-id C0123456789 \
  --yes

slack-agent-cli auth allow-writes set \
  --profile work \
  --conversation-id C0123456789 \
  --dry-run

slack-agent-cli auth allow-writes set \
  --profile work \
  --conversation-id C0123456789 \
  --yes
```

If an explicitly confirmed re-login replaces a profile's identity or credential
generation, its existing policy is intentionally stale. Use a reviewed
`allow-reads set` with `--reset-stale-policy` once to replace that binding;
the transition drops all old write targets and installs only the displayed
exact read IDs. Rebuild the write policy afterwards.

## Bounded reads

Collection reads require a limit from 1 through 100. Start small and follow the
opaque `meta.next_cursor` only while the task requires more data.

```sh
slack-agent-cli conversations list \
  --profile work \
  --types public_channel,private_channel \
  --limit 25

slack-agent-cli messages history \
  --profile work \
  --conversation-id C0123456789 \
  --limit 25
```

Normal output is one compact v1 JSON envelope:

```json
{"ok":true,"v":1,"data":[],"meta":{"profile":"work","workspace_id":"T123","content_trust":"untrusted","count":0}}
```

## Guarded writes

Message text is bounded plain text from stdin. First produce a local receipt;
this step does not access Keychain or Slack and does not echo the text:

```sh
printf '%s' 'Deployment complete.' |
  slack-agent-cli messages send \
    --profile work \
    --conversation-id C0123456789 \
    --text-stdin \
    --dry-run
```

After reviewing the exact receipt, repeat the identical input with its
`intent_sha256`:

```sh
printf '%s' 'Deployment complete.' |
  slack-agent-cli messages send \
    --profile work \
    --conversation-id C0123456789 \
    --text-stdin \
    --confirm-intent EXACT_SHA256_FROM_RECEIPT \
    --yes
```

Never retry a send after exit 9 or `WRITE_OUTCOME_UNKNOWN`.

## Install the Agent Skill

The binary embeds one canonical Skill and writes identical content for either
provider. Inspect the local plan first:

```sh
slack-agent-cli skill install --provider codex --scope user --dry-run
slack-agent-cli skill install --provider codex --scope user --yes

slack-agent-cli skill install --provider claude --scope user --dry-run
slack-agent-cli skill install --provider claude --scope user --yes
```

Project scope additionally requires an explicit `--project-dir`. The
installer tracks owned hashes and refuses to overwrite modified, symlinked, or
unowned destinations.

## Development

```sh
test -z "$(gofmt -l .)"
go mod verify
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...
govulncheck ./...
gitleaks dir . --redact
gitleaks git . --redact
actionlint
shellcheck tools/release/*.sh
tools/release/test-source-bundle.sh
```

See [CONTRIBUTING.md](CONTRIBUTING.md) before changing the machine, credential,
transport, policy, or write-recovery boundary.
