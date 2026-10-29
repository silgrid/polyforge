package server

// machineinbox.go: the in-memory relay registry and the holder inbox
// long-poll. Progress/artifact queries are NEVER stored:
// a pending entry exists only between enqueue and answer/deadline.

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
)

const (
	progressQueryDeadline = 5 * time.Second
	inboxLongPoll         = 25 * time.Second
)

// The relay admission budget ( A2). A relayed query costs
// its holder one signed inbox wake (laneInbox) and one signed answer
// (laneAnswer), each recorded for machinesign.ReplayTTL; the answer comes
// at most progressQueryDeadline after the admission. Counting admissions
// over relayWindow = TTL + deadline therefore bounds the relay-driven
// entries of both lanes at any instant by relayHolderQuota: 448 answers
// ≤ 512, and 448 wakes plus the idle polls of the query and message loops
// (one per 25 s each, ≤50 per window) = 498 ≤ 512. So no stream of
// queries, from any number of members, can fill a holder's lanes — the
// excess is refused here, before it reaches the holder. relayMemberQuota
// keeps one member (per holder) under three quarters of that budget: it
// is above the ≈303 queries one resident `pf watch` (2 s poll) sends per
// window for one remote work item, and it leaves 128 for every other
// member.
const (
	relayHolderQuota = 448
	relayMemberQuota = 320
	relayWindow      = machinesign.ReplayTTL + progressQueryDeadline
)

// relayBudget admits relayed queries per holder machine and per (member,
// holder) over a sliding window. A refused query records nothing; every
// admit sweeps every key, so memory follows live admissions (at most
// 2 × relayHolderQuota timestamps per holder with live admissions).
type relayBudget struct {
	mu          sync.Mutex
	holderQuota int
	memberQuota int
	window      time.Duration
	byHolder    map[string][]time.Time
	byMember    map[relayMember][]time.Time
}

type relayMember struct{ user, holder string }

func newRelayBudget(holderQuota, memberQuota int, window time.Duration) *relayBudget {
	return &relayBudget{holderQuota: holderQuota, memberQuota: memberQuota, window: window,
		byHolder: map[string][]time.Time{}, byMember: map[relayMember][]time.Time{}}
}

