package cli

// verbs_task.go: `pf task` — the one-shot task agent (carried
// from #40), on the daemon's UDS face.
//
//	pf task spawn --project <p> [--max-duration D] [--max-tokens N] "<prompt>"
//	pf task list [--json]
//	pf task show <id> [--json]
//	pf task cancel <id>
//
// A spawn returns the task id immediately; the task runs in a fresh harness
// agent, hard-capped on wall clock and tokens. There is no `--wait`, no
// `--models`, no task type: those are refused, never swallowed. Inside a
// task the CLI refuses every write verb (stepctx.go taskRefusal) and reads
// memories through the daemon's /task/memories face.

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// DaemonClient task faces.

func (d *DaemonClient) TaskSpawn(body map[string]any) (json.RawMessage, error) {
	return d.do("POST", "/task/spawn", "", body, nil)
}
func (d *DaemonClient) TaskList() (json.RawMessage, error) {
	return d.do("GET", "/task/list", "", nil, nil)
}
func (d *DaemonClient) TaskShow(id string) (json.RawMessage, error) {
	return d.do("GET", "/task/show/"+id, "", nil, nil)
}
func (d *DaemonClient) TaskCancel(id string) (json.RawMessage, error) {
	return d.do("POST", "/task/cancel/"+id, "", map[string]any{}, nil)
}

var cmdTask = &Command{
	Name:    "task",
	Group:   GroupExec,
	Summary: "Spawn a one-shot task agent over a project's workspace (spawn, list, show, cancel)",
	Usage: "pf task spawn --project <p> [--max-duration D] [--max-tokens N] \"<prompt>\"\n" +
		"       pf task list [--json]\n" +
		"       pf task show <id> [--json]\n" +
		"       pf task cancel <id>",
	Setup: func(fs *flag.FlagSet) {
		fs.String("project", "", "spawn: the mapped project whose workspace the task runs in (a name, never a path)")
		fs.String("max-duration", "", "spawn: wall-clock cap (default 20m, ceiling 60m)")
		fs.String("max-tokens", "", "spawn: token cap (default 300000, ceiling 1000000)")
		// refused, never swallowed: the lane has no spawn-level model
		// override, no wait mode and no task type
		fs.String("models", "", "refused: the task's candidates come from steps.<project>.task.models / default.models")
		fs.Bool("wait", false, "refused: a task never blocks the caller; poll `pf task show`")
		fs.String("type", "", "refused: there is one task kind")
		// --json also works in the post-position form (`pf task show <id>
		// --json`): the flag is mirrored into ctx.JSON before dispatch.
		fs.Bool("json", false, "machine-readable output")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if boolFlag(fs, "json") {
			ctx.JSON = true
		}
		if len(args) == 0 {
			return fmt.Errorf("task: expected spawn|list|show|cancel (see `pf help task`)")
		}
		switch args[0] {
		case "spawn":
			return taskSpawn(ctx, fs, args[1:])
		case "list":
			return taskList(ctx)
		case "show":
			if len(args) != 2 {
				return fmt.Errorf("task show: expected exactly one task id")
			}
			return taskShow(ctx, args[1])
		case "cancel":
			if len(args) != 2 {
				return fmt.Errorf("task cancel: expected exactly one task id")
			}
			raw, err := ctx.daemon().TaskCancel(args[1])
			if err != nil {
				return err
			}
			ctx.finish(raw, func() { fmt.Fprintf(ctx.Stdout, "cancelling %s\n", args[1]) })
			return nil
		}
		return fmt.Errorf("task: unknown action %q (expected spawn|list|show|cancel)", args[0])
	},
}

func taskSpawn(ctx *Context, fs *flag.FlagSet, args []string) error {
	for _, f := range []string{"models", "type"} {
		if stringFlag(fs, f) != "" {
			return fmt.Errorf("task spawn: --%s is not supported (the lane has no spawn-level model override or task type; candidates come from steps.<project>.task.models, else steps.<project>.default.models)", f)
		}
	}
	if boolFlag(fs, "wait") {
		return fmt.Errorf("task spawn: --wait is not supported (a task never blocks the caller; poll `pf task show <id>`)")
	}
	project := strings.TrimSpace(stringFlag(fs, "project"))
	if project == "" {
		return fmt.Errorf("task spawn: --project <name> is required (the only selector)")
	}
	if strings.ContainsAny(project, "/\\") || strings.HasPrefix(project, ".") || strings.HasPrefix(project, "~") {
		return fmt.Errorf("task spawn: %q is a filesystem path; the only selector is --project <name> of a project mapped on this daemon", project)
	}
	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "" {
		return fmt.Errorf("task spawn: a prompt is required")
	}
	body := map[string]any{"project": project, "prompt": prompt}
	if s := stringFlag(fs, "max-duration"); s != "" {
		if _, err := task.ParseDuration(s); err != nil {
			return fmt.Errorf("task spawn: %w", err)
		}
		body["max_duration"] = s
	}
	if s := stringFlag(fs, "max-tokens"); s != "" {
		n, err := task.ParseTokens(s)
		if err != nil {
			return fmt.Errorf("task spawn: %w", err)
		}
		body["max_tokens"] = n
	}
	if ctx.Cfg.ChatID != "" {
		body["origin_chat"] = ctx.Cfg.ChatID
	}
	// The caller's self-asserted id (PF_USER_ID, else cli.toml user_id —
	// the same source as a work item's origin): the daemon resolves it
	// once to the list's reporter.
	if ctx.Cfg.UserID != "" {
		body["caller_id"] = ctx.Cfg.UserID
	}
	raw, err := ctx.daemon().TaskSpawn(body)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(t *task.Task) {
		fmt.Fprintf(ctx.Stdout, "%s\n", t.ID)
	})
	return nil
}

func taskList(ctx *Context) error {
	raw, err := ctx.daemon().TaskList()
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(doc *struct {
		Tasks []task.Task `json:"tasks"`
	}) {
		rows := make([][]string, 0, len(doc.Tasks))
		now := time.Now()
		for _, t := range doc.Tasks {
			rows = append(rows, []string{t.ID, t.Project, string(t.State), t.Age(now).Truncate(time.Second).String()})
		}
		printTable(ctx.Stdout, []string{"ID", "PROJECT", "STATE", "AGE"}, rows)
	})
	return nil
}

func taskShow(ctx *Context, id string) error {
	raw, err := ctx.daemon().TaskShow(id)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(t *task.Task) {
		w := ctx.Stdout
		fmt.Fprintf(w, "id: %s\nproject: %s\n", t.ID, t.Project)
		fmt.Fprintf(w, "state: %s\ncreated: %s\n", t.State, t.CreatedAt.Format(time.RFC3339))
		if !t.EndedAt.IsZero() {
			fmt.Fprintf(w, "ended: %s\n", t.EndedAt.Format(time.RFC3339))
		}
		fmt.Fprintf(w, "wall: %s of %s\ntokens: %d of %d\n",
			t.WallConsumed.Truncate(time.Second), t.MaxDuration, t.TokensConsumed, t.MaxTokens)
		if t.Candidate != "" {
			fmt.Fprintf(w, "candidate: %s\n", t.Candidate)
		}
		if t.Error != "" {
			fmt.Fprintf(w, "error: %s\n", t.Error)
		}
		if t.Truncated {
			fmt.Fprintln(w, "truncated: true")
		}
		fmt.Fprintf(w, "\n%s\n", t.Report)
	})
	return nil
}
