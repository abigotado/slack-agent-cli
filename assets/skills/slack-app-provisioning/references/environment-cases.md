# Environment adversarial cases

The environment gate must produce these exact outcomes before Slack CLI runs.
Never inspect credential contents while evaluating them.

| Condition | Required outcome |
| --- | --- |
| Caller `HOME` names another existing, current-user-owned directory | Refuse because it differs from `getpwuid(getuid())` |
| Canonical home has a symlink component | Refuse |
| Canonical home or existing `.slack` is group- or world-writable | Refuse |
| Caller defines `SLACK_TEST_VERSION`, `SLACK_CONFIG_DIR`, `SLACK_API_URL`, `SLACK_CLI_XAPP`, or any other `SLACK_*` name | Refuse and report the name only |
| Canonical home contains spaces or shell metacharacters such as `$()`, semicolon, backtick, or backslash | Preserve it as one argv element; never evaluate it |
| Canonical home contains a single quote | Encode the human command with the POSIX `'"'"'` sequence |
| Execution tool accepts only a shell command string | Do not run an agent command; emit the fully quoted human handoff and stop |

The canonical current user's ordinary home passes only when its owner is the
current numeric UID, it is not group- or world-writable, caller `HOME` is an
exact match, and an existing `.slack` directory passes the same checks.
