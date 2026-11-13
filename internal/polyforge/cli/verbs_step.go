package cli

// verbs_step.go: `pf step` — a workflow step's one entry point into the
// daemon (workflow contract 4, design #483 §5: the driver reacts to the
// dispatch's accepted hand-off). Every subcommand is one
// POST /step/call to the daemon, which resolves the calling step from its
// process tree, records the call in the capsule's call ledger
// (runtime-state/step-calls.jsonl) before it runs it, and answers:
//
//	pf step handoff --status <done|fail|blocked|cancel|waive|rewind> --body @file
//	                [--evidence <path>]… [--to <step>] [--per-owner <at>] [--reason "<one line>"]
//	pf step ask "<question>"
//	pf step recall [--type T] [--tags a,b] [--limit N] [--any] <query>
//	pf step memory show <id>
//	pf step remember --type experience.wrap --title S [--tags a,b] <content | - | @file>
//	pf step hub get <wi> | list [--status S] [--bundle L] [--attr <path>=<value>]… [--limit N]
//	pf step hub file --key K --lane L --goal G --content <text | @file> [--priority P] [--label X]…
//	pf step hub attrs <wi | key:K> --set <key> <json | @file> [--if <path>=<value>]… [--per-owner <at>]
//	pf step hub wrap-attrs <name> <json | @file>
//	pf step hub batch <@ops.json | ->   ([] is a recorded no-op)
//
// Outside a step (no PF_WI_ID + PF_DAEMON_ID) it refuses before any
// request; inside one the daemon is the judge — it refuses a caller that
// is no step process of its own. A step holds no aihub credential: the
// hub verbs reach aihub through the daemon's client, and its writes are
// recorded pending, made only when the dispatch hands off done.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/stepmem"
)

var cmdStep = &Command{
	Name:    "step",
	Group:   GroupExec,
	Summary: "A workflow step's one entry point: hand off, ask, memories, hub (inside a step only)",
	Usage: "pf step handoff --status <done|fail|blocked|cancel|waive|rewind> --body @file [--evidence <path>]... [--to <step>] [--per-owner <at>] [--reason \"<one line>\"]\n" +
		"       pf step ask \"<question>\" | pf step ask - (stdin)\n" +
		"       pf step recall [--type T] [--tags a,b] [--limit N] [--any] <query>\n" +
		"       pf step memory show <id> [--max-bytes N]\n" +
		"       pf step remember --type experience.wrap --title S [--tags a,b] <content | - (stdin) | @file>\n" +
		"       pf step hub get <wi>\n" +
		"       pf step hub list [--status S] [--bundle L] [--attr <path>=<value>]... [--limit N]\n" +
		"       pf step hub file --key K --lane L --goal G --content <text | @file> [--priority P] [--label X]...\n" +
		"       pf step hub attrs <wi | key:K> --set <key> <json | @file> [--if <path>=<value>]... [--per-owner <at>]\n" +
		"       pf step hub wrap-attrs <name> <json | @file>\n" +
		"       pf step hub batch <@ops.json | - (stdin)>",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if ctx.Step == "" {
			return fmt.Errorf("step: runs inside a workflow step only (%s and %s are not set): it is a step's entry point into the daemon that started it", stepMarkerEnv, stepDaemonIDEnv)
		}
		if len(args) == 0 {
			return fmt.Errorf("step: expected handoff, ask, recall, memory, remember or hub (see `pf help step`)")
		}
		sub, rest := args[0], args[1:]
		switch sub {
		case "handoff":
			return stepHandoff(ctx, rest)
		case "ask":
			return stepAsk(ctx, rest)
		case "recall":
			return stepRecall(ctx, rest)
		case "memory":
			if len(rest) == 0 || rest[0] != "show" {
				return fmt.Errorf("step memory: expected show <id>")
			}
			return memoryShowWith(ctx, "step memory show", rest[1:], ctx.stepReadMemory)
		case "remember":
			return stepRememberCall(ctx, rest)
		case "hub":
			return stepHub(ctx, rest)
		}
		return fmt.Errorf("step: unknown subcommand %q (see `pf help step`)", sub)
	},
}

