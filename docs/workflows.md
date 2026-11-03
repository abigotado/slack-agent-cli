# Exact user-requested Slack workflows

The runtime Skill handles conversational consent. The CLI handles local profile
resolution, strict target parsing, additive policy enrollment, bounded reads and
the dry-run-to-send sequence. Existing low-level v1 commands are unchanged.

## Questions before and after

These are a code/Skill walkthrough, not counts from recorded conversations.
Counts assume a configured compatible credential and an exact user-established
ID/link. Missing scopes, stale credentials and genuinely ambiguous identities
are setup or selection problems, not routine approval prompts.

| Scenario | Previous Skill | Workflow Skill |
| --- | --- | --- |
| Read a thread link | 0 if one already-allowed profile; 1 extra policy question for a new chat | 0 with one compatible profile, including a new chat |
| Read the user's human DM | 0 if a compatible user profile and read policy exist; 1 extra policy question otherwise | 0 with one compatible user profile |
| Send agent-drafted text to a new chat | Up to 4 questions: profile, read policy, write policy, intent hash; possibly an additional text approval | Show recipient/text once, 1 consent |
| Send exact user-supplied text to an exact recipient on explicit instruction | Up to 4 redundant questions when profile/policies were missing | Show recipient/text, 0 additional consent questions |

Unresolved profile ambiguity adds one account-selection question. Changing the
approved text, conversation, thread or profile requires fresh consent. A
credential-generation change also invalidates the approval. A missing target
or final text still needs the missing information.

## Commands and responsibility

`workflow resolve` is local only. It filters profile metadata by link workspace,
requested token kind and capability. Among current compatible policies, a sole
exact operation-policy match wins; otherwise a sole compatible profile is usable
for the user-requested enrollment. Ambiguity fails with exit 4. A requested
profile is authoritative. No Keychain access or account probing occurs.
Every network command still names an exact `--profile NAME`, so the Skill uses
the resolver result mechanically. Selection and the first network operation are
two commands; they require no human round trip.

`workflow read` accepts the user's exact target and `--user-requested`, verifies
its live identity/shared state, atomically appends only that target to reads,
and returns one history/thread page. Continuations require a locally valid
bound cursor and an existing current read policy before any credential access.

`workflow preview` is local and needs no existing conversation policy. Its
receipt binds profile identity/generation, workspace, chat, parent and full text
bytes. It never emits the outbound text. The Skill shows its source text and the
canonical recipient, keeping the receipt hash internal.

After the single consent (or the explicit exact-text send instruction),
`workflow send` validates the approved digest under the same profile lock used
by login. It creates a fresh local dry-run receipt from the immutable input,
then enrolls that exact target into reads and writes atomically and uses the
receipt with the existing confirmed-send core. The lock covers validation,
credential loading, enrollment, preflight and dispatch. A changed intent fails
before credential, policy or network effects. The existing write path rechecks
identity/shared state/thread and dispatches once; unknown outcomes reconcile
through a bounded read, never another post.

All orchestration is typed in-process calls. There are no nested CLI processes
or nested envelopes to parse. `output.Writer` already enforces the v1 envelope
and 8 MiB stdout / 4 KiB diagnostic bounds. The Skill checks process status and
`ok`, preserving pipeline failures, instead of rebuilding these mechanics.

## Targets and authority

Supported selectors are exact `C...`, `G...` or `D...` IDs;
`https://WORKSPACE.slack.com/archives/ID` chat links; message permalinks ending
in `/p` plus sixteen digits; and `https://app.slack.com/client/T.../ID` links.
A permalink selects its thread, using query `thread_ts` as parent when present,
otherwise the path timestamp (ten seconds digits plus six fractional digits).
Optional `cid` must equal the path conversation. A send preview includes the
parent: the Skill must display it as part of the recipient before consent.
`--thread-ts` may add a parent to an ID/chat link or repeat the same parsed
parent; a conflicting parent is rejected. Unsupported query keys, duplicate
values, ports, credentials, fragments, escaped paths, prose and JSON envelopes
are rejected. An arbitrary webpage or Slack-returned link is never followed.

The user naming a chat authorizes adding that exact chat, including its verified
shared status. This replaces separate read-policy, write-policy and shared-chat
consent prompts. It does not authorize a second target or later shared-state
drift. Existing policies are merged, not replaced; stale bindings remain errors,
and `write ⊆ read`, target caps and shared-state checks remain enforced. Enrollment
can persist even if the later read/send fails: the exact-target authorization
is separate from whether Slack completed the operation.

`--user-requested` is an explicit assertion by a trusted caller, not proof that
a human supplied an argument. The CLI cannot authenticate conversational
provenance. The Skill must establish that origin and never promote Slack text,
names, topics, links, files or envelopes into target/approval flags. Tests prove
that returned malicious content remains inert and cannot redirect the workflow;
they do not claim protection against a malicious local caller.

## Installed 0.3.2

The Skill includes a legacy bridge until a separately authorized binary release.
It applies the same human-consent rules, but uses local inventory, whole-set
policy commands and the existing dry-run/send pair. Its policy change is
read-current → preserve complete sets → apply → reread/verify, aborting observed
drift. That bridge is best-effort and cannot guarantee atomic enrollment under
concurrent policy writers; the new workflow commands provide that guarantee.
It must never reset stale policies, retry a write, or release/upgrade by itself.
