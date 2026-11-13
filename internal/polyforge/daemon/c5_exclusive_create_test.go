package daemon

// c5_exclusive_create_test.go: a lane's `exclusive: true` (workflow
// contract 1). A work-item create the proxy forwards on a lane of the
// project's lane set declaring it reaches aihub with `"exclusive": true`;
// every other request — another lane, no lane, a lane the set does not
// carry, a body that is no JSON object, another path or method — reaches it
// byte for byte as sent. The shipped lane set declares it on integrate
// alone.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// c5ShippedLanes is the shipped lane set, from this package.
const c5ShippedLanes = "../../../workflows"

// c5LaneSet writes a built-in lane set — gate declaring exclusive: true,
// build not — and a workspace mapping proj whose user lanes add mine,
// declaring exclusive: true; it answers a daemon over them.
func c5LaneSet(t *testing.T) *Daemon {
	t.Helper()
	builtin, ws := t.TempDir(), t.TempDir()
	workflowtest.WriteLane(t, builtin, "gate", "exclusive: true\nsteps:\n  - { id: check }\n")
	workflowtest.WriteLane(t, builtin, "build", "exclusive: false\nsteps:\n  - { id: work }\n")
	workflowtest.WriteLane(t, workflow.UserLanesRoot(ws), "mine", "exclusive: true\nsteps:\n  - { id: work }\n")
	return &Daemon{cfg: Config{WorkflowsDir: builtin, Mapping: mappingFor(ws, "proj")}}
}

func TestC5ExclusiveCreateOnTheDeclaredLane(t *testing.T) {
	d := c5LaneSet(t)
	exclusive := func(body string) bool {
		t.Helper()
		out := d.exclusiveCreate(http.MethodPost, "/api/v1/work-items", []byte(body))
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatalf("%s answered %s: %v", body, out, err)
		}
		return fields["exclusive"] == true
	}
	for _, body := range []string{
		`{"project":"proj","goal":"g","bundle_name":"gate"}`,
		`{"project":"other","goal":"g","bundle_name":"gate","exclusive":false}`, // the built-in lane, whatever the project; a false is overridden
		`{"project":"proj","goal":"g","bundle_name":"mine"}`,                    // the project workspace's user lane
	} {
		if !exclusive(body) {
			t.Fatalf("a create on a lane declaring exclusive: true is forwarded exclusive: %s", body)
		}
	}
	out := d.exclusiveCreate(http.MethodPost, "/api/v1/work-items", []byte(`{"project":"proj","goal":"g","content":"x","labels":["a"],"bundle_name":"gate","after":["p#1"]}`))
	var fields map[string]any
	_ = json.Unmarshal(out, &fields)
	if fields["goal"] != "g" || fields["content"] != "x" || fields["bundle_name"] != "gate" || len(fields["labels"].([]any)) != 1 || len(fields["after"].([]any)) != 1 || len(fields) != 7 {
		t.Fatalf("only exclusive is added, every other field kept: %s", out)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/work-items", `{"project":"proj","goal":"g","bundle_name":"build"}`},    // declares exclusive: false
		{"POST", "/api/v1/work-items", `{"project":"other","goal":"g","bundle_name":"mine"}`},    // another workspace's user lane
		{"POST", "/api/v1/work-items", `{"project":"proj","goal":"g","bundle_name":"ghost"}`},    // no lane of the set
		{"POST", "/api/v1/work-items", `{"project":"proj","goal":"g"}`},                          // no bundle
		{"POST", "/api/v1/work-items", `{"project":"proj","goal":"g","bundle_name":null}`},       // no bundle
		{"POST", "/api/v1/work-items", `[{"project":"proj","bundle_name":"gate"}]`},              // no JSON object
		{"POST", "/api/v1/work-items", `{"project":"proj","bundle_name":"gate"`},                 // no JSON
		{"PATCH", "/api/v1/work-items", `{"project":"proj","goal":"g","bundle_name":"gate"}`},    // no create
		{"POST", "/api/v1/work-items/wi_a", `{"project":"proj","bundle_name":"gate"}`},           // no create
		{"POST", "/api/v1/work-items?x=1", `{"project":"proj","goal":"g","bundle_name":"gate"}`}, // no create aihubclient sends
	} {
		if out := d.exclusiveCreate(c.method, c.path, []byte(c.body)); string(out) != c.body {
			t.Fatalf("%s %s %s is forwarded as sent, got %s", c.method, c.path, c.body, out)
		}
	}
	if out := (&Daemon{}).exclusiveCreate("POST", "/api/v1/work-items", []byte(`{"project":"proj","bundle_name":"gate"}`)); string(out) != `{"project":"proj","bundle_name":"gate"}` {
		t.Fatalf("no workflows dir, no lane set, nothing added: %s", out)
	}
}

// TestC5ShippedLanesExclusive: the shipped lane set declares exclusive:
// true on integrate alone (one unfinished batch per project), so the proxy
// forwards an integrate create exclusive and a develop create as sent.
func TestC5ShippedLanesExclusive(t *testing.T) {
	dirs, err := workflow.LaneDirs(c5ShippedLanes)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{WorkflowsDir: c5ShippedLanes}}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		if got, want := d.laneExclusive("p", name), name == "integrate"; got != want {
			t.Fatalf("the shipped lane %s: exclusive %v, want %v", name, got, want)
		}
	}
}

// TestC5ProxyForwardsTheCreateExclusive: through the Host's proxy, the
// Hub receives an exclusive lane's create with "exclusive": true and
// another lane's byte for byte; the Hub's refusal comes back unchanged.
func TestC5ProxyForwardsTheCreateExclusive(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	var got []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"CORE-409","detail":"p#8 (queued) of project proj is still open on bundle gate: one at a time","extra":{"reason":"OPEN_ON_BUNDLE"}}`))
	}))
	defer hub.Close()
	cell := th.cell("default").d
	cell.hub = aihubclient.New(hub.URL, "def-key")
	cell.cfg.WorkflowsDir = c5LaneSet(t).cfg.WorkflowsDir
	post := func(body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", "http://daemon/aihub/api/v1/work-items", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := th.client("").HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	code, answer := post(`{"project":"proj","goal":"g","bundle_name":"gate"}`)
	if code != http.StatusConflict || !strings.Contains(answer, "OPEN_ON_BUNDLE") {
		t.Fatalf("the Hub's refusal comes back unchanged: %d %s", code, answer)
	}
	post(`{"project":"proj","goal":"g","bundle_name":"build"}`)
	if len(got) != 2 || !strings.Contains(got[0], `"exclusive":true`) || got[1] != `{"project":"proj","goal":"g","bundle_name":"build"}` {
		t.Fatalf("the Hub received %q", got)
	}
}
