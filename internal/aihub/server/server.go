// Package server is the AIHub HTTP face: the business endpoints, the
// admin face, the session endpoints (claim/session-verify/
// session-attach/force-end-session/session-end), and the /git/{wi}.git/...
// proxy mount. Every error is the typed common/api errStatus envelope;
// auth is the Bearer API key.
package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/aihub/memory"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
	"github.com/silgrid/polyforge/webui"
)

// Server wires the domain managers behind one mux.
type Server struct {
	DB       *core.DB
	WIs      *core.WIManager
	Owners   *core.OwnerManager
	Sessions *core.SessionManager
	Admin    *core.AdminManager
	Mems     *memory.Store
	Git      *git.Manager
	DataDir  string

	// StaleAfter is the machines liveness threshold used by the machines
	// read face: a machine whose last_heartbeat_at is older than this is
	// reported stale. Zero keeps the default (5× the 60s heartbeat
	// default = 5m); cmd/aihub wires it to -machines-stale-after.
	StaleAfter time.Duration

	// pending is the in-memory progress/artifact relay registry, relay its
	// enqueue-time admission budget ( A2) and replay the
	// signed-request anti-replay cache (; since
	// 512-live-entry buckets, 600 s TTL, no global eviction; since
	// one bucket per machine id per signed-route lane).
	// None of them persists anything.
	pending *queryRegistry
	relay   *relayBudget
	replay  *machinesign.ReplayCache

	// webDist is the embedded console tree (webui/dist), resolved once at
	// construction: the serving paths read it without a lazy write, so
	// two first requests cannot race on it.
	webDist distFS

	mux *http.ServeMux
}

