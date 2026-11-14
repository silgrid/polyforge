package cli

// carries: internal/polyforge/cli/*, internal/polyforge/mapping/*

// w17_routed_placement_test.go (polyforge#17): the placement validation
// of `file create`, `recall` and `remember` reads the [workspace] mapping
// of the Cell the invocation routes to (profile.go's resolution: --profile
// → PF_PROFILE → the Host's effective workspace index → default), not the
// Host file's mapping. The fixture is the oracle's dual-Cell shape: a Host
// file mapping one workspace (the default Cell), a named Cell file mapping
// another, and a fake Host socket answering GET /profiles with the
// dual-Cell effective index and proxying /aihub/* to the AIHub of the
// X-PF-Profile Cell. Every case drives the real Run entry and is judged on
// the exit code, the hub each request reached (or that none did) and the
// error text.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// w17Hub is one Cell's fake AIHub: its recorder counts what reached it.
type w17Hub struct {
	rec *reqRecorder
	srv *httptest.Server
}

// w17FakeHub answers the three placement verbs' wire shapes: the create,
// the memory search and the memory write.
func w17FakeHub(t *testing.T) *w17Hub {
	t.Helper()
	h := &w17Hub{rec: &reqRecorder{}}
	h.srv = newFakeAihub(t, h.rec, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/memories/search"):
			io.WriteString(w, `{"items":[],"total":0}`)
		case strings.Contains(r.URL.Path, "/memories"):
			io.WriteString(w, `{"id":"mem_new","type":"fact.note"}`)
		default:
			io.WriteString(w, cannedWI)
		}
	})
	return h
}

// w17CellHost is the fake Host on a unix socket: GET /profiles answers the
// dual-Cell effective workspace index, GET /status the acceptance-carrier
// facts (a workflows dir holding a develop lane), and the data-plane proxy
// ANY /aihub/<path> forwards to the AIHub of the X-PF-Profile Cell ("" and
// "default" → the default hub) — the profile header is consumed, exactly
// like the real Host's proxy.
func w17CellHost(t *testing.T, def, alpha *w17Hub, wsD, wsA string) string {
	t.Helper()
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n  - { id: review }\n  - { id: sync }\n  - { id: verification }\n  - { id: publish }\n  - { id: merge }\n")
	proxies := map[string]*httputil.ReverseProxy{}
	for profile, hub := range map[string]*w17Hub{"default": def, "alpha": alpha} {
		u, err := url.Parse(hub.srv.URL)
		if err != nil {
			t.Fatalf("hub URL %s: %v", hub.srv.URL, err)
		}
		p := httputil.NewSingleHostReverseProxy(u)
		base := p.Director
		p.Director = func(r *http.Request) {
			base(r)
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
			r.URL.RawPath = ""
			r.Header.Del(protocol.ProfileHeader)
		}
		proxies[profile] = p
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /profiles", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ProfilesResponse{
			Profiles: []ProfileInfo{
				{Profile: "default", Valid: true, State: "serving", Workspaces: []string{wsD}},
				{Profile: "alpha", Valid: true, State: "serving", Workspaces: []string{wsA}},
			},
			Index: []cellcfg.IndexEntry{
				{Profile: "default", Workspace: wsD},
				{Profile: "alpha", Workspace: wsA},
			},
		})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"machine_id":"m-w17","workflows_dir":`+jsonBody(wf)+`,"jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		profile := r.Header.Get(protocol.ProfileHeader)
		if profile == "" {
			profile = "default"
		}
		p, ok := proxies[profile]
		if !ok || !strings.HasPrefix(r.URL.Path, "/aihub/") {
			http.NotFound(w, r)
			return
		}
		p.ServeHTTP(w, r)
	})
	sock := filepath.Join(shortTempDir(t), "w17.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// w17DualCellFixture builds the dual-Cell shape: a Host file mapping wsD
// to "dproj" (the default Cell), a named Cell file cli-alpha.toml mapping
// wsA to "aproj", and the fake Host socket. The env drives runPf; def and
// alpha are the two Cells' fake AIHubs.
func w17DualCellFixture(t *testing.T) (env map[string]string, wsD, wsA string, def, alpha *w17Hub) {
	t.Helper()
	def, alpha = w17FakeHub(t), w17FakeHub(t)
	wsD, wsA = t.TempDir(), t.TempDir()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := "server = " + jsonBody(def.srv.URL) + "\napi_key = \"kd\"\n\n" +
		"[workspace.\"" + wsD + "\"]\nprojects = [\"dproj\"]\n"
	cell := "server = " + jsonBody(alpha.srv.URL) + "\napi_key = \"ka\"\n\n" +
		"[workspace.\"" + wsA + "\"]\nprojects = [\"aproj\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli-alpha.toml"), []byte(cell), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := w17CellHost(t, def, alpha, wsD, wsA)
	env = map[string]string{"XDG_CONFIG_HOME": xdg, "PF_DAEMON_SOCKET": sock, "PF_PROFILE": ""}
	return env, wsD, wsA, def, alpha
}

