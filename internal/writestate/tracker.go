// Package writestate tracks the one process-local message dispatch boundary.
package writestate

import "sync/atomic"

// State is monotonic for one CLI invocation.
type State uint32

const (
	NotStarted State = iota
	Started
	Confirmed
)

// Tracker is safe for transport callbacks and root panic recovery.
type Tracker struct{ state atomic.Uint32 }

// MarkStarted records that the HTTP dispatch boundary is being crossed.
func (t *Tracker) MarkStarted() {
	if t != nil {
		t.state.CompareAndSwap(uint32(NotStarted), uint32(Started))
	}
}

// MarkConfirmed records that Slack success or exact reconciliation was proven.
func (t *Tracker) MarkConfirmed() {
	if t != nil {
		t.state.Store(uint32(Confirmed))
	}
}

// State returns the current monotonic dispatch state.
func (t *Tracker) State() State {
	if t == nil {
		return NotStarted
	}
	return State(t.state.Load())
}