// New assembles the server.
func New(db *core.DB, dataDir string, gitMgr *git.Manager) *Server {
	s := &Server{
		DB:       db,
		WIs:      core.NewWIManager(db),
		Owners:   core.NewOwnerManager(db),
		Sessions: core.NewSessionManager(db),
		Admin:    core.NewAdminManager(db),
		Mems:     memory.NewStore(db, dataDir),
		Git:      gitMgr,
		DataDir:  dataDir,
		pending:  newQueryRegistry(),
		relay:    newRelayBudget(relayHolderQuota, relayMemberQuota, relayWindow),
		replay:   machinesign.NewReplayCache(),
	}
	// The embedded console tree resolves once, here: an unbuilt dist (a
	// clean checkout before webui-build) leaves it nil and the /web/
	// routes answer the static 404, as before.
	if dist, err := webui.Dist(); err == nil {
		s.webDist = dist
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := http.NewServeMux()

	// Business face (reference.md §4.1; git replaces the chain).
	m.HandleFunc("POST /api/v1/work-items", s.auth(s.hCreateWI))
	m.HandleFunc("GET /api/v1/work-items", s.auth(s.hListWIs))
	m.HandleFunc("GET /api/v1/work-items/{id}", s.auth(s.hGetWI))
	m.HandleFunc("PATCH /api/v1/work-items/{id}", s.auth(s.hPatchWI))
	m.HandleFunc("POST /api/v1/work-items/{id}/cancel", s.auth(s.hCancelWI))
	// The terminal verdict on a work item with no bundle (no daemon ever
	// holds one); an execution ticket is wrapped through its session end.
	m.HandleFunc("POST /api/v1/work-items/{id}/wrap", s.auth(s.hWrapWI))
	m.HandleFunc("POST /api/v1/work-items/{id}/claim", s.auth(s.hSessionClaim))
	m.HandleFunc("POST /api/v1/work-items/{id}/session-verify", s.auth(s.hSessionVerify))
	m.HandleFunc("POST /api/v1/work-items/{id}/session-attach", s.auth(s.hSessionAttach))
	m.HandleFunc("POST /api/v1/work-items/{id}/session-end", s.auth(s.hSessionEnd))
	// Recovery door: an admin/maintainer force-ends a stuck holder's session
	// (WI -> queued); any daemon then claims it normally.
	m.HandleFunc("POST /api/v1/work-items/{id}/force-end-session", s.auth(s.hForceEndSession))
	// polyforge#116: the §6.4 owner resume door on a workflow parked
	// blocked — resume (the owner's re-dispatch-in-place verdict, claimed
	// back immediately). The abandon exit is cancel.
	m.HandleFunc("POST /api/v1/work-items/{id}/resume-blocked", s.auth(s.hResumeBlockedWI))
	m.HandleFunc("POST /api/v1/work-items/{id}/dependencies", s.auth(s.hAddDep))
	m.HandleFunc("GET /api/v1/work-items/{id}/dependencies", s.auth(s.hDeps))
	m.HandleFunc("DELETE /api/v1/work-items/{id}/dependencies/{blocking}/{kind}", s.auth(s.hDelDep))
	m.HandleFunc("GET /api/v1/projects/{project}/ready-queue", s.auth(s.hReadyQueue))
	m.HandleFunc("POST /api/v1/machines/heartbeat", s.auth(s.hHeartbeat))
	m.HandleFunc("GET /api/v1/machines", s.authAdmin(s.hListMachines))
	m.HandleFunc("POST /api/v1/machines/{id}/rekey", s.authAdmin(s.hRekeyMachine))
	// cross-machine face: signed, timestamped, replay-protected
	// (see machineface.go), each route recording its signatures in its own
	// replay lane ( A2). Reachability: a step agent cannot
	// reach any of them — step processes hold no aihub key at all.
	m.HandleFunc("GET /api/v1/machines/inbox", s.signedMachine(laneInbox, s.hMachineInbox))
	m.HandleFunc("POST /api/v1/queries/{qid}/answer", s.signedMachine(laneAnswer, s.hQueryAnswer))
	m.HandleFunc("POST /api/v1/work-items/{id}/progress-query", s.signedMachine(laneQuery, s.hProgressQuery))
	m.HandleFunc("POST /api/v1/work-items/{id}/artifact-query", s.signedMachine(laneQuery, s.hArtifactQuery))
	// messaging: Bearer-auth user-to-user conversations with
	// permanent history. Reachability: a step agent cannot reach them
	// (`pf msg` is refused inside a step; step processes hold no key).
	m.HandleFunc("POST /api/v1/messages", s.auth(s.hSendMessage))
	m.HandleFunc("POST /api/v1/messages/ack", s.auth(s.hAckMessage))
	m.HandleFunc("POST /api/v1/messages/delivered", s.auth(s.hDeliveredMessage))
	m.HandleFunc("GET /api/v1/messages/{id}", s.auth(s.hGetMessage))
	m.HandleFunc("GET /api/v1/conversations", s.auth(s.hListConversations))
	m.HandleFunc("GET /api/v1/conversations/{id}/messages", s.auth(s.hListMessages))

	// Memory face.
	m.HandleFunc("POST /api/v1/memories", s.auth(s.hCreateMemory))
	m.HandleFunc("GET /api/v1/memories/search", s.auth(s.hSearchMemory))
	m.HandleFunc("GET /api/v1/memories/{id}", s.auth(s.hGetMemory))
	m.HandleFunc("POST /api/v1/memories/{id}/redact", s.auth(s.hRedactMemory))

	// Admin face.
	m.HandleFunc("POST /api/v1/users", s.authAdmin(s.hCreateUser))
	m.HandleFunc("GET /api/v1/users", s.authAdmin(s.hListUsers))
	m.HandleFunc("GET /api/v1/users/{id}", s.authAdmin(s.hGetUser))
	m.HandleFunc("PATCH /api/v1/users/{id}", s.authAdmin(s.hPatchUser))
	m.HandleFunc("POST /api/v1/api-keys", s.authAdmin(s.hCreateAPIKey))
	m.HandleFunc("GET /api/v1/api-keys", s.authAdmin(s.hListAPIKeys))
	m.HandleFunc("DELETE /api/v1/api-keys/{id}", s.authAdmin(s.hRevokeAPIKey))
	m.HandleFunc("POST /api/v1/projects", s.authAdmin(s.hCreateProject))
	m.HandleFunc("GET /api/v1/projects", s.authAdmin(s.hListProjects))
	m.HandleFunc("GET /api/v1/projects/{id}", s.auth(s.hGetProject))
	m.HandleFunc("PATCH /api/v1/projects/{id}", s.auth(s.hPatchProject))
	// The project member face (read-only): the caller's human
	// co-members as id + display name; contacts.go states its gate and
	// step-agent reachability.
	m.HandleFunc("GET /api/v1/projects/{id}/members", s.auth(s.hProjectMemberContacts))
	// The project hard delete: admin role (authAdmin)
	// plus the same api-key project-scope check as PATCH, inside the
	// handler. Reachability: a step agent cannot reach this route — the
	// daemon's aihub face is an explicit allowlist that forwards nothing
	// of the admin face, and `pf projects` is refused inside a workflow
	// step (cli/stepctx.go) — so no project deletion is reachable from
	// a capsule.
	m.HandleFunc("DELETE /api/v1/projects/{id}", s.authAdmin(s.hDeleteProject))
	m.HandleFunc("POST /api/v1/admin/backup", s.authAdmin(s.hBackup))

	// Git proxy (own auth: session-scoped Basic credential).
	m.Handle("/git/", http.HandlerFunc(s.Git.HandleGit))

	// The read-only web console is served from the RAW
	// path in ServeHTTP — BEFORE this mux — so no path-cleanup 301 can
	// escape its header set and the face emits no redirects. /api/v1
	// keeps Bearer-only authentication. The HTML face was
	// deleted with the console, never aliased or redirected.

	s.mux = m
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The console face is intercepted from the RAW path before
	// http.ServeMux can canonicalise it: ServeMux answers a cleaned
	// 301 for /web//x, /web/./x and trailing-slash shapes WITHOUT the
	// header set (the viewer's N5 defect), so the wrapper owns every
	// /web response, including encoded separators, and the face emits
	// no redirects at all.
	if p := r.URL.Path; p == "/web" || strings.HasPrefix(p, "/web/") {
		s.web(w, r)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// ---------------------------------------------------------------------------
// Errors and JSON

// writeErr renders the typed envelope from any error shape.
func writeErr(w http.ResponseWriter, err error) {
	switch e := err.(type) {
	case *api.Error:
		writeJSON(w, e.HTTPStatus(), e)
	case *memory.APIError:
		ae := &api.Error{Code: e.Code, Detail: e.Detail, Extra: reasonExtra(e.Reason)}
		writeJSON(w, ae.HTTPStatus(), ae)
	default:
		writeJSON(w, http.StatusInternalServerError, &api.Error{Code: api.ErrCoreInternal, Detail: err.Error()})
	}
}

func reasonExtra(reason string) json.RawMessage {
	if reason == "" {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"reason": reason})
	return raw
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	if err := dec.Decode(v); err != nil {
		return core.Fail(api.ErrCoreBadInput, "", "invalid JSON body: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Authentication and authorization

// identity is the authenticated caller.
type identity struct {
	UserID   string
	Role     string // admin|writer
	KeyScope *string
}

// authenticate resolves the Bearer API key (hash-only storage).
func (s *Server) authenticate(r *http.Request) (*identity, error) {
	h := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if token == "" {
		return nil, core.Fail(api.ErrAuthUnauthorized, "", "missing api key")
	}
	return s.keyIdentity(token)
}

// keyIdentity resolves one api key token to its caller identity — the
// one key query the Bearer face and the viewer's Basic password both go
// through (hash-only storage; every key kind then meets the same read
// gates, no viewer-specific policy).
func (s *Server) keyIdentity(token string) (*identity, error) {
	var id identity
	var scope sql.NullString
	err := s.DB.QueryRow(`SELECT u.id, u.role, k.project_id
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_hash=?1 AND k.revoked_at IS NULL`, core.Sha256Hex(token)).
		Scan(&id.UserID, &id.Role, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.Fail(api.ErrAuthUnauthorized, "", "invalid api key")
	}
	if err != nil {
		return nil, err
	}
	if scope.Valid {
		id.KeyScope = &scope.String
	}
	return &id, nil
}

// handler is an authenticated endpoint: it writes its answer and returns
// nil, or returns the error its wrapper renders as the typed envelope
// (having written nothing).
type handler func(w http.ResponseWriter, r *http.Request, idn *identity) error

// auth wraps a handler with key authentication.
func (s *Server) auth(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idn, err := s.authenticate(r)
		if err == nil {
			err = next(w, r, idn)
		}
		if err != nil {
			writeErr(w, err)
		}
	}
}

// authAdmin additionally requires the global admin role.
func (s *Server) authAdmin(next handler) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, idn *identity) error {
		if idn.Role != "admin" {
			return core.Fail(api.ErrAuthForbidden, "", "admin role required")
		}
		return next(w, r, idn)
	})
}

// reply writes v as the JSON answer of a handler.
func reply(w http.ResponseWriter, status int, v any) error {
	writeJSON(w, status, v)
	return nil
}

// memberRole returns the caller's project role ("" when not a member).
func (s *Server) memberRole(projectID, userID string) (string, error) {
	var role string
	err := s.DB.QueryRow(`SELECT role FROM project_members WHERE project_id=?1 AND user_id=?2`,
		projectID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// projectIDOf resolves a project name-or-id to its id.
func (s *Server) projectIDOf(nameOrID string) (string, error) {
	var id string
	err := s.DB.QueryRow(`SELECT id FROM projects WHERE name=?1 OR id=?1`, nameOrID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", core.Fail(api.ErrCoreNotFound, "", "project %q", nameOrID)
	}
	return id, err
}

// scopeCheck enforces the api-key project scope (middleware-level).
func scopeCheck(idn *identity, projectID string) error {
	if idn.KeyScope != nil && *idn.KeyScope != projectID {
		return core.Fail(api.ErrAuthForbidden, "", "api key is scoped to another project")
	}
	return nil
}

// canRead: object data access = membership OR global admin.
func (s *Server) canRead(idn *identity, projectID string) (bool, error) {
	if err := scopeCheck(idn, projectID); err != nil {
		return false, err
	}
	if idn.Role == "admin" {
		return true, nil
	}
	role, err := s.memberRole(projectID, idn.UserID)
	return role != "", err
}

// canWrite: writer+ membership OR admin.
func (s *Server) canWrite(idn *identity, projectID string) (bool, error) {
	if err := scopeCheck(idn, projectID); err != nil {
		return false, err
	}
	if idn.Role == "admin" {
		return true, nil
	}
	role, err := s.memberRole(projectID, idn.UserID)
	return role == "writer" || role == "maintainer", err
}

// mustWrite enforces write access to projectID.
func (s *Server) mustWrite(idn *identity, projectID string) error {
	ok, err := s.canWrite(idn, projectID)
	if err == nil && !ok {
		err = core.Fail(api.ErrAuthForbidden, "", "writer role on the project required")
	}
	return err
}

// mustRead enforces read access to projectID, hiding the object behind
// the caller-supplied not-found detail on denial.
func (s *Server) mustRead(idn *identity, projectID, notFound string, args ...any) error {
	ok, err := s.canRead(idn, projectID)
	if err == nil && !ok {
		err = core.Fail(api.ErrCoreNotFound, "", notFound, args...)
	}
	return err
}

// wiGrant is the resolved authorization state of one work-item-addressed
// request: the resolved id, the project, the caller's
// project role, the global-admin and reporter flags, and whether the
// caller's api key is scoped to another project. Its gates answer the
// missing-work-item CORE-404 for a caller without access — a non-member
// with no retained reporter right, or a key scoped away from the
// project — before any body decoding or state change, so the refusal is
// indistinguishable from a genuinely missing reference and structurally
// writes nothing. A member whose role is too low keeps the route's
// existing AUTH-403.
type wiGrant struct {
	id         string // the canonical work-item id
	ref        string // the reference the caller sent (echoed in refusals)
	projectID  string
	role       string // the caller's project role ("" = not a member)
	admin      bool
	reporter   bool
	scopedAway bool
}

// resolveWIGrant resolves the addressed work item and the caller's
// relationship to it. A missing reference answers the same typed
// CORE-404 a concealed caller gets, so both sides of the comparison stay
// byte-identical for the same reference.
func (s *Server) resolveWIGrant(idn *identity, ref string) (*wiGrant, error) {
	id, err := s.WIs.Resolve(ref)
	var ae *api.Error
	if err != nil {
		if errors.As(err, &ae) && ae.Code == api.ErrCoreNotFound {
			return nil, core.Fail(api.ErrCoreNotFound, "", "work item %s", ref)
		}
		return nil, err
	}
	var projectID, reporter string
	if err := s.DB.QueryRow(`SELECT project_id, reporter FROM work_items WHERE id=?1`, id).
		Scan(&projectID, &reporter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, core.Fail(api.ErrCoreNotFound, "", "work item %s", ref)
		}
		return nil, err
	}
	g := &wiGrant{id: id, ref: ref, projectID: projectID,
		admin: idn.Role == "admin", reporter: reporter == idn.UserID,
		scopedAway: idn.KeyScope != nil && *idn.KeyScope != projectID}
	if !g.admin {
		if g.role, err = s.memberRole(projectID, idn.UserID); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// hidden is the missing-work-item answer: exactly what the route answers
// for a reference that does not exist.
func (g *wiGrant) hidden() error {
	return core.Fail(api.ErrCoreNotFound, "", "work item %s", g.ref)
}

// requireRead conceals the work item from a caller without read access
// (a non-member, or a key scoped to another project).
func (g *wiGrant) requireRead() error {
	if g.scopedAway || (!g.admin && g.role == "") {
		return g.hidden()
	}
	return nil
}

// requireWrite conceals the work item from a non-member and keeps the
// route's AUTH-403 for a member whose role is too low.
func (g *wiGrant) requireWrite() error {
	if g.scopedAway {
		return g.hidden()
	}
	if g.admin {
		return nil
	}
	switch g.role {
	case "writer", "maintainer":
		return nil
	case "":
		return g.hidden()
	default:
		return core.Fail(api.ErrAuthForbidden, "", "writer role on the project required")
	}
}

// requireOwnerDecision conceals the work item from a caller with no
// relationship to it. A reporter keeps the retained cancel/wrap/
// resume-blocked right even after losing project membership
//; the decision itself stays the reporter/
// maintainer/admin rule.
func (g *wiGrant) requireOwnerDecision() error {
	if g.scopedAway {
		return g.hidden()
	}
	if g.admin || g.role != "" || g.reporter {
		return nil
	}
	return g.hidden()
}

// ownerDecision is the reporter/maintainer/admin verdict on the human
// decision doors (cancel, wrap, resume-blocked) after concealment.
func (g *wiGrant) ownerDecision() bool {
	return g.admin || g.reporter || g.role == "maintainer"
}

// requireForceEnd is the recovery door's gate: concealment, then the
// existing admin-or-project-maintainer rule (no reporter admission).
func (g *wiGrant) requireForceEnd() error {
	if err := g.requireRead(); err != nil {
		return err
	}
	if g.admin || g.role == "maintainer" {
		return nil
	}
	return core.Fail(api.ErrAuthForbidden, "", "end-session requires admin or project maintainer")
}

// ---------------------------------------------------------------------------
// Work items

func (s *Server) hCreateWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var body struct {
		core.CreateRequest
		// RepoInventoryBase is the removed pre-v2.5.0 second array:
		// refused by name, never silently dropped (absent, null and []
		// mean "no bases").
		RepoInventoryBase json.RawMessage `json:"repo_inventory_base"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if b := strings.TrimSpace(string(body.RepoInventoryBase)); b != "" && b != "null" && b != "[]" {
		return core.Fail(api.ErrCoreBadInput, "INVENTORY_BASE_UNSUPPORTED",
			"repo_inventory_base is not supported; omit it (the work machine clones each repo's default branch)")
	}
	req := body.CreateRequest
	req.Reporter = idn.UserID
	projectID, err := s.projectIDOf(req.Project)
	if err != nil {
		return err
	}
	if err := s.mustWrite(idn, projectID); err != nil {
		return err
	}
	for _, ref := range req.After {
		if _, err := s.visibleWI(idn, ref); err != nil {
			return err
		}
	}
	wi, err := s.WIs.Create(req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, wi)
}

// visibleWI resolves a dependency endpoint (id or slug) the caller names.
// A work item in a project the caller cannot read answers exactly like
// an unknown one (CORE-404 "work item <ref>"), so dependency writes are
// no existence oracle across projects.
func (s *Server) visibleWI(idn *identity, ref string) (string, error) {
	notFound := core.Fail(api.ErrCoreNotFound, "", "work item %s", ref)
	id, err := s.WIs.Resolve(ref)
	var ae *api.Error
	if err != nil {
		if errors.As(err, &ae) && ae.Code == api.ErrCoreNotFound {
			return "", notFound
		}
		return "", err
	}
	var projectID string
	if err := s.DB.QueryRow(`SELECT project_id FROM work_items WHERE id=?1`, id).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", notFound
		}
		return "", err
	}
	ok, err := s.canRead(idn, projectID)
	if err != nil && !errors.As(err, &ae) {
		return "", err // a store failure stays a 5xx
	}
	if err != nil || !ok {
		return "", notFound
	}
	return id, nil
}

// replyWI answers the work item's current view: the WorkItem with its
// public session audit block (m4: session_fingerprint / session_machine /
// session_started_at / clean_pause; NEVER session_hash, never the secret).
func (s *Server) replyWI(w http.ResponseWriter, id string) error {
	wi, err := s.WIs.Get(id, core.GetOptions{})
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

func (s *Server) hListWIs(w http.ResponseWriter, r *http.Request, idn *identity) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit")) // 0/absent = core default; clamped there
	// The readable-project set is pushed into the list SQL:
	// the page is filtered BEFORE the limit and no row costs a per-row
	// canRead membership lookup. total stays "the returned count" — the
	// same semantics for admin and non-admin.
	readable, err := s.readableProjects(idn)
	if err != nil {
		return err
	}
	items, err := s.WIs.List(core.ListFilters{
		Project:          q.Get("project"),
		Status:           q.Get("status"),
		Assignee:         q.Get("assignee"),
		Priority:         q.Get("priority"),
		Milestone:        q.Get("milestone"),
		Limit:            limit,
		ReadableProjects: readable,
	})
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, struct {
		Items []core.WIView `json:"items"`
		Total int           `json:"total"`
	}{Items: items, Total: len(items)})
}

func (s *Server) hGetWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireRead(); err != nil {
		return err
	}
	opts := core.GetOptions{}
	for _, inc := range strings.Split(r.URL.Query().Get("include"), ",") {
		switch strings.TrimSpace(inc) {
		case "events":
			opts.IncludeEvents = true
		case "":
		default:
			return core.Fail(api.ErrCoreBadInput, "", "unknown include value %q", inc)
		}
	}
	wi, err := s.WIs.Get(g.id, opts)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

func (s *Server) hPatchWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := decodeBody(r, &fields); err != nil {
		return err
	}
	wi, err := s.WIs.Patch(g.id, fields, idn.UserID)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

func (s *Server) hCancelWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	// Cancel authorization: reporter, project maintainer, or admin
	// (reference.md §2.1); a caller with no relationship to the work item
	// gets the missing-work-item answer.
	if err := g.requireOwnerDecision(); err != nil {
		return err
	}
	if !g.ownerDecision() {
		return core.Fail(api.ErrAuthForbidden, "", "cancel requires reporter, maintainer or admin")
	}
	// Optional body: the owner's verbatim reason and answered question
	// ride the event; dry_run answers this authorization gate alone and
	// writes nothing (the daemon's probe of a reserved cancel answer).
	var req api.CancelRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return core.Fail(api.ErrCoreBadInput, "", "invalid JSON body: %v", err)
	}
	if req.DryRun {
		return reply(w, http.StatusOK, map[string]any{"id": g.id, "dry_run": true, "authorized": true})
	}
	wi, err := s.WIs.Cancel(g.id, idn.UserID, req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

// hWrapWI is the terminal verdict on a work item with no bundle
// (core.Wrap). Same authorization as cancel: reporter, project
// maintainer, or admin. The body is optional ({"fail":bool,"note":"..."}).
func (s *Server) hWrapWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireOwnerDecision(); err != nil {
		return err
	}
	if !g.ownerDecision() {
		return core.Fail(api.ErrAuthForbidden, "", "wrap requires reporter, maintainer or admin")
	}
	var req api.WrapRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return core.Fail(api.ErrCoreBadInput, "", "invalid JSON body: %v", err)
	}
	wi, err := s.WIs.Wrap(g.id, idn.UserID, req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

// ---------------------------------------------------------------------------
// v1.5 session endpoints (reference.md §3). The claim path is the machine
// execution surface (a writer/maintainer member or a global admin — the
// shared work-item grant's requireWrite); force-end-session keeps
// the human decision gate (admin or project maintainer).

// sessionClaimEnvelope is the claim/attach response: the SAME
// shape as api.SessionClaimResponse (which cannot gain fields — Phase 0
// owns it), with the work item served as the m4 audit view so its JSON
// carries the public session block.
type sessionClaimEnvelope struct {
	Claim    api.SessionClaim `json:"claim"`
	WorkItem core.WIView      `json:"work_item"`
}

// ensureBare creates the work item's bare repo (the chain begins here)
// BEFORE the session transaction: a claim or attach that commits always
// finds its chain, so a failure here refuses the call with nothing
// committed.
func (s *Server) ensureBare(id string) error {
	if err := s.Git.EnsureBare(id); err != nil {
		return core.Fail(api.ErrCoreInternal, "", "create bare repo: %v", err)
	}
	return nil
}

// sessionClaimResponse assembles the claim/attach envelope over the
// committed claim half.
func (s *Server) sessionClaimResponse(w http.ResponseWriter, id string, claim *api.SessionClaim, err error) error {
	if err != nil {
		return err
	}
	wi, err := s.WIs.Get(id, core.GetOptions{})
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, sessionClaimEnvelope{Claim: *claim, WorkItem: *wi})
}

// hSessionClaim serves POST /api/v1/work-items/{id}/claim (the v1.5 CAS
// session claim; it replaced the deleted v1 generation-era intent
// handler).
func (s *Server) hSessionClaim(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	var req api.SessionClaimRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	req.WorkItemID = g.id
	// the gate first: a refused claim never creates the bare repo
	if err := s.Sessions.ClaimGate(g.id, req.SessionID); err != nil {
		return err
	}
	if err := s.ensureBare(g.id); err != nil {
		return err
	}
	claim, err := s.Sessions.Claim(g.id, req, idn.UserID)
	return s.sessionClaimResponse(w, g.id, claim, err)
}

// hSessionVerify serves the compare-only holder check (B3: the hub
// stores only the hash; the secret never echoes, not even on a match).
// Read access is required: a caller without access gets
// the missing-work-item answer, so the verify answer is no existence
// oracle and a leaked session secret changes nothing.
func (s *Server) hSessionVerify(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireRead(); err != nil {
		return err
	}
	var req api.SessionVerifyRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	resp, err := s.Sessions.Verify(g.id, req.SessionID)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, resp)
}

// hSessionAttach serves the same-session resume of a paused WI (M3).
// Write access is required, like claim.
func (s *Server) hSessionAttach(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	var req api.SessionAttachRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	req.WorkItemID = g.id
	if err := s.Sessions.AttachGate(g.id, req.SessionID); err != nil {
		return err
	}
	if err := s.ensureBare(g.id); err != nil {
		return err
	}
	claim, err := s.Sessions.Attach(g.id, req, idn.UserID)
	return s.sessionClaimResponse(w, g.id, claim, err)
}

// hForceEndSession serves the admin/maintainer force-end of a held session
// (WI -> queued, session cleared, the old secret fenced). Authorization is
// admin or project maintainer: the human recovery decision.
func (s *Server) hForceEndSession(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireForceEnd(); err != nil {
		return err
	}
	var req api.ForceEndSessionRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	if err := s.Sessions.ForceEnd(g.id, req.Reason, idn.UserID); err != nil {
		return err
	}
	return s.replyWI(w, g.id)
}

// hSessionEnd serves the session-fenced terminal verdict (wrapped|
// failed|blocked) and — the merge amend — the M3 pause transition
// ("paused": the session is KEPT, clean_pause rides the same fenced
// update). Write access is required, like claim; a
// leaked session secret does not lift the membership gate. It fully
// replaces the deleted v1 /end-generation endpoint.
func (s *Server) hSessionEnd(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	var req api.SessionEndRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	if err := s.Sessions.End(g.id, req, idn.UserID); err != nil {
		return err
	}
	return s.replyWI(w, g.id)
}

// hResumeBlockedWI serves the §6.4 owner resume verdict's hub door
// (polyforge#116): the door `pf resume` exercises on a workflow parked
// blocked on an honest incomplete result. Authorization (v2-converge §7):
// the WI's reporter, a project maintainer, or an admin — the same gate as
// cancel. accept_lost_work clears the blocked seal (core.ResumeBlockedWith)
// under the same gate.
func (s *Server) hResumeBlockedWI(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireOwnerDecision(); err != nil {
		return err
	}
	if !g.ownerDecision() {
		return core.Fail(api.ErrAuthForbidden, "", "resume-blocked requires the work item's reporter, a project maintainer or admin")
	}
	// The body is optional: an empty one is a plain resume.
	var req api.ResumeBlockedRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return core.Fail(api.ErrCoreBadInput, "", "invalid JSON body: %v", err)
	}
	wi, err := s.WIs.ResumeBlockedWith(g.id, idn.UserID, req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, wi)
}

// ---------------------------------------------------------------------------
// Dependencies

func (s *Server) hAddDep(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	blocked := g.id
	var req struct {
		BlockingWIID string `json:"blocking_wi_id"`
		Kind         string `json:"kind,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	// The blocking side accepts an id or a slug, like the path (#161).
	blocking, err := s.visibleWI(idn, req.BlockingWIID)
	if err != nil {
		return err
	}
	if err := s.WIs.AddDependency(blocking, blocked, req.Kind); err != nil {
		return err
	}
	return s.replyDeps(w, http.StatusCreated, blocked)
}

func (s *Server) hDeps(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireRead(); err != nil {
		return err
	}
	return s.replyDeps(w, http.StatusOK, g.id)
}

// replyDeps answers the dependency edges of wiID. A query failure is a
// typed 5xx envelope — never a false "no edges" answer (the empty lists
// stay reserved for a genuinely edge-free work item).
func (s *Server) replyDeps(w http.ResponseWriter, status int, wiID string) error {
	deps, err := s.WIs.Dependencies(wiID)
	if err != nil {
		return err
	}
	return reply(w, status, deps)
}

func (s *Server) hDelDep(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	blocked := g.id
	blocking, err := s.visibleWI(idn, r.PathValue("blocking"))
	if err != nil {
		return err
	}
	if err := s.WIs.RemoveDependency(blocking, blocked, r.PathValue("kind")); err != nil {
		return err
	}
	return s.replyDeps(w, http.StatusOK, blocked)
}

// ---------------------------------------------------------------------------
// Ready queue

func (s *Server) hReadyQueue(w http.ResponseWriter, r *http.Request, idn *identity) error {
	project := r.PathValue("project")
	projectID, err := s.projectIDOf(project)
	if err != nil {
		return err
	}
	if err := s.mustRead(idn, projectID, "project %q", project); err != nil {
		return err
	}
	q, err := s.WIs.ReadyQueue(project)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, q)
}

// ---------------------------------------------------------------------------
// Machines

// defaultMachinesStaleAfter is 5× the daemon heartbeat default (60s): a
// machine missing five consecutive heartbeats is stale.
const defaultMachinesStaleAfter = 5 * time.Minute

// authorizeMachineRegistration gates the heartbeat path's two registration
// acts ( R1): a machine_id with no machines row, and the claim
// of a legacy row that has no operator. Any project_members row qualifies —
// viewer, writer or maintainer, no role filter — and so does a global admin
// outside every project. A refresh never calls it: membership gates
// registration only (R5). Reachability: the heartbeat route is reached by a
// daemon holding its operator's API key plus the machine key, and a step
// agent holds no aihub key at all.
func (s *Server) authorizeMachineRegistration(idn *identity) error {
	if idn.Role == "admin" {
		return nil
	}
	var member int
	if err := s.DB.QueryRow(`SELECT 1 FROM project_members WHERE user_id=?1 LIMIT 1`,
		idn.UserID).Scan(&member); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if member == 1 {
		return nil
	}
	return core.Fail(api.ErrAuthForbidden, "", "machine registration requires a project membership or a global admin")
}

// hHeartbeat is the one heartbeat write path. The body is read once (the
// bytes the signature covers are the bytes sent), the caller must be the
// machine's operator, and the server's cryptographic verdict (signature,
// freshness, replay) rides into core as a HeartbeatProof. Reachability: a
// daemon holding its operator's API key plus the machine key reaches it;
// during the transition the operator's key alone refreshes an
// already-registered machine's liveness, and only while that row's stored
// runtime_version still predates v2.20.8 — core's version gate, which a
// signed v2.20.8+ beat ratchets closed. A step agent holds no aihub key
// and reaches neither.
func (s *Server) hHeartbeat(w http.ResponseWriter, r *http.Request, idn *identity) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return core.Fail(api.ErrCoreBadInput, "", "unreadable request body")
	}
	var req core.MachineHeartbeat
	if err := json.Unmarshal(body, &req); err != nil {
		return core.Fail(api.ErrCoreBadInput, "", "invalid JSON body: %v", err)
	}
	// A machine belongs to its operator. Refuse anyone else here, before a
	// signature they may not hold is checked, so the machine_id collision is
	// AUTH-403 whatever the beat looks like (the message says another
	// operator and suggests --machine-id, naming no one —).
	if err := s.Admin.HeartbeatCallerCheck(req.MachineID, idn.UserID); err != nil {
		return err
	}
	proof, err := s.heartbeatProof(r, body, req, idn)
	if err != nil {
		return err
	}
	// The registration gate is evaluated here, by value: inside the
	// core's heartbeat transaction the store's single connection is held,
	// so the gate's membership query could never run. Core enforces the
	// verdict only on the two registration acts; a refresh ignores it
	// ( R5), and the refusal order is unchanged (#74).
	gate := s.authorizeMachineRegistration(idn)
	res, err := s.Admin.HeartbeatAuthorized(req, proof, gate)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, res)
}

