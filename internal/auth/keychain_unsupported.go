//go:build !darwin || !cgo

package auth

import (
	"context"
	"errors"
)

// KeychainStore is unavailable outside macOS cgo builds in v0.1.
type KeychainStore struct{}

func (KeychainStore) Load(context.Context, string) (Credential, error) {
	return Credential{}, errors.New("macOS Keychain is unavailable")
}
func (KeychainStore) Save(context.Context, string, Credential) error {
	return errors.New("macOS Keychain is unavailable")
}
func (KeychainStore) Delete(context.Context, string) error {
	return errors.New("macOS Keychain is unavailable")
}
