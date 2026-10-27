package daemon

// messaging.go: the daemon's live message delivery face (workbench#11).
// It runs only for an interactive daemon whose operator has a second live
// machine (the single-machine idle rule). Each message prints exactly one
// line to the daemon log, then the operator's server-side DELIVERY cursor is
// advanced per message (never the read cursor: delivery clears no unread);
// no local cursor is kept, so a restart resumes from the server and a
// kill -9 replays at most one line.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func (d *Daemon) messageLoop(ctx context.Context) {
	client := d.faceClient()
	if client == nil {
		return
	}
	failStreak := 0
	for ctx.Err() == nil {
		if !d.messageFaceWanted() {
			if !faceSleep(ctx, time.Second) {
				return
			}
			continue
		}
		inbox, err := client.Inbox("messages", inboxWait)
		if err != nil {
			failStreak++
			if failStreak == 1 {
				fmt.Fprintf(os.Stderr, "daemon: message inbox: %v (retrying)\n", err)
			}
			if !faceSleep(ctx, time.Second) {
				return
			}
			continue
		}
		failStreak = 0
		for _, msg := range inbox.Messages {
			fmt.Fprintf(os.Stderr, "daemon: message %s from %s (conversation %s): %s\n",
				msg.ID, msg.SenderID, msg.ConversationID, oneLine(msg.Text))
			if err := client.DeliverMessage(msg.ConversationID, msg.Seq); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: deliver message %s: %v\n", msg.ID, err)
				// The server will hand the same pending rows back; back off
				// instead of spinning on a failing delivery.
				if !faceSleep(ctx, time.Second) {
					return
				}
			}
		}
	}
}

func (d *Daemon) messageFaceWanted() bool {
	d.mfaceMu.Lock()
	defer d.mfaceMu.Unlock()
	return d.face.available && d.face.role == "interactive" && d.face.multiMachine && !d.face.superseded
}

// oneLine collapses agent-authored text to one log line (no terminal
// escapes, no embedded newlines).
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if rs := []rune(out); len(rs) > 120 {
		out = string(rs[:120]) + "…"
	}
	return out
}
