package test

// w56_messages_acceptance_test.go: the real-binary acceptance suite for
// (delivery/read cursor split, `pf msg ack`, `list --before`,
// unread excluding self). Authored BEFORE `code` against
// `artifacts/spec.md` + `artifacts/plan.md` only: every scenario drives the
// shipped `aihub` and `pf` binaries as real processes and asserts on real
// wire/DB state, so the baseline reds are assertion-level (a missing verb or
// route answers non-zero/404/CORE-400), never build-level.
//
// Scenario document: artifacts/e2e-scenarios.md (W56-S1..W56-S5).

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w56Pair creates two human members plus their keys.
func w56Pair(t *testing.T, inst *v1Install) (string, string, string, string) {
	t.Helper()
	alice, aliceKey := w11MintKey(t, inst, "w56-alice")
	bob, bobKey := w11MintKey(t, inst, "w56-bob")
	return alice, aliceKey, bob, bobKey
}

// w56Send sends one message as the given key and returns the stored row.
func w56Send(t *testing.T, url, key, to, text string) api.Message {
	t.Helper()
	raw, err := aihubclient.New(url, key).SendMessage(api.SendMessage{To: to, Text: text})
	if err != nil {
		t.Fatalf("send %q: %v", text, err)
	}
	var msg api.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("send answer %q: %v (%s)", text, err, raw)
	}
	return msg
}