// hRekeyMachine is the admin-only public-key rebind (POST
// /api/v1/machines/{id}/rekey). There is no key-history table: the old key
// simply stops verifying.
func (s *Server) hRekeyMachine(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.RekeyRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	if err := s.Admin.Rekey(r.PathValue("id"), req.PublicKey); err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]any{"ok": true})
}

// machinesStaleAfter resolves the configured threshold (0 = default).
func (s *Server) machinesStaleAfter() time.Duration {
	if s.StaleAfter <= 0 {
		return defaultMachinesStaleAfter
	}
	return s.StaleAfter
}

// machineHeartbeat is the API's one staleness definition, shared by
// /api/v1/machines and the console's machines snapshot: the heartbeat
// age in seconds (clamped at 0) and its staleness against the
// threshold. ok is false when the stored timestamp does not parse —
// the caller then carries neither the age nor the stale mark.
func machineHeartbeat(lastHeartbeatAt string, now time.Time, staleAfter time.Duration) (age int64, stale, ok bool) {
	hb, err := time.Parse(time.RFC3339, lastHeartbeatAt)
	if err != nil {
		return 0, false, false
	}
	age = int64(now.Sub(hb).Seconds())
	if age < 0 {
		age = 0
	}
	return age, time.Duration(age)*time.Second > staleAfter, true
}

