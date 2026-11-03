# Command reference

Every network command requires `--profile NAME`.

```text
slack-agent-cli auth login --profile NAME --token-kind user|bot \
  --capability read [--capability message-write] --token-stdin [--yes]
slack-agent-cli auth list
slack-agent-cli auth status --profile NAME [--check]
slack-agent-cli auth logout --profile NAME --yes

slack-agent-cli auth allow-reads show --profile NAME
slack-agent-cli auth allow-reads set --profile NAME \
  --conversation-id ID... [--allow-slack-connect] [--reset-stale-policy] --dry-run|--yes
slack-agent-cli auth allow-reads clear --profile NAME --dry-run|--yes

slack-agent-cli auth allow-writes show --profile NAME
slack-agent-cli auth allow-writes set --profile NAME \
  --conversation-id ID... [--allow-slack-connect] --dry-run|--yes
slack-agent-cli auth allow-writes clear --profile NAME --dry-run|--yes

slack-agent-cli me --profile NAME
slack-agent-cli conversations list --profile NAME --types TYPES [--limit N] [--cursor CURSOR]
slack-agent-cli conversations get CONVERSATION_ID --profile NAME
slack-agent-cli messages history --profile NAME --conversation-id ID [--limit N] \
  [--cursor CURSOR] [--oldest TS] [--latest TS]
slack-agent-cli messages thread --profile NAME --conversation-id ID \
  --thread-ts TS [--limit N] [--cursor CURSOR]
slack-agent-cli users get USER_ID --profile NAME
slack-agent-cli files get FILE_ID --profile NAME --conversation-id ID \
  --message-ts TS [--thread-ts PARENT_TS]
slack-agent-cli files download FILE_ID --profile NAME --conversation-id ID \
  --message-ts TS [--thread-ts PARENT_TS] --output PATH

slack-agent-cli messages send --profile NAME --conversation-id ID \
  [--thread-ts TS] --text-stdin --dry-run
slack-agent-cli messages send --profile NAME --conversation-id ID \
  [--thread-ts TS] --text-stdin --confirm-intent SHA256 --yes
```

`--reset-stale-policy` is valid only for `allow-reads set` after an explicitly
confirmed profile-identity or credential-generation migration. Its dry-run and
confirmed apply replace the old policy binding, drop every old write target,
and install only the supplied exact read IDs. Rebuild writes afterwards.

The Skill must never call a command named `api`, `request`, or `raw`; these are
not part of the binary contract.

Paginated reads default to `--limit 50`; explicit values must be 1-100.

## User-requested workflows

Local selection and preview never load Keychain or contact Slack:

```text
slack-agent-cli workflow resolve --target ID_OR_LINK --operation read|send \
  [--profile NAME] [--token-kind user|bot] [--workspace-id ID] [--thread-ts TS]
slack-agent-cli workflow preview --profile NAME --profile-binding BINDING --target ID_OR_LINK \
  [--thread-ts TS] --text-stdin
slack-agent-cli workflow read --profile NAME --profile-binding BINDING --target ID_OR_LINK \
  [--thread-ts TS] --user-requested [--limit N] [--cursor CURSOR]
slack-agent-cli workflow send --profile NAME --profile-binding BINDING --target ID_OR_LINK \
  [--thread-ts TS] --text-stdin --approved-intent INTERNAL_PREVIEW_DIGEST \
  --yes --user-requested
```

Carry `profile_binding` from resolve to every workflow target command; it
pins the selected identity/generation before any effects.
The user's exact chat request authorizes additive enrollment, without a second
allowlist question. Send approval is for the recipient and source text, not a
human-visible hash. A permalink's parent is part of the approved recipient.
Existing explicit-profile commands remain available; see legacy.md for 0.3.2.
