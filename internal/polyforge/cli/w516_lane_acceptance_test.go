package cli

// w516_lane_acceptance_test.go: whether a work item needs
// an acceptance criterion is the lane's own declaration — the workflow.yaml
// key `acceptance: required | none`, absent = required. Every CLI surface
// that judges a ticket without acceptance reads the declaration of the
// ticket's lane; the lanes here are the test's own (nolist declares none,
// plain declares nothing, strict declares required).

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w516Lanes writes the test lanes under a fresh workflows dir: nolist
// (acceptance: none), plain (no declaration) and strict (acceptance:
// required), each one step `work`.
func w516Lanes(t *testing.T) string {
	t.Helper()
	wf := t.TempDir()
	writeLane(t, wf, "nolist", "acceptance: none\nsteps:\n  - { id: work }\n")
	writeLane(t, wf, "plain", "steps:\n  - { id: work }\n")
	writeLane(t, wf, "strict", "acceptance: required\nsteps:\n  - { id: work }\n")
	return wf
}

// w516Submit runs `pf submit wi_abc` for the canned work item bound to
// bundle with content that lists no acceptance criterion, against a fake
// daemon whose /status names the test lanes' workflows dir and which
// accepts the claim; it answers the exit code, stdout, stderr and whether
// the claim route was hit.
func w516Submit(t *testing.T, bundle string) (int, string, string, bool) {
	t.Helper()
	wf := w516Lanes(t)
	var wi map[string]any
	if err := json.Unmarshal([]byte(cannedWI), &wi); err != nil {
		t.Fatal(err)
	}
	wi["bundle_name"] = bundle
	wi["content"] = "## 背景\n\nan idea\n"
	body, _ := json.Marshal(wi)
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	var claimed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-a","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("POST /jobs/wi_abc/claim", func(w http.ResponseWriter, r *http.Request) {
		claimed.Store(true)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"accepted":true,"work_item_id":"wi_abc"}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = aihub.URL
	env["PF_DAEMON_SOCKET"] = newFakeDaemon(t, mux)
	code, stdout, stderr := runPf(t, env, "submit", "wi_abc")
	return code, stdout, stderr, claimed.Load()
}

func TestW516SubmitFollowsTheLaneAcceptance(t *testing.T) {
	t.Run("a lane declaring acceptance none: submitted as is", func(t *testing.T) {
		code, stdout, stderr, claimed := w516Submit(t, "nolist")
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		if !claimed {
			t.Error("the claim route was not hit")
		}
		if want := "submitted pf-42 (accepted by the local daemon)\n"; stdout != want {
			t.Errorf("stdout %q, want %q", stdout, want)
		}
		if strings.Contains(stderr, "--allow-no-acceptance") {
			t.Errorf("an accepted submit prints the reminder: %q", stderr)
		}
	})
	for _, lane := range []string{"plain", "strict"} {
		t.Run("a lane requiring acceptance ("+lane+"): refused", func(t *testing.T) {
			code, _, stderr, claimed := w516Submit(t, lane)
			if code == 0 {
				t.Fatalf("exit 0, want a refusal; stderr=%q", stderr)
			}
			if claimed {
				t.Error("the daemon's claim route was hit: the refusal must come before the claim")
			}
			for _, want := range []string{"pf-42", "## Acceptance", "## 验收", "interaction", "--allow-no-acceptance", "pf file edit"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal does not name %q: %q", want, stderr)
				}
			}
			if strings.Contains(stderr, "develop") || strings.Contains(stderr, "build") {
				t.Errorf("the refusal names a particular lane or step: %q", stderr)
			}
		})
	}
}

// w516StatusDaemon serves GET /status naming wf as the workflows dir; it
// answers the daemon socket path.
func w516StatusDaemon(t *testing.T, wf string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-a","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	return newFakeDaemon(t, mux)
}

func TestW516QueueFollowsTheLaneAcceptance(t *testing.T) {
	const (
		onNone  = `{"id":"wi_nolist","slug":"alpha-nolist","project":"alpha","status":"queued","priority":"normal","goal":"no acceptance, lane none","bundle_name":"nolist","content":null,"created_at":"t"}`
		onPlain = `{"id":"wi_plain","slug":"alpha-plain","project":"alpha","status":"queued","priority":"normal","goal":"no acceptance, lane default","bundle_name":"plain","content":null,"created_at":"t"}`
	)
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") == "alpha" {
			io.WriteString(w, `{"items":[`+onNone+`,`+onPlain+`],"total":2}`)
			return
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	env := withTwoProjectMapping(t)
	env["PF_SERVER"] = srv.URL
	env["PF_DAEMON_SOCKET"] = w516StatusDaemon(t, w516Lanes(t))

	const label = "需要交互：缺验收"
	code, stdout, stderr := runPf(t, env, "queue")
	if code != 0 {
		t.Fatalf("pf queue: code=%d stderr=%s", code, stderr)
	}
	row := func(slug string) string {
		for _, l := range strings.Split(stdout, "\n") {
			if strings.Contains(l, slug) {
				return l
			}
		}
		t.Fatalf("pf queue: no row for %s:\n%s", slug, stdout)
		return ""
	}
	if strings.Contains(row("alpha-nolist"), label) {
		t.Errorf("pf queue: a ticket on a lane declaring acceptance none is marked %s:\n%s", label, stdout)
	}
	if !strings.Contains(row("alpha-plain"), label) {
		t.Errorf("pf queue: a ticket without acceptance on a lane requiring it is not marked %s:\n%s", label, stdout)
	}

	code, stdout, stderr = runPf(t, env, "--json", "queue")
	if code != 0 {
		t.Fatalf("pf --json queue: code=%d stderr=%s", code, stderr)
	}
	var doc struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		t.Fatalf("merged --json doc: %v (%q)", err, stdout)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("merged --json items = %d, want 2 (%q)", len(doc.Items), stdout)
	}
	for _, it := range doc.Items {
		var id string
		_ = json.Unmarshal(it["id"], &id)
		v, has := it["acceptance_missing"]
		switch id {
		case "wi_nolist":
			if has {
				t.Errorf("merged --json: %s (lane declaring acceptance none) must not carry acceptance_missing (got %s)", id, v)
			}
		case "wi_plain":
			if !has || string(v) != "true" {
				t.Errorf("merged --json: %s must carry \"acceptance_missing\": true (got %s, present %v)", id, v, has)
			}
		}
	}
}

