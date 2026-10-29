package server

// web_api.go: the console's private JSON face — read-only,
// same-origin, non-contractual (human-console data only, never an
// /api/v1 contract; no stability promise is implied). All data comes
// from the core layer through the same read gates the CLI uses; every
// 404 is the one static body, unknown and unreadable alike.

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/memory"
	"github.com/silgrid/polyforge/internal/common/api"
)

// writeWebJSON answers one buffered JSON body with the same guarantees
// as every /web/ answer: Content-Length for GET and HEAD alike, no
// half-written response on the wire.
func writeWebJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeWebBody(w, r, http.StatusInternalServerError, "application/json", []byte("{\"error\":\"internal error\"}\n"))
		return
	}
	body = append(body, '\n')
	writeWebBody(w, r, status, "application/json", body)
}

// writeWebError renders one console read's error: the typed bad-input
// classes are the static 400, anything else the 500 (a storage
// failure never becomes a false empty answer). The typed not-found
// classes are handled at their call sites — they are the one static
// 404, byte-identical with every other not-found.
func writeWebError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *api.Error
	var me *memory.APIError
	if (errors.As(err, &ae) && ae.Code == api.ErrCoreBadInput) ||
		(errors.As(err, &me) && me.Code == api.ErrMemBadInput) {
		writeWebBadRequest(w, r)
		return
	}
	writeWebBody(w, r, http.StatusInternalServerError, "application/json", []byte("{\"error\":\"internal error\"}\n"))
}

// writeWebTypedNotFound answers the static 404 for one of the typed
// not-found shapes (a core CORE-404 or a memory MEM-404); anything
// else is the caller's error to render.
func webIsNotFound(err error) bool {
	var ae *api.Error
	var me *memory.APIError
	// The core family raises *api.Error (CORE-404); the memory store
	// raises *memory.APIError (MEM-404) — and the server's own memory
	// read gates raise *api.Error carrying MEM-404. All are the one
	// static 404 here.
	if errors.As(err, &ae) && (ae.Code == api.ErrCoreNotFound || ae.Code == api.ErrMemNotFound) {
		return true
	}
	if errors.As(err, &me) && me.Code == api.ErrMemNotFound {
		return true
	}
	return false
}

// webAPI routes the private JSON face. The wrapper has already
// authenticated the caller and gated the method; an unknown api path is
// the static 404, never a guess.
func (s *Server) webAPI(w http.ResponseWriter, r *http.Request, idn *identity, segments []string) {
	if len(segments) == 0 {
		writeWebNotFound(w, r)
		return
	}
	switch segments[0] {
	case "context":
		if len(segments) == 1 {
			s.webAPIContext(w, r, idn)
			return
		}
	case "work-items":
		switch len(segments) {
		case 1:
			s.webAPIWorkItems(w, r, idn)
			return
		case 2:
			s.webAPIWorkItemDetail(w, r, idn, segments[1])
			return
		}
	case "memories":
		switch len(segments) {
		case 1:
			s.webAPIMemories(w, r, idn)
			return
		case 2:
			s.webAPIMemoryDetail(w, r, idn, segments[1])
			return
		}
	case "search":
		if len(segments) == 1 {
			s.webAPISearch(w, r, idn)
			return
		}
	case "machines":
		if len(segments) == 1 {
			s.webAPIMachines(w, r, idn)
			return
		}
	}
	writeWebNotFound(w, r)
}

// webContextProject is one readable project of the context answer.
type webContextProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// webContext is the /web/api/context answer: the caller's identity and
// exactly its readable projects.
type webContext struct {
	UserID   string              `json:"user_id"`
	Role     string              `json:"role"`
	Admin    bool                `json:"admin"`
	Projects []webContextProject `json:"projects"`
}

// webAPIContext answers GET /web/api/context.
func (s *Server) webAPIContext(w http.ResponseWriter, r *http.Request, idn *identity) {
	projects, err := s.webContextProjects(idn)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	writeWebJSON(w, r, http.StatusOK, webContext{
		UserID: idn.UserID, Role: idn.Role, Admin: idn.Role == "admin", Projects: projects,
	})
}

// webWorkItemRow is one console list row: only the fields the list UI
// needs (the search face's rows carry the content too).
type webWorkItemRow struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Seq       int64  `json:"seq"`
	Goal      string `json:"goal"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content,omitempty"`
}

