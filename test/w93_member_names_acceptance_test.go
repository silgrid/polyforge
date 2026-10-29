package test

// w93_member_names_acceptance_test.go: the real-binary acceptance suite for
//'s aihub/pf half — the conversation list's `member_names`
// mapping and `pf msg list --json` pass-through. Authored BEFORE `code`
// against `artifacts/spec.md` only: every scenario boots the shipped `aihub`
// and drives the real `pf` CLI, so the baseline reds are assertion-level (the
// field is absent from the wire answer), never build-level. The test decodes
// the wire JSON into local structs on purpose: it must compile against the
// pre-implementation tree, where the shared api type carries no MemberNames.
//
// Scenario document: artifacts/e2e-scenarios.md (W93-S1..W93-S4).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w93Conversation is the wire shape a conversation row must have after the
// change: the existing member ids plus their display-name mapping.
type w93Conversation struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Members     []string          `json:"members"`
	MemberNames map[string]string `json:"member_names"`
}

type w93Answer struct {
	Conversations []w93Conversation `json:"conversations"`
}

// w93List reads the conversation list as one caller and returns both the raw
// bytes (for whole-answer leak checks) and the decoded rows.
func w93List(t *testing.T, inst *v1Install, apiKey string) (string, w93Answer) {
	t.Helper()
	raw, err := aihubclient.New(inst.url, apiKey).ListConversations()
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	var answer w93Answer
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("conversation list answer: %v (%s)", err, raw)
	}
	return string(raw), answer
}

// w93Find returns the named conversation row, failing when the caller's list
// does not contain it.
func w93Find(t *testing.T, answer w93Answer, convID string) w93Conversation {
	t.Helper()
	for _, conversation := range answer.Conversations {
		if conversation.ID == convID {
			return conversation
		}
	}
	t.Fatalf("conversation %s is missing from the caller's list: %+v", convID, answer.Conversations)
	return w93Conversation{}
}

// w93WantNames asserts member_names covers exactly `members` and maps every
// member to that user's display name — the scope contract, not just presence.
func w93WantNames(t *testing.T, conversation w93Conversation, displayName map[string]string) {
	t.Helper()
	if conversation.MemberNames == nil {
		t.Fatalf("conversation %s carries no member_names (members %v)", conversation.ID, conversation.Members)
	}
	if len(conversation.MemberNames) != len(conversation.Members) {
		t.Fatalf("conversation %s member_names has %d keys, members has %d: %v vs %v",
			conversation.ID, len(conversation.MemberNames), len(conversation.Members),
			conversation.MemberNames, conversation.Members)
	}
	for _, member := range conversation.Members {
		name, ok := conversation.MemberNames[member]
		if !ok {
			t.Fatalf("conversation %s member_names is missing member %s: %v", conversation.ID, member, conversation.MemberNames)
		}
		if want, known := displayName[member]; known && name != want {
			t.Fatalf("conversation %s member %s name = %q, want %q", conversation.ID, member, name, want)
		}
	}
}

// W93-S1: a conversation's member_names is decided by membership alone, so an
// admin who is not a member still reads the names and every caller sees the
// same mapping (no admin-specific branch).
func TestW93AdminSeesNamesForANonMemberConversation(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w93-alice")
	bob, bobKey := w11MintKey(t, inst, "w93-bob")
	conv := w93Send(t, inst.url, aliceKey, bob, "w93 s1 hello")

	raw, adminAnswer := w93List(t, inst, inst.adminKey)
	if !strings.Contains(raw, conv) {
		t.Fatalf("the admin's list does not carry conversation %s: %s", conv, raw)
	}
	adminRow := w93Find(t, adminAnswer, conv)
	for _, member := range []string{alice, bob} {
		if containsString(adminRow.Members, member) {
			continue
		}
		t.Fatalf("the admin's row for %s is missing member %s: %v", conv, member, adminRow.Members)
	}
	if adminRow.MemberNames == nil {
		t.Fatalf("the admin's row for %s carries no member_names (members %v)", conv, adminRow.Members)
	}
	if got := adminRow.MemberNames[alice]; got != "w93-alice" {
		t.Fatalf("admin row name for alice = %q, want %q", got, "w93-alice")
	}
	if got := adminRow.MemberNames[bob]; got != "w93-bob" {
		t.Fatalf("admin row name for bob = %q, want %q", got, "w93-bob")
	}

	// A member's mapping for the same conversation is identical: membership
	// alone decides the names, not the caller's role.
	_, memberAnswer := w93List(t, inst, bobKey)
	memberRow := w93Find(t, memberAnswer, conv)
	if len(memberRow.MemberNames) != len(adminRow.MemberNames) {
		t.Fatalf("admin and member disagree on member_names: %v vs %v", adminRow.MemberNames, memberRow.MemberNames)
	}
	for member, name := range adminRow.MemberNames {
		if memberRow.MemberNames[member] != name {
			t.Fatalf("admin branch: %s name = %q for the member, want the admin's %q",
				member, memberRow.MemberNames[member], name)
		}
	}
}

