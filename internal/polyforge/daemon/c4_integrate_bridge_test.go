package daemon

// c4_integrate_bridge_test.go (workflow contract 4 C2, design #483 §5.3):
// the integrate lane's step commands no longer get a pool the daemon
// rendered, nor hand effects to the daemon — they read the pool through
// the step's hub and register generic hub writes. On one aihub fixture:
//
//   - the pool a step script computes through `pf step hub list/get`
//     (integrate.py's read over the real `pf` CLI, the daemon's /step/call
//     and its hub proxy) is the fixture's pool (fixturePool) field by
//     field;
//   - the writes collect, revert, full-gate and close register with `pf
//     step hub batch`, made by the pending-writes executor through the
//     daemon's sink, leave aihub's work items exactly as the contract 3
//     effects did (testdata/c4_batch_writes.golden: the work items the
//     effects applier left, which this test compared side by side until
//     the effects package left with workflow contract 4's stage C3, and —
//     since workflow contract 1 — the generic attrs writes that leave
//     them so).
//
// Beside them, the two C1 rulings these steps rely on: an empty batch is
// a recorded no-op (R4), and a write on the owner's word carries the
// owner message it rests on — its writer, stamp and words go to aihub
// with the write (R5).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// c4Aihub is a stateful aihub fixture: the work items, served by aihub's
// list (its attr=<path>:<value> filters included) and get routes, and the
// two writes the integrate lane makes (create, the fenced attrs write),
// each recorded.
type c4Aihub struct {
	mu     sync.Mutex
	items  []map[string]any
	seq    int
	writes []string
}

func newC4Aihub(t *testing.T, items []map[string]any) (*c4Aihub, *httptest.Server) {
	t.Helper()
	h := &c4Aihub{}
	raw, _ := json.Marshal(items)
	if err := json.Unmarshal(raw, &h.items); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *c4Aihub) find(ref string) map[string]any {
	for _, it := range h.items {
		if it["id"] == ref || it["slug"] == ref {
			return it
		}
	}
	return nil
}

func attrsOf(it map[string]any) map[string]any {
	attrs, _ := it["attrs"].(map[string]any)
	if attrs == nil {
		attrs = map[string]any{}
		it["attrs"] = attrs
	}
	return attrs
}

// attrAt answers the value at an attrs dot path (nil when absent), as
// aihub reads it.
func attrAt(it map[string]any, path string) any {
	var cur any = attrsOf(it)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[seg]
	}
	return cur
}

func (h *c4Aihub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	refuse := func(status int, code, detail string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "detail": detail})
	}
	const base = "/api/v1/work-items"
	rest := strings.TrimPrefix(r.URL.Path, base)
	var body map[string]any
	if r.Method == "POST" || r.Method == "PATCH" {
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(r.Body)
		_ = json.Unmarshal(raw.Bytes(), &body)
		canon, _ := json.Marshal(body)
		h.writes = append(h.writes, r.Method+" "+r.URL.Path+" "+string(canon))
	}
	switch {
	case r.Method == "GET" && rest == "":
		q := r.URL.Query()
		out := []map[string]any{}
		for _, it := range h.items {
			if p := q.Get("project"); p != "" && it["project"] != p {
				continue
			}
			if s := q.Get("status"); s != "" && it["status"] != s {
				continue
			}
			if b := q.Get("bundle"); b != "" && it["bundle_name"] != b {
				continue
			}
			match := true
			for _, f := range q["attr"] {
				path, value, _ := strings.Cut(f, ":")
				match = match && attrAt(it, path) == value
			}
			if !match {
				continue
			}
			out = append(out, it)
		}
		if n, _ := strconv.Atoi(q.Get("limit")); n > 0 && len(out) > n {
			out = out[:n]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
	case r.Method == "GET":
		it := h.find(strings.TrimPrefix(rest, "/"))
		if it == nil {
			refuse(404, api.ErrCoreNotFound, "no work item "+rest)
			return
		}
		_ = json.NewEncoder(w).Encode(it)
	case r.Method == "POST" && rest == "":
		h.seq++
		bundle, _ := body["bundle_name"].(string)
		it := map[string]any{"id": fmt.Sprintf("wi_new%d", h.seq), "slug": fmt.Sprintf("proj#%d", 100+h.seq), "seq": 100 + h.seq,
			"project": body["project"], "goal": body["goal"], "content": body["content"], "priority": body["priority"],
			"labels": body["labels"], "bundle_name": bundle, "status": "queued", "attrs": map[string]any{}}
		h.items = append(h.items, it)
		_ = json.NewEncoder(w).Encode(it)
	case r.Method == "PATCH" && strings.HasSuffix(rest, "/attrs"):
		it := h.find(strings.TrimSuffix(strings.TrimPrefix(rest, "/"), "/attrs"))
		if it == nil {
			refuse(404, api.ErrCoreNotFound, "no work item")
			return
		}
		conds, _ := body["if"].(map[string]any)
		for path, want := range conds {
			if !reflect.DeepEqual(attrAt(it, path), want) {
				refuse(409, api.ErrCoreConflict, "attrs."+path+" is not "+fmt.Sprint(want))
				return
			}
		}
		key, _ := body["key"].(string)
		if body["value"] == nil {
			delete(attrsOf(it), key)
		} else {
			attrsOf(it)[key] = body["value"]
		}
		_ = json.NewEncoder(w).Encode(it)
	default:
		refuse(404, api.ErrCoreNotFound, r.Method+" "+r.URL.Path)
	}
}