// webWorkItemList is the list answer: the page plus the next page's
// opaque cursor (omitted on the last page).
type webWorkItemList struct {
	Items      []webWorkItemRow `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// webAPIWorkItems answers GET /web/api/work-items?project=&status=&q=&after=
// — the console list page. The readable set is narrowed inside the SQL
// before the page cut (the shipped anti-pressure property).
func (s *Server) webAPIWorkItems(w http.ResponseWriter, r *http.Request, idn *identity) {
	q := webQuery(r)
	projectName := q.Get("project")
	if projectName == "" {
		writeWebBadRequest(w, r)
		return
	}
	projectID, ok, err := s.webProject(idn, projectName)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	if !ok {
		writeWebNotFound(w, r)
		return
	}
	items, next, err := s.WIs.ListWeb(core.WebListFilters{
		ProjectID:        projectID,
		Status:           q.Get("status"),
		Search:           q.Get("q"),
		After:            q.Get("after"),
		ReadableProjects: []string{projectID},
	})
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	rows := make([]webWorkItemRow, 0, len(items))
	for _, it := range items {
		rows = append(rows, webWorkItemRow{
			ID: it.ID, Slug: it.Slug, Seq: it.Seq, Goal: it.Goal,
			Status: it.Status, Priority: it.Priority, CreatedAt: it.CreatedAt,
		})
	}
	writeWebJSON(w, r, http.StatusOK, webWorkItemList{Items: rows, NextCursor: next})
}

// ---------------------------------------------------------------------------
// Work-item detail

// webDepEdge is one dependency edge seen from the detail's side. A
// target the caller cannot read is projected as its id alone — no
// slug, status, title or project (the established non-leak
// convention); a readable target carries its slug and status so the
// console can link it without a second read.
type webDepEdge struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Slug   string `json:"slug,omitempty"`
	Status string `json:"status,omitempty"`
}

// webEvent is one timeline row: time, actor, type and the escaped
// payload with the session identity keys removed (the 11 aihub event
// types stay visible; nothing else is touched).
type webEvent struct {
	CreatedAt string          `json:"created_at"`
	Actor     string          `json:"actor"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// webWIDetail is the console detail DTO: the whitelisted fields only —
// never attrs, never the session block, never session_hash, never the
// repo inventory or any daemon step data. WIView is never serialized
// directly.
type webWIDetail struct {
	ID        string       `json:"id"`
	Slug      string       `json:"slug"`
	Seq       int64        `json:"seq"`
	Goal      string       `json:"goal"`
	Status    string       `json:"status"`
	Priority  string       `json:"priority"`
	Milestone string       `json:"milestone,omitempty"`
	Bundle    string       `json:"bundle,omitempty"`
	Labels    []string     `json:"labels"`
	Reporter  string       `json:"reporter"`
	Assignee  string       `json:"assignee,omitempty"`
	CreatedAt string       `json:"created_at"`
	UpdatedAt string       `json:"updated_at"`
	ClosedAt  string       `json:"closed_at,omitempty"`
	HTML      string       `json:"html"`
	BlockedBy []webDepEdge `json:"blocked_by"`
	Blocking  []webDepEdge `json:"blocking"`
	Events    []webEvent   `json:"events"`
}

// webRedactPayload removes the session identity keys from one event
// payload object. Anything else in the payload stays visible; a
// payload that is not a JSON object passes through untouched.
func webRedactPayload(raw json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return raw
	}
	for _, key := range []string{"session_hash", "session_fingerprint", "old_session_fingerprint", "machine_id", "old_machine_id"} {
		delete(obj, key)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// webAPIWorkItemDetail answers GET /web/api/work-items/{id}?project=.
// The item's project must equal the URL project; unknown, unreadable
// and wrong-project are all the one static 404.
func (s *Server) webAPIWorkItemDetail(w http.ResponseWriter, r *http.Request, idn *identity, ref string) {
	q := webQuery(r)
	projectName := q.Get("project")
	if projectName == "" {
		writeWebBadRequest(w, r)
		return
	}
	projectID, ok, err := s.webProject(idn, projectName)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	if !ok {
		writeWebNotFound(w, r)
		return
	}
	g, err := s.resolveWIGrant(idn, ref)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.ErrCoreNotFound {
			writeWebNotFound(w, r)
			return
		}
		writeWebError(w, r, err)
		return
	}
	if err := g.requireRead(); err != nil || g.projectID != projectID {
		// Concealment and a wrong-project reference are the same
		// answer as a genuinely missing one.
		writeWebNotFound(w, r)
		return
	}
	wi, err := s.WIs.Get(g.id, core.GetOptions{})
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	deps, err := s.WIs.Dependencies(g.id)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	rawEvents, err := s.WIs.Events(g.id)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	// The one server-side sanitizer renders the content (raw HTML off,
	// links as text, images as alt text); the console inserts that
	// already-inert HTML and renders everything else as text.
	body := ""
	if wi.Content != nil {
		if html, herr := renderMarkdownBody(*wi.Content); herr == nil {
			body = string(html)
		} else {
			writeWebError(w, r, herr)
			return
		}
	}
	detail := webWIDetail{
		ID: wi.ID, Slug: wi.Slug, Seq: wi.Seq, Goal: wi.Goal,
		Status: wi.Status, Priority: wi.Priority,
		Labels: wi.Labels, Reporter: wi.Reporter,
		CreatedAt: wi.CreatedAt, UpdatedAt: wi.UpdatedAt,
		HTML: body, BlockedBy: []webDepEdge{}, Blocking: []webDepEdge{}, Events: []webEvent{},
	}
	if wi.Milestone != nil {
		detail.Milestone = *wi.Milestone
	}
	if wi.BundleName != nil {
		detail.Bundle = *wi.BundleName
	}
	if wi.Assignee != nil {
		detail.Assignee = *wi.Assignee
	}
	if wi.ClosedAt != nil {
		detail.ClosedAt = *wi.ClosedAt
	}
	detail.BlockedBy = s.webDepEdges(idn, deps.BlockedBy)
	detail.Blocking = s.webDepEdges(idn, deps.Blocking)
	detail.Events = webTimeline(rawEvents)
	writeWebJSON(w, r, http.StatusOK, detail)
}

// webDepEdges projects dependency edges through the read gate: an
// unreadable target keeps its id alone, a readable one its slug and
// status.
func (s *Server) webDepEdges(idn *identity, edges []core.Edge) []webDepEdge {
	out := make([]webDepEdge, 0, len(edges))
	for _, e := range edges {
		edge := webDepEdge{ID: e.WI, Kind: e.Kind}
		if ok, err := s.webWIReadable(idn, e.WI); err == nil && ok {
			edge.Slug, edge.Status = e.Slug, e.Status
		}
		out = append(out, edge)
	}
	return out
}

// webTimeline projects the aihub event timeline ascending by time then
// id, with the session identity keys redacted from every payload.
func webTimeline(events []core.Event) []webEvent {
	out := make([]webEvent, 0, len(events))
	for _, e := range events {
		out = append(out, webEvent{
			CreatedAt: e.CreatedAt, Actor: e.Actor, Type: e.Type,
			Payload: webRedactPayload(e.Payload),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, erri := time.Parse(time.RFC3339Nano, out[i].CreatedAt)
		tj, errj := time.Parse(time.RFC3339Nano, out[j].CreatedAt)
		if erri == nil && errj == nil && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return false
	})
	return out
}

// ---------------------------------------------------------------------------
// Memories

// webListSearchCap is the global-search group cap: each group shows at
// most 20 rows, whatever the module's own first page returned.
const webListSearchCap = 20

// webMemoryRow is one console memory row: the public fields alone —
// the list fields every consumer (list page and search group) needs.
type webMemoryRow struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Visibility string   `json:"visibility"`
	Tags       []string `json:"tags"`
	UpdatedAt  string   `json:"updated_at"`
	WorkItemID string   `json:"work_item_id,omitempty"`
}

// webMemoryList is the memories page: the rows plus the browse path's
// next-cursor (omitted on the last page) and the keyword path's refine
// hint (exactly when the one top-50 page came back full).
type webMemoryList struct {
	Items      []webMemoryRow `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
	RefineHint bool           `json:"refine_hint"`
}

// webMemoryDetail is the memory detail: the header-card fields plus
// the body already sanitized by the one server-side renderer.
type webMemoryDetail struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Visibility string   `json:"visibility"`
	Tags       []string `json:"tags"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	WorkItemID string   `json:"work_item_id,omitempty"`
	HTML       string   `json:"html"`
}

// The console memory page sizes (the #103 semantics carried over
// unchanged): the browse list pages at 20, the keyword page is one
// page of the top 50.
const (
	webMemBrowseLimit = 20
	webMemSearchLimit = 50
)

// webAPIMemories answers GET /web/api/memories?project=&q=&type=&tags=&after=
// — the #103 read behaviour at the console prefix: an empty q browses
// with the existing keyset cursor, a keyword returns the one top-50
// relevance page with the refine hint when full. No ranking, sort
// params or N1 normalization are added.
func (s *Server) webAPIMemories(w http.ResponseWriter, r *http.Request, idn *identity) {
	q := webQuery(r)
	projectName := q.Get("project")
	if projectName == "" {
		writeWebBadRequest(w, r)
		return
	}
	projectID, ok, err := s.webProject(idn, projectName)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	if !ok {
		writeWebNotFound(w, r)
		return
	}
	memType := q.Get("type")
	var tags []string
	if t := q.Get("tags"); t != "" {
		tags = strings.Split(t, ",")
	}
	visible := s.memVisible(idn)
	if keyword := strings.TrimSpace(q.Get("q")); keyword != "" {
		items, serr := s.Mems.SearchMode("", []string{projectID}, keyword, memType, tags, webMemSearchLimit, visible)
		if serr != nil {
			writeWebError(w, r, serr)
			return
		}
		rows := make([]webMemoryRow, 0, len(items))
		for i := range items {
			rows = append(rows, webMemEntry(&items[i]))
		}
		writeWebJSON(w, r, http.StatusOK, webMemoryList{
			Items: rows, RefineHint: len(items) >= webMemSearchLimit,
		})
		return
	}
	items, next, berr := s.Mems.Browse([]string{projectID}, "", q.Get("after"), memType, tags, webMemBrowseLimit, visible)
	if berr != nil {
		writeWebError(w, r, berr)
		return
	}
	rows := make([]webMemoryRow, 0, len(items))
	for i := range items {
		rows = append(rows, webMemEntry(&items[i]))
	}
	writeWebJSON(w, r, http.StatusOK, webMemoryList{Items: rows, NextCursor: next})
}

// webAPIMemoryDetail answers GET /web/api/memories/{id}?project=.
// Unknown, unreadable, a visibility tier above the caller and a memory
// of another project reached through this one's path are all the one
// static 404; the body is the already-sanitized HTML of the shared
// renderer.
func (s *Server) webAPIMemoryDetail(w http.ResponseWriter, r *http.Request, idn *identity, memID string) {
	q := webQuery(r)
	projectName := q.Get("project")
	if projectName == "" {
		writeWebBadRequest(w, r)
		return
	}
	projectID, ok, err := s.webProject(idn, projectName)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	if !ok {
		writeWebNotFound(w, r)
		return
	}
	entry, content, rerr := s.memRead(memID, idn)
	if rerr != nil {
		if webIsNotFound(rerr) {
			writeWebNotFound(w, r)
			return
		}
		writeWebError(w, r, rerr)
		return
	}
	if entry.ProjectID != projectID {
		writeWebNotFound(w, r)
		return
	}
	html, herr := renderMarkdownBody(memory.Body(content))
	if herr != nil {
		writeWebError(w, r, herr)
		return
	}
	detail := webMemoryDetail{
		ID: entry.ID, Title: webMemTitle(entry), Type: entry.Type,
		Visibility: entry.Visibility, Tags: entry.Tags,
		CreatedAt: entry.CreatedAt, UpdatedAt: entry.UpdatedAt, HTML: string(html),
	}
	if entry.WorkItemID != nil {
		detail.WorkItemID = *entry.WorkItemID
	}
	writeWebJSON(w, r, http.StatusOK, detail)
}

// ---------------------------------------------------------------------------
// Global search

// webSearchResults is the global-search answer: two labelled groups,
// each capped at 20, scoped to the selected project only.
type webSearchResults struct {
	WorkItems []webWorkItemRow `json:"work_items"`
	Memories  []webMemoryRow   `json:"memories"`
}

// webAPISearch answers GET /web/api/search?project=&q= — the first
// ListWeb page (≤20) for work items and the first 20 of the existing
// top-50 relevance result for memories. No new ranking, no
// cross-project search: both groups are the modules' own first pages.
func (s *Server) webAPISearch(w http.ResponseWriter, r *http.Request, idn *identity) {
	q := webQuery(r)
	projectName := q.Get("project")
	keyword := strings.TrimSpace(q.Get("q"))
	if projectName == "" || keyword == "" {
		writeWebBadRequest(w, r)
		return
	}
	projectID, ok, err := s.webProject(idn, projectName)
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	if !ok {
		writeWebNotFound(w, r)
		return
	}
	wiRows, _, werr := s.WIs.ListWeb(core.WebListFilters{
		ProjectID: projectID, Search: keyword,
		ReadableProjects: []string{projectID},
	})
	if werr != nil {
		writeWebError(w, r, werr)
		return
	}
	workItems := make([]webWorkItemRow, 0, len(wiRows))
	for _, it := range wiRows {
		// The search face's rows carry the content too, so a hit that
		// matched the content (not the goal) shows its match.
		workItems = append(workItems, webWorkItemRow{
			ID: it.ID, Slug: it.Slug, Seq: it.Seq, Goal: it.Goal,
			Status: it.Status, Priority: it.Priority, CreatedAt: it.CreatedAt,
			Content: it.Content,
		})
	}
	memHits, merr := s.Mems.SearchMode("", []string{projectID}, keyword, "", nil, webMemSearchLimit, s.memVisible(idn))
	if merr != nil {
		writeWebError(w, r, merr)
		return
	}
	memories := make([]webMemoryRow, 0, len(memHits))
	for i := range memHits {
		memories = append(memories, webMemEntry(&memHits[i]))
	}
	if len(memories) > webListSearchCap {
		memories = memories[:webListSearchCap]
	}
	writeWebJSON(w, r, http.StatusOK, webSearchResults{WorkItems: workItems, Memories: memories})
}

// ---------------------------------------------------------------------------
// Machines

// webMachineLiveWI is one machine's live work item: the id, plus the
// slug when the caller can read the item's project (an unreadable link
// keeps its id alone).
type webMachineLiveWI struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
}

