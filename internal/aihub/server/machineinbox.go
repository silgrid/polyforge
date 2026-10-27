package server

// machineinbox.go: the in-memory relay registry and the holder inbox
// long-poll (workbench#11). Progress/artifact queries are NEVER stored:
// a pending entry exists only between enqueue and answer/deadline.

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

const (
	progressQueryDeadline = 5 * time.Second
	inboxLongPoll         = 25 * time.Second
)

type pendingQuery struct {
	ID           string
	Kind         string // progress|artifact
	WorkItem     string
	ArtifactPath string
	Holder       string
	Deadline     time.Time
	Delivered    bool
	Waiter       chan *queryResult
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
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []api.MachineQuery{}
	for id, p := range q.items {
		if now.After(p.Deadline) {
			delete(q.items, id)
			if p.Waiter != nil {
				p.Waiter <- &queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer within the deadline"}}
			}
			continue
		}
		if p.Holder != machineID || p.Delivered {
			continue
		}
		p.Delivered = true
		out = append(out, api.MachineQuery{ID: p.ID, Kind: p.Kind, WorkItem: p.WorkItem, ArtifactPath: p.ArtifactPath})
	}
	return out
}

func (q *queryRegistry) get(id string) *pendingQuery {
	q.mu.Lock()
	defer q.mu.Unlock()
	p := q.items[id]
	if p != nil && time.Now().After(p.Deadline) {
		delete(q.items, id)
		if p.Waiter != nil {
			p.Waiter <- &queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer within the deadline"}}
		}
		return nil
	}
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
	if p.Waiter != nil {
		if body.Error != nil {
			p.Waiter <- &queryResult{Err: body.Error}
		} else {
			p.Waiter <- &queryResult{Body: &body}
		}
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
