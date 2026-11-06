package daemon

// stepcall.go: `pf step`, a step's one entry point (workflow contract 4,
// design #483 §5; this is its first stage C1, — served, not
// yet read by the driver). Every subcommand arrives here as one call:
//
//	POST /step/call   {verb, args}
//
// verb: handoff | ask | recall | memory.show | remember | hub.<verb>.
//
// The call is appended to the capsule's call ledger
// (runtime-state/step-calls.jsonl, stepcall package) BEFORE it runs — a
// ledger that cannot be written runs nothing — and what it came to (ok,
// refused, failed; an accepted hand-off's record; the hub writes recorded
// pending) is appended after it, before the answer leaves: the ledger is
// the record of the step's calls, so a result it does not carry counts as
// not done.
//
// Reach: step processes of this daemon only. stepCaller resolves the
// caller from its process tree (peerStepOrigin: the spawn journal's live
// step processes among its ancestors, never anything from the request)
// and refuses every other caller DAEMON-403; the step-origin guard lets
// this POST through, the one step write (routes.go); a
// task agent's process tree is refused before that (taskOriginRefusal).
// The calls reach aihub only through the daemon's own client, scoped to
// the calling step's project; a step holds no aihub credential.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

const stepCallPath = "/step/call"

// maxStepCallBytes bounds one call's request.
const maxStepCallBytes = 1 << 20

// stepCallRequest is one `pf step` call.
type stepCallRequest struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args"`
}

// stepCall is one call being served: the caller's job, its ledger call
// line and the execution record read for it, and what the verb leaves
// for the result line (set only on success).
type stepCall struct {
	d    *Daemon
	j    stepJob
	call stepcall.Line
	ex   *capsule.Execution
	// handoff is an accepted hand-off's record; pending the hub writes
	// recorded pending.
	handoff *stepcall.Handoff
	pending []stepcall.Op
}

// stepCallRoute records, serves and settles one call.
func (d *Daemon) stepCallRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxStepCallBytes+1))
	if err != nil {
		writeErr(w, api.ErrDaemonBadInput, "body read: "+err.Error())
		return
	}
	var req stepCallRequest
	var reqErr error
	if len(raw) > maxStepCallBytes {
		reqErr = fmt.Errorf("the call is over the %d-byte bound: put the detail into files and name their paths", maxStepCallBytes)
	} else if err := json.Unmarshal(raw, &req); err != nil {
		reqErr = fmt.Errorf("the call is not a valid {verb, args} JSON request: %v", err)
	}
	// A checking call (a hand-off, an ask, a hub write) reads the record it
	// then appends to: it holds the capsule's step-call lock from its call
	// line through its result line, so a second call of the dispatch sees
	// the first one's result (the once-only hand-off, the key a pending
	// file holds, the writes refused after the hand-off).
	if reqErr == nil && stepCallChecks(req.Verb) {
		defer lockStepCalls(j.capsuleDir)()
	}
	ex := readExecutionFile(filepath.Join(j.capsuleDir, capsule.ExecutionJSON))
	line := stepcall.Line{Kind: stepcall.KindCall, Step: j.step, Round: stepRound(ex, j.step), Spawn: int64(j.attempt), Verb: req.Verb}
	if reqErr == nil && len(req.Args) > 0 && json.Valid(req.Args) {
		line.Args = req.Args
	}
	call, err := stepcall.Append(j.capsuleDir, line)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "the call was not run: it could not be recorded in the call ledger: "+err.Error())
		return
	}
	sc := &stepCall{d: d, j: j, call: call, ex: ex}
	buf := &bufferedResponse{header: http.Header{}}
	if reqErr != nil {
		writeErr(buf, api.ErrDaemonBadInput, reqErr.Error())
	} else {
		sc.serve(buf, req)
	}
	res := stepcall.Line{Kind: stepcall.KindResult, Step: call.Step, Round: call.Round, Spawn: call.Spawn, Verb: call.Verb, Call: call.Seq}
	switch {
	case buf.status() < 300:
		res.Outcome, res.Handoff, res.Pending = stepcall.OutcomeOK, sc.handoff, sc.pending
	case buf.status() < 500:
		res.Outcome = stepcall.OutcomeRefused
	default:
		res.Outcome = stepcall.OutcomeFailed
	}
	if res.Outcome != stepcall.OutcomeOK {
		var ae api.Error
		if json.Unmarshal(buf.body.Bytes(), &ae) == nil {
			res.Code, res.Error = ae.Code, ae.Detail
		}
	}
	if _, err := stepcall.Append(j.capsuleDir, res); err != nil {
		writeErr(w, api.ErrDaemonInternal, fmt.Sprintf("call %d ran, but what it came to could not be recorded in the call ledger, so it counts as not done: %v", call.Seq, err))
		return
	}
	buf.flushTo(w)
}

