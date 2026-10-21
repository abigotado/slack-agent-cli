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
	return bind(profileIdentity, workspaceID, operation, normalizedQuery, upstream, 1)
}

// BindThread marks transport-owned thread continuation state separately from legacy native cursors.
func BindThread(profileIdentity, workspaceID, operation, normalizedQuery, continuation string) (string, error) {
	return bind(profileIdentity, workspaceID, operation, normalizedQuery, continuation, 2)
}

func bind(profileIdentity, workspaceID, operation, normalizedQuery, upstream string, version int) (string, error) {
	if upstream == "" {
		return "", nil
	}
	value := payload{Version: version, ProfileID: profileIdentity, WorkspaceID: workspaceID, Operation: operation, QueryDigest: digest(normalizedQuery), Upstream: upstream}
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
	item, err := decode(value, profileIdentity, workspaceID, operation, normalizedQuery)
	if err != nil {
		return "", err
	}
	if item.Version != 1 {
		return "", errors.New("cursor version is invalid")
	}
	return item.Upstream, nil
}

// UnbindThread distinguishes a legacy native cursor from normalized thread state.
func UnbindThread(value, profileIdentity, workspaceID, operation, normalizedQuery string) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	item, err := decode(value, profileIdentity, workspaceID, operation, normalizedQuery)
	if err != nil {
		return "", false, err
	}
	if item.Version != 1 && item.Version != 2 {
		return "", false, errors.New("cursor version is invalid")
	}
	return item.Upstream, item.Version == 2, nil
}

func decode(value, profileIdentity, workspaceID, operation, normalizedQuery string) (payload, error) {
	if len(value) > contract.MaxCursorBytes {
		return payload{}, errors.New("cursor exceeds v1 bound")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return payload{}, errors.New("cursor is invalid")
	}
	var item payload
	if err := json.Unmarshal(decoded, &item); err != nil {
		return payload{}, errors.New("cursor is invalid")
	}
	if item.ProfileID != profileIdentity || item.WorkspaceID != workspaceID || item.Operation != operation || item.QueryDigest != digest(normalizedQuery) || item.Upstream == "" {
		return payload{}, errors.New("cursor does not belong to this profile and query")
	}
	return item, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
