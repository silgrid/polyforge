package daemon

// landing.go: the daemon's landing-lock manager (polyforge#245, 245-lane).
// It owns the daemon's leases of the generic lease base: the lane-declared
// `lease_interval` interval (develop and epic) holds them across steps, and a
// single-dispatch merge lock (micro's merge, a lane's revert step) holds them
// for one dispatch. The plaintext grant tokens live here only — never in
// the capsule, an event, a log or a step process; the private request
// journal (aihubclient.LeaseJournal, 0600) carries the request ids a
// restart needs to release by request id (decided Q6/Q9).
//
// Reachability: daemon code only. A step reaches the manager through
// `pf step lock-check` (the daemon's /step/call), which renews and refuses
// without ever exposing a token. The aihub lease routes are signed machine
// routes; the daemon's client is the one caller.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// leaseJournalFile is the manager's private request journal under the
// daemon's data directory (never the capsule).
const (
	// leaseKind is the generic lease base's landing kind (the server's key
	// derivation normalizes the repository and joins the target branch).
	leaseKind        = "landing"
	leaseJournalFile = "landing-leases.json"
)

// The lease timings of the decided Q1: a 300 s server lease, a 30 s
// renewal and a 5 s poll. They are the server's and the protocol's values,
// not configuration keys.
const (
	leaseTTL        = 300 * time.Second
	leaseRenewEvery = 30 * time.Second
	leasePollEvery  = 5 * time.Second
)

// leaseHub is the client face the manager needs (the production
// *aihubclient.Client; tests bind a fake).
type leaseHub interface {
	AcquireLease(kind string, req api.LeaseAcquire) (*api.LeaseGrant, error)
	RenewLease(kind string, req api.LeaseRenew) (*api.LeaseGrant, error)
	ReleaseLease(kind string, req api.LeaseRelease) (*api.LeaseGrant, error)
	LookupLease(requestID, workItem string) (*api.LeaseGrant, error)
}

// leaseHeld is one acquired lease of one repository.
type leaseHeld struct {
	repo      string
	requestID string
	key       string
	token     string
}

// leaseCycle is one work item's live lease group: all its held
// repositories, the renew loop and the lost-lease state.
type leaseCycle struct {
	mu     sync.Mutex
	wi     string
	cycle  string
	leases map[string]*leaseHeld
	// err is the lost-lease state: once set, no new merge may be checked
	// and the driver releases the whole group (Q7). A late success never
	// clears it.
	err  error
	stop chan struct{}
	once sync.Once
	done chan struct{}
}

// leaseManager is the daemon's lease manager.
type leaseManager struct {
	hub     leaseHub
	journal *aihubclient.LeaseJournal
	now     func() time.Time
	// poll, renewEvery and ttl are the protocol's timings (tests shorten
	// them).
	poll, renewEvery, ttl time.Duration
	// runCtx bounds the renew loops (the daemon's run context; a nil one
	// is replaced by context.Background in Hold).
	runCtx context.Context

	mu        sync.Mutex
	cycles    map[string]*leaseCycle
	recovered bool
}

// newLeaseManagerIf assembles the manager only for the production aihub
// client: a daemon without one (the unit tests' fake hub) binds no manager,
// and the driver runs a declared interval without a lease exactly as a lane
// declaring none does. Production always has the client.
func newLeaseManagerIf(hub leaseHub, journalPath string) *leaseManager {
	if hub == nil {
		return nil
	}
	return newLeaseManager(hub, journalPath)
}

// newLeaseManager assembles the manager over one private journal path.
func newLeaseManager(hub leaseHub, journalPath string) *leaseManager {
	return &leaseManager{
		hub: nil, journal: aihubclient.NewLeaseJournal(journalPath),
		now: time.Now, poll: leasePollEvery, renewEvery: leaseRenewEvery, ttl: leaseTTL,
		cycles: map[string]*leaseCycle{},
	}
}

// Hold acquires (waiting in the server's FIFO queue) the landing lease of
// every repo at the ticket's target branch and answers the group. queued
// is called with the 1-based queue position while it waits. The group is
// held until Release; a renewal failure marks it lost (Err).
func (l *leaseManager) Hold(ctx context.Context, wi, capsuleDir string, repos []string, queued func(position int, since time.Time)) (leaseHold, error) {
	if l == nil || l.hub == nil {
		return nil, fmt.Errorf("this daemon has no aihub client for the landing lock: upgrade or configure the daemon's aihub connection")
	}
	if err := l.recoverOnce(ctx); err != nil {
		return nil, err
	}
	branch, err := targetBranchOf(capsuleDir)
	if err != nil {
		return nil, err
	}
	sorted := leaseRepos(repos)
	c, err := l.cycle(ctx, wi)
	if err != nil {
		return nil, err
	}
	since := l.now()
	for i, repo := range sorted {
		if err := l.acquire(ctx, c, branch, repo, func(pos int) {
			if queued != nil && i == 0 {
				queued(pos, since)
			}
		}); err != nil {
			l.releaseCycle(c)
			return nil, err
		}
	}
	return &leaseHoldGroup{manager: l, cycle: c, repos: sorted}, nil
}