// serve runs the call's verb, answering on w.
func (sc *stepCall) serve(w http.ResponseWriter, req stepCallRequest) {
	switch {
	case req.Verb == stepcall.VerbHandoff:
		sc.handoffVerb(w, req.Args)
	case req.Verb == stepcall.VerbAsk:
		sc.askVerb(w, req.Args)
	case req.Verb == stepcall.VerbRecall:
		sc.recallVerb(w, req.Args)
	case req.Verb == stepcall.VerbMemoryShow:
		var a struct {
			ID string `json:"id"`
		}
		if !decodeArgs(w, req.Args, &a) {
			return
		}
		if strings.TrimSpace(a.ID) == "" {
			writeErr(w, api.ErrDaemonBadInput, "memory show: the memory id is required")
			return
		}
		sc.d.forwardMemoryRead(w, a.ID, sc.j.project)
	case req.Verb == stepcall.VerbRemember:
		var a stepRememberRequest
		if !decodeArgs(w, req.Args, &a) {
			return
		}
		sc.d.stepRememberAs(w, sc.j, a)
	case strings.HasPrefix(req.Verb, stepcall.VerbHubPrefix):
		sc.hubVerb(w, strings.TrimPrefix(req.Verb, stepcall.VerbHubPrefix), req.Args)
	default:
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("no pf step verb %q (the verbs: handoff, ask, recall, memory.show, remember, hub.<verb>)", req.Verb))
	}
}

// askVerb is `pf step ask`: the dispatch's question to the owner,
// appended to the capsule's asks.jsonl under the dispatch the daemon
// resolved (the driver parks the step on it as on `pf ask`). A second
// question while one of the dispatch is open is refused.
func (sc *stepCall) askVerb(w http.ResponseWriter, raw json.RawMessage) {
	var a struct {
		Text string `json:"text"`
	}
	if !decodeArgs(w, raw, &a) {
		return
	}
	text := strings.TrimSpace(a.Text)
	switch {
	case text == "":
		writeErr(w, api.ErrDaemonBadInput, "ask: the question is empty")
		return
	case len(text) > capsule.MaxAskBytes:
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("ask: the question is %d bytes, over the %d-byte bound: ask the one thing you need and put the detail in an artifact", len(text), capsule.MaxAskBytes))
		return
	case !utf8.ValidString(text):
		writeErr(w, api.ErrDaemonBadInput, "ask: the question is not valid UTF-8")
		return
	}
	lines, err := capsule.ReadAsks(sc.j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "ask: "+err.Error())
		return
	}
	if open := capsule.OpenAsk(lines, sc.j.step, int64(sc.j.attempt)); open != nil {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("ask: this dispatch already has an open question (asked %s): end your turn so the owner can answer it; ask again only after the reply", open.At))
		return
	}
	line := capsule.AskLine{At: time.Now().UTC().Format(time.RFC3339Nano), Step: sc.j.step, Spawn: int64(sc.j.attempt), Kind: capsule.AskKindAsk, Text: text}
	if err := capsule.AppendAsk(sc.j.capsuleDir, line); err != nil {
		writeErr(w, api.ErrDaemonInternal, "ask: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, line)
}

// recallVerb is `pf step recall`: one search of the calling step's
// project (forwardMemorySearchQuery: search only, the project fixed).
func (sc *stepCall) recallVerb(w http.ResponseWriter, raw json.RawMessage) {
	var a struct {
		Q     string   `json:"q"`
		Type  string   `json:"type"`
		Tags  []string `json:"tags"`
		Limit int      `json:"limit"`
		Match string   `json:"match"`
	}
	if !decodeArgs(w, raw, &a) {
		return
	}
	q := url.Values{"q": {a.Q}}
	if a.Type != "" {
		q.Set("type", a.Type)
	}
	if len(a.Tags) > 0 {
		q.Set("tags", strings.Join(a.Tags, ","))
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	if a.Match != "" {
		q.Set("match", a.Match)
	}
	sc.d.forwardMemorySearchQuery(w, q, sc.j.project, "a step of "+sc.j.wi)
}

// decodeArgs decodes a call's args strictly into v (an unknown key is a
// refusal), or writes the refusal and answers false.
func decodeArgs(w http.ResponseWriter, raw json.RawMessage, v any) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, api.ErrDaemonBadInput, "the call's args: "+err.Error())
		return false
	}
	return true
}

// stepRound answers step's round on the execution record (0 when none).
func stepRound(ex *capsule.Execution, step string) int64 {
	if ex == nil || ex.Steps[step] == nil {
		return 0
	}
	return ex.Steps[step].Round
}

// stepCallChecks reports whether verb is a checking call: one that reads
// the capsule's record and then appends to it (stepCallRoute holds the
// capsule's step-call lock across it). The reads (recall, memory show,
// remember, hub get and list) take no lock: a slow aihub never holds a
// hand-off.
func stepCallChecks(verb string) bool {
	if hub, ok := strings.CutPrefix(verb, stepcall.VerbHubPrefix); ok {
		return stepcall.IsHubWrite(hub)
	}
	return verb == stepcall.VerbHandoff || verb == stepcall.VerbAsk
}

// stepCallLocks serializes the checking calls of one capsule, each from
// its call line through its result line (stepCallRoute). Held across that
// capsule's file I/O only — never the job table's lock.
var stepCallLocks sync.Map

// lockStepCalls locks capsuleDir's checking calls and answers the unlock.
func lockStepCalls(capsuleDir string) func() {
	m, _ := stepCallLocks.LoadOrStore(capsuleDir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// bufferedResponse holds a verb's answer until the call's result line is
// on the ledger.
type bufferedResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}

// status answers the answer's status (200 when none was written).
func (b *bufferedResponse) status() int {
	if b.code == 0 {
		return http.StatusOK
	}
	return b.code
}

// flushTo writes the held answer to w.
func (b *bufferedResponse) flushTo(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	w.WriteHeader(b.status())
	_, _ = w.Write(b.body.Bytes())
}
