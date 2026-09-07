package contract

import (
	"slices"
	"testing"
)

func TestV1FrozenValues(t *testing.T) {
	t.Parallel()
	limits := V1Limits()
	if EnvelopeVersion != 1 || limits.TokenBytes != 8192 || limits.MessageRunes != 4000 || limits.MessageBytes != 16384 || limits.ResponseBytes != 8<<20 || limits.CollectionLimitMin != 1 || limits.CollectionLimitMax != 100 {
		t.Fatalf("v1 limits drifted: %+v", limits)
	}
	want := []string{"auth.test", "chat.postMessage", "conversations.history", "conversations.info", "conversations.list", "conversations.replies", "files.info", "users.info"}
	if !slices.Equal(Routes(), want) {
		t.Fatalf("route contract drifted: %v", Routes())
	}
	routes := Routes()
	routes[0] = "mutated"
	if Routes()[0] == "mutated" {
		t.Fatal("Routes exposed mutable global state")
	}
}
