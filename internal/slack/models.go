// Package slack is the fixed-route typed Slack Web API boundary.
package slack

// Token is intentionally opaque and must never be formatted into diagnostics.
type Token string

// Identity is the projected result of auth.test.
type Identity struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	WorkspaceURL  string `json:"workspace_url"`
	UserID        string `json:"user_id"`
	BotID         string `json:"bot_id,omitempty"`
	EnterpriseID  string `json:"enterprise_id,omitempty"`
}

// TextField is a minimal untrusted Slack topic/purpose projection.
type TextField struct {
	Value string `json:"value"`
}

// Conversation is the minimal typed discovery/detail projection.
type Conversation struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	User        string    `json:"user,omitempty"`
	IsChannel   bool      `json:"is_channel"`
	IsIM        bool      `json:"is_im"`
	IsMPIM      bool      `json:"is_mpim"`
	IsPrivate   bool      `json:"is_private"`
	IsArchived  bool      `json:"is_archived"`
	IsShared    *bool     `json:"is_shared"`
	IsExtShared *bool     `json:"is_ext_shared"`
	IsOrgShared *bool     `json:"is_org_shared"`
	Topic       TextField `json:"topic"`
	Purpose     TextField `json:"purpose"`
}

// SharedStatus is a fail-closed target classification.
type SharedStatus struct {
	Shared           bool `json:"shared"`
	ExternallyShared bool `json:"externally_shared"`
	OrgShared        bool `json:"org_shared"`
}

// ClassifyShared requires Slack to return all classification fields.
func (c Conversation) ClassifyShared() (SharedStatus, bool) {
	if c.IsShared == nil || c.IsExtShared == nil || c.IsOrgShared == nil {
		return SharedStatus{}, false
	}
	return SharedStatus{Shared: *c.IsShared, ExternallyShared: *c.IsExtShared, OrgShared: *c.IsOrgShared}, true
}

// ConversationPage is one bounded discovery page.
type ConversationPage struct {
	Conversations []ConversationSummary `json:"conversations"`
	NextCursor    string                `json:"next_cursor,omitempty"`
}

// ConversationSummary deliberately excludes topic and purpose content.
type ConversationSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	User        string `json:"user,omitempty"`
	IsChannel   bool   `json:"is_channel"`
	IsIM        bool   `json:"is_im"`
	IsMPIM      bool   `json:"is_mpim"`
	IsPrivate   bool   `json:"is_private"`
	IsArchived  bool   `json:"is_archived"`
	IsShared    bool   `json:"is_shared"`
	IsExtShared bool   `json:"is_ext_shared"`
	IsOrgShared bool   `json:"is_org_shared"`
}

// Message is a minimal untrusted message projection.
type Message struct {
	Type       string `json:"type"`
	User       string `json:"user,omitempty"`
	BotID      string `json:"bot_id,omitempty"`
	Text       string `json:"text"`
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
}

// MessagePage is one bounded history or thread page.
type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// User is a minimal untrusted identity projection.
type User struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id"`
	Name        string `json:"name"`
	RealName    string `json:"real_name"`
	DisplayName string `json:"display_name"`
	Deleted     bool   `json:"deleted"`
	IsBot       bool   `json:"is_bot"`
	IsStranger  bool   `json:"-"`
}

// ListOptions defines bounded conversation discovery.
type ListOptions struct {
	Types  string
	Limit  int
	Cursor string
}

// HistoryOptions defines a bounded exact-conversation history read.
type HistoryOptions struct {
	ConversationID string
	Limit          int
	Cursor         string
	Oldest         string
	Latest         string
}

// ThreadOptions defines a bounded exact thread read.
type ThreadOptions struct {
	ConversationID string
	ThreadTS       string
	Limit          int
	Cursor         string
}

// PostOptions is the only v1 Slack write operation.
type PostOptions struct {
	ConversationID string
	ThreadTS       string
	Text           string
}

// PostResult is a minimal confirmed message identity; it excludes echoed text.
type PostResult struct {
	ConversationID string `json:"conversation_id"`
	TS             string `json:"ts"`
	ThreadTS       string `json:"thread_ts,omitempty"`
}
