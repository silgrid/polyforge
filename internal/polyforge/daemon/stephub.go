package daemon

// stephub.go: `pf step hub <verb>` (workflow contract 4, design #483 §5.2;
// stage C1,) — a step's reads and writes of aihub work
// items, through the daemon's own client and only aihub's existing routes:
//
//   - get <wi> and list (project, status, bundle, attrs filters, limit)
//     are read through at once (GET /work-items/{id}, GET /work-items with
//     attr=<path>:<value> per filter), scoped to the calling step's
//     project;
//   - file, attrs, wrap-attrs and batch (several of them, in order) are
//     never written when called: the call's result line records them
//     pending, and the driver has the pending-writes executor make a step
//     round's writes once it hands off done, through stepHubSink (the
//     daemon's binding of it), and drops them on any other status. An
//     empty batch is a recorded no-op (an ok result recording nothing), so
//     a skill registers a command's ops file whatever it holds;
//   - an attrs write names one top-level attrs key, which must be on the
//     step's `hub.attrs` whitelist; one carrying out the owner's word names
//     the owner message (per_owner, its stamp) — an owner message to this
//     step in its current round, checked as a hand-off's --per-owner is —
//     and the daemon copies that message's words, writer and stamp
//     verbatim as the write's owner_message, which aihub records with the
//     write. The daemon never reads the words, nor the value written.
//
// Which verbs a step may call, and which attrs keys it may write, is the
// step's whitelist (Daemon.stepHubAllow / stepHubAttrs, laneHubAllow /
// laneHubAttrs: the lane's `hub:` verbs, plus wrap-attrs for `wrap_attrs`
// and attrs for `hub.attrs`), not its lane's kind: anything outside it is
// refused. Reach: step processes of this daemon only (stepcall.go); a step
// reaches aihub's attrs route (PATCH /work-items/{id}/attrs) only through
// this proxy — the pending write the daemon makes on its own signed key.
// Security note (soft policy, one line): the proxy gives a step indirect
// aihub write capability, bounded by the per-step whitelist.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// stepHubVerbs answers the hub verbs the dispatch j may call.
func (d *Daemon) stepHubVerbs(j stepJob) map[string]bool {
	out := map[string]bool{}
	if d.stepHubAllow == nil {
		return out
	}
	for _, v := range d.stepHubAllow(j) {
		out[v] = true
	}
	return out
}

// laneHubAllow is the production whitelist: the calling step's
// `hub.verbs` in the capsule's lane snapshot, plus wrap-attrs when it
// declares `wrap_attrs` and attrs when its `hub.attrs` names a key
// (workflow contracts 4 and 5); only the step graph is read
// (capsuleLaneGraph). An unreadable workflow.yaml allows nothing.
func laneHubAllow(j stepJob) []string {
	return capsuleLaneGraph(j.capsuleDir).HubAllowed(j.step)
}

// laneHubAttrs is the production attrs whitelist: the calling step's
// `hub.attrs` in the capsule's lane snapshot (nothing when unreadable).
func laneHubAttrs(j stepJob) []string {
	return capsuleLaneGraph(j.capsuleDir).HubAttrs(j.step)
}

// stepHubKeys answers the attrs keys the dispatch j may write.
func (d *Daemon) stepHubKeys(j stepJob) map[string]bool {
	out := map[string]bool{}
	if d.stepHubAttrs == nil {
		return out
	}
	for _, k := range d.stepHubAttrs(j) {
		out[k] = true
	}
	return out
}

// hubVerb serves one `pf step hub` call.
func (sc *stepCall) hubVerb(w http.ResponseWriter, verb string, raw json.RawMessage) {
	allowed := sc.d.stepHubVerbs(sc.j)
	known := false
	for _, v := range stepcall.HubVerbs {
		known = known || v == verb
	}
	switch {
	case !known:
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("no hub verb %q (the verbs: %s)", verb, strings.Join(stepcall.HubVerbs, ", ")))
		return
	case !allowed[verb]:
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("pf step hub %s refused: step %s may not call it (the step's hub whitelist: %s)", verb, sc.j.step, verbList(allowed)))
		return
	}
	switch verb {
	case stepcall.HubGet:
		sc.hubGet(w, raw)
	case stepcall.HubList:
		sc.hubList(w, raw)
	case stepcall.HubBatch:
		var a struct {
			Ops []stepcall.Op `json:"ops"`
		}
		if !decodeArgs(w, raw, &a) {
			return
		}
		sc.recordWrites(w, a.Ops, allowed) // none: a recorded no-op
	default:
		var op stepcall.Op
		if !decodeArgs(w, raw, &op) {
			return
		}
		op.Verb = verb
		sc.recordWrites(w, []stepcall.Op{op}, allowed)
	}
}

