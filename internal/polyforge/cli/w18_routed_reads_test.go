package cli

// carries: internal/polyforge/cli/*, internal/polyforge/mapping/*

// w18_routed_reads_test.go (polyforge#18): every read that resolves a
// workspace through the [workspace] mapping reads the mapping of the Cell
// the invocation routes to (profile.go), not the Host file's — the
// workspace faces `pf workflows --cwd` (listing and validate), the merged
// `pf queue`, the submit acceptance check, `pf msg contacts` and the
// `pf init` memory_policy write. The fixture is the oracle's dual-Cell
// shape: a Host file mapping wsD (the default Cell), cli-alpha.toml
// mapping wsA, cli-beta.toml mapping nothing, and a fake Host socket
// answering GET /profiles with the dual-Cell effective index, GET /status
// the lane-set facts and the data-plane proxy ANY /aihub/<path> to the
// AIHub of the X-PF-Profile Cell. Every case drives the real Run entry
// and is judged on the exit code, the output document and the hub each
// request reached.

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

// w18Hub is one Cell's fake AIHub: its recorder counts what reached it.
type w18Hub struct {
	rec *reqRecorder
	srv *httptest.Server
}

// w18FakeHub answers the read verbs' wire shapes this file pins: the
// filtered work-item list (one queued execution ticket of the requested
// project, bundle devuser, content without an acceptance section), the
// detail read, and one project's member face.
func w18FakeHub(t *testing.T) *w18Hub {
	t.Helper()
	h := &w18Hub{rec: &reqRecorder{}}
	h.srv = newFakeAihub(t, h.rec, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/v1/work-items") && r.Method == http.MethodGet && path == "/api/v1/work-items":
			project := r.URL.Query().Get("project")
			item := map[string]any{
				"id": "wi_" + project, "slug": project + "-1", "project_name": project,
				"status": "queued", "bundle_name": "devuser", "goal": "w18 " + project,
				"content": "no acceptance here", "created_at": "2026-11-14T00:00:00Z",
			}
			_, _ = io.WriteString(w, jsonBody(map[string]any{"items": []any{item}, "total": 1}))
		case strings.HasPrefix(path, "/api/v1/work-items/"):
			id := strings.TrimPrefix(path, "/api/v1/work-items/")
			item := map[string]any{
				"id": id, "slug": "aproj-1", "project_name": "aproj",
				"status": "queued", "bundle_name": "devuser", "goal": "w18 submit",
				"content": "no acceptance here", "created_at": "2026-11-14T00:00:00Z",
			}
			_, _ = io.WriteString(w, jsonBody(item))
		case strings.HasPrefix(path, "/api/v1/projects/") && strings.HasSuffix(path, "/members"):
			project := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/projects/"), "/members")
			_, _ = io.WriteString(w, jsonBody(map[string]any{
				"contacts": []any{map[string]any{"id": "u-" + project, "display_name": "Member of " + project}},
			}))
		default:
			http.NotFound(w, r)
		}
	})
	return h
}

