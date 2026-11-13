package stepscripts

// hubpool_test.go (workflow contract 4, design #483 §5.3): the pool the
// integrate lane's commands read through the step's hub when no --pool is
// named — the reads `pf step hub list` / `get` they make, what they take
// a refusal for, and collect, triage and close on the hub's pool exactly
// as on the same pool written to a file. The daemon side (the real CLI,
// /step/call, the hub proxy, the pool of one aihub fixture) is the daemon
// package's TestC4HubPoolIsTheFixturesPool.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// fakeHubReads answers `pf step hub list/get` from the fixture's landed
// items (as aihub carries them: status wrapped, attrs integration and
// waivers) and the lane's batches, recording every call.
type fakeHubReads struct {
	mu      sync.Mutex
	items   []api.WorkItem
	batches []api.WorkItem
	tickets []api.WorkItem // read by get only
	calls   []string
	fail    error
}

func (h *fakeHubReads) run(_ context.Context, args []string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, strings.Join(args, " "))
	if h.fail != nil {
		return nil, h.fail
	}
	switch args[0] {
	case "list":
		f := map[string]string{}
		for i := 1; i+1 < len(args); i += 2 {
			f[args[i]] = args[i+1]
		}
		list := h.items
		if f["--bundle"] != "" {
			list = h.batches
		}
		return json.Marshal(map[string]any{"items": list})
	case "get":
		for _, it := range append(append(append([]api.WorkItem{}, h.items...), h.batches...), h.tickets...) {
			if it.ID == args[1] || it.Slug == args[1] {
				return json.Marshal(it)
			}
		}
		return nil, &api.Error{Code: api.ErrDaemonNotFound, Detail: "get: work item " + args[1] + " not found in project p"}
	}
	return nil, fmt.Errorf("no verb %s", args[0])
}

// hubOf answers the fixture's landed items as the hub serves them, and
// writes the capsule's identity (the project and the lane the command
// reads the pool for).
func (f *batchFixture) hubOf(mod func(*fakeHubReads)) *fakeHubReads {
	f.t.Helper()
	writeFile(f.t, f.dir, "capsule.json", `{"work_item_id":"wi_batch","project":"p","bundle_name":"integrate"}`)
	h := &fakeHubReads{}
	for _, it := range f.items {
		landed := it.Landed
		attrs := map[string]any{"integration": it.Integration}
		if it.Waivers != nil {
			attrs["waivers"] = it.Waivers
		}
		raw, _ := json.Marshal(attrs)
		var a map[string]any
		_ = json.Unmarshal(raw, &a)
		h.items = append(h.items, api.WorkItem{ID: it.ID, Slug: it.Slug, ProjectName: "p", Status: api.StatusWrapped, ClosedAt: &landed, Attrs: a})
	}
	if mod != nil {
		mod(h)
	}
	return h
}

