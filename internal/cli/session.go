package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/abigotado/slack-agent-cli/internal/auth"
	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

var timestampPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

type session struct {
	profile profile.Profile
	token   slack.Token
}

func loadSession(ctx context.Context, dependencies Dependencies, name string, capability profile.Capability) (session, error) {
	if err := requireProfile(name); err != nil {
		return session{}, err
	}
	value, err := dependencies.Profiles.Get(ctx, name)
	if err != nil {
		return session{}, err
	}
	if !value.HasCapability(capability) {
		return session{}, errxPermission("CAPABILITY_NOT_DECLARED", "profile does not declare the required capability")
	}
	credential, err := auth.LoadBound(ctx, dependencies.Credentials, value)
	if err != nil {
		return session{}, err
	}
	return session{profile: value, token: slack.Token(credential.Token)}, nil
}

func preflightTarget(ctx context.Context, dependencies Dependencies, current session, conversationID string, kind policy.Kind) (slack.Conversation, slack.SharedStatus, error) {
	if err := profile.ValidateSlackID(conversationID); err != nil {
		return slack.Conversation{}, slack.SharedStatus{}, usageError("INVALID_CONVERSATION_ID", "conversation ID is invalid", err)
	}
	conversation, err := dependencies.Conversations.ConversationInfo(ctx, current.token, conversationID)
	if err != nil {
		return slack.Conversation{}, slack.SharedStatus{}, err
	}
	if conversation.ID != conversationID {
		return slack.Conversation{}, slack.SharedStatus{}, errors.New("slack returned a different conversation identity")
	}
	shared, known := conversation.ClassifyShared()
	if !known {
		return slack.Conversation{}, slack.SharedStatus{}, errxPermission("TARGET_SHARED_STATE_UNKNOWN", "Slack did not provide complete shared-state fields")
	}
	if err := dependencies.Policies.Require(ctx, current.profile, kind, conversationID, shared); err != nil {
		return slack.Conversation{}, slack.SharedStatus{}, err
	}
	return conversation, shared, nil
}

func validateLimit(limit int) error {
	if limit < contract.MinCollectionLimit || limit > contract.MaxCollectionLimit {
		return usageError("INVALID_LIMIT", "--limit must be between 1 and 100", nil)
	}
	return nil
}
func validateTimestamp(value, label string) error {
	if value == "" || len(value) > contract.MaxSlackTimestampBytes || !timestampPattern.MatchString(value) {
		return usageError("INVALID_TIMESTAMP", fmt.Sprintf("%s is invalid", label), nil)
	}
	return nil
}
func errxPermission(code, message string) error {
	return errx.New(errx.PermissionDenied, code, message, "request explicit access; do not broaden automatically")
}

func normalizeTypes(value string) (string, error) {
	allowed := map[string]bool{"public_channel": true, "private_channel": true, "im": true, "mpim": true}
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return "", usageError("INVALID_TYPES", "--types is required", nil)
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !allowed[part] || seen[part] {
			return "", usageError("INVALID_TYPES", "--types must be unique supported conversation types", nil)
		}
		seen[part] = true
		normalized = append(normalized, part)
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ","), nil
}
