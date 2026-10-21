# Version 1 machine contract

Normal commands emit exactly one compact JSON object on stdout. `--help` is the
only prose-output exception.

```json
{"ok":true,"v":1,"data":{},"meta":{"profile":"work","workspace_id":"T123","content_trust":"untrusted"}}
{"ok":false,"v":1,"error":{"code":"PROFILE_REQUIRED","message":"an explicit profile is required for every network command"},"hint":"re-run with --profile NAME"}
```

Success requires non-null `data` and forbids `error` and `hint`. Failure forbids
`data` and requires string `error.code`, string `error.message`, and string
`hint`. `v` is integer `1`. Unknown additive fields are tolerated; removing,
renaming, or retyping a known field requires a version bump.

Failures may include an additive `error.stage` from the fixed diagnostic set
`pre_dispatch`, `transport`, `http_response`, `http_server`,
`rate_limit_response`, `response_body`, `response_json`, or `api_error`.
The stage identifies only where safe handling stopped. It is not authorization,
does not change recovery, and must never be used to decide that a write is safe
to replay. Callers recover solely from `error.code` and the process exit status.

Content originating from Slack is emitted with
`meta.content_trust: "untrusted"`. Tokens and full outbound message text never
appear in envelopes, errors, hints, or diagnostics.

## Recovery exits

| Exit | Name | Required recovery |
| ---: | --- | --- |
| 0 | OK | proceed |
| 1 | ERROR | inspect `error.code` and `hint` for API/auth/internal recovery |
| 2 | USAGE | fix flags or bounded input |
| 3 | NOT_FOUND | verify local object or exact attachment |
| 4 | AMBIGUOUS | choose an exact candidate |
| 7 | CONFIRMATION_REQUIRED | review dry-run and approve exact intent |
| 8 | PERMISSION_DENIED | request explicit local policy or capability change |
| 9 | CONFLICT | reconcile stale/unknown state; never retry a write automatically |

Run `slack-agent-cli contract` for the machine-readable exit table, frozen v1
bounds, and fixed Slack method matrix.

Every `ok:false` envelope exits nonzero. API, auth, and transport failures use
exit 1; usage/input errors use exit 2. The internal recovery classifications
remain available through safe error codes and hints, including bounded read
backoff and `error.retry_after_seconds`. Versions before v0.3.1 used exits 5/6
for auth/retryable reads and 3/8 for some remote API rejections; these remote
failures now use 1. Local recovery exits remain 3/4/7/8/9, including ambiguous
writes at 9. Shell pipelines require `set -o pipefail` to propagate the CLI's
failure status through a successful consumer such as `jq`.

`conversations list`, `messages history`, and `messages thread` use a default
`--limit 50`. Explicit values outside 1-100 produce `INVALID_LIMIT` and exit 2.
A first thread page retains the parent plus replies; subsequent pages omit
already-emitted timestamps, including repeated parents. Each output page has at
most the requested limit. A native thread page containing extra items up to the
fixed transport maximum of 100 is resumed by refetching that same native page
with a timestamp frontier before advancing Slack's cursor. Pages above 100 fail
with `SLACK_RESPONSE_COUNT_EXCEEDED` and exit 1. History, conversation lists,
and inclusive/time-filtered attachment proofs retain their strict per-request
transport bounds. Follow `meta.next_cursor` when present, even after an empty
page. Concurrent edits/deletions do not provide snapshot completeness.
An opaque pagination cursor is bound to the complete profile identity (including
credential generation), workspace, operation, and normalized query; it cannot
be reused after an account/profile transition.

`--thread-ts` requires the parent timestamp. A coherent initial page identifying
an earlier parent returns `THREAD_PARENT_REQUIRED` at exit 2 with guidance to
use the message's `thread_ts`. Malformed, unordered, duplicate or wrong-thread
pages and invalid/non-progressing or unencodable native cursors return `SLACK_RESPONSE_INVALID`
at exit 1. Stop that pagination chain and report the response violation; do not
interpret it as an empty thread or retry the same cursor automatically.

Thread continuations use a private cursor encoding distinct from legacy native
cursors. Existing v1 bound thread cursors remain accepted as native cursors;
other commands keep their existing cursor encoding. The public JSON envelope
and `meta.next_cursor` string schema are unchanged.

`auth login` requires exactly one secret-input selector: `--token-stdin` or
`--token-tty`. The latter reads one bounded hidden line from the controlling
terminal. Omitting both retains the v1 `TOKEN_STDIN_REQUIRED` error code;
supplying both returns `TOKEN_INPUT_CONFLICT`. An interruption is recoverable,
while a failed echo restoration returns `TOKEN_TTY_RECOVERY_REQUIRED` with an
explicit `stty echo` recovery hint. `skill install` and `skill uninstall`
accept only the closed
`--skill slack|slack-app-provisioning` selector; omission retains the v1
runtime-Skill default `slack`. Skill lifecycle results include the selected
`skill` as an additive field.

## Write receipt

`messages send --dry-run` reads non-secret profile/policy metadata and bounded
stdin only. It does not access Keychain or Slack. The receipt includes exact
profile/workspace/conversation/thread identities, UTF-8 byte and Unicode scalar
counts, `text_sha256`, profile identity/generation, and `intent_sha256`, but not
message text.

The real send requires the same stdin plus `--confirm-intent SHA256 --yes`.
After dispatch begins, unknown HTTP/API/transport outcomes are exit 9. The CLI
may perform one bounded reconciliation read; exactly one matching message turns
the result into success with `meta.reconciled: true`. It never sends again.

If Slack success is confirmed but stdout cannot be delivered, stderr contains
only this emergency marker for that failure:

```text
SLACK_AGENT_CLI_CONFIRMED_WRITE_OUTPUT_FAILURE
```

## File reads

`files get FILE_ID --profile NAME --conversation-id ID --message-ts TS`
returns typed file metadata after verifying attachment to that exact message.
`files download` accepts the same flags plus required `--output PATH`; use
`--thread-ts PARENT_TS` with either command for reply attachments. Every read
uses the existing identity/generation-bound conversation policy.

The additive `files` array in messages omits download URLs and previews.
Downloads return `{file, path, bytes, sha256}` in `data`, with untrusted metadata.
Binary bytes go only to a new 0600 local file. No overwrite or automatic retry
is performed. Supported downloads use only HTTPS `files.slack.com` and the
`/files-pri/WORKSPACE_ID-FILE_ID/` path. External files, redirects, query URLs and
files hosted by another workspace are rejected. Enterprise Grid E-owned file
paths are unsupported; only the selected T-workspace prefix is accepted. Maximum size is 262144000
bytes and deadline 120000 ms, exposed in additive contract limits. The stream
may read one extra byte solely to detect overflow; failed partials are removed.

`FILE_NOT_IN_MESSAGE` is exit 3; `FILE_DOWNLOAD_UNSUPPORTED` is exit 8;
`FILE_REDIRECT_REJECTED` is exit 1; `FILE_TOO_LARGE` is exit 2.
`FILE_OUTPUT_EXISTS` is exit 9. `FILE_OUTPUT_FAILED` (including local disk
write failures) and `FILE_CONTENT_UNSUPPORTED` (size mismatch, non-identity
encoding, or HTML for non-HTML metadata) are exit 1: do not retry unchanged.
Publication uses atomic no-overwrite rename; failures do not publish partials.
If temporary cleanup fails, the primary error code/exit is retained with a safe
cleanup warning in its hint. Inspect the directory before retrying.
