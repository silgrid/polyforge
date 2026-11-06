package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// TestOpsRoleIsTheMachineRoleEntry ( AC5): the interactive
// role request and its --force takeover — once a side effect of `pf chat
// init` — are `pf ops role`; the default Cell unless --profile names one.
func TestOpsRoleIsTheMachineRoleEntry(t *testing.T) {
	var mu sync.Mutex
	var bodies, profiles []string
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		profiles = append(profiles, r.Method+" "+r.URL.Path+" "+r.Header.Get(protocol.ProfileHeader))
		switch r.URL.Path {
		case "/machine/role":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			raw, _ := json.Marshal(b)
			bodies = append(bodies, string(raw))
			_, _ = w.Write(raw)
		case "/status":
			_, _ = w.Write([]byte(`{"machine_id":"m1","queue":{},"machine_face":{"role":"runner","superseded_by":"m-other"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	env := map[string]string{"PF_DAEMON_SOCKET": sock}
	if code, out, stderr := runPf(t, env, "ops", "role", "interactive", "--force"); code != 0 || !strings.Contains(out, "takeover") {
		t.Fatalf("role --force: %d %q %q", code, out, stderr)
	}
	if code, out, stderr := runPf(t, env, "--profile", "alpha", "ops", "role", "runner"); code != 0 || !strings.Contains(out, "role runner requested for Cell alpha") {
		t.Fatalf("role runner: %d %q %q", code, out, stderr)
	}
	if code, out, _ := runPf(t, env, "ops", "role"); code != 0 || !strings.Contains(out, "role: runner (superseded by m-other)") {
		t.Fatalf("bare role: %d %q", code, out)
	}
	// the adapters' request: never undoes an explicit choice (the daemon decides)
	if code, out, stderr := runPf(t, env, "ops", "role", "interactive", "--if-unset"); code != 0 {
		t.Fatalf("--if-unset: %d %q %q", code, out, stderr)
	}
	// the Cell resolves like every Cell-scoped command: PF_PROFILE names it
	beta := map[string]string{"PF_DAEMON_SOCKET": sock, "PF_PROFILE": "beta"}
	if code, out, stderr := runPf(t, beta, "ops", "role", "interactive"); code != 0 || !strings.Contains(out, "Cell beta") {
		t.Fatalf("role under PF_PROFILE: %d %q %q", code, out, stderr)
	}
	if code, _, stderr := runPf(t, env, "ops", "role", "--if-unset"); code == 0 || !strings.Contains(stderr, "--if-unset needs a role") {
		t.Fatalf("bare --if-unset: %d %q", code, stderr)
	}
	for _, bad := range [][]string{{"ops", "role", "boss"}, {"ops", "role", "runner", "--force"}, {"ops", "role", "--force"}, {"ops", "role", "interactive", "--if-unset", "--force"}} {
		if code, _, _ := runPf(t, env, bad...); code == 0 {
			t.Fatalf("%v must be refused", bad)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 4 || bodies[0] != `{"force":true,"role":"interactive"}` || bodies[1] != `{"force":false,"role":"runner"}` ||
		bodies[2] != `{"force":false,"if_unset":true,"role":"interactive"}` {
		t.Fatalf("bodies = %v", bodies)
	}
	want := []string{"POST /machine/role default", "POST /machine/role alpha", "GET /status default", "POST /machine/role default", "POST /machine/role beta"}
	if strings.Join(profiles, "|") != strings.Join(want, "|") {
		t.Fatalf("requests = %v, want %v", profiles, want)
	}
}
