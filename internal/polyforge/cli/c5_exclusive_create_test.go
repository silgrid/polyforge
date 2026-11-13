package cli

// c5_exclusive_create_test.go: workflow contract 5 files an integration
// batch like any work item, `pf file create --bundle integrate` + `pf
// submit`; `pf ops integrate` is gone. A lane declaring `exclusive: true`
// has its creates forwarded exclusive by the daemon (daemon package,
// TestC5ExclusiveCreateOnTheDeclaredLane), so aihub refuses a second
// unfinished work item of the project on it CORE-409 OPEN_ON_BUNDLE. `pf
// file create` answers that refusal with its typed code, naming the open
// work item and its status; a queued one no machine ever claimed (its filer
// stopped between filing and submitting it) is pointed at `pf submit` /
// `pf cancel`.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const c5OpenOnBundle = `{"code":"CORE-409","detail":"polyforge#8 (queued) of project polyforge is still open on bundle integrate: one at a time","extra":{"reason":"OPEN_ON_BUNDLE"}}`

func TestC5FileCreateExclusiveRefusal(t *testing.T) {
	const filed = "2026-11-12T08:00:00Z"
	done := `{"id":"wi_w","slug":"polyforge#5","seq":5,"status":"wrapped","created_at":"2026-11-01T08:00:00Z"}`
	stale := `{"id":"wi_o","slug":"polyforge#8","seq":8,"status":"queued","created_at":"` + filed + `"}`
	claimed := `{"id":"wi_o","slug":"polyforge#8","seq":8,"status":"queued","assignee":"u_1","created_at":"` + filed + `"}`
	running := `{"id":"wi_o","slug":"polyforge#8","seq":8,"status":"running","assignee":"u_1","created_at":"` + filed + `"}`
	later := `{"id":"wi_l","slug":"polyforge#9","seq":9,"status":"blocked","assignee":"u_1","created_at":"` + filed + `"}`
	hint := []string{"filed at " + filed, "no machine has claimed it", "`pf submit polyforge#8`", "`pf cancel polyforge#8`"}
	for _, tc := range []struct {
		name      string
		list      string // "" = the list fails
		want      string
		pointedAt bool
	}{
		{"queued, never claimed", done + "," + stale, "file create: polyforge#8 of polyforge is queued on the lane integrate, which declares exclusive: true — one unfinished work item at a time, nothing was filed", true},
		{"queued again after a claim", claimed, "file create: polyforge#8 of polyforge is queued on the lane integrate", false},
		{"running", running + "," + done, "file create: polyforge#8 of polyforge is running on the lane integrate", false},
		{"the first filed of two, as aihub names it", later + "," + stale, "file create: polyforge#8 of polyforge is queued on the lane integrate", true},
		{"the open one not read back: aihub's refusal", "", "polyforge#8 (queued) of project polyforge is still open on bundle integrate: one at a time", false},
		{"finished meanwhile: aihub's refusal", done, "polyforge#8 (queued) of project polyforge is still open on bundle integrate: one at a time", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec reqRecorder
			srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "POST" && r.URL.Path == "/api/v1/work-items":
					w.WriteHeader(http.StatusConflict)
					io.WriteString(w, c5OpenOnBundle)
				case r.Method == "GET" && r.URL.Path == "/api/v1/work-items" && tc.list != "" &&
					r.URL.Query().Get("project") == "polyforge" && r.URL.Query().Get("bundle") == "integrate":
					io.WriteString(w, `{"items":[`+tc.list+`]}`)
				default:
					w.WriteHeader(http.StatusInternalServerError)
					io.WriteString(w, `{"code":"CORE-500","detail":"down"}`)
				}
			})
			env := withMappingConfig(t)
			env["PF_SERVER"] = srv.URL
			code, stdout, stderr := runPf(t, env, "file", "create", "--project", "polyforge", "--goal", "integrate: gate the landed work items", "--bundle", "integrate")
			if code != 1 || !strings.Contains(stderr, "CORE-409") || !strings.Contains(stderr, tc.want) {
				t.Fatalf("refused with its typed code, naming the open work item and its status: %d %s %s", code, stdout, stderr)
			}
			for _, h := range hint {
				if strings.Contains(stderr, h) != tc.pointedAt {
					t.Fatalf("the pointer %q only for a queued work item never claimed (want %v): %s", h, tc.pointedAt, stderr)
				}
			}
			posts := 0
			for _, r := range rec.all() {
				if r.Method == "POST" {
					posts++
				}
			}
			if posts != 1 {
				t.Fatalf("one create, never retried: %d", posts)
			}
		})
	}

	// --json: the typed envelope carries the code, the message and aihub's
	// reason
	srv := newFakeAihub(t, &reqRecorder{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, c5OpenOnBundle)
			return
		}
		io.WriteString(w, `{"items":[`+stale+`]}`)
	})
	env := withMappingConfig(t)
	env["PF_SERVER"] = srv.URL
	code, _, stderr := runPf(t, env, "--json", "file", "create", "--project", "polyforge", "--goal", "g", "--bundle", "integrate")
	var envelope struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Extra  struct {
			Reason string `json:"reason"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &envelope); err != nil || code != 1 || envelope.Code != "CORE-409" ||
		envelope.Extra.Reason != "OPEN_ON_BUNDLE" || !strings.Contains(envelope.Detail, "`pf submit polyforge#8`") {
		t.Fatalf("the --json envelope: %d %s %v", code, stderr, err)
	}
}

// TestC5OpsIntegrateIsGone: `pf ops integrate` is no command — refused as
// an unknown action, asking neither the daemon nor aihub anything — and
// the plain `pf ops status` asks the daemon's GET /status with no query
// (ported from the deleted w404 test of the verb).
func TestC5OpsIntegrateIsGone(t *testing.T) {
	d := newDaemonFixture(t)
	d.answers["GET /status"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"machine_id":"m1","auto":false,"workspaces":[],"shutting_down":false,"jobs":[],"queue":{"pending":0,"running":0,"max_concurrent":2}}`)
	}
	var hub reqRecorder
	srv := newFakeAihub(t, &hub, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	env := map[string]string{"PF_DAEMON_SOCKET": d.sock, "PF_SERVER": srv.URL}
	for _, args := range [][]string{{"ops", "integrate", "--project", "p"}, {"ops", "integrate", "status"}, {"ops", "integrate", "--clear-hold", "p#1", "--reason", "r"}} {
		code, stdout, stderr := runPf(t, env, args...)
		if code == 0 || !strings.Contains(stderr, `ops: unknown action "integrate"`) {
			t.Fatalf("%v is no command: %d %s %s", args, code, stdout, stderr)
		}
	}
	if len(d.rec.all()) != 0 || len(hub.all()) != 0 {
		t.Fatalf("nothing is asked: daemon %+v, aihub %+v", d.rec.all(), hub.all())
	}
	if code, _, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "ops", "status"); code != 0 {
		t.Fatalf("ops status: %d %s", code, stderr)
	}
	for _, r := range d.rec.all() {
		if r.Path != "/status" {
			t.Fatalf("pf ops status asked %s: it asks the plain status", r.Path)
		}
	}
}
