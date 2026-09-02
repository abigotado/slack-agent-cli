package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/abigotado/slack-agent-cli/internal/profile"
)

var ErrOverwriteConfirmationRequired = errors.New("profile overwrite requires confirmation")

const compensationTimeout = 3 * time.Second

// Login publishes a verified profile and bound credential transactionally.
func Login(ctx context.Context, store Store, registry *profile.Registry, verified profile.Profile, token string, overwriteConfirmed bool) (profile.Profile, error) {
	if store == nil || registry == nil {
		return profile.Profile{}, errors.New("login dependencies are required")
	}
	if verified.CredentialGeneration != "" {
		return profile.Profile{}, errors.New("verified profile must not contain a generation")
	}
	generation, err := profile.NewGeneration()
	if err != nil {
		return profile.Profile{}, fmt.Errorf("create credential generation: %w", err)
	}
	verified.CredentialGeneration = generation
	if err := verified.Validate(); err != nil {
		return profile.Profile{}, err
	}
	credential := Credential{Version: 1, Token: token, ProfileIdentity: profile.Identity(verified), Generation: generation, Capabilities: append([]profile.Capability(nil), verified.Capabilities...)}

	var previous Credential
	var previousExists, credentialChanged bool
	err = registry.TransactWithRollback(ctx, func(transaction *profile.Transaction) error {
		_, profileErr := transaction.Get(verified.Name)
		profileExists := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, profile.ErrNotFound) {
			return profileErr
		}
		previous, err = store.Load(ctx, verified.Name)
		previousExists = err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("load existing credential: %w", err)
		}
		if (profileExists || previousExists) && !overwriteConfirmed {
			return ErrOverwriteConfirmationRequired
		}
		if err := store.Save(ctx, verified.Name, credential); err != nil {
			return fmt.Errorf("save credential: %w", err)
		}
		credentialChanged = true
		return transaction.Put(verified)
	}, func() error {
		if !credentialChanged {
			return nil
		}
		compensationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
		defer cancel()
		return rollback(compensationContext, store, verified.Name, previous, previousExists)
	})
	if err == nil || profile.WasCommitted(err) {
		return verified, err
	}
	return profile.Profile{}, err
}

// Logout deletes the Keychain item before removing public metadata and restores
// it if the metadata commit fails before becoming visible.
func Logout(ctx context.Context, store Store, registry *profile.Registry, name string) error {
	if store == nil || registry == nil {
		return errors.New("logout dependencies are required")
	}
	var previous Credential
	var previousExists, credentialChanged bool
	err := registry.TransactWithRollback(ctx, func(transaction *profile.Transaction) error {
		var err error
		previous, err = store.Load(ctx, name)
		previousExists = err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("load credential: %w", err)
		}
		if err := store.Delete(ctx, name); err != nil {
			return fmt.Errorf("delete credential: %w", err)
		}
		credentialChanged = previousExists
		if err := transaction.Remove(name); errors.Is(err, profile.ErrNotFound) {
			return nil
		} else {
			return err
		}
	}, func() error {
		if !credentialChanged {
			return nil
		}
		compensationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
		defer cancel()
		return store.Save(compensationContext, name, previous)
	})
	if err == nil || profile.WasCommitted(err) {
		return err
	}
	return err
}

// LoadBound loads an exact credential and verifies identity/generation.
func LoadBound(ctx context.Context, store Store, value profile.Profile) (Credential, error) {
	credential, err := store.Load(ctx, value.Name)
	if err != nil {
		return Credential{}, err
	}
	if err := credential.ValidateBinding(value); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func rollback(ctx context.Context, store Store, name string, previous Credential, restore bool) error {
	if restore {
		return store.Save(ctx, name, previous)
	}
	return store.Delete(ctx, name)
}