// TestIntegrateCollectReadsThePoolThroughTheHub: collect without --pool
// takes the batch from the pool the hub's reads give — the same batch as
// from the same pool in a file (the items, the skipped, the holds, the
// red heads); an item carrying an owner waiver (attrs.waivers) is waived
// in the batch and on close's record. A capsule naming no project or lane,
// or a hub that refuses, is exit 2 naming why.
func TestIntegrateCollectReadsThePoolThroughTheHub(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	f.land("wi_b", "p#2", map[string]string{"pkg/b.go": "package pkg\n"})
	f.items[1].Waivers = []any{map[string]any{"gate": "verification", "words": "这次不算"}}
	f.items[0].Integration["red_rounds"], f.items[0].Integration["fix_tickets"] = 1.0, []any{"p#9", "p#10"}
	red := map[string]any{"result": "red", "main": map[string]any{"app": "0000000000000000000000000000000000000001"}}
	if exit, _, errb := f.collect(f.pool(func(s *poolSnapshot) {
		s.RedHeads["app"] = "0000000000000000000000000000000000000001"
		s.Tickets = map[string]poolTicket{"p#9": {Status: "running"}, "p#10": {}}
	})); exit != 0 {
		t.Fatalf("collect from the file: %d\n%s", exit, errb)
	}
	fromFile := readResult(t, f.art("batch.json"))
	h := f.hubOf(func(h *fakeHubReads) {
		h.tickets = []api.WorkItem{{ID: "wi_t9", Slug: "p#9", Status: "running"}}
		h.batches = []api.WorkItem{{ID: "wi_old", Seq: 1, Status: api.StatusWrapped, Attrs: map[string]any{"integration_batch": red}}, {ID: "wi_batch", Seq: 2, Status: "running"}}
	})
	exit, _, errb := invokeScript(t, NameIntegrate, []string{"collect", "--capsule", f.dir, "--json", "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(t), h)
	if exit != 0 {
		t.Fatalf("collect through the hub: %d\n%s", exit, errb)
	}
	fromHub := readResult(t, f.art("batch.json"))
	var dh, df collectDetails
	detailsOf(t, fromHub, &dh)
	detailsOf(t, fromFile, &df)
	dh.TakenAt, df.TakenAt = "", ""
	gh, _ := json.Marshal(dh)
	gf, _ := json.Marshal(df)
	if string(gh) != string(gf) {
		t.Fatalf("the batch through the hub differs from the batch on the same pool's file:\n hub %s\nfile %s", gh, gf)
	}
	if len(dh.Holds) != 1 || dh.Holds[0].WI != "wi_a" || strings.Join(dh.Holds[0].FixTickets, ",") != "p#9,p#10" || dh.RedHeads["app"] == "" {
		t.Fatalf("the hub's pool carried the hold and the red head: %s", gh)
	}
	waived := false
	for _, it := range dh.Items {
		waived = waived || (it.ID == "wi_b" && it.Waived)
	}
	if !waived {
		t.Fatalf("the item with an owner waiver is waived in the batch: %s", gh)
	}
	if got := strings.Join(h.calls, " | "); !strings.HasPrefix(got, "list --status wrapped --attr integration.state=landed --limit 1000 | get p#9 | get p#10 | list --bundle integrate --limit 200") {
		t.Fatalf("the hub's reads: %s", got)
	}
	// close on the hub's pool: the waived item on the record
	writeDecision(t, f, "empty")
	args := append([]string{}, f.closeArgs("")...)
	for i := range args {
		if args[i] == "--pool" {
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	exit, _, errb = invokeScript(t, NameIntegrate, args, batchEnv(), newFakeRuntime(t), h)
	if exit != 0 {
		t.Fatalf("close through the hub: %d\n%s", exit, errb)
	}
	var cd closeDetails
	detailsOf(t, readResult(t, f.art("close.json")), &cd)
	if fmt.Sprint(cd.IntegrationBatch["waived"]) != "[wi_b]" {
		t.Fatalf("the record's waived: %v", cd.IntegrationBatch)
	}
	// the capsule names no lane: nothing can be read
	writeFile(t, f.dir, "capsule.json", `{"work_item_id":"wi_batch","project":"p"}`)
	exit, _, _ = invokeScript(t, NameIntegrate, []string{"collect", "--capsule", f.dir, "--json", "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(t), h)
	if res := readResult(t, f.art("batch.json")); exit != 2 || !strings.Contains(res.Summary, "names no project and lane") {
		t.Fatalf("a capsule without its lane: exit %d %q", exit, res.Summary)
	}
	// the hub refuses: not judged, the refusal named
	f.hubOf(nil)
	h.fail = &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "aihub unreachable: dial tcp"}
	exit, _, _ = invokeScript(t, NameIntegrate, []string{"collect", "--capsule", f.dir, "--json", "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(t), h)
	if res := readResult(t, f.art("batch.json")); exit != 2 || !strings.Contains(res.Summary, "aihub unreachable") || !strings.Contains(res.Summary, "through the step's hub") {
		t.Fatalf("a hub refusal: exit %d %q", exit, res.Summary)
	}
}

// TestGateTriageReadsThePoolThroughTheHub: gate.py --triage without
// --pool attributes the red unit to the landed item's merge from the pool
// the step's hub gives (`pf --json step hub list`, the fake pf answering
// the fixture's landed items as the hub serves them), as
// TestGateTriageAttributesTheRedToItsMerge does from a file.
func TestGateTriageReadsThePoolThroughTheHub(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	f.land("wi_b", "p#2", map[string]string{"broken": "x\n"})
	h := f.hubOf(nil)
	if exit, _, errb := invokeScript(t, NameIntegrate, []string{"collect", "--capsule", f.dir, "--json", "--out", f.art("batch.json")}, batchEnv(), newFakeRuntime(t), h); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	if exit, _, errb := f.gate(); exit != 1 {
		t.Fatalf("the gate: exit %d\n%s", exit, errb)
	}
	h.calls = nil
	exit, _, errb := invokeScript(t, NameGate, f.triageArgs(""), batchEnv(), newFakeRuntime(t), h)
	if exit != 1 {
		t.Fatalf("the triage: exit %d\n%s", exit, errb)
	}
	var td triageDetails
	detailsOf(t, readResult(t, f.art("triage.json")), &td)
	if len(td.Units) != 1 || td.Units[0].Class != "attributed" || td.Units[0].Item != "wi_b" {
		t.Fatalf("the triage on the hub's pool: %+v", td)
	}
	if got := strings.Join(h.calls, " | "); got != "list --status wrapped --attr integration.state=landed --limit 1000" {
		t.Fatalf("the hub reads: %q", got)
	}
	// the hub unreachable: not judged
	h.fail = &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "aihub unreachable"}
	exit, _, _ = invokeScript(t, NameGate, f.triageArgs(""), batchEnv(), newFakeRuntime(t), h)
	if res := readResult(t, f.art("triage.json")); exit != 2 || !strings.Contains(res.Summary, "aihub unreachable") {
		t.Fatalf("a triage whose pool cannot be read: exit %d %q", exit, res.Summary)
	}
}
