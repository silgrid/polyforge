package observe

// backpressure_test.go: §九 — a failed push is retried with an
// exponential delay (bounded by PushBackoffMax); reaching the bound
// writes ONE push_backpressure event and NEVER kills the agent; a newer
// Version is pushed at once without resetting the delay, and only a
// success ends the episode.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// pushRecorder is a scripted PushFunc that records each attempt's moment.
type pushRecorder struct {
	mu   sync.Mutex
	at   []time.Time
	fail bool
}

func (p *pushRecorder) push(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at = append(p.at, time.Now())
	if p.fail {
		return "sha_local", false, gitops.PushTimings{}, errors.New("GIT-500: upstream refused")
	}
	return "sha_ok", true, gitops.PushTimings{}, nil
}

func (p *pushRecorder) attempts() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.at...)
}

func (p *pushRecorder) setFail(v bool) {
	p.mu.Lock()
	p.fail = v
	p.mu.Unlock()
}

func TestPushRetryBacksOffExponentiallyAndBackpressuresOnce(t *testing.T) {
	rec := &pushRecorder{fail: true}
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), func(c *monitorCfg) {
		c.PushRetryBackoff = 20 * time.Millisecond
		c.PushBackoffMax = 160 * time.Millisecond
		c.push = rec.push
	})
	m.writeExec(t, 1, "code")
	// delays 20, 40, 80, 160, 160 ms: six attempts take >= 460ms
	soon(t, 5*time.Second, "six failed pushes", func() bool { return len(rec.attempts()) >= 6 })
	at := rec.attempts()
	for i, want := range []time.Duration{20, 40, 80, 160, 160} {
		if gap := at[i+1].Sub(at[i]); gap < want*time.Millisecond {
			t.Fatalf("retry %d came after %v, want >= %v (exponential backoff)", i+1, gap, want*time.Millisecond)
		}
	}
	soon(t, time.Second, "one push_failed per failed attempt", func() bool { return len(eventsOf(m, EvPushFailed)) >= 6 })
	soon(t, time.Second, "the degradation event", func() bool { return len(eventsOf(m, EvPushBackpressure)) == 1 })
	// The agent is NOT killed and the monitor still consumes facts.
	m.link.Fact(supervise.StepStarted{Step: "code", SpawnN: 1, StartedAt: time.Now()})
	soon(t, 300*time.Millisecond, "monitor alive during degradation", func() bool { return m.activity().InDispatch })

	// A success ends the episode: published, and no further
	// degradation event.
	rec.setFail(false)
	soon(t, time.Second, "successful push after degradation", func() bool { return len(eventsOf(m, EvPublished)) >= 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(eventsOf(m, EvPushBackpressure)); n != 1 {
		t.Fatalf("push_backpressure events: %d, want exactly 1 per episode", n)
	}
}

func TestNewerVersionPushesAtOnceDespiteBackoff(t *testing.T) {
	rec := &pushRecorder{fail: true}
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), func(c *monitorCfg) {
		c.PushRetryBackoff = time.Hour
		c.PushBackoffMax = time.Hour
		c.push = rec.push
	})
	m.writeExec(t, 1, "code")
	soon(t, time.Second, "the first failed push", func() bool { return len(rec.attempts()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(rec.attempts()); n != 1 {
		t.Fatalf("the failed Version must wait out its delay, got %d attempts", n)
	}
	rec.setFail(false)
	m.writeExec(t, 2, "code")
	soon(t, time.Second, "the newer Version published at once", func() bool { return len(eventsOf(m, EvPublished)) == 1 })
}

// Versions that keep arriving during an outage are each pushed at once,
// but they do not restart the ladder: the failures still climb to the
// cap and the degradation event fires once.
func TestBackpressureFiresWhileVersionsKeepArriving(t *testing.T) {
	rec := &pushRecorder{fail: true}
	m, _ := newTestMonitor(t, newFakeHandle("att_1"), func(c *monitorCfg) {
		c.PushRetryBackoff = time.Hour
		c.PushBackoffMax = 4 * time.Hour // reached at the third failure
		c.push = rec.push
	})
	for v := int64(1); v <= 5; v++ {
		m.writeExec(t, v, "code")
		soon(t, time.Second, "the new Version pushed at once", func() bool { return len(rec.attempts()) == int(v) })
	}
	soon(t, time.Second, "the degradation event", func() bool { return len(eventsOf(m, EvPushBackpressure)) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(eventsOf(m, EvPushBackpressure)); n != 1 {
		t.Fatalf("push_backpressure events: %d, want exactly 1", n)
	}
}
