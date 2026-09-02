# Changelog

All notable changes to this project are documented in this file.

## [0.2.1] - 2026-09-02

- Republish the v0.2 feature set with the complete immutable source release
  bundle after the initial v0.2.0 GitHub release was removed before assets were
  attached. Runtime behavior is unchanged from v0.2.0.

## [0.2.0] - 2026-09-02

- Add bounded hidden controlling-terminal token input with `auth login
  --token-tty`.
- Add a separately installable Slack app-provisioning Skill for Codex and
  Claude Code with pinned official Slack CLI scaffolding and exact public plus
  private channel manifests.

## [0.1.0] - 2026-09-02

- Add explicit multi-profile Slack authentication backed by macOS Keychain.
- Add bounded typed conversation, message, thread, user, and identity reads.
- Add identity-bound read/write conversation allowlists.
- Add one-shot plain-text sends with local intent receipts and recovery-aware
  exit codes.
- Add a stable v1 JSON envelope and one embedded Agent Skill for Codex and
  Claude Code.

[0.1.0]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.1.0
[0.2.0]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.2.0
[0.2.1]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.2.1
