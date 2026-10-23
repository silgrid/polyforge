package watcher

// adopt_hint_test.go — the scoped-broadcast decision print
// carries the one human-readable adopt hint 「可用 `pf watch adopt <wi>`
// 接管」 — once per flagged (Adoptable) WI per batch that printed at
// least one of that WI's decision lines. A Passive subscription (an
// owner-routed subscriber outside the eligible set) gets progress only:
// no decision, no hint. An unflagged wake target prints its decisions
// with no hint. The fresh-subscription AttentionLine folds decision
// lines, so it never carries the hint. And nothing here ever adopts on
// its own — the hint is the only surface the watcher has.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestAdoptHintLineTemplate(t *testing.T) {
	l := AdoptHintLine("proj#122", "wi_gmi14")
	if !strings.Contains(l, "pf watch adopt proj#122") || !strings.HasPrefix(l, "可用") || !strings.HasSuffix(l, "接管") {
		t.Fatalf("the hint template: %q", l)
	}
	// the WI is named through the same safeID filter: no free text ever
	// rides the line
	if l := AdoptHintLine("proj; rm -rf /", "wi_x"); strings.Contains(l, "rm -rf") {
		t.Fatalf("an unsafe slug must be filtered: %q", l)
	}
}

// needsInputSnap answers a snapshot holding one undelivered needs_input
// decision (q-1).
func needsInputSnap(wi string) *protocol.WatchSnapshot {
	snap := running(wi, "s1")
	snap.Execution.PendingQuestion = &protocol.WatchPendingQuestion{ID: "q-1", Step: "s1", Question: "?"}
	return snap
}

func runWatch(t *testing.T, api API, opts Options) string {
	t.Helper()
	var out bytes.Buffer
	opts.Out, opts.Poll = &out, 20*time.Millisecond
	if err := Run(context.Background(), api, opts); err != nil {
		t.Fatalf("watcher: %v", err)
	}
	return out.String()
}

func TestAdoptHintRidesScopedDecisionPrints(t *testing.T) {
	// a flagged (Adoptable) subscription with an undelivered decision:
	// the decision line carries the one hint, once per batch
	f := &fakeAPI{snaps: map[string]*protocol.WatchSnapshot{"wi_1": needsInputSnap("wi_1")}}
	f.subs = protocol.ChatSubscriptions{ChatID: "c1", Subscriptions: []protocol.ChatSub{{WI: "wi_1", Cursor: 0, Adoptable: true}}}
	out := runWatch(t, f, Options{WaitDecision: true})
	if !strings.Contains(out, "needs_input q-1") {
		t.Fatalf("the decision must print: %q", out)
	}
	if n := strings.Count(out, "pf watch adopt"); n != 1 || !strings.Contains(out, "接管") {
		t.Fatalf("the scoped decision print must carry the ONE hint: %q", out)
	}
	// the hint is not acked state: it prints per batch that printed a
	// decision line of the flagged WI (the decision above is delivered,
	// so a later batch prints neither)
}

func TestAdoptHintAbsentWithoutTheFlag(t *testing.T) {
	// an unflagged wake target: its decisions print without the hint
	// (the flag is the daemon's eligibility fact, never the watcher's
	// guess)
	f := &fakeAPI{snaps: map[string]*protocol.WatchSnapshot{"wi_1": needsInputSnap("wi_1")}}
	f.subs = protocol.ChatSubscriptions{ChatID: "c1", Subscriptions: []protocol.ChatSub{{WI: "wi_1", Cursor: 0}}}
	out := runWatch(t, f, Options{WaitDecision: true})
	if !strings.Contains(out, "needs_input q-1") || strings.Contains(out, "pf watch adopt") {
		t.Fatalf("an unflagged decision print carries no hint: %q", out)
	}
}

func TestAdoptHintAbsentForPassiveAndFreshSubscriptions(t *testing.T) {
	// Passive: progress only — no decision, no hint (the batch prints
	// nothing and Run keeps polling; drive one cycle through a context
	// deadline)
	f := &fakeAPI{snaps: map[string]*protocol.WatchSnapshot{"wi_1": needsInputSnap("wi_1")}}
	f.subs = protocol.ChatSubscriptions{ChatID: "c1", Subscriptions: []protocol.ChatSub{{WI: "wi_1", Cursor: 0, Passive: true, Adoptable: false}}}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if err := Run(ctx, f, Options{Out: &out, Poll: 20 * time.Millisecond}); err != nil {
		t.Fatalf("watcher: %v", err)
	}
	if strings.Contains(out.String(), "[polyforge]") {
		t.Fatalf("a Passive subscription prints no decision: %q", out.String())
	}

	// fresh: the first cycle's subscribe-owned add folds the decision
	// into the AttentionLine — no decision line, no hint
	f2 := &fakeAPI{snaps: map[string]*protocol.WatchSnapshot{"wi_2": needsInputSnap("wi_2")}}
	f2.owned = []string{"wi_2"}
	var out2 bytes.Buffer
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	if err := Run(ctx2, f2, Options{Out: &out2, Poll: 20 * time.Millisecond}); err != nil {
		t.Fatalf("watcher: %v", err)
	}
	if !strings.Contains(out2.String(), "1 WI needs you") {
		t.Fatalf("the fresh decision folds into the attention line: %q", out2.String())
	}
	if strings.Contains(out2.String(), "pf watch adopt") {
		t.Fatalf("the attention line never carries the adopt hint: %q", out2.String())
	}
}
