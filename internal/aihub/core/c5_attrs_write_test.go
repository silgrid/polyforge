package core

// c5_attrs_write_test.go (workflow contract 1, design §3.4):
// aihub keeps no lane's business rules — the generic attrs write replaces
// one top-level key whole on a work item of any status (a terminal one
// included), fenced by its `if` conditions (compare and swap: a mismatch
// is CORE-409 ATTRS_IF_MISMATCH and writes nothing, and of racing writers
// fenced on the same value exactly one wins), refuses the keys Core
// writes itself, and records one attrs_written event per write with the
// key, the machine and the owner message; the list filters on attrs paths.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func attrsReq(key, value string, ifs map[string]string) api.AttrsWriteRequest {
	req := api.AttrsWriteRequest{Key: key, Value: json.RawMessage(value)}
	if ifs != nil {
		req.If = map[string]json.RawMessage{}
		for p, v := range ifs {
			req.If[p] = json.RawMessage(v)
		}
	}
	return req
}

func TestC5WriteAttrReplacesOneKeyOnAnyStatus(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "attrs goal", map[string]any{"keep": "me", "integration": map[string]any{"state": "landed", "repos": []any{"app"}}}, "")
	wi, err := env.WIs.WriteAttr(id, attrsReq("integration", `{"state":"merged"}`, nil), env.UserID, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(wi.Attrs["integration"]); string(got) != `{"state":"merged"}` || wi.Attrs["keep"] != "me" {
		t.Fatalf("the key is replaced whole and every other key kept: %v", wi.Attrs)
	}
	// a terminal work item is written too: a later lane moves its record on
	mustSessionClaim(t, m, env, id, "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a")
	if err := m.End(id, api.SessionEndRequest{SessionID: "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a", Status: api.StatusWrapped}, env.UserID); err != nil {
		t.Fatal(err)
	}
	wi, err = env.WIs.WriteAttr(id, attrsReq("note", `[1,"two",{"three":3}]`, nil), env.UserID, "m1")
	if err != nil || wi.Status != api.StatusWrapped {
		t.Fatalf("a wrapped work item's attrs: %v (%v)", err, wi)
	}
	if got, _ := json.Marshal(wi.Attrs["note"]); string(got) != `[1,"two",{"three":3}]` {
		t.Fatalf("any JSON value: %s", got)
	}
	// null deletes the key
	wi, err = env.WIs.WriteAttr(id, attrsReq("note", `null`, nil), env.UserID, "m1")
	if _, has := wi.Attrs["note"]; err != nil || has {
		t.Fatalf("null deletes the key: %v %v", err, wi.Attrs)
	}
	canceled := mustCreateExecWI(t, env, "canceled goal", nil, "")
	if _, err := env.WIs.Cancel(canceled, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.WriteAttr(canceled, attrsReq("note", `"x"`, nil), env.UserID, "m1"); err != nil {
		t.Fatalf("a canceled work item's attrs: %v", err)
	}
}

func TestC5WriteAttrIfIsACompareAndSwap(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "cas goal", map[string]any{"integration": map[string]any{"state": "landed", "red_rounds": 1, "batch": map[string]any{"n": "p#9"}}}, "")
	for _, c := range []struct {
		name string
		ifs  map[string]string
	}{
		{"another state", map[string]string{"integration.state": `"merged"`}},
		{"a number as a string", map[string]string{"integration.red_rounds": `"1"`}},
		{"one of two conditions", map[string]string{"integration.state": `"landed"`, "integration.red_rounds": `2`}},
		{"an absent path is null, not a value", map[string]string{"integration.green_tag": `""`}},
		{"a path through a non-object", map[string]string{"integration.state.x": `"landed"`}},
		{"an object differing", map[string]string{"integration.batch": `{"n":"p#8"}`}},
	} {
		_, err := env.WIs.WriteAttr(id, attrsReq("integration", `{"state":"merged"}`, c.ifs), env.UserID, "m1")
		if !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != ReasonAttrsIfMismatch {
			t.Fatalf("%s: want CORE-409 %s, got %v", c.name, ReasonAttrsIfMismatch, err)
		}
	}
	wi, _ := env.WIs.Get(id, GetOptions{})
	if in, _ := wi.Attrs["integration"].(map[string]any); in["state"] != "landed" {
		t.Fatalf("a refused write writes nothing: %v", wi.Attrs)
	}
	evs, _ := env.WIs.Events(id)
	for _, e := range evs {
		if e.Type == "attrs_written" {
			t.Fatalf("a refused write records no event: %+v", e)
		}
	}
	// every condition holding: the write is made
	ok := map[string]string{"integration.state": `"landed"`, "integration.red_rounds": `1`, "integration.batch": `{"n":"p#9"}`, "integration.green_tag": `null`, "absent": `null`}
	if _, err := env.WIs.WriteAttr(id, attrsReq("integration", `{"state":"merged"}`, ok), env.UserID, "m1"); err != nil {
		t.Fatalf("every condition holds: %v", err)
	}
	// the record left landed: the same fenced write is refused now
	if _, err := env.WIs.WriteAttr(id, attrsReq("integration", `{"state":"reverted"}`, map[string]string{"integration.state": `"landed"`}), env.UserID, "m1"); !IsCode(err, api.ErrCoreConflict) {
		t.Fatalf("a write from the state the record left: %v", err)
	}
}

