# Provisioning state machine

## Local states

`prepared` means an explicit empty directory contains only the canonical
scaffold and selected manifest. `dependencies-ready` means the separately
approved locked `npm ci --ignore-scripts --no-audit --no-fund` completed.
`validated` means the environment/version gates, manifest inspection, and
manifest validation all completed within their output bounds.

Every transition that invokes Slack CLI uses the sanitized environment with
telemetry disabled. A caller-provided Slack environment variable or a command
without that prefix leaves the state unchanged.

Only `validated` may advance to a human install handoff.

## Remote handoff states

- `awaiting-human-install`: show exact Team ID, manifest digest, scopes, and
  command, then stop.
- `installed`: the operator reports success and supplies the exact app ID.
  A bounded exact-ID `manifest diff` may inspect the result.
- `admin-approval-pending`: stop. Do not broaden scopes, change workspace, or
  resubmit.
- `outcome-unknown`: any timeout, truncation, prompt mismatch, nonzero exit,
  interruption, or lost output. Never rerun create/install automatically.

Resume from `outcome-unknown` only after the operator independently supplies
the exact app ID and installation state from Slack. If no exact app ID is
available, remain stopped. Update, uninstall, and delete are unsupported.

## User-only live acceptance

The `user-direct-message-read-only` variant is not accepted merely because its
manifest validates. Slack CLI 4.7.0 does not document the user-only install
path, and its admin-approval behavior may be bot-scope-oriented.

For an exact Team ID, the operator performs the install once using a separate
canonical app. Any admin-approval state, unexpected prompt, missing output,
failure, or uncertainty is terminal: do not retry, broaden scopes, update an
existing bot app, or switch tools.

After reported success, the operator must independently confirm all of these
without exposing token or page content to the agent:

1. OAuth & Permissions shows a User OAuth Token.
2. No Bot User OAuth Token or bot grant was created.
3. Hidden-TTY import with token kind `user` succeeds for a separate profile.
4. `auth status --profile PROFILE --check` confirms that exact credential.
5. After explicit approval of one exact `D...` read target, policy dry-run and
   apply succeed, followed by bounded `conversations list --types im`,
   conversation info, history, and replies smoke reads.

Until all five checks pass, report the variant as unverified and do not release
or recommend it as an end-to-end supported workflow.
