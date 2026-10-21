package cli

// verbs_watch.go: `pf watch` — the human-coordination lane (v2-converge §3).
//
//	pf watch <wi> --once [--json]          one snapshot of a WI
//	pf watch --chat <id> [--wait-decision] the resident per-chat watcher
//	pf watch --chat <id> --statusline      print the progress file (fast)
//	pf watch subscribe|unsubscribe <wi>    daemon-held subscriptions
//	pf watch list                          the chat's subscriptions
//
// The chat defaults to PF_CHAT_ID; --harness-session maps a harness
// session id to its chat through the daemon (the Claude Code statusLine
// gets the session JSON on stdin, not PF_CHAT_ID). All mechanics live in
// internal/polyforge/watcher; this file only wires flags and the daemon.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

// watchNow is the clock seam for the snapshot render.
var watchNow = time.Now

// chatAPI adapts the daemon client to watcher.API for one chat.
type chatAPI struct {
	d    *DaemonClient
	chat string
}

func (c chatAPI) Subscriptions() (protocol.ChatSubscriptions, error) {
	return c.d.ChatSubscriptions(c.chat)
}
func (c chatAPI) Snapshot(wi string, after int64) (*protocol.WatchSnapshot, error) {
	// the resident watcher's polls NEVER carry the demand marker
	// (polyforge#224): only the human-side --once --demand poll arms the
	// lane.
	return c.d.WatchSnapshot(wi, after, false)
}
func (c chatAPI) Ack(a protocol.ChatAck) error { return c.d.ChatAck(c.chat, a) }
func (c chatAPI) SubscribeOwned(owner string) ([]protocol.ChatSub, error) {
	return c.d.ChatSubscribeOwned(c.chat, owner)
}

var cmdWatch = &Command{
	Name:    "watch",
	Group:   GroupExec,
	Summary: "Watch work items: one snapshot, or the resident per-chat watcher (progress + human-decision wake-ups)",
	Usage: "pf watch <wi> --once [--json] [--after N] [--events N] [--demand]\n" +
		"       pf watch --chat ID [--wait-decision] [--poll D]      (chat default: $PF_CHAT_ID)\n" +
		"       pf watch --chat ID --statusline                       (print the progress file)\n" +
		"       pf watch --harness-session ID|- [--harness claude|pi] ...   (resolve the chat via the daemon; - reads {\"session_id\"} from stdin)\n" +
		"       pf watch subscribe|unsubscribe <wi> [--chat ID]\n" +
		"       pf watch list [--chat ID]",
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("once", false, "print one snapshot of <wi> and exit (status, step, ages, pending question, latest events)")
		fs.Bool("json", false, "machine-readable output for --once / list")
		fs.Int64("after", 0, "--once: only events with seq above this cursor")
		fs.Int("events", 8, "--once: how many latest events to show")
		fs.Bool("demand", false, "--once: mark this snapshot request as human-side demand for the summarize lane (the panel's poll arms the narration; the resident watcher never marks)")
		fs.String("chat", "", "chat id (default: env PF_CHAT_ID)")
		fs.Bool("wait-decision", false, "resident: exit 0 printing one fixed-template line per human decision")
		fs.Bool("statusline", false, "print the resident watcher's progress lines (for a harness status line) and exit")
		fs.String("harness-session", "", "resolve the chat from a harness session id via the daemon ('-' = read session_id from stdin JSON)")
		fs.String("harness", "claude", "harness of --harness-session: claude|pi")
		fs.Duration("poll", 2*time.Second, "resident poll interval")
		fs.Bool("no-parent-check", false, "resident: do not exit when the parent process goes away")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		// polyforge#224: --demand is a --once flag — an authorizing flag
		// is never silently dropped (the --chat/--harness-session
		// usage-error pattern).
		if boolFlag(fs, "demand") && !boolFlag(fs, "once") {
			return fmt.Errorf("watch: --demand needs --once — it marks this snapshot request as human-side demand for the summarize lane (the panel's poll arms the narration; the resident watcher never marks)")
		}
		asJSON := ctx.JSON || boolFlag(fs, "json")
		if len(args) > 0 {
			switch args[0] {
			case "subscribe", "unsubscribe":
				if len(args) != 2 {
					return fmt.Errorf("watch %s: expected exactly one work item id", args[0])
				}
				chat, err := watchChat(ctx, fs)
				if err != nil {
					return err
				}
				ans, err := ctx.daemon().ChatSubscribe(chat, args[1], args[0] == "subscribe")
				if err != nil {
					return err
				}
				verb := map[bool]string{true: "subscribed", false: "unsubscribed"}[args[0] == "subscribe"]
				if asJSON {
					b, _ := json.Marshal(ans)
					ctx.Out(b)
				} else if ans.Changed {
					fmt.Fprintf(ctx.Stdout, "%s %s: %s\n", verb, chat, api.DisplayName(ans.Slug, ans.WI))
				} else {
					fmt.Fprintf(ctx.Stdout, "no change: %s %s was already %s\n", chat, api.DisplayName(ans.Slug, ans.WI), map[bool]string{true: "subscribed", false: "not subscribed"}[args[0] == "subscribe"])
				}
				return nil
			case "list":
				chat, err := watchChat(ctx, fs)
				if err != nil {
					return err
				}
				subs, err := ctx.daemon().ChatSubscriptions(chat)
				if err != nil {
					return err
				}
				if asJSON {
					b, _ := json.Marshal(subs)
					ctx.Out(b)
					return nil
				}
				fmt.Fprintf(ctx.Stdout, "chat %s: %d subscription(s)\n", chat, len(subs.Subscriptions))
				for _, s := range subs.Subscriptions {
					state := "active"
					if s.Done {
						state = "finished"
					}
					fmt.Fprintf(ctx.Stdout, "  %-22s %-8s cursor=%d\n", api.DisplayName(s.Slug, s.WI), state, s.Cursor)
				}
				return nil
			}
		}
		if boolFlag(fs, "statusline") {
			return watchStatusline(ctx, fs)
		}
		if len(args) == 1 && args[0] != "" {
			return watchOnce(ctx, fs, args[0], asJSON)
		}
		if len(args) > 1 {
			return fmt.Errorf("watch: expected one work item id (with --once) or none (with --chat)")
		}
		if boolFlag(fs, "once") {
			return fmt.Errorf("watch --once: a work item id is required")
		}
		return watchResident(ctx, fs)
	},
}

