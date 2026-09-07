# Canonical manifests

Choose one exact manifest. Redirect URLs are absent. Interactivity, org
deployment, Socket Mode, and token rotation are explicitly disabled. Events,
outgoing domains, distribution, App Home, shortcuts, slash commands, Agents,
and MCP are absent.

## `read-only`

Asset: `assets/manifests/read-only.json`

Bot scopes:

```text
channels:history
channels:read
files:read
users:read
```

Use with runtime capability `read`.

## `read-message-write`

Asset: `assets/manifests/read-message-write.json`

Bot scopes:

```text
channels:history
channels:read
chat:write
files:read
users:read
```

Use with runtime capabilities `read` and `message-write`. `chat:write.public`
is intentionally absent; the bot must be invited to an allowlisted channel.

## `all-channels-message-write`

Asset: `assets/manifests/all-channels-message-write.json`

Bot scopes:

```text
channels:history
channels:read
chat:write
files:read
groups:history
groups:read
users:read
```

Use with runtime capabilities `read` and `message-write` when both public and
private channels are required. Slack still exposes only conversations the bot
can access. `chat:write.public` remains intentionally absent, so the bot must
be invited before it can write. Direct and group direct messages remain out of
scope; `im:*` and `mpim:*` are absent.

## `user-direct-message-read-only`

Asset: `assets/manifests/user-direct-message-read-only.json`

User scopes:

```text
files:read
im:history
im:read
users:read
```

Use with runtime capability `read` in a separate user-token profile. This
variant reads one-to-one direct messages that the authorizing user belongs to.
`im:read` discovers and inspects those conversations, `im:history` reads their
history and replies, and `users:read` supports the runtime's fixed exact-ID
`users.info` operation. The runtime uses that operation to verify the other DM
participant's workspace, and `users get` can inspect any exact valid user ID;
there is no user listing or search operation.

This is a dedicated user-only app: bot scopes, `bot_user`, `chat:write`,
`im:write`, all `mpim:*` scopes, channel and private-channel scopes, search,
events, and redirect URLs are absent. Never apply it to an existing bot app.
Group direct messages remain unsupported.

Slack's manifest schema permits user scopes without redirect URLs or a bot
user, but Slack CLI 4.7.0 does not document installation of this exact
user-only shape. Local inspection and `manifest validate` are necessary but
not sufficient. Apply the live, human-only acceptance gate in
[state-machine.md](state-machine.md) before treating the variant as usable.

## `user-workspace-message-write`

Asset: `assets/manifests/user-workspace-message-write.json`

User scopes:

```text
channels:history
channels:read
chat:write
files:read
groups:history
groups:read
im:history
im:read
mpim:history
mpim:read
users:read
```

Use with runtime capabilities `read` and `message-write`. This is the single
user-identity variant for an operator who wants the agent to act as them
across exact allowlisted public channels, private channels, one-to-one DMs,
and group DMs. It has no bot user, bot scopes, redirect URLs, events, search,
reactions, admin, or `chat:write.public` grant. The fixed runtime still
cannot search Slack, create conversations, or send without a
local receipt and exact confirmation.

User tokens can read public channels visible to the workspace and can access
private channels, DMs, and group DMs only where the authorizing user has
access. Every content target remains an exact-ID policy; Slack Connect needs
an additional explicit opt-in. Group DM support is conditional on the live
acceptance gate: if its `conversations.info` shape omits the shared-state
fields required by the runtime, do not claim support or weaken classification.

Provision this as a new separate user-only app. Never add user scopes to an
existing bot app, and never overwrite an installed app to broaden it.

Before validation or handoff, hash the exact selected manifest and show the
SHA-256 to the operator. Never accept an edited or merged manifest.

## File reads and existing installations

All five variants include `files:read` for the fixed `files get` and
`files download` commands. This grants neither file upload nor deletion.
Files must be attached to an exact message in a read-allowlisted conversation.
Hosted downloads are restricted to the selected workspace and 250 MiB.

Existing installations do not gain scopes from a CLI update. The operator
must authorize the updated scopes in Slack (reinstall/reauthorize the app),
then import the resulting credential using `auth login` for the exact profile.
The runtime Skill cannot perform this administrative step. Re-login changes
credential generation and invalidates old policies. Review the complete target
set, run `auth allow-reads set --reset-stale-policy --dry-run`, then apply that
exact set with `--yes`. This clears old write targets; rebuild write policy
separately after review. Never add a requested channel implicitly.
