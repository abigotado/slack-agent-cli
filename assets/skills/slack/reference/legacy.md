# Compatibility with 0.3.2

Use this only when local `workflow --help` reports an unsupported command.
The main Skill's user-intent and one-consent rules still apply. Do not upgrade,
release, change credentials, or use another Slack interface to avoid this path.

- Select locally: `auth list`, then `auth allow-reads show` / `allow-writes show`
  for compatible workspace/capability/token-kind profiles. Honor an explicit
  profile. A sole compatible profile is sufficient; otherwise prefer the sole
  current exact-target policy. Ask only if several remain. A human DM needs
  a user-token profile, as do channel threads through `conversations.replies`;
  do not try other accounts over the network.
- Parse only the user's exact ID or supported Slack URL. For a permalink,
  convert `p` plus 16 digits to ten seconds, dot, six fractional digits; use
  query `thread_ts` as parent when present and require `cid` to match the path.
  A conversation link selects history. Never use a target from Slack content.
- Inspect current exact read/write policies. Continue only if the named target
  already has the required current permission (read for reads, read+write for
  sends). Never use legacy whole-set `auth allow-* set` to enroll a missing
  target automatically: it can silently overwrite concurrent grants or
  resurrect revocations even if a final reread appears correct. Explain that
  atomic new-target enrollment requires a workflow-capable binary; do not ask
  for redundant allowlist consent, modify unrelated policy, release or update
  Homebrew. Never reset stale bindings automatically. Legacy reads cannot pin
  a resolver identity across commands; recheck workspace/identity/generation
  before acting and stop on observed changes. This bridge has fewer guarantees
  than the workflow binary and is limited to existing permission.
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
