package writestate

import "testing"

func TestTrackerIsMonotonicAndNilSafe(t *testing.T) {
	t.Parallel()
	var absent *Tracker
	absent.MarkStarted()
	absent.MarkConfirmed()
	if absent.State() != NotStarted {
		t.Fatal("nil tracker changed state")
	}

	tracker := &Tracker{}
	tracker.MarkStarted()
	tracker.MarkStarted()
	if tracker.State() != Started {
		t.Fatalf("state %v", tracker.State())
	}
	tracker.MarkConfirmed()
	tracker.MarkStarted()
	if tracker.State() != Confirmed {
		t.Fatalf("state regressed to %v", tracker.State())
	}
}
