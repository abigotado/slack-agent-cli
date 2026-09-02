# Canonical manifests

Choose one exact manifest. User scopes and redirect URLs are absent.
Interactivity, org deployment, Socket Mode, and token rotation are explicitly
disabled. Events, outgoing domains, distribution, App Home, shortcuts, slash
commands, Agents, and MCP are absent.

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

Before validation or handoff, hash the exact selected manifest and show the
SHA-256 to the operator. Never accept an edited or merged manifest.
