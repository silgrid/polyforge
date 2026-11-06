package cli

// verbs_workflow_test.go ( AC8/AC9): `pf workflows validate`
// judges lanes with the daemon's own loader and exits 1 on any defect;
// `pf doctor`'s `workflow lanes` row runs the same judgement and lists the
// queued work items bound to a lane the daemon's lane set does not carry
// (WFC-123), as the merged queue view does.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w429Lanes writes a lanes root holding one good lane and, when bad, one
// lane the contract refuses.
func w429Lanes(t *testing.T, bad bool) string {
	t.Helper()
	root := t.TempDir()
	workflowtest.WriteLane(t, root, "good", "steps:\n  - { id: fix }\n")
	if bad {
		writeLane(t, root, "broken", "steps:\n  - { id: fix, context_policy: fresh }\n")
	}
	return root
}

func TestWorkflowValidateJudgesLanes(t *testing.T) {
	root := w429Lanes(t, true)
	code, stdout, stderr := runPf(t, nil, "workflows", "validate", filepath.Join(root, "good"))
	if code != 0 || !strings.Contains(stdout, "ok    good") {
		t.Fatalf("a good lane: exit %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = runPf(t, nil, "workflows", "validate", filepath.Join(root, "good"), filepath.Join(root, "broken"))
	if code != 1 || !strings.Contains(stdout, "fail  broken") || !strings.Contains(stdout, "WFC-111 "+filepath.Join(root, "broken", workflow.WorkflowFile)+":5 steps[0](fix).context_policy") {
		t.Fatalf("a refused lane must exit 1 naming the code, file:line and path:\n%s", stdout)
	}
	code, stdout, _ = runPf(t, nil, "--json", "workflows", "validate", filepath.Join(root, "broken"))
	var rep validateReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil || code != 1 || rep.OK || rep.Contract != workflow.ContractVersion || len(rep.Lanes) != 1 || len(rep.Lanes[0].Errors) != 1 || rep.Lanes[0].Errors[0].Code != workflow.CodeUnknownKey {
		t.Fatalf("--json: exit %d %v %+v", code, err, rep)
	}
	// no lane directory: the lane set the daemon runs work items on
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"machine_id":"m-v","workflows_dir":"`+root+`","jobs":[],"queue":{}}`)
	})
	sock := newFakeDaemon(t, mux)
	env := doctorServerConfig(t, "http://unused.invalid:1", "k", sock, "polyforge")
	code, stdout, _ = runPf(t, env, "workflows", "validate", "--workspace", t.TempDir())
	if code != 1 || !strings.Contains(stdout, "lane set "+root) || !strings.Contains(stdout, "ok    good") || !strings.Contains(stdout, "fail  broken") {
		t.Fatalf("the daemon's lane set: exit %d\n%s", code, stdout)
	}
	// usage refusals
	if code, _, _ := runPf(t, nil, "workflow", "check"); code == 0 {
		t.Fatal("an unknown workflow action must be refused")
	}
	if code, _, _ := runPf(t, nil, "workflows", "validate", "--workspace", filepath.Join(root, "nowhere")); code == 0 {
		t.Fatal("a --workspace that is no directory must be refused")
	}
}

// w429QueueHub is a fake aihub listing one queued work item on a missing
// lane, one on the installed lane and one running on the missing lane.
func w429QueueHub(t *testing.T) string {
	t.Helper()
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ready-queue") {
			io.WriteString(w, `{"items":[],"running":[],"paused":[]}`)
			return
		}
		if r.URL.Query().Get("status") == "queued" {
			io.WriteString(w, `{"items":[{"id":"wi_gone","slug":"polyforge#7","status":"queued","project":"polyforge","goal":"g","bundle_name":"feature-full","created_at":"t"},`+
				`{"id":"wi_ok","slug":"polyforge#8","status":"queued","project":"polyforge","goal":"g","bundle_name":"good","created_at":"t"}],"total":2}`)
			return
		}
		io.WriteString(w, `{"items":[{"id":"wi_gone","slug":"polyforge#7","status":"queued","project":"polyforge","goal":"g","bundle_name":"feature-full","created_at":"t"},`+
			`{"id":"wi_ok","slug":"polyforge#8","status":"queued","project":"polyforge","goal":"g","bundle_name":"good","created_at":"t"},`+
			`{"id":"wi_run","slug":"polyforge#9","status":"running","project":"polyforge","goal":"g","bundle_name":"feature-full","created_at":"t"}],"total":3}`)
	})
	return srv.URL
}

func TestQueueListsWorkItemsOnMissingLanes(t *testing.T) {
	root := w429Lanes(t, false)
	env := doctorServerConfig(t, w429QueueHub(t), "k", liveDaemonSocketLanes(t, "m-q", root), "polyforge")
	code, stdout, stderr := runPf(t, env, "--json", "queue")
	var doc struct {
		Items        []WIView      `json:"items"`
		LanesMissing []laneMissing `json:"lanes_missing"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); code != 0 || err != nil {
		t.Fatalf("pf --json queue: exit %d %v\n%s%s", code, err, stdout, stderr)
	}
	if len(doc.LanesMissing) != 1 || doc.LanesMissing[0] != (laneMissing{ID: "wi_gone", Slug: "polyforge#7", Bundle: "feature-full", Code: workflow.CodeLaneMissing}) {
		t.Fatalf("lanes_missing = %+v, want only the queued item on the missing lane", doc.LanesMissing)
	}
	code, stdout, _ = runPf(t, env, "queue")
	if code != 0 || !strings.Contains(stdout, "WFC-123: 1 queued work item(s) bound to a lane outside their workspace's lane set") || !strings.Contains(stdout, "polyforge#7 (lane feature-full)") || strings.Contains(stdout, "polyforge#9 (lane") {
		t.Fatalf("the human queue must list the item under its table:\n%s", stdout)
	}
	// a daemon that reports no lane set lists nothing (its set is unknown)
	env = doctorServerConfig(t, w429QueueHub(t), "k", liveDaemonSocket(t, "m-q2"), "polyforge")
	if code, stdout, _ := runPf(t, env, "--json", "queue"); code != 0 || strings.Contains(stdout, "lanes_missing") {
		t.Fatalf("no lane set, no listing: %d %s", code, stdout)
	}
}

