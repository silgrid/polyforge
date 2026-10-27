package cli

import (
	"flag"
	"fmt"
	"os"
)

// chat — the harness-chat identity face (v2 identity model). A chat is
// one interactive harness conversation; the daemon maps
// (harness, harness-session) to a stable chat id, so a resumed
// conversation keeps its chat. The id is a notification label, never a
// credential.
//
// Every init tags the chat with the workspace its session cwd resolves
// to: the tag decides adopt eligibility and the scoped decision
// broadcast, and a re-init refreshes it from the current cwd.
var cmdChat = &Command{
	Name:    "chat",
	Group:   GroupExec,
	Summary: "Harness chat identity: get the chat id for a harness session",
	Usage:   "pf [--json] chat init --harness claude|pi --harness-session ID [--force]",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 || args[0] != "init" {
			return fmt.Errorf("chat: expected init")
		}
		f := subFlags(ctx, "chat init")
		harness := f.String("harness", "", "harness: claude|pi")
		session := f.String("harness-session", "", "the harness's own session id")
		force := f.Bool("force", false, "take the interactive role over from the current holder (supersession)")
		rest, err := parseCommandArgs(f, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			return fmt.Errorf("chat init: unexpected arguments %v", rest)
		}
		if *harness != "claude" && *harness != "pi" {
			return fmt.Errorf("chat init: --harness must be claude|pi")
		}
		if *session == "" {
			return fmt.Errorf("chat init: --harness-session is required")
		}
		ws, err := sessionWorkspace(ctx)
		if err != nil {
			return err
		}
		raw, err := ctx.daemon().ChatInit(*harness, *session, ws, *force)
		if err != nil {
			return err
		}
		finishOrRaw(ctx, raw, func(rec *ChatRecord) bool {
			if rec.ChatID == "" {
				return false
			}
			fmt.Fprintln(ctx.Stdout, rec.ChatID)
			return true
		})
		return nil
	},
}

// sessionWorkspace answers the workspace root the current cwd resolves
// to against the CLI's own [workspace] mapping: the tag every
// chat-init path records for the chat ("" = unmapped cwd — the chat is
// minted untagged). The resolution lives here, the CLI side, because it
// is the session's cwd the daemon can never see; the daemon validates the
// value against its own mapping and records it.
func sessionWorkspace(ctx *Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("chat init: resolve the session cwd: %w", err)
	}
	return ctx.Cfg.Mapping.MatchWorkspace(cwd), nil
}