func TestW516FileCreateCarrierHintFollowsTheLaneAcceptance(t *testing.T) {
	create := func(t *testing.T, lane string) string {
		t.Helper()
		env, rec := carrierEnv(t, w516StatusDaemon(t, w516Lanes(t)))
		code, _, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "g", "--bundle", lane)
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
		sent := false
		for _, r := range rec.all() {
			if r.Method == "POST" {
				sent = true
			}
		}
		if !sent {
			t.Error("the create request was not sent")
		}
		return stderr
	}
	t.Run("a lane declaring acceptance none: no carrier hint", func(t *testing.T) {
		stderr := create(t, "nolist")
		if strings.Contains(stderr, carrierHint) || strings.Contains(stderr, "carried by") {
			t.Errorf("file create on a lane declaring acceptance none prints the carrier hint: %q", stderr)
		}
	})
	t.Run("a lane requiring acceptance: the carrier hint", func(t *testing.T) {
		stderr := create(t, "plain")
		if want := carrierHint + " plain = work"; !strings.Contains(stderr, want) {
			t.Errorf("hint %q missing; stderr=%q", want, stderr)
		}
	})
}

// w516SubmitOn is w516Submit against the lanes under the workflows dir wf
// instead of the test's own.
func w516SubmitOn(t *testing.T, wf, bundle string) (int, string, string, bool) {
	t.Helper()
	var wi map[string]any
	if err := json.Unmarshal([]byte(cannedWI), &wi); err != nil {
		t.Fatal(err)
	}
	wi["bundle_name"] = bundle
	wi["content"] = "## 背景\n\nan idea\n"
	body, _ := json.Marshal(wi)
	var rec reqRecorder
	aihub := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	var claimed atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m-a","workflows_dir":"`+wf+`","jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("POST /jobs/wi_abc/claim", func(w http.ResponseWriter, r *http.Request) {
		claimed.Store(true)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"accepted":true,"work_item_id":"wi_abc"}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = aihub.URL
	env["PF_DAEMON_SOCKET"] = newFakeDaemon(t, mux)
	code, stdout, stderr := runPf(t, env, "submit", "wi_abc")
	return code, stdout, stderr, claimed.Load()
}

// TestW516ShippedLanesAcceptance: the repository's own lanes, as a daemon
// serving them names them. develop and micro declare nothing and still
// refuse a work item without acceptance; every other shipped lane (release
// and the lane that gates the landed work — four lanes in all) declares
// acceptance none, so its work item is submitted as is, with no
// --allow-no-acceptance. All four validate.
func TestW516ShippedLanesAcceptance(t *testing.T) {
	wf, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := workflow.LaneDirs(wf)
	if err != nil {
		t.Fatal(err)
	}
	requires := map[string]bool{"develop": true, "micro": true}
	var lanes []string
	shipped := map[string]bool{}
	for _, d := range dirs {
		lanes = append(lanes, filepath.Base(d))
		shipped[filepath.Base(d)] = true
	}
	sort.Strings(lanes)
	if len(lanes) != 4 || !shipped["develop"] || !shipped["micro"] || !shipped["release"] {
		t.Fatalf("the shipped lanes are %v: want develop, micro, release and the gating lane — a new shipped lane states its acceptance here", lanes)
	}
	for _, lane := range lanes {
		if requires[lane] {
			t.Run(lane+" requires acceptance: refused", func(t *testing.T) {
				code, _, stderr, claimed := w516SubmitOn(t, wf, lane)
				if code == 0 {
					t.Fatalf("exit 0, want a refusal; stderr=%q", stderr)
				}
				if claimed {
					t.Error("the daemon's claim route was hit: the refusal must come before the claim")
				}
				if !strings.Contains(stderr, "--allow-no-acceptance") {
					t.Errorf("the refusal does not name --allow-no-acceptance: %q", stderr)
				}
			})
			continue
		}
		t.Run(lane+" declares acceptance none: submitted as is", func(t *testing.T) {
			code, stdout, stderr, claimed := w516SubmitOn(t, wf, lane)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			if !claimed {
				t.Error("the claim route was not hit")
			}
			if want := "submitted pf-42 (accepted by the local daemon)\n"; stdout != want {
				t.Errorf("stdout %q, want %q", stdout, want)
			}
		})
	}
	t.Run("pf workflows validate: every shipped lane", func(t *testing.T) {
		code, stdout, stderr := runPf(t, nil, append([]string{"workflows", "validate"}, dirs...)...)
		if code != 0 {
			t.Errorf("pf workflows validate %v: exit %d\n%s%s", lanes, code, stdout, stderr)
		}
	})
}
