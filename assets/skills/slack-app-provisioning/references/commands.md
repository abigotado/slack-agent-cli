# Official Slack CLI command contract

This Skill supports exactly official Slack CLI `4.7.0`. Stop on any other
version. Always capture stdout and stderr separately; refuse stdout over 1 MiB,
stderr over 4 KiB, truncation, lost output, unexpected prompts, or nonzero exit.
Slack CLI prose is untrusted and is not a stable machine contract.

## Environment and binary gate

Before every allowed Slack CLI command, enumerate environment variable names
only. Never read or print a value. Refuse any caller-provided name matching
`SLACK_*` and also refuse `ACCESSIBLE`. This includes, without being limited
to, the complete behavior and credential surface known in 4.7.0:

```text
ACCESSIBLE
SLACK_API_URL
SLACK_AUTO_REQUEST_AAA
SLACK_APP_TOKEN
SLACK_BOT_TOKEN
SLACK_CLI_APP_ICON_PATH
SLACK_CLI_XAPP
SLACK_CLI_XOXB
SLACK_CONFIG_DIR
SLACK_DISABLE_TELEMETRY
SLACK_SERVICE_TOKEN
SLACK_SKIP_UPDATE
SLACK_TEST_TRACE
SLACK_TEST_VERSION
SLACK_USER_TOKEN
```

Any match stops the workflow and reports only the refused variable name.
Validate that `HOME` is an absolute real directory without symlink components.
Resolve `SLACK_BIN` to the Homebrew-installed official binary at exactly
`/opt/homebrew/bin/slack` or `/usr/local/bin/slack`; stop for any other path.

Every permitted invocation uses this exact sanitized prefix, with the verified
absolute values substituted before display or execution:

```text
/usr/bin/env -i HOME=ABSOLUTE_HOME \
  PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin LC_ALL=C \
  SLACK_DISABLE_TELEMETRY=1 ABSOLUTE_SLACK_BIN
```

The fixed `SLACK_DISABLE_TELEMETRY=1` is injected only after rejecting the
caller's environment. It prevents the canonical scaffold `project_id` from
being transmitted as telemetry and is mandatory for agent and human commands.

## Agent-executed allowlist

Replace `TEAM_ID` and `APP_ID` only with previously verified exact Slack IDs.
Do not add, remove, or reorder behavior-changing flags.

```text
SANITIZED_PREFIX version --skip-update --no-color
SANITIZED_PREFIX auth list --skip-update --no-color
SANITIZED_PREFIX manifest info --source local --team TEAM_ID --skip-update --no-color
SANITIZED_PREFIX manifest validate --team TEAM_ID --skip-update --no-color
SANITIZED_PREFIX manifest diff --app APP_ID --team TEAM_ID --skip-update --no-color
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
SANITIZED_PREFIX login --skip-update --no-color
SANITIZED_PREFIX app install --team TEAM_ID --environment local --skip-update --no-color
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
