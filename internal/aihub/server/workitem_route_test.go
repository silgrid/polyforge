package server

// workitem_route_test.go — the authorization regression
// that covers EVERY work-item-addressed route automatically. The route
// list is derived from the literal m.HandleFunc patterns registered in
// routes() (server.go is parsed, not a hand list), so a newly added
// work-item route without the access gate fails this test. For one
// reference the suite first captures what every route answers while no
// work item resolves to it, then creates a work item that resolves to
// that same reference and requires byte-identical answers from a caller
// without access.
//
// The file is self-contained against the pre-change tree, so the
// external oracle can run it red on the base and green on the branch.

import (
	"crypto/ed25519"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// w74rRoute is one literal work-item-addressed route registration.
type w74rRoute struct {
	method  string
	pattern string
	signed  bool
}

// w74rHandleFuncRe matches the literal registrations inside routes():
// method, path pattern and the auth wrapper (auth, or signedMachine with
// its replay lane — A2).
var w74rHandleFuncRe = regexp.MustCompile(
	`m\.HandleFunc\("([A-Z]+) (/api/v1/work-items/\{id\}[^"]*)", s\.(auth\(|signedMachine\(lane\w+, )s\.h\w+\)\)`)

// w74rParseRoutes answers every work-item-addressed route a routes()
// source registers, in registration order.
func w74rParseRoutes(source string) []w74rRoute {
	var out []w74rRoute
	for _, m := range w74rHandleFuncRe.FindAllStringSubmatch(source, -1) {
		out = append(out, w74rRoute{method: m[1], pattern: m[2], signed: strings.HasPrefix(m[3], "signedMachine")})
	}
	return out
}

// w74rServerRoutes reads the live routes() source of server.go.
func w74rServerRoutes(t *testing.T) []w74rRoute {
	t.Helper()
	raw, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	s := string(raw)
	const header = "func (s *Server) routes() {"
	i := strings.Index(s, header)
	if i < 0 {
		t.Fatalf("routes() not found in server.go")
	}
	body := s[i:]
	j := strings.Index(body, "\n}\n")
	if j < 0 {
		t.Fatalf("routes() block end not found in server.go")
	}
	return w74rParseRoutes(body[:j])
}

// w74rPath instantiates a route pattern for one reference.
func w74rPath(r w74rRoute, ref string) string {
	p := strings.ReplaceAll(r.pattern, "{id}", ref)
	p = strings.ReplaceAll(p, "{blocking}", "wi_w74r_blocker")
	return strings.ReplaceAll(p, "{kind}", "blocks")
}

// TestWorkItemRouteEnumeratorFindsNewRoute: the enumerator derives its
// list from the literal HandleFunc patterns, so a newly added literal
// work-item route is discovered without editing any hand list — and a
// non-work-item registration stays out.
func TestWorkItemRouteEnumeratorFindsNewRoute(t *testing.T) {
	src := `func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/work-items/{id}", s.auth(s.hGetWI))
	m.HandleFunc("POST /api/v1/work-items/{id}/future-door", s.auth(s.hFutureDoor))
	m.HandleFunc("POST /api/v1/work-items/{id}/progress-query", s.signedMachine(laneQuery, s.hProgressQuery))
	m.HandleFunc("GET /api/v1/projects/{id}", s.auth(s.hGetProject))
}`
	routes := w74rParseRoutes(src)
	if len(routes) != 3 {
		t.Fatalf("enumerator found %d routes, want 3: %+v", len(routes), routes)
	}
	future := false
	for _, r := range routes {
		if !strings.HasPrefix(r.pattern, "/api/v1/work-items/{id}") {
			t.Fatalf("non-work-item route enumerated: %+v", r)
		}
		if r.pattern == "/api/v1/work-items/{id}/future-door" && r.method == "POST" {
			future = true
		}
	}
	if !future {
		t.Fatalf("a newly added literal work-item route was not discovered: %+v", routes)
	}
	if !routes[2].signed {
		t.Fatalf("the signed wrapper was not recognized: %+v", routes[2])
	}
}

// TestWorkItemRouteCoverageConceals: for one reference, every enumerated
// work-item-addressed route answers a caller without access exactly as
// it answers while no work item resolves to that reference — same
// status, same code, byte-identical body.
func TestWorkItemRouteCoverageConceals(t *testing.T) {
	e := newSrvEnv(t)
	routes := w74rServerRoutes(t)
	if len(routes) < 15 {
		t.Fatalf("the enumerator found only %d work-item routes — the parse is broken: %+v", len(routes), routes)
	}
	// The registered set must contain the known doors; a future route
	// joins them automatically through the same enumeration.
	for _, want := range []string{
		"/api/v1/work-items/{id}", "/api/v1/work-items/{id}/cancel",
		"/api/v1/work-items/{id}/wrap", "/api/v1/work-items/{id}/claim",
		"/api/v1/work-items/{id}/session-verify", "/api/v1/work-items/{id}/session-attach",
		"/api/v1/work-items/{id}/session-end", "/api/v1/work-items/{id}/force-end-session",
		"/api/v1/work-items/{id}/resume-blocked", "/api/v1/work-items/{id}/dependencies",
		"/api/v1/work-items/{id}/dependencies/{blocking}/{kind}",
		"/api/v1/work-items/{id}/progress-query", "/api/v1/work-items/{id}/artifact-query",
	} {
		found := false
		for _, r := range routes {
			if r.pattern == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("route %s missing from the enumerated set", want)
		}
	}

	// The reference: the PathName form "<project>-<seq>" of a work item
	// that does not exist yet, so the very same reference is first
	// missing and then resolves.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w74cov"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	ref := "w74cov-1"
	// A caller without access: no membership anywhere, plus two of its
	// own registered machines for the signed relay routes (the missing
	// and the existing capture of one route must not share a signature,
	// or the replay cache refuses the second as a replay).
	outID, outKey := memberUser(t, e, "w74r-nonmember", "")
	privA, _ := signedMachineFor(t, e, "m-w74r-a", outID)
	privB, _ := signedMachineFor(t, e, "m-w74r-b", outID)
	sid := strings.Repeat("5a", 16)

	// The body each route gets. A GET/DELETE never decodes it; the POST
	// bodies are the shapes the gates never reach.
	bodyOf := func(r w74rRoute) any {
		switch {
		case r.pattern == "/api/v1/work-items/{id}" && r.method == "PATCH":
			return map[string]any{"goal": "x"}
		case strings.HasSuffix(r.pattern, "/claim"):
			return map[string]any{"session_id": sid, "machine_id": ""}
		case strings.HasSuffix(r.pattern, "/session-verify"):
			return map[string]any{"session_id": sid}
		case strings.HasSuffix(r.pattern, "/session-attach"):
			return map[string]any{"session_id": sid, "machine_id": ""}
		case strings.HasSuffix(r.pattern, "/session-end"):
			return map[string]any{"session_id": sid, "status": "paused"}
		case strings.HasSuffix(r.pattern, "/force-end-session"):
			return map[string]any{"reason": "probe"}
		case strings.HasSuffix(r.pattern, "/dependencies") && r.method == "POST":
			return map[string]any{"blocking_wi_id": "wi_w74r_blocker", "kind": "blocks"}
		default:
			return map[string]any{}
		}
	}
	request := func(r w74rRoute, priv ed25519.PrivateKey, machine string) (int, []byte) {
		path := w74rPath(r, ref)
		if r.signed {
			return signedPost(t, e, outKey, machine, priv, path, bodyOf(r))
		}
		return e.do(t, r.method, path, outKey, bodyOf(r), nil)
	}

	// Phase 1: no work item resolves to the reference yet.
	missing := make(map[string][]byte)
	for _, r := range routes {
		code, body := request(r, privA, "m-w74r-a")
		if code != 404 || !strings.Contains(string(body), api.ErrCoreNotFound) {
			t.Fatalf("%s %s (missing): %d %s, want 404 %s", r.method, r.pattern, code, body, api.ErrCoreNotFound)
		}
		missing[r.method+" "+r.pattern] = body
	}

	// Phase 2: a work item now resolves to the same reference, in a
	// project the caller cannot read.
	var created struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "w74cov", "goal": "w74r coverage target", "bundle_name": "demo-bundle"}, &created); code != 201 || created.ID == "" {
		t.Fatalf("create coverage target: %d %s", code, raw)
	}
	for _, r := range routes {
		code, body := request(r, privB, "m-w74r-b")
		want := missing[r.method+" "+r.pattern]
		if code != 404 {
			t.Fatalf("%s %s (existing): %d %s, want the missing-reference 404", r.method, r.pattern, code, body)
		}
		if string(body) != string(want) {
			t.Fatalf("%s %s: existing answered %s; missing answered %s (must be byte-identical)", r.method, r.pattern, body, want)
		}
	}

	// The probes wrote nothing.
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+created.ID, e.key, nil, &wi); code != 200 || wi.Status != api.StatusQueued {
		t.Fatalf("coverage probes changed the work item: %d %s %+v", code, raw, wi)
	}
}
