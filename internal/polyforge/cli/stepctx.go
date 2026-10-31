package cli

// stepctx.go: `pf` inside a workflow step (the PF_WI_ID + PF_DAEMON_ID
// markers every step process carries). A step holds no aihub credential
// path: recall, memory show and remember go through the daemon's
// /step/memories face; every other aihub verb is refused and the
// invocation carries no key. `pf result submit` is the step's one way
// to deliver its result envelope (the daemon's /step/result). cli.toml stays readable under the shared
// HOME — a guard against accidents, not malice.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/stepmem"
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

var stepAllowed = map[string]bool{"recall": true, "remember": true, "memory": true}

// stepRefusal refuses, inside a step, every data-plane and management verb
// but recall / remember / memory, and doctor (it probes aihub with the key).
func stepRefusal(cmd *Command) error {
	if stepAllowed[cmd.Name] || (cmd.Group != GroupData && cmd.Group != GroupMgmt && cmd.Name != "doctor") {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonForbidden, Detail: fmt.Sprintf(
		"pf %s refused inside a workflow step: step agents hold no aihub access — they use `pf recall`, `pf memory show` and `pf remember` only, which the daemon mediates (%s)",
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

// memoryFace answers the daemon's memory face prefix of the current mode.
func (ctx *Context) memoryFace() string {
	if ctx.Task != "" {
		return "/task/memories"
	}
	return "/step/memories"
}

// searchMemories is `pf recall`'s search (via the daemon in a step).
// sort and after (browse paging,) reach aihub on the human
// direct path only: the daemon's step/task memory proxy is search-only
// and cmdRecall refuses browse and its flags there before calling this,
// so they are never put on a daemon request.
func (ctx *Context) searchMemories(proj, q, memType string, tags []string, limit int, match, sort, after string) (json.RawMessage, error) {
	if ctx.Step == "" && ctx.Task == "" {
		return ctx.aihub().SearchMemories(proj, q, memType, tags, limit, match, sort, after)
	}
	v := url.Values{"q": {q}, "project": {proj}, "type": {memType}, "tags": {strings.Join(tags, ",")},
		"limit": {strconv.Itoa(limit)}, "match": {match}}
	return ctx.daemon().do("GET", ctx.memoryFace()+"/search", v.Encode(), nil, nil)
}

// readMemory is `pf memory show`'s read (via the daemon in a step).
func (ctx *Context) readMemory(id string) (json.RawMessage, []byte, error) {
	if ctx.Step == "" && ctx.Task == "" {
		meta, err := ctx.aihub().GetMemory(id)
		if err != nil {
			return nil, nil, err
		}
		body, err := ctx.aihub().GetMemoryContent(id)
		return meta, body, err
	}
	raw, err := ctx.daemon().do("GET", ctx.memoryFace()+"/"+url.PathEscape(id), "", nil, nil)
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

// stepRemember posts the step's memory to the daemon, which checks it
// again and stores it for the step's own work item; it fails fast here.
func stepRemember(ctx *Context, fs *flag.FlagSet, memType, proj, content string) error {
	title := strings.TrimSpace(stringFlag(fs, "title"))
	var tags []string
	for _, t := range strings.Split(stringFlag(fs, "tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	if why := stepmem.Check(memType, title, tags, content); why != "" {
		return fmt.Errorf("remember: refused — %s", why)
	}
	if v := stringFlag(fs, "visibility"); v != "project" {
		return fmt.Errorf("remember: a step memory is project-visible (got --visibility %s)", v)
	}
	raw, err := ctx.daemon().do("POST", "/step/memories", "", map[string]any{
		"type": memType, "title": title, "tags": tags, "content": content,
		"project": proj, "work_item": stringFlag(fs, "work-item"),
	}, nil)
	if err != nil {
		return fmt.Errorf("remember: not stored (the step goes on): %w", err)
	}
	finishAs(ctx, raw, func(m *api.Memory) {
		fmt.Fprintf(ctx.Stdout, "stored %s (type %s, visibility %s)\n", m.ID, m.Type, m.Visibility)
	})
	return nil
}

// ---------------------------------------------------------------------------
// result — the step's result delivery (polyforge#214).
// ---------------------------------------------------------------------------

// cmdResult delivers a workflow step's result envelope through the
// daemon, which identifies the calling step from its process tree, stamps
// wi_id / step_id / spawn_n / submitted_at itself and lands the envelope
// at the dispatch's drop path. Reachable from a step process only: the
// daemon refuses every other caller.
var cmdResult = &Command{
	Name:    "result",
	Group:   GroupExec,
	Summary: "Deliver this workflow step's result envelope (inside a step only)",
	Usage:   "pf result submit <json | - (stdin) | @file>",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 || args[0] != "submit" {
			return fmt.Errorf("result: expected submit (see `pf help result`)")
		}
		return resultSubmit(ctx, args[1:])
	},
}

// resultSubmit posts the envelope payload — outcome + output (or the
// needs_input question pair), plus command_evidence when the step owes
// it — to the daemon. A refusal exits non-zero and nothing is written.
func resultSubmit(ctx *Context, args []string) error {
	var raw []byte
	var err error
	switch {
	case len(args) == 1 && args[0] == "-":
		raw, err = io.ReadAll(ctx.Stdin)
	case len(args) == 1 && strings.HasPrefix(args[0], "@"):
		raw, err = os.ReadFile(strings.TrimPrefix(args[0], "@"))
	case len(args) >= 1:
		raw = []byte(strings.Join(args, " "))
	default:
		return fmt.Errorf("result submit: the envelope is required (positional JSON, '-' for stdin or '@file')")
	}
	if err != nil {
		return fmt.Errorf("result submit: %w", err)
	}
	if !json.Valid(raw) {
		return fmt.Errorf("result submit: the envelope is not valid JSON")
	}
	out, err := ctx.daemon().do("POST", "/step/result", "", json.RawMessage(raw), nil)
	if err != nil {
		return err
	}
	finishAs(ctx, out, func(r *struct {
		WIID    string `json:"wi_id"`
		StepID  string `json:"step_id"`
		SpawnN  int64  `json:"spawn_n"`
		Outcome string `json:"outcome"`
	}) {
		fmt.Fprintf(ctx.Stdout, "delivered %s result of %s step %s (spawn %d)\n", r.Outcome, r.WIID, r.StepID, r.SpawnN)
	})
	return nil
}
