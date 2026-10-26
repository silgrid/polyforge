package daemon

// chatsubs.go: the daemon-held half of `pf watch` (v2-converge §3/§4).
//
//   - <data-dir>/chat-subs.json: per chat id, the subscribed WIs with their
//     event cursor and the state-derived decision keys already delivered.
//     Written atomically. A chat id is a notification label, not a
//     credential.
//   - routes under /chats/{id}/..., the per-WI GET /jobs/{wi}/watch snapshot
//     (execution.json authority + events after a cursor + job bookkeeping).
//
// Notification routing: nothing here pushes. A submit subscribes its chat;
// every watcher cycle subscribes its chat to every held WI the chat
// submitted or its owner owns (subscribe-owned). Decisions stay un-acked
// (cursor not advanced) until a watcher of a subscribed chat prints them.
//
// Wake target (polyforge#199): a WI's decisions wake ONE chat — its
// origin chat (the one that submitted it) — not every chat of the owner.
// A subscription owner routing added to another chat records that origin
// chat and shares the origin chat's delivery record (cursor, delivered
// keys, done): it is answered with that record, and its acks land there.
// So a decision is delivered once for the origin chat and its
// owner-routed chats together — whichever of them printed it, none
// prints it again. While the origin chat's watcher is live the others
// are Passive (progress only, no decision, no ack); when it is not, the
// lowest-id live one of them is the fallback and prints only what the
// record says nobody has delivered yet. A WI without an origin chat, or
// whose origin chat holds no subscription to it (unsubscribed there),
// scopes its decision wake to its project-mapping workspace:
// the live subscribed chats tagged with that workspace are wake targets
// of their own records — flagged Adoptable, one of them may `pf watch
// adopt` the role over — while every other owner-routed subscriber is
// Passive and an explicit subscriber stays a wake target of its own
// record, untouched. An eligible chat's ack lands durably in those
// Passive subscribers' records too (see Ack), so what the scoped
// audience delivered never replays when the scope later lapses. With no
// eligible chat the owner-wide broadcast of
// before applies, unchanged, with no new output. An explicit subscribe
// is a wake target of its own record in every case. "Live" is the
// in-memory last subscribe-owned call of the chat's watcher (every poll
// makes one), within chatLiveFor.
//
// Wake-target handover: an unsubscribed chat's dropped
// delivery record is RETAINED on its chatDoc (Records) — `pf watch
// adopt <wi>` (see Adopt) unions the current wake target's retained
// record into the adopter's subscription, re-points every owner-routed
// subscription at the adopter and rewrites the WI's origin chat to it;
// nothing either side already delivered replays.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

const chatSubsFile = "chat-subs.json"

var chatIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type subsDoc struct {
	Version int                 `json:"version"`
	Chats   map[string]*chatDoc `json:"chats"`
}

type chatDoc struct {
	Subs map[string]*protocol.ChatSub `json:"subs"`
	// Workspace is the chat's workspace tag: the workspace root
	// its session cwd resolved to at `pf chat init` ("" = untagged —
	// neither adopt-eligible nor in a scoped decision broadcast). Absent
	// on chats minted before the tag existed: no backfill; the session's
	// next init records one, and every init refreshes it.
	Workspace string `json:"workspace,omitempty"`
	// Records holds the delivery records of the subscriptions the chat
	// dropped (unsubscribed): the record survives there for a later
	// adopter to inherit (nothing replays on an adopt). Subscribe
	// deletes a retained record (a fresh subscription starts fresh);
	// Adopt consumes it.
	Records map[string]*protocol.ChatSub `json:"records,omitempty"`
	// Muted holds the WIs the chat explicitly unsubscribed from: the
	// owner routing (SubscribeNew) never re-adds them; an explicit
	// subscribe clears the mark.
	Muted map[string]bool `json:"muted,omitempty"`
}

// chatLiveFor is how long a chat counts as live after its watcher's last
// subscribe-owned call (a resident watcher makes one every poll, 2s by
// default). Silence does not prove the chat is gone: a Claude Code chat
// has no watcher while it handles a --wait-decision wake, and a --poll
// above chatLiveFor looks silent too. That only decides who prints a
// decision NOBODY has delivered yet — what the origin chat already got
// is in the shared record and never goes to a fallback.
const chatLiveFor = time.Minute

// subStore persists subscriptions and cursors.
type subStore struct {
	mu   sync.Mutex
	path string
	doc  subsDoc
	// seen: when each chat's watcher last called subscribe-owned. In
	// memory only: after a daemon start a chat not seen yet counts as
	// live for chatLiveFor from born (see liveLocked).
	seen map[string]time.Time
	born time.Time
	now  func() time.Time
	// scope answers the wake-target facts of a WI the scoped-broadcast
	// computation reads: its origin chat id and its
	// project-mapping workspace root. Wired by the daemon (in-memory
	// resolvers; nil in a bare store — the scoped broadcast then never
	// fires, which is exactly today's semantics).
	scope wakeScope
}

