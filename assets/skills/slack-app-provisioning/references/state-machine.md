# Provisioning state machine

## Local states

`prepared` means an explicit empty directory contains only the canonical
scaffold and selected manifest. `dependencies-ready` means the separately
approved locked `npm ci --ignore-scripts --no-audit --no-fund` completed.
`validated` means the environment/version gates, manifest inspection, and
manifest validation all completed within their output bounds.

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
