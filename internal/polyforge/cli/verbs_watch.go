package cli

// verbs_watch.go: `pf watch` — the human-coordination lane (v2-converge §3).
//
//	pf watch <wi> --once [--json]          one snapshot of a WI
//	pf watch decisions [--json]            the machine's decision feed
//	pf watch dismiss <id>...|--all         mark feed entries seen
//
// The resident per-chat watcher, its subscriptions, adopt and statusline
// faces are retired: human decisions are the daemon's
// decision feed, shown and handled in the Pi Web Polyforge panel. All
// rendering mechanics live in internal/polyforge/watcher; this file only
// wires flags and the daemon.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

// watchNow is the clock seam for the snapshot render.
var watchNow = time.Now

var cmdWatch = &Command{
	Name:    "watch",
	Group:   GroupExec,
	Summary: "Watch work items: one snapshot of a work item, or the machine's decision feed (the human decisions the Pi Web panel shows)",
	Usage: "pf watch <wi> --once [--json] [--after N] [--events N] [--demand] [--artifact <relpath>]\n" +
		"       pf watch decisions [--json]                    (the decision feed of every Cell, newest first)\n" +
		"       pf watch dismiss <id>... | --all               (mark feed entries seen)",
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("once", false, "print one snapshot of <wi> and exit (status, step, ages, pending question, latest events)")
		fs.Bool("json", false, "machine-readable output for --once / decisions / dismiss")
		fs.Int64("after", 0, "--once: only events with seq above this cursor")
		fs.Int("events", 8, "--once: how many latest events to show")
		fs.Bool("demand", false, "--once: mark this snapshot request as human-side demand for the summarize lane (the panel's poll arms the narration)")
		fs.String("artifact", "", "--once: read a capsule artifacts/<relpath> file (local holder or signed remote relay)")
		fs.Bool("all", false, "dismiss: every open entry")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		// polyforge#224: --demand is a --once flag — an authorizing flag
		// is never silently dropped.
		if boolFlag(fs, "demand") && !boolFlag(fs, "once") {
			return fmt.Errorf("watch: --demand needs --once — it marks this snapshot request as human-side demand for the summarize lane (the panel's poll arms the narration)")
		}
		asJSON := ctx.JSON || boolFlag(fs, "json")
		if len(args) > 0 {
			switch args[0] {
			case "decisions":
				if len(args) != 1 {
					return fmt.Errorf("watch decisions: unexpected arguments %v", args[1:])
				}
				return watchDecisions(ctx, asJSON)
			case "dismiss":
				return watchDismiss(ctx, args[1:], boolFlag(fs, "all"), asJSON)
			case "list", "subscribe", "unsubscribe", "adopt", "ack":
				// the chat watcher's subcommand words: refused by name, never
				// read as a work item id
				return fmt.Errorf("watch %s: retired with the chat subscriptions human decisions are the machine's decision feed: `pf watch decisions` (mark them seen with `pf watch dismiss`), shown in Pi Web's Polyforge panel", args[0])
			}
		}
		if boolFlag(fs, "all") {
			return fmt.Errorf("watch: --all belongs to `pf watch dismiss`")
		}
		if len(args) == 1 && args[0] != "" {
			return watchOnce(ctx, fs, args[0], asJSON)
		}
		if len(args) > 1 {
			return fmt.Errorf("watch: expected one work item id (with --once), or decisions / dismiss")
		}
		return fmt.Errorf("watch: a work item id is required (pf watch <wi> --once), or use `pf watch decisions`")
	},
}

// hostDaemon answers the Host-scope client: the decision feed is one list
// for every Cell, so no profile header rides it.
func hostDaemon(ctx *Context) *DaemonClient {
	d := ctx.daemon()
	d.Profile, d.ProfileErr = "", nil
	return d
}

// watchDecisions prints the decision feed (`pf watch decisions`).
func watchDecisions(ctx *Context, asJSON bool) error {
	raw, err := hostDaemon(ctx).Decisions()
	if err != nil {
		return err
	}
	if asJSON {
		ctx.Out(raw)
		return nil
	}
	var feed protocol.DecisionFeed
	if err := json.Unmarshal(raw, &feed); err != nil {
		return fmt.Errorf("watch decisions: decode the feed: %w", err)
	}
	renderDecisions(ctx.Stdout, feed)
	return nil
}