// wakeScope is the subStore's view of the runtime's per-WI facts (the
// daemon wires it; one fact, one place — the job record owns them, and
// both facts still answer after the runtime dropped the job: a terminal
// close is exactly the decision that fires the scoped wake, so the
// daemon's resolvers read them through runtime.ScopeOf, which retains
// the dropped record's facts).
type wakeScope struct {
	// originChat answers the WI's origin chat id (the job record's
	// Origin.ChatID, the same field a resume rewrites; "" = none — a
	// chat-less submit or a missing origin record).
	originChat func(wi string) string
	// workspace answers the WI's project-mapping workspace root (the
	// job record's Project through the [workspace] inverse; "" =
	// unmapped — the eligible set is then necessarily empty).
	workspace func(wi string) string
}

// newSubStore loads (or starts) the store at path.
func newSubStore(path string) (*subStore, error) {
	s := &subStore{path: path, doc: subsDoc{Version: 1, Chats: map[string]*chatDoc{}},
		seen: map[string]time.Time{}, now: time.Now}
	s.born = s.now()
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.doc); err != nil {
			return nil, fmt.Errorf("%s unparsable: %w", chatSubsFile, err)
		}
		if s.doc.Chats == nil {
			s.doc.Chats = map[string]*chatDoc{}
		}
	case !os.IsNotExist(err):
		return nil, err
	}
	return s, nil
}

func (s *subStore) persistLocked() error {
	blob, err := json.MarshalIndent(&s.doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, blob, 0o600)
}

func (s *subStore) chatLocked(id string, create bool) *chatDoc {
	c := s.doc.Chats[id]
	if c == nil && create {
		c = &chatDoc{Subs: map[string]*protocol.ChatSub{}}
		s.doc.Chats[id] = c
	}
	if c != nil && c.Subs == nil {
		c.Subs = map[string]*protocol.ChatSub{}
	}
	return c
}

// TagWorkspace records (or refreshes) the chat's workspace tag (
// `pf chat init"): the workspace root its session cwd resolved to, ""
// = untagged. The chat is recorded even untagged — an untagged chat is
// a recorded fact (neither adopt-eligible nor in a scoped broadcast),
// not an absent one. One atomic write under the store's own mutex.
func (s *subStore) TagWorkspace(chat, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, true)
	c.Workspace = workspace
	return s.persistLocked()
}

// WorkspaceOf answers the chat's recorded workspace tag ("" = untagged
// or never recorded). In-memory only, no persist.
func (s *subStore) WorkspaceOf(chat string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chatLocked(chat, false); c != nil {
		return c.Workspace
	}
	return ""
}

// Subscribe adds (or re-arms) a subscription; cursor is the event seq the
// watcher should start after for a NEW subscription (history is not
// replayed), slug the WI's display name ("" = unknown). Re-subscribing a
// finished WI re-arms it. Reports whether anything changed.
func (s *subStore) Subscribe(chat, wi, slug string, cursor int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, true)
	unmuted := c.Muted[wi]
	delete(c.Muted, wi)
	// a fresh subscription starts fresh (ruling 3): the record the chat
	// retained when it unsubscribed is dropped, not resumed.
	delete(c.Records, wi)
	if sub, ok := c.Subs[wi]; ok {
		fill := sub.Slug == "" && slug != ""
		if fill {
			sub.Slug = slug // a subscription recorded before its slug was known
		}
		if sub.OriginChat != "" {
			// an explicit subscribe makes an owner-routed subscription
			// a wake target of its own, with a record of its own that
			// starts like a new subscription
			sub.OriginChat, sub.Cursor, sub.Notified, sub.Done = "", cursor, nil, false
			return true, s.persistLocked()
		}
		if !sub.Done {
			if fill || unmuted {
				return false, s.persistLocked()
			}
			return false, nil
		}
		sub.Done, sub.Notified = false, nil
		return true, s.persistLocked()
	}
	c.Subs[wi] = &protocol.ChatSub{WI: wi, Slug: slug, Cursor: cursor}
	return true, s.persistLocked()
}

// SubscribeNew adds a subscription only when the chat has none for wi
// and has not muted it (a finished one is left finished); originChat is
// the WI's origin chat when it is not this chat ("" otherwise): while
// that chat holds a subscription to wi, this one shares its record and
// cursor is not used. The added subscription is marked OwnerRouted
// an adopt re-points it at the adopter; an explicit subscribe
// clears the mark. Reports whether it added one.
func (s *subStore) SubscribeNew(chat, wi, slug, originChat string, cursor int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, true)
	if c.Subs[wi] != nil || c.Muted[wi] {
		return false, nil
	}
	c.Subs[wi] = &protocol.ChatSub{WI: wi, Slug: slug, Cursor: cursor, OriginChat: originChat, OwnerRouted: true}
	return true, s.persistLocked()
}

