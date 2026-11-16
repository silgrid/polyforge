package supervise

// w51_slow_latency_test.go (polyforge#51): the slow-model attribution of
// rule 4 reads the SINGLE-CALL latencies of the dying dispatch's COMPLETED
// model calls — the median over the median bound (default 60s) or the p90
// over the p90 bound (default 180s), from at least 5 completed calls —
// beside the kept "ran at least half of T" condition. The wait-share
// condition is gone: a dispatch of many fast calls is never the slow
// model, however much of the clock it spent waiting (the polyforge#25
// misjudgement).

import (
	"strings"
	"testing"
	"time"
)

func w51Latencies(n int, d time.Duration, tail ...time.Duration) []time.Duration {
	out := make([]time.Duration, 0, n+len(tail))
	for i := 0; i < n; i++ {
		out = append(out, d)
	}
	return append(out, tail...)
}

// w51P25 is polyforge#25's dying dispatch, from the ticket's measured
// session: 652 model calls, single-call median 6s, p90 15s, longest 120s.
func w51P25() []time.Duration {
	return append(w51Latencies(610, 6*time.Second, make([]time.Duration, 0)...),
		append(w51Latencies(40, 15*time.Second), 120*time.Second, 90*time.Second)...)
}

func TestW51SlowModelJudgesSingleCallLatency(t *testing.T) {
	const T = 90 * time.Minute
	now := time.Unix(10_000_000, 0)
	cases := []struct {
		name string
		// prior is the runtime earlier dispatches of the step spent (T is
		// cumulative); age is this dispatch's own runtime.
		prior time.Duration
		age   time.Duration
		// share of the clock spent waiting (the OLD rule's input)
		model   time.Duration
		calls   []time.Duration
		want    Cause
		wantNil bool
	}{
		// polyforge#25 verbatim: 652 calls, median 6s, p90 15s, max 120s,
		// the waiting share over half — an ordinary ticket's workload,
		// not a slow model. The cumulative bound expires: long task.
		{"polyforge#25: many fast calls, over half the clock waiting",
			0, T, 50 * time.Minute, w51P25(), CauseStepTimeoutLongTask, false},
		// a handful of individually slow calls is the slow model, even
		// when the waiting share of the clock is tiny
		{"five calls of 70s, waiting share 5m of 90m",
			0, T, 5 * time.Minute, w51Latencies(5, 70*time.Second), CauseStepTimeoutSlowModel, false},
		// the p90 bound alone is enough: most calls fast, the tail past 180s
		{"median 10s but the p90 tail over 180s",
			0, T, 10 * time.Minute, append(w51Latencies(4, 10*time.Second), 200*time.Second), CauseStepTimeoutSlowModel, false},
		// fewer than MinSlowModelCalls completed calls: no judgement
		// however slow they were (a median of four calls is noise)
		{"four slow calls, mostly waiting",
			0, T, 80 * time.Minute, w51Latencies(4, 20*time.Minute), CauseStepTimeoutLongTask, false},
		// a long ongoing call counts nowhere: only the four completed
		// ones are judged
		{"one call still in progress, four completed fast calls",
			0, T, 89 * time.Minute, w51Latencies(4, 30*time.Second), CauseStepTimeoutLongTask, false},
		// many fast calls with a high waiting share: still a long task
		{"fifty calls of 40s, waiting share 80m of 90m",
			0, T, 80 * time.Minute, w51Latencies(50, 40*time.Second), CauseStepTimeoutLongTask, false},
		// the kept half-of-T condition: the dying dispatch ran less than
		// half of the bound, so the step's history owns the expiry
		{"slow calls, but the dispatch ran 12m38s of the 90m bound",
			77*time.Minute + 22*time.Second, 12*time.Minute + 38*time.Second, 12 * time.Minute, w51Latencies(5, 12*time.Minute), CauseStepTimeoutLongTask, false},
		// exactly half of T is on the blamed side, as before
		{"exactly half of T, six calls of 7m",
			45 * time.Minute, 45 * time.Minute, 42 * time.Minute, w51Latencies(6, 7*time.Minute), CauseStepTimeoutSlowModel, false},
	}
	for _, tc := range cases {
		s := Snapshot{
			Step: "code", SpawnN: 4, Begun: true, StartedAt: now.Add(-tc.age),
			Thresholds:         Thresholds{StepTimeout: T, PriorRuntime: tc.prior},
			ModelTime:          tc.model,
			ModelCallLatencies: tc.calls,
		}
		v := Judge(s, now, 0)
		if tc.wantNil {
			if v != nil {
				t.Errorf("%s: verdict %+v, want none", tc.name, v)
			}
			continue
		}
		if v == nil || v.Cause != tc.want {
			t.Errorf("%s: verdict %+v, want %s", tc.name, v, tc.want)
			continue
		}
		// the evidence states the dispatch's share of the bound and the
		// latency split the attribution read
		if !strings.Contains(v.Evidence, "of the bound") || !strings.Contains(v.Evidence, "median") {
			t.Errorf("%s: evidence does not state the attribution: %s", tc.name, v.Evidence)
		}
	}
}