// state answers the fixture's work items, canonically.
func (h *c4Aihub) state() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	raw, _ := json.Marshal(h.items)
	return string(raw)
}

// sortedWrites answers the recorded writes, sorted.
func (h *c4Aihub) sortedWrites() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string{}, h.writes...)
	sort.Strings(out)
	return out
}

// c4HubDaemon is a daemon bound to the fixture: its own aihub client
// (machine-signed, as the attrs route needs).
func c4HubDaemon(t *testing.T, srv *httptest.Server) *testDaemon {
	t.Helper()
	c := aihubclient.New(srv.URL, "k")
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetMachineIdentity("m_test", key)
	return newTestDaemon(t, func(cfg *Config) { cfg.Server, cfg.APIKey = srv.URL, "k"; cfg.Hub = c })
}

// c4BatchFixture is a claimed integration batch whose step calls reach
// the fixture: the fake hub's generic reads forwarded to it.
func c4BatchFixture(t *testing.T, srv *httptest.Server) *c4Fixture {
	t.Helper()
	f := newC4Fixture(t, "wi_batch")
	c := aihubclient.New(srv.URL, "k")
	f.td.hub.mu.Lock()
	f.td.hub.memDo = func(method, path string, body, out any) error { return c.Do(method, path, body, out) }
	f.td.hub.mu.Unlock()
	start := "2026-11-11T09:00:00Z"
	steps := map[string]*capsule.Step{}
	for _, s := range []string{"collect", "revert", "full-gate", "close"} {
		steps[s] = &capsule.Step{Status: capsule.StepInProgress, Round: 1, StepStartedAt: start}
	}
	f.writeExec(t, steps)
	return f
}