// Adopt transfers the WI's wake-target role to the adopter chat
// (`pf watch adopt <wi>`): the adopter's subscription becomes
// the WI's single delivery record — the union of the current wake
// target's retained record (what the target kept when it unsubscribed;
// absent = nothing to inherit) and the adopter's own delivered keys —
// so nothing either side already delivered replays. An adopter that
// holds no subscription gets one created (its own delivered keys =
// none); the adopter's mute mark on the WI is cleared (a stronger act
// than the explicit subscribe it mirrors). Every OTHER owner-routed
// subscription to the WI — including a cross-workspace one — is
// re-pointed at the adopter (OriginChat = adopter), exactly as under a
// live origin; explicit subscribers (OwnerRouted clear) stay wake
// targets of their own records, untouched. The target's retained
// record is consumed (deleted): after the adopt the adopter's
// subscription is the single delivery record. Idempotent by
// construction — re-running the surgery over an already-adopted state
// changes nothing. One atomic write under the store's own mutex.
// slug fills a created subscription's display name.
func (s *subStore) Adopt(chat, wi, slug, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		inherited *protocol.ChatSub
		tc        *chatDoc
	)
	if target != "" {
		if tc = s.chatLocked(target, false); tc != nil {
			inherited = tc.Records[wi]
		}
	}
	c := s.chatLocked(chat, true)
	delete(c.Muted, wi) // adopt mirrors the explicit subscribe that clears the mark
	sub := c.Subs[wi]
	if sub == nil {
		sub = &protocol.ChatSub{WI: wi, Slug: slug}
		c.Subs[wi] = sub
	} else if sub.Slug == "" && slug != "" {
		sub.Slug = slug // a subscription recorded before its slug was known
	}
	// the adopter's subscription is a wake target of its own record now
	sub.OriginChat, sub.OwnerRouted = "", false
	if inherited != nil {
		if inherited.Cursor > sub.Cursor {
			sub.Cursor = inherited.Cursor // events either side consumed never replay
		}
		sub.Notified = unionNotified(sub.Notified, inherited.Notified)
		sub.Done = sub.Done || inherited.Done
		if tc != nil {
			delete(tc.Records, wi) // consumed: one delivery record, one place
		}
	}
	for id, other := range s.doc.Chats {
		if id == chat {
			continue
		}
		if o := other.Subs[wi]; o != nil && o.OwnerRouted {
			o.OriginChat = chat
		}
	}
	return s.persistLocked()
}

// unionNotified answers the union of two delivered-key sets, deduped
// and sorted (the adopter's record = its own ∪ the inherited keys).
func unionNotified(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, keys := range [][]string{a, b} {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// HoldsSubscription reports whether the chat holds a subscription to
// the WI (a muted chat never does — mute is the dropped subscription's
// shadow). In-memory only, no persist; the adopt route's live-target
// refusal reads it.
func (s *subStore) HoldsSubscription(chat, wi string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holdsLocked(chat, wi)
}

// Seen records that the chat's watcher is polling now.
func (s *subStore) Seen(chat string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[chat] = s.now()
}

// Settled reports whether SubscribeNew would leave wi alone for the
// chat: it holds a subscription (live or finished) or muted it.
func (s *subStore) Settled(chat, wi string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, false)
	return c != nil && (c.Subs[wi] != nil || c.Muted[wi])
}

// Unsubscribe drops the chat's subscription to wi and mutes wi, so the
// owner routing does not re-add it. The dropped subscription's delivery
// record (cursor, delivered keys, done) is RETAINED on the chat:
// a later adopter inherits it, so nothing the chat already saw replays
// after a handover. Reports whether a subscription was dropped.
func (s *subStore) Unsubscribe(chat, wi string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, true)
	had := c.Subs[wi] != nil
	if !had && c.Muted[wi] {
		return false, nil
	}
	if sub := c.Subs[wi]; sub != nil {
		if c.Records == nil {
			c.Records = map[string]*protocol.ChatSub{}
		}
		c.Records[wi] = sub
	}
	delete(c.Subs, wi)
	if c.Muted == nil {
		c.Muted = map[string]bool{}
	}
	c.Muted[wi] = true
	return had, s.persistLocked()
}

// List answers the chat's subscriptions, sorted by WI. An owner-routed
// one whose origin chat holds a subscription is answered with that
// record, Passive unless this chat is the WI's wake target now. A WI
// whose origin chat is gone (unsubscribed or none) and whose workspace
// holds a live subscribed chat scopes its decision wake: the
// eligible chats are wake targets of their own records with Adoptable
// set (the adopt hint flag), every other owner-routed subscriber is
// Passive, and an explicit subscriber stays a wake target of its own
// record, untouched. With an empty eligible set today's owner-wide
// semantics apply unchanged — every subscribed chat wakes its own
// record, no Passive, no flag, no new output.
func (s *subStore) List(chat string) []protocol.ChatSub {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []protocol.ChatSub{}
	c := s.chatLocked(chat, false)
	if c == nil {
		return out
	}
	scoped := map[string]*scopedWake{}
	for _, sub := range c.Subs {
		cp := *sub
		if o := s.recordLocked(sub); o != sub {
			cp.Cursor, cp.Notified, cp.Done = o.Cursor, o.Notified, o.Done
			cp.Passive = !s.fallbackLocked(chat, sub)
		} else if w, ok := s.scopedWakeOfLocked(scoped, sub.WI); ok && w.any {
			// the WI's origin is gone and its workspace holds a live
			// subscribed chat: the decision wake is scoped
			if w.eligible[chat] {
				// this chat IS one of them: a wake target of its own
				// record, flagged for the adopt hint
				cp.Adoptable = true
			} else if sub.OwnerRouted {
				// an owner-routed subscriber outside the eligible set:
				// Passive — the owner-wide fanout is what stops
				cp.Passive = true
			}
			// an explicit subscriber stays a wake target of its own
			// record, untouched (design ruling 5)
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WI < out[j].WI })
	return out
}

