package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// DaemonClient speaks the daemon's UDS HTTP face: HTTP over the local
// unix socket (<data-dir>/daemon.sock by default). The authoritative route
// list is the mount list in internal/polyforge/daemon/routes.go (mirrored
// in docs/reference/uds-routes.md); the core of it:
//
//	POST /jobs/{id}/claim                   body {"priority":"normal"} or {"resume":true}
//	POST /jobs/{id}/resume-paused
//	POST /jobs/{id}/answer                  body = AnswerPayload
//	                                        {answer_text, answered_by}
//	POST /jobs/{id}/pause  {}               POST /jobs/{id}/wrap {"reason"}
//	GET  /status
//	POST /ops/config {"auto":bool,"max":N}
//	POST /shutdown
//
// pf watch lane (v2-converge §3): GET /jobs/{id}/watch?after=<seq> (one WI's
// authoritative snapshot + events after a cursor) and the daemon-held chat
// subscriptions under /chats/{chat}/ (subscribe, unsubscribe, subscriptions,
// ack, heartbeat, attention) — see protocol/watch.go.
//
// Error envelope: the daemon answers non-2xx with the SAME typed
// envelope the aihub face uses (common/api.Error: {code, detail} at the
// top level, DAEMON-* codes per reference.md §5). The phase-3 daemon assembly
// must serve exactly this shape.
type DaemonClient struct {
	Sock string
	HTTP *http.Client
	// Origin, when set, rides on the submit-family bodies so the daemon
	// can record who submitted the job (the owner).
	Origin *SubmitOrigin
	// Profile, when set, is stamped as the X-PF-Profile header on every
	// request: the Cell the Host routes it to ("" = no header — the
	// default Cell, or a Host-scope request).
	Profile string
	// ProfileErr is the resolver's fail-closed answer (the Host's
	// effective index could not be read): while Profile is still
	// unresolvedProfile every request answers it before any dial. A
	// caller that routes explicitly assigns Profile and so replaces both.
	ProfileErr error
}

// unresolvedProfile marks a client whose Cell the resolver could not
// decide (never a valid profile name: cellcfg.ValidName refuses it).
const unresolvedProfile = "\x00unresolved"

// SubmitOrigin is the optional "origin" of a submit-family request body.
type SubmitOrigin struct {
	Owner string `json:"owner,omitempty"`
}

// MachineRole asks the daemon for a machine role (POST /machine/role):
// interactive, or runner; force takes the interactive role over from the
// machine that holds it (the move `pf chat init --force`
// used to make); ifUnset applies only while no operator chose a role.
func (d *DaemonClient) MachineRole(role string, force, ifUnset bool) (json.RawMessage, error) {
	body := map[string]any{"role": role, "force": force}
	if ifUnset {
		body["if_unset"] = true
	}
	return d.do("POST", "/machine/role", "", body, nil)
}

// withOrigin adds the origin to a request body when one is configured.
func (d *DaemonClient) withOrigin(body map[string]any) map[string]any {
	if d.Origin != nil {
		body["origin"] = d.Origin
	}
	return body
}

// NewDaemonClient builds a client dialing the given unix socket.
func NewDaemonClient(sock string) *DaemonClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	return &DaemonClient{
		Sock: sock,
		HTTP: &http.Client{Transport: tr, Timeout: 120 * time.Second},
	}
}

// do issues one request and returns the raw response body. Any non-2xx
// answer is decoded into *api.Error; a body without the envelope
// degrades to a synthesized "HTTP-<status>" code rather than masking
// the real status.
func (d *DaemonClient) do(method, path, query string, body any, header map[string]string) (json.RawMessage, error) {
	return d.doCtx(context.Background(), method, path, query, body, header)
}