// w17WantOnlyOn asserts every request so far went to exactly one hub: n
// on "got", none anywhere else.
func w17Counts(def, alpha *w17Hub) (int, int) {
	return len(def.rec.all()), len(alpha.rec.all())
}

// TestW17FileCreatePlacesThroughTheRoutedCell (AC1): a project mapped
// only in the named Cell's file passes that Cell's local validation and
// the create reaches that Cell's AIHub — named by --profile and by the
// cwd match alike; the cwd inference answers from the routed Cell's
// mapping; the default Cell's own shape is unchanged.
func TestW17FileCreatePlacesThroughTheRoutedCell(t *testing.T) {
	env, wsD, wsA, def, alpha := w17DualCellFixture(t)

	// --profile alpha, a project mapped only in alpha's file.
	code, _, stderr := runPf(t, env, "--profile", "alpha", "file", "create",
		"--project", "aproj", "--goal", "w17 flag-routed")
	if code != 0 {
		t.Fatalf("--profile alpha create: code=%d stderr=%s", code, stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 1 {
		t.Fatalf("flag-routed create: default hub got %d, alpha hub got %d (want 0/1)", nDef, nAlpha)
	}
	if body := alpha.rec.last().Body; !strings.Contains(body, "aproj") {
		t.Fatalf("alpha hub saw no aproj create: %s", body)
	}

	// Inside alpha's mapped workspace, without --profile: the resolver's
	// cwd match routes to alpha.
	t.Chdir(wsA)
	code, _, stderr = runPf(t, env, "file", "create", "--project", "aproj", "--goal", "w17 cwd-routed")
	if code != 0 {
		t.Fatalf("cwd-routed create: code=%d stderr=%s", code, stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 2 {
		t.Fatalf("cwd-routed create: default hub got %d, alpha hub got %d (want 0/2)", nDef, nAlpha)
	}

	// Without --project: the inference answers aproj from the routed
	// Cell's mapping.
	code, _, stderr = runPf(t, env, "file", "create", "--goal", "w17 inferred")
	if code != 0 {
		t.Fatalf("inferred create in wsA: code=%d stderr=%s", code, stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 3 {
		t.Fatalf("inferred create: default hub got %d, alpha hub got %d (want 0/3)", nDef, nAlpha)
	}
	if body := alpha.rec.last().Body; !strings.Contains(body, "aproj") {
		t.Fatalf("inferred create carried no aproj: %s", body)
	}

	// The default Cell keeps its shape: --profile default places dproj
	// through the default hub, and inside wsD the cwd match routes there.
	code, _, stderr = runPf(t, env, "--profile", "default", "file", "create",
		"--project", "dproj", "--goal", "w17 default by flag")
	if code != 0 {
		t.Fatalf("--profile default create: code=%d stderr=%s", code, stderr)
	}
	t.Chdir(wsD)
	code, _, stderr = runPf(t, env, "file", "create", "--project", "dproj", "--goal", "w17 default by cwd")
	if code != 0 {
		t.Fatalf("cwd-routed default create: code=%d stderr=%s", code, stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 2 || nAlpha != 3 {
		t.Fatalf("default-cell creates: default hub got %d, alpha hub got %d (want 2/3)", nDef, nAlpha)
	}
	if body := def.rec.last().Body; !strings.Contains(body, "dproj") {
		t.Fatalf("default hub saw no dproj create: %s", body)
	}
}

// TestW17RecallRememberPlaceThroughTheRoutedCell (AC2): recall and
// remember validate --project against the routed Cell's mapping and reach
// that Cell's AIHub — named by --profile and by the cwd match alike.
func TestW17RecallRememberPlaceThroughTheRoutedCell(t *testing.T) {
	env, _, wsA, def, alpha := w17DualCellFixture(t)

	code, _, stderr := runPf(t, env, "--profile", "alpha", "recall", "--project", "aproj", "the query")
	if code != 0 {
		t.Fatalf("--profile alpha recall: code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runPf(t, env, "--profile", "alpha", "remember", "--project", "aproj",
		"--type", "fact.note", "w17 body")
	if code != 0 {
		t.Fatalf("--profile alpha remember: code=%d stderr=%s", code, stderr)
	}

	t.Chdir(wsA)
	code, _, stderr = runPf(t, env, "recall", "--project", "aproj", "the query")
	if code != 0 {
		t.Fatalf("cwd-routed recall: code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runPf(t, env, "remember", "--project", "aproj", "--type", "fact.note", "w17 body")
	if code != 0 {
		t.Fatalf("cwd-routed remember: code=%d stderr=%s", code, stderr)
	}

	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 4 {
		t.Fatalf("recall/remember: default hub got %d, alpha hub got %d (want 0/4)", nDef, nAlpha)
	}
}

// TestW17DefaultOnlyProjectRefusedOnTheRoutedCell (AC3): routed to the
// named Cell, a project mapped only in the default Cell's file is refused
// locally — nothing is sent — and the error's "mapped here" lists the
// routed Cell's projects and names the routed Cell's file.
func TestW17DefaultOnlyProjectRefusedOnTheRoutedCell(t *testing.T) {
	env, _, _, def, alpha := w17DualCellFixture(t)
	cases := [][]string{
		{"file", "create", "--project", "dproj", "--goal", "w17 refused"},
		{"recall", "--project", "dproj", "the query"},
		{"remember", "--project", "dproj", "--type", "fact.note", "w17 body"},
	}
	for _, args := range cases {
		code, _, stderr := runPf(t, env, append([]string{"--profile", "alpha"}, args...)...)
		if code != 1 {
			t.Fatalf("%v: exit %d, want the local refusal (stderr=%s)", args[:2], code, stderr)
		}
		for _, tok := range []string{`project "dproj" is not mapped`, "mapped here: aproj", "cli-alpha.toml"} {
			if !strings.Contains(stderr, tok) {
				t.Fatalf("%v: stderr misses %q: %s", args[:2], tok, stderr)
			}
		}
		if strings.Contains(stderr, "mapped here: dproj") {
			t.Fatalf("%v: mapped here lists the default Cell's projects: %s", args[:2], stderr)
		}
		if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 0 {
			t.Fatalf("%v: refused call still sent %d/%d request(s)", args[:2], nDef, nAlpha)
		}
	}
}

// TestW17UnknownNamedCellRefusedBeforeTheWire: a --profile naming no Cell
// file is a loud local error naming the missing file — placement never
// validates against a mapping it could not read, and nothing is sent.
func TestW17UnknownNamedCellRefusedBeforeTheWire(t *testing.T) {
	env, _, _, def, alpha := w17DualCellFixture(t)
	code, _, stderr := runPf(t, env, "--profile", "ghost", "file", "create",
		"--project", "aproj", "--goal", "w17 ghost")
	if code != 1 {
		t.Fatalf("ghost profile: exit %d, want the local refusal (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "cli-ghost.toml") {
		t.Fatalf("ghost profile: stderr must name the missing Cell file: %s", stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 0 {
		t.Fatalf("ghost profile: %d/%d request(s) still sent", nDef, nAlpha)
	}
}

// TestW17AcceptanceCarrierAdvisoryOnTheNamedCell: the acceptance-carrier
// advisory resolves the create's project against the routed Cell's
// mapping too — a named-Cell create keeps its step-table hint.
func TestW17AcceptanceCarrierAdvisoryOnTheNamedCell(t *testing.T) {
	env, _, _, def, alpha := w17DualCellFixture(t)
	code, _, stderr := runPf(t, env, "--profile", "alpha", "file", "create",
		"--project", "aproj", "--goal", "w17 advisory", "--bundle", "develop",
		"--content", "## Acceptance\n- the new behavior passes\n")
	if code != 0 {
		t.Fatalf("named-Cell advisory create: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, carrierHint) {
		t.Fatalf("named-Cell create lost the step-table advisory: %q", stderr)
	}
	if nDef, nAlpha := w17Counts(def, alpha); nDef != 0 || nAlpha != 1 {
		t.Fatalf("advisory create: default hub got %d, alpha hub got %d (want 0/1)", nDef, nAlpha)
	}
}
