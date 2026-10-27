package daemon

import "testing"

// TestInterruptedFromCausePolicy pins the archive policy of a re-derived
// driver: an empty or operator-shaped recovery cause archives nothing (a
// deliberate stop is not a model verdict); every other cause archives
// verbatim, booking the interrupted dispatch's wall time only for a
// system relaunch after a hang, stall or death.
func TestInterruptedFromCausePolicy(t *testing.T) {
	if got := interruptedFromCause(""); got != nil {
		t.Fatalf("an empty cause archives nothing, got %+v", got)
	}
	if got := interruptedFromCause("operator daemon shutdown"); got != nil {
		t.Fatalf("an operator-shaped cause archives nothing, got %+v", got)
	}
	for cause, book := range map[string]bool{"agent_death": true, "driver_stalled": true, "rate limit hit": true, "daemon_restart": false, "operator resume": false} {
		got := interruptedFromCause(cause)
		if got == nil || got.Detail != cause || got.BookWall != book {
			t.Fatalf("cause %q: archive %+v, want detail verbatim and BookWall %v", cause, got, book)
		}
	}
}