// liveSince keeps the admissions at or after cutoff.
func liveSince(ts []time.Time, cutoff time.Time) []time.Time {
	kept := ts[:0]
	for _, at := range ts {
		if !at.Before(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// retryWithin answers the whole seconds until the oldest admission in ts
// leaves the window (at least 1).
func (b *relayBudget) retryWithin(now time.Time, ts []time.Time) int {
	oldest := ts[0]
	for _, at := range ts[1:] {
		if at.Before(oldest) {
			oldest = at
		}
	}
	secs := int(oldest.Add(b.window).Sub(now)/time.Second) + 1
	if secs < 1 {
		secs = 1
	}
	return secs
}

// admit records one relayed query from userID to holder, or answers the
// typed CORE-429 naming the bound that refused it.
func (b *relayBudget) admit(now time.Time, userID, holder string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := now.Add(-b.window)
	for k, ts := range b.byHolder {
		if ts = liveSince(ts, cutoff); len(ts) == 0 {
			delete(b.byHolder, k)
		} else {
			b.byHolder[k] = ts
		}
	}
	for k, ts := range b.byMember {
		if ts = liveSince(ts, cutoff); len(ts) == 0 {
			delete(b.byMember, k)
		} else {
			b.byMember[k] = ts
		}
	}
	window := int(b.window / time.Second)
	key := relayMember{user: userID, holder: holder}
	if ts := b.byMember[key]; len(ts) >= b.memberQuota {
		return core.Fail(api.ErrCoreTooMany, "",
			"relay refused: you already sent %d relayed queries to holder machine %s in the last %d s (at most %d per member per holder); retry within %d s",
			len(ts), holder, window, b.memberQuota, b.retryWithin(now, ts))
	}
	if ts := b.byHolder[holder]; len(ts) >= b.holderQuota {
		return core.Fail(api.ErrCoreTooMany, "",
			"relay refused: holder machine %s already received %d relayed queries in the last %d s (at most %d per holder); retry within %d s",
			holder, len(ts), window, b.holderQuota, b.retryWithin(now, ts))
	}
	b.byMember[key] = append(b.byMember[key], now)
	b.byHolder[holder] = append(b.byHolder[holder], now)
	return nil
}

type pendingQuery struct {
	ID           string
	Kind         string // progress|artifact
	WorkItem     string
	ArtifactPath string
	Holder       string
	Deadline     time.Time
	Delivered    bool
	Waiter       chan *queryResult

	// resMu/resSent are the first-wins result gate: at most
	// one result is ever handed to Waiter — the expiry notice or the
	// holder's answer, whichever arrives first; the other is dropped.
	resMu   sync.Mutex
	resSent bool
}

// deliver hands res to the waiter at most once and never blocks: the
// gate guarantees at most one send per query, and the select's default
// arm (the waiter already holds a result — first wins) keeps a full
// buffer from blocking the sender, so delivery is safe to call from any
// path, including with q.mu NOT held (the callers collect under the lock,
// unlock, then deliver).
func (p *pendingQuery) deliver(res *queryResult) {
	if p == nil || p.Waiter == nil {
		return
	}
	p.resMu.Lock()
	defer p.resMu.Unlock()
	if p.resSent {
		return
	}
	p.resSent = true
	select {
	case p.Waiter <- res:
	default:
	}
}

type queryResult struct {
	Body *api.QueryAnswerBody
	Err  *api.Error
}

type queryRegistry struct {
	mu    sync.Mutex
	items map[string]*pendingQuery
	wake  chan struct{}
}

func newQueryRegistry() *queryRegistry {
	return &queryRegistry{items: map[string]*pendingQuery{}, wake: make(chan struct{})}
}

// signal wakes EVERY current long-poll waiter (close-and-replace): a
// single 1-slot channel would hand a query/message to one arbitrary
// machine's poll and leave the addressed holder asleep.
func (q *queryRegistry) signal() {
	q.mu.Lock()
	ch := q.wake
	q.wake = make(chan struct{})
	q.mu.Unlock()
	close(ch)
}

// waitCh snapshots the current broadcast channel. Call it BEFORE taking
// items: anything added after the snapshot closes this channel, so no
// wakeup is lost between the check and the wait.
func (q *queryRegistry) waitCh() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.wake
}

func (q *queryRegistry) add(p *pendingQuery) {
	q.mu.Lock()
	q.items[p.ID] = p
	ch := q.wake
	q.wake = make(chan struct{})
	q.mu.Unlock()
	close(ch)
}

func (q *queryRegistry) take(machineID string) []api.MachineQuery {
	now := time.Now()
	var expired []*pendingQuery
	q.mu.Lock()
	out := []api.MachineQuery{}
	for id, p := range q.items {
		if now.After(p.Deadline) {
			delete(q.items, id)
			expired = append(expired, p)
			continue
		}
		if p.Holder != machineID || p.Delivered {
			continue
		}
		p.Delivered = true
		out = append(out, api.MachineQuery{ID: p.ID, Kind: p.Kind, WorkItem: p.WorkItem, ArtifactPath: p.ArtifactPath})
	}
	q.mu.Unlock()
	// Delivery happens only after q.mu is released: the
	// send can never block (deliver), so holding the lock across it was
	// pure deadlock surface.
	for _, p := range expired {
		p.deliver(&queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer within the deadline"}})
	}
	return out
}

func (q *queryRegistry) get(id string) *pendingQuery {
	q.mu.Lock()
	p := q.items[id]
	if p != nil && time.Now().After(p.Deadline) {
		delete(q.items, id)
		q.mu.Unlock()
		p.deliver(&queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer within the deadline"}})
		return nil
	}
	q.mu.Unlock()
	return p
}

func (q *queryRegistry) remove(id string) {
	q.mu.Lock()
	delete(q.items, id)
	q.mu.Unlock()
}

// hMachineInbox is the signed holder long-poll. faces=queries is always on
// for every registered daemon (runners included). faces=messages is served
// to any signed machine that asks: the server answers the operator's unread
// messages. The interactive + fresh-peer + not-superseded idle rule is the
// DAEMON's (`messageFaceWanted`), not a server gate. It returns immediately
// on a new item and otherwise holds up to `wait` (cap 25 s).
func (s *Server) hMachineInbox(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	faces := r.URL.Query().Get("faces")
	if faces == "" {
		faces = "queries"
	}
	wait := inboxLongPoll
	if raw := r.URL.Query().Get("wait"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 && d <= inboxLongPoll {
			wait = d
		}
	}
	wantQueries := strings.Contains(","+faces+",", ",queries,")
	wantMessages := strings.Contains(","+faces+",", ",messages,")
	deadline := time.Now().Add(wait)
	for {
		wake := s.pending.waitCh()
		inbox := api.MachineInbox{Queries: []api.MachineQuery{}, Messages: []api.Message{}}
		if wantQueries {
			inbox.Queries = s.pending.take(m.MachineID)
		}
		if wantMessages {
			msgs, err := s.Admin.PendingDelivery(m.OperatorID, 200)
			if err != nil {
				return err
			}
			if len(msgs) > 0 {
				inbox.Messages = msgs
			}
		}
		if len(inbox.Queries) > 0 || len(inbox.Messages) > 0 || !time.Now().Before(deadline) {
			return reply(w, http.StatusOK, inbox)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return reply(w, http.StatusOK, inbox)
		}
		timer := time.NewTimer(remaining)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return nil
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// hQueryAnswer is the signed POST /api/v1/queries/{qid}/answer: only the
// addressed machine may answer, and an unknown query is CORE-404.
func (s *Server) hQueryAnswer(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	var body api.QueryAnswerBody
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	qid := r.PathValue("qid")
	p := s.pending.get(qid)
	if p == nil {
		return core.Fail(api.ErrCoreNotFound, "", "query %q is unknown or expired", qid)
	}
	if p.Holder != m.MachineID {
		return core.Fail(api.ErrAuthForbidden, "", "query %s is addressed to machine %s", qid, p.Holder)
	}
	s.pending.remove(qid)
	// First-wins delivery: if the expiry notice already
	// reached the waiter, this answer is dropped instead of blocking the
	// holder's HTTP answer forever — the holder still gets its 200.
	if body.Error != nil {
		p.deliver(&queryResult{Err: body.Error})
	} else {
		p.deliver(&queryResult{Body: &body})
	}
	return reply(w, http.StatusOK, map[string]any{"ok": true})
}

// waitQueryResult waits for the holder's answer or the deadline.
func waitQueryResult(p *pendingQuery) (*api.QueryAnswerBody, error) {
	timer := time.NewTimer(progressQueryDeadline)
	defer timer.Stop()
	select {
	case res := <-p.Waiter:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Body, nil
	case <-timer.C:
		return nil, core.Fail(api.ErrCoreUpstream, "", "holder %s did not answer within %s", p.Holder, progressQueryDeadline)
	}
}

// enqueueProgress builds, registers and waits on one pending query.
func (s *Server) enqueueQuery(holder, kind, wi, artifactPath string) (*api.QueryAnswerBody, error) {
	qid, err := core.NewID("q_")
	if err != nil {
		return nil, err
	}
	p := &pendingQuery{
		ID: qid, Kind: kind, WorkItem: wi, ArtifactPath: artifactPath, Holder: holder,
		Deadline: time.Now().Add(progressQueryDeadline),
		Waiter:   make(chan *queryResult, 1),
	}
	s.pending.add(p)
	body, err := waitQueryResult(p)
	s.pending.remove(qid)
	if err != nil {
		return nil, err
	}
	return body, nil
}
