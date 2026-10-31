// Package aihubclient is the shared HTTP client for the aihub server, used
// by runtime (session claim/attach/force-end/end), observe (read-through)
// and the CLI verbs. Wire types come from internal/common/api; error
// decoding maps the typed errStatus envelope back into *api.Error.
package aihubclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
)

// Client talks to one aihub server.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// MachineID/MachineKey are the daemon's cross-machine identity; nil key
	// = SignedDo refuses (no machine face).
	MachineID  string
	MachineKey ed25519.PrivateKey
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Do performs a typed request. body may be nil. out receives the decoded
// JSON response when non-nil. Non-2xx answers decode as *api.Error.
func (c *Client) Do(method, path string, body, out any) error {
	return c.DoContext(context.Background(), method, path, body, out)
}

// DoContext is Do bounded by ctx: a caller that must not wait the
// client's full timeout gives the request its own deadline.
func (c *Client) DoContext(ctx context.Context, method, path string, body, out any) error {
	data, err := c.send(ctx, method, path, body)
	if err != nil || out == nil || len(data) == 0 {
		return err
	}
	return json.Unmarshal(data, out)
}

// send performs one authenticated request and returns the response body
// (bounded to 32 MiB); a non-2xx answer is the typed error.
func (c *Client) send(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, typedError(resp.StatusCode, data)
	}
	return data, nil
}

