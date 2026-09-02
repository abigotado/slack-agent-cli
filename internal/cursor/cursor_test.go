package cursor

import "testing"

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