// inStep puts this process in the fixture's step, as a step script's `pf`
// finds it: the work item, the daemon and its socket, no aihub identity of
// its own (a script's `pf --json step hub` inherits them).
func inStep(t *testing.T, f *c4Fixture) {
	for _, k := range []string{"PF_SERVER", "PF_API_KEY", "PF_USER_ID", "PF_CHAT_ID", "PF_TASK_ID", "PF_PROFILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("PF_WI_ID", f.wi)
	t.Setenv("PF_DAEMON_ID", "d-test")
	t.Setenv("PF_DAEMON_SOCKET", f.td.sock)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// poolFixture is the project's work items as aihub holds them: landed
// items (waived, held with fix tickets in every state), items the pool
// never takes (another project's, one merged, one not wrapped), and the
// lane's batches (red, green, failed, canceled, unfinished).
func poolFixture() []map[string]any {
	landed := func(id, slug string, extra map[string]any) map[string]any {
		in := map[string]any{"state": "landed", "repos": []any{map[string]any{"repo": "app", "pr": "https://forge.invalid/pr/" + id, "head": "h-" + id, "merge_commit": "m-" + id}},
			"merge_commits": map[string]any{"app": []any{"m-" + id}}}
		for k, v := range extra {
			in[k] = v
		}
		return map[string]any{"id": id, "slug": slug, "seq": 1, "project": "proj", "status": "wrapped", "priority": "normal", "bundle_name": "develop",
			"updated_at": "2026-11-10T08:00:00Z", "attrs": map[string]any{"integration": in}}
	}
	a := landed("wi_a", "proj#1", nil)
	a["closed_at"] = "2026-11-10T07:00:00Z"
	a["attrs"].(map[string]any)["waivers"] = []any{map[string]any{"gate": "verification", "words": "这次 FAIL 不算", "reading": "waive the light tier", "at": "2026-11-09T10:00:00Z"}}
	held := landed("wi_h", "proj#2", map[string]any{"red_rounds": 1.0, "fix_tickets": []any{"proj#9", "wi_t2", "proj#404"}, "batch": "wi_old"})
	held["priority"] = "high"
	fix := landed("wi_t1", "proj#9", map[string]any{"merge_commits": map[string]any{"app": []any{"m-fix1", "m-fix2"}}})
	fix["closed_at"] = "2026-11-10T09:00:00Z"
	batch := func(id string, seq int, status, result string, main map[string]any, labels ...string) map[string]any {
		attrs := map[string]any{}
		if result != "" {
			attrs["integration_batch"] = map[string]any{"result": result, "main": main}
		}
		return map[string]any{"id": id, "slug": fmt.Sprintf("proj#%d", 50+seq), "seq": seq, "project": "proj", "status": status, "bundle_name": "integrate", "labels": labels, "attrs": attrs}
	}
	merged := landed("wi_m", "proj#3", map[string]any{"state": "merged"})
	foreign := landed("wi_f", "other#1", nil)
	foreign["project"] = "other"
	return []map[string]any{
		a, held, fix, merged, foreign,
		{"id": "wi_t2", "slug": "proj#10", "project": "proj", "status": "running", "bundle_name": "develop", "attrs": map[string]any{}},
		batch("wi_b1", 1, "wrapped", "red", map[string]any{"app": "red-old", "lib": "lib-red"}),
		batch("wi_b2", 2, "wrapped", "red", map[string]any{"app": "red-new"}),
		batch("wi_b3", 3, "wrapped", "green", map[string]any{"app": "green-head"}),
		batch("wi_b4", 4, "failed", "red", map[string]any{"app": "failed-head"}),
		batch("wi_b5", 5, "canceled", "", nil),
		batch("wi_batch", 6, "running", "", nil, "integration"),
		batch("wi_b7", 7, "queued", "", nil, "integration"),
	}
}

// pfShim answers a directory holding a `pf` that is this test binary
// running as the pf CLI (TestMain's testAsPF): put first on a script's
// PATH, its `pf --json step hub` reaches the fixture's daemon.
func pfShim(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	shim := "#!/bin/sh\n" + testAsPF + "=1 exec '" + exe + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pf"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scriptPoolRun reads the pool the way the workflow's integrate.py does —
// its read_pool over `pf --json step hub list/get` — and answers it as a
// pool file carries it (snapshot_json), with the read's stderr.
func scriptPoolRun(t *testing.T, lane string) ([]byte, string, error) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	scripts, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	code := "import json, os, sys\nsys.path.insert(0, sys.argv[1])\nimport integrate\nfrom pfstep import hub\n" +
		"snap = integrate.read_pool(integrate.HubLister(hub.runner(os.environ)), sys.argv[2])\n" +
		"print(json.dumps(integrate.snapshot_json(snap)))\n"
	c := exec.Command(py, "-B", "-c", code, scripts, lane)
	c.Env = append(os.Environ(), "PATH="+pfShim(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err = c.Run()
	return out.Bytes(), errb.String(), err
}

// scriptPool is scriptPoolRun's pool; a read that fails fails the test.
func scriptPool(t *testing.T, lane string) []byte {
	t.Helper()
	out, errb, err := scriptPoolRun(t, lane)
	if err != nil {
		t.Fatalf("integrate.py's pool read: %v\n%s", err, errb)
	}
	return out
}

// fixturePool is poolFixture's pool as a pool file carries it: the landed
// items of proj (when each landed, its waivers), the registered fix
// tickets' states (one landed with its merge commits, one running, one
// not found), the batches on the lane not finished (newest first: a
// failed or canceled one is finished) and per repo the newest red
// batch's head (a newer green batch names none). It is the daemon's own
// pool reader's answer on this fixture, recorded when that reader left
// with the integration status face.
const fixturePool = `{
	"items": [
		{"id": "wi_a", "slug": "proj#1", "priority": "normal", "landed": "2026-11-10T07:00:00Z",
			"integration": {"state": "landed", "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/wi_a", "head": "h-wi_a", "merge_commit": "m-wi_a"}],
				"merge_commits": {"app": ["m-wi_a"]}},
			"waivers": [{"gate": "verification", "words": "这次 FAIL 不算", "reading": "waive the light tier", "at": "2026-11-09T10:00:00Z"}]},
		{"id": "wi_h", "slug": "proj#2", "priority": "high", "landed": "2026-11-10T08:00:00Z",
			"integration": {"state": "landed", "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/wi_h", "head": "h-wi_h", "merge_commit": "m-wi_h"}],
				"merge_commits": {"app": ["m-wi_h"]}, "red_rounds": 1, "fix_tickets": ["proj#9", "wi_t2", "proj#404"], "batch": "wi_old"}},
		{"id": "wi_t1", "slug": "proj#9", "priority": "normal", "landed": "2026-11-10T09:00:00Z",
			"integration": {"state": "landed", "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/wi_t1", "head": "h-wi_t1", "merge_commit": "m-wi_t1"}],
				"merge_commits": {"app": ["m-fix1", "m-fix2"]}}}
	],
	"open_batches": ["wi_b7", "wi_batch"],
	"red_heads": {"app": "red-new", "lib": "lib-red"},
	"tickets": {
		"proj#9": {"status": "wrapped", "integration": "landed", "merge_commits": {"app": ["m-fix1", "m-fix2"]}},
		"wi_t2": {"status": "running"},
		"proj#404": {"status": ""}
	}
}`

// TestC4HubPoolIsTheFixturesPool: the pool a step script reads through
// the step's hub — the real `pf step hub list/get`, the daemon's
// /step/call and hub proxy — is the fixture's pool (fixturePool), field
// by field: the workflow script's (integrate.py, its pool read over the
// same `pf` as a separate process); the reads are on the step's call
// ledger and held to its whitelist.
func TestC4HubPoolIsTheFixturesPool(t *testing.T) {
	_, srv := newC4Aihub(t, poolFixture())
	f := c4BatchFixture(t, srv)
	f.as("collect", 1)
	f.td.stepHubAllow = func(stepJob) []string {
		return []string{stepcall.HubGet, stepcall.HubList, stepcall.HubBatch, stepcall.HubFile, stepcall.HubAttrs}
	}
	inStep(t, f)
	var gotPy, wantAny any
	if err := json.Unmarshal(scriptPool(t, "integrate"), &gotPy); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fixturePool), &wantAny); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotPy, wantAny) {
		gp, _ := json.MarshalIndent(gotPy, "", "  ")
		t.Fatalf("integrate.py's pool through the step's hub differs from the fixture's:\n got %s\nwant %s", gp, fixturePool)
	}
	// the reads are the step's calls: on its ledger
	reads := map[string]int{}
	for _, l := range f.ledger(t) {
		if l.Kind == stepcall.KindCall {
			reads[l.Verb]++
		}
	}
	if reads["hub.list"] != 2 || reads["hub.get"] != 3 {
		t.Fatalf("integrate.py's reads are the step's calls, on its ledger: %v", reads)
	}
	// a step whose whitelist names no list cannot read the pool
	f.td.stepHubAllow = func(stepJob) []string { return []string{stepcall.HubGet, stepcall.HubBatch} }
	if _, errb, err := scriptPoolRun(t, "integrate"); err == nil || !strings.Contains(errb, "may not call it") {
		t.Fatalf("a pool read outside the whitelist: %v\n%s", err, errb)
	}
}

// c3Content renders a fix ticket's body as contract 3's effectsOf did
// (background, then the Sources, Units and Acceptance sections) — the
// body the contract 4 skills write into a `file` op.
func c3Content(background string, sources, units, acceptance []string) string {
	var b strings.Builder
	b.WriteString(background)
	if len(sources) > 0 {
		b.WriteString("\n\n## Sources\n")
		for _, s := range sources {
			b.WriteString("- " + s + "\n")
		}
	}
	if len(units) > 0 {
		b.WriteString("\n## Units\n")
		for _, u := range units {
			b.WriteString("- " + u + "\n")
		}
	}
	b.WriteString("\n## Acceptance\n")
	for _, a := range acceptance {
		b.WriteString("- " + a + "\n")
	}
	return b.String()
}

// writesFixture is the batch's landed items: a held item ruled fix, one
// ruled clear, one blamed by the red gate, one reverted, one a green tag
// covers.
func writesFixture() []map[string]any {
	item := func(id, slug string, in map[string]any) map[string]any {
		in["state"] = "landed"
		return map[string]any{"id": id, "slug": slug, "project": "proj", "status": "wrapped", "bundle_name": "develop", "attrs": map[string]any{"integration": in}}
	}
	return []map[string]any{
		item("wi_h", "proj#2", map[string]any{"red_rounds": 1.0, "fix_tickets": []any{"proj#9"}}),
		item("wi_c", "proj#3", map[string]any{"red_rounds": 2.0, "fix_tickets": []any{}}),
		item("wi_r", "proj#4", map[string]any{}),
		item("wi_v", "proj#5", map[string]any{}),
		item("wi_g", "proj#6", map[string]any{}),
		{"id": "wi_t1", "slug": "proj#9", "project": "proj", "status": "wrapped", "bundle_name": "develop", "content": "an earlier fix\n", "attrs": map[string]any{}},
	}
}

// TestC4BatchWritesAreContract3Effects: collect's hold fix and clear,
// full-gate's fix ticket and attribution, revert's and close's moves —
// registered by each step with `pf step hub batch` as generic attrs
// writes (workflow contract 1: each the whole record, fenced on the state
// it moves from) and made by the pending-writes executor through the
// daemon's sink — leave aihub's work items as the contract 3 effects of
// the same rulings did (the effects applier through the integration
// binding): the same work items, the same records. The one deliberate
// difference kept from contract 4: a hold clear's answered_at is the owner
// message's stamp (R5) where contract 3 stamped the moment it applied the
// effect — the contract 3 side is given that stamp.
func TestC4BatchWritesAreContract3Effects(t *testing.T) {
	const words = "wi_c 的红是环境问题，清掉它。"
	const at = "2026-11-11T10:00:00Z"
	fixH := c3Content("the hold's fix: TestBroken red since proj#2", []string{"wi_h", "https://forge.invalid/pr/2"}, nil, []string{"TestBroken passes on the fixed head"})
	fixR := c3Content("TestFlaky broke in proj#4", []string{"wi_r", "https://forge.invalid/pr/4"}, []string{"app: TestFlaky"}, []string{"`TestFlaky` passes on the fixed head (the full gate, app, go test)"})
	green := map[string]any{"state": "merged", "batch": "wi_batch", "green_tags": map[string]any{"app": "green/20261111-1"}, "green_tag": "green/20261111-1", "green_commit": "c-green"}
	reverted := map[string]any{"state": "reverted", "batch": "wi_batch", "revert_prs": []any{"https://forge.invalid/pr/77"}, "revert_merges": map[string]any{"app": "m-revert"}}
	attribution := map[string]any{"state": "landed", "batch": "wi_batch", "red_rounds": 1.0, "red_heads": map[string]any{"app": "c-red"}}
	cleared := map[string]any{"state": "merged", "red_rounds": 2.0, "fix_tickets": []any{},
		"hold_cleared": map[string]any{"reason": words, "cleared_in": "wi_batch", "answered_by": "owner", "answered_at": at}}
	fenced := map[string]any{"integration.state": "landed"}

	// contract 4: each step registers its ops file, the executor makes the
	// writes when it hands off done (here: right after the registration)
	h4, srv4 := newC4Aihub(t, writesFixture())
	f := c4BatchFixture(t, srv4)
	f.ownerSays(t, "collect", at, words)
	f.td.stepHubAllow = func(stepJob) []string { return stepcall.HubVerbs }
	f.td.stepHubAttrs = func(stepJob) []string { return []string{"integration"} }
	sink := stepHubSink{c4HubDaemon(t, srv4).Daemon}
	set := func(m map[string]any, kv ...any) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	write := func(target string, value map[string]any) map[string]any {
		return map[string]any{"verb": "attrs", "target": target, "name": "integration", "value": value, "if": fenced}
	}
	clear := write("wi_c", cleared)
	clear["per_owner"] = at
	for _, s := range []struct {
		step string
		ops  []map[string]any
	}{
		{"collect", []map[string]any{ // collect-ops.json
			{"verb": "file", "key": "hold-fix/wi_h/1", "lane": "develop", "goal": "fix proj#2's red", "content": fixH, "priority": "high", "labels": []string{"integration-fix"}},
			write("wi_h", map[string]any{"state": "landed", "red_rounds": 1.0, "fix_tickets": []any{"proj#9", "key:hold-fix/wi_h/1"}}),
			clear,
		}},
		{"revert", []map[string]any{write("wi_v", reverted)}}, // revert.py --ops-out
		{"full-gate", []map[string]any{ // full-gate-ops.json
			{"verb": "file", "key": "wi_batch/fix/wi_r/0", "lane": "develop", "goal": "app: TestFlaky broke", "content": fixR, "priority": "high", "labels": []string{"integration-fix"}},
			write("wi_r", set(attribution, "fix_tickets", []any{"key:wi_batch/fix/wi_r/0"})),
		}},
		{"close", []map[string]any{write("wi_g", green)}}, // integrate.py close --ops-out
	} {
		f.as(s.step, 1)
		if code, body := c4Call(t, f.td, "hub.batch", map[string]any{"ops": s.ops}); code != 200 {
			t.Fatalf("%s: the batch = %d %s", s.step, code, body)
		}
		if _, err := pendingwrites.Execute(context.Background(), f.capDir, s.step, 1, sink); err != nil {
			t.Fatalf("%s: the writes: %v", s.step, err)
		}
	}

	got := h4.state() + "\n--- writes ---\n" + strings.Join(h4.sortedWrites(), "\n") + "\n"
	golden := filepath.Join("testdata", "c4_batch_writes.golden")
	if os.Getenv("PF_WRITE_C4_BATCH_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%s: %v", golden, err)
	}
	if got != string(want) {
		t.Fatalf("aihub after the batches differs from aihub after the contract 3 effects (%s):\n got %s\nwant %s", golden, got, want)
	}
	// not vacuous: the tickets were filed, registered, the hold cleared on
	// the owner's message (which rides the write), the moves made
	st := h4.state()
	for _, w := range []string{`"fix_tickets":["proj#9","proj#101"]`, `"fix_tickets":["proj#102"]`, `"answered_at":"` + at + `"`, `"answered_by":"owner"`,
		`"reason":"` + words + `"`, `"cleared_in":"wi_batch"`, `"state":"reverted"`, `"green_tag":"green/20261111-1"`, "integration-ticket-key: wi_batch/fix/wi_r/0"} {
		if !strings.Contains(st, w) {
			t.Fatalf("aihub lacks %s:\n%s", w, st)
		}
	}
	if w := strings.Join(h4.sortedWrites(), "\n"); !strings.Contains(w, `"owner_message":{"at":"`+at+`","by":"owner","words":"`+words+`"}`) {
		t.Fatalf("the clear carries the owner's message to aihub:\n%s", w)
	}
	// a replayed write whose fence the record already left, holding the
	// value written, is no failure; one holding another value is
	if err := sink.Attrs(context.Background(), stepcall.Op{Verb: stepcall.HubAttrs, Target: "wi_g", Name: "integration", Value: mustJSON(green), If: fenced}); err != nil {
		t.Fatalf("a repeated write: %v", err)
	}
	if err := sink.Attrs(context.Background(), stepcall.Op{Verb: stepcall.HubAttrs, Target: "wi_g", Name: "integration", Value: mustJSON(reverted), If: fenced}); err == nil || !strings.Contains(err.Error(), "CORE-409") {
		t.Fatalf("a write the fence refuses: %v", err)
	}
}