// stepCall posts one call to the daemon's /step/call.
func (ctx *Context) stepCall(verb string, args any) (json.RawMessage, error) {
	return ctx.daemon().do("POST", "/step/call", "", map[string]any{"verb": verb, "args": args}, nil)
}

// stepHandoff is `pf step handoff`.
func stepHandoff(ctx *Context, args []string) error {
	f := subFlags(ctx, "step handoff")
	status := f.String("status", "", "done | fail | blocked | cancel | waive | rewind")
	body := f.String("body", "", "the body: @file (or - for stdin), carrying the template's seven sections")
	var evidence stringList
	f.Var(&evidence, "evidence", "an evidence file inside the capsule (repeatable)")
	to := f.String("to", "", "rewind: the step to run again")
	perOwner := f.String("per-owner", "", "the stamp (at) of the owner message the status carries out")
	reason := f.String("reason", "", "one line: why")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("step handoff: unexpected argument %q (the body is --body @file)", rest[0])
	}
	text, err := readAtArg(ctx, "step handoff --body", *body, false)
	if err != nil {
		return err
	}
	cwd, err := osGetwd()
	if err != nil {
		return fmt.Errorf("step handoff: %w", err)
	}
	var paths []string
	for _, p := range evidence {
		if p != "" && !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		paths = append(paths, p)
	}
	raw, err := ctx.stepCall("handoff", map[string]any{
		"status": *status, "body": text, "evidence": paths, "to": *to, "per_owner": *perOwner, "reason": *reason,
	})
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(r *struct {
		Step   string `json:"step"`
		Round  int64  `json:"round"`
		Spawn  int64  `json:"spawn"`
		Status string `json:"status"`
		Path   string `json:"path"`
	}) {
		fmt.Fprintf(ctx.Stdout, "handed off %s (step %s, round %d, spawn %d): the section is appended to %s — now end your turn\n", r.Status, r.Step, r.Round, r.Spawn, r.Path)
	})
	return nil
}

// stepAsk is `pf step ask`.
func stepAsk(ctx *Context, args []string) error {
	text, err := askText(ctx.Stdin, args)
	if err != nil {
		return err
	}
	raw, err := ctx.stepCall("ask", map[string]any{"text": text})
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(l *struct {
		At    string `json:"at"`
		Step  string `json:"step"`
		Spawn int64  `json:"spawn"`
	}) {
		fmt.Fprintf(ctx.Stdout, "asked (step %s, spawn %d, %s): now end your turn — the daemon shows the question to the owner and brings the reply back into this session\n", l.Step, l.Spawn, l.At)
	})
	return nil
}

// stepRecall is `pf step recall`: a search of the step's own project.
func stepRecall(ctx *Context, args []string) error {
	f := subFlags(ctx, "step recall")
	memType := f.String("type", "", "memory type: exact, a trailing .* prefix, or *")
	tags := f.String("tags", "", "comma-separated tags")
	limit := f.Int("limit", 20, "max results (1..200)")
	anyMatch := f.Bool("any", false, "ranked any-term match instead of every-word")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	q := strings.TrimSpace(strings.Join(rest, " "))
	if q == "" {
		return fmt.Errorf("step recall: a query is required (a step searches only)")
	}
	if *limit < 1 || *limit > 200 {
		return fmt.Errorf("step recall: --limit must be 1..200")
	}
	typ, err := recallTypeFilter(*memType)
	if err != nil {
		return err
	}
	a := map[string]any{"q": q, "type": typ, "limit": *limit}
	if *tags != "" {
		a["tags"] = strings.Split(*tags, ",")
	}
	if *anyMatch {
		a["match"] = "any"
	}
	raw, err := ctx.stepCall("recall", a)
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(l *itemList[memorySearchHit]) {
		fmt.Fprintf(ctx.Stdout, "%d hit(s)\n", len(l.Items))
		renderMemories(ctx.Stdout, l.Items)
	})
	return nil
}