// doCtx is do under the caller's context: the request ends when ctx does.
func (d *DaemonClient) doCtx(ctx context.Context, method, path, query string, body any, header map[string]string) (json.RawMessage, error) {
	if d.ProfileErr != nil && d.Profile == unresolvedProfile {
		return nil, d.ProfileErr
	}
	u := "http://daemon" + path // host is decorative; transport dials the socket
	if query != "" {
		u += "?" + query
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}
	var req *http.Request
	var err error
	if payload != nil {
		req, err = http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if d.Profile != "" {
		req.Header.Set(protocol.ProfileHeader, d.Profile)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, parseWireError(resp.StatusCode, raw)
	}
	return json.RawMessage(raw), nil
}

// parseWireError decodes a non-2xx body into *api.Error.
func parseWireError(status int, body []byte) *api.Error {
	var ae api.Error
	if json.Unmarshal(body, &ae) == nil && ae.Code != "" {
		return &ae
	}
	return &api.Error{
		Code:   fmt.Sprintf("HTTP-%d", status),
		Detail: strings.TrimSpace(string(body)),
	}
}

// AnswerPayload is the POST /jobs/{id}/answer body: the owner's words to
// the work item's current step agent.
type AnswerPayload struct {
	AnswerText string `json:"answer_text"`
	AnsweredBy string `json:"answered_by"`
	// Wake names the park whose door the caller opened (workflow contract
	// 4): the daemon records the message and wakes the parked work item.
	Wake   string        `json:"wake,omitempty"`
	Origin *SubmitOrigin `json:"origin,omitempty"`
}

// OpsConfigRequest is the POST /ops/config body: the auto switch and/or
// the max-concurrent-jobs number (at least one).
type OpsConfigRequest struct {
	Auto *bool `json:"auto,omitempty"`
	Max  *int  `json:"max,omitempty"`
}

// ConfigKeyOutcome is one key's reload outcome (R15).
type ConfigKeyOutcome struct {
	Key    string `json:"key"`
	Class  string `json:"class,omitempty"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ConfigReloadResponse is POST /config/reload's answer: the per-key
// outcomes plus the pending-restart and flag-overridden sets.
type ConfigReloadResponse struct {
	Outcomes   []ConfigKeyOutcome `json:"outcomes"`
	Pending    []string           `json:"pending,omitempty"`
	Overridden []string           `json:"overridden,omitempty"`
}

// ConfigMappingEntry is one proposed [workspace] section for
// POST /config/check.
type ConfigMappingEntry struct {
	Workspace string   `json:"workspace"`
	Projects  []string `json:"projects"`
}

// ConfigCheckResponse is POST /config/check's answer.
type ConfigCheckResponse struct {
	OK bool `json:"ok"`
}

// SubmitResponse is the claim-family answer (the claim's
// verdict). A refusal is the error envelope, never this answer; accepted
// is always true here. state says where the claim stands when the answer
// is written: "claimed" — aihub answered the claim (the capsule clone
// may still be under way) — or "queued" — nothing is claimed yet, the
// entry waits on the daemon's queue for queued_reason. The placement
// fields ride a claimed answer only: machine_id / capsule_path state the
// claim's landing (absent when the placement is unknown — a probe that
// did not resolve), missing_repos names every repo the claim will clone
// whose clone is missing from the mapped workspace, sorted (absent when
// every clone is present or the check was skipped). The shape is
// replaced in place — no dual shape (docs/upgrade.md notes the change
// for external parsers).
type SubmitResponse struct {
	Accepted     bool     `json:"accepted"`
	WorkItemID   string   `json:"work_item_id"`
	State        string   `json:"state,omitempty"`
	QueuedReason string   `json:"queued_reason,omitempty"`
	MachineID    string   `json:"machine_id,omitempty"`
	CapsulePath  string   `json:"capsule_path,omitempty"`
	MissingRepos []string `json:"missing_repos,omitempty"`
}

// The SubmitResponse states.
const (
	SubmitStateClaimed = "claimed"
	SubmitStateQueued  = "queued"
)

// StatusResponse is the GET /status answer: local bookkeeping + auto switch +
// queue counters. It is NOT workflow execution state (INV-1) — just
// "what this machine is managing". The daemon serves this very type.
type StatusResponse struct {
	MachineID string `json:"machine_id"`
	// Version is the running daemon's own build version:
	// after an install swapped the binary, a daemon still on the old one
	// answers its old version here. Absent on an older daemon.
	Version string `json:"version,omitempty"`
	// DisplayName is the display-only daemon name (default: hostname).
	DisplayName string `json:"display_name,omitempty"`
	// Auto is the auto-pull switch (v2-converge §5); the max-jobs number
	// rides queue.max_concurrent.
	Auto bool `json:"auto"`
	// Workspaces is the mapped workspace set (#113): every [workspace]
	// path the daemon routes capsules into, in mapping declaration
	// order — a multi-workspace daemon never presents one workspace as
	// if it were the only one.
	Workspaces []string `json:"workspaces"`
	// WorkflowsDir is the absolute workflows dir the daemon runs bundles
	// from (pf doctor judges the workspace yaml's step-id keys by it).
	WorkflowsDir string `json:"workflows_dir,omitempty"`
	// Harnesses is the daemon's view of the step harness binaries
	// (polyforge#159): found, version, credential variables set/unset —
	// never a value. Only on GET /status?harnesses=1 (it may run
	// `claude --version`); pf doctor judges the claude harness by it.
	Harnesses    map[string]HarnessStatus `json:"harnesses,omitempty"`
	ShuttingDown bool                     `json:"shutting_down"`
	// EnvWarnings: retired POLYFORGE_* variables the daemon found set to
	// a differing value at boot (no longer read; pf doctor relays them).
	EnvWarnings []string `json:"env_warnings,omitempty"`
	// EnvNames: the NAMES (never values) of the daemon's environment,
	// sorted (env hygiene; pf doctor's "daemon env" check flags
	// credential-looking names outside the ensure allow-list).
	EnvNames []string `json:"env_names,omitempty"`
	// PiModelsEnvRefs: the variable NAMES the step agent dir's
	// models.json interpolates (`$NAME` / `${NAME}`); pf doctor warns on
	// those no ensure-started daemon carries.
	PiModelsEnvRefs []string `json:"pi_models_env_refs,omitempty"`
	// CommandPath is the PATH the daemon's in-process commands (its git
	// and gh) run under: the live [daemon] command_path, else the daemon
	// process's own PATH. A PATH is not a credential.
	CommandPath string `json:"command_path,omitempty"`
	// StepPath is the PATH a step process starts with, before its harness
	// binary's directory: with command_path set, the daemon's pf directory
	// and then command_path (each entry once); unset, the daemon process's
	// own PATH.
	StepPath string      `json:"step_path,omitempty"`
	Jobs     []StatusJob `json:"jobs"`
	Queue    QueueStats  `json:"queue"`
	// ModelHealth is the additive model-health summary:
	// the window and the currently firing warnings, never the full table
	// (that stays `pf ops models`). Absent on an older daemon.
	ModelHealth *modelhealth.Summary `json:"model_health,omitempty"`
	// Supervision is the running daemon's live supervision bounds
	//: T (`step_timeout`) and N (`stall_window`) as Go
	// duration strings. It makes a live reload of those keys observable
	// without a dispatch. Absent on an older daemon.
	Supervision *SupervisionStatus `json:"supervision,omitempty"`
	// Reload is the cli.toml hot-reload summary: keys
	// pending a restart, keys overridden by an explicit daemon flag, and
	// the last reload's per-key outcomes. Absent on an older daemon.
	Reload *ReloadStatus `json:"reload,omitempty"`
	// MachineFace is the cross-machine face state:
	// availability, negotiated role, supersession. Additive.
	MachineFace map[string]any `json:"machine_face,omitempty"`
	// Tasks is the one-shot task agent summary: running
	// count, the fixed limit and the terminal-state counts. Tasks run
	// outside the job slots, so queue.running never counts them. Absent on
	// an older daemon.
	Tasks *task.Summary `json:"tasks,omitempty"`
	// BackgroundModelCalls is the Host's one bound over background model
	// calls (every Cell's task lane): {bound, running}. Host aggregate
	// only; absent on an older daemon.
	BackgroundModelCalls *BackgroundCalls `json:"background_model_calls,omitempty"`
	// ExecutionDisabled names why this Cell's execution plane is closed
	// ("" = open): no workspace configured, no model-credential source.
	ExecutionDisabled string `json:"execution_disabled,omitempty"`
	// Cells is the Host aggregate's per-Cell block (GET /status without a
	// profile): one GET /profiles row per Cell. Absent on a Cell's own
	// status.
	Cells []ProfileInfo `json:"cells,omitempty"`
}

// SupervisionStatus is the live supervision bounds GET /status carries
// (; 0 = unset, the documented default applies).
type SupervisionStatus struct {
	StepTimeout string `json:"step_timeout"`
	StallWindow string `json:"stall_window"`
}

// HarnessStatus is one harness's daemon-side status: binary, version,
// and whether the credential variables are set ("set"/"unset" — never a
// value). The claude entry also carries its harness-root layout
// (polyforge#171: the three dirs) and the claude-config settings.json env
// block facts (key → set/unset, never values) plus its pairing warnings.
type HarnessStatus struct {
	Bin             string                `json:"bin"`
	Found           bool                  `json:"found"`
	Path            string                `json:"path,omitempty"`
	Version         string                `json:"version,omitempty"`
	Verified        bool                  `json:"verified,omitempty"`
	MinVersion      string                `json:"min_version,omitempty"`
	VersionError    string                `json:"version_error,omitempty"`
	AuthTokenEnv    string                `json:"auth_token_env,omitempty"`
	APIKeyEnv       string                `json:"api_key_env,omitempty"`
	BaseURLEnv      string                `json:"base_url_env,omitempty"`
	SettingsEnv     map[string]string     `json:"settings_env,omitempty"`
	HarnessDirs     map[string]HarnessDir `json:"harness_dirs,omitempty"`
	SettingsWarning string                `json:"settings_warning,omitempty"`
	BackgroundTasks string                `json:"background_tasks,omitempty"`
}

// HarnessDir is one harness dir's existence fact under the harness root
// (polyforge#171: step_agent / step_agent_sessions / claude_config).
type HarnessDir struct {
	Path   string `json:"path,omitempty"`
	Exists bool   `json:"exists"`
}

// StatusJob is one jobs[] bookkeeping entry.
type StatusJob struct {
	WorkItemID string `json:"work_item_id"`
	// Profile is the Cell holding the job (the Host aggregate's rows; a
	// work reference is (profile, work_item_id) — equal ids on two Cells
	// are two jobs).
	Profile string `json:"profile,omitempty"`
	// Slug is the "<project>#<seq>" display name (absent on a job record
	// that predates it: the WORK ITEM column then shows the id).
	Slug   string `json:"slug,omitempty"`
	Status string `json:"status"`
	// SpawnN is the v1.5 B2 derivation counter; SessionFingerprint is
	// the PUBLIC session handle (B3) — the session model's identity
	// pair.
	SpawnN             int    `json:"spawn_n,omitempty"`
	SessionFingerprint string `json:"session_fingerprint,omitempty"`
	AgentPID           int    `json:"agent_pid,omitempty"`
	// NeedsInput/QuestionID carry the D6 owner face: the monitor's
	// pending-question projection merged over the runtime state machine
	// (a job the runtime calls running but the monitor holds a pending
	// question for is presented as needs_input — an agent blocked on a
	// human must never read as merely busy).
	NeedsInput bool   `json:"needs_input,omitempty"`
	QuestionID string `json:"question_id,omitempty"`
	// StepAgeSec / LastWorkAgoSec are the owner liveness face: whole
	// seconds since the current dispatch began and since its harness last
	// did REAL work (nil = none — rendered "-" in the STEP-AGE and
	// LAST-WORK columns). UI-noise records never refresh the work clock,
	// so a wedged-but-chattering job reads as stale, not fresh.
	StepAgeSec     *int64 `json:"step_age_sec,omitempty"`
	LastWorkAgoSec *int64 `json:"last_work_ago_sec,omitempty"`
	// Candidate is the in-flight dispatch's `<harness>:<model>[:<effort>]`
	// (polyforge#176; absent = none in flight or the harness default).
	Candidate string `json:"candidate,omitempty"`
	// WaitCalls / WaitSeconds are the in-flight step's wait calls — bash
	// calls that only `sleep N`, N > 3s — and their summed seconds, over
	// every dispatch of the step this daemon watched: counted and
	// surfaced, never judged; nil = none.
	WaitCalls   *int64 `json:"wait_calls,omitempty"`
	WaitSeconds *int64 `json:"wait_seconds,omitempty"`
	// Context is the in-flight step session's context-window usage
	//; absent outside a dispatch or when unknown.
	Context *protocol.ContextUsage `json:"context,omitempty"`
	// RetryInSec / RetryCause: the current step waits a decided retry
	// backoff before its next dispatch — whole seconds left (rounded up)
	// and the death cause that owes it (absent = no wait owed). Rendered
	// "retry in 3m: upstream_transient" in the RETRY column.
	RetryInSec *int64 `json:"retry_in_sec,omitempty"`
	RetryCause string `json:"retry_cause,omitempty"`
	// VerifyQueue: the job's daemon-run verification waits in the
	// heavy-run admission queue position, the holding
	// gate, since. Rendered "queued #2: <reason>" in the VERIFY column.
	VerifyQueue *protocol.WatchVerifyQueue `json:"verify_queue,omitempty"`
	// Origin is how the job was submitted (may be absent).
	Origin *protocol.Origin `json:"origin,omitempty"`
}

// QueueStats is the local job queue counters.
type QueueStats struct {
	Pending       int `json:"pending"`
	Running       int `json:"running"`
	MaxConcurrent int `json:"max_concurrent"`
	// MaxConfigured is the daemon's last APPLIED/startup cap:
	// `pf ops max` changes only MaxConcurrent, so a differing pair tells the
	// panel that a temporary ops override is in effect rather than a stale
	// file value. Additive and read-only; absent on an older daemon.
	MaxConfigured int `json:"max_configured,omitempty"`
}

// ---------------------------------------------------------------------------
// Routes.
// ---------------------------------------------------------------------------

// ClaimWithModels submits a single-WI interactive claim request carrying
// the manual `--models` candidate list (nil keeps the default). priority
// defaults to "normal" server-side; empty keeps the default.
func (d *DaemonClient) ClaimWithModels(wiID, priority string, models []string) (json.RawMessage, error) {
	if priority == "" {
		priority = "normal"
	}
	body := map[string]any{"priority": priority}
	if len(models) > 0 {
		body["models"] = models
	}
	return d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/claim", "", d.withOrigin(body), nil)
}

// ResumeBlocked is the daemon half of the §6.4 owner move for a workflow
// parked blocked (polyforge#116), after the CLI landed the hub's
// resume-blocked door: the claim runs synchronously and re-establishes
// execution over the parked capsule — the claim/start path's owner move
// unblocks the workflow and the loop re-dispatches the frozen step in
// place as a fresh derivation.
func (d *DaemonClient) ResumeBlocked(wiID string) (json.RawMessage, error) {
	return d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/claim", "", d.withOrigin(map[string]any{"resume": true}), nil)
}

// ResumePaused is the live-holder half of `pf resume` on a paused work
// item (wi_seWTgHeKtaa8, v2-converge §7): the CLI has already cleared
// clean_pause at the hub; this asks the LOCAL daemon — when it is the
// paused work item's holder — to re-validate the session and continue
// the current step IN PLACE (SessionAttach + the recovery continuation,
// the same semantics attaching after a restart has). A daemon that does
// not hold the work item answers DAEMON-404; a live holder whose hub
// flag was not cleared yet answers DAEMON-409.
type ResumePausedResponse struct {
	Resumed    bool   `json:"resumed"`
	WorkItemID string `json:"work_item_id"`
	// Outcome: attached (continued in place) | already_live | closed
	// (re-attached, but the recovery closed the job) | parked
	// (re-attached, but the recovery waits on the previous driver Run).
	Outcome string `json:"outcome,omitempty"`
}

func (d *DaemonClient) ResumePaused(wiID string) (*ResumePausedResponse, error) {
	raw, err := d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/resume-paused", "", d.withOrigin(map[string]any{}), nil)
	if err != nil {
		return nil, err
	}
	var out ResumePausedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode the resume-paused answer: %w", err)
	}
	return &out, nil
}

// Answer relays the owner's words to the work item's current step agent.
// The relay message is the request body itself; the caller identity
// travels in the X-PF-User header (the daemon only uses it to fill an
// empty answered_by; it does not cross-check the two).
func (d *DaemonClient) Answer(wiID string, payload AnswerPayload) (json.RawMessage, error) {
	if payload.Wake != "" && payload.Origin == nil {
		payload.Origin = d.Origin // the wake claims the work item, as a resume does
	}
	return d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/answer", "", payload,
		map[string]string{"X-PF-User": payload.AnsweredBy})
}

// Pause asks the daemon to pause a running WI (no body: the daemon's close
// path carries the clean_pause semantics itself).
func (d *DaemonClient) Pause(wiID string) (json.RawMessage, error) {
	return d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/pause", "", nil, nil)
}

// Wrap asks the daemon to wrap (or fail, when reason=="fail") a running
// WI — same final-flush requirement as pause.
func (d *DaemonClient) Wrap(wiID, reason string) (json.RawMessage, error) {
	if reason == "" {
		reason = "wrap"
	}
	return d.do("POST", "/jobs/"+url.PathEscape(wiID)+"/wrap", "",
		map[string]any{"reason": reason}, nil)
}

// Status returns the local jobs bookkeeping.
func (d *DaemonClient) Status() (json.RawMessage, error) {
	return d.do("GET", "/status", "", nil, nil)
}

// Models reads the read-only model-health profile. The
// raw answer is re-emitted unchanged by `pf ops models --json`.
func (d *DaemonClient) Models() (json.RawMessage, error) {
	return d.do("GET", "/models", "", nil, nil)
}

// StatusWithHarnesses is GET /status?harnesses=1: the status plus the
// daemon's view of the step harness binaries (polyforge#159; pf doctor).
func (d *DaemonClient) StatusWithHarnesses() (json.RawMessage, error) {
	return d.do("GET", "/status", "harnesses=1", nil, nil)
}

// OpsConfig issues one POST /ops/config operation.
func (d *DaemonClient) OpsConfig(req OpsConfigRequest) (json.RawMessage, error) {
	return d.do("POST", "/ops/config", "", req, nil)
}

// ConfigReload asks the daemon to re-read its cli.toml and apply the
// reloadable keys (POST /config/reload). A typed refusal is an
// *api.Error; a dial failure means no daemon is running.
func (d *DaemonClient) ConfigReload() (*ConfigReloadResponse, error) {
	raw, err := d.do("POST", "/config/reload", "", map[string]any{}, nil)
	if err != nil {
		return nil, err
	}
	var resp ConfigReloadResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("config reload: undecodable answer: %w", err)
	}
	return &resp, nil
}

// ConfigCheck asks the daemon whether a proposed mapping may be applied
// without stranding a held job (POST /config/check).
func (d *DaemonClient) ConfigCheck(entries []ConfigMappingEntry) (*ConfigCheckResponse, error) {
	raw, err := d.do("POST", "/config/check", "", map[string]any{"mapping": entries}, nil)
	if err != nil {
		return nil, err
	}
	var resp ConfigCheckResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("config check: undecodable answer: %w", err)
	}
	return &resp, nil
}

// Shutdown triggers graceful shutdown (quiesce).
func (d *DaemonClient) Shutdown() (json.RawMessage, error) {
	return d.do("POST", "/shutdown", "", map[string]any{}, nil)
}

// ---------------------------------------------------------------------------
// pf watch lane routes.
// ---------------------------------------------------------------------------

// WatchSnapshot reads one WI's authoritative state plus the events after
// the cursor (first call: 0). demand marks the request as human-side
// demand for the summarize lane (polyforge#224): the daemon's route
// carries it into the lane's demand gate (the poll arms the narration);
// the resident watcher's polls never set it.
func (d *DaemonClient) WatchSnapshot(wiID string, after int64, demand bool) (*protocol.WatchSnapshot, error) {
	query := "after=" + strconv.FormatInt(after, 10)
	if demand {
		query += "&demand=1"
	}
	raw, err := d.do("GET", "/jobs/"+url.PathEscape(wiID)+"/watch", query, nil, nil)
	if err != nil {
		return nil, err
	}
	var s protocol.WatchSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode the watch snapshot: %w", err)
	}
	return &s, nil
}

// Decisions reads the machine's decision feed (GET /decisions).
func (d *DaemonClient) Decisions() (json.RawMessage, error) {
	return d.do("GET", "/decisions", "", nil, nil)
}

// DismissDecisions marks feed entries dismissed (the ids, or every open
// one).
func (d *DaemonClient) DismissDecisions(ids []int64, all bool) (json.RawMessage, error) {
	return d.do("POST", "/decisions/dismiss", "", protocol.DecisionDismiss{IDs: ids, All: all}, nil)
}

// WatchArtifact reads one capsule artifact through the daemon's fixed
// decision chain (local holder or signed remote relay).
func (d *DaemonClient) WatchArtifact(wi, relpath string) (json.RawMessage, error) {
	return d.do("GET", "/jobs/"+url.PathEscape(wi)+"/artifact", "path="+url.QueryEscape(relpath), nil, nil)
}

// analyzeClientTimeout bounds the one analyze (and prompt) call: the
// daemon blocks until the task is terminal (its wait bound is 6 minutes), so
// this call outlasts the client's global 120 s bound — every other verb
// keeps that one.
const analyzeClientTimeout = 6*time.Minute + 30*time.Second

// AnalyzeMessage asks the local daemon for the read-only analysis of one
// message (human-triggered only; never automatic) and blocks until the
// analysis task ends. callerID is the CLI's own user id (the task's
// reporter); empty when it has none.
func (d *DaemonClient) AnalyzeMessage(id, callerID string) (json.RawMessage, error) {
	return d.messageTask(id, "analyze", callerID)
}

// PromptMessage asks the local daemon to turn one message into an
// execution prompt (human-triggered only; one read-only task, the same
// wait bound as analyze) and blocks until that task ends. callerID is the
// CLI's own user id (the task's reporter); empty when it has none.
func (d *DaemonClient) PromptMessage(id, callerID string) (json.RawMessage, error) {
	return d.messageTask(id, "prompt", callerID)
}

// messageTask is the one blocking POST /messages/{id}/<action> call, under
// analyzeClientTimeout instead of the global bound.
func (d *DaemonClient) messageTask(id, action, callerID string) (json.RawMessage, error) {
	c := *d
	hc := http.Client{Timeout: analyzeClientTimeout}
	if d.HTTP != nil {
		hc.Transport = d.HTTP.Transport
	}
	c.HTTP = &hc
	body := map[string]any{}
	if callerID != "" {
		body["caller_id"] = callerID
	}
	return c.do("POST", "/messages/"+url.PathEscape(id)+"/"+action, "", body, nil)
}

// ProfilesResponse is the GET /profiles answer: the Host's Cell registry
// and its EFFECTIVE workspace index (what the Host actually serves — a
// pending change's newer disk mapping is not in it). The CLI resolver's cwd
// match reads Index; a file on disk never pre-routes business.
type ProfilesResponse struct {
	// Dir is the configuration directory the Host loaded its files from.
	Dir      string               `json:"dir"`
	Profiles []ProfileInfo        `json:"profiles"`
	Index    []cellcfg.IndexEntry `json:"index"`
}

// ProfileInfo is one Cell of the Host registry. Credentials are never
// carried; the server is the normalized spelling.
type ProfileInfo struct {
	Profile string `json:"profile"`
	File    string `json:"file"`
	// State: serving | degraded | invalid | removed | pending.
	State string `json:"state"`
	Valid bool   `json:"valid"`
	// Reason names why the Cell is invalid or degraded ("" otherwise).
	Reason     string   `json:"reason,omitempty"`
	Server     string   `json:"server,omitempty"`
	Workspaces []string `json:"workspaces"`
	DataDir    string   `json:"data_dir,omitempty"`
	MachineID  string   `json:"machine_id,omitempty"`
	// Heartbeat is the Cell's last heartbeat outcome ("ok", an error, or ""
	// before the first beat).
	Heartbeat string `json:"heartbeat,omitempty"`
	// ExecutionDisabled names why the Cell's execution plane is closed
	// ("" = open): no workspace configured, no model-credential source.
	ExecutionDisabled string `json:"execution_disabled,omitempty"`
	// CellMaxConcurrentJobs is the persisted optional Cell ceiling (0 =
	// none); MaxConcurrent the effective one (0 = only the Host ceiling).
	CellMaxConcurrentJobs int `json:"cell_max_concurrent_jobs,omitempty"`
	MaxConcurrent         int `json:"max_concurrent,omitempty"`
	Running               int `json:"running"`
	Pending               int `json:"pending"`
	// Draining: a connection change waits for the Cell to go idle;
	// PendingChange names it — always present on the row, "" once no
	// change is pending (the documented row shape), so a reader can watch
	// the switch complete.
	Draining      bool   `json:"draining,omitempty"`
	PendingChange string `json:"pending_change"`
}

// BackgroundCalls is the Host's background-model-call bound and use.
type BackgroundCalls struct {
	Bound   int `json:"bound"`
	Running int `json:"running"`
}

// Profiles reads the Host's Cell registry (GET /profiles).
func (d *DaemonClient) Profiles() (*ProfilesResponse, error) {
	raw, err := d.do("GET", "/profiles", "", nil, nil)
	if err != nil {
		return nil, err
	}
	var out ProfilesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("GET /profiles: unreadable answer: %w", err)
	}
	return &out, nil
}