// watchOnce prints one snapshot (`pf watch <wi> --once`).
func watchOnce(ctx *Context, fs *flag.FlagSet, wi string, asJSON bool) error {
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

// watchChat resolves the chat id (addendum 1): the explicit flags
// outrank the environment — --chat and --harness-session are BOTH
// explicit (passing both is a usage error, never a silent pick), and
// the session lookup's result (including its not-found error) wins
// over PF_CHAT_ID. With neither flag, PF_CHAT_ID is used; with nothing
// at all, the existing no-chat-id error.
func watchChat(ctx *Context, fs *flag.FlagSet) (string, error) {
	chat := stringFlag(fs, "chat")
	hs := stringFlag(fs, "harness-session")
	if chat != "" && hs != "" {
		return "", fmt.Errorf("watch: --chat and --harness-session are both explicit chat-id sources — pass exactly one (not both)")
	}
	if chat != "" {
		return validateChatID(chat)
	}
	if hs != "" {
		c, err := chatFromHarnessSession(ctx, stringFlag(fs, "harness"), hs)
		if err != nil {
			return "", err
		}
		return validateChatID(c)
	}
	if c := ctx.Cfg.ChatID; c != "" {
		return validateChatID(c)
	}
	return "", fmt.Errorf("watch: no chat id — pass --chat <id>, set PF_CHAT_ID, or use --harness-session <id>")
}

// validateChatID enforces the chat-id shape at the resolution exit.
func validateChatID(chat string) (string, error) {
	if !watcher.ChatIDRe.MatchString(chat) {
		return "", fmt.Errorf("watch: chat id %q must be 1-64 characters of [A-Za-z0-9_-]", chat)
	}
	return chat, nil
}

// chatFromHarnessSession maps a harness session id to its chat (`pf chat
// init` semantics: created on first use, stable on resume). The answer is
// cached in the runtime dir so a statusLine refresh stays off the daemon.
func chatFromHarnessSession(ctx *Context, harness, session string) (string, error) {
	if session == "-" {
		raw, err := io.ReadAll(io.LimitReader(ctx.Stdin, 1<<20))
		if err != nil {
			return "", err
		}
		var in struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(raw, &in) != nil || in.SessionID == "" {
			return "", fmt.Errorf("watch: no session_id in the JSON on stdin")
		}
		session = in.SessionID
	}
	sum := sha256.Sum256([]byte(harness + ":" + session))
	rdir := watcher.RuntimeDir()
	if err := watcher.EnsureDir(rdir); err != nil {
		return "", fmt.Errorf("watch: %w", err)
	}
	cache := rdir + "/session-" + hex.EncodeToString(sum[:8]) + ".chat"
	if raw, err := os.ReadFile(cache); err == nil {
		if c := strings.TrimSpace(string(raw)); watcher.ChatIDRe.MatchString(c) {
			return c, nil
		}
	}
	raw, err := ctx.daemon().ChatInit(harness, session)
	if err != nil {
		return "", err
	}
	var rec ChatRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.ChatID == "" {
		return "", fmt.Errorf("watch: unexpected chat init answer")
	}
	_ = os.WriteFile(cache, []byte(rec.ChatID+"\n"), 0o600)
	return rec.ChatID, nil
}

// watchStatusline prints the progress file. It must never fail a
// status line: any problem prints nothing.
func watchStatusline(ctx *Context, fs *flag.FlagSet) error {
	chat, err := watchChat(ctx, fs)
	if err != nil {
		return nil
	}
	fmt.Fprint(ctx.Stdout, watcher.NewFiles("", chat).ReadStatusline(30*time.Second))
	return nil
}

// watchResident runs the per-chat watcher until its parent goes away, the
// user interrupts it, or (--wait-decision) a decision was printed.
func watchResident(ctx *Context, fs *flag.FlagSet) error {
	chat, err := watchChat(ctx, fs)
	if err != nil {
		return err
	}
	files := watcher.NewFiles("", chat)
	release, err := files.Lock(5 * time.Second)
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer release()
	_ = files.WritePID()

	d := ctx.daemon()
	d.HTTP.Timeout = 10 * time.Second
	api := chatAPI{d: d, chat: chat}

	opts := watcher.Options{
		Owner:        ctx.Cfg.UserID,
		WaitDecision: boolFlag(fs, "wait-decision"),
		Poll:         durationFlag(fs, "poll"),
		Out:          ctx.Stdout,
		Err:          ctx.Stderr,
		Status:       files.WriteStatus,
	}
	if !boolFlag(fs, "no-parent-check") {
		opts.ParentAlive = parentAlive()
	}
	sctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = watcher.Run(sctx, api, opts)
	if err == watcher.ErrParentGone {
		return nil
	}
	return err
}

// parentAlive answers false once the process that started the watcher is
// gone (reparented). A watcher launched already orphaned (ppid 1, e.g. by
// nohup or a service manager) has no parent to follow and never exits on
// this account.
func parentAlive() func() bool {
	ppid := os.Getppid()
	if ppid == 1 {
		return func() bool { return true }
	}
	return func() bool { return os.Getppid() == ppid }
}
