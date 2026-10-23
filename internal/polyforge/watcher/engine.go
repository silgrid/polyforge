package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// API is the daemon face one chat's watcher needs.
type API interface {
	Subscriptions() (protocol.ChatSubscriptions, error)
	Snapshot(wi string, after int64) (*protocol.WatchSnapshot, error)
	Ack(a protocol.ChatAck) error
	// SubscribeOwned subscribes the chat to every work item the daemon
	// holds that this chat submitted or owner owns and the chat has no
	// subscription for, and answers the subscriptions it added. It is
	// idempotent: a WI already subscribed is never added again.
	SubscribeOwned(owner string) ([]protocol.ChatSub, error)
}

// Options configure Run.
type Options struct {
	Owner string
	// WaitDecision: exit 0 after printing the first batch of decisions.
	WaitDecision bool
	Poll         time.Duration
	MaxBackoff   time.Duration
	// Out receives decision lines ONLY; Err receives operational notices.
	Out, Err io.Writer
	// Status writes the progress file (nil = none).
	Status func(lines []string) error
	// ParentAlive, when set, is checked every poll; false ends the run.
	ParentAlive func() bool
	Now         func() time.Time
	// Sleep waits d (or until ctx ends); tests inject a fake clock.
	Sleep func(ctx context.Context, d time.Duration)
}

// PausedLine is the status/notice line while the daemon cannot be reached.
const PausedLine = "daemon unreachable; progress paused"

