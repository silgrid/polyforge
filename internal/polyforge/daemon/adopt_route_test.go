package daemon

// adopt_route_test.go — the adopt route's unit surface, the
// half the acceptance e2e deliberately does not duplicate: the
// no-local-job refusal (a WI this daemon does not hold has no
// wake-target role left to take over), the Origin.Owner-untouched fact
// of the origin rewrite, and the step-origin guard's DAEMON-403 on the
// route itself (the existing guard's test pattern — a child process
// holding the step markers dials the real UDS face, so the kernel's
// SO_PEERCRED names exactly what the guard judges).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// gmi14AdoptScene answers a two-workspace daemon (proj → wsA, a foreign
// project → wsB), a tagged origin chat and a tagged adopter chat, and
// a live monitored WI submitted by the origin chat (owner alice).
func gmi14AdoptScene(t *testing.T) (td *testDaemon, wi, origin, adopter, wsA string) {
	t.Helper()
	dir := t.TempDir()
	wsA, wsB := filepath.Join(dir, "wsA"), filepath.Join(dir, "wsB")
	for _, d := range []string{wsA, wsB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := mapping.Build([]mapping.Entry{
		{Workspace: wsA, Projects: []string{"proj"}},
		{Workspace: wsB, Projects: []string{"gmi14other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	td = newTestDaemon(t, func(c *Config) { c.Mapping = p })
	td.hub.mu.Lock()
	td.hub.slugs = map[string]string{"wi_g14": "proj#14"}
	td.hub.mu.Unlock()
	origin, err = chatInitOf(td, "gmi14-origin", wsA)
	if err != nil {
		t.Fatal(err)
	}
	adopter, err = chatInitOf(td, "gmi14-adopter", wsA)
	if err != nil {
		t.Fatal(err)
	}
	td.client.Origin = &cli.SubmitOrigin{ChatID: origin, Owner: "alice"}
	td.submitClaim(t, "wi_g14")
	td.waitMonitored(t, "wi_g14")
	return td, "wi_g14", origin, adopter, wsA
}

// chatInitOf mints (or resumes) one chat over the real /chat/init route,
// tagged with the given workspace root, and answers its id.
func chatInitOf(td *testDaemon, session, workspace string) (string, error) {
	raw, err := td.client.ChatInit("pi", session, workspace)
	if err != nil {
		return "", err
	}
	var rec chatRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.ChatID == "" {
		return "", fmt.Errorf("chat init %q answer: %s (%v)", session, raw, err)
	}
	return rec.ChatID, nil
}

// TestAdoptRouteNoLocalJobRefused — a WI this daemon holds no job
// record for is refused DAEMON-404: adoption requires a locally held
// job record (running / paused / needs_input — every state whose
// decisions can still fire); a sealed-and-dropped WI has no
// wake-target role left to take over.
func TestAdoptRouteNoLocalJobRefused(t *testing.T) {
	td, _, _, adopter, _ := gmi14AdoptScene(t)
	_, err := td.client.ChatAdopt(adopter, "wi_neverheld")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonNotFound {
		t.Fatalf("an adopt of a WI with no local job must refuse DAEMON-404: %v", err)
	}
}

// TestAdoptRouteRewritesOriginOwnerUntouched — an eligible adopt
// rewrites the job record's origin chat to the adopter (the same field
// a resume rewrites) and leaves Origin.Owner untouched — owner routing
// keys on the owner, never the chat. The answer names the adopter.
func TestAdoptRouteRewritesOriginOwnerUntouched(t *testing.T) {
	td, wi, origin, adopter, _ := gmi14AdoptScene(t)
	// the current wake target unsubscribed: the role is there to take
	if _, err := td.client.ChatSubscribe(origin, wi, false); err != nil {
		t.Fatal(err)
	}
	ans, err := td.client.ChatAdopt(adopter, wi)
	if err != nil {
		t.Fatal(err)
	}
	if !ans.Adopted || ans.ChatID != adopter || ans.WI != wi || ans.Slug != "proj#14" {
		t.Fatalf("the adopt answer: %+v", ans)
	}
	raw, err := td.client.Status()
	if err != nil {
		t.Fatal(err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	for _, j := range st.Jobs {
		if j.WorkItemID != wi {
			continue
		}
		if j.Origin == nil || j.Origin.ChatID != adopter {
			t.Fatalf("the origin chat field must be rewritten to the adopter: %+v", j.Origin)
		}
		if j.Origin.Owner != "alice" {
			t.Fatalf("Origin.Owner is untouched by the rewrite: %+v", j.Origin)
		}
		return
	}
	t.Fatalf("%s never answered in status", wi)
}

// TestAdoptRouteRefusesStepProcesses — the route is a POST outside the
// step-write allowlist, so the step-origin guard refuses a step process
// tree with DAEMON-403 by construction: a step agent can never adopt.
func TestAdoptRouteRefusesStepProcesses(t *testing.T) {
	td := newTestDaemon(t, nil)
	out := stepOriginRequest(t, td, "POST", "/chats/chat_x/adopt",
		"PF_WI_ID=wi_step", "PF_DAEMON_ID="+td.cfg.MachineID)
	if !strings.Contains(out, "PFSO-STATUS 403") || !strings.Contains(out, "DAEMON-403") || !strings.Contains(out, "step process tree") {
		t.Fatalf("a step process's adopt was not refused: %s", out)
	}
}

// gmi14ChatDocOf reads the chat's chat-subs.json entry: its live
// subscription, its retained records and its mute marks.
func gmi14ChatDocOf(t *testing.T, td *testDaemon, chat, wi string) (sub, record struct {
	Notified []string `json:"notified"`
}, muted bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, chatSubsFile))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Chats map[string]struct {
			Subs map[string]struct {
				Notified []string `json:"notified"`
			} `json:"subs"`
			Records map[string]struct {
				Notified []string `json:"notified"`
			} `json:"records"`
			Muted map[string]bool `json:"muted"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Chats == nil {
		t.Fatalf("%s unparsable: %v", chatSubsFile, err)
	}
	c, ok := doc.Chats[chat]
	if !ok {
		t.Fatalf("chat %s is not recorded in %s", chat, chatSubsFile)
	}
	sub, record = c.Subs[wi], c.Records[wi]
	return sub, record, c.Muted[wi]
}

// TestAdoptRouteSelfReAdoptAfterUnsubscribe — a self re-adopt is
// idempotent only while the chat still holds its subscription: after
// the origin chat unsubscribed (and thereby muted) the WI, a bare
// id == target short-circuit would answer success while the chat
// stayed unsubscribed and muted, its retained record unconsumed and
// the scoped broadcast firing. The route must run the full surgery
// instead — Adopt with target == chat inherits the chat's own
// retained record (nothing replays), re-creates the subscription and
// clears the mute — and a second adopt, now over a held subscription,
// is the idempotent no-change success.
func TestAdoptRouteSelfReAdoptAfterUnsubscribe(t *testing.T) {
	td, wi, origin, _, _ := gmi14AdoptScene(t)
	// a delivered decision in the origin's record: q-1 printed once
	gmi14SeedQ(t, td, wi, "g14q1")
	if out := gmi14RunChat(t, td, origin, 5*time.Second); !strings.Contains(out, "needs_input g14q1") {
		t.Fatalf("the origin chat must be woken first: %q", out)
	}
	// the origin unsubscribed: its record is retained on the chat
	if _, err := td.client.ChatSubscribe(origin, wi, false); err != nil {
		t.Fatal(err)
	}
	ans, err := td.client.ChatAdopt(origin, wi)
	if err != nil || !ans.Adopted || ans.ChatID != origin {
		t.Fatalf("a self re-adopt after the unsubscribe must run the full surgery and succeed: %v %+v", err, ans)
	}
	sub, record, muted := gmi14ChatDocOf(t, td, origin, wi)
	if len(sub.Notified) != 1 || sub.Notified[0] != "needs_input:g14q1" {
		t.Fatalf("the re-created subscription must inherit the chat's own retained record (nothing replays): %+v", sub)
	}
	if record.Notified != nil {
		t.Fatalf("the retained record must be consumed by the adopt: %+v", record)
	}
	if muted {
		t.Fatal("the adopt must clear the chat's mute mark on the WI")
	}
	if origin2 := gmi14StatusOrigin(t, td, wi); origin2 != origin {
		t.Fatalf("the origin field stays the chat itself: %q", origin2)
	}
	// nothing replays: the still-pending q-1 stays delivered in the
	// inherited record — the chat's watcher prints no decision
	if out := gmi14RunChat(t, td, origin, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
		t.Fatalf("the inherited delivered key must not replay: %q", out)
	}
	// a second adopt, now over a held subscription: idempotent, no
	// state change (the record stays consumed, the notified keys stay)
	if ans, err := td.client.ChatAdopt(origin, wi); err != nil || !ans.Adopted {
		t.Fatalf("a self re-adopt over a held subscription is an idempotent success: %v %+v", err, ans)
	}
	sub2, record2, muted2 := gmi14ChatDocOf(t, td, origin, wi)
	if len(sub2.Notified) != 1 || sub2.Notified[0] != "needs_input:g14q1" || record2.Notified != nil || muted2 {
		t.Fatalf("the idempotent re-adopt changes nothing: sub %+v record %+v muted %v", sub2, record2, muted2)
	}
}
