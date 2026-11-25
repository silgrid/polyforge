package driver

// autoanswer_identity_test.go: the automatic reply's reserved identity is
// a delivery, never the owner's authorization (autoanswer.go, relay.go,
// ownermsg.go; polyforge#239). The owner's per-owner checks read
// OwnerMessagesOfRound, so the automatic line must be absent there; the
// step's delivery set still carries it. A normal `pf answer` may not
// record the reserved author at all.

import (
	"context"
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestAutoAnswerIdentityIsNotOwnerAuthorization(t *testing.T) {
	const askAt = "2026-11-23T00:00:00Z"
	lines := []capsule.AskLine{
		{At: askAt, Step: "fix", Spawn: 1, Kind: capsule.AskKindAsk, Text: "| 问题 | 推荐选项 | 理由 | 重点关注程度 |\n|---|---|---|---|\n| 1. x | A | y | ★ |"},
		{At: "2026-11-23T00:00:01Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindAwaiting, AskAt: askAt},
		{At: "2026-11-23T00:30:00Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindReply, AskAt: askAt, By: capsule.AutoAnswerBy, Text: "auto"},
		{At: "2026-11-23T00:31:00Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindMessage, By: "alice", Text: "human"},
	}
	// The per-owner authorization set (hand-off --per-owner, hub attrs,
	// intents) never counts the daemon's own reply.
	owner := OwnerMessagesOfRound(lines, "fix", nil)
	if len(owner) != 1 || owner[0].By != "alice" {
		t.Fatalf("owner authorization set = %+v, want only the human message", owner)
	}
	// The delivery set still carries both: the agent must receive the
	// automatic reply as well.
	undelivered := capsule.Undelivered(lines, "fix")
	if len(undelivered) != 2 {
		t.Fatalf("delivery set = %+v, want the automatic reply and the human message", undelivered)
	}

	// A normal answer may not record the reserved author.
	if _, err := OwnerMessageLine(t.TempDir(), "fix", 1, "x", capsule.AutoAnswerBy, "2026-11-23T00:32:00Z"); !errors.Is(err, ErrInvalidOwnerMessage) {
		t.Fatalf("OwnerMessageLine with the reserved author = %v, want ErrInvalidOwnerMessage", err)
	}
	dir := t.TempDir()
	d := &Driver{
		wi:   WIInfo{ID: "wi", CapsuleDir: dir},
		exec: &capsule.Execution{WorkflowStatus: StatusRunning, CurrentStep: "fix", Steps: map[string]*capsule.Step{"fix": {Status: capsule.StepInProgress, Dispatch: 1}}},
	}
	if _, err := d.OwnerMessage(context.Background(), "x", capsule.AutoAnswerBy); !errors.Is(err, ErrInvalidOwnerMessage) {
		t.Fatalf("OwnerMessage with the reserved author = %v, want ErrInvalidOwnerMessage", err)
	}
	if lines, _ := capsule.ReadAsks(dir); len(lines) != 0 {
		t.Fatalf("a refused impersonation recorded %+v", lines)
	}
}