// renderDecisions prints one line per entry, newest first: id, time,
// state, the work item (or task), class, identifiers and the fixed
// phrase; a task entry adds its summary on the next line.
func renderDecisions(w io.Writer, feed protocol.DecisionFeed) {
	fmt.Fprintf(w, "%d open decision(s), %d retained\n", feed.Attention, len(feed.Decisions))
	for _, e := range feed.Decisions {
		state := "info"
		switch {
		case e.Dismissed:
			state = "dismissed"
		case e.Resolved:
			state = "resolved"
		case e.Attention:
			state = "OPEN"
		}
		name := api.DisplayName(e.Slug, e.WI)
		if e.Kind == protocol.DecisionTask {
			name = "task " + e.TaskID
		}
		if e.Profile != "" && e.Profile != "default" {
			name = e.Profile + ":" + name
		}
		class := e.Class
		if e.Source != "" && e.Source != e.Class && e.Source != "task" {
			class += " (" + e.Source + ")"
		}
		ident := ""
		switch {
		case e.QuestionID != "":
			ident = " " + e.QuestionID
		case e.Repo != "":
			ident = " " + e.Repo
		}
		fmt.Fprintf(w, "#%-5d %s %-9s %s %s%s — %s\n", e.ID, e.At, state, name, class, ident, e.Phrase)
		if e.Text != "" {
			fmt.Fprintf(w, "       %s\n", e.Text)
		}
	}
}

// watchDismiss marks feed entries dismissed (`pf watch dismiss`).
func watchDismiss(ctx *Context, args []string, all, asJSON bool) error {
	if all == (len(args) > 0) {
		return fmt.Errorf("watch dismiss: name the entry ids or pass --all (not both)")
	}
	var ids []int64
	for _, a := range args {
		n, err := strconv.ParseInt(strings.TrimPrefix(a, "#"), 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("watch dismiss: %q is not a decision id (a positive integer)", a)
		}
		ids = append(ids, n)
	}
	raw, err := hostDaemon(ctx).DismissDecisions(ids, all)
	if err != nil {
		return err
	}
	if asJSON {
		ctx.Out(raw)
		return nil
	}
	var ans protocol.DecisionDismissAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return fmt.Errorf("watch dismiss: decode the answer: %w", err)
	}
	fmt.Fprintf(ctx.Stdout, "dismissed %d decision(s)\n", ans.Dismissed)
	return nil
}

// watchOnce prints one snapshot (`pf watch <wi> --once`).
func watchOnce(ctx *Context, fs *flag.FlagSet, wi string, asJSON bool) error {
	if rel := stringFlag(fs, "artifact"); rel != "" {
		raw, err := ctx.daemon().WatchArtifact(wi, rel)
		if err != nil {
			return err
		}
		if asJSON {
			ctx.Out(raw)
			return nil
		}
		var ans api.ArtifactAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			return err
		}
		if ans.Pointer {
			fmt.Fprintf(ctx.Stdout, "artifact:   %s (%s, %s) — pointer only; the work item is not running\n", ans.Path, api.DisplayName(ans.Slug, ans.WorkItem), ans.Status)
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "artifact:   %s (%d bytes)\n", ans.Path, len(ans.Content))
		fmt.Fprint(ctx.Stdout, string(ans.Content))
		if len(ans.Content) > 0 && ans.Content[len(ans.Content)-1] != '\n' {
			fmt.Fprintln(ctx.Stdout)
		}
		return nil
	}
	snap, err := ctx.daemon().WatchSnapshot(wi, int64Flag(fs, "after"), boolFlag(fs, "demand"))
	if err != nil {
		return err
	}
	if asJSON {
		b, _ := json.Marshal(snap)
		ctx.Out(b)
		return nil
	}
	fmt.Fprint(ctx.Stdout, watcher.FormatOnce(snap, watchNow(), intFlag(fs, "events")))
	return nil
}
