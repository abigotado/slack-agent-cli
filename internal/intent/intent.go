// Package intent creates canonical one-shot message confirmation receipts.
package intent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/profile"
)

// Receipt is safe to show to an operator and deliberately omits message text.
type Receipt struct {
	Version         int    `json:"version"`
	Profile         string `json:"profile"`
	WorkspaceID     string `json:"workspace_id"`
	ConversationID  string `json:"conversation_id"`
	ThreadTS        string `json:"thread_ts,omitempty"`
	TextBytes       int    `json:"text_bytes"`
	TextRunes       int    `json:"text_runes"`
	TextSHA256      string `json:"text_sha256"`
	ProfileIdentity string `json:"profile_identity"`
	Generation      string `json:"generation"`
	IntentSHA256    string `json:"intent_sha256"`
	RemoteChecks    string `json:"remote_checks"`
	Applied         bool   `json:"applied"`
}

type canonical struct {
	Version         int    `json:"version"`
	Profile         string `json:"profile"`
	WorkspaceID     string `json:"workspace_id"`
	ConversationID  string `json:"conversation_id"`
	ThreadTS        string `json:"thread_ts"`
	TextBytes       int    `json:"text_bytes"`
	TextRunes       int    `json:"text_runes"`
	TextSHA256      string `json:"text_sha256"`
	ProfileIdentity string `json:"profile_identity"`
	Generation      string `json:"generation"`
}

// New validates bounded plain text and returns its exact receipt.
func New(p profile.Profile, conversationID, threadTS, text string) (Receipt, error) {
	if err := profile.ValidateSlackID(conversationID); err != nil {
		return Receipt{}, err
	}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || strings.ContainsRune(text, '\x00') {
		return Receipt{}, errors.New("message text must be non-empty valid UTF-8 without NUL")
	}
	bytesCount := len([]byte(text))
	runesCount := utf8.RuneCountInString(text)
	if bytesCount > contract.MaxMessageBytes || runesCount > contract.MaxMessageRunes {
		return Receipt{}, errors.New("message text exceeds the v1 bound")
	}
	textDigest := DigestText(text)
	value := canonical{
		Version: 1, Profile: p.Name, WorkspaceID: p.WorkspaceID,
		ConversationID: conversationID, ThreadTS: threadTS,
		TextBytes: bytesCount, TextRunes: runesCount, TextSHA256: textDigest,
		ProfileIdentity: profile.Identity(p), Generation: p.CredentialGeneration,
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return Receipt{}, err
	}
	sum := sha256.Sum256(payload)
	return Receipt{
		Version: value.Version, Profile: value.Profile, WorkspaceID: value.WorkspaceID,
		ConversationID: value.ConversationID, ThreadTS: value.ThreadTS,
		TextBytes: value.TextBytes, TextRunes: value.TextRunes,
		TextSHA256: value.TextSHA256, ProfileIdentity: value.ProfileIdentity,
		Generation: value.Generation, IntentSHA256: hex.EncodeToString(sum[:]),
		RemoteChecks: "not_performed", Applied: false,
	}, nil
}

// DigestText returns the canonical full-content digest used for reconciliation.
func DigestText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
