package intent

import (
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/profile"
)

func testProfile() profile.Profile {
	return profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite}, CredentialGeneration: "g1"}
}

func TestReceiptIsStableAndOmitsText(t *testing.T) {
	t.Parallel()
	first, err := New(testProfile(), "C1", "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(testProfile(), "C1", "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if first.IntentSHA256 != second.IntentSHA256 || first.TextSHA256 != DigestText("hello") {
		t.Fatalf("unstable receipt: %+v %+v", first, second)
	}
	changed, err := New(testProfile(), "C1", "", "hello!")
	if err != nil {
		t.Fatal(err)
	}
	if changed.IntentSHA256 == first.IntentSHA256 {
		t.Fatal("text change did not change intent")
	}
}

func TestMessageBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, text string
		valid      bool
	}{
		{"valid multiline", "hello\nworld", true},
		{"empty", "  \n", false},
		{"nul", "a\x00b", false},
		{"runes", strings.Repeat("é", 4001), false},
		{"bytes", strings.Repeat("😀", 4000) + strings.Repeat("a", 385), false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := New(testProfile(), "C1", "", test.text)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}
