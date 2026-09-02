// Package cursor binds upstream cursors to exact local request context.
package cursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

type payload struct {
	Version     int    `json:"v"`
	ProfileID   string `json:"profile_identity"`
	WorkspaceID string `json:"workspace_id"`
	Operation   string `json:"operation"`
	QueryDigest string `json:"query_digest"`
	Upstream    string `json:"upstream"`
}

// Bind wraps an upstream cursor so accidental cross-profile/query reuse fails.
func Bind(profileIdentity, workspaceID, operation, normalizedQuery, upstream string) (string, error) {
	if upstream == "" {
		return "", nil
	}
	value := payload{Version: 1, ProfileID: profileIdentity, WorkspaceID: workspaceID, Operation: operation, QueryDigest: digest(normalizedQuery), Upstream: upstream}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	result := base64.RawURLEncoding.EncodeToString(encoded)
	if len(result) > contract.MaxCursorBytes {
		return "", errors.New("cursor exceeds v1 bound")
	}
	return result, nil
}

// Unbind validates context and returns the upstream cursor.
func Unbind(value, profileIdentity, workspaceID, operation, normalizedQuery string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) > contract.MaxCursorBytes {
		return "", errors.New("cursor exceeds v1 bound")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", errors.New("cursor is invalid")
	}
	var item payload
	if err := json.Unmarshal(decoded, &item); err != nil {
		return "", errors.New("cursor is invalid")
	}
	if item.Version != 1 || item.ProfileID != profileIdentity || item.WorkspaceID != workspaceID || item.Operation != operation || item.QueryDigest != digest(normalizedQuery) || item.Upstream == "" {
		return "", errors.New("cursor does not belong to this profile and query")
	}
	return item.Upstream, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