// raw performs one CLI-facing passthrough call: out is the undecoded
// response body so --json mode hands the server payload through verbatim.
func (c *Client) raw(method, path string, body any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.Do(method, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// typedError maps a non-2xx response body back into *api.Error: the
// typed envelope when one is present, a status-synthesized code
// otherwise (a proxy's HTML 502/504 page, a bare 404).
func typedError(status int, data []byte) error {
	var ae api.Error
	if json.Unmarshal(data, &ae) == nil && ae.Code != "" {
		return &ae
	}
	return &api.Error{
		Code:   fmt.Sprintf("HTTP-%d", status),
		Detail: fmt.Sprintf("HTTP %d: %.200s", status, data),
	}
}

func (c *Client) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	var wi api.WorkItem
	if err := c.Do("GET", "/api/v1/work-items/"+url.PathEscape(idOrSlug), nil, &wi); err != nil {
		return nil, err
	}
	return &wi, nil
}

// Queue returns the ready queue's executable items for autonomous
// claiming (the daemon auto-pull).
func (c *Client) Queue(project string) ([]api.WorkItem, error) {
	var resp struct {
		Items []api.WorkItem `json:"items"`
	}
	if err := c.Do("GET", "/api/v1/projects/"+url.PathEscape(project)+"/ready-queue", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// ---------------------------------------------------------------------------
// CLI-facing endpoints.
//
// These return the RAW response body (json.RawMessage) so the CLI's
// --json mode can pass the server payload through verbatim; the human
// renderers decode their own view structs from the same bytes. Endpoint
// surface is the v0 openapi face minus the deleted SYNC-*/revision-chain
// rows (git IS the revision chain in v1).
//
// Only additive methods live here: no existing signature above changes.
// ---------------------------------------------------------------------------

// CreateWorkItemRequest is the work-item create body.
type CreateWorkItemRequest struct {
	Project    string         `json:"project"`
	Goal       string         `json:"goal"`
	Content    *string        `json:"content,omitempty"`
	ParentID   *string        `json:"parent_id,omitempty"`
	Priority   string         `json:"priority,omitempty"`
	Milestone  *string        `json:"milestone,omitempty"`
	Labels     []string       `json:"labels,omitempty"`
	Attrs      map[string]any `json:"attrs,omitempty"`
	BundleName *string        `json:"bundle_name,omitempty"`
	// RepoInventory is passed through to aihub as given (reference.md §2.1:
	// [{name}] of project repos), so aihub refuses what it does not accept
	// (url, subdir, any other key) instead of the CLI dropping it.
	RepoInventory json.RawMessage `json:"repo_inventory,omitempty"`
	// After lists the work items (id or slug) this one runs after: aihub
	// writes one `blocks` edge per entry inside the create transaction,
	// or refuses the whole create (polyforge#161).
	After []string `json:"after,omitempty"`
}

// CreateWorkItem creates a work item and returns the raw detail record.
func (c *Client) CreateWorkItem(req CreateWorkItemRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/work-items", req)
}

// GetWorkItemRaw reads the detail record undecoded (--json passthrough).
func (c *Client) GetWorkItemRaw(idOrSlug string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/work-items/"+url.PathEscape(idOrSlug), nil)
}

// PatchWorkItem updates the PATCHable field set. body must contain only
// keys from aihub's whitelist {goal, content, labels, priority,
// milestone, waiting_on, clean_pause, bundle_name} — any other key
// (attrs included) answers CORE-400 IMMUTABLE server-side.
func (c *Client) PatchWorkItem(id string, body map[string]any) (json.RawMessage, error) {
	return c.raw("PATCH", "/api/v1/work-items/"+url.PathEscape(id), body)
}

// CancelWorkItem terminates a queued/paused/blocked WI (no active
// generation).
func (c *Client) CancelWorkItem(id string) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/work-items/"+url.PathEscape(id)+"/cancel", map[string]any{})
}

// WrapWorkItem is the hub's terminal verdict on a work item with no
// bundle (queued/paused/blocked -> wrapped, or failed with req.Fail).
func (c *Client) WrapWorkItem(id string, req api.WrapRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/work-items/"+url.PathEscape(id)+"/wrap", req)
}

// ListWorkItemsRaw lists work items undecoded (0 limit = the server
// default cap).
func (c *Client) ListWorkItemsRaw(project, status string, limit int) (json.RawMessage, error) {
	q := url.Values{}
	if project != "" {
		q.Set("project", project)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.raw("GET", "/api/v1/work-items?"+q.Encode(), nil)
}

// ReadyQueueRaw is the raw-body variant of Queue (same endpoint).
func (c *Client) ReadyQueueRaw(project string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/projects/"+url.PathEscape(project)+"/ready-queue", nil)
}

// AddBlocksDependency creates the `blocks` edge "blocking blocks
// blocked" (POST /work-items/{blocked}/dependencies; both sides accept
// an id or a slug). Only the `blocks` kind is exposed (polyforge#161).
// Answers the raw two-sided dependency view of blocked.
func (c *Client) AddBlocksDependency(blocking, blocked string) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/work-items/"+url.PathEscape(blocked)+"/dependencies",
		map[string]string{"blocking_wi_id": blocking, "kind": "blocks"})
}

// RemoveBlocksDependency deletes the `blocks` edge "blocking blocks
// blocked". Answers the raw dependency view of blocked.
func (c *Client) RemoveBlocksDependency(blocking, blocked string) (json.RawMessage, error) {
	return c.raw("DELETE", "/api/v1/work-items/"+url.PathEscape(blocked)+"/dependencies/"+
		url.PathEscape(blocking)+"/blocks", nil)
}

// DependenciesRaw reads a work item's two-sided dependency view.
func (c *Client) DependenciesRaw(idOrSlug string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/work-items/"+url.PathEscape(idOrSlug)+"/dependencies", nil)
}

// SearchExcerptRunes is the content excerpt length SearchMemories asks
// for: the search answer is memory.Entry rows (no body), and the server
// only attaches content_excerpt when the caller names a length (without
// it the recall table has no text).
const SearchExcerptRunes = 120

// SearchMemories runs the memory retrieval (06 §5.1). A non-empty q is
// the keyword search; an empty q is browse mode (the
// human direct-aihub path only): the readable active memories as a
// page, answered as {items, total, next_cursor?}. memType, tags and
// limit are optional filters; match is the keyword search mode ("" —
// the server default, every token must match — or "any", the ranked
// any-term mode). sort ("updated"/"created") and after (a previous
// browse page's next_cursor) are the browse paging parameters, sent
// only when non-empty: aihub answers MEM-400 when they ride a keyword
// search, so the mixed shape is refused there, never paraphrased here.
// Each keyword hit carries a SearchExcerptRunes content_excerpt.
func (c *Client) SearchMemories(project, q, memType string, tags []string, limit int, match, sort, after string) (json.RawMessage, error) {
	v := url.Values{}
	v.Set("project", project)
	if q != "" {
		v.Set("q", q)
	}
	if match != "" {
		v.Set("match", match)
	}
	if sort != "" {
		v.Set("sort", sort)
	}
	if after != "" {
		v.Set("after", after)
	}
	v.Set("excerpt", strconv.Itoa(SearchExcerptRunes))
	if memType != "" {
		v.Set("type", memType)
	}
	if len(tags) > 0 {
		v.Set("tags", strings.Join(tags, ","))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	return c.raw("GET", "/api/v1/memories/search?"+v.Encode(), nil)
}

// GetMemory reads one memory's metadata (GET /api/v1/memories/{id}, the
// v1 per-id memory face): the default JSON response is the memory.Entry
// metadata and carries NO content — the body only comes back from
// GetMemoryContent (?format=content). Raw so the CLI's --json mode can
// hand the server metadata through verbatim.
func (c *Client) GetMemory(id string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/memories/"+url.PathEscape(id), nil)
}

// GetMemoryContent reads one memory's body (GET
// /api/v1/memories/{id}?format=content): the server answers the bare
// body (no front-matter) as text/markdown, NOT JSON, so the body bytes
// are returned undecoded — a JSON decode would refuse plain markdown.
func (c *Client) GetMemoryContent(id string) ([]byte, error) {
	return c.send(context.Background(), "GET", "/api/v1/memories/"+url.PathEscape(id)+"?format=content", nil)
}

// The admin request bodies are the shared common/api wire types.
type (
	UserCreateRequest    = api.UserCreateRequest
	UserPatchRequest     = api.UserPatchRequest
	ApiKeyCreateRequest  = api.APIKeyCreateRequest
	ProjectCreateRequest = api.ProjectCreateRequest
	ProjectMember        = api.ProjectMember
	ProjectPatchRequest  = api.ProjectPatchRequest
	RepoRef              = api.ProjectRepo
)

// CreateUser issues a user (M1).
func (c *Client) CreateUser(req UserCreateRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/users", req)
}

// ListUsers lists users (M1).
func (c *Client) ListUsers() (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/users", nil)
}

// GetUser reads one user (M1).
func (c *Client) GetUser(id string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/users/"+url.PathEscape(id), nil)
}

// PatchUser updates display_name and/or role (M1).
func (c *Client) PatchUser(id string, req UserPatchRequest) (json.RawMessage, error) {
	return c.raw("PATCH", "/api/v1/users/"+url.PathEscape(id), req)
}

// CreateApiKey mints a key (M2).
func (c *Client) CreateApiKey(req ApiKeyCreateRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/api-keys", req)
}

// ListApiKeys lists key metadata, never material (M2).
func (c *Client) ListApiKeys() (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/api-keys", nil)
}

// RevokeApiKey sets revoked_at (M2).
func (c *Client) RevokeApiKey(id string) (json.RawMessage, error) {
	return c.raw("DELETE", "/api/v1/api-keys/"+url.PathEscape(id), nil)
}

// CreateProject creates a project (M3).
func (c *Client) CreateProject(req ProjectCreateRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/projects", req)
}

// ListProjects lists visible projects (M3).
func (c *Client) ListProjects() (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/projects", nil)
}

// GetProject reads one project by id or name (M3).
func (c *Client) GetProject(idOrName string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/projects/"+url.PathEscape(idOrName), nil)
}

// PatchProject updates a project and/or its membership rows (M3).
func (c *Client) PatchProject(idOrName string, req ProjectPatchRequest) (json.RawMessage, error) {
	return c.raw("PATCH", "/api/v1/projects/"+url.PathEscape(idOrName), req)
}

// DeleteProject hard-deletes a project (DELETE /api/v1/projects/{id};
// admin only). dryRun runs the same checks and answers the same body
// with nothing written (?dry_run=1 — the CLI's confirmation preflight).
func (c *Client) DeleteProject(idOrName string, dryRun bool) (json.RawMessage, error) {
	path := "/api/v1/projects/" + url.PathEscape(idOrName)
	if dryRun {
		path += "?dry_run=1"
	}
	return c.raw("DELETE", path, nil)
}

// Backup triggers the on-demand snapshot (M4).
func (c *Client) Backup() (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/admin/backup", map[string]any{})
}

// ---------------------------------------------------------------------------
// Machines faces (M3 heartbeat wiring).

// The machines bodies are the shared common/api wire types.
type (
	MachineHeartbeat       = api.MachineHeartbeat
	MachineHeartbeatResult = api.MachineHeartbeatResult
	MachineStatus          = api.MachineStatus
	MachinesList           = api.MachinesList
)

// Heartbeat posts one machine heartbeat (upsert: first call registers). It
// is a SIGNED machine-face request: the server accepts a beat only from the
// machine's operator presenting its bound key, so the client proves
// possession with the identity SetMachineIdentity installed (the daemon
// installs it in Run before any lane starts). A client without one gets the
// existing AUTH-401 "machine identity is not loaded".
func (c *Client) Heartbeat(req MachineHeartbeat) (*MachineHeartbeatResult, error) {
	var out MachineHeartbeatResult
	if err := c.SignedDo("POST", "/api/v1/machines/heartbeat", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMachinesRaw reads the machines liveness face undecoded (--json
// passthrough: the server body byte-for-byte).
func (c *Client) ListMachinesRaw() (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/machines", nil)
}

// ---------------------------------------------------------------------------
// v1.5 session model client (reference.md §3).

// SessionClaim performs the CAS claim: set the session only if the WI is
// queued and unheld (M1 joint gate). A repeat with the same session
// secret answers the same claim while that session holds the WI (D4).
func (c *Client) SessionClaim(req api.SessionClaimRequest) (*api.SessionClaimResponse, error) {
	var out api.SessionClaimResponse
	if err := c.Do("POST", "/api/v1/work-items/"+url.PathEscape(req.WorkItemID)+"/claim", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionAttach resumes a paused WI under the same session (M3).
func (c *Client) SessionAttach(wi string, req api.SessionAttachRequest) (*api.SessionClaimResponse, error) {
	var out api.SessionClaimResponse
	if err := c.Do("POST", "/api/v1/work-items/"+url.PathEscape(wi)+"/session-attach", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ForceEndSession is the admin/maintainer recovery door: it ends the held
// session (WI running|paused -> queued, old secret fenced); any daemon then
// claims the WI normally. Authorization is server-side (admin or project
// maintainer).
func (c *Client) ForceEndSession(wi, reason string) error {
	return c.Do("POST", "/api/v1/work-items/"+url.PathEscape(wi)+"/force-end-session",
		api.ForceEndSessionRequest{Reason: reason}, nil)
}

// SessionVerify is the compare-only holder check (B3: no secret echo).
func (c *Client) SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error) {
	var out api.SessionVerifyResponse
	req := api.SessionVerifyRequest{SessionID: sessionID}
	if err := c.Do("POST", "/api/v1/work-items/"+url.PathEscape(wi)+"/session-verify", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionEnd is the session-fenced terminal verdict (and the paused
// transition — the merge amend).
func (c *Client) SessionEnd(wi string, req api.SessionEndRequest) error {
	return c.Do("POST", "/api/v1/work-items/"+url.PathEscape(wi)+"/session-end", req, nil)
}

// ResumeBlockedWorkItem is the §6.4 owner resume verdict's hub door
// (polyforge#116): `pf resume` on a workflow parked blocked — status
// blocked → queued for the owner's immediate re-claim; the daemon then
// re-establishes execution over the parked capsule. Authorization is
// server-side (the reporter, a project maintainer or an admin).
func (c *Client) ResumeBlockedWorkItem(idOrSlug string) (json.RawMessage, error) {
	return c.ResumeBlockedWorkItemWith(idOrSlug, api.ResumeBlockedRequest{})
}

// ResumeBlockedWorkItemWith is ResumeBlockedWorkItem with the door's
// options (AcceptLostWork also clears the blocked seal).
func (c *Client) ResumeBlockedWorkItemWith(idOrSlug string, req api.ResumeBlockedRequest) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/work-items/"+url.PathEscape(idOrSlug)+"/resume-blocked", req)
}

// ---------------------------------------------------------------------------
// machine face (signed client half)

// SetMachineIdentity binds the daemon's Ed25519 key and machine id to this
// client. The signer is immutable for the process lifetime.
func (c *Client) SetMachineIdentity(machineID string, key ed25519.PrivateKey) {
	c.MachineID = machineID
	c.MachineKey = key
}

// SignedDo performs one machine-face request: the Bearer key PLUS the three
// X-PF-* headers over METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID.
func (c *Client) SignedDo(method, path string, body, out any) error {
	data, err := c.signedSend(method, path, body)
	if err != nil || out == nil || len(data) == 0 {
		return err
	}
	return json.Unmarshal(data, out)
}

func (c *Client) signedSend(method, path string, body any) ([]byte, error) {
	if c.MachineKey == nil || c.MachineID == "" {
		return nil, &api.Error{Code: api.ErrAuthUnauthorized, Detail: "machine identity is not loaded"}
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	ts := time.Now().Unix()
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(machinesign.HeaderMachineID, c.MachineID)
	req.Header.Set(machinesign.HeaderTimestamp, strconv.FormatInt(ts, 10))
	signPath := path
	if i := strings.IndexByte(signPath, '?'); i >= 0 {
		signPath = signPath[:i]
	}
	req.Header.Set(machinesign.HeaderSignature, machinesign.Sign(c.MachineKey, method, signPath, raw, ts, c.MachineID))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, typedError(resp.StatusCode, data)
	}
	return data, nil
}

// Inbox long-polls the signed holder feed.
func (c *Client) Inbox(faces string, wait time.Duration) (*api.MachineInbox, error) {
	path := "/api/v1/machines/inbox?faces=" + url.QueryEscape(faces)
	if wait > 0 {
		path += "&wait=" + wait.String()
	}
	var out api.MachineInbox
	if err := c.SignedDo("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AnswerQuery answers one pending query from the holder inbox.
func (c *Client) AnswerQuery(qid string, body api.QueryAnswerBody) error {
	return c.SignedDo("POST", "/api/v1/queries/"+url.PathEscape(qid)+"/answer", body, nil)
}

// ProgressQuery asks aihub to relay a progress (or artifact) query.
func (c *Client) ProgressQuery(wi, artifactPath string) (*api.QueryAnswerBody, error) {
	path := "/api/v1/work-items/" + url.PathEscape(wi)
	if artifactPath != "" {
		path += "/artifact-query"
	} else {
		path += "/progress-query"
	}
	var out api.QueryAnswerBody
	if err := c.SignedDo("POST", path, api.ProgressQuery{WorkItem: wi, ArtifactPath: artifactPath}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RekeyMachine is the admin-only public-key rebind.
func (c *Client) RekeyMachine(machineID, publicKey string) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/machines/"+url.PathEscape(machineID)+"/rekey", api.RekeyRequest{PublicKey: publicKey})
}

// SendMessage posts one user-to-user message.
func (c *Client) SendMessage(req api.SendMessage) (json.RawMessage, error) {
	return c.raw("POST", "/api/v1/messages", req)
}

// ListConversations lists the caller's conversations.
func (c *Client) ListConversations() (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/conversations", nil)
}

// ProjectContacts reads one project's member face: the caller's human
// co-members as id + display name.
func (c *Client) ProjectContacts(project string) ([]api.ProjectMemberContact, error) {
	return c.projectContacts(context.Background(), project, "")
}

// ProjectContactsWithCaller reads the same member face with the caller's
// own row kept (include_caller=true): the daemon's spawn-time reporter
// resolution, where the caller is the daemon key's owner; ctx bounds the
// read.
func (c *Client) ProjectContactsWithCaller(ctx context.Context, project string) ([]api.ProjectMemberContact, error) {
	return c.projectContacts(ctx, project, "?include_caller=true")
}

func (c *Client) projectContacts(ctx context.Context, project, query string) ([]api.ProjectMemberContact, error) {
	var answer api.ProjectMemberContacts
	if err := c.DoContext(ctx, "GET", "/api/v1/projects/"+url.PathEscape(project)+"/members"+query, nil, &answer); err != nil {
		return nil, err
	}
	return answer.Contacts, nil
}

// ListMessages reads one conversation's history.
func (c *Client) ListMessages(convID string, after, before int64, limit int) (json.RawMessage, error) {
	v := url.Values{}
	if after > 0 {
		v.Set("after", strconv.FormatInt(after, 10))
	}
	if before > 0 {
		v.Set("before", strconv.FormatInt(before, 10))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/v1/conversations/" + url.PathEscape(convID) + "/messages"
	if enc := v.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.raw("GET", path, nil)
}

// GetMessage reads one message.
func (c *Client) GetMessage(id string) (json.RawMessage, error) {
	return c.raw("GET", "/api/v1/messages/"+url.PathEscape(id), nil)
}

// Acknowledge advances the caller's read cursor and answers the server
// payload unchanged (the CLI's --json passthrough).
func (c *Client) Acknowledge(convID string, seq int64) (json.RawMessage, error) {
	return c.send(context.Background(), "POST", "/api/v1/messages/ack", api.AckMessage{ConversationID: convID, Seq: seq})
}

// DeliverMessage advances the caller's machine delivery cursor only.
func (c *Client) DeliverMessage(convID string, seq int64) error {
	return c.Do("POST", "/api/v1/messages/delivered", api.AckMessage{ConversationID: convID, Seq: seq}, nil)
}