// hListMachines is the operators' liveness read face (admin): every
// registered machine with its last_heartbeat_at and the stale projection
// computed against StaleAfter. This is the read half the M3 gate metric
// judges against (--m3-heartbeat-max-age reads the same table offline).
func (s *Server) hListMachines(w http.ResponseWriter, r *http.Request, idn *identity) error {
	rows, err := s.Admin.Machines()
	if err != nil {
		return err
	}
	staleAfter := s.machinesStaleAfter()
	now := time.Now()
	out := make([]api.MachineStatus, 0, len(rows))
	for _, m := range rows {
		st := api.MachineStatus{
			ID:              m.ID,
			RuntimeVersion:  m.RuntimeVersion,
			Mode:            m.Mode,
			LiveWIIDs:       m.LiveWIIDs,
			LastHeartbeatAt: m.LastHeartbeatAt,
			PublicKey:       m.PublicKey,
			Launcher:        m.Launcher,
			Role:            m.Role,
			OperatorID:      m.OperatorID,
		}
		if age, stale, ok := machineHeartbeat(m.LastHeartbeatAt, now, staleAfter); ok {
			st.HeartbeatAgeSeconds = &age
			st.Stale = stale
		}
		out = append(out, st)
	}
	return reply(w, http.StatusOK, api.MachinesList{
		Count:             len(out),
		Machines:          out,
		StaleAfterSeconds: int64(staleAfter.Seconds()),
	})
}

