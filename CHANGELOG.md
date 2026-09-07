# Changelog

All notable changes to this project are documented in this file.

## [0.3.0] - 2026-09-07

- Add typed file attachment metadata to message history and thread results.
- Add `files get` and `files download` with exact-message verification behind
  the selected profile's read allowlist.
- Stream hosted Slack files up to 250 MiB to new local files, with a two-minute
  deadline, strict download-origin checks, no redirects, and SHA-256 output.
- Add `files:read` to provisioning manifests and document reauthorization,
  credential reimport, and explicit read/write policy migration.

## [0.2.3] - 2026-09-04

- Add a canonical dedicated user-only Slack app variant for allowlisted,
  read-only access to one-to-one direct messages.
- Correct both embedded Skills so `auth login --token-kind user` is described
  as credential import, not token issuance or permission grant.
- Accept Slack's documented sparse direct-message shape while revalidating the
  exact other participant's workspace before every allowlisted content read.
- Add a fail-closed human acceptance gate for Slack CLI 4.7.0's undocumented
  user-only installation path.

## [0.2.2] - 2026-09-02

- Fix successful `auth.test` decoding for Slack's documented string-valued
  `user` field so valid bot and user tokens can be saved as named profiles.
- Add fixed, non-sensitive failure stages to the v1 error envelope while
  preserving existing error codes and recovery-oriented exit codes.
- Enforce the same bounded printable-ASCII token input contract for hidden TTY
  and stdin login paths.

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
[0.2.2]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.2.2
[0.2.3]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.2.3

[0.3.0]: https://github.com/abigotado/slack-agent-cli/releases/tag/v0.3.0
