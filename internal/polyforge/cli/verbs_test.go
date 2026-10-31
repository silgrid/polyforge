package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// Direct verbs: request-shape assertions against a fake aihub (httptest).
// Each case drives the full Run path and asserts method, path/query,
// auth header and body shape on the wire.

const cannedWI = `{"id":"wi_abc","slug":"pf-42","seq":42,"project":"polyforge","goal":"the goal",
"status":"queued","priority":"normal","labels":["a"],"reporter":"usr_1","created_at":"2026-10-01T00:00:00Z",
"updated_at":"2026-10-01T00:00:00Z"}`

func TestVerbFileCreateRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, _, stderr := runPf(t, env,
		"file", "create", "--project", "polyforge", "--goal", "do the thing",
		"--label", "infra", "--label", "urgent", "--priority", "high",
		"--bundle", "b1", "--attr", "team=x")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "POST" || got.Path != "/api/v1/work-items" {
		t.Errorf("request = %s %s", got.Method, got.Path)
	}
	if got.Header.Get("Authorization") != "Bearer k_cli" {
		t.Errorf("auth = %q", got.Header.Get("Authorization"))
	}
	for _, want := range []string{
		`"project":"polyforge"`, `"goal":"do the thing"`,
		`"labels":["infra","urgent"]`, `"priority":"high"`,
		`"bundle_name":"b1"`, `"team":"x"`,
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %s: %s", want, got.Body)
		}
	}
}

// TestVerbFileCreateRepoInventory: --repo (repeatable) sends name-only
// inventory entries (aihub fills the URL from the project's repos); the
// --stdin body's repo_inventory reaches aihub as given.
func TestVerbFileCreateRepoInventory(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g",
		"--repo", "app", "--repo", "lib")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if want := `"repo_inventory":[{"name":"app"},{"name":"lib"}]`; !strings.Contains(rec.last().Body, want) {
		t.Errorf("body missing %s: %s", want, rec.last().Body)
	}

	// --stdin: repo_inventory reaches aihub as given (aihub refuses
	// url/subdir/other keys — the CLI drops nothing).
	stdinBody := `{"project":"polyforge","goal":"g","repo_inventory":[{"name":"app","url":"file:///etc"}]}`
	var stdout, errb bytes.Buffer
	if code := Run([]string{"file", "create", "--stdin"}, strings.NewReader(stdinBody), &stdout, &errb); code != 0 {
		t.Fatalf("stdin create: code=%d stderr=%s", code, errb.String())
	}
	for _, want := range []string{
		`"repo_inventory":[{"name":"app","url":"file:///etc"}]`,
	} {
		if !strings.Contains(rec.last().Body, want) {
			t.Errorf("stdin body missing %s: %s", want, rec.last().Body)
		}
	}

	// --repo together with a stdin repo_inventory is refused locally.
	sent := len(rec.all())
	stdout.Reset()
	errb.Reset()
	if code := Run([]string{"file", "create", "--stdin", "--repo", "lib"}, strings.NewReader(stdinBody), &stdout, &errb); code != 1 ||
		!strings.Contains(errb.String(), "--repo cannot be combined") {
		t.Fatalf("--repo + stdin repo_inventory: code=%d stderr=%s", code, errb.String())
	}
	if len(rec.all()) != sent {
		t.Fatal("the refused create still reached aihub")
	}

	// The removed repo_inventory_base array is refused locally, never
	// dropped.
	stdout.Reset()
	errb.Reset()
	removed := `{"project":"polyforge","goal":"g","repo_inventory_base":[{"name":"app","base":"abc"}]}`
	if code := Run([]string{"file", "create", "--stdin"}, strings.NewReader(removed), &stdout, &errb); code != 1 ||
		!strings.Contains(errb.String(), "repo_inventory_base is not supported") {
		t.Fatalf("stdin repo_inventory_base: code=%d stderr=%s", code, errb.String())
	}
	if len(rec.all()) != sent {
		t.Fatal("the refused stdin create still reached aihub")
	}
}

// TestVerbFileCreateProjectContract pins the #113 placement contract at
// the unit level: without --project the verb refuses pointing at the
// mapping; with an unmapped --project it refuses naming the project;
// with a mapped one it proceeds.
func TestVerbFileCreateProjectContract(t *testing.T) {
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	// No --project, mapping present: required-flag guidance.
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	code, _, stderr := runPf(t, env, "file", "create", "--goal", "g")
	if code != 1 {
		t.Fatalf("no --project: code=%d, want 1", code)
	}
	for _, tok := range []string{"--project", "required", "[workspace"} {
		if !strings.Contains(stderr, tok) {
			t.Errorf("no --project stderr %q misses %q", stderr, tok)
		}
	}
	// Unmapped --project: naming error, no wire call.
	var rec reqRecorder
	srv2 := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env2 := withMappingConfig(t)
	env2["PF_SERVER"] = srv2.URL
	code, _, stderr = runPf(t, env2, "file", "create", "--project", "ghostproj", "--goal", "g")
	if code != 1 {
		t.Fatalf("unmapped --project: code=%d, want 1", code)
	}
	if !strings.Contains(stderr, "ghostproj") || !strings.Contains(stderr, "doctor") {
		t.Errorf("unmapped stderr %q misses the naming/guidance tokens", stderr)
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("unmapped placement must not reach the wire; got %d requests", n)
	}
}

func TestVerbFileShowAndEditRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	if code, _, stderr := runPf(t, env, "file", "show", "wi_abc"); code != 0 {
		t.Fatalf("show: code=%d stderr=%s", code, stderr)
	}
	// The human face reads the record, then its dependency block (#161).
	if all := rec.all(); len(all) != 2 || all[0].Method != "GET" || all[0].Path != "/api/v1/work-items/wi_abc" ||
		all[1].Method != "GET" || all[1].Path != "/api/v1/work-items/wi_abc/dependencies" {
		t.Errorf("show requests = %+v", all)
	}

	if code, _, stderr := runPf(t, env, "file", "edit", "wi_abc", "--priority", "low"); code != 0 {
		t.Fatalf("edit: code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "PATCH" || got.Path != "/api/v1/work-items/wi_abc" {
		t.Errorf("edit request = %s %s", got.Method, got.Path)
	}
	// PATCH body carries ONLY the flags provided.
	if got.Body != `{"priority":"low"}` {
		t.Errorf("edit body = %s, want only the priority key", got.Body)
	}

	// --bundle binds a name; an empty value is the JSON-null unbind.
	for _, c := range []struct{ val, want string }{
		{"release", `{"bundle_name":"release"}`},
		{"", `{"bundle_name":null}`},
	} {
		if code, _, stderr := runPf(t, env, "file", "edit", "wi_abc", "--bundle", c.val); code != 0 {
			t.Fatalf("edit --bundle %q: code=%d stderr=%s", c.val, code, stderr)
		}
		if got := rec.last(); got.Body != c.want {
			t.Errorf("edit --bundle %q body = %s, want %s", c.val, got.Body, c.want)
		}
	}

	// No --attr on edit: aihub's PATCH whitelist refuses attrs (CORE-400
	// IMMUTABLE), so the flag could only ever fail on the wire. It is a
	// local usage error now and nothing reaches aihub.
	before := len(rec.all())
	if code, _, _ := runPf(t, env, "file", "edit", "wi_abc", "--attr", "team=x"); code == 0 {
		t.Errorf("edit --attr: exit 0, want a usage error")
	}
	if n := len(rec.all()); n != before {
		t.Errorf("edit --attr reached aihub (%d new requests)", n-before)
	}
}

func TestVerbQueueRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[`+cannedWI+`],"total":1}`)
	})

	// An explicit --project passes through to aihub unvalidated: exactly
	// one query carrying the value.
	if code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL},
		"queue", "--project", "polyforge", "--status", "queued"); code != 0 {
		t.Fatalf("queue: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Method != "GET" || got.Path != "/api/v1/work-items?project=polyforge&status=queued" {
		t.Errorf("queue request = %s %s", got.Method, got.Path)
	}

	if code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL},
		"queue", "--project", "polyforge", "--ready"); code != 0 {
		t.Fatalf("queue --ready: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Path != "/api/v1/projects/polyforge/ready-queue" {
		t.Errorf("ready request path = %s", got.Path)
	}
}

// TestVerbQueueMergedAllMapped pins the #113 merged mode: without
// --project, ONE merged presentation over ALL mapped projects — one
// query per mapped project (mapping declaration order), merged rows,
// and --json emitting one merged items array.
func TestVerbQueueMergedAllMapped(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		proj := r.URL.Query().Get("project")
		switch proj {
		case "alpha":
			io.WriteString(w, `{"items":[{"id":"wi_a","slug":"a-1","project":"alpha","status":"queued","priority":"normal","goal":"alpha goal","created_at":"t"}],"total":1}`)
		case "beta":
			io.WriteString(w, `{"items":[{"id":"wi_b","slug":"b-1","project":"beta","status":"queued","priority":"high","goal":"beta goal","created_at":"t"}],"total":1}`)
		default:
			io.WriteString(w, `{"items":[],"total":0}`)
		}
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL

	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("merged queue: code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"2 work item(s)", "a-1", "b-1", "alpha", "beta", "PROJECT"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("merged queue stdout missing %q:\n%s", want, stdout)
		}
	}
	// Deterministic mapping order: alpha first (mapping declaration),
	// then beta.
	if strings.Index(stdout, "a-1") > strings.Index(stdout, "b-1") {
		t.Errorf("merged rows must follow mapping declaration order:\n%s", stdout)
	}
	// --json: ONE merged items array; every record carries its project.
	code, stdout, _ = runPf(t, env, "--json", "queue")
	if code != 0 {
		t.Fatalf("merged queue --json: code=%d", code)
	}
	var doc struct {
		Items []struct {
			Project string `json:"project"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		t.Fatalf("merged --json doc: %v (%q)", err, stdout)
	}
	if len(doc.Items) != 2 || doc.Total != 2 {
		t.Fatalf("merged --json items = %d total = %d, want 2/2 (%q)", len(doc.Items), doc.Total, stdout)
	}
	seen := map[string]bool{}
	for _, it := range doc.Items {
		if it.Project == "" {
			t.Errorf("merged item without project identity: %q", stdout)
		}
		seen[it.Project] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Errorf("merged --json does not cover both projects: %v (%q)", seen, stdout)
	}
	// The wire saw one query per mapped project, in mapping order, per
	// merged run (the human run then the --json run: 4 requests total).
	reqs := rec.all()
	if len(reqs) != 4 {
		t.Fatalf("wire requests = %d, want 2 per merged run", len(reqs))
	}
	for i, want := range []string{"project=alpha", "project=beta", "project=alpha", "project=beta"} {
		if !strings.Contains(reqs[i].Path, want) {
			t.Errorf("request[%d] = %s, want it to carry %s", i, reqs[i].Path, want)
		}
	}
}

func TestVerbQueueReadyHumanOutput(t *testing.T) {
	second := strings.Replace(cannedWI, "pf-42", "pf-99", 1)
	body := `{"items":[` + cannedWI + `,` + second + `],"running":[],"paused":[]}`
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	// Human mode: the count and rows are the items segment only.
	code, stdout, stderr := runPf(t, env, "queue", "--project", "polyforge", "--ready")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"2 work item(s)", "pf-42", "pf-99"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}

	// --json stays verbatim passthrough of the full server body.
	code, stdout, stderr = runPf(t, env, "--json", "queue", "--project", "polyforge", "--ready")
	if code != 0 {
		t.Fatalf("json: code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != body {
		t.Errorf("json stdout = %q, want verbatim body", stdout)
	}
}