func (o *Options) defaults() {
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 30 * time.Second
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Err == nil {
		o.Err = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
}

// ErrParentGone ends a run whose harness process disappeared.
var ErrParentGone = errors.New("parent process gone")

// Run is the resident watcher. Every cycle it first subscribes the chat
// to the work items it or its owner holds (so a WI that becomes the
// owner's while the watcher runs is picked up within one cycle; a failed
// call is reported and retried next cycle, it does not stop the cycle),
// then lists the chat's subscriptions and — per WI — reconciles the
// authoritative state BEFORE it replays events after the daemon-held cursor (so a WI that ended
// while the watcher was down is reported, never missed, and a reconnect
// after a daemon restart needs no special case). Decisions print as
// fixed-template lines on Out — those of the work items the first
// cycle subscribed fold into one "N WIs need you" line; progress goes to the
// status file. A Passive subscription (another chat is woken for its WI)
// gets its progress line, none of its decisions and no ack. With WaitDecision
// the first non-empty batch is printed, acked and Run returns nil.
//
// Delivery is fetch-then-ack: nothing the daemon hands out counts as
// delivered until the watcher acks it after printing, so a crash between
// fetch and print loses no line — the next start prints it. A crash
// between print and ack prints that line once more on the next start
// (the only duplicate); a failed ack inside one run is retried without
// re-printing (B-9/B-23).
func Run(ctx context.Context, api API, opts Options) error {
	opts.defaults()
	var (
		backoff    = time.Second
		paused     bool
		lastNotice time.Time
		// owned: the first subscribe-owned call landed; fresh holds the
		// work items it added, until their first decisions are acked.
		// Work items a later call adds are not fresh: their decisions
		// print one by one.
		owned bool
		fresh map[string]bool
		// ownedFailing: the last subscribe-owned call failed and said so;
		// the cycle went on with the subscriptions the chat already has.
		ownedFailing bool
		// unacked holds the keys (outLine.key) of the lines already
		// printed whose ack failed: the daemon still holds the old
		// cursor, so the next cycle re-derives the same decisions —
		// they are not printed again, only their acks retried, until an
		// ack round fully succeeds.
		unacked = map[string]bool{}
	)
	for {
		if opts.ParentAlive != nil && !opts.ParentAlive() {
			return ErrParentGone
		}
		if ctx.Err() != nil {
			return nil
		}
		var (
			lines []string
			b     batch
			err   error
		)
		added, ownErr := api.SubscribeOwned(opts.Owner)
		if ownErr == nil && !owned {
			owned, fresh = true, map[string]bool{}
			for _, sub := range added {
				fresh[sub.WI] = true
			}
		}
		lines, b, err = cycle(api, &opts, fresh)
		if err != nil {
			if !paused || opts.Now().Sub(lastNotice) >= time.Minute {
				fmt.Fprintf(opts.Err, "polyforge watch: %s (%s); retrying in %s\n", pausedText(err), shortErr(err), backoff)
				lastNotice = opts.Now()
			}
			paused = true
			writeStatus(&opts, []string{PausedLine})
			sleepChecked(ctx, &opts, backoff)
			if backoff *= 2; backoff > opts.MaxBackoff {
				backoff = opts.MaxBackoff
			}
			continue
		}
		if paused {
			fmt.Fprintln(opts.Err, "polyforge watch: daemon reachable again; reconciled")
			paused = false
		}
		// A failed subscribe-owned only delays picking up newly owned
		// work items: the subscribed ones keep delivering meanwhile.
		if ownErr != nil && !ownedFailing {
			fmt.Fprintf(opts.Err, "polyforge watch: cannot subscribe the owner's work items (%s); retrying every poll\n", shortErr(ownErr))
		}
		ownedFailing = ownErr != nil
		backoff = time.Second
		writeStatus(&opts, lines)
		printed := 0
		for _, l := range b.out {
			if unacked[l.key] {
				continue
			}
			if _, werr := fmt.Fprintln(opts.Out, l.text); werr != nil {
				return werr // stdout gone: nobody is listening
			}
			printed++
		}
		// Ack only after the lines are out: a crash between print and
		// ack re-delivers (at-least-once), never loses.
		var ackErr error
		for _, a := range b.acks {
			if aerr := api.Ack(a); aerr != nil && ackErr == nil {
				ackErr = aerr
			}
		}
		if ackErr != nil {
			if len(unacked) == 0 {
				fmt.Fprintf(opts.Err, "polyforge watch: ack failed (%s); retrying without re-printing\n", shortErr(ackErr))
			}
			for _, l := range b.out {
				unacked[l.key] = true
			}
		} else {
			unacked = map[string]bool{}
			fresh = nil // the start's reminder is delivered; later decisions print one by one
		}
		if printed > 0 && opts.WaitDecision {
			return nil
		}
		opts.Sleep(ctx, opts.Poll)
	}
}

// sleepChecked sleeps d in poll-sized slices so a long backoff still
// notices the parent going away.
func sleepChecked(ctx context.Context, o *Options, d time.Duration) {
	for d > 0 && ctx.Err() == nil {
		step := d
		if step > o.Poll {
			step = o.Poll
		}
		o.Sleep(ctx, step)
		d -= step
		if o.ParentAlive != nil && !o.ParentAlive() {
			return
		}
	}
}

type batch struct {
	out  []outLine
	acks []protocol.ChatAck
}

// outLine is one line to print; key names the event it reports, so two
// events whose lines read the same are still two lines.
type outLine struct {
	key, text string
}

func pausedText(err error) string {
	var ae *api.Error
	if errors.As(err, &ae) {
		return "daemon error " + ae.Code + "; progress paused"
	}
	return PausedLine
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func writeStatus(o *Options, lines []string) {
	if o.Status == nil {
		return
	}
	if err := o.Status(lines); err != nil {
		fmt.Fprintf(o.Err, "polyforge watch: cannot write the progress file: %v\n", err)
	}
}

// cycle runs one poll: the whole of it fails (returns err) only when the
// daemon cannot list subscriptions or goes away mid-cycle; a single WI's
// failure shows as its progress line and is retried next cycle. The
// decisions of the fresh work items fold into one AttentionLine.
func cycle(d API, o *Options, fresh map[string]bool) ([]string, batch, error) {
	var b batch
	subs, err := d.Subscriptions()
	if err != nil {
		return nil, b, err
	}
	now := o.Now()
	var lines, needYou []string
	for _, sub := range subs.Subscriptions {
		if sub.Done {
			continue
		}
		snap, err := d.Snapshot(sub.WI, sub.Cursor)
		if err == nil && snap.LastSeq < sub.Cursor {
			// The daemon's event seq continues from the work item's events
			// file, so it is monotonic across restarts. It falls below a
			// cursor when that file is gone (wiped, or dropped after 30
			// silent days) while the chat's subscription survived — then
			// replay from the start rather than wait on ids that will
			// never come.
			sub.Cursor = 0
			snap, err = d.Snapshot(sub.WI, 0)
		}
		if err != nil {
			var ae *api.Error
			if !errors.As(err, &ae) {
				return nil, batch{}, err // transport: the daemon went away mid-cycle
			}
			lines = append(lines, safeID(api.DisplayName(sub.Slug, sub.WI))+" unavailable ("+ae.Code+")")
			continue
		}
		lines = append(lines, ProgressLine(snap, now))
		if sub.Passive {
			// another chat is woken for this WI (polyforge#199): progress
			// only. No ack either — the record is that chat's, so what it
			// has not delivered stays undelivered for whichever chat
			// becomes the wake target.
			continue
		}
		res := Evaluate(sub, snap)
		if fresh[sub.WI] {
			if len(res.Decisions) > 0 {
				needYou = append(needYou, api.DisplayName(snap.Slug, sub.WI))
			}
		} else {
			for _, dec := range res.Decisions {
				b.out = append(b.out, outLine{key: dec.id(), text: dec.Line()})
			}
			// a scoped-broadcast decision print carries the one
			// human-readable adopt hint, once per flagged WI per batch that
			// printed at least one of that WI's decision lines — attached to
			// decision-line prints only (the fresh-subscription AttentionLine
			// folds no decision lines, so it never carries the hint). ONLY a
			// hint: nothing in this watcher (or anywhere else) ever adopts on
			// its own — adopt runs on an explicit user request.
			if sub.Adoptable && len(res.Decisions) > 0 {
				b.out = append(b.out, outLine{key: "adopt-hint " + sub.WI, text: AdoptHintLine(sub.Slug, sub.WI)})
			}
		}
		if len(res.Decisions) > 0 || res.Cursor != sub.Cursor || res.Done || !sameKeys(res.Notified, sub.Notified) {
			b.acks = append(b.acks, protocol.ChatAck{WI: sub.WI, Cursor: res.Cursor, Notified: res.Notified, Done: res.Done})
		}
	}
	if len(needYou) > 0 {
		// keyed by its text: the WI list is in it, so a reminder that
		// grew while its ack failed is printed again
		l := AttentionLine(needYou)
		b.out = append([]outLine{{key: "attention " + l, text: l}}, b.out...)
	}
	return lines, b, nil
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