// leaseRepos sorts and dedups the claim repositories one cycle holds.
func leaseRepos(repos []string) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		if r != "" && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

// HoldRepo acquires one repository's landing lease for one dispatch (the
// single-dispatch merge lock of micro's merge and a lane's revert step, Q4):
// same key, same server mechanism, same before-merge check as the lanes'
// interval. The release gives just this repository back.
func (l *leaseManager) HoldRepo(ctx context.Context, wi, capsuleDir, repo string) (func(), error) {
	if l == nil || l.hub == nil {
		return nil, fmt.Errorf("this daemon has no aihub client for the landing lock: upgrade or configure the daemon's aihub connection")
	}
	if err := l.recoverOnce(ctx); err != nil {
		return nil, err
	}
	branch, err := targetBranchOf(capsuleDir)
	if err != nil {
		return nil, err
	}
	c, err := l.cycle(ctx, wi)
	if err != nil {
		return nil, err
	}
	if err := l.acquire(ctx, c, branch, repo, nil); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { l.releaseRepo(c, repo) })
	}, nil
}

// Check renews one held repository's lease and answers nil only when the
// ticket still holds it: the one before-merge check (`pf step lock-check`).
func (l *leaseManager) Check(ctx context.Context, wi, repo string) error {
	if l == nil || l.hub == nil {
		return fmt.Errorf("this daemon has no aihub client for the landing lock")
	}
	l.mu.Lock()
	c := l.cycles[wi]
	l.mu.Unlock()
	if c == nil {
		return fmt.Errorf("the landing lock of %s is not held by this daemon: no merge may run", wi)
	}
	if err := c.lost(); err != nil {
		return err
	}
	if findLeaseRepo(c, repo) == nil {
		return fmt.Errorf("the landing lock of %s is not held for repository %q: no merge may run", wi, repo)
	}
	return l.renewCycle(ctx, c)
}

// Release gives the whole group back (the interval's exit, or a release
// before an ask), idempotent.
func (l *leaseManager) Release(wi string) {
	l.mu.Lock()
	c := l.cycles[wi]
	l.mu.Unlock()
	if c != nil {
		l.releaseCycle(c)
	}
}

// cycle answers the work item's live cycle, creating it when absent.
func (l *leaseManager) cycle(ctx context.Context, wi string) (*leaseCycle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.cycles[wi]; c != nil {
		if err := c.lost(); err != nil {
			// a lost cycle is released by its holder; never revived
			return nil, err
		}
		return c, nil
	}
	if l.runCtx != nil {
		ctx = l.runCtx
	}
	c := &leaseCycle{wi: wi, cycle: leaseCycleID(), leases: map[string]*leaseHeld{},
		stop: make(chan struct{}), done: make(chan struct{})}
	l.cycles[wi] = c
	go l.renewLoop(c)
	return c, nil
}

// acquire takes one repository's lease, journaling its request id before
// the first call (Q9: a crash after the server granted but before the
// answer loses the token, so the id is the only way back) and polling the
// queue until the server grants it.
func (l *leaseManager) acquire(ctx context.Context, c *leaseCycle, branch, repo string, queued func(int)) error {
	rid := leaseRequestID()
	if err := l.journal.Add(aihubclient.LeaseJournalEntry{
		RequestID: rid, Kind: leaseKind, WorkItem: c.wi, Cycle: c.cycle,
	}); err != nil {
		return fmt.Errorf("the landing lock of %s could not be journaled (no lease is requested): %w", c.wi, err)
	}
	for {
		g, err := l.hub.AcquireLease(leaseKind, api.LeaseAcquire{
			Repo: repo, Branch: branch, RequestID: rid, WorkItem: c.wi,
		})
		if err != nil {
			_ = l.withdraw(rid, c.wi, "")
			return fmt.Errorf("the landing lock of %s/%s: %w", repo, branch, err)
		}
		switch g.Status {
		case coreStatusGranted:
			h := &leaseHeld{repo: repo, requestID: rid, key: g.Key, token: g.Token}
			c.muSet(h)
			return nil
		case coreStatusHeld:
			// The response to the granting poll was lost: the token is
			// unrecoverable. Withdraw this request and fail the cycle; a
			// new request re-queues.
			_ = l.withdraw(rid, c.wi, g.Key)
			return fmt.Errorf("the landing lock of %s/%s was granted without its token (a lost answer): the cycle is void and re-queues", repo, branch)
		case coreStatusQueued:
			if queued != nil {
				queued(g.Position)
			}
			select {
			case <-ctx.Done():
				_ = l.withdraw(rid, c.wi, g.Key)
				return ctx.Err()
			case <-time.After(l.poll):
			}
		default:
			_ = l.withdraw(rid, c.wi, g.Key)
			return fmt.Errorf("the landing lock of %s/%s answered %q: no lease is held", repo, branch, g.Status)
		}
	}
}

