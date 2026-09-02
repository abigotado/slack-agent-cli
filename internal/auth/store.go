// Package auth owns bounded secret input and credential persistence.
package auth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/profile"
)

var (
	ErrNotFound              = errors.New("credential not found")
	ErrInteractionNotAllowed = errors.New("keychain interaction not allowed")
	ErrBindingMismatch       = errors.New("credential binding does not match profile")
)

const KeychainService = "com.abigotado.slack-agent-cli"

// Credential is the versioned Keychain payload.
type Credential struct {
	Version         int                  `json:"version"`
	Token           string               `json:"token"`
	ProfileIdentity string               `json:"profile_identity"`
	Generation      string               `json:"generation"`
	Capabilities    []profile.Capability `json:"capabilities"`
}

// Store is the secret persistence boundary.
type Store interface {
	Load(context.Context, string) (Credential, error)
	Save(context.Context, string, Credential) error
	Delete(context.Context, string) error
}

// ValidateBinding fails closed on profile/Keychain drift.
func (c Credential) ValidateBinding(p profile.Profile) error {
	if c.Version != 1 || c.Token == "" || c.ProfileIdentity != profile.Identity(p) || c.Generation != p.CredentialGeneration {
		return ErrBindingMismatch
	}
	credentialCapabilities := append([]profile.Capability(nil), c.Capabilities...)
	profileCapabilities := append([]profile.Capability(nil), p.Capabilities...)
	sort.Slice(credentialCapabilities, func(i, j int) bool { return credentialCapabilities[i] < credentialCapabilities[j] })
	sort.Slice(profileCapabilities, func(i, j int) bool { return profileCapabilities[i] < profileCapabilities[j] })
	if !slices.Equal(credentialCapabilities, profileCapabilities) {
		return ErrBindingMismatch
	}
	return nil
}

// ReadToken reads exactly one bounded non-empty token line.
func ReadToken(reader io.Reader) (string, error) {
	limited := io.LimitReader(reader, contract.MaxTokenBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if len(payload) == 0 || len(payload) > contract.MaxTokenBytes {
		return "", errors.New("token input is empty or exceeds 8 KiB")
	}
	payload = bytes.TrimSuffix(payload, []byte{'\n'})
	payload = bytes.TrimSuffix(payload, []byte{'\r'})
	if len(payload) == 0 || bytes.ContainsAny(payload, "\r\n\x00") {
		return "", errors.New("token input must be exactly one non-empty line")
	}
	if !utf8.Valid(payload) {
		return "", errors.New("token input must be valid UTF-8")
	}
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Split(bufio.ScanRunes)
	for scanner.Scan() {
		r, _ := utf8.DecodeRune(scanner.Bytes())
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("token input contains whitespace or control characters")
		}
	}
	return string(payload), nil
}

func encodeCredential(value Credential) ([]byte, error) {
	if value.Version != 1 || value.Token == "" || value.ProfileIdentity == "" || value.Generation == "" {
		return nil, errors.New("credential is invalid")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(payload) > contract.MaxTokenBytes*2 {
		return nil, errors.New("credential payload exceeds bound")
	}
	return payload, nil
}

func decodeCredential(payload []byte) (Credential, error) {
	if len(payload) == 0 || len(payload) > contract.MaxTokenBytes*2 {
		return Credential{}, errors.New("stored credential is invalid")
	}
	var value Credential
	if err := json.Unmarshal(payload, &value); err != nil {
		return Credential{}, errors.New("stored credential is invalid")
	}
	if value.Version != 1 || value.Token == "" {
		return Credential{}, errors.New("stored credential is invalid")
	}
	return value, nil
}
