package server

// wrap_nobundle_test.go — POST /api/v1/work-items/{id}/wrap: the terminal
// verdict on a work item with no bundle. queued/paused/blocked becomes
// wrapped (failed with "fail") with closed_at and a work_item_closed
// event; execution tickets, held work items and terminal ones are refused;
// the gate is cancel's (reporter, project maintainer, admin).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

type wrappedWI struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	ClosedAt *string `json:"closed_at"`
}

func createPlainWI(t *testing.T, e *srvEnv, key, goal string) string {
	t.Helper()
	var wi wrappedWI
	if code, raw := e.do(t, "POST", "/api/v1/work-items", key,
		map[string]any{"project": "alpha", "goal": goal}, &wi); code != 201 || wi.ID == "" {
		t.Fatalf("create: %d %s", code, raw)
	}
	return wi.ID
}

// closedEvents answers the work_item_closed events of id: payload plus
// the actor under "_actor".
func closedEvents(t *testing.T, e *srvEnv, id string) []map[string]any {
	t.Helper()
	rows, err := e.db.Query(`SELECT actor, payload FROM wi_events
		WHERE work_item_id=?1 AND event_type='work_item_closed'`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actor, payload string
		if err := rows.Scan(&actor, &payload); err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatal(err)
		}
		m["_actor"] = actor
		out = append(out, m)
	}
	return out
}

func TestHubWrapNoBundleWorkItem(t *testing.T) {
	e := newSrvEnv(t)
	var rootID string
	if err := e.db.QueryRow(`SELECT id FROM users WHERE email='root@example.com'`).Scan(&rootID); err != nil {
		t.Fatal(err)
	}

	id := createPlainWI(t, e, e.key, "design ticket")
	var got wrappedWI
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/wrap", e.key,
		map[string]any{"note": "shipped in v2.15"}, &got); code != 200 {
		t.Fatalf("wrap: %d %s", code, raw)
	}
	if got.Status != api.StatusWrapped || got.ClosedAt == nil || *got.ClosedAt == "" {
		t.Fatalf("wrap answered %+v, want wrapped with closed_at", got)
	}
	ev := closedEvents(t, e, id)
	if len(ev) != 1 || ev[0]["status"] != api.StatusWrapped || ev[0]["note"] != "shipped in v2.15" || ev[0]["_actor"] != rootID {
		t.Fatalf("work_item_closed events = %v", ev)
	}

	// paused and blocked registration tickets close too; the body is optional
	for _, st := range []string{api.StatusPaused, api.StatusBlocked} {
		x := createPlainWI(t, e, e.key, "parked "+st)
		if _, err := e.db.Exec(`UPDATE work_items SET status=?1 WHERE id=?2`, st, x); err != nil {
			t.Fatal(err)
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items/"+x+"/wrap", e.key, nil, &got); code != 200 || got.Status != api.StatusWrapped {
			t.Fatalf("wrap %s: %d %s", st, code, raw)
		}
		if ev := closedEvents(t, e, x); len(ev) != 1 || ev[0]["note"] != nil {
			t.Fatalf("%s: events = %v", st, ev)
		}
	}

	// the failed verdict
	f := createPlainWI(t, e, e.key, "decision dropped as wrong")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+f+"/wrap", e.key,
		map[string]any{"fail": true}, &got); code != 200 || got.Status != api.StatusFailed || got.ClosedAt == nil {
		t.Fatalf("wrap fail: %d %s", code, raw)
	}
	if ev := closedEvents(t, e, f); len(ev) != 1 || ev[0]["status"] != api.StatusFailed {
		t.Fatalf("fail events = %v", ev)
	}

	// already terminal: wrapped again, or canceled
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/wrap", e.key, map[string]any{}, nil)
	if c, reason := errEnvelope(t, raw); code != 410 || c != api.ErrCoreGone || reason != "TERMINAL_STATE" {
		t.Fatalf("re-wrap: %d %s", code, raw)
	}
	cx := createPlainWI(t, e, e.key, "abandoned")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+cx+"/cancel", e.key, map[string]any{}, nil); code != 200 {
		t.Fatalf("cancel: %d %s", code, raw)
	}
	code, raw = e.do(t, "POST", "/api/v1/work-items/"+cx+"/wrap", e.key, map[string]any{}, nil)
	if c, _ := errEnvelope(t, raw); code != 410 || c != api.ErrCoreGone {
		t.Fatalf("wrap of canceled: %d %s", code, raw)
	}
	if ev := closedEvents(t, e, cx); len(ev) != 0 {
		t.Fatalf("refused wrap recorded %v", ev)
	}
}