// TestC4EmptyBatchIsARecordedNoOp (R4): an empty ops file is accepted —
// the call and its ok result are on the ledger, nothing is pending, aihub
// sees nothing — so a skill registers a command's ops file whatever it
// holds; after the dispatch's hand-off it is refused like any write.
func TestC4EmptyBatchIsARecordedNoOp(t *testing.T) {
	f := newC4Fixture(t, "wi_c4r4")
	f.as("build", 1)
	f.td.stepHubAllow = func(stepJob) []string { return stepcall.HubVerbs }
	calls := 0
	f.td.hub.mu.Lock()
	f.td.hub.memDo = func(string, string, any, any) error { calls++; return nil }
	f.td.hub.mu.Unlock()
	code, body := c4Call(t, f.td, "hub.batch", map[string]any{"ops": []any{}})
	if code != 200 || !strings.Contains(body, `"pending":[]`) || !strings.Contains(body, "no-op") {
		t.Fatalf("an empty batch = %d %s", code, body)
	}
	lines := f.ledger(t)
	if len(lines) != 2 || lines[0].Kind != stepcall.KindCall || lines[1].Kind != stepcall.KindResult || lines[1].Outcome != stepcall.OutcomeOK ||
		lines[1].Verb != "hub.batch" || len(lines[1].Pending) != 0 || calls != 0 {
		t.Fatalf("the ledger of an empty batch: %+v (aihub calls %d)", lines, calls)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("hand-off = %d %s", code, body)
	}
	if code, _ := c4Call(t, f.td, "hub.batch", map[string]any{"ops": []any{}}); code != 409 {
		t.Fatalf("an empty batch after the hand-off = %d", code)
	}
}