// ---------------------------------------------------------------------------
// Readable-project set

// readableProjects answers the caller's readable project-id set in ONE
// query — the single fact the multi-object reads (memory search, the WI
// list) narrow their candidate SQL with, so no unreadable match costs a
// per-candidate membership lookup. nil = no narrowing (a
// global admin with an unscoped key keeps the all-projects semantics);
// a non-nil EMPTY slice = nothing readable anywhere.
func (s *Server) readableProjects(idn *identity) ([]string, error) {
	if idn.Role == "admin" {
		if idn.KeyScope == nil {
			return nil, nil
		}
		// The admin's own membership is irrelevant, but the key's scope
		// still applies: a scoped admin key never reads past its scope.
		return []string{*idn.KeyScope}, nil
	}
	rows, err := s.DB.Query(`SELECT project_id FROM project_members WHERE user_id=?1`, idn.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Non-nil from here on: an empty answer means "nothing readable", not
	// "no narrowing".
	projects := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if idn.KeyScope == nil || p == *idn.KeyScope {
			projects = append(projects, p)
		}
	}
	return projects, rows.Err()
}

// ---------------------------------------------------------------------------
// Memory

// memVisible is the visibility-tier predicate alone (no membership, no
// SQL): the "admin" tier only an admin sees, "private" its owner or an
// admin; every other tier passes — membership is already enforced by
// the readable-project set the search query is narrowed with. The
// single-object read path (memAllowed/memRead) adds membership back.
func (s *Server) memVisible(idn *identity) func(*memory.Entry) bool {
	return func(e *memory.Entry) bool {
		switch e.Visibility {
		case "admin":
			return idn.Role == "admin"
		case "private":
			return e.OwnerUserID == idn.UserID || idn.Role == "admin"
		default: // project | team | public: any project reader
			return true
		}
	}
}

