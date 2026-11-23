package liveconfig

// autoanswer_test.go: the automatic reply's live values (polyforge#239):
// the switch and timeout are swapped whole by a reload and read by the
// next judgement.

import (
	"testing"
	"time"
)

func TestAutoAnswerLiveValues(t *testing.T) {
	s := New(nil, "", 0, 0)
	if s.AskAutoReplyEnabled() != nil || s.AskAutoReplyTimeout() != 0 {
		t.Fatalf("new state = %v / %v, want nil / 0", s.AskAutoReplyEnabled(), s.AskAutoReplyTimeout())
	}
	off := false
	s.SetAskAutoReplyEnabled(&off)
	s.SetAskAutoReplyTimeout(45 * time.Minute)
	if v := s.AskAutoReplyEnabled(); v == nil || *v {
		t.Fatalf("enabled = %v, want a false pointer", v)
	}
	if got := s.AskAutoReplyTimeout(); got != 45*time.Minute {
		t.Fatalf("timeout = %v, want 45m", got)
	}
	s.SetAskAutoReplyEnabled(nil)
	if s.AskAutoReplyEnabled() != nil {
		t.Fatal("unset switch did not read back as nil")
	}
}