// scopedWake is the scoped-broadcast fact set of one WI.
type scopedWake struct {
	// eligible: the chat ids holding a subscription to the WI, tagged
	// with its project-mapping workspace, live (chatLiveFor). A muted
	// chat holds no subscription, so it is excluded by construction.
	// Evaluated per poll: state-backed decisions persist until acted
	// on, and a same-workspace chat that becomes live later joins then —
	// for decisions not delivered yet (an eligible chat's ack marks a
	// delivered one delivered for the Passive subscribers too, see Ack).
	eligible map[string]bool
	// any: the eligible set is non-empty — the scoped broadcast fires;
	// empty falls back to today's owner-wide semantics.
	any bool
}

// scopedWakeOfLocked answers the scoped-broadcast facts of one WI,
// cached per List answer; ok=false when its origin chat is NOT gone
// (there is one and it holds a subscription to the WI — the
// wake-target model's record sharing applies, and after an adopt the
// condition is false by construction: the adopter IS the origin and
// holds the subscription, so no further scoped broadcast fires). A
// bare store (no scope resolvers) answers not-gone: today's semantics.
func (s *subStore) scopedWakeOfLocked(cache map[string]*scopedWake, wi string) (*scopedWake, bool) {
	if w, ok := cache[wi]; ok {
		return w, w != nil
	}
	var w *scopedWake
	if s.scope.originChat != nil && s.scope.workspace != nil {
		if origin := s.scope.originChat(wi); origin == "" || !s.holdsLocked(origin, wi) {
			ws := s.scope.workspace(wi)
			eligible := map[string]bool{}
			if ws != "" {
				for id, c := range s.doc.Chats {
					if c.Subs[wi] != nil && c.Workspace == ws && s.liveLocked(id) {
						eligible[id] = true
					}
				}
			}
			w = &scopedWake{eligible: eligible, any: len(eligible) > 0}
		}
	}
	cache[wi] = w
	return w, w != nil
}

// holdsLocked reports whether the chat holds a subscription to the WI
// (the store-internal half of HoldsSubscription; a muted chat never
// does — mute is the dropped subscription's shadow).
func (s *subStore) holdsLocked(chat, wi string) bool {
	c := s.chatLocked(chat, false)
	return c != nil && c.Subs[wi] != nil
}

// recordLocked answers the delivery record a subscription reads and
// acks: the origin chat's subscription to the WI for an owner-routed
// one, while the origin chat holds it; the subscription itself
// otherwise.
func (s *subStore) recordLocked(sub *protocol.ChatSub) *protocol.ChatSub {
	if sub.OriginChat == "" {
		return sub
	}
	if c := s.doc.Chats[sub.OriginChat]; c != nil && c.Subs[sub.WI] != nil {
		return c.Subs[sub.WI]
	}
	return sub
}

// fallbackLocked: the chat is the wake target of its owner-routed
// subscription — the origin chat's watcher is not live and no live chat
// with a lower id shares the same record.
func (s *subStore) fallbackLocked(chat string, sub *protocol.ChatSub) bool {
	if s.liveLocked(sub.OriginChat) {
		return false
	}
	for id, c := range s.doc.Chats {
		if o := c.Subs[sub.WI]; id < chat && o != nil && o.OriginChat == sub.OriginChat && s.liveLocked(id) {
			return false
		}
	}
	return true
}

// liveLocked: the chat's watcher called subscribe-owned within
// chatLiveFor; a chat not seen since the daemon started counts as live
// for the daemon's first chatLiveFor (its watcher reconnects within its
// backoff, ≤30s, so a restart hands nothing to a fallback).
func (s *subStore) liveLocked(chat string) bool {
	now := s.now()
	if t, ok := s.seen[chat]; ok {
		return now.Sub(t) < chatLiveFor
	}
	return now.Sub(s.born) < chatLiveFor
}