// TestC4AttrsPerOwnerCarriesTheOwnersMessage (R5): an attrs write on the
// owner's word names the owner message it carries out (--per-owner, as a
// hand-off does); one naming none — another step's, an earlier round's,
// the step's own question — is refused with nothing recorded. The
// recorded write carries the message's words verbatim, its writer and
// stamp (whatever the caller sent as owner_message); the executor sends
// them to aihub's attrs route with the write. A key off the step's
// hub.attrs whitelist is refused DAEMON-403, the step's whitelist named.
func TestC4AttrsPerOwnerCarriesTheOwnersMessage(t *testing.T) {
	f := newC4Fixture(t, "wi_c4r5")
	f.writeExec(t, map[string]*capsule.Step{
		"collect": {Status: capsule.StepInProgress, Round: 2, Attempts: []capsule.Attempt{{Cause: "gate_fail_rewind", ArchivedAt: "2026-11-11T09:00:00Z"}}},
		"close":   {Status: capsule.StepInProgress, Round: 1},
	})
	f.ownerSays(t, "collect", "2026-11-11T08:00:00Z", "上一轮的话")
	f.ownerSays(t, "close", "2026-11-11T09:10:00Z", "给 close 的话")
	if err := capsule.AppendAsk(f.capDir, capsule.AskLine{At: "2026-11-11T09:20:00Z", Step: "collect", Spawn: 1, Kind: capsule.AskKindAsk, Text: "清还是修？"}); err != nil {
		t.Fatal(err)
	}
	if err := capsule.AppendAsk(f.capDir, capsule.AskLine{At: "2026-11-11T09:30:00Z", Step: "collect", Spawn: 1, Kind: capsule.AskKindMessage, Text: "清掉 wi_s。\n  原样照抄。", By: "u_alice"}); err != nil {
		t.Fatal(err)
	}
	f.ownerSays(t, "collect", "2026-11-11T09:40:00Z", "wi_t 也清掉")
	f.as("collect", 3) // dispatch 3 of round 2: the pending writes belong to the round (R8)
	f.td.stepHubAllow = func(stepJob) []string { return stepcall.HubVerbs }
	f.td.stepHubAttrs = func(stepJob) []string { return []string{"integration"} }
	write := func(key, perOwner string) map[string]any {
		return map[string]any{"target": "wi_s", "name": key, "value": map[string]any{"state": "merged"}, "if": map[string]any{"integration.state": "landed"},
			"per_owner": perOwner, "owner_message": map[string]any{"words": "forged", "by": "forged", "at": "forged"}}
	}
	for _, c := range []struct {
		key, perOwner string
		code          int
		want          string
	}{
		{"integration", "2026-11-11T08:00:00Z", 400, "names no owner message"}, // an earlier round's
		{"integration", "2026-11-11T09:10:00Z", 400, "names no owner message"}, // another step's
		{"integration", "2026-11-11T09:20:00Z", 400, "names no owner message"}, // the step's own question
		{"integration_batch", "", 403, `may not write attrs key \"integration_batch\" (the step's hub.attrs: [integration])`},
		{"Bad", "", 400, "not of the shape"},
	} {
		if code, body := c4Call(t, f.td, "hub.attrs", write(c.key, c.perOwner)); code != c.code || !strings.Contains(body, c.want) {
			t.Fatalf("%s per_owner %q = %d %s", c.key, c.perOwner, code, body)
		}
	}
	if n := len(stepcall.PendingOps(f.ledger(t), "collect", 2)); n != 0 {
		t.Fatalf("a refused write was recorded: %d", n)
	}
	if code, body := c4Call(t, f.td, "hub.attrs", write("integration", "2026-11-11T09:30:00Z")); code != 200 {
		t.Fatalf("a write on the owner's word = %d %s", code, body)
	}
	plain := write("integration", "")
	plain["target"] = "wi_u"
	if code, body := c4Call(t, f.td, "hub.batch", map[string]any{"ops": []map[string]any{
		{"verb": "attrs", "target": "wi_t", "name": "integration", "value": map[string]any{"state": "merged"}, "per_owner": "2026-11-11T09:40:00Z"},
		plainOp(plain),
	}}); code != 200 {
		t.Fatalf("writes in a batch = %d %s", code, body)
	}
	ops := stepcall.PendingOps(f.ledger(t), "collect", 2)
	if len(ops) != 3 {
		t.Fatalf("the writes: %+v", ops)
	}
	if o := ops[0]; o.OwnerMessage == nil || *o.OwnerMessage != (api.OwnerMessage{Words: "清掉 wi_s。\n  原样照抄。", By: "u_alice", At: "2026-11-11T09:30:00Z"}) || o.PerOwner != "2026-11-11T09:30:00Z" {
		t.Fatalf("the write on the owner's words: %+v", o)
	}
	if o := ops[1]; o.OwnerMessage == nil || *o.OwnerMessage != (api.OwnerMessage{Words: "wi_t 也清掉", By: "owner", At: "2026-11-11T09:40:00Z"}) {
		t.Fatalf("the write whose message names no writer: %+v", o)
	}
	if o := ops[2]; o.OwnerMessage != nil {
		t.Fatalf("a write naming no message carries none, whatever the caller sent: %+v", o)
	}
	// the executor sends the message with the write to aihub's attrs route
	var got []api.AttrsWriteRequest
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var req api.AttrsWriteRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		got, paths = append(got, req), append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "x"})
	}))
	t.Cleanup(srv.Close)
	if _, err := pendingwrites.Execute(context.Background(), f.capDir, "collect", 2, stepHubSink{c4HubDaemon(t, srv).Daemon}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || paths[0] != "PATCH /api/v1/work-items/"+url.PathEscape("wi_s")+"/attrs" || got[0].Key != "integration" || string(got[0].Value) != `{"state":"merged"}` ||
		string(got[0].If["integration.state"]) != `"landed"` || got[0].OwnerMessage == nil || *got[0].OwnerMessage != (api.OwnerMessage{Words: "清掉 wi_s。\n  原样照抄。", By: "u_alice", At: "2026-11-11T09:30:00Z"}) ||
		got[2].OwnerMessage != nil {
		t.Fatalf("the attrs requests: %v %+v", paths, got)
	}
}

// plainOp is a write map as a batch op (the attrs verb named).
func plainOp(m map[string]any) map[string]any {
	out := map[string]any{"verb": "attrs"}
	for k, v := range m {
		if k != "per_owner" {
			out[k] = v
		}
	}
	return out
}
