package cli

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/abigotado/slack-agent-cli/internal/profile"
)

var (
	workflowIDPattern        = regexp.MustCompile(`^[CGD][A-Z0-9]+$`)
	workflowTeamPattern      = regexp.MustCompile(`^T[A-Z0-9]+$`)
	workflowHostPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.slack\.com$`)
	workflowTimePattern      = regexp.MustCompile(`^[0-9]{10}\.[0-9]{6}$`)
	workflowPermalinkPattern = regexp.MustCompile(`^p[0-9]{16}$`)
)

type workflowTarget struct {
	ConversationID string `json:"conversation_id"`
	ThreadTS       string `json:"thread_ts,omitempty"`
	WorkspaceHost  string `json:"workspace_host,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
}

// Only the caller's exact selector is parsed; Slack-returned content never enters here.
func parseWorkflowTarget(raw, thread string) (workflowTarget, error) {
	invalid := func() (workflowTarget, error) {
		return workflowTarget{}, usageError("INVALID_WORKFLOW_TARGET", "target must be one exact conversation ID or supported Slack HTTPS link", nil)
	}
	if len(raw) == 0 || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return invalid()
	}
	target := workflowTarget{}
	if workflowIDPattern.MatchString(raw) && profile.ValidateSlackID(raw) == nil {
		target.ConversationID = raw
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || strings.Contains(raw, "#") || u.Host != u.Hostname() || u.RawPath != "" || strings.Contains(u.Path, "%") {
			return invalid()
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		switch {
		case u.Host == "app.slack.com":
			if len(parts) != 3 || parts[0] != "client" || !workflowTeamPattern.MatchString(parts[1]) || u.RawQuery != "" || u.ForceQuery {
				return invalid()
			}
			target.WorkspaceID, target.ConversationID = parts[1], parts[2]
		case workflowHostPattern.MatchString(u.Host):
			if (len(parts) != 2 && len(parts) != 3) || parts[0] != "archives" {
				return invalid()
			}
			target.WorkspaceHost, target.ConversationID = u.Host, parts[1]
			if len(parts) == 3 {
				if !workflowPermalinkPattern.MatchString(parts[2]) {
					return invalid()
				}
				target.ThreadTS = parts[2][1:11] + "." + parts[2][11:]
			}
			query, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				return invalid()
			}
			for key, values := range query {
				if len(values) != 1 {
					return invalid()
				}
				switch key {
				case "cid":
					if values[0] != target.ConversationID {
						return invalid()
					}
				case "thread_ts":
					if target.ThreadTS == "" || !workflowTimePattern.MatchString(values[0]) {
						return invalid()
					}
					target.ThreadTS = values[0]
				default:
					return invalid()
				}
			}
		default:
			return invalid()
		}
		if !workflowIDPattern.MatchString(target.ConversationID) || profile.ValidateSlackID(target.ConversationID) != nil || (target.WorkspaceID != "" && profile.ValidateSlackID(target.WorkspaceID) != nil) {
			return invalid()
		}
	}
	if thread != "" {
		if !workflowTimePattern.MatchString(thread) || (target.ThreadTS != "" && target.ThreadTS != thread) {
			return invalid()
		}
		target.ThreadTS = thread
	}
	return target, nil
}

func (target workflowTarget) matches(p profile.Profile) bool {
	if target.WorkspaceID != "" && target.WorkspaceID != p.WorkspaceID {
		return false
	}
	if target.WorkspaceHost != "" {
		u, err := url.Parse(p.WorkspaceURL)
		return err == nil && u.Host == target.WorkspaceHost
	}
	return true
}