// Ack records a watcher's progress in the subscription's record (the
// origin chat's for an owner-routed one, see recordLocked); unknown WIs
// are ignored (the subscription may have been removed meanwhile).
//
// Under a scoped decision wake an eligible chat's ack also
// lands in the record of every owner-routed subscriber outside the
// eligible set — the Passive ones, which never ack themselves — the way
// an ack lands in a shared record: cursor max, delivered keys set to the
// acker's (still-pending keys only, so a key that cleared is pruned and
// its recurrence fires again), done or. What the scoped audience
// delivered is then delivered for them too, durably, in the same atomic
// write. The eligible set is volatile (liveness, the runtime's in-memory
// scope facts); the delivered keys are not, so a later owner-wide
// fallback (a daemon restart, every eligible chat gone quiet) prints only
// what the scoped audience has not delivered — the shared-record
// semantics of a live origin. Explicit subscribers and the other
// eligible chats keep their own records, untouched.
func (s *subStore) Ack(chat string, a protocol.ChatAck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chatLocked(chat, false)
	if c == nil || c.Subs[a.WI] == nil {
		return nil
	}
	sub := s.recordLocked(c.Subs[a.WI])
	sub.Cursor, sub.Notified, sub.Done = a.Cursor, a.Notified, a.Done
	if sub == c.Subs[a.WI] {
		if w, ok := s.scopedWakeOfLocked(map[string]*scopedWake{}, a.WI); ok && w.eligible[chat] {
			for id, other := range s.doc.Chats {
				o := other.Subs[a.WI]
				if o == nil || !o.OwnerRouted || w.eligible[id] || s.recordLocked(o) != o {
					continue
				}
				if sub.Cursor > o.Cursor {
					o.Cursor = sub.Cursor
				}
				o.Notified = append([]string(nil), sub.Notified...)
				o.Done = o.Done || sub.Done
			}
		}
	}
	return s.persistLocked()
}

// ---------------------------------------------------------------------------
// Routes.
// ---------------------------------------------------------------------------

// chatID validates the path chat id (it names files on the watcher side).
func chatID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !chatIDRe.MatchString(id) {
		writeErr(w, api.ErrDaemonBadInput, "chat id must be 1-64 characters of [A-Za-z0-9_-]")
		return "", false
	}
	return id, true
}

func (d *Daemon) subscriptionsRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, protocol.ChatSubscriptions{ChatID: id, Subscriptions: d.subs.List(id)})
}

func (d *Daemon) subscribeRoute(subscribe bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := chatID(w, r)
		if !ok {
			return
		}
		var body struct {
			WI string `json:"wi"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.WI) == "" {
			writeErr(w, api.ErrDaemonBadInput, "wi is required")
			return
		}
		wi := d.canonWI(strings.TrimSpace(body.WI))
		var (
			changed bool
			err     error
		)
		slug := d.slugOf(wi)
		if subscribe {
			changed, err = d.subs.Subscribe(id, wi, slug, d.lastSeq(wi))
		} else {
			changed, err = d.subs.Unsubscribe(id, wi)
		}
		if err != nil {
			writeErr(w, api.ErrDaemonInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, protocol.ChatSubscribeAnswer{ChatID: id, WI: wi, Slug: slug, Changed: changed})
	}
}

func (d *Daemon) ackRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	var a protocol.ChatAck
	if !decodeBody(w, r, &a) {
		return
	}
	if a.WI == "" || a.Cursor < 0 {
		writeErr(w, api.ErrDaemonBadInput, "ack needs wi and a non-negative cursor")
		return
	}
	if err := d.subs.Ack(id, a); err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// subscribeOwnedRoute is POST /chats/{id}/subscribe-owned {"owner"}:
// every watcher cycle subscribes its chat to every held work item the
// chat submitted or the owner owns (origin owner, else the WI reporter)
// that the chat has no subscription for and has not unsubscribed from,
// and gets the added ones back. A
// new subscription's cursor sits just before the WI's newest decision
// event, so a decision nobody has been told about yet is replayed; the
// state-backed decisions come from the snapshot anyway. A WI the chat is
// already subscribed to (or muted) costs one map lookup, and a reporter is fetched
// from the hub once per WI, so the per-cycle call stays cheap.
func (d *Daemon) subscribeOwnedRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	var body struct {
		Owner string `json:"owner"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	owner := strings.TrimSpace(body.Owner)
	d.subs.Seen(id) // every watcher poll calls this route: the chat is live
	added := []protocol.ChatSub{}
	jobs, _ := d.statusJobs()
	for _, j := range jobs {
		if d.subs.Settled(id, j.WorkItemID) {
			continue
		}
		mine := j.Origin != nil && j.Origin.ChatID == id
		if !mine && !(owner != "" && d.ownerOf(j) == owner) {
			continue
		}
		cursor := d.lastSeq(j.WorkItemID)
		if e, ok := d.obs.Events().Newest(j.WorkItemID, watcher.IsEventDecision); ok {
			cursor = e.Seq - 1
		}
		originChat := ""
		if !mine && j.Origin != nil {
			originChat = j.Origin.ChatID
		}
		fresh, err := d.subs.SubscribeNew(id, j.WorkItemID, j.Slug, originChat, cursor)
		if err != nil {
			writeErr(w, api.ErrDaemonInternal, err.Error())
			return
		}
		if fresh {
			added = append(added, protocol.ChatSub{WI: j.WorkItemID, Slug: j.Slug, Cursor: cursor, OriginChat: originChat})
		}
	}
	writeJSON(w, http.StatusOK, protocol.ChatSubscriptions{ChatID: id, Subscriptions: added})
}

// ownerOf answers a WI's owner: the origin owner, else the WI reporter
// (a WI's reporter never changes: the first successful hub read is kept).
func (d *Daemon) ownerOf(j statusJob) string {
	if j.Origin != nil && j.Origin.Owner != "" {
		return j.Origin.Owner
	}
	if r, ok := d.reporters.Load(j.WorkItemID); ok {
		return r.(string)
	}
	if d.hub != nil {
		if item, err := d.hub.GetWorkItem(j.WorkItemID); err == nil && item != nil {
			d.reporters.Store(j.WorkItemID, item.Reporter)
			return item.Reporter
		}
	}
	return ""
}

// autoSubscribe subscribes the submitting chat (when the request carries
// one) to the WI, starting after cursor.
func (d *Daemon) autoSubscribe(o *reqOrigin, wi string, cursor int64) {
	if d.subs == nil || o == nil || !chatIDRe.MatchString(o.ChatID) {
		return
	}
	if _, err := d.subs.Subscribe(o.ChatID, wi, d.slugOf(wi), cursor); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: auto-subscribe %s to %s: %v\n", o.ChatID, wi, err)
	}
}