// memAllowed builds the single-object visibility predicate for a
// memory READ by id (memRead): the visibility tiers (memVisible) plus
// the project gate — membership or global admin — for the project tiers.
func (s *Server) memAllowed(idn *identity) func(*memory.Entry) bool {
	visible := s.memVisible(idn)
	return func(e *memory.Entry) bool {
		if e.Visibility != "admin" && e.Visibility != "private" {
			if idn.Role == "admin" {
				return true
			}
			ok, err := s.canRead(idn, e.ProjectID)
			return err == nil && ok
		}
		return visible(e)
	}
}

// memReadAllowed is the coarse project gate of a memory READ. A denied
// gate must answer the same not-found shape as a genuinely missing
// memory — object existence must not leak (reference.md §4.1: a
// non-member's read is 404, only writes are 403) — so membership
// denial and project-scope mismatch both land as MEM-404 carrying the
// memory's own id, indistinguishable from memRead's visibility-tier
// 404 below. The write path keeps MEM-403 (memWriteAllowed); real
// storage errors still propagate.
func (s *Server) memReadAllowed(idn *identity, projectID, memID string) error {
	if idn.Role == "admin" {
		return nil
	}
	if err := scopeCheck(idn, projectID); err != nil {
		return core.Fail(api.ErrMemNotFound, "", "memory %q", memID)
	}
	role, err := s.memberRole(projectID, idn.UserID)
	if err != nil {
		return err
	}
	if role == "" {
		return core.Fail(api.ErrMemNotFound, "", "memory %q", memID)
	}
	return nil
}

func (s *Server) hCreateMemory(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req memory.CreateRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	projectID, err := s.projectIDOf(req.Project)
	if err != nil {
		return err
	}
	if err := s.memWriteAllowed(idn, projectID); err != nil {
		return err
	}
	// The attributing work item accepts every WI spelling the other faces
	// do (id or "<project>#<seq>" slug) and is stored as the canonical
	// id; an unknown or unreadable one is a typed CORE-404, never the
	// foreign-key failure of the insert (polyforge#155). An empty one is
	// no attribution. Unreadable covers a non-member AND a key scoped to
	// another project: both get the missing-work-item answer of the
	// shared work-item grant, never the scope's AUTH-403, so the body
	// field is no existence oracle across projects ( A1).
	// A memory belongs to one project: a readable work item of another
	// project is refused as bad input (checked after the read gate, so an
	// unreadable one stays indistinguishable from an unknown one).
	if req.WorkItemID != nil && *req.WorkItemID == "" {
		req.WorkItemID = nil
	}
	if req.WorkItemID != nil {
		g, err := s.resolveWIGrant(idn, *req.WorkItemID)
		if err != nil {
			return err
		}
		if err := g.requireRead(); err != nil {
			return err
		}
		if g.projectID != projectID {
			return core.Fail(api.ErrCoreBadInput, "",
				"work item %s belongs to another project than %s", *req.WorkItemID, req.Project)
		}
		req.WorkItemID = &g.id
	}
	e, err := s.Mems.Create(req, idn.UserID)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, e)
}

func (s *Server) memWriteAllowed(idn *identity, projectID string) error {
	if err := scopeCheck(idn, projectID); err != nil {
		return err
	}
	if idn.Role == "admin" {
		return nil
	}
	role, err := s.memberRole(projectID, idn.UserID)
	if err != nil {
		return err
	}
	if role != "writer" && role != "maintainer" {
		return core.Fail(api.ErrMemForbidden, "", "writer role on the project required")
	}
	return nil
}

func (s *Server) hGetMemory(w http.ResponseWriter, r *http.Request, idn *identity) error {
	e, content, err := s.memRead(r.PathValue("id"), idn)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("format") == "content" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(memory.Body(content)))
		return nil
	}
	return reply(w, http.StatusOK, e)
}