// w18CellHost is the fake Host on a unix socket: GET /profiles answers
// the dual-Cell effective workspace index, GET /status the lane-set facts
// (a workflows dir whose builtin develop lane requires acceptance), POST
// /jobs/<id>/claim the accepted claim, and the data-plane proxy ANY
// /aihub/<path> forwards to the AIHub of the X-PF-Profile Cell ("", the
// unknown and "default" → the default hub) — the profile header is
// consumed, exactly like the real Host's proxy.
func w18CellHost(t *testing.T, def, alpha *w18Hub, wsD, wsA string) string {
	t.Helper()
	wf := t.TempDir()
	writeLane(t, wf, "develop", "steps:\n  - { id: build }\n  - { id: review }\n")
	proxies := map[string]*httputil.ReverseProxy{}
	for profile, hub := range map[string]*w18Hub{"default": def, "alpha": alpha} {
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
		_, _ = io.WriteString(w, `{"machine_id":"m-w18","workflows_dir":`+jsonBody(wf)+`,"jobs":[],"queue":{}}`)
	})
	mux.HandleFunc("POST /jobs/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, jsonBody(map[string]any{"accepted": true}))
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
	sock := filepath.Join(shortTempDir(t), "w18.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// w18Fixture builds the dual-Cell shape: a Host file mapping wsD to
// "dproj" (the default Cell), a named Cell file cli-alpha.toml mapping
// wsA to "aproj", an empty-mapping Cell file cli-beta.toml, and the fake
// Host socket. Each mapped workspace carries a minimal .polyforge.yaml so
// the workflows face's effective-chain read settles.
type w18Fixture struct {
	env        map[string]string
	wsD, wsA   string
	def, alpha *w18Hub
}

func newW18Fixture(t *testing.T) *w18Fixture {
	t.Helper()
	def, alpha := w18FakeHub(t), w18FakeHub(t)
	wsD, wsA := t.TempDir(), t.TempDir()
	for _, ws := range []string{wsD, wsA} {
		if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte("steps: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	host := "server = " + jsonBody(def.srv.URL) + "\napi_key = \"kd\"\nuser_id = \"u-d\"\n\n" +
		"[workspace.\"" + wsD + "\"]\nprojects = [\"dproj\"]\n"
	alphaFile := "server = " + jsonBody(alpha.srv.URL) + "\napi_key = \"ka\"\nuser_id = \"u-a\"\n\n" +
		"[workspace.\"" + wsA + "\"]\nprojects = [\"aproj\"]\n"
	betaFile := "server = " + jsonBody(def.srv.URL) + "\napi_key = \"kb\"\nuser_id = \"u-b\"\n"
	for name, text := range map[string]string{"cli.toml": host, "cli-alpha.toml": alphaFile, "cli-beta.toml": betaFile} {
		if err := os.WriteFile(filepath.Join(xdg, "polyforge", name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sock := w18CellHost(t, def, alpha, wsD, wsA)
	return &w18Fixture{
		env: map[string]string{"XDG_CONFIG_HOME": xdg, "PF_DAEMON_SOCKET": sock, "PF_PROFILE": ""},
		wsD: wsD, wsA: wsA,
		def: def, alpha: alpha,
	}
}

// w18Counts answers the two Cell hubs' request counts (default, alpha).
func w18Counts(def, alpha *w18Hub) (int, int) {
	return len(def.rec.all()), len(alpha.rec.all())
}

// TestW18WorkflowsListsTheRoutedCellsMapping (AC1): `pf workflows --cwd`
// resolves the workspace against the routed Cell's mapping — --profile
// alpha answers wsA/aproj where the Host file maps nothing; a workspace
// the routed Cell does not map answers nothing even though the Host file
// maps it; the default Cell's behavior is unchanged, and the read still
// degrades (never fails) with the daemon down.
func TestW18WorkflowsListsTheRoutedCellsMapping(t *testing.T) {
	f := newW18Fixture(t)

	// The named Cell's own workspace, named by --profile.
	code, stdout, stderr := runPf(t, f.env, "--profile", "alpha", "--json", "workflows", "--cwd", f.wsA)
	if code != 0 {
		t.Fatalf("--profile alpha workflows: code=%d stderr=%s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	if doc.Workspace == nil || *doc.Workspace != f.wsA {
		t.Fatalf("--profile alpha --cwd wsA: workspace %v, want wsA (the routed Cell's mapping)", doc.Workspace)
	}
	if len(doc.Projects) != 1 || doc.Projects[0] != "aproj" {
		t.Fatalf("--profile alpha --cwd wsA: projects %v, want [aproj]", doc.Projects)
	}

	// The same workspace, routed by the cwd match alone.
	t.Chdir(f.wsA)
	code, stdout, stderr = runPf(t, f.env, "--json", "workflows", "--cwd", f.wsA)
	if code != 0 {
		t.Fatalf("cwd-routed workflows: code=%d stderr=%s", code, stderr)
	}
	doc = decodeWorkflows(t, stdout)
	if doc.Workspace == nil || *doc.Workspace != f.wsA || len(doc.Projects) != 1 || doc.Projects[0] != "aproj" {
		t.Fatalf("cwd-routed --cwd wsA: workspace %v projects %v, want wsA [aproj]", doc.Workspace, doc.Projects)
	}

	// A workspace the routed Cell does not map answers nothing, even
	// though the Host file (the default Cell) maps it.
	code, stdout, stderr = runPf(t, f.env, "--profile", "alpha", "--json", "workflows", "--cwd", f.wsD)
	if code != 0 {
		t.Fatalf("--profile alpha --cwd wsD: code=%d stderr=%s", code, stderr)
	}
	doc = decodeWorkflows(t, stdout)
	if doc.Workspace != nil || len(doc.Projects) != 0 {
		t.Fatalf("--profile alpha --cwd wsD: workspace %v projects %v, want none (alpha does not map wsD)", doc.Workspace, doc.Projects)
	}

	// The default Cell keeps its shape: the Host file's mapping answers.
	code, stdout, stderr = runPf(t, f.env, "--profile", "default", "--json", "workflows", "--cwd", f.wsD)
	if code != 0 {
		t.Fatalf("--profile default workflows: code=%d stderr=%s", code, stderr)
	}
	doc = decodeWorkflows(t, stdout)
	if doc.Workspace == nil || *doc.Workspace != f.wsD || len(doc.Projects) != 1 || doc.Projects[0] != "dproj" {
		t.Fatalf("--profile default --cwd wsD: workspace %v projects %v, want wsD [dproj]", doc.Workspace, doc.Projects)
	}

	// The unreachable daemon stays a degradation, never a failure: with
	// no Host answering, the resolver falls back to the disk index and
	// the default Cell's mapping still answers the --cwd facts.
	down := map[string]string{"XDG_CONFIG_HOME": f.env["XDG_CONFIG_HOME"], "PF_DAEMON_SOCKET": filepath.Join(t.TempDir(), "none.sock")}
	code, stdout, stderr = runPf(t, down, "--json", "workflows", "--cwd", f.wsD)
	if code != 0 {
		t.Fatalf("daemon-down workflows: code=%d stderr=%s", code, stderr)
	}
	doc = decodeWorkflows(t, stdout)
	if doc.DaemonReachable || doc.Reason == "" {
		t.Fatalf("daemon-down doc must carry the degradation: %+v", doc)
	}
	if doc.Workspace == nil || *doc.Workspace != f.wsD || len(doc.Projects) != 1 || doc.Projects[0] != "dproj" {
		t.Fatalf("daemon-down --cwd wsD: workspace %v projects %v, want wsD [dproj]", doc.Workspace, doc.Projects)
	}
}
