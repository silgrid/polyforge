package cli

// stepctx.go: `pf` inside a workflow step (the PF_WI_ID + PF_DAEMON_ID
// markers every step process carries). A step holds no aihub credential
// path: its one entry into the daemon is `pf step` (verbs_step.go: hand
// off, ask, recall, memory show, remember, hub); every data-plane and
// management verb is refused and the invocation carries no key. cli.toml
// stays readable under the shared HOME — a guard against accidents, not
// malice.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

const (
	stepMarkerEnv   = "PF_WI_ID"
	stepDaemonIDEnv = "PF_DAEMON_ID"
)

// stepWorkItem answers PF_WI_ID when both markers are set ("" = not a
// step). It only selects step mode; the daemon resolves the real work
// item from the process tree.
func stepWorkItem() string {
	wi := strings.TrimSpace(os.Getenv(stepMarkerEnv))
	if wi == "" || strings.TrimSpace(os.Getenv(stepDaemonIDEnv)) == "" {
		return ""
	}
	return wi
}

// stepModeNote names what switched step mode on.
func stepModeNote() string {
	return fmt.Sprintf("step mode: %s=%s and %s=%s are set — unset both if this is not a workflow step",
		stepMarkerEnv, os.Getenv(stepMarkerEnv), stepDaemonIDEnv, os.Getenv(stepDaemonIDEnv))
}

// stepRefused are the verbs outside the data/management groups a step may
// not run: `pf profile` (a step's Cell is the Host's ownership record, never
// a resolution of its own).
var stepRefused = map[string]bool{"doctor": true, "profile": true}

// stepRefusal refuses, inside a step, every data-plane and management verb
// (recall, memory and remember, Cell lifecycle, configuration and the
// migration included), doctor (it probes aihub) and profile: a step reads
// and writes memories with `pf step recall | memory show | remember`.
func stepRefusal(cmd *Command) error {
	if cmd.Group != GroupData && cmd.Group != GroupMgmt && !stepRefused[cmd.Name] {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonForbidden, Detail: fmt.Sprintf(
		"pf %s refused inside a workflow step: step agents hold no aihub access — a step's one entry is `pf step` (`pf step recall`, `pf step memory show`, `pf step remember`, `pf step hub`), which the daemon mediates (%s)",
		cmd.Name, stepModeNote())}
}

// taskProcessID answers PF_TASK_ID when PF_DAEMON_ID is set too ("" = not a
// task agent). Like stepWorkItem it only selects the mode; the daemon
// resolves the real task from the process tree.
func taskProcessID() string {
	id := strings.TrimSpace(os.Getenv("PF_TASK_ID"))
	if id == "" || strings.TrimSpace(os.Getenv(stepDaemonIDEnv)) == "" {
		return ""
	}
	return id
}

var taskAllowed = map[string]bool{"recall": true, "memory": true, "watch": true, "version": true}

// taskRefusal refuses, inside a task agent, every verb but the read ones
// (recall, memory show, watch --once): a task agent never drives the daemon
// and cannot use the aihub-direct verbs. The spawn env carries no aihub key
// and task mode clears it, but the shared HOME keeps cli.toml readable — an
// accident guard, not a boundary.
func taskRefusal(cmd *Command) error {
	if taskAllowed[cmd.Name] {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonForbidden, Detail: fmt.Sprintf(
		"pf %s refused inside a task agent: a task reads memories (`pf recall`, `pf memory show`) and watch snapshots (`pf watch <wi> --once`) only (PF_TASK_ID=%s is set — unset it if this is not a task)",
		cmd.Name, os.Getenv("PF_TASK_ID"))}
}

// taskMemories is the daemon's memory face of a task agent.
const taskMemories = "/task/memories"

// searchMemories is `pf recall`'s search (via the daemon in a task).
// sort and after (browse paging) reach aihub on the human
// direct path only: the daemon's task memory proxy is search-only and
// cmdRecall refuses browse and its flags there before calling this, so
// they are never put on a daemon request.
func (ctx *Context) searchMemories(proj, q, memType string, tags []string, limit int, match, sort, after string) (json.RawMessage, error) {
	if ctx.Task == "" {
		return ctx.aihub().SearchMemories(proj, q, memType, tags, limit, match, sort, after)
	}
	v := url.Values{"q": {q}, "project": {proj}, "type": {memType}, "tags": {strings.Join(tags, ",")},
		"limit": {strconv.Itoa(limit)}, "match": {match}}
	return ctx.daemon().do("GET", taskMemories+"/search", v.Encode(), nil, nil)
}

// readMemory is `pf memory show`'s read (via the daemon in a task).
func (ctx *Context) readMemory(id string) (json.RawMessage, []byte, error) {
	if ctx.Task == "" {
		meta, err := ctx.aihub().GetMemory(id)
		if err != nil {
			return nil, nil, err
		}
		body, err := ctx.aihub().GetMemoryContent(id)
		return meta, body, err
	}
	raw, err := ctx.daemon().do("GET", taskMemories+"/"+url.PathEscape(id), "", nil, nil)
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Meta    json.RawMessage `json:"meta"`
		Content string          `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Meta) == 0 {
		return nil, nil, fmt.Errorf("memory show: the daemon's answer is unreadable")
	}
	return doc.Meta, []byte(doc.Content), nil
}