// W93-S2: a non-admin who is not a member neither receives the conversation
// nor any name from it, while their own conversation still carries names.
func TestW93NonMemberGetsNeitherConversationNorNames(t *testing.T) {
	inst := newV1Install(t)
	_, aliceKey := w11MintKey(t, inst, "w93-alice")
	bob, _ := w11MintKey(t, inst, "w93-bob")
	carol, carolKey := w11MintKey(t, inst, "w93-carol")
	dave, daveKey := w11MintKey(t, inst, "w93-dave")
	foreign := w93Send(t, inst.url, aliceKey, bob, "w93 s2 not for carol")
	own := w93Send(t, inst.url, daveKey, carol, "w93 s2 carol and dave")

	raw, answer := w93List(t, inst, carolKey)
	for _, conversation := range answer.Conversations {
		if conversation.ID == foreign {
			t.Fatalf("a non-member received conversation %s: %s", foreign, raw)
		}
	}
	// The foreign conversation's members' names are unreachable for her too.
	for _, name := range []string{"w93-alice", "w93-bob"} {
		if strings.Contains(raw, name) {
			t.Fatalf("a non-member's list carries the display name %q: %s", name, raw)
		}
	}
	// Control: her own conversation does carry names, so the reds above are
	// about scope, not about an empty list.
	ownRow := w93Find(t, answer, own)
	w93WantNames(t, ownRow, map[string]string{carol: "w93-carol", dave: "w93-dave"})
	if ownRow.MemberNames[dave] != "w93-dave" {
		t.Fatalf("carol's own conversation lost dave's name: %v", ownRow.MemberNames)
	}
}

// W93-S3: member_names' key set equals members exactly and a non-member's
// display name never appears in any conversation list answer.
func TestW93MemberNamesKeysEqualMembersAndNeverAWiderScope(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w93-alice")
	bob, _ := w11MintKey(t, inst, "w93-bob")
	// carol belongs to no conversation: her name must never be joined in.
	w11MintKey(t, inst, "w93-carol")
	conv := w93Send(t, inst.url, aliceKey, bob, "w93 s3 only alice and bob")

	raw, answer := w93List(t, inst, inst.adminKey)
	if len(answer.Conversations) == 0 {
		t.Fatalf("the admin's list is empty, so the key-set pin cannot run")
	}
	for _, conversation := range answer.Conversations {
		seen := map[string]bool{}
		for _, member := range conversation.Members {
			if seen[member] {
				t.Fatalf("conversation %s repeats member %s", conversation.ID, member)
			}
			seen[member] = true
		}
		w93WantNames(t, conversation, map[string]string{alice: "w93-alice", bob: "w93-bob"})
	}
	row := w93Find(t, answer, conv)
	if len(row.MemberNames) != 2 || row.MemberNames[alice] != "w93-alice" || row.MemberNames[bob] != "w93-bob" {
		t.Fatalf("conversation %s member_names = %v, want exactly alice and bob", conv, row.MemberNames)
	}
	if strings.Contains(raw, "w93-carol") {
		t.Fatalf("a non-member's display name appears in a conversation list answer: %s", raw)
	}
}

// W93-S4: `pf msg list --json` exposes member_names exactly as aihub returned
// it, and the messaging CLI surface gains no name verb.
func TestW93PfListPassesMemberNamesThroughUnchanged(t *testing.T) {
	inst := newV1Install(t)
	alice, aliceKey := w11MintKey(t, inst, "w93-alice")
	bob, _ := w11MintKey(t, inst, "w93-bob")
	conv := w93Send(t, inst.url, aliceKey, bob, "w93 s4 passthrough")

	_, viaAihub := w93List(t, inst, aliceKey)

	out, code := w11PfAs(t, inst, aliceKey, "--json", "msg", "list")
	if code != 0 {
		t.Fatalf("pf msg list --json exit %d: %s", code, out)
	}
	var viaPf w93Answer
	if err := json.Unmarshal([]byte(out), &viaPf); err != nil {
		t.Fatalf("pf msg list --json answer: %v (%s)", err, out)
	}
	row := w93Find(t, viaPf, conv)
	w93WantNames(t, row, map[string]string{alice: "w93-alice", bob: "w93-bob"})
	want := w93Find(t, viaAihub, conv).MemberNames
	if len(row.MemberNames) != len(want) {
		t.Fatalf("pf passed %d names through, aihub sent %d: %v vs %v", len(row.MemberNames), len(want), row.MemberNames, want)
	}
	for member, name := range want {
		if row.MemberNames[member] != name {
			t.Fatalf("pf changed member %s name to %q, aihub sent %q", member, row.MemberNames[member], name)
		}
	}

	// No name-resolution surface appeared: the messaging verbs still refuse an
	// unknown action and list none in their usage line.
	out, code = w11PfAs(t, inst, aliceKey, "msg", "lookup")
	if code == 0 {
		t.Fatalf("pf grew a name-resolution verb: pf msg lookup succeeded: %s", out)
	}
	if strings.Contains(out, "lookup") {
		t.Fatalf("pf msg lookup is advertised: %s", out)
	}
}

// w93Send sends one message as the given key and answers its conversation id.
func w93Send(t *testing.T, url, key, to, text string) string {
	t.Helper()
	raw, err := aihubclient.New(url, key).SendMessage(api.SendMessage{To: to, Text: text})
	if err != nil {
		t.Fatalf("send %q: %v", text, err)
	}
	var msg struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil || msg.ConversationID == "" {
		t.Fatalf("send answer for %q: %v (%s)", text, err, raw)
	}
	return msg.ConversationID
}

// containsString answers whether needle is one of haystack's entries.
func containsString(haystack []string, needle string) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}
