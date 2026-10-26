package daemon

// Repro test for polyforge#199 (pi#108 grill-r1 fallout): the watcher
// wakes EVERY subscribed chat on a needs_input decision. N pi-web tabs
// of one owner are N chats (each pi session gets its own chat id from
// `pf chat init`); owner routing (subscribe-owned) subscribes every one
// of them to every WI the owner owns, so a pending question prints its
// decision line in ALL of them — and each printed line is a
// sendMessage(..., {triggerTurn: true}) in skills/polyforge/extensions/
// pf-chat.ts, i.e. the decision enters every session's context and
// wakes every agent. Whichever non-origin agent answers first consumes
// the question; the origin chat's later answer is then refused.
//
// Expected behavior per the work item: the needs_input decision wakes
// the WI's ORIGIN chat (the chat that submitted it; a fallback when it
// is gone); the owner's other chats keep passive progress only — their
// watcher still polls and writes the progress file, but never prints a
// decision line.
//
// Before the fix this test fails at the "REPORTED FAILURE" assertion:
// the owner's second chat prints a wake line on the pending question.

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

// A needs_input decision wakes only the origin chat; the owner's other
// chat (subscribed through owner routing, like a second pi-web tab)
// keeps passive progress only.
func TestNeedsInputWakesOnlyTheOriginChat(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.hub.slugs = map[string]string{"wi_wake": "proj#199"}
	td.client.Origin = &cli.SubmitOrigin{ChatID: "chat_origin", Owner: "alice"}
	td.submitClaim(t, "wi_wake")
	td.waitMonitored(t, "wi_wake")
	td.seedPendingQuestion(t, "wi_wake", "q-108", "repro", "the secret question text")
	waitNeedsInput(t, td, "wi_wake")

	// runChat runs the real resident watcher for one chat (the same
	// engine pf-chat.ts spawns per pi session) and answers its decision
	// stdout and its progress file.
	runChat := func(chat string, d time.Duration) (string, string) {
		api := clientAPI{d: td.client, chat: chat}
		var out bytes.Buffer
		files := watcher.NewFiles(t.TempDir(), chat)
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		if err := watcher.Run(ctx, api, watcher.Options{
			Owner: "alice", WaitDecision: true,
			Out: &out, Poll: 20 * time.Millisecond, Status: files.WriteStatus,
		}); err != nil {
			t.Fatalf("%s watcher: %v", chat, err)
		}
		raw, err := os.ReadFile(files.StatusPath())
		if err != nil {
			t.Fatalf("%s progress file: %v", chat, err)
		}
		return out.String(), string(raw)
	}

	// the origin chat: woken by the fixed-template needs_input line
	outOrigin, _ := runChat("chat_origin", 5*time.Second)
	if !strings.Contains(outOrigin, "[polyforge] proj#199 needs_input q-108") {
		t.Fatalf("the ORIGIN chat must be woken on needs_input: %q", outOrigin)
	}

	// the owner's other chat (a second pi-web tab): passive progress
	// only — no wake line may reach its stdout (its sendMessage sink)
	outOther, statusOther := runChat("chat_other", 400*time.Millisecond)
	if strings.Contains(outOther, "[polyforge]") {
		t.Fatalf("REPORTED FAILURE (polyforge#199): the owner's OTHER chat was woken on needs_input — every subscribed chat gets the decision line (sendMessage+triggerTurn in every pi-web tab): %q", outOther)
	}
	if !strings.Contains(statusOther, "proj#199") {
		t.Fatalf("the other chat must still get passive progress: %q", statusOther)
	}
}
