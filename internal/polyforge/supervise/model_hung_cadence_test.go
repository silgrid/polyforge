package supervise

// model_hung_cadence_test.go: the model_hung latency question. A pi
// dispatch whose model stops streaming is judged model_hung on the first
// supervisor tick at or after X (10m for pi's token-level output) past its
// last output — through the real pi record shapes and the production tick
// cadence. The evidence "waiting for the model for 46m54s with no output
// (bound 10m0s)" is therefore not something the rule produces late on its
// own: it is what the FIRST judgement reads when no judgement ran for
// ~37 minutes after the bound passed (a supervisor that was not ticking —
// on 2026-10-26 a host freeze stopped the daemon and the step process
// alike; the monitor's observation-gap handling now discounts such a gap).

import (
	"strings"
	"testing"
	"time"
)

// piStreamUntilSilence feeds pi records for one model call that streams
// thinking every 30s for streamFor and then goes silent; it answers the
// instant of the last output.
func piStreamUntilSilence(t *testing.T, c *fixClock, tr *Tracker, streamFor time.Duration) time.Time {
	t.Helper()
	pi := NewPi(tr)
	pi.Feed([]byte(`{"type":"agent_start"}`))
	pi.Feed([]byte(`{"type":"turn_start"}`))
	pi.Feed([]byte(`{"type":"message_start","message":{"role":"assistant","content":[]}}`))
	var last time.Time
	for elapsed := time.Duration(0); elapsed <= streamFor; elapsed += 30 * time.Second {
		pi.Feed([]byte(`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta"}}`))
		last = c.now
		c.advance(30 * time.Second)
	}
	return last
}

func TestPiModelSilenceIsJudgedAtTheTokenBound(t *testing.T) {
	x := DefaultSilence(NewPi(nil).Granularity())
	if x != 10*time.Minute {
		t.Fatalf("pi's silence bound X = %v, want 10m (token-level)", x)
	}
	c := &fixClock{now: time.Unix(1_700_000_000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: x, Tool: 30 * time.Minute, StepTimeout: 90 * time.Minute})
	lastOutput := piStreamUntilSilence(t, c, tr, 10*time.Minute)
	const poll = 500 * time.Millisecond // the production supervisor tick
	for deadline := lastOutput.Add(time.Hour); c.now.Before(deadline); c.advance(poll) {
		v := judge(c, tr)
		if v == nil {
			continue
		}
		if v.Cause != CauseModelHung {
			t.Fatalf("first verdict %+v, want model_hung", v)
		}
		late := c.now.Sub(lastOutput)
		if late < x || late > x+poll {
			t.Fatalf("model_hung judged %v after the last output, want within one tick of X=%v", late, x)
		}
		if !strings.Contains(v.Evidence, "(bound 10m0s)") {
			t.Fatalf("evidence %q, want the 10m bound named", v.Evidence)
		}
		return
	}
	t.Fatalf("no verdict within an hour of the model going silent")
}

// The incident's evidence is the first judgement after a 46m54s silence:
// the rule names the whole unobserved span, it does not wait for it.
func TestModelHungEvidenceNamesTheSpanSinceTheLastJudgeableOutput(t *testing.T) {
	c := &fixClock{now: time.Unix(1_700_000_000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: 10 * time.Minute})
	lastOutput := piStreamUntilSilence(t, c, tr, time.Minute)
	c.now = lastOutput.Add(46*time.Minute + 54*time.Second)
	v := judge(c, tr)
	if v == nil || v.Cause != CauseModelHung ||
		v.Evidence != "waiting for the model for 46m54s with no output (bound 10m0s)" {
		t.Fatalf("verdict %+v, want the incident's model_hung evidence", v)
	}
}
