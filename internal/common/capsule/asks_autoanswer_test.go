package capsule

// asks_autoanswer_test.go: the daemon-only awaiting line (polyforge#239):
// the first moment a dispatch was seen awaiting the owner is T0, and only
// that first line counts. A restart reads it; it never rewrites it. An
// older wait with no line, a torn stamp or a T0 in the future has no
// trustworthy wait start, so the automatic reply must refuse.

import (
	"testing"
	"time"
)

func TestAwaitingSinceIsTheFirstRecordedWait(t *testing.T) {
	const (
		step  = "build"
		askAt = "2026-11-23T00:00:00.000000001Z"
	)
	now := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	ask := AskLine{At: askAt, Step: step, Spawn: 1, Kind: AskKindAsk, Text: "q"}
	first := AskLine{At: "2026-11-23T00:00:01Z", Step: step, Spawn: 1, Kind: AskKindAwaiting, AskAt: askAt}
	second := AskLine{At: "2026-11-23T00:05:00Z", Step: step, Spawn: 1, Kind: AskKindAwaiting, AskAt: askAt}

	// the earliest line wins, not the latest
	got, ok := AwaitingSince([]AskLine{ask, first, second}, step, 1, askAt, now)
	if !ok || !got.Equal(time.Date(2026, 11, 23, 0, 0, 1, 0, time.UTC)) {
		t.Fatalf("AwaitingSince = %v, %v; want the first line's 00:00:01", got, ok)
	}

	// an old wait (no awaiting line) is not trustworthy
	if _, ok := AwaitingSince([]AskLine{ask}, step, 1, askAt, now); ok {
		t.Fatal("an ask with no awaiting line answered a T0")
	}
	// another ask / another dispatch / another step is not this wait
	for _, l := range []AskLine{
		{At: "2026-11-23T00:00:01Z", Step: step, Spawn: 2, Kind: AskKindAwaiting, AskAt: askAt},
		{At: "2026-11-23T00:00:01Z", Step: "review", Spawn: 1, Kind: AskKindAwaiting, AskAt: askAt},
		{At: "2026-11-23T00:00:01Z", Step: step, Spawn: 1, Kind: AskKindAwaiting, AskAt: "2026-11-23T00:00:00.000000002Z"},
	} {
		if _, ok := AwaitingSince([]AskLine{ask, l}, step, 1, askAt, now); ok {
			t.Fatalf("a foreign awaiting line %+v answered a T0", l)
		}
	}
	// a T0 in the future of now is no trustworthy wait start
	future := AskLine{At: "2026-11-23T03:00:00Z", Step: step, Spawn: 1, Kind: AskKindAwaiting, AskAt: askAt}
	if _, ok := AwaitingSince([]AskLine{ask, future}, step, 1, askAt, now); ok {
		t.Fatal("a future T0 answered a trustworthy wait start")
	}
	// a torn stamp is no T0
	torn := AskLine{At: "not-a-time", Step: step, Spawn: 1, Kind: AskKindAwaiting, AskAt: askAt}
	if _, ok := AwaitingSince([]AskLine{ask, torn}, step, 1, askAt, now); ok {
		t.Fatal("a torn T0 stamp answered a trustworthy wait start")
	}
}