// TestC5WriteAttrConcurrentCASOneWins: writers racing on the same fence —
// each moving the record from landed — never both win: exactly one write
// is made, every other is CORE-409, and the record is the winner's.
func TestC5WriteAttrConcurrentCASOneWins(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "race goal", map[string]any{"integration": map[string]any{"state": "landed"}}, "")
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = env.WIs.WriteAttr(id, attrsReq("integration", fmt.Sprintf(`{"state":"merged","by":%d}`, i), map[string]string{"integration.state": `"landed"`}), env.UserID, "m1")
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner >= 0:
			t.Fatalf("two writers won the same fence: %d and %d", winner, i)
		case err == nil:
			winner = i
		case !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != ReasonAttrsIfMismatch:
			t.Fatalf("writer %d: want CORE-409, got %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no writer won")
	}
	wi, _ := env.WIs.Get(id, GetOptions{})
	if in, _ := wi.Attrs["integration"].(map[string]any); in["by"] != float64(winner) {
		t.Fatalf("the record is the winner's (%d): %v", winner, in)
	}
	evs, _ := env.WIs.Events(id)
	count := 0
	for _, e := range evs {
		if e.Type == "attrs_written" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("one write, one event: %d", count)
	}
}

func TestC5WriteAttrRecordsTheWrite(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "event goal", nil, "")
	om := &api.OwnerMessage{Words: "清掉它。\n  原样。", By: "u_alice", At: "2026-11-11T10:00:00Z"}
	req := attrsReq("integration", `{"state":"merged"}`, nil)
	req.OwnerMessage = om
	if _, err := env.WIs.WriteAttr(id, req, env.UserID, "m7"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.WriteAttr(id, attrsReq("note", `1`, nil), env.UserID, "m8"); err != nil {
		t.Fatal(err)
	}
	evs, _ := env.WIs.Events(id)
	var got []map[string]any
	for _, e := range evs {
		if e.Type != "attrs_written" {
			continue
		}
		if e.Actor != env.UserID {
			t.Fatalf("the event names the actor: %+v", e)
		}
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		got = append(got, p)
	}
	if len(got) != 2 {
		t.Fatalf("two writes, two events: %v", got)
	}
	want0 := map[string]any{"key": "integration", "machine_id": "m7", "owner_message": map[string]any{"words": om.Words, "by": om.By, "at": om.At}}
	if a, b := fmt.Sprint(got[0]), fmt.Sprint(want0); a != b {
		t.Fatalf("the write on the owner's word: %s, want %s", a, b)
	}
	if _, has := got[1]["owner_message"]; has || got[1]["key"] != "note" || got[1]["machine_id"] != "m8" {
		t.Fatalf("a plain write: %v", got[1])
	}
}

func TestC5WriteAttrRefusals(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "refusal goal", nil, "")
	for _, c := range []struct {
		name string
		req  api.AttrsWriteRequest
		code string
	}{
		{"an empty key", attrsReq("", `1`, nil), api.ErrCoreBadInput},
		{"an upper-case key", attrsReq("Integration", `1`, nil), api.ErrCoreBadInput},
		{"a dotted key", attrsReq("integration.state", `1`, nil), api.ErrCoreBadInput},
		{"a key over 64", attrsReq("a"+strings.Repeat("b", 64), `1`, nil), api.ErrCoreBadInput},
		{"the blocked seal", attrsReq("blocked_seal", `{}`, nil), api.ErrCoreBadInput},
		{"no value", attrsReq("note", ``, nil), api.ErrCoreBadInput},
		{"a value not JSON", attrsReq("note", `{`, nil), api.ErrCoreBadInput},
		{"a malformed path", attrsReq("note", `1`, map[string]string{"a..b": `1`}), api.ErrCoreBadInput},
		{"an upper-case path", attrsReq("note", `1`, map[string]string{"Note": `1`}), api.ErrCoreBadInput},
		{"a condition with no value", attrsReq("note", `1`, map[string]string{"note": ``}), api.ErrCoreBadInput},
		{"an owner message without its words", func() api.AttrsWriteRequest {
			r := attrsReq("note", `1`, nil)
			r.OwnerMessage = &api.OwnerMessage{By: "owner", At: "t"}
			return r
		}(), api.ErrCoreBadInput},
	} {
		if _, err := env.WIs.WriteAttr(id, c.req, env.UserID, "m1"); !IsCode(err, c.code) {
			t.Fatalf("%s: want %s, got %v", c.name, c.code, err)
		}
	}
	if _, err := env.WIs.WriteAttr("wi_nope", attrsReq("note", `1`, nil), env.UserID, "m1"); !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("an unknown work item: %v", err)
	}
	wi, _ := env.WIs.Get(id, GetOptions{})
	if len(wi.Attrs) != 0 {
		t.Fatalf("a refused write writes nothing: %v", wi.Attrs)
	}
}

