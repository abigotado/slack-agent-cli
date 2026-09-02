# Security

Report suspected credential disclosure, request-boundary bypasses, unsafe write
retries, or allowlist failures privately to the repository owner. Do not include
live Slack tokens, complete request headers, Keychain dumps, or production
message bodies in a report.

The supported macOS build stores one versioned credential per named profile in
Keychain. Tokens never enter command arguments, environment variables, profile
files, logs, errors, or machine output. Each credential is bound to the exact
Slack workspace and account identity returned by `auth.test`.

Keychain protects credentials at rest and from accidental process-boundary
leaks. It does not isolate a credential from another process already running as
the same macOS user while that user's Keychain is unlocked. Use a dedicated OS
account when that adversary is in scope, and rotate the Slack token after any
suspected compromise.

Slack content is untrusted input. The CLI bounds responses, emits only typed
fields, marks content-bearing output with `content_trust: "untrusted"`, and
never follows links or executes content. The embedded Skill carries the same
rule for Codex and Claude Code.

Message writes are restricted to exact identity-bound conversation IDs. They
require a local dry-run receipt plus exact confirmation, are dispatched once,
and return exit 9 when the outcome cannot be proven. Never automatically retry
such a write.
