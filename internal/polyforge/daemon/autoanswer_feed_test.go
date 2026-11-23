package daemon

// autoanswer_feed_test.go: the decision feed's automatic-reply entry
// (decisions.go; polyforge#239). The event resolves the open question's
// needs_input entry as replied and records its own distinguishable
// information entry.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

func TestAutoAnswerFeedEntry(t *testing.T) {
	f, err := newDecisionFeed(filepath.Join(t.TempDir(), "decisions.json"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		wi    = "wi_feed"
		askAt = "2026-11-23T00:00:00Z"
	)
	askBody, _ := json.Marshal(map[string]any{"step": "build", "spawn_n": 1, "ask_at": askAt, "question_id": askAt, "question": "q"})
	f.Ingest("default", protocol.EventEntry{At: "2026-11-23T00:00:01Z", WI: wi, Kind: driver.EvAwaitingOwner, Body: askBody}, nil)
	autoBody, _ := json.Marshal(map[string]any{
		"step": "build", "spawn_n": 1, "ask_at": askAt, "question_id": askAt, "at": "2026-11-23T00:30:00Z",
		"by": capsule.AutoAnswerBy, "elapsed": "30m0s", "timeout": "30m0s", "recommendations": []string{"A"},
	})
	f.Ingest("default", protocol.EventEntry{At: "2026-11-23T00:30:00Z", WI: wi, Kind: driver.EvAutoAnswered, Body: autoBody}, nil)

	var needs, auto *protocol.Decision
	feed := f.List()
	for i := range feed.Decisions {
		switch feed.Decisions[i].Class {
		case "needs_input":
			needs = &feed.Decisions[i]
		case "auto_answered":
			auto = &feed.Decisions[i]
		}
	}
	if auto == nil || auto.Phrase != "已自动按推荐继续" || auto.Attention {
		t.Fatalf("auto entry = %+v, want the fixed phrase and no attention", auto)
	}
	if needs == nil || !needs.Replied || needs.RepliedAt != "2026-11-23T00:30:00Z" || needs.Attention {
		t.Fatalf("needs_input entry = %+v, want it replied at the auto reply's stamp", needs)
	}
}