// TestVerbQueueSessionColumnDecodeRender: a RAW aihub WI body carrying
// session_fingerprint — a key api.WorkItem does not define — decodes
// additively into WIView and renders in the SESSION column of the pf
// queue table, both the plain list form and the --ready form; a body
// WITHOUT the key renders "-" there, exactly as before the v1.5 keys
// existed.
func TestVerbQueueSessionColumnDecodeRender(t *testing.T) {
	held := `{"id":"wi_1","slug":"pf-1","status":"running","priority":"high",` +
		`"project":"polyforge","goal":"fix the thing","created_at":"t0",` +
		`"session_fingerprint":"ab12cd34ef56aa00"}`
	unheld := `{"id":"wi_2","slug":"pf-2","status":"queued","priority":"normal",` +
		`"project":"polyforge","goal":"another goal","created_at":"t0"}`

	// Plain list form: the fingerprint lands in the SESSION column.
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[`+held+`,`+unheld+`],"total":2}`)
	})
	env := map[string]string{"PF_SERVER": srv.URL}
	code, stdout, stderr := runPf(t, env, "queue", "--project", "polyforge")
	if code != 0 {
		t.Fatalf("queue: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueListSessionColumn", stdout,
		`2 work item(s)
SLUG  PROJECT    STATUS   SESSION           PRIORITY  BUNDLE  GOAL
----  ---------  -------  ----------------  --------  ------  -------------
pf-1  polyforge  running  ab12cd34ef56aa00  high      -       fix the thing
pf-2  polyforge  queued   -                 normal    -       another goal
`)

	// --ready form: same additive decode, same SESSION column.
	srv = newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[`+held+`,`+unheld+`]}`)
	})
	env = map[string]string{"PF_SERVER": srv.URL}
	code, stdout, stderr = runPf(t, env, "queue", "--project", "polyforge", "--ready")
	if code != 0 {
		t.Fatalf("queue --ready: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueReadySessionColumn", stdout,
		`2 work item(s)
SLUG  PROJECT    STATUS   SESSION           PRIORITY  BUNDLE  GOAL
----  ---------  -------  ----------------  --------  ------  -------------
pf-1  polyforge  running  ab12cd34ef56aa00  high      -       fix the thing
pf-2  polyforge  queued   -                 normal    -       another goal
`)
}

