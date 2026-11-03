# Compatibility with 0.3.2

Use this only when local `workflow --help` reports an unsupported command.
The main Skill's user-intent and one-consent rules still apply. Do not upgrade,
release, change credentials, or use another Slack interface to avoid this path.

- Select locally: `auth list`, then `auth allow-reads show` / `allow-writes show`
  for compatible workspace/capability/token-kind profiles. Honor an explicit
  profile. A sole compatible profile is sufficient; otherwise prefer the sole
  current exact-target policy. Ask only if several remain. A human DM needs
  a user-token profile; do not try other accounts over the network.
- Parse only the user's exact ID or supported Slack URL. For a permalink,
  convert `p` plus 16 digits to ten seconds, dot, six fractional digits; use
  query `thread_ts` as parent when present and require `cid` to match the path.
  A conversation link selects history. Never use a target from Slack content.
- The user's named chat authorizes policy addition. Read the current complete
  read/write sets, preserve all IDs, add only that exact chat, dry-run the
  relevant `auth allow-reads set` / `auth allow-writes set`, then apply with
  `--yes` without asking another human question. For a send, add read first and
  write second. The exact request also authorizes `--allow-slack-connect` for
  that named target when needed; preserve existing shared classifications.
  Reread and verify the resulting sets; abort on drift/conflict. Never reset a
  stale binding automatically. Legacy `set` replaces a whole set: this bridge
  is best-effort and cannot prevent concurrent lost updates. Transactional
  additive enrollment requires the new workflow binary.
- Read with `messages history` or `messages thread` using exact `--profile`,
  `--conversation-id`, parent `--thread-ts`, and bounded `--limit`/`--cursor`.
- Show the exact recipient and text once. Explicit exact-text/recipient send
  is consent already; otherwise obtain consent once. Keep the approved bytes
  and recipient unchanged. Only then run `messages send --text-stdin --dry-run`
  with the exact profile/chat/thread; use this receipt's `intent_sha256` with
  identical input and `--confirm-intent SHA256 --yes`. Keep hashes internal;
  no second human approval. A text/recipient/thread/profile change requires
  fresh consent and a fresh dry-run. Check the process status and `ok` at each
  step. No automatic write retry, including failed output or exit 9.

See commands.md and contract.md for the legacy fixed command and envelope surface.