// webMachine is one machine health card. Never the public key, never
// the capability card: identity material has no place in the console.
type webMachine struct {
	ID                 string             `json:"id"`
	OperatorID         string             `json:"operator_id"`
	Role               string             `json:"role"`
	Mode               string             `json:"mode"`
	RuntimeVersion     string             `json:"runtime_version"`
	Launcher           string             `json:"launcher"`
	LastHeartbeatAt    string             `json:"last_heartbeat_at"`
	HeartbeatAgeSecond *int64             `json:"heartbeat_age_seconds,omitempty"`
	Stale              bool               `json:"stale"`
	Superseded         bool               `json:"superseded"`
	LiveWIs            []webMachineLiveWI `json:"live_wis"`
}

// webMachinesSnapshot is the machines page: a snapshot, one answer per
// load — no refresh, no polling, no timer.
type webMachinesSnapshot struct {
	StaleAfterSeconds int64        `json:"stale_after_seconds"`
	Machines          []webMachine `json:"machines"`
}

// webAPIMachines answers GET /web/api/machines — admin-only (a
// non-admin gets the identical static 404, so the route is no
// existence oracle for operators' machine fleets). The health
// projection follows the api's staleness definition: stale comes from
// the configured StaleAfter threshold against the same table.
func (s *Server) webAPIMachines(w http.ResponseWriter, r *http.Request, idn *identity) {
	if idn.Role != "admin" {
		writeWebNotFound(w, r)
		return
	}
	rows, err := s.Admin.Machines()
	if err != nil {
		writeWebError(w, r, err)
		return
	}
	staleAfter := s.machinesStaleAfter()
	now := time.Now()
	out := make([]webMachine, 0, len(rows))
	for _, m := range rows {
		card := webMachine{
			ID: m.ID, OperatorID: m.OperatorID, Role: m.Role, Mode: m.Mode,
			RuntimeVersion: m.RuntimeVersion, Launcher: m.Launcher,
			LastHeartbeatAt: m.LastHeartbeatAt, Superseded: m.SupersededAt != "",
			LiveWIs: []webMachineLiveWI{},
		}
		if age, stale, ok := machineHeartbeat(m.LastHeartbeatAt, now, staleAfter); ok {
			card.HeartbeatAgeSecond = &age
			card.Stale = stale
		}
		for _, wiID := range m.LiveWIIDs {
			link := webMachineLiveWI{ID: wiID}
			if ok, rerr := s.webWIReadable(idn, wiID); rerr == nil && ok {
				link.Slug = s.webWISlug(wiID)
			}
			card.LiveWIs = append(card.LiveWIs, link)
		}
		out = append(out, card)
	}
	writeWebJSON(w, r, http.StatusOK, webMachinesSnapshot{
		StaleAfterSeconds: int64(staleAfter.Seconds()), Machines: out,
	})
}