// TestW51TrackerRecordsCompletedCallLatencies: the tracker keeps one entry
// per COMPLETED model call — a phase closed by a tool start or a turn end
// — never the call in progress, and the observation-gap correction takes
// the forgiven span out of the recorded entry too.
func TestW51TrackerRecordsCompletedCallLatencies(t *testing.T) {
	c := &fixClock{now: time.Unix(1000, 0)}
	tr := newTrackerAt(c, Thresholds{Silence: time.Hour * 10, Tool: time.Hour, StepTimeout: time.Hour})
	for i := 0; i < 3; i++ {
		tr.WaitingModel()
		c.advance(6 * time.Second)
		tr.TurnEnded(false, "")
	}
	s := tr.Snapshot(c.now)
	if len(s.ModelCallLatencies) != 3 || s.ModelCallLatencies[0] != 6*time.Second {
		t.Fatalf("completed calls = %v, want three 6s entries", s.ModelCallLatencies)
	}
	// the call in progress is recorded nowhere
	tr.WaitingModel()
	c.advance(2 * time.Minute)
	s = tr.Snapshot(c.now)
	if len(s.ModelCallLatencies) != 3 {
		t.Fatalf("the in-progress call must not count: %v", s.ModelCallLatencies)
	}
	if s.ModelTime < 2*time.Minute {
		t.Fatalf("the in-progress wait still shows in ModelTime: %s", s.ModelTime)
	}
	// close it, then declare an observation gap over its whole span: the
	// recorded entry loses the gap like the cumulative split does
	c.advance(1 * time.Minute)
	tr.TurnEnded(false, "")
	// the monitor's shape: cutoff in the past, shift the unwatched span
	tr.ObserveTick(c.now.Add(-1*time.Minute), 3*time.Minute)
	s = tr.Snapshot(c.now)
	if len(s.ModelCallLatencies) != 4 {
		t.Fatalf("four completed calls: %v", s.ModelCallLatencies)
	}
	if got := s.ModelCallLatencies[3]; got != 0 {
		t.Fatalf("the gap-forgiven call keeps %s, want the 3m gap removed (0s)", got)
	}
	if s.ModelTime >= 2*time.Minute {
		t.Fatalf("the cumulative split must lose the gap too: %s", s.ModelTime)
	}
}

// TestW51MedianAndP90: the two statistics read exactly the ticket's
// definitions — the middle value (the mean of the two middles for an even
// count) and the smallest value at or above 90%.
func TestW51MedianAndP90(t *testing.T) {
	if m := medianDuration(w51Latencies(5, 60*time.Second)); m != 60*time.Second {
		t.Fatalf("median of five 60s calls = %s", m)
	}
	if m := medianDuration([]time.Duration{1 * time.Second, 2 * time.Second, 100 * time.Second, 3 * time.Second, 4 * time.Second}); m != 3*time.Second {
		t.Fatalf("median of an odd count = %s, want the middle", m)
	}
	if m := medianDuration([]time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second, 200 * time.Second}); m != 2500*time.Millisecond {
		t.Fatalf("median of an even count = %s, want the mean of the two middles (2.5s)", m)
	}
	small := w51Latencies(9, 6*time.Second)
	if p := p90Duration(small); p != 6*time.Second {
		t.Fatalf("p90 of ten 6s calls = %s", p)
	}
	mixed := w51Latencies(80, 6*time.Second)
	mixed = append(mixed, w51Latencies(18, 15*time.Second)...)
	mixed = append(mixed, 120*time.Second)
	if p := p90Duration(mixed); p != 15*time.Second {
		t.Fatalf("p90 = %s, want 15s (the 90th percentile of the #25 shape)", p)
	}
}
