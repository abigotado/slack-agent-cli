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

Before validation or handoff, hash the exact selected manifest and show the
SHA-256 to the operator. Never accept an edited or merged manifest.
