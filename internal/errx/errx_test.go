package errx

import (
	"errors"
	"slices"
	"testing"
)

func TestProcessExitContract(t *testing.T) {
	t.Parallel()
	var got []Exit
	for _, item := range Codes() {
		got = append(got, item.Exit)
	}
	if want := []Exit{OK, Internal, Usage, NotFound, Ambiguous, ConfirmationRequired, PermissionDenied, Conflict}; !slices.Equal(got, want) {
		t.Fatalf("process exits=%v want=%v", got, want)
	}
	codes := Codes()
	codes[0].Name = "mutated"
	if Codes()[0].Name != "OK" {
		t.Fatal("Codes exposed mutable state")
	}
}

func TestUnknownErrorIsSafeInternal(t *testing.T) {
	t.Parallel()
	secret := errors.New("xoxb-secret")
	got := As(secret)
	if got.Exit != Internal || got.Code != "INTERNAL" || got.Message == secret.Error() {
		t.Fatalf("unsafe conversion: %+v", got)
	}
	if !errors.Is(got, secret) {
		t.Fatal("diagnostic cause was not retained")
	}
}