// slugOf answers a WI's display name: the local job record's slug, else
// the aihub record's; "" when neither knows it (callers show the id).
func (d *Daemon) slugOf(wi string) string {
	if ji, ok := d.rt.Info(wi); ok && ji.Slug != "" {
		return ji.Slug
	}
	if d.hub != nil {
		if item, err := d.hub.GetWorkItem(wi); err == nil && item != nil {
			return item.Slug
		}
	}
	return ""
}

// lastSeq is the highest retained event seq of a WI (0 = none).
func (d *Daemon) lastSeq(wi string) int64 {
	if d.obs == nil {
		return 0
	}
	return d.obs.Events().LastSeq(wi)
}

// ---------------------------------------------------------------------------
// GET /jobs/{wi}/watch?after=<seq>
// ---------------------------------------------------------------------------

func (d *Daemon) watchSnapshotRoute(w http.ResponseWriter, r *http.Request) {
	var after int64
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, api.ErrDaemonBadInput, "after must be a non-negative integer cursor")
			return
		}
		after = n
	}
	wi := r.PathValue("wi")
	canonical, item := d.resolveWI(wi)
	// the resolution already read aihub (record or failure): the snapshot
	// does not read it again
	hubRead := item != nil || canonical == ""
	if canonical == "" {
		canonical = wi
	}
	// polyforge#224: the demand marker — exactly the value 1 marks this
	// snapshot request as human-side demand for the summarize lane; any
	// other value is unmarked (ignored, no error — the route stays a
	// read). The marker is honored only when the UDS peer credential is
	// available AND the peer is not a step process tree of this daemon
	// (the same machinery and posture as the step-origin guard, never
	// anything from the request body): an unavailable cred cannot prove
	// human-side, so the marker is ignored — fail closed, no error, the
	// route still answers as a read.
	if r.URL.Query().Get("demand") == "1" && d.humanSidePeer(r) {
		d.summarizeDemand(canonical)
	}
	writeJSON(w, http.StatusOK, d.watchSnapshot(canonical, item, hubRead, after))
}

// humanSidePeer reports whether the request's UDS peer may mark demand:
// the peer credential is available and the peer is NOT a step process
// tree of this daemon (runtime.StepProcessOrigin — the same machinery
// the step-origin guard uses). Unavailable → false (fail closed: the
// marker is ignored, never an error).
func (d *Daemon) humanSidePeer(r *http.Request) bool {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return false
	}
	if _, isStep := runtime.StepProcessOrigin(cred.pid, d.cfg.MachineID); isStep {
		return false
	}
	return true
}

// summarizeDemand arms the lane for one human-side demand-marked
// snapshot request: the demand-marked request IS the demand — its gates
// run on it (no timestamp is recorded; the demand is fresh by
// construction) — over the caller-resolved dispatch facts, and the route
// answers immediately with the pre-call state: the call runs in the
// background, off the request path, and no lock guarding daemon shared
// data is held across it or its landing.
func (d *Daemon) summarizeDemand(wi string) {
	ji, ok := d.rt.Info(wi)
	if !ok || ji.CapsuleDir == "" {
		return
	}
	d.lane.Marked(wi, ji.SummarizeModels, ji.SummarizeLanguage, time.Duration(ji.SummarizeIntervalSec)*time.Second, d.resolveDispatch(ji))
}

