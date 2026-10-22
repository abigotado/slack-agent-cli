package cursor

import (
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

func TestBindingRejectsCrossProfileAndQuery(t *testing.T) {
	t.Parallel()
	value, err := Bind("work", "T1", "history", "limit=25", "upstream")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unbind(value, "work", "T1", "history", "limit=25")
	if err != nil || got != "upstream" {
		t.Fatalf("got %q %v", got, err)
	}
	tests := []struct{ profile, workspace, operation, query string }{{"other", "T1", "history", "limit=25"}, {"work", "T2", "history", "limit=25"}, {"work", "T1", "thread", "limit=25"}, {"work", "T1", "history", "limit=50"}}
	for _, test := range tests {
		if _, err := Unbind(value, test.profile, test.workspace, test.operation, test.query); err == nil {
			t.Fatalf("accepted mismatched context: %+v", test)
		}
	}
}

func TestThreadCursorVersionsAndContext(t *testing.T) {
	t.Parallel()
	// A legacy Slack cursor may look exactly like normalized transport state.
	upstream := `{"v":1,"upstream":"legacy","after":"2.0"}`
	for _, normalized := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "normalized"}[normalized], func(t *testing.T) {
			bind := Bind
			if normalized {
				bind = BindThread
			}
			value, err := bind("identity:generation", "T1", "thread", "channel=C1&ts=1.0&limit=50", upstream)
			if err != nil {
				t.Fatal(err)
			}
			got, domain, err := UnbindThread(value, "identity:generation", "T1", "thread", "channel=C1&ts=1.0&limit=50")
			if err != nil || got != upstream || domain != normalized {
				t.Fatalf("got=%q normalized=%t error=%v", got, domain, err)
			}
			if _, err := Unbind(value, "identity:generation", "T1", "thread", "channel=C1&ts=1.0&limit=50"); (err != nil) != normalized {
				t.Fatalf("generic domain error=%v", err)
			}
			for _, context := range []struct{ identity, workspace, operation, query string }{
				{"other:generation", "T1", "thread", "channel=C1&ts=1.0&limit=50"},
				{"identity:rotated", "T1", "thread", "channel=C1&ts=1.0&limit=50"},
				{"identity:generation", "T2", "thread", "channel=C1&ts=1.0&limit=50"},
				{"identity:generation", "T1", "history", "channel=C1&ts=1.0&limit=50"},
				{"identity:generation", "T1", "thread", "channel=C2&ts=1.0&limit=50"},
				{"identity:generation", "T1", "thread", "channel=C1&ts=1.0&limit=100"},
			} {
				if _, _, err := UnbindThread(value, context.identity, context.workspace, context.operation, context.query); err == nil {
					t.Fatalf("accepted changed context: %+v", context)
				}
			}
		})
	}
}

func TestThreadCursorFinalEncodedBound(t *testing.T) {
	t.Parallel()
	lastAccepted := 0
	for size := 1; size <= contract.MaxCursorBytes; size++ {
		upstream := strings.Repeat("x", size)
		value, err := BindThread("identity:generation", "T1", "thread", "limit=50", upstream)
		if err != nil {
			if value != "" || lastAccepted == 0 {
				t.Fatalf("unsafe bound failure: size=%d cursor=%q error=%v", size, value, err)
			}
			return
		}
		if len(value) > contract.MaxCursorBytes {
			t.Fatalf("emitted oversized cursor: %d", len(value))
		}
		got, normalized, err := UnbindThread(value, "identity:generation", "T1", "thread", "limit=50")
		if err != nil || !normalized || got != upstream {
			t.Fatalf("size=%d cannot round trip: %v", size, err)
		}
		lastAccepted = size
	}
	t.Fatal("accepted every payload without accounting for encoding overhead")
}
