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

User tokens inherit the private-conversation visibility of the authorizing
Slack member and therefore have a larger confidentiality impact than bot
tokens. Direct-message access uses a separate user-only app and profile with
only `im:read`, `im:history`, and `users:read`. The last scope verifies the
other DM participant's workspace and permits `users get` to inspect any exact
valid user ID; the CLI has no user listing or search operation. There are no
write, bot, group-DM, channel, search, or event grants. Runtime reads still
require an exact conversation allowlist bound to that profile and credential
generation. The unlocked-Keychain same-OS-user limitation above is especially
important for this profile.

Because Slack may omit two Slack Connect booleans on ordinary direct-message
objects, DM preflight also verifies the exact other participant with
`users.info`. Missing or malformed identity fields fail closed, and a Team ID
different from the selected profile's workspace is treated as externally
shared. The check runs both when policy is created and before every content
read so a changed participant classification cannot reuse stale authorization.

Message writes are restricted to exact identity-bound conversation IDs. They
require a local dry-run receipt plus exact confirmation, are dispatched once,
and return exit 9 when the outcome cannot be proven. Never automatically retry
such a write.