func TestDoctorWorkflowLanesRow(t *testing.T) {
	hub := w429QueueHub(t)
	// a refused lane in the daemon's set fails the row, naming the defect
	root := w429Lanes(t, true)
	env := doctorServerConfig(t, hub, "k", liveDaemonSocketLanes(t, "m-l", root), "polyforge")
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLanes); st != "fail" || !strings.Contains(detail, "WFC-111 "+filepath.Join(root, "broken", workflow.WorkflowFile)+":5") {
		t.Fatalf("a refused lane: %s %q", st, detail)
	}
	// a loading set with a queued item on a missing lane warns, listing it
	root = w429Lanes(t, false)
	env = doctorServerConfig(t, hub, "k", liveDaemonSocketLanes(t, "m-l2", root), "polyforge")
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLanes); st != "warn" || !strings.Contains(detail, "1 lane(s) in "+root+" load under workflow contract "+strconv.Itoa(workflow.ContractVersion)) || !strings.Contains(detail, "WFC-123") || !strings.Contains(detail, "polyforge#7 (lane feature-full)") {
		t.Fatalf("a queued item on a missing lane: %s %q", st, detail)
	}
	// the item re-bound: pass
	if err := os.MkdirAll(filepath.Join(root, "feature-full"), 0o755); err != nil {
		t.Fatal(err)
	}
	workflowtest.WriteLane(t, root, "feature-full", "steps:\n  - { id: fix }\n")
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLanes); st != "pass" || !strings.Contains(detail, "no queued work item is bound to a missing lane") {
		t.Fatalf("every lane installed: %s %q", st, detail)
	}
	// the daemon down: skip, never a faked pass
	env = doctorServerConfig(t, hub, "k", filepath.Join(t.TempDir(), "dead.sock"), "polyforge")
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, _ := doctorFind(t, doc, docChLanes); st != "skip" {
		t.Fatalf("the daemon down: %s, want skip", st)
	}
}