// resolveDispatch answers the current dispatch's live-transcript facts
// (token, path, size) for the job — gated on a LIVE RUNNING STEP
// DISPATCH (assumption A11: a summarize call happens only inside a
// running step attempt). The gate is stepDispatchLive (the job runs
// with a live driver, the execution record's current step in_progress
// and not parked on a question) plus the dispatch's own process alive
// (the spawn journal's latest entry's pid): a needs_input (parked),
// paused, between-steps or finished WI has no live dispatch — its
// journal's latest entry, however resolvable, is no source (the growth
// gate alone cannot see that: a finished dispatch's entry and
// transcript still resolve, and a fresh lane's zero position makes any
// size a growth) — so the zero Dispatch answers (no source → no growth
// → no call). The transcript resolves through the shared per-dispatch
// rule (runtime.LiveTranscript — the same function the observe lane's
// commit-boundary sync sources from: a resumed pi dispatch's live
// transcript is the capsule file itself, every other dispatch's the
// session store / claude native session). The journal is
// agent-writable, so the entry's spawn token is only the reset key
// (never a trust boundary — the transcript path comes from that rule);
// a missing journal, a dead pid, no resolved transcript or an unreadable
// stat answers the zero Dispatch.
func (d *Daemon) resolveDispatch(ji runtime.JobInfo) summarize.Dispatch {
	if !stepDispatchLive(ji) {
		return summarize.Dispatch{}
	}
	entries, err := capsule.ReadSpawnJournal(ji.CapsuleDir)
	if err != nil || len(entries) == 0 {
		return summarize.Dispatch{}
	}
	last := entries[len(entries)-1]
	// the dispatch's own process, probed through the watcher's
	// PIDInfo.Alive — the same signal-0/EPERM probe, one fact in one
	// place, not a second copy of it beside the daemon's journal read
	proc := watcher.PIDInfo{PID: last.PID}
	if !proc.Alive() {
		return summarize.Dispatch{} // the dispatch's process is gone: a dispatch that already ended
	}
	path := d.locateTranscript(ji.CapsuleDir, entries)
	if path == "" {
		return summarize.Dispatch{}
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return summarize.Dispatch{}
	}
	return summarize.Dispatch{Token: last.SpawnToken, Path: path, Size: st.Size()}
}

// stepDispatchLive reports whether the job's current step attempt is
// live and running — the A11 gate's attempt half: the job is running
// with a live driver (the same posture pendingQuestions reads — a
// paused, recovering or sealed job is not running), and the execution
// record's current step is in_progress and not parked on a question
// (execution.json is the driver's single-writer authority on step
// state: a between-steps WI's current step is pending, a finished one
// done, a needs_input one in_progress with a pending question). A job
// with no execution record yet (the claim window before the driver's
// first write) has no running step either.
func stepDispatchLive(ji runtime.JobInfo) bool {
	if ji.State != runtime.StateRunning || ji.Agent == nil {
		return false
	}
	ex := readExecutionFile(filepath.Join(ji.CapsuleDir, capsule.ExecutionJSON))
	if ex == nil {
		return false
	}
	st := ex.Steps[ex.CurrentStep]
	return st != nil && st.Status == capsule.StepInProgress && st.PendingQuestion == nil
}

// watchSnapshot answers one work item's snapshot by its canonical id.
// item is its aihub record when the caller already read it; hubRead
// says the caller already asked aihub (so a nil item is a failed read,
// not one still to make).
func (d *Daemon) watchSnapshot(wi string, item *api.WorkItem, hubRead bool, after int64) protocol.WatchSnapshot {
	snap := protocol.WatchSnapshot{WorkItem: wi, Events: []protocol.EventEntry{}}
	snap.Queued = d.q != nil && d.q.IsPending(wi)
	jobs, _ := d.statusJobs()
	localLive, held := false, false
	for _, j := range jobs {
		if j.WorkItemID != wi {
			continue
		}
		snap.Known, held = true, true
		snap.Slug = j.Slug
		localLive = j.Status != string(runtime.StatePaused)
		wj := &protocol.WatchJob{Status: j.Status, Origin: j.Origin,
			QuestionID: j.QuestionID, LastProgressAgoSec: j.LastWorkAgoSec}
		snap.Job = wj
	}
	// The hub record (read at most once per snapshot, by the resolution
	// or here) serves only when no live local job answers: it names a
	// closed WI's capsule (project + slug), its display name,
	// and — a manual close (wrap / wrap --fail / cancel / pause) leaves
	// execution.json at `running` — its status, the authority for how
	// the WI ended.
	if localLive || (snap.Queued && held) {
		item = nil
	} else if !hubRead && d.hub != nil {
		if it, err := d.hub.GetWorkItem(wi); err == nil && it != nil {
			item = it
		}
	}
	if item != nil {
		if snap.Slug == "" {
			snap.Slug = item.Slug
		}
		if !snap.Queued {
			snap.HubStatus = item.Status
		}
	}
	if exec := d.readExecution(wi, item); exec != nil {
		snap.Known = true
		snap.Execution = exec
	}
	if d.obs != nil {
		// live progress of the in-flight dispatch (current command, recent
		// files, tool calls); absent between steps / when nothing is monitored
		if lp, ok := d.obs.Progress(wi); ok && lp.InDispatch {
			snap.Progress = watchProgress(lp)
		}
		snap.LastSeq = d.lastSeq(wi)
		snap.Events = append(snap.Events, d.obs.Events().SinceAfter(wi, after)...)
	}
	// polyforge#224: the summarize lane's held narration — the four
	// fields projected under the lane's mutex. Nothing waits on a call
	// in flight: the projection answers the pre-call state (which may
	// lag by one call), and a slow or hung call never delays this
	// response.
	if d.lane != nil {
		p := d.lane.Projection(wi)
		snap.Summary, snap.SummaryError, snap.SummaryAgeSec, snap.SummaryCapped = p.Summary, p.SummaryError, p.SummaryAgeSec, p.SummaryCapped
	}
	return snap
}

