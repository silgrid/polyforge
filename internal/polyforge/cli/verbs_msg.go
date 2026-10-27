package cli

// verbs_msg.go: the user-to-user messaging verbs (workbench#11).
//
//	pf msg send --to <user> --text <text> [--about-wi <wi>] [--reply-to <msg>]
//	pf msg list [--conversation <id>] [--after <seq>] [--limit N]
//	pf msg show <id>
//	pf msg analyze <id>
//
// send/list/show are data-plane (direct to aihub with the human's key);
// analyze is execution-plane (the recipient's daemon runs a read-only,
// budget-capped one-shot). All support --json passthrough.

import (
	"flag"
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

var cmdMsg = &Command{
	Name:    "msg",
	Group:   GroupData,
	Summary: "User-to-user messages: direct conversations with permanent, server-owned history",
	Usage: "pf msg send --to <user> --text <text> [--about-wi <wi>] [--reply-to <msg>]\n" +
		"       pf msg list [--conversation <id>] [--after <seq>] [--limit N]\n" +
		"       pf msg show <id>\n" +
		"       pf msg analyze <id>",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("msg: expected send|list|show|analyze")
		}
		sub, rest := args[0], args[1:]
		switch sub {
		case "send":
			f := subFlags(ctx, "msg send")
			to := f.String("to", "", "recipient human user id")
			text := f.String("text", "", "message text (<=4096 bytes)")
			about := f.String("about-wi", "", "optional work-item anchor (never a route)")
			replyTo := f.String("reply-to", "", "optional message id in the same conversation")
			rest, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(rest) != 0 || *to == "" || *text == "" {
				return fmt.Errorf("msg send: --to and --text are required")
			}
			raw, err := ctx.aihub().SendMessage(api.SendMessage{To: *to, Text: *text, AboutWI: *about, ReplyTo: *replyTo})
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(m *api.Message) {
				fmt.Fprintf(ctx.Stdout, "sent %s to %s (conversation %s)\n", m.ID, *to, m.ConversationID)
			})
			return nil
		case "list":
			f := subFlags(ctx, "msg list")
			conv := f.String("conversation", "", "conversation id (without it: list conversations)")
			after := f.Int64("after", 0, "only messages with seq above this cursor")
			limit := f.Int("limit", 0, "page size (default 50, max 200)")
			rest, err := parseCommandArgs(f, rest)
			if err != nil {
				return err
			}
			if len(rest) != 0 {
				return fmt.Errorf("msg list: no positional arguments")
			}
			if *conv != "" {
				raw, err := ctx.aihub().ListMessages(*conv, *after, 0, *limit)
				if err != nil {
					return err
				}
				finishAs(ctx, raw, func(p *api.MessagePage) { renderMessages(ctx.Stdout, p.Messages) })
				return nil
			}
			raw, err := ctx.aihub().ListConversations()
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(l *api.ConversationsAnswer) { renderConversations(ctx.Stdout, l.Conversations) })
			return nil
		case "show":
			if len(rest) != 1 {
				return fmt.Errorf("msg show: expected exactly one message id")
			}
			raw, err := ctx.aihub().GetMessage(rest[0])
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(m *api.Message) { renderMessages(ctx.Stdout, []api.Message{*m}) })
			return nil
		case "analyze":
			if len(rest) != 1 {
				return fmt.Errorf("msg analyze: expected exactly one message id")
			}
			raw, err := ctx.daemon().AnalyzeMessage(rest[0])
			if err != nil {
				return err
			}
			finishAs(ctx, raw, func(r *api.AnalyzeResult) {
				fmt.Fprintf(ctx.Stdout, "analysis of %s", r.MessageID)
				if r.Model != "" {
					fmt.Fprintf(ctx.Stdout, " (model %s)", r.Model)
				}
				fmt.Fprintf(ctx.Stdout, ":\n%s\n", r.Conclusion)
			})
			return nil
		default:
			return fmt.Errorf("msg: unknown action %q", sub)
		}
	},
}

func renderConversations(w interface{ Write([]byte) (int, error) }, convs []api.Conversation) {
	fmt.Fprintf(w, "%d conversation(s)\n", len(convs))
	for _, c := range convs {
		fmt.Fprintf(w, "  %-20s %-6s last=%d unread=%d members=%v\n", c.ID, c.Kind, c.LastSeq, c.Unread, c.Members)
	}
}

func renderMessages(w interface{ Write([]byte) (int, error) }, msgs []api.Message) {
	for _, m := range msgs {
		anchor := ""
		if m.AboutWI != "" {
			anchor = " about=" + m.AboutWI
		}
		fmt.Fprintf(w, "#%d %s %s->%s%s: %s\n", m.Seq, m.ID, m.SenderID, m.ConversationID, anchor, msgText(m.Text))
	}
}

// msgText keeps a remote message on one printable line for the human
// render (terminal escapes and embedded newlines never reach the
// operator's terminal; --json still passes the raw text through).
func msgText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