// memRead loads an entry and enforces read access in one shot.
func (s *Server) memRead(idOrSlug string, idn *identity) (*memory.Entry, []byte, error) {
	memID := idOrSlug
	if !strings.HasPrefix(memID, "mem_") {
		// Memories are addressed by id only; anything else is a 404.
		return nil, nil, core.Fail(api.ErrMemNotFound, "", "memory %q", idOrSlug)
	}
	e, err := s.Mems.Get(memID)
	if err != nil {
		return nil, nil, err
	}
	if err := s.memReadAllowed(idn, e.ProjectID, memID); err != nil {
		return nil, nil, err
	}
	allowed := s.memAllowed(idn)
	if !allowed(e) {
		return nil, nil, core.Fail(api.ErrMemNotFound, "", "memory %q", memID)
	}
	content, err := s.Mems.Content(memID)
	if err != nil {
		return nil, nil, err
	}
	return e, content, nil
}

func (s *Server) hRedactMemory(w http.ResponseWriter, r *http.Request, idn *identity) error {
	memID := r.PathValue("id")
	e, _, err := s.memRead(memID, idn)
	if err != nil {
		return err
	}
	if e.OwnerUserID != idn.UserID && idn.Role != "admin" {
		return core.Fail(api.ErrMemForbidden, "", "only the owner (or admin) can redact")
	}
	if err := s.Mems.Redact(memID); err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]string{"id": memID, "status": "redacted"})
}

