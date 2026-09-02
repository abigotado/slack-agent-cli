// Package assets embeds the canonical provider-neutral Slack Agent Skills.
package assets

import "embed"

// Skills contains the exact same installable bytes for Codex and Claude Code.
//
//go:embed all:skills/slack all:skills/slack-app-provisioning
var Skills embed.FS