// The wire statuses (the shared api.LeaseGrant vocabulary).
const (
	coreStatusGranted = "granted"
	coreStatusHeld    = "held"
	coreStatusQueued  = "queued"
)

// withdraw releases a queued request or a lease by request id (no token).
func (l *leaseManager) withdraw(rid, wi, key string) error {
	_, err := l.hub.ReleaseLease(leaseKind, api.LeaseRelease{Key: key, RequestID: rid, WorkItem: wi})
	_ = l.journal.Remove(rid)
	return err
}

// renewLoop renews the cycle's leases every renewEvery. The first failure
// voids the whole cycle (Q7): the error stays, the loop stops, and the
// holder releases and re-queues.
func (l *leaseManager) renewLoop(c *leaseCycle) {
	defer close(c.done)
	t := time.NewTicker(l.renewEvery)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			if err := l.renewCycle(context.Background(), c); err != nil {
				c.setErr(err)
				return
			}
		}
	}
}

// renewCycle renews every held lease of the cycle; one failure voids the
// whole group.
func (l *leaseManager) renewCycle(ctx context.Context, c *leaseCycle) error {
	if err := c.lost(); err != nil {
		return err
	}
	c.mu.Lock()
	leases := make([]*leaseHeld, 0, len(c.leases))
	for _, h := range c.leases {
		leases = append(leases, h)
	}
	c.mu.Unlock()
	for _, h := range leases {
		if _, err := l.hub.RenewLease(leaseKind, api.LeaseRenew{
			Key: h.key, RequestID: h.requestID, WorkItem: c.wi, Token: h.token,
		}); err != nil {
			err = fmt.Errorf("the landing lock of %s was lost (renewing %s): %w", c.wi, h.repo, err)
			c.setErr(err)
			return err
		}
	}
	return nil
}

// releaseCycle stops the renew loop and gives every held lease back, by
// token, falling back to the request id when the token is refused. The
// journal drops each request id only once the server answered.
func (l *leaseManager) releaseCycle(c *leaseCycle) {
	l.mu.Lock()
	if l.cycles[c.wi] == c {
		delete(l.cycles, c.wi)
	}
	l.mu.Unlock()
	c.once.Do(func() {
		close(c.stop)
		<-c.done
	})
	c.mu.Lock()
	leases := make([]*leaseHeld, 0, len(c.leases))
	for _, h := range c.leases {
		leases = append(leases, h)
	}
	c.leases = map[string]*leaseHeld{}
	c.mu.Unlock()
	for _, h := range leases {
		l.releaseHeld(c.wi, h)
	}
}

// releaseRepo gives one repository of the cycle back, dropping the cycle
// once it holds nothing.
func (l *leaseManager) releaseRepo(c *leaseCycle, repo string) {
	h := c.muTake(repo)
	if h != nil {
		l.releaseHeld(c.wi, h)
	}
	c.mu.Lock()
	empty := len(c.leases) == 0
	c.mu.Unlock()
	if empty {
		l.mu.Lock()
		if l.cycles[c.wi] == c {
			delete(l.cycles, c.wi)
		}
		l.mu.Unlock()
		c.once.Do(func() {
			close(c.stop)
			<-c.done
		})
	}
}

// releaseHeld releases one held lease: with its token; when that is
// refused (the lease already expired) the request id alone settles the
// journal, and an error keeps the journal for the next recovery.
func (l *leaseManager) releaseHeld(wi string, h *leaseHeld) {
	_, err := l.hub.ReleaseLease(leaseKind, api.LeaseRelease{
		Key: h.key, RequestID: h.requestID, WorkItem: wi, Token: h.token,
	})
	if err != nil {
		if _, rerr := l.hub.ReleaseLease(leaseKind, api.LeaseRelease{
			Key: h.key, RequestID: h.requestID, WorkItem: wi,
		}); rerr != nil {
			return // keep the journal entry: the restart's recovery tries again
		}
	}
	_ = l.journal.Remove(h.requestID)
}

