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

Content originating from Slack is emitted with
`meta.content_trust: "untrusted"`. Tokens and full outbound message text never
appear in envelopes, errors, hints, or diagnostics.

## Recovery exits

| Exit | Name | Required recovery |
| ---: | --- | --- |
| 0 | OK | proceed |
| 1 | INTERNAL | report; do not retry unchanged |
| 2 | USAGE | fix flags or bounded input |
| 3 | NOT_FOUND | verify exact ID and profile visibility |
| 4 | AMBIGUOUS | choose an exact candidate |
| 5 | AUTH | login, rotate, or repair the exact profile |
| 6 | RETRYABLE | honor bounded backoff and retry only the safe read |
| 7 | CONFIRMATION_REQUIRED | review dry-run and approve exact intent |
| 8 | PERMISSION_DENIED | request Slack permission or explicit policy change |
| 9 | CONFLICT | reconcile stale/unknown state; never retry a write automatically |

Run `slack-agent-cli contract` for the machine-readable exit table, frozen v1
bounds, and fixed Slack method matrix.

Collection limits are enforced at both command and transport boundaries. An
opaque pagination cursor is bound to the complete profile identity (including
credential generation), workspace, operation, and normalized query; it cannot
be reused after an account/profile transition.

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
