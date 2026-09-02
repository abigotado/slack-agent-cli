// Package profile owns non-secret workspace/account identities.
package profile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

var (
	ErrNotFound = errors.New("profile not found")
	namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9]+$`)
)

// TokenKind describes the identity represented by a Slack Web API token.
type TokenKind string

const (
	TokenUser TokenKind = "user"
	TokenBot  TokenKind = "bot"
)

// Capability is a local allowlist of high-level operations.
type Capability string

const (
	CapabilityRead         Capability = "read"
	CapabilityMessageWrite Capability = "message-write"
)

// Profile binds one exact Slack workspace and authenticated account.
type Profile struct {
	Name                 string       `json:"name"`
	WorkspaceID          string       `json:"workspace_id"`
	WorkspaceName        string       `json:"workspace_name"`
	WorkspaceURL         string       `json:"workspace_url"`
	UserID               string       `json:"user_id"`
	BotID                string       `json:"bot_id,omitempty"`
	EnterpriseID         string       `json:"enterprise_id,omitempty"`
	TokenKind            TokenKind    `json:"token_kind"`
	Capabilities         []Capability `json:"capabilities"`
	CredentialGeneration string       `json:"credential_generation"`
}

// ValidateName validates a profile selector without consulting defaults.
func ValidateName(name string) error {
	if name == "" || len(name) > contract.MaxProfileNameBytes || !namePattern.MatchString(name) {
		return errors.New("profile name must contain 1-64 ASCII letters, digits, '.', '_', or '-'")
	}
	return nil
}

// ValidateSlackID validates the exact IDs accepted by the v1 contract.
func ValidateSlackID(id string) error {
	if id == "" || len(id) > contract.MaxSlackIDBytes || !idPattern.MatchString(id) {
		return errors.New("slack ID must contain 1-64 ASCII letters or digits")
	}
	return nil
}

// Validate checks profile identity and capability invariants.
func (p Profile) Validate() error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if err := ValidateSlackID(p.WorkspaceID); err != nil {
		return err
	}
	if err := ValidateSlackID(p.UserID); err != nil {
		return err
	}
	if p.BotID != "" {
		if err := ValidateSlackID(p.BotID); err != nil {
			return err
		}
	}
	if p.EnterpriseID != "" {
		if err := ValidateSlackID(p.EnterpriseID); err != nil {
			return err
		}
	}
	if p.WorkspaceName == "" || p.WorkspaceURL == "" {
		return errors.New("workspace name and URL are required")
	}
	if len(p.WorkspaceName) > 256 || strings.IndexFunc(p.WorkspaceName, unicode.IsControl) >= 0 {
		return errors.New("workspace name is invalid")
	}
	parsedURL, err := url.Parse(p.WorkspaceURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Path != "" && parsedURL.Path != "/") || !(parsedURL.Hostname() == "slack.com" || strings.HasSuffix(parsedURL.Hostname(), ".slack.com")) {
		return errors.New("workspace URL is not an exact Slack HTTPS URL")
	}
	if p.TokenKind != TokenUser && p.TokenKind != TokenBot {
		return errors.New("token kind must be user or bot")
	}
	if p.TokenKind == TokenBot && p.BotID == "" {
		return errors.New("bot token requires bot identity")
	}
	if p.TokenKind == TokenUser && p.BotID != "" {
		return errors.New("user token cannot contain bot identity")
	}
	if !slices.Contains(p.Capabilities, CapabilityRead) {
		return errors.New("read capability is required")
	}
	seenCapabilities := map[Capability]bool{}
	for _, capability := range p.Capabilities {
		if capability != CapabilityRead && capability != CapabilityMessageWrite {
			return errors.New("unknown capability")
		}
		if seenCapabilities[capability] {
			return errors.New("duplicate capability")
		}
		seenCapabilities[capability] = true
	}
	if p.CredentialGeneration == "" {
		return errors.New("credential generation is required")
	}
	return nil
}

// NewGeneration returns an unpredictable non-secret generation identifier.
func NewGeneration() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// Identity returns a stable digest of the complete non-secret binding.
func Identity(p Profile) string {
	clone := p
	clone.Capabilities = append([]Capability(nil), p.Capabilities...)
	slices.Sort(clone.Capabilities)
	payload, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// HasCapability reports whether a capability was explicitly declared.
func (p Profile) HasCapability(capability Capability) bool {
	return slices.Contains(p.Capabilities, capability)
}

// NormalizeURL removes only a trailing slash from verified Slack workspace URL.
func NormalizeURL(value string) string { return strings.TrimSuffix(value, "/") }