func (s *Server) hSearchMemory(w http.ResponseWriter, r *http.Request, idn *identity) error {
	q := r.URL.Query()
	var tags []string
	if t := q.Get("tags"); t != "" {
		tags = strings.Split(t, ",")
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	excerpt, _ := strconv.Atoi(q.Get("excerpt"))
	// project= narrows the search to one project. It is resolved, then
	// read-gated with the same non-leaking not-found shape the rest of
	// the read face uses (an unresolvable and an inaccessible project
	// answer identically), and applied inside the candidate SQL IN
	// ADDITION to — never instead of — the readable-set and visibility
	// layers (06-memory-search.md §6's fixed two-layer order).
	project := ""
	if p := q.Get("project"); p != "" {
		projectID, err := s.projectIDOf(p)
		if err != nil {
			return err
		}
		if err := s.mustRead(idn, projectID, "project %q", p); err != nil {
			return err
		}
		project = projectID
	}
	// match= picks the search mode: "all" (the default — every token
	// must match, the shape of a short human query) or "any" (ranked
	// any-term — the daemon's claim-time recall over a whole task
	// description). An unknown mode is refused (MEM-400 from the store),
	// never silently ANDed.
	//
	// An absent or blank q is browse mode: the read face
	// lists the caller's readable memories instead of refusing. Cursor
	// and sort stay confined to browse — a non-empty q that carries
	// either (an empty value included) is refused before the keyword
	// path, which stays byte-identical and never sees a cursor.
	if strings.TrimSpace(q.Get("q")) == "" {
		return s.hBrowseMemory(w, r, tags, project, idn)
	}
	if q.Has("after") || q.Has("sort") {
		return core.Fail(api.ErrMemBadInput, "",
			"after and sort are browse parameters and cannot be combined with q")
	}
	return s.hSearchRun(w, q.Get("match"), q.Get("q"), q.Get("type"), tags, project, limit, excerpt, idn)
}

// Browse page-size bounds: default 20, maximum 200.
// The keyword path above keeps its own clamp to 50 unchanged.
const (
	defaultBrowseLimit = 20
	maxBrowseLimit     = 200
)

// browseResponse is the browse-mode answer: the readable page, the total
// number of visible entries the whole walk covers and the
// next page's opaque cursor, omitted on the last page.
type browseResponse struct {
	Items      []memory.Entry `json:"items"`
	Total      int            `json:"total"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// hBrowseMemory serves the empty-q browse mode of GET
// /api/v1/memories/search. limit is parsed strictly on this path alone
// (absent = 20, 1..200 accepted, anything else MEM-400 — no silent
// substitution); sort and after are validated by the store (a malformed
// or sort-mismatched cursor is MEM-400, never a restart).
func (s *Server) hBrowseMemory(w http.ResponseWriter, r *http.Request, tags []string, project string, idn *identity) error {
	q := r.URL.Query()
	limit := defaultBrowseLimit
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > maxBrowseLimit {
			return core.Fail(api.ErrMemBadInput, "", "limit must be between 1 and %d", maxBrowseLimit)
		}
		limit = n
	}
	readable, err := s.readableProjects(idn)
	if err != nil {
		return err
	}
	projects := readable
	if project != "" {
		// The project read gate above already proved this caller may read
		// it, so it subsumes the readable set.
		projects = []string{project}
	}
	visible := s.memVisible(idn)
	items, next, err := s.Mems.Browse(projects, q.Get("sort"), q.Get("after"), q.Get("type"), tags, limit, visible)
	if err != nil {
		return err
	}
	// total is the whole walk's size under the same narrowing and
	// visibility, not the page length.
	total, err := s.Mems.BrowseTotal(projects, q.Get("type"), tags, visible)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, browseResponse{Items: items, Total: total, NextCursor: next})
}

// maxMemoryExcerptRunes bounds the content excerpt a search answer may
// carry (the F6 recall faces ask for bounded excerpts; the cap keeps a
// hostile/absent caller from turning search into a full-content dump).
const maxMemoryExcerptRunes = 8192

// memoryHit is one search answer row with a content excerpt attached:
// every memory.Entry field (embedded, marshaled flat) plus the first
// excerpt runes of the stored content. Only served when the caller
// asked for an excerpt — the plain search answer carries no excerpt.
type memoryHit struct {
	memory.Entry
	ContentExcerpt string `json:"content_excerpt,omitempty"`
}

// memoryHitList is the excerpt search answer (ListResponse shape).
type memoryHitList struct {
	Items []memoryHit `json:"items"`
	Total int         `json:"total"`
}

func (s *Server) hSearchRun(w http.ResponseWriter, match, query, memType string, tags []string, project string, limit, excerpt int, idn *identity) error {
	// The caller's readable-project set (key scope ∩ membership) is
	// computed once and pushed into the candidate SQL: unreadable matches
	// cost no load at all. nil = no narrowing (an unscoped
	// admin); an empty non-nil set answers the empty result without a
	// candidate query.
	readable, err := s.readableProjects(idn)
	if err != nil {
		return err
	}
	projects := readable
	if project != "" {
		// hSearchMemory's read gate already proved this caller may read
		// the requested project, so it subsumes the set (a non-member
		// project is 404-folded before it reaches here).
		projects = []string{project}
	}
	if projects != nil && len(projects) == 0 {
		return reply(w, http.StatusOK, api.ListResponse[memory.Entry]{Items: []memory.Entry{}, Total: 0})
	}
	items, err := s.Mems.SearchMode(match, projects, query, memType, tags, limit, s.memVisible(idn))
	if err != nil {
		return err
	}
	if items == nil {
		items = []memory.Entry{}
	}
	// The excerpt extension (F6): a caller that names an excerpt length
	// gets each hit with a bounded content prefix attached. Purely
	// additive — an absent/zero excerpt keeps the plain answer shape.
	if excerpt > 0 {
		if excerpt > maxMemoryExcerptRunes {
			excerpt = maxMemoryExcerptRunes
		}
		hits := make([]memoryHit, 0, len(items))
		for i := range items {
			h := memoryHit{Entry: items[i]}
			if content, cerr := s.Mems.Content(items[i].ID); cerr == nil {
				h.ContentExcerpt = excerptBody(string(content), excerpt)
			}
			hits = append(hits, h)
		}
		return reply(w, http.StatusOK, memoryHitList{Items: hits, Total: len(hits)})
	}
	return reply(w, http.StatusOK, api.ListResponse[memory.Entry]{Items: items, Total: len(items)})
}

// excerptBody answers the first n runes of the memory's BODY: the
// stored document's leading frontmatter block (the id/type/title rows
// the hit already carries as fields) is skipped, so the excerpt is
// content, not metadata echo.
func excerptBody(doc string, n int) string {
	return firstRunes(strings.TrimLeft(memory.Body([]byte(doc)), "\n"), n)
}

// firstRunes answers the first n runes of s (shorter strings pass
// through; n <= 0 answers "").
func firstRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	c := 0
	for i := range s {
		if c == n {
			return s[:i]
		}
		c++
	}
	return s
}

// ---------------------------------------------------------------------------
// Admin face

func (s *Server) hCreateUser(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.UserCreateRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	u, err := s.Admin.CreateUser(req.DisplayName, req.Email, req.Role, req.UserType)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, u)
}

func (s *Server) hListUsers(w http.ResponseWriter, r *http.Request, idn *identity) error {
	// one capped page (200, no cursor); total is the true user count
	users, total, err := s.Admin.ListUsers()
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, api.ListResponse[api.User]{Items: users, Total: total})
}

func (s *Server) hCreateAPIKey(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.APIKeyCreateRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	scope := ""
	if req.ProjectID != nil {
		scope = *req.ProjectID
	}
	key, err := s.Admin.CreateAPIKey(req.UserID, req.Name, scope)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, key) // plaintext rides once, here
}

func (s *Server) hRevokeAPIKey(w http.ResponseWriter, r *http.Request, idn *identity) error {
	if err := s.Admin.RevokeAPIKey(r.PathValue("id")); err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) hCreateProject(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req core.CreateProjectRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	p, err := s.Admin.CreateProject(req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, p)
}

func (s *Server) hListProjects(w http.ResponseWriter, r *http.Request, idn *identity) error {
	// one capped page (200, no cursor); total is the true project count
	projects, total, err := s.Admin.ListProjects()
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, api.ListResponse[api.Project]{Items: projects, Total: total})
}

func (s *Server) hGetProject(w http.ResponseWriter, r *http.Request, idn *identity) error {
	projectID, err := s.projectIDOf(r.PathValue("id"))
	if err != nil {
		return err
	}
	// Read face: membership OR admin (the project surfaces the doctor and
	// the repo-units scan read; a non-member's read stays hidden behind
	// the not-found detail, like every other object read).
	if err := s.mustRead(idn, projectID, "project %q", r.PathValue("id")); err != nil {
		return err
	}
	p, err := s.Admin.GetProject(r.PathValue("id"))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, p)
}

func (s *Server) hGetUser(w http.ResponseWriter, r *http.Request, idn *identity) error {
	u, err := s.Admin.GetUser(r.PathValue("id"))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, u)
}

func (s *Server) hPatchUser(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.UserPatchRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	u, err := s.Admin.PatchUser(r.PathValue("id"), req.DisplayName, req.Role)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, u)
}

func (s *Server) hListAPIKeys(w http.ResponseWriter, r *http.Request, idn *identity) error {
	// one capped page (500, no cursor); total is the true key count
	keys, total, err := s.Admin.ListAPIKeys()
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, api.ListResponse[core.APIKeyInfo]{Items: keys, Total: total})
}

// hPatchProject serves PATCH /api/v1/projects/{id} (M3): description,
// visibility and membership. Authorization: global admin or a maintainer
// of THAT project (02 M3); a project-scoped key may only touch its own.
func (s *Server) hPatchProject(w http.ResponseWriter, r *http.Request, idn *identity) error {
	projectID, err := s.projectIDOf(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := scopeCheck(idn, projectID); err != nil {
		return err
	}
	if idn.Role != "admin" {
		role, merr := s.memberRole(projectID, idn.UserID)
		if merr != nil || role != "maintainer" {
			return core.Fail(api.ErrAuthForbidden, "", "project patch requires admin or project maintainer")
		}
	}
	var req core.ProjectPatchRequest
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	p, err := s.Admin.PatchProject(r.PathValue("id"), req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, p)
}

func (s *Server) hBackup(w http.ResponseWriter, r *http.Request, idn *identity) error {
	name, err := s.Admin.Backup(s.DataDir)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]string{"backup": name})
}

// ---------------------------------------------------------------------------
// Project hard delete

// hDeleteProject serves DELETE /api/v1/projects/{id}: the admin-only
// project hard delete — every project-owned row in one transaction
// (core.AdminManager.DeleteProject), then the per-WI bare git repos
// from disk AFTER the commit (no I/O while the single-writer connection
// is held). ?dry_run=1 runs the same checks and answers the same body
// with nothing written and no repo removed. The 200 body identifies the
// deleted project (api.ProjectDeleteResult). No automatic backup is
// taken: the docs instruct `POST /api/v1/admin/backup` first.
func (s *Server) hDeleteProject(w http.ResponseWriter, r *http.Request, idn *identity) error {
	projectID, err := s.projectIDOf(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := scopeCheck(idn, projectID); err != nil {
		return err
	}
	dryRun := r.URL.Query().Get("dry_run") == "1"
	res, wiIDs, err := s.Admin.DeleteProject(r.PathValue("id"), idn.UserID, dryRun)
	if err != nil {
		return err
	}
	if !dryRun {
		// The transaction has committed: the repos go from disk now,
		// under no lock. A removal failure answers CORE-500 naming the
		// dirs — the delete itself is complete, and the next aihub start
		// (ReplayDeletedRepos) retries the removal.
		if failed := s.removeRepos(wiIDs); len(failed) > 0 {
			return core.Fail(api.ErrCoreInternal, "",
				"project %s deleted but removing its git repos failed: %s (the delete is complete; the next aihub start retries)",
				res.Name, strings.Join(failed, ", "))
		}
	}
	return reply(w, http.StatusOK, res)
}

// removeRepos removes the per-WI bare git repos from disk. It runs only
// AFTER the delete transaction has committed and holds no lock. It
// answers the repo dirs that could not be removed; os.RemoveAll of a
// missing dir is a no-op, so a retry (the startup replay) is idempotent.
func (s *Server) removeRepos(wiIDs []string) []string {
	var failed []string
	for _, id := range wiIDs {
		if err := os.RemoveAll(s.Git.RepoDir(id)); err != nil {
			failed = append(failed, s.Git.RepoDir(id))
		}
	}
	return failed
}

// ReplayDeletedRepos converges an interrupted project delete: every
// work item a performed delete recorded (admin_audit.wi_ids) gets its
// bare git repo removed — a delete that died between its commit and
// the removal is finished here on the next start
// (crash-equals-shutdown). Errors are logged, never fatal: the next
// start retries.
func (s *Server) ReplayDeletedRepos() {
	ids, err := s.Admin.DeletedWorkItemIDs()
	if err != nil {
		log.Printf("server: deleted-repo replay: %v", err)
		return
	}
	if failed := s.removeRepos(ids); len(failed) > 0 {
		log.Printf("server: deleted-repo replay: removing %s failed (the next start retries)", strings.Join(failed, ", "))
	}
}