// verbList renders a whitelist ("none" when empty).
func verbList(allowed map[string]bool) string {
	var out []string
	for _, v := range stepcall.HubVerbs {
		if allowed[v] {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return fmt.Sprint(out)
}

// hubGet reads one work item of the calling step's project (another
// project's answers not found, whatever aihub said).
func (sc *stepCall) hubGet(w http.ResponseWriter, raw json.RawMessage) {
	var a struct {
		WI string `json:"wi"`
	}
	if !decodeArgs(w, raw, &a) {
		return
	}
	if a.WI == "" {
		writeErr(w, api.ErrDaemonBadInput, "get: the work item is required")
		return
	}
	var item json.RawMessage
	if err := sc.d.hub.Do("GET", "/api/v1/work-items/"+url.PathEscape(a.WI), nil, &item); err != nil {
		writeHubErr(w, err)
		return
	}
	var it struct {
		Project string `json:"project"`
	}
	if json.Unmarshal(item, &it) != nil || it.Project != sc.j.project {
		writeErr(w, api.ErrDaemonNotFound, fmt.Sprintf("get: work item %s not found in project %s", a.WI, sc.j.project))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(item)
}

// hubList lists the calling step's project's work items with aihub's
// filters: each attrs filter `<path>=<value>` is passed on as aihub's
// attr=<path>:<value> (aihub judges the path).
func (sc *stepCall) hubList(w http.ResponseWriter, raw json.RawMessage) {
	var a struct {
		Project string   `json:"project"`
		Status  string   `json:"status"`
		Bundle  string   `json:"bundle"`
		Attrs   []string `json:"attrs"`
		Limit   int      `json:"limit"`
	}
	if !decodeArgs(w, raw, &a) {
		return
	}
	if a.Project != "" && a.Project != sc.j.project {
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("list: a step of %s lists project %q only (asked for %q)", sc.j.wi, sc.j.project, a.Project))
		return
	}
	q := url.Values{"project": {sc.j.project}}
	for k, v := range map[string]string{"status": a.Status, "bundle": a.Bundle} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for _, f := range a.Attrs {
		path, value, ok := strings.Cut(f, "=")
		if !ok || path == "" {
			writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("list: --attr %q is not <path>=<value>", f))
			return
		}
		q.Add("attr", path+":"+value)
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	var page json.RawMessage
	if err := sc.d.hub.Do("GET", "/api/v1/work-items?"+q.Encode(), nil, &page); err != nil {
		writeHubErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(page)
}

// writeHubErr answers an aihub read failure: aihub's typed refusal
// verbatim, anything else (the hub not reached) DAEMON-503.
func writeHubErr(w http.ResponseWriter, err error) {
	if _, ok := err.(*api.Error); !ok {
		err = &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "aihub unreachable: " + err.Error()}
	}
	writeErrOf(w, err)
}

// recordWrites records ops pending on the step round: each checked for its
// shape and the whitelist (an attrs write's key too), given its id
// ("<call seq>.<n>"), the calling step's project (a file) or the owner
// message an attrs write names (ownerMessage); a file whose key the step
// round already has pending is that write (not recorded again); a key:<K>
// reference must name a file of the step round recorded before it. One
// defect refuses the whole call — nothing is recorded. No op at all (an
// empty batch) records nothing and is answered ok. After the dispatch
// handed off, nothing more is recorded.
func (sc *stepCall) recordWrites(w http.ResponseWriter, ops []stepcall.Op, allowed map[string]bool) {
	j := sc.j
	lines, err := stepcall.Read(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "hub: "+err.Error())
		return
	}
	if prev := stepcall.HandoffOf(lines, j.step, int64(j.attempt)); prev != nil {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("hub: this dispatch already handed off %s (call %d): a write recorded now would never be made", prev.Handoff.Status, prev.Call))
		return
	}
	files := map[string]string{} // key → the id of the step round's pending file
	for _, op := range stepcall.PendingOps(lines, j.step, sc.call.Round) {
		if op.Verb == stepcall.HubFile {
			files[op.Key] = op.ID
		}
	}
	var recorded []stepcall.Op
	var ids []string
	for i, op := range ops {
		at := ""
		if len(ops) > 1 {
			at = fmt.Sprintf("op %d: ", i+1)
		}
		if !allowed[op.Verb] && stepcall.IsHubWrite(op.Verb) && op.Verb != stepcall.HubBatch {
			writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("hub: %s%s refused: step %s may not call it (the step's hub whitelist: %s)", at, op.Verb, j.step, verbList(allowed)))
			return
		}
		op.ID, op.Project, op.OwnerMessage = fmt.Sprintf("%d.%d", sc.call.Seq, i+1), "", nil
		if op.Verb == stepcall.HubFile {
			op.Project = j.project
		}
		if err := stepcall.CheckOp(op); err != nil {
			writeErr(w, api.ErrDaemonBadInput, "hub: "+at+err.Error())
			return
		}
		if op.Verb == stepcall.HubAttrs {
			if keys := sc.d.stepHubKeys(j); !keys[op.Name] {
				writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("hub: %sattrs refused: step %s may not write attrs key %q (the step's hub.attrs: %s)", at, j.step, op.Name, keyList(keys)))
				return
			}
			if op.PerOwner != "" {
				var ok bool
				if op, ok = sc.ownerMessage(w, at, op); !ok {
					return
				}
			}
		}
		for _, k := range stepcall.KeyRefs(op) {
			if _, ok := files[k]; !ok {
				writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("hub: %s%s%s names no file of this step round recorded before it (file it first, with --key %s)", at, stepcall.KeyRefPrefix, k, k))
				return
			}
		}
		if op.Verb == stepcall.HubFile {
			if id, ok := files[op.Key]; ok {
				ids = append(ids, id)
				continue // the same ticket: recorded once
			}
			files[op.Key] = op.ID
		}
		recorded = append(recorded, op)
		ids = append(ids, op.ID)
	}
	sc.pending = recorded
	note := "recorded pending: written in order only when this step round hands off done; dropped otherwise"
	if len(ops) == 0 {
		ids, note = []string{}, "nothing to record: the ops file names no write (a recorded no-op)"
	}
	writeJSON(w, http.StatusOK, map[string]any{"call": sc.call.Seq, "pending": ids, "note": note})
}