// stepReadMemory reads one memory of the step's project through its call.
func (ctx *Context) stepReadMemory(id string) (json.RawMessage, []byte, error) {
	raw, err := ctx.stepCall("memory.show", map[string]any{"id": id})
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Meta    json.RawMessage `json:"meta"`
		Content string          `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Meta) == 0 {
		return nil, nil, fmt.Errorf("step memory show: the daemon's answer is unreadable")
	}
	return doc.Meta, []byte(doc.Content), nil
}

// stepRememberCall is `pf step remember`: the step's summary memory,
// checked here first and again by the daemon.
func stepRememberCall(ctx *Context, args []string) error {
	f := subFlags(ctx, "step remember")
	memType := f.String("type", "", "memory type (a step stores experience.wrap)")
	title := f.String("title", "", "memory title")
	tags := f.String("tags", "", "comma-separated tags")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	var content string
	switch {
	case len(rest) == 1 && (rest[0] == "-" || strings.HasPrefix(rest[0], "@")):
		if content, err = readAtArg(ctx, "step remember", rest[0], true); err != nil {
			return err
		}
	case len(rest) >= 1:
		content = strings.Join(rest, " ")
	}
	var tagList []string
	for _, t := range strings.Split(*tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tagList = append(tagList, t)
		}
	}
	t := strings.TrimSpace(*title)
	if why := stepmem.Check(*memType, t, tagList, content); why != "" {
		return fmt.Errorf("step remember: refused — %s", why)
	}
	raw, err := ctx.stepCall("remember", map[string]any{"type": *memType, "title": t, "tags": tagList, "content": content})
	if err != nil {
		return err
	}
	finishAs(ctx, raw, func(m *api.Memory) {
		fmt.Fprintf(ctx.Stdout, "stored %s (type %s, visibility %s)\n", m.ID, m.Type, m.Visibility)
	})
	return nil
}

// stepHub is `pf step hub <verb>`.
func stepHub(ctx *Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("step hub: expected get, list, file, attrs, wrap-attrs or batch")
	}
	verb, rest := args[0], args[1:]
	f := subFlags(ctx, "step hub "+verb)
	var a any
	switch verb {
	case "get":
		pos, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("step hub get: expected exactly one work item")
		}
		a = map[string]any{"wi": pos[0]}
	case "list":
		status := f.String("status", "", "work item status")
		bundle := f.String("bundle", "", "lane (bundle) name")
		var attrs stringList
		f.Var(&attrs, "attr", "<path>=<value>: the attr at the dot path is that string (repeatable)")
		limit := f.Int("limit", 0, "max items (aihub's default when 0)")
		if _, err := parseCommandArgs(f, rest); err != nil {
			return err
		}
		a = map[string]any{"status": *status, "bundle": *bundle, "attrs": []string(attrs), "limit": *limit}
	case "file":
		key := f.String("key", "", "the ticket's identity (a replay finds it filed under it)")
		lane := f.String("lane", "", "the lane the ticket is filed on")
		goal := f.String("goal", "", "the ticket's goal")
		content := f.String("content", "", "the ticket's body: text or @file")
		priority := f.String("priority", "", "the ticket's priority")
		var labels stringList
		f.Var(&labels, "label", "a label (repeatable)")
		if _, err := parseCommandArgs(f, rest); err != nil {
			return err
		}
		body, err := readAtArg(ctx, "step hub file --content", *content, true)
		if err != nil {
			return err
		}
		a = map[string]any{"key": *key, "lane": *lane, "goal": *goal, "content": body, "priority": *priority, "labels": []string(labels)}
	case "attrs":
		key := f.String("set", "", "the top-level attrs key written whole (one of the step's hub.attrs); its value is the positional JSON or @file")
		var conds stringList
		f.Var(&conds, "if", "<path>=<value>: the write is made only while the attr at the dot path is that string (repeatable)")
		perOwner := f.String("per-owner", "", "the stamp (at) of the owner message the write carries out")
		pos, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return fmt.Errorf("step hub attrs: expected <wi | key:K> --set <key> <json | @file>")
		}
		raw, err := readJSONArg(ctx, "step hub attrs --set "+*key, pos[1])
		if err != nil {
			return err
		}
		ifs := map[string]string{}
		for _, c := range conds {
			path, value, ok := strings.Cut(c, "=")
			if !ok || path == "" {
				return fmt.Errorf("step hub attrs: --if %q is not <path>=<value>", c)
			}
			ifs[path] = value
		}
		a = map[string]any{"target": pos[0], "name": *key, "value": raw, "if": ifs, "per_owner": *perOwner}
	case "wrap-attrs":
		pos, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return fmt.Errorf("step hub wrap-attrs: expected <name> <json | @file>")
		}
		raw, err := readJSONArg(ctx, "step hub wrap-attrs", pos[1])
		if err != nil {
			return err
		}
		a = map[string]any{"name": pos[0], "value": raw}
	case "batch":
		pos, err := parseCommandArgs(f, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("step hub batch: expected <@ops.json | ->")
		}
		raw, err := readJSONArg(ctx, "step hub batch", pos[0])
		if err != nil {
			return err
		}
		var ops []json.RawMessage
		if err := json.Unmarshal(raw, &ops); err != nil {
			return fmt.Errorf("step hub batch: the ops file is a JSON array of writes ({\"verb\": \"file\"|\"attrs\"|\"wrap-attrs\", …}): %v", err)
		}
		if ops == nil {
			ops = []json.RawMessage{} // [] records nothing: a no-op the daemon still records
		}
		a = map[string]any{"ops": ops}
	default:
		return fmt.Errorf("step hub: unknown verb %q (get, list, file, attrs, wrap-attrs, batch)", verb)
	}
	raw, err := ctx.stepCall("hub."+verb, a)
	if err != nil {
		return err
	}
	switch verb {
	case "get", "list":
		ctx.Out(raw)
	default:
		finishAs(ctx, raw, func(r *struct {
			Pending []string `json:"pending"`
			Note    string   `json:"note"`
		}) {
			if len(r.Pending) == 0 {
				fmt.Fprintln(ctx.Stdout, r.Note)
				return
			}
			fmt.Fprintf(ctx.Stdout, "%s: %s\n", strings.Join(r.Pending, ", "), r.Note)
		})
	}
	return nil
}

// readAtArg answers an argument's text: @file reads the file, - reads
// stdin, anything else is the text itself when inline is allowed.
func readAtArg(ctx *Context, what, v string, inline bool) (string, error) {
	switch {
	case v == "-":
		if ctx.Stdin == nil {
			return "", fmt.Errorf("%s: no stdin to read", what)
		}
		raw, err := io.ReadAll(io.LimitReader(ctx.Stdin, 1<<20))
		return string(raw), err
	case strings.HasPrefix(v, "@"):
		raw, err := os.ReadFile(strings.TrimPrefix(v, "@"))
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		return string(raw), nil
	case inline:
		return v, nil
	case v == "":
		return "", fmt.Errorf("%s is required (@file, or - for stdin)", what)
	}
	return "", fmt.Errorf("%s is @file (or - for stdin), not inline text", what)
}

// readJSONArg answers an argument that is one JSON document, inline or
// @file / -.
func readJSONArg(ctx *Context, what, v string) (json.RawMessage, error) {
	if v == "" {
		return nil, fmt.Errorf("%s is required (JSON, @file or -)", what)
	}
	text, err := readAtArg(ctx, what, v, true)
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(text)) {
		return nil, fmt.Errorf("%s is not valid JSON", what)
	}
	return json.RawMessage(text), nil
}

// askText answers the question: the positional words joined, or stdin
// for `-`; non-empty after trimming, valid UTF-8, at most
// capsule.MaxAskBytes.
func askText(stdin io.Reader, args []string) (string, error) {
	var text string
	switch {
	case len(args) == 1 && args[0] == "-":
		if stdin == nil {
			return "", fmt.Errorf("ask: no stdin to read the question from")
		}
		raw, err := io.ReadAll(io.LimitReader(stdin, capsule.MaxAskBytes+1))
		if err != nil {
			return "", fmt.Errorf("ask: %w", err)
		}
		text = string(raw)
	case len(args) >= 1:
		text = strings.Join(args, " ")
	default:
		return "", fmt.Errorf("ask: the question is required (`pf step ask \"<question>\"`, or `pf step ask -` with the question on stdin)")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("ask: the question is empty")
	}
	if len(text) > capsule.MaxAskBytes {
		return "", fmt.Errorf("ask: the question is %d bytes, over the %d-byte bound: ask the one thing you need and put the detail in an artifact", len(text), capsule.MaxAskBytes)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("ask: the question is not valid UTF-8")
	}
	return text, nil
}