// w56Unread reads one conversation's unread count through the real CLI.
func w56Unread(t *testing.T, inst *v1Install, key, conv string) int {
	t.Helper()
	out, code := w11PfAs(t, inst, key, "--json", "msg", "list")
	if code != 0 {
		t.Fatalf("pf msg list exit %d: %s", code, out)
	}
	var ans struct {
		Conversations []struct {
			ID     string `json:"id"`
			Unread int    `json:"unread"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal([]byte(out), &ans); err != nil {
		t.Fatalf("pf msg list answer: %v (%s)", err, out)
	}
	for _, c := range ans.Conversations {
		if c.ID == conv {
			return c.Unread
		}
	}
	t.Fatalf("conversation %s is not in the caller's list: %s", conv, out)
	return -1
}

// w56Page reads one message page through the real CLI and answers its rows.
func w56Page(t *testing.T, inst *v1Install, key, conv string, args ...string) api.MessagePage {
	t.Helper()
	out, code := w11PfAs(t, inst, key, append([]string{"--json", "msg", "list", "--conversation", conv}, args...)...)
	if code != 0 {
		t.Fatalf("pf msg list --conversation %s %v exit %d: %s", conv, args, code, out)
	}
	var page api.MessagePage
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("msg page %v: %v (%s)", args, err, out)
	}
	return page
}

// w56Ack runs the real acknowledgment verb and answers output + exit code.
func w56Ack(t *testing.T, inst *v1Install, key string, args ...string) (string, int) {
	t.Helper()
	return w11PfAs(t, inst, key, append([]string{"--json", "msg", "ack"}, args...)...)
}

// W56-S1: acknowledging moves the read cursor; own messages are never unread.
func TestW56AckAdvancesTheReadCursorOnly(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey, bob, bobKey := w56Pair(t, inst)
	own := w56Send(t, inst.url, aliceKey, bob, "w56 s1 own")
	w56Send(t, inst.url, bobKey, alice, "w56 s1 peer a")
	last := w56Send(t, inst.url, bobKey, alice, "w56 s1 peer b")
	conv := own.ConversationID

	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread before ack = %d, want 2 (own message excluded)", got)
	}
	if out, code := w56Ack(t, inst, aliceKey, conv); code != 0 {
		t.Fatalf("pf msg ack <conversation> exit %d: %s", code, out)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 0 {
		t.Fatalf("unread after ack-to-newest = %d, want 0", got)
	}

	// A newer message arrives, then a repeated and a backward ack are no-ops.
	newest := w56Send(t, inst.url, bobKey, alice, "w56 s1 peer c")
	if newest.Seq <= last.Seq {
		t.Fatalf("new message seq %d did not advance past %d", newest.Seq, last.Seq)
	}
	if out, code := w56Ack(t, inst, aliceKey, conv, "--seq", fmt.Sprint(last.Seq)); code != 0 {
		t.Fatalf("backward ack exit %d: %s", code, out)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 1 {
		t.Fatalf("unread after a backward ack = %d, want 1 (the newer message stays unread)", got)
	}
	if out, code := w56Ack(t, inst, aliceKey, conv, "--seq", fmt.Sprint(newest.Seq)); code != 0 {
		t.Fatalf("ack to newest exit %d: %s", code, out)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 0 {
		t.Fatalf("unread after ack --seq %d = %d, want 0", newest.Seq, got)
	}
}

// W56-S2: an out-of-range, non-positive or unknown-conversation ack is refused.
func TestW56AckRefusesOutOfRangeAndUnknownConversation(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey, _, bobKey := w56Pair(t, inst)
	first := w56Send(t, inst.url, bobKey, alice, "w56 s2 peer a")
	w56Send(t, inst.url, bobKey, alice, "w56 s2 peer b")
	conv := first.ConversationID
	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread before refusals = %d, want 2", got)
	}

	out, code := w56Ack(t, inst, aliceKey, conv, "--seq", fmt.Sprint(first.Seq+1000))
	if code == 0 {
		t.Fatalf("ack past the newest message succeeded: %s", out)
	}
	if !strings.Contains(out, "CORE-400") {
		t.Fatalf("ack past the newest message = %q, want CORE-400", out)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("a refused ack changed unread to %d, want 2", got)
	}

	for _, seq := range []string{"0", "-3"} {
		out, code = w56Ack(t, inst, aliceKey, conv, "--seq", seq)
		if code == 0 {
			t.Fatalf("ack --seq %s succeeded: %s", seq, out)
		}
		if !strings.Contains(out, "CORE-400") {
			t.Fatalf("ack --seq %s = %q, want CORE-400", seq, out)
		}
	}

	out, code = w56Ack(t, inst, aliceKey, "conv_does_not_exist")
	if code == 0 {
		t.Fatalf("ack of an unknown conversation succeeded: %s", out)
	}
	if !strings.Contains(out, "CORE-404") {
		t.Fatalf("ack of an unknown conversation = %q, want CORE-404", out)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread after the refusals = %d, want 2 (nothing written)", got)
	}
}

// W56-S3: unread counts only the other participant after the read cursor, and
// delivered-but-unacknowledged messages stay unread.
func TestW56UnreadCountsOnlyTheOtherParticipant(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey, bob, bobKey := w56Pair(t, inst)
	own := w56Send(t, inst.url, aliceKey, bob, "w56 s3 own")
	w56Send(t, inst.url, bobKey, alice, "w56 s3 peer a")
	last := w56Send(t, inst.url, bobKey, alice, "w56 s3 peer b")
	conv := own.ConversationID

	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread = %d, want 2: own message must not count and both peer messages must", got)
	}

	// Machine delivery is the daemon's own credential; it must not read.
	var ignored map[string]any
	if err := aihubclient.New(inst.url, aliceKey).Do("POST", "/api/v1/messages/delivered",
		map[string]any{"conversation_id": conv, "seq": last.Seq}, &ignored); err != nil {
		t.Fatalf("delivered route: %v", err)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread after delivery = %d, want 2 (delivered is not read)", got)
	}
	if got := w56Unread(t, inst, bobKey, conv); got != 1 {
		t.Fatalf("sender's unread = %d, want 1 (only alice's own message)", got)
	}
}

// W56-S4: `--before` pages backwards with no gap or duplicate; `--after` is
// unchanged.
func TestW56ListBeforePagesBackwards(t *testing.T) {
	inst := newV1Install(t)
	carol, carolKey := w11MintKey(t, inst, "w56-carol")
	dave, daveKey := w11MintKey(t, inst, "w56-dave")
	conv := ""
	for i := 1; i <= 120; i++ {
		key, to := carolKey, dave
		if i%2 == 0 {
			key, to = daveKey, carol
		}
		msg := w56Send(t, inst.url, key, to, fmt.Sprintf("w56 paging %03d", i))
		if conv == "" {
			conv = msg.ConversationID
		}
		if i == 120 && msg.Seq != 120 {
			t.Fatalf("newest seq = %d, want 120 in a fresh install", msg.Seq)
		}
	}

	newest := w56Page(t, inst, carolKey, conv, "--before", "121", "--limit", "50")
	if len(newest.Messages) != 50 {
		t.Fatalf("newest page has %d messages, want 50", len(newest.Messages))
	}
	if newest.Messages[0].Text != "w56 paging 071" || newest.Messages[49].Text != "w56 paging 120" {
		t.Fatalf("newest page = %q..%q, want paging 071..120",
			newest.Messages[0].Text, newest.Messages[49].Text)
	}
	for i := 1; i < len(newest.Messages); i++ {
		if newest.Messages[i].Seq <= newest.Messages[i-1].Seq {
			t.Fatalf("newest page is not ascending at %d", i)
		}
	}

	older := w56Page(t, inst, carolKey, conv, "--before", "71", "--limit", "50")
	if len(older.Messages) != 50 {
		t.Fatalf("older page has %d messages, want 50", len(older.Messages))
	}
	if older.Messages[0].Text != "w56 paging 021" || older.Messages[49].Text != "w56 paging 070" {
		t.Fatalf("older page = %q..%q, want paging 021..070",
			older.Messages[0].Text, older.Messages[49].Text)
	}
	seen := map[int64]bool{}
	for _, msg := range append(append([]api.Message{}, newest.Messages...), older.Messages...) {
		if seen[msg.Seq] {
			t.Fatalf("seq %d appeared twice across the two pages", msg.Seq)
		}
		seen[msg.Seq] = true
	}

	forward := w56Page(t, inst, carolKey, conv, "--after", "70", "--limit", "10")
	if len(forward.Messages) != 10 || forward.Messages[0].Text != "w56 paging 071" || forward.Messages[9].Text != "w56 paging 080" {
		t.Fatalf("forward page after 70 = %d messages (%q..%q), want paging 071..080",
			len(forward.Messages), forward.Messages[0].Text, forward.Messages[len(forward.Messages)-1].Text)
	}
}

// W56-S5: machine delivery advances a separate delivery cursor, the caller's
// own only, and leaves read/unread untouched.
func TestW56DeliveryAdvancesOnlyTheDeliveryCursor(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey, _, bobKey := w56Pair(t, inst)
	w56Send(t, inst.url, bobKey, alice, "w56 s5 peer a")
	last := w56Send(t, inst.url, bobKey, alice, "w56 s5 peer b")
	conv := last.ConversationID

	var ignored map[string]any
	if err := aihubclient.New(inst.url, aliceKey).Do("POST", "/api/v1/messages/delivered",
		map[string]any{"conversation_id": conv, "seq": last.Seq}, &ignored); err != nil {
		t.Fatalf("delivered route: %v", err)
	}

	// Two cursors exist and only delivery moved.
	cursors := func(user string) (int64, int64) {
		t.Helper()
		db := w11DB(t, inst)
		defer db.Close()
		var delivered, read int64
		if err := db.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
			WHERE conversation_id=?1 AND user_id=?2`, conv, user).Scan(&delivered, &read); err != nil {
			t.Fatalf("member state for %s: %v", user, err)
		}
		return delivered, read
	}
	delivered, read := cursors(alice)
	if delivered != last.Seq || read != 0 {
		t.Fatalf("alice cursors = delivered %d / read %d, want %d / 0", delivered, read, last.Seq)
	}
	if got := w56Unread(t, inst, aliceKey, conv); got != 2 {
		t.Fatalf("unread after delivery = %d, want 2", got)
	}

	// Another caller's delivery advances only that caller's row.
	if err := aihubclient.New(inst.url, bobKey).Do("POST", "/api/v1/messages/delivered",
		map[string]any{"conversation_id": conv, "seq": last.Seq}, &ignored); err != nil {
		t.Fatalf("bob delivered route: %v", err)
	}
	if delivered, read = cursors(alice); delivered != last.Seq || read != 0 {
		t.Fatalf("bob's delivery moved alice's row: delivered %d / read %d", delivered, read)
	}
}