// watchProgress projects the observe lane's LiveProgress onto the wire
// block; nil when the dispatch has nothing to show yet.
func watchProgress(lp observe.LiveProgress) *protocol.WatchProgress {
	p := &protocol.WatchProgress{Command: lp.Command, RecentFiles: lp.RecentFiles}
	if lp.Command != "" {
		age := lp.CommandAgeSec
		p.CommandAgeSec = &age
	}
	if lp.ToolCalls > 0 {
		n := int64(lp.ToolCalls)
		p.ToolCalls = &n
	}
	if p.Command == "" && len(p.RecentFiles) == 0 && p.ToolCalls == nil {
		return nil
	}
	return p
}

// loadExecution reads the WI's execution.json (the authority), from the
// job record's capsule or, for a WI the daemon no longer holds, from the
// capsule its hub record names: capsules/<project>-<seq> under the
// project's mapped workspace (item nil = no record to fall back on). It
// is the one execution.json reader of the daemon (the watch projection
// and the no-id answer both use it), and answers the capsule dir it read
// it from too ("" with a nil execution).
func (d *Daemon) loadExecution(wi string, item *api.WorkItem) (*capsule.Execution, string) {
	var candidates []string
	if d.rt != nil {
		if ji, ok := d.rt.Info(wi); ok && ji.CapsuleDir != "" {
			candidates = append(candidates, ji.CapsuleDir)
		}
	}
	if len(candidates) == 0 && item != nil && d.cfg.Mapping != nil {
		if ws, err := d.cfg.Mapping.WorkspaceFor(item.ProjectName); err == nil {
			candidates = append(candidates, filepath.Join(ws, "capsules", api.PathName(item.Slug, wi)))
		}
	}
	for _, dir := range candidates {
		if ex := readExecutionFile(filepath.Join(dir, capsule.ExecutionJSON)); ex != nil {
			return ex, dir
		}
	}
	return nil, ""
}

// declaredSteps answers the step sequence of the workflow the capsule's
// driver executes — its pinned bundle (capsule.json bundle_name under
// workflows/), parsed by the driver's own loader — in frontmatter order.
// nil when capsule.json or the workflow.md is missing or unparseable: the
// snapshot then omits the field and nothing else changes.
func declaredSteps(capsuleDir string) []protocol.WatchDeclaredStep {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return nil
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil || idn.BundleName == "" {
		return nil
	}
	wf, err := driver.LoadWorkflow(filepath.Join(capsuleDir, capsule.DirWorkflows, idn.BundleName))
	if err != nil {
		return nil
	}
	out := make([]protocol.WatchDeclaredStep, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		ds := protocol.WatchDeclaredStep{ID: s.ID}
		if s.OnFail != nil {
			target := s.OnFail.Target
			ds.OnFail = &target
		}
		out = append(out, ds)
	}
	return out
}

// readExecutionFile parses one execution.json; nil when absent or torn.
func readExecutionFile(path string) *capsule.Execution {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ex capsule.Execution
	if json.Unmarshal(raw, &ex) != nil {
		return nil
	}
	return &ex
}

// pendingQuestions answers, per running job with a live driver, the id
// of the question its execution.json (the authority) holds at the
// current step — the status face presents such a job as needs_input
// (D6), and an answer to it is accepted. Before the driver is
// (re-)derived nothing could take the answer, so nothing is shown. A
// pending without an id is unanswerable and ignored (the monitor's
// pending_invalid names it).
func (d *Daemon) pendingQuestions() map[string]string {
	out := map[string]string{}
	for _, ji := range d.rt.List() {
		if ji.State != runtime.StateRunning || ji.Agent == nil || ji.CapsuleDir == "" {
			continue
		}
		ex := readExecutionFile(filepath.Join(ji.CapsuleDir, capsule.ExecutionJSON))
		if ex == nil {
			continue
		}
		if st := ex.Steps[ex.CurrentStep]; st != nil && st.PendingQuestion != nil && st.PendingQuestion.ID != "" {
			out[ji.WorkItem] = st.PendingQuestion.ID
		}
	}
	return out
}

// readExecution projects loadExecution onto the watch wire shape.
func (d *Daemon) readExecution(wi string, item *api.WorkItem) *protocol.WatchExecution {
	ex, dir := d.loadExecution(wi, item)
	if ex == nil {
		return nil
	}
	out := &protocol.WatchExecution{
		WorkflowStatus: ex.WorkflowStatus, CurrentStep: ex.CurrentStep, UpdatedAt: ex.UpdatedAt,
		Steps: map[string]protocol.WatchStep{}, DeclaredSteps: declaredSteps(dir),
	}
	ids := make([]string, 0, len(ex.Steps))
	for id := range ex.Steps {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic pick when several steps hold a question
	for _, id := range ids {
		st := ex.Steps[id]
		if st == nil {
			continue
		}
		out.Steps[id] = protocol.WatchStep{Status: st.Status, StartedAt: st.StepStartedAt}
		if st.PendingQuestion != nil && st.PendingQuestion.ID != "" && out.PendingQuestion == nil {
			out.PendingQuestion = &protocol.WatchPendingQuestion{ID: st.PendingQuestion.ID,
				Step: st.PendingQuestion.Step, Question: st.PendingQuestion.Question, AskedAt: st.PendingQuestion.AskedAt}
		}
	}
	return out
}