// keyList renders an attrs whitelist ("none" when empty).
func keyList(keys map[string]bool) string {
	var out []string
	for k := range keys {
		out = append(out, k)
	}
	if len(out) == 0 {
		return "none"
	}
	sort.Strings(out)
	return fmt.Sprint(out)
}

// ownerMessage binds an attrs write to the owner message it names (op's
// per_owner): an owner message to the calling step in its current round
// (driver.OwnerMessagesOfRound, the record a hand-off's --per-owner is
// checked against), else the call is refused. The write carries the
// message's words verbatim, its writer ("owner" when the record names
// none) and its stamp; the words are copied, never read.
func (sc *stepCall) ownerMessage(w http.ResponseWriter, at string, op stepcall.Op) (stepcall.Op, bool) {
	j := sc.j
	asks, err := capsule.ReadAsks(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "hub: "+err.Error())
		return op, false
	}
	var st *capsule.Step
	if sc.ex != nil {
		st = sc.ex.Steps[j.step]
	}
	var msg *capsule.AskLine
	owner := driver.OwnerMessagesOfRound(asks, j.step, st)
	for i := range owner {
		if owner[i].At == op.PerOwner {
			msg = &owner[i]
		}
	}
	if msg == nil {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("hub: %sattrs refused: --per-owner %s names no owner message to step %s in its current round (runtime-state/asks.jsonl: the reply and message lines of this step since the round opened)", at, op.PerOwner, j.step))
		return op, false
	}
	by := msg.By
	if by == "" {
		by = "owner"
	}
	op.OwnerMessage = &api.OwnerMessage{Words: msg.Text, By: by, At: msg.At}
	return op, true
}

// stepHubSink is the daemon's binding of the pending-writes executor:
// aihub's routes on the daemon's own client — a work item filed
// idempotently by its key's body line (fileKeyed), and the signed attrs
// write, tolerating its own repeat.
type stepHubSink struct{ d *Daemon }

var _ pendingwrites.Sink = stepHubSink{}

func (s stepHubSink) File(_ context.Context, op stepcall.Op) (string, error) {
	c, err := s.client()
	if err != nil {
		return "", err
	}
	return fileKeyed(c, op.Project, op.Lane, op.Key, op.Goal, op.Content, op.Priority, op.Labels)
}

// Attrs writes op's attrs key through aihub's signed PATCH
// /work-items/{id}/attrs. A fence refusal (CORE-409) whose key already
// holds the value written — a resumed round repeating a write that was
// made before its execution was recorded — is no failure.
func (s stepHubSink) Attrs(_ context.Context, op stepcall.Op) error {
	c, err := s.client()
	if err != nil {
		return err
	}
	req := api.AttrsWriteRequest{Key: op.Name, Value: op.Value, OwnerMessage: op.OwnerMessage}
	if len(op.If) > 0 {
		req.If = make(map[string]json.RawMessage, len(op.If))
		for path, v := range op.If {
			raw, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("attrs: --if %s: %w", path, err)
			}
			req.If[path] = raw
		}
	}
	_, err = c.WriteAttrs(op.Target, req)
	var ae *api.Error
	if err == nil || !errors.As(err, &ae) || ae.Code != api.ErrCoreConflict {
		return err
	}
	cur, gerr := c.GetWorkItem(op.Target)
	if gerr != nil {
		return err
	}
	var want any
	if json.Unmarshal(op.Value, &want) == nil && reflect.DeepEqual(cur.Attrs[op.Name], want) {
		return nil
	}
	return err
}

// client answers the daemon's aihub client (the sink needs its list
// filters and its signed machine route).
func (s stepHubSink) client() (*aihubclient.Client, error) {
	c, ok := s.d.hub.(*aihubclient.Client)
	if !ok || c == nil {
		return nil, errors.New("this daemon's aihub client cannot make a step's hub writes")
	}
	return c, nil
}
