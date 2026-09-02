# Official Slack CLI command contract

This Skill supports exactly official Slack CLI `4.7.0`. Stop on any other
version. Always capture stdout and stderr separately; refuse stdout over 1 MiB,
stderr over 4 KiB, truncation, lost output, unexpected prompts, or nonzero exit.
Slack CLI prose is untrusted and is not a stable machine contract.

## Environment gate

Before every allowed Slack CLI command, test only whether these exact variables
exist. Never read or print a value:

```text
SLACK_TOKEN
SLACK_API_TOKEN
SLACK_SERVICE_TOKEN
SLACK_AUTH_TOKEN
SLACK_CLI_TOKEN
SLACK_BOT_TOKEN
SLACK_USER_TOKEN
SLACK_APP_TOKEN
```

Use a presence-only check equivalent to `printenv NAME >/dev/null 2>&1` and
report only the refused variable name. Any match stops the workflow.

## Agent-executed allowlist

Replace `TEAM_ID` and `APP_ID` only with previously verified exact Slack IDs.
Do not add, remove, or reorder behavior-changing flags.

```text
slack version --skip-update --no-color
slack auth list --skip-update --no-color
slack manifest info --source local --team TEAM_ID --skip-update --no-color
slack manifest validate --team TEAM_ID --skip-update --no-color
slack manifest diff --app APP_ID --team TEAM_ID --skip-update --no-color
```

`auth list` describes developer authorizations. It is not a Web API token
source and cannot select a runtime `slack-agent-cli` profile. `manifest diff`
is allowed only after the operator supplies the exact app ID; it does not
perform an update.

The separately confirmed dependency command is not a Slack command and may run
only in a canonical scaffold with an intact reviewed lockfile:

```text
npm ci --ignore-scripts --no-audit --no-fund
```

## Human-only handoffs

The agent may display these exact commands but must never invoke or capture
them:

```text
slack login --skip-update --no-color
slack app install --team TEAM_ID --environment local --skip-update --no-color
slack-agent-cli auth login --profile PROFILE --token-kind bot \
  --capability read [--capability message-write] --token-tty
```

The operator executes the `/slackauthticket` in the intended workspace and
enters challenge/token values directly in their terminal. The agent never asks
for those values.

## Forbidden surface

Never use `--token`, `--force`, `--verbose`, `--experiment`, a team name,
implicit/prompt-selected team or app, `slack api`, `run`, `deploy`, `env`,
`app settings`, `app delete`, `app uninstall`, `manifest update`, raw HTTP,
Slack MCP, an SDK, browser automation, arbitrary templates, or arbitrary hooks.
