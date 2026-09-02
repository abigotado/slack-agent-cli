package errx

import (
	"errors"
	"testing"
)

func TestExitContractIsStable(t *testing.T) {
	t.Parallel()
	codes := Codes()
	if len(codes) != 10 {
		t.Fatalf("got %d exits", len(codes))
	}
	for index, item := range codes {
		if int(item.Exit) != index {
			t.Fatalf("exit %d moved to %d", index, item.Exit)
		}
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