// TestVerbQueueLimit: --limit is a passthrough of the server list cap.
// On the wire it maps to ?limit=N (1..200, recall's validation style);
// omitted it maps to NOTHING (unlike recall's explicit 20, the owner
// chose omission so the server's own default applies). It is a default-
// list-mode flag only: combining it with --ready is an explicit error,
// never a silent ignore. The render of the capped list is pinned by
// goldens, and --json stays a raw passthrough of the capped body.
func TestVerbQueueLimit(t *testing.T) {
	capped := `{"items":[` + cannedWI + `],"total":1}`
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, capped)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	// Omitted --limit: ?limit= is NOT sent; the server default applies.
	if code, _, stderr := runPf(t, env, "queue", "--project", "polyforge"); code != 0 {
		t.Fatalf("omitted --limit: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); strings.Contains(got.Path, "limit=") {
		t.Errorf("omitted --limit: path = %s, want no limit param", got.Path)
	}

	// Both ends of the in-range contract pass through unchanged.
	for _, n := range []string{"1", "200"} {
		if code, _, stderr := runPf(t, env, "queue", "--project", "polyforge", "--limit", n); code != 0 {
			t.Fatalf("queue --limit %s: code=%d stderr=%s", n, code, stderr)
		}
		if got := rec.last(); !strings.Contains(got.Path, "limit="+n+"&") {
			t.Errorf("--limit %s: path = %s", n, got.Path)
		}
	}

	// Composes with --status on the same wire call.
	if code, _, stderr := runPf(t, env, "queue", "--project", "polyforge", "--status", "queued", "--limit", "5"); code != 0 {
		t.Fatalf("queue --status queued --limit 5: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Path != "/api/v1/work-items?limit=5&project=polyforge&status=queued" {
		t.Errorf("--limit + --status: path = %s", got.Path)
	}

	// Out of range (0, negatives, >200): loud local error in recall's
	// style, non-zero exit, and no request on the wire.
	for _, n := range []string{"0", "-3", "201"} {
		before := len(rec.all())
		code, _, stderr := runPf(t, env, "queue", "--project", "polyforge", "--limit", n)
		if code == 0 {
			t.Fatalf("--limit %s must exit non-zero", n)
		}
		if !strings.Contains(stderr, "queue: --limit must be 1..200") {
			t.Errorf("--limit %s stderr = %q", n, stderr)
		}
		if after := len(rec.all()); after != before {
			t.Errorf("--limit %s sent a request (%d → %d)", n, before, after)
		}
	}

	// --limit + --ready fails explicitly (never silently ignored), with
	// no request on the wire — even for an in-range value.
	for _, n := range []string{"5", "201"} {
		before := len(rec.all())
		code, _, stderr := runPf(t, env, "queue", "--project", "polyforge", "--ready", "--limit", n)
		if code == 0 {
			t.Fatalf("--ready --limit %s must exit non-zero", n)
		}
		if !strings.Contains(stderr, "queue: --limit cannot be combined with --ready") {
			t.Errorf("--ready --limit %s stderr = %q", n, stderr)
		}
		if after := len(rec.all()); after != before {
			t.Errorf("--ready --limit %s sent a request (%d → %d)", n, before, after)
		}
	}
}

// TestVerbQueueLimitRenderGoldens: the CLI render of the capped list.
// The server already applies ?limit=; these goldens pin what the CLI
// does with the (capped) body it gets back — the human table and the
// verbatim --json passthrough.
func TestVerbQueueLimitRenderGoldens(t *testing.T) {
	held := `{"id":"wi_1","slug":"pf-1","status":"running","priority":"high",` +
		`"project":"polyforge","goal":"fix the thing","created_at":"t0",` +
		`"session_fingerprint":"ab12cd34ef56aa00"}`
	unheld := `{"id":"wi_2","slug":"pf-2","status":"queued","priority":"normal",` +
		`"project":"polyforge","goal":"another goal","created_at":"t0"}`
	body := `{"items":[` + held + `,` + unheld + `],"total":2}`
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	// Human render of the capped list.
	code, stdout, stderr := runPf(t, env, "queue", "--project", "polyforge", "--limit", "2")
	if code != 0 {
		t.Fatalf("queue --limit 2: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueListCapped", stdout,
		`2 work item(s)
SLUG  PROJECT    STATUS   SESSION           PRIORITY  BUNDLE  GOAL
----  ---------  -------  ----------------  --------  ------  -------------
pf-1  polyforge  running  ab12cd34ef56aa00  high      -       fix the thing
pf-2  polyforge  queued   -                 normal    -       another goal
`)

	// --json: raw passthrough of the capped server body, untouched.
	code, stdout, stderr = runPf(t, env, "--json", "queue", "--project", "polyforge", "--limit", "2")
	if code != 0 {
		t.Fatalf("json: code=%d stderr=%s", code, stderr)
	}
	golden(t, "QueueListCappedJSON", stdout, body+"\n")
}

func TestVerbCancelRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Replace(cannedWI, `"queued"`, `"canceled"`, 1))
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "cancel", "wi_abc")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); got.Method != "POST" || got.Path != "/api/v1/work-items/wi_abc/cancel" {
		t.Errorf("request = %s %s", got.Method, got.Path)
	}
	if !strings.Contains(stdout, "cancelled pf-42 (status canceled)") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestVerbRecallRequestShape(t *testing.T) {
	var rec reqRecorder
	// The fake answers the REAL search wire shape (memory.Entry rows:
	// content_sha/content_len, never `content`), and attaches
	// content_excerpt only when the request names an excerpt length —
	// exactly the server's contract (repo review 2026-10-16 B-10: the old
	// fake sent `content`, so a CONTENT column that was always empty in
	// production passed here).
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		row := `"id":"mem_1","project_id":"prj_1","owner_user_id":"usr_1","work_item_id":null,"type":"experience.debug","title":null,"visibility":"project","content_sha":"abc","content_len":16,"tags":["x"],"related":[],"status":"active","created_at":"t","updated_at":"t"`
		titled := `"id":"mem_2","project_id":"prj_1","owner_user_id":"usr_1","work_item_id":null,"type":"fact.note","title":"titled lesson","visibility":"project","content_sha":"def","content_len":9,"tags":[],"related":[],"status":"active","created_at":"t","updated_at":"t2"`
		if n, _ := strconv.Atoi(r.URL.Query().Get("excerpt")); n > 0 {
			row += `,"content_excerpt":"first line\nmore"`
			titled += `,"content_excerpt":"body text"`
		}
		io.WriteString(w, `{"items":[{`+row+`},{`+titled+`}],"total":2}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	code, stdout, stderr := runPf(t, env,
		"recall", "--project", "polyforge", "flaky build", "--type", "experience.debug", "--tags", "x,y", "--limit", "5")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "GET" {
		t.Errorf("method = %s", got.Method)
	}
	for _, want := range []string{
		"/api/v1/memories/search?",
		"project=polyforge", "q=flaky+build",
		"type=experience.debug", "tags=x%2Cy", "limit=5", "excerpt=120",
	} {
		if !strings.Contains(got.Path, want) {
			t.Errorf("path missing %s: %s", want, got.Path)
		}
	}
	// The table shows the excerpt's first line for an untitled memory
	// and the title for a titled one.
	for _, want := range []string{"SUMMARY", "first line", "titled lesson"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("recall table missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "body text") || strings.Contains(stdout, "more") {
		t.Errorf("recall table shows more than title / first excerpt line:\n%s", stdout)
	}
}

// TestVerbRecallAnyMode: --any asks the server for the ranked any-term
// mode (match=any); the default sends no match= at all, so the server's
// every-token AND stays the human default.
func TestVerbRecallAnyMode(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "flaky build"); code != 0 {
		t.Fatalf("recall: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); strings.Contains(got.Path, "match=") {
		t.Errorf("default recall must not send match=: %s", got.Path)
	}
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "--any", "flaky build"); code != 0 {
		t.Fatalf("recall --any: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Path, "match=any") {
		t.Errorf("recall --any: path = %s, want match=any", got.Path)
	}
}

// TestVerbRecallLimitContract: --limit is the CLI's product contract —
// the default 20 is sent when the flag is omitted, and 1..200 is
// validated locally so an out-of-range value fails loudly with NO
// request sent. (The endpoint's own 50 default stays a wire backstop
// for other callers and is not the CLI's business.)
func TestVerbRecallLimitContract(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL

	// Omitted --limit: the default 20 is sent explicitly.
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "flaky build"); code != 0 {
		t.Fatalf("recall default limit: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Path, "limit=20") {
		t.Errorf("omitted --limit: path = %s, want limit=20", got.Path)
	}

	// Both ends of the in-range contract pass through unchanged.
	for _, n := range []string{"1", "200"} {
		if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "flaky build", "--limit", n); code != 0 {
			t.Fatalf("recall --limit %s: code=%d stderr=%s", n, code, stderr)
		}
		if got := rec.last(); !strings.Contains(got.Path, "limit="+n+"&") {
			t.Errorf("--limit %s: path = %s", n, got.Path)
		}
	}

	// Out of range (0, negatives, >200): loud local error, non-zero
	// exit, and no request on the wire.
	for _, n := range []string{"0", "-3", "201"} {
		before := len(rec.all())
		code, _, stderr := runPf(t, env, "recall", "flaky build", "--limit", n)
		if code == 0 {
			t.Fatalf("--limit %s must exit non-zero", n)
		}
		if !strings.Contains(stderr, "recall: --limit must be 1..200") {
			t.Errorf("--limit %s stderr = %q", n, stderr)
		}
		if after := len(rec.all()); after != before {
			t.Errorf("--limit %s sent a request (%d → %d)", n, before, after)
		}
	}
}

// TestVerbRecallBrowseMode: with no query the human
// direct path browses — one request with no q, the default limit 20, no
// sort unless given, the cursor passed through unchanged — and prints
// the page with "N of TOTAL" and the next cursor; --json is aihub's
// answer verbatim. A keyword search forwards an explicit --after/--sort
// (aihub answers the mixed shape MEM-400, surfaced as-is); --any with no
// query and an out-of-range --limit are refused with no request sent.
func TestVerbRecallBrowseMode(t *testing.T) {
	var rec reqRecorder
	const page = `{"items":[{"id":"mem_b1","type":"doc.note","title":"browse row","updated_at":"t1","tags":[]}],"total":41,"next_cursor":"cur-2"}`
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") != "" && (q.Has("after") || q.Has("sort")) {
			w.WriteHeader(400)
			io.WriteString(w, `{"code":"MEM-400","detail":"after and sort are browse parameters and cannot be combined with q"}`)
			return
		}
		io.WriteString(w, page)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL

	code, stdout, stderr := runPf(t, env, "recall", "--project", "polyforge")
	if code != 0 {
		t.Fatalf("browse: code=%d stderr=%s", code, stderr)
	}
	got := rec.last().Path
	if strings.Contains(got, "q=") || strings.Contains(got, "sort=") || strings.Contains(got, "after=") ||
		strings.Contains(got, "match=") || !strings.Contains(got, "limit=20") || !strings.Contains(got, "project=polyforge") {
		t.Fatalf("first browse request = %s", got)
	}
	for _, want := range []string{"mem_b1", "browse row", "1 of 41", "next: cur-2"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("browse output lacks %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = runPf(t, env, "--json", "recall", "--project", "polyforge", "--after", "cur-2", "--sort", "created", "--limit", "5")
	if code != 0 {
		t.Fatalf("browse --after: code=%d stderr=%s", code, stderr)
	}
	got = rec.last().Path
	for _, want := range []string{"after=cur-2", "sort=created", "limit=5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cursor browse request lacks %s: %s", want, got)
		}
	}
	if strings.TrimSpace(stdout) != page {
		t.Fatalf("--json browse = %s, want aihub's answer verbatim", stdout)
	}

	for _, extra := range [][]string{{"--after", "cur-2"}, {"--sort", "updated"}} {
		args := append([]string{"recall", "--project", "polyforge", "needle"}, extra...)
		code, _, stderr = runPf(t, env, args...)
		if code == 0 || !strings.Contains(stderr, "MEM-400") {
			t.Fatalf("keyword + %v: code=%d stderr=%s, want aihub's MEM-400", extra, code, stderr)
		}
		if got := rec.last().Path; !strings.Contains(got, "q=needle") {
			t.Fatalf("keyword + %v request = %s", extra, got)
		}
	}

	before := len(rec.all())
	if code, _, stderr = runPf(t, env, "recall", "--project", "polyforge", "--any"); code == 0 || !strings.Contains(stderr, "--any") {
		t.Fatalf("--any browse: code=%d stderr=%s", code, stderr)
	}
	if code, _, stderr = runPf(t, env, "recall", "--project", "polyforge", "--limit", "201"); code == 0 || !strings.Contains(stderr, "1..200") {
		t.Fatalf("browse --limit 201: code=%d stderr=%s", code, stderr)
	}
	if n := len(rec.all()); n != before {
		t.Fatalf("a locally refused browse sent %d request(s)", n-before)
	}
}

// TestVerbRecallTypeForms: --type accepts exactly three forms — a
// trailing .* prefix (passed through for the store's prefix
// interpretation), a lone * (all types: no type filter sent at all),
// and a plain value (exact match). Any other * placement fails loudly
// with no request sent.
func TestVerbRecallTypeForms(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL

	// Trailing .*: sent through (the wire percent-encodes the *).
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "q", "--type", "experience.*"); code != 0 {
		t.Fatalf("recall --type experience.*: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Path, "type=experience.%2A") {
		t.Errorf("--type experience.*: path = %s", got.Path)
	}

	// Plain value: exact match, sent as-is.
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "q", "--type", "experience.debug"); code != 0 {
		t.Fatalf("recall --type experience.debug: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Path, "type=experience.debug") {
		t.Errorf("--type experience.debug: path = %s", got.Path)
	}

	// Lone *: all types — equivalent to omitting --type entirely.
	if code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "q", "--type", "*"); code != 0 {
		t.Fatalf("recall --type *: code=%d stderr=%s", code, stderr)
	}
	if got := rec.last(); strings.Contains(got.Path, "type=") {
		t.Errorf("--type *: path = %s, want no type filter sent", got.Path)
	}

	// Every other * placement: loud local refusal, no request.
	for _, bad := range []string{"*.debug", "exp*rience", "experience*", "experience.**", ".*"} {
		before := len(rec.all())
		code, _, stderr := runPf(t, env, "recall", "--project", "polyforge", "q", "--type", bad)
		if code == 0 {
			t.Fatalf("--type %q must exit non-zero", bad)
		}
		if !strings.Contains(stderr, "recall: --type") {
			t.Errorf("--type %q stderr = %q", bad, stderr)
		}
		if after := len(rec.all()); after != before {
			t.Errorf("--type %q sent a request (%d → %d)", bad, before, after)
		}
	}
}

func TestVerbSubmitPrecheckRefusesTerminal(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Replace(cannedWI, `"queued"`, `"wrapped"`, 1))
	})
	code, _, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL, "PF_DAEMON_SOCKET": "/nonexistent/x.sock"},
		"submit", "wi_abc")
	if code != 1 {
		t.Fatalf("terminal precheck must exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "terminal (wrapped)") {
		t.Errorf("stderr = %q", stderr)
	}
	// No claim may be attempted after the refusal: the only aihub request
	// is the precheck read.
	if n := len(rec.all()); n != 1 {
		t.Errorf("aihub requests = %d, want 1 (precheck only)", n)
	}
}

func TestAdminVerbsRequestShapes(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/users"):
			io.WriteString(w, `{"id":"usr_1","display_name":"A","email":"a@x","role":"writer","user_type":"human","created_at":"t"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/"):
			io.WriteString(w, `{"id":"usr_1","display_name":"B","role":"admin","user_type":"human","created_at":"t"}`)
		case strings.HasSuffix(r.URL.Path, "/api-keys"):
			io.WriteString(w, `{"id":"key_1","key":"pfk_plain","user_id":"usr_1"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/api-keys/"):
			io.WriteString(w, `{"revoked":true}`)
		case r.URL.Path == "/api/v1/projects":
			io.WriteString(w, `{"id":"prj_1","name":"polyforge","visible":true,"created_at":"t","updated_at":"t"}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/projects/"):
			io.WriteString(w, `{"id":"prj_1","name":"polyforge","visible":true,"members":[],"created_at":"t","updated_at":"t"}`)
		case strings.HasSuffix(r.URL.Path, "/backup"):
			io.WriteString(w, `{"backup":"/var/backups/snap.db"}`)
		}
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	if code, _, stderr := runPf(t, env, "users", "create", "--display-name", "A", "--user-type", "machine"); code != 0 {
		t.Fatalf("users create: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "POST" || got.Path != "/api/v1/users" ||
		!strings.Contains(got.Body, `"user_type":"machine"`) {
		t.Errorf("users create = %s %s %s", got.Method, got.Path, got.Body)
	}

	if code, _, stderr := runPf(t, env, "users", "patch", "usr_1", "--role", "admin"); code != 0 {
		t.Fatalf("users patch: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "PATCH" || got.Path != "/api/v1/users/usr_1" ||
		got.Body != `{"role":"admin"}` {
		t.Errorf("users patch = %s %s %s", got.Method, got.Path, got.Body)
	}

	if code, _, stderr := runPf(t, env, "api-keys", "create", "--user", "usr_1", "--project-scope", "prj_1"); code != 0 {
		t.Fatalf("api-keys create: %d %s", code, stderr)
	}
	if got := rec.last(); got.Path != "/api/v1/api-keys" ||
		!strings.Contains(got.Body, `"user_id":"usr_1"`) || !strings.Contains(got.Body, `"project_id":"prj_1"`) {
		t.Errorf("api-keys create = %s %s", got.Method, got.Path)
	}

	if code, _, stderr := runPf(t, env, "api-keys", "revoke", "key_1"); code != 0 {
		t.Fatalf("api-keys revoke: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "DELETE" || got.Path != "/api/v1/api-keys/key_1" {
		t.Errorf("api-keys revoke = %s %s", got.Method, got.Path)
	}

	if code, _, stderr := runPf(t, env, "projects", "create", "--name", "polyforge"); code != 0 {
		t.Fatalf("projects create: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "POST" || got.Path != "/api/v1/projects" ||
		!strings.Contains(got.Body, `"name":"polyforge"`) {
		t.Errorf("projects create = %s %s %s", got.Method, got.Path, got.Body)
	}

	if code, _, stderr := runPf(t, env, "projects", "members", "add", "prj_1", "--user", "usr_1", "--role", "maintainer"); code != 0 {
		t.Fatalf("members add: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "PATCH" || got.Path != "/api/v1/projects/prj_1" ||
		!strings.Contains(got.Body, `"members":[{"user_id":"usr_1","role":"maintainer"}]`) {
		t.Errorf("members add = %s %s %s", got.Method, got.Path, got.Body)
	}

	if code, _, stderr := runPf(t, env, "projects", "members", "remove", "prj_1", "--user", "usr_1", "--user", "usr_2"); code != 0 {
		t.Fatalf("members remove: %d %s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Body, `"remove_members":["usr_1","usr_2"]`) {
		t.Errorf("members remove body = %s", got.Body)
	}

	// `projects patch` with NO --visible flag must NOT carry the key.
	if code, _, stderr := runPf(t, env, "projects", "patch", "prj_1"); code != 0 {
		t.Fatalf("projects patch: %d %s", code, stderr)
	}
	if got := rec.last(); got.Body != "{}" {
		t.Errorf("projects patch body = %s, want {}", got.Body)
	}

	// -repos is the explicit full replacement of the task-repo
	// inventory (pilot item: the wire face was already there, the CLI
	// flag was not). Omitted → key absent; spelled → the full list.
	if code, _, stderr := runPf(t, env, "projects", "patch", "prj_1",
		"--repos", "demo-src=file:///x/demo.git,other=https://h/o.git"); code != 0 {
		t.Fatalf("projects patch --repos: %d %s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Body,
		`"repos":[{"name":"demo-src","url":"file:///x/demo.git"},{"name":"other","url":"https://h/o.git"}]`) {
		t.Errorf("projects patch --repos body = %s", got.Body)
	}
	// the explicit clear: -repos "" carries an empty list.
	if code, _, stderr := runPf(t, env, "projects", "patch", "prj_1", "--repos", ""); code != 0 {
		t.Fatalf("projects patch --repos clear: %d %s", code, stderr)
	}
	if got := rec.last(); !strings.Contains(got.Body, `"repos":[]`) {
		t.Errorf("projects patch --repos clear body = %s", got.Body)
	}
	// a malformed entry is refused client-side, never silently dropped.
	if code, _, _ := runPf(t, env, "projects", "patch", "prj_1", "--repos", "no-url-here"); code == 0 {
		t.Fatal("malformed -repos entry must fail")
	}

	if code, _, stderr := runPf(t, env, "backup"); code != 0 {
		t.Fatalf("backup: %d %s", code, stderr)
	}
	if got := rec.last(); got.Method != "POST" || got.Path != "/api/v1/admin/backup" {
		t.Errorf("backup = %s %s", got.Method, got.Path)
	}
}

// TestFlagBeatsEnvEndToEnd: --server on the command line wins over the
// PF_SERVER environment value on a real verb call (level 3 of the
// config chain; levels 0-2 are pinned in config_test.go).
func TestFlagBeatsEnvEndToEnd(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := map[string]string{"PF_SERVER": "http://env-loses.invalid:1", "PF_API_KEY": "k_env"}
	code, _, stderr := runPf(t, env, "--server", srv.URL, "--api-key", "k_flag", "file", "show", "wi_abc")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	// The flag server received the request; the env server never could.
	if got := rec.last(); got.Header.Get("Authorization") != "Bearer k_flag" {
		t.Errorf("auth = %q, want the flag api key", got.Header.Get("Authorization"))
	}
}

// TestJSONPassthrough: --json emits the server body verbatim.
func TestJSONPassthrough(t *testing.T) {
	const body = `{"id":"wi_abc","slug":"pf-42","seq":42,"extra_unknown_field":  {"kept": true}}`
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "--json", "file", "show", "wi_abc")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != body {
		t.Errorf("--json passthrough altered the body:\n got: %s\nwant: %s", stdout, body)
	}
}

// TestDaemonVerbJSONPassthrough: the UDS face too.
func TestDaemonVerbJSONPassthrough(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	const body = `{"accepted":true,"work_item_id":"wi_abc"}`
	d.answer("POST", "/jobs/wi_abc/claim", body)
	code, stdout, stderr := runPf(t, map[string]string{
		"PF_SERVER":        aihub.URL,
		"PF_DAEMON_SOCKET": d.sock,
	}, "--json", "submit", "wi_abc")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != body {
		t.Errorf("--json passthrough altered the body: %s", stdout)
	}
}

func TestVerbRememberRequestShape(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"mem_new","type":"experience.debug","visibility":"project","created_at":"t","updated_at":"t"}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	code, stdout, stderr := runPf(t, env,
		"remember", "--project", "polyforge", "--type", "experience.debug", "--title", "ladders",
		"--tags", "infra,flaky", "--work-item", "wi_abc",
		"the lesson about bounded retry ladders")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "POST" || got.Path != "/api/v1/memories" {
		t.Errorf("request = %s %s", got.Method, got.Path)
	}
	for _, want := range []string{
		`"project":"polyforge"`, `"type":"experience.debug"`,
		`"title":"ladders"`, `"visibility":"project"`,
		`"tags":["infra","flaky"]`, `"work_item_id":"wi_abc"`,
		`"content":"the lesson about bounded retry ladders"`,
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %s: %s", want, got.Body)
		}
	}
	if !strings.Contains(stdout, "stored mem_new") {
		t.Errorf("stdout = %q", stdout)
	}

	// Validation: a missing --type is a client-side refusal (no wire call).
	srv2 := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {})
	env2 := withMappingConfig(t)
	env2["PF_SERVER"] = srv2.URL
	if code, _, _ := runPf(t, env2,
		"remember", "content only"); code != 1 {
		t.Fatalf("missing --type: code=%d, want 1", code)
	}
}

// TestMachinesListRequestShapeAndHumanRender: `pf machines list` hits
// GET /api/v1/machines with the Bearer key and renders the human table
// (RUNTIME column, LIVE WI as the bare count, server-computed STATE).
func TestMachinesListRequestShapeAndHumanRender(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"count":1,"stale_after_seconds":120,"machines":[`+
			`{"id":"m_1","user_id":"usr_1","runtime_version":"go1.99","mode":"agent",`+
			`"live_wi_ids":["wi_a","wi_b"],"last_heartbeat_at":"t1",`+
			`"heartbeat_age_seconds":42,"stale":false}]}`)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL, "PF_API_KEY": "k_cli"},
		"machines", "list")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := rec.last()
	if got.Method != "GET" || got.Path != "/api/v1/machines" {
		t.Errorf("request = %s %s", got.Method, got.Path)
	}
	if got.Header.Get("Authorization") != "Bearer k_cli" {
		t.Errorf("auth = %q", got.Header.Get("Authorization"))
	}
	if !strings.Contains(stdout, "daemons: 1 (stale threshold 120s)\n") {
		t.Errorf("stdout header missing: %q", stdout)
	}
	// The full row: runtime rendered, LIVE WI the bare count 2 (never the
	// ids themselves), age 42s, STATE alive.
	if !strings.Contains(stdout,
		"m_1  -     -         agent  go1.99   2        42s            t1              alive\n") {
		t.Errorf("stdout row missing or malformed:\n%s", stdout)
	}
	if strings.Contains(stdout, "wi_a") {
		t.Errorf("live wi ids leaked into the human render:\n%s", stdout)
	}
}

// TestMachinesListJSONPassthrough: --json emits the server body
// byte-for-byte — pinned against a body that would FAIL under the old
// typed re-marshal: runtime_version omitempty-elided, an unknown
// top-level field, and non-alphabetical key order.
func TestMachinesListJSONPassthrough(t *testing.T) {
	const body = `{"stale_after_seconds":120,"machines":[` +
		`{"id":"m_2","user_id":"usr_1","live_wi_ids":[],` +
		`"last_heartbeat_at":"t2","stale":true}],` +
		`"count":1,"unknown_top_level":{"kept":true}}`
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	})
	code, stdout, stderr := runPf(t, map[string]string{"PF_SERVER": srv.URL}, "--json", "machines", "list")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) != body {
		t.Errorf("--json passthrough altered the body:\n got: %s\nwant: %s", stdout, body)
	}
}

// TestMachinesListForbidden: the non-admin 403 surfacing — human mode
// exits 1 with the code prominent (`pf: CODE: detail`); --json mode
// re-emits the typed envelope on stderr instead.
func TestMachinesListForbidden(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"code":"AUTH-403","detail":"admin credential required"}`)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	code, _, stderr := runPf(t, env, "machines", "list")
	if code != 1 {
		t.Fatalf("human exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "pf: AUTH-403: admin credential required") {
		t.Errorf("stderr = %q, want the pf: CODE: detail line", stderr)
	}

	code, _, stderr = runPf(t, env, "--json", "machines", "list")
	if code != 1 {
		t.Fatalf("--json exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, `"code":"AUTH-403"`) || strings.Contains(stderr, "pf: AUTH-403") {
		t.Errorf("stderr = %q, want the re-emitted typed envelope only", stderr)
	}
}

// TestMachinesHelpStatesAdminOnly: `pf help machines` documents the
// admin-credential requirement.
func TestMachinesHelpStatesAdminOnly(t *testing.T) {
	code, stdout, stderr := runPf(t, map[string]string{}, "help", "machines")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "admin") {
		t.Errorf("help output must state the admin-only requirement:\n%s", stdout)
	}
}

// TestDaemonHelpTeachesMapping pins the #113 help surface: `pf help
// daemon` documents --config with the [workspace] mapping requirement —
// and lists NONE of the retired selection surface (either dash spelling
// of the deleted flags, or either retired env variable, contiguous).
func TestDaemonHelpTeachesMapping(t *testing.T) {
	code, stdout, stderr := runPf(t, map[string]string{}, "help", "daemon")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"--config", "[workspace", "capsules", "doctor"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("daemon help must mention %q:\n%s", want, stdout)
		}
	}
	// The retired names are assembled from fragments so this file's own
	// bytes never carry them contiguously (the greppable convention).
	for _, tok := range []string{
		"-" + "work" + "space", // either dash spelling: the single-dash form is a substring
		"-" + "proj" + "ect",
		"PF_" + "PROJECT",
		"POLYFORGE_" + "WORKSPACE",
	} {
		if strings.Contains(stdout, tok) {
			t.Errorf("daemon help still lists the retired surface token %q:\n%s", tok, stdout)
		}
	}
}

// pf submit --models: parsed (spaces trimmed), validated locally before
// any request, and carried to the daemon's claim route.
func TestVerbSubmitModels(t *testing.T) {
	var aihubRec reqRecorder
	aihub := newFakeAihub(t, &aihubRec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	d := newDaemonFixture(t)
	d.answer("POST", "/jobs/wi_abc/claim", `{"accepted":true,"work_item_id":"wi_abc"}`)
	env := map[string]string{"PF_SERVER": aihub.URL, "PF_DAEMON_SOCKET": d.sock}

	for _, bad := range []string{"pi:a,,pi:b", "pi:a,pi:a", " ", ",", "pi:a, pi:a", "p/a", "pi:a,p/b", "codex:a", "claude:"} {
		before := len(aihubRec.all())
		code, _, stderr := runPf(t, env, "submit", "wi_abc", "--models", bad)
		if code == 0 || !strings.Contains(stderr, "--models") {
			t.Fatalf("--models %q: code=%d stderr=%q, want a bad-input refusal", bad, code, stderr)
		}
		if len(aihubRec.all()) != before || len(d.rec.all()) != 0 {
			t.Fatalf("--models %q must be refused before any request", bad)
		}
	}

	// polyforge#175: the prefix is mandatory — the refusal names the
	// candidate and the two prefixes, before any request
	if _, _, stderr := runPf(t, env, "submit", "wi_abc", "--models", "pi:x/y,example-gateway/model-a"); !strings.Contains(stderr, `submit: --models: "example-gateway/model-a" needs a harness prefix (pi:… or claude:…)`) {
		t.Fatalf("unprefixed --models refusal: %q", stderr)
	}
	if code, _, stderr := runPf(t, env, "submit", "wi_abc", "--models", " pi:p/a , claude:claude-opus-5-5"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	got := d.rec.all()
	if len(got) != 1 || !strings.Contains(got[0].Body, `"models":["pi:p/a","claude:claude-opus-5-5"]`) {
		t.Fatalf("claim body = %+v, want the trimmed models list", got)
	}
	// without the flag the body carries no models key
	if code, _, stderr := runPf(t, env, "submit", "wi_abc"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := d.rec.all(); strings.Contains(got[1].Body, "models") {
		t.Fatalf("plain submit body must not carry models: %s", got[1].Body)
	}
}

// TestVerbRecallRememberMappingValidated (repo review 2026-10-16 D-5;
// the owner superseded the 2026-10-13 C15 ruling — "recall/remember are
// not mapping-restricted" — on 2026-10-16):
// recall and remember validate --project against the cli.toml
// [workspace] mapping like file create — an unmapped project, or no
// mapping at all, is a local error naming the mapped projects and no
// request reaches aihub; a mapped project goes on the wire; an unset
// --project stays a loud local error.
func TestVerbRecallRememberMappingValidated(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/memories/search"):
			io.WriteString(w, `{"items":[],"total":0}`)
		case strings.Contains(r.URL.Path, "/memories"):
			io.WriteString(w, `{"id":"mem_new","type":"fact.note"}`)
		default:
			io.WriteString(w, cannedWI)
		}
	})
	argsFor := func(verb, project string) []string {
		args := []string{verb}
		if project != "" {
			args = append(args, "--project", project)
		}
		if verb == "remember" {
			args = append(args, "--type", "fact.note")
		}
		return append(args, "probe")
	}

	mapped := withMappingConfig(t) // maps exactly "polyforge"
	mapped["PF_SERVER"] = srv.URL
	unmapped := map[string]string{"PF_SERVER": srv.URL} // no [workspace] section at all
	for _, verb := range []string{"recall", "remember"} {
		before := len(rec.all())
		code, _, stderr := runPf(t, mapped, argsFor(verb, "v4unmapped")...)
		if code != 1 || !strings.Contains(stderr, `project "v4unmapped" is not mapped`) || !strings.Contains(stderr, "mapped here: polyforge") {
			t.Errorf("%s of an unmapped project: code=%d stderr=%s", verb, code, stderr)
		}
		code, _, stderr = runPf(t, unmapped, argsFor(verb, "v4unmapped")...)
		if code != 1 || !strings.Contains(stderr, "no [workspace] mapping is configured") {
			t.Errorf("%s without any mapping: code=%d stderr=%s", verb, code, stderr)
		}
		code, _, stderr = runPf(t, mapped, argsFor(verb, "")...)
		if code != 1 || !strings.Contains(stderr, "required") {
			t.Errorf("%s without --project: code=%d stderr=%s", verb, code, stderr)
		}
		if n := len(rec.all()) - before; n != 0 {
			t.Errorf("%s: refused calls still sent %d request(s) to aihub", verb, n)
		}
		if code, _, stderr := runPf(t, mapped, argsFor(verb, "polyforge")...); code != 0 {
			t.Errorf("%s of the mapped project: code=%d stderr=%s", verb, code, stderr)
		}
		last := rec.last()
		if !strings.Contains(last.Path+last.Body, "polyforge") {
			t.Errorf("%s of the mapped project: request %s %s %s", verb, last.Method, last.Path, last.Body)
		}
	}
}

// TestRequireMappedProjectTyped: the refusal wraps the typed
// *mapping.UnmappedError (callers can errors.As it).
func TestRequireMappedProjectTyped(t *testing.T) {
	plan, err := mapping.Build([]mapping.Entry{{Workspace: "/ws", Projects: []string{"alpha", "beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &Context{Cfg: Config{Mapping: plan}}
	err = requireMappedProject(ctx, "recall", "ghost")
	var ue *mapping.UnmappedError
	if !errors.As(err, &ue) || ue.Project != "ghost" || !strings.Contains(err.Error(), "mapped here: alpha, beta") {
		t.Fatalf("requireMappedProject = %v, want a wrapped *mapping.UnmappedError naming alpha, beta", err)
	}
	if err := requireMappedProject(ctx, "recall", "beta"); err != nil {
		t.Fatalf("mapped project refused: %v", err)
	}
}