// TestC5ListFiltersOnAttrsPaths: each filter narrows to the work items
// whose attr at the path is the string; several filters all hold; a
// number is no string; a path's segment may start with a digit.
func TestC5ListFiltersOnAttrsPaths(t *testing.T) {
	env := newEnv(t)
	a := mustCreateExecWI(t, env, "a", map[string]any{"integration": map[string]any{"state": "landed", "batch": "p#9"}}, "")
	b := mustCreateExecWI(t, env, "b", map[string]any{"integration": map[string]any{"state": "landed", "batch": "p#8"}}, "")
	c := mustCreateExecWI(t, env, "c", map[string]any{"integration": map[string]any{"state": "merged"}, "n": 1, "x": map[string]any{"2d": "y"}}, "")
	mustCreateExecWI(t, env, "d", nil, "")
	ids := func(f ...string) string {
		t.Helper()
		ms, err := ParseAttrFilters(f)
		if err != nil {
			t.Fatal(err)
		}
		items, err := env.WIs.List(ListFilters{Project: "alpha", Attrs: ms})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.ID)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		filters []string
		want    string
	}{
		{[]string{"integration.state:landed"}, b + "," + a},
		{[]string{"integration.state:landed", "integration.batch:p#9"}, a},
		{[]string{"integration.state:merged"}, c},
		{[]string{"integration.state:"}, ""},
		{[]string{"n:1"}, ""},
		{[]string{"x.2d:y"}, c},
		{[]string{"integration.state:reverted"}, ""},
	} {
		if got := ids(tc.filters...); got != tc.want {
			t.Fatalf("%v: %s, want %s", tc.filters, got, tc.want)
		}
	}
	for _, bad := range []string{"integration.state", "Integration.state:landed", "a..b:x", ".a:x", "$.a:x", "a'b:x"} {
		if _, err := ParseAttrFilters([]string{bad}); !IsCode(err, api.ErrCoreBadInput) {
			t.Fatalf("%q: want CORE-400, got %v", bad, err)
		}
	}
}

// TestC5WriteAttrWholeRecordFence: two writes built in one round from one
// read of a record — the integrate lane's scripts fence each on the whole
// record as read (`if: {integration: <record>}`, pfstep/integration.py) —
// never both land: the first replaces the record, the second meets a
// record that is no longer the one it was built from and is CORE-409
// ATTRS_IF_MISMATCH, writing nothing, so the first one's keys stand. A
// fence on the state alone would have let the second replace the record.
func TestC5WriteAttrWholeRecordFence(t *testing.T) {
	env := newEnv(t)
	read := `{"state":"landed","repos":[{"repo":"app","pr":"https://forge.invalid/pr/1"}],"merge_commits":{"app":["m1"]},"red_rounds":1,"fix_tickets":["p#9"]}`
	var rec map[string]any
	if err := json.Unmarshal([]byte(read), &rec); err != nil {
		t.Fatal(err)
	}
	id := mustCreateExecWI(t, env, "cas goal", map[string]any{"integration": rec}, "")
	fence := map[string]string{"integration": read}
	fix := `{"state":"landed","repos":[{"repo":"app","pr":"https://forge.invalid/pr/1"}],"merge_commits":{"app":["m1"]},"red_rounds":1,"fix_tickets":["p#9","p#10"]}`
	green := `{"state":"merged","repos":[{"repo":"app","pr":"https://forge.invalid/pr/1"}],"merge_commits":{"app":["m1"]},"red_rounds":1,"fix_tickets":["p#9"],"green_tag":"green/20261112-1"}`
	if _, err := env.WIs.WriteAttr(id, attrsReq("integration", fix, fence), env.UserID, "m1"); err != nil {
		t.Fatalf("the first write from the read: %v", err)
	}
	_, err := env.WIs.WriteAttr(id, attrsReq("integration", green, fence), env.UserID, "m1")
	if !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != ReasonAttrsIfMismatch {
		t.Fatalf("the second write from the same read: want CORE-409 %s, got %v", ReasonAttrsIfMismatch, err)
	}
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(wi.Attrs["integration"]); !strings.Contains(string(got), `"fix_tickets":["p#9","p#10"]`) || strings.Contains(string(got), "green_tag") {
		t.Fatalf("the first write's keys stand, the second wrote nothing: %s", got)
	}
}