// recoverOnce releases the grants a previous daemon life left in the
// journal before any new request queues (Q9): lookup answers the key (and
// proves the grant exists), then the empty-token release frees it. A
// lookup that answers "no such request" means the server already expired
// it, so the journal entry goes; a transport error keeps the journal and
// blocks new cycles (never a token-less machine-wide delete, never a guess
// from the local clock).
func (l *leaseManager) recoverOnce(ctx context.Context) error {
	l.mu.Lock()
	if l.recovered {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	entries, err := l.journal.Entries()
	if err != nil {
		return fmt.Errorf("the landing-lock journal could not be read, so no new lease is requested: %w", err)
	}
	if len(entries) > 0 && l.hub == nil {
		return fmt.Errorf("the landing-lock journal carries %d request(s) and this daemon has no aihub client to release them", len(entries))
	}
	for _, e := range entries {
		g, err := l.hub.LookupLease(e.RequestID, e.WorkItem)
		if err != nil {
			if isLeaseGone(err) {
				_ = l.journal.Remove(e.RequestID)
				continue
			}
			return fmt.Errorf("the landing-lock recovery of request %s (work item %s) could not be confirmed: %w; the daemon holds no new lease until it is", e.RequestID, e.WorkItem, err)
		}
		if _, err := l.hub.ReleaseLease(leaseKind, api.LeaseRelease{
			Key: g.Key, RequestID: e.RequestID, WorkItem: e.WorkItem,
		}); err != nil {
			return fmt.Errorf("the landing-lock recovery could not release request %s (work item %s): %w", e.RequestID, e.WorkItem, err)
		}
		if err := l.journal.Remove(e.RequestID); err != nil {
			return err
		}
	}
	l.mu.Lock()
	l.recovered = true
	l.mu.Unlock()
	return nil
}

// isLeaseGone reports the server's "this request holds nothing" answer.
func isLeaseGone(err error) bool {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code == "LEASE_NOT_QUEUED" || ae.Code == "CORE-404" || ae.Code == "LEASE_LOST"
	}
	return false
}

// landingBranch answers the ticket's landing target branch: its fixed
// target (runtime-state/target-branch.json), else the real default branch
// of its first task repo (origin/HEAD). A ticket without either cannot be
// leased (Q2: never guess a branch and take another lock).
func targetBranchOf(capsuleDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, "runtime-state", "target-branch.json"))
	if err == nil {
		var doc struct {
			Target string `json:"target"`
		}
		if json.Unmarshal(raw, &doc) == nil && strings.TrimSpace(doc.Target) != "" {
			return strings.TrimSpace(doc.Target), nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("the ticket's target branch could not be read: %w", err)
	}
	var capDoc capsule.Identity
	if raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON)); err == nil {
		if json.Unmarshal(raw, &capDoc) == nil {
			names := make([]string, 0, len(capDoc.Repos))
			for name := range capDoc.Repos {
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) > 0 {
				dir := filepath.Join(capsuleDir, "repos", names[0])
				if b, err := defaultBranch(dir); err == nil && b != "" {
					return b, nil
				}
			}
		}
	}
	return "", fmt.Errorf("the ticket has no fixed target branch and no task repo whose default branch answers: the landing lock is not taken (Q2)")
}

// defaultBranch answers the clone's recorded default branch
// (refs/remotes/origin/HEAD, what `git clone` writes), "origin/" dropped.
func defaultBranch(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := taskrepo.Command(ctx, nil, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git symbolic-ref --short refs/remotes/origin/HEAD: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	b := strings.TrimSpace(out.String())
	b = strings.TrimPrefix(b, "origin/")
	if b == "" {
		return "", fmt.Errorf("%s records no default branch", dir)
	}
	return b, nil
}

// leaseHold is the group a lane's interval holds (the driver's face).
type leaseHold = driver.IntervalLease

type leaseHoldGroup struct {
	manager *leaseManager
	cycle   *leaseCycle
	repos   []string
}

func (g *leaseHoldGroup) Err() error { return g.cycle.lost() }

func (g *leaseHoldGroup) Check(ctx context.Context) error {
	return g.manager.renewCycle(ctx, g.cycle)
}

func (g *leaseHoldGroup) Release() { g.manager.releaseCycle(g.cycle) }

// leaseCycleID and leaseRequestID are non-secret identifiers (the
// events may carry them; no token rides them).
func leaseCycleID() string   { return "lc_" + randomHex(8) }
func leaseRequestID() string { return "lr_" + randomHex(16) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// The small helpers that keep the cycle's own state under its own lock.
func (c *leaseCycle) lost() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *leaseCycle) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

func (c *leaseCycle) muSet(h *leaseHeld) {
	c.mu.Lock()
	c.leases[h.repo] = h
	c.mu.Unlock()
}

func (c *leaseCycle) muTake(repo string) *leaseHeld {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := c.leases[repo]
	delete(c.leases, repo)
	return h
}

func findLeaseRepo(c *leaseCycle, repo string) *leaseHeld {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.leases[repo]
}