func TestHubWrapRefusesExecutionAndHeldWorkItems(t *testing.T) {
	e := newSrvEnv(t)

	// a bound bundle, never claimed: the daemon wraps execution tickets
	ex := createExecWIOverHTTP(t, e, "execution ticket")
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+ex+"/wrap", e.key, map[string]any{}, nil)
	if c, _ := errEnvelope(t, raw); code != 409 || c != api.ErrCoreConflict || !strings.Contains(string(raw), "execution ticket") {
		t.Fatalf("wrap of an execution ticket: %d %s", code, raw)
	}

	// a session holds it (running)
	held := createExecWIOverHTTP(t, e, "running ticket")
	if code, raw, _ := sessClaim(t, e, held, strings.Repeat("h", 32), "k-held"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	code, raw = e.do(t, "POST", "/api/v1/work-items/"+held+"/wrap", e.key, map[string]any{}, nil)
	if c, _ := errEnvelope(t, raw); code != 409 || c != api.ErrCoreConflict || !strings.Contains(string(raw), "session holds") {
		t.Fatalf("wrap of a held work item: %d %s", code, raw)
	}

	for id, want := range map[string]string{ex: api.StatusQueued, held: api.StatusRunning} {
		var st string
		if err := e.db.QueryRow(`SELECT status FROM work_items WHERE id=?1`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st != want || len(closedEvents(t, e, id)) != 0 {
			t.Fatalf("%s changed by a refused wrap: status %s, want %s", id, st, want)
		}
	}
}

// TestHubWrapEpicWaitsForChildren: an epic (a ticket with children) wraps
// only once every child is terminal.
func TestHubWrapEpicWaitsForChildren(t *testing.T) {
	e := newSrvEnv(t)
	epic := createPlainWI(t, e, e.key, "epic")
	var kids [2]string
	for i := range kids {
		var wi wrappedWI
		if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
			map[string]any{"project": "alpha", "goal": "child", "parent_id": epic}, &wi); code != 201 {
			t.Fatalf("child: %d %s", code, raw)
		}
		kids[i] = wi.ID
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+kids[0]+"/wrap", e.key, nil, nil); code != 200 {
		t.Fatalf("wrap child: %d %s", code, raw)
	}
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+epic+"/wrap", e.key, nil, nil)
	if c, reason := errEnvelope(t, raw); code != 409 || c != api.ErrCoreConflict || reason != "OPEN_CHILDREN" ||
		!strings.Contains(string(raw), "1 child ticket(s)") {
		t.Fatalf("wrap of an epic with an open child: %d %s", code, raw)
	}
	if ev := closedEvents(t, e, epic); len(ev) != 0 {
		t.Fatalf("refused wrap recorded %v", ev)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+kids[1]+"/cancel", e.key, map[string]any{}, nil); code != 200 {
		t.Fatalf("cancel child: %d %s", code, raw)
	}
	var got wrappedWI
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+epic+"/wrap", e.key, nil, &got); code != 200 || got.Status != api.StatusWrapped {
		t.Fatalf("wrap of an epic with terminal children: %d %s", code, raw)
	}
}

func TestHubWrapAuthorizationMatchesCancel(t *testing.T) {
	e := newSrvEnv(t)
	mk := func(name string) (string, string) {
		u, err := e.admin.CreateUser(name, name+"@example.com", "writer", "human")
		if err != nil {
			t.Fatal(err)
		}
		k, err := e.admin.CreateAPIKey(u.ID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID, k.Plain
	}
	repID, repKey := mk("wraprep")
	otherID, otherKey := mk("wrapmember")
	maintID, maintKey := mk("wrapmaint")
	_, outKey := mk("wrapout")
	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{
			{UserID: repID, Role: "writer"},
			{UserID: otherID, Role: "writer"},
			{UserID: maintID, Role: "maintainer"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	id := createPlainWI(t, e, repKey, "reporter's ticket")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/wrap", otherKey, map[string]any{}, nil); code != 403 {
		t.Fatalf("plain member wrap: %d %s", code, raw)
	}
	// An outsider is concealed — the missing-work-item answer
	//, byte-identical to an unknown reference.
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/wrap", outKey, map[string]any{}, nil)
	if code != 404 || errCodeOf(raw) != api.ErrCoreNotFound {
		t.Fatalf("outsider wrap: %d %s", code, raw)
	}
	if ev := closedEvents(t, e, id); len(ev) != 0 {
		t.Fatalf("refused wrap recorded %v", ev)
	}
	var got wrappedWI
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/wrap", repKey, map[string]any{}, &got); code != 200 || got.Status != api.StatusWrapped {
		t.Fatalf("reporter wrap: %d %s", code, raw)
	}
	id2 := createPlainWI(t, e, repKey, "maintainer wraps")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id2+"/wrap", maintKey, map[string]any{}, &got); code != 200 || got.Status != api.StatusWrapped {
		t.Fatalf("maintainer wrap: %d %s", code, raw)
	}
	id3 := createPlainWI(t, e, repKey, "admin wraps")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id3+"/wrap", e.key, map[string]any{}, &got); code != 200 || got.Status != api.StatusWrapped {
		t.Fatalf("admin wrap: %d %s", code, raw)
	}
}
