// Package assets embeds the canonical provider-neutral Slack Agent Skill.
package assets

import "embed"

// Skill contains the exact same installable bytes for Codex and Claude Code.
//
//go:embed skills/slack
var Skill embed.FS