// w433UserLane writes the user lane name of the workspace ws: one agent
// step whose definition includes the built-in {{principles}} fragment (so
// only a load over the built-in shared tree accepts it).
func w433UserLane(t *testing.T, ws, name string) string {
	t.Helper()
	dir := filepath.Join(workflow.UserLanesRoot(ws), name)
	if err := os.MkdirAll(filepath.Join(dir, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workflow.WorkflowFile), []byte(workflowtest.Header(name)+"steps:\n  - { id: draft }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "steps", "draft.md"), []byte(workflowtest.Definition("draft")+"{{principles}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// workflow contract §13.4: with no lane directory,
// validate judges the workspace's whole lane set — the built-in lanes and
// its user lanes — the workspace being --workspace or the mapped one
// holding the working directory; a user lane named like a built-in one is
// WFC-122. A user lane directory named on the command line is judged as
// the user lane it is (over the built-in shared tree).
func TestW433ValidateJudgesTheWorkspaceLaneSet(t *testing.T) {
	root := w429Lanes(t, false)
	if err := os.MkdirAll(filepath.Join(root, workflow.SharedDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workflow.SharedDir, "principles.md"), []byte("Principles.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := doctorServerConfig(t, "http://unused.invalid:1", "k", liveDaemonSocketLanes(t, "m-v", root), "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	notes := w433UserLane(t, ws, "notes")
	shadow := w433UserLane(t, ws, "good")
	sub := filepath.Join(ws, "capsules")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPfInCwd(t, sub, env, "workflows", "validate")
	if code != 1 || !strings.Contains(stdout, "lane set "+root) || !strings.Contains(stdout, "user lanes "+workflow.UserLanesRoot(ws)+" (workspace "+ws+")") ||
		!strings.Contains(stdout, "ok    good  "+filepath.Join(root, "good")+"\n") || !strings.Contains(stdout, "ok    notes  "+notes+" (user lane)") ||
		!strings.Contains(stdout, "fail  good  "+shadow+" (user lane)") || !strings.Contains(stdout, "WFC-122 "+filepath.Join(shadow, workflow.WorkflowFile)+":2 name") {
		t.Fatalf("the workspace lane set (cwd inside the workspace): exit %d\n%s%s", code, stdout, stderr)
	}
	code, stdout, _ = runPfInCwd(t, t.TempDir(), env, "--json", "workflows", "validate", "--workspace", ws)
	var rep validateReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil || code != 1 || rep.Workspace != ws || rep.UserLanes != workflow.UserLanesRoot(ws) || len(rep.Lanes) != 3 {
		t.Fatalf("--workspace --json: exit %d %v %+v", code, err, rep)
	}
	for _, l := range rep.Lanes {
		want := map[string]string{filepath.Join(root, "good"): workflow.SourceBuiltin, notes: workflow.SourceWorkspace, shadow: workflow.SourceWorkspace}[l.Dir]
		if l.Source != want {
			t.Fatalf("lane %s source %q, want %q", l.Dir, l.Source, want)
		}
	}
	// the shadowing lane renamed away: the set passes
	if err := os.RemoveAll(shadow); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runPfInCwd(t, sub, env, "workflows", "validate"); code != 0 || !strings.Contains(stdout, "ok    notes") {
		t.Fatalf("the workspace lane set without the shadowing lane: exit %d\n%s", code, stdout)
	}
	// outside every workspace: the built-in lanes alone
	if code, stdout, _ := runPfInCwd(t, t.TempDir(), env, "workflows", "validate"); code != 0 || strings.Contains(stdout, "notes") || strings.Contains(stdout, "user lanes") {
		t.Fatalf("outside a workspace only the built-in lanes are judged: exit %d\n%s", code, stdout)
	}
	// the user lane named directly: judged as that workspace's user lane
	if code, stdout, _ := runPf(t, env, "workflows", "validate", notes); code != 0 || !strings.Contains(stdout, "ok    notes  "+notes+" (user lane)") {
		t.Fatalf("a user lane dir must load over the built-in shared tree: exit %d\n%s", code, stdout)
	}
}

// The doctor row: a refused user lane of a mapped
// workspace warns (only that lane is unavailable), never fails; a queued
// work item bound to a user lane of its workspace is not on a missing
// lane.
func TestW433DoctorWorkflowLanesRowUserLanes(t *testing.T) {
	root := w429Lanes(t, false)
	if err := os.MkdirAll(filepath.Join(root, workflow.SharedDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workflow.SharedDir, "principles.md"), []byte("Principles.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := doctorServerConfig(t, w429QueueHub(t), "k", liveDaemonSocketLanes(t, "m-l", root), "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	w433UserLane(t, ws, "feature-full") // the lane wi_gone is bound to, now a user lane of its workspace
	writeLane(t, workflow.UserLanesRoot(ws), "broken", "steps:\n  - { id: fix, context_policy: fresh }\n")
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChLanes)
	if st != "warn" || !strings.Contains(detail, "and 1 user lane(s) of the mapped workspaces") || !strings.Contains(detail, "WFC-111 "+filepath.Join(workflow.UserLanesRoot(ws), "broken", workflow.WorkflowFile)) || strings.Contains(detail, "WFC-123") {
		t.Fatalf("a refused user lane warns, and a work item on a user lane is not missing: %s %q", st, detail)
	}
}

// A directory named under a workspace's user lane root that is no lane of
// its set — a typo, a `_shared` (a workspace has none), any `_`-prefixed
// one — is judged and refused WFC-110 with exit 1, as a missing directory
// is anywhere else: never an empty report that passes.
func TestW433ValidateRefusesANonLaneUnderTheUserLaneRoot(t *testing.T) {
	root := w429Lanes(t, false)
	if err := os.MkdirAll(filepath.Join(root, workflow.SharedDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workflow.SharedDir, "principles.md"), []byte("Principles.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := doctorServerConfig(t, "http://unused.invalid:1", "k", liveDaemonSocketLanes(t, "m-v", root), "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	notes := w433UserLane(t, ws, "notes")
	user := workflow.UserLanesRoot(ws)
	for _, d := range []string{"_shared", "_drafts"} {
		if err := os.MkdirAll(filepath.Join(user, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(user, "_drafts", workflow.WorkflowFile), []byte(workflowtest.Header("_drafts")+"steps:\n  - { id: draft }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(user, "noets"), filepath.Join(user, workflow.SharedDir), filepath.Join(user, "_drafts")} {
		code, stdout, stderr := runPf(t, env, "--json", "workflows", "validate", d)
		var rep validateReport
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil || code != 1 || rep.OK || len(rep.Lanes) != 1 || rep.Lanes[0].Dir != d || rep.Lanes[0].OK ||
			len(rep.Lanes[0].Errors) == 0 || rep.Lanes[0].Errors[0].Code != workflow.CodeLaneFile {
			t.Fatalf("%s: want exit 1 and one WFC-110 lane report, got exit %d %v\n%s%s", d, code, err, stdout, stderr)
		}
	}
	// named beside a real user lane, the typo still fails the run
	if code, stdout, _ := runPf(t, env, "workflows", "validate", notes, filepath.Join(user, "noets")); code != 1 || !strings.Contains(stdout, "ok    notes") || !strings.Contains(stdout, "fail  noets") || !strings.Contains(stdout, "WFC-110 "+filepath.Join(user, "noets", workflow.WorkflowFile)) {
		t.Fatalf("a typo beside a real user lane: exit %d\n%s", code, stdout)
	}
}
