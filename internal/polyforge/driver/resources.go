package driver

// resources.go: the daemon's step-level admission and locks — the one
// primitive every step that needs the machine's heavy-run slots or a repo
// lock goes through (workflow contract v2, orchestration O-5: the daemon
// schedules what a step declares; the step never takes a slot or a lock
// itself). The daemon binds its two faces here: the heavy-run admission
// queue (Config.Admission, daemon/verifyqueue.go) and the forge binding
// with the per-repo merge lock (Config.Forge, daemon/mergelock.go).
//
// acquireResources takes what one dispatch holds in a FIXED order —
// admission first, the merge locks after — and all or nothing: a lock
// that cannot be taken gives the admission back, so two dispatches never
// hold half of each other's needs. The release returns them in the
// reverse order. The admission order (priority, then the time first
// queued, then arrival) is the queue's and is not touched here.
//
// What a step's dispatch holds is its contract-2 `resources` declaration
// (acquireStepResources), held for each dispatch and released when its
// process is gone — at an ask too: such a dispatch keeps no warm window
// (stepDeclaresResources).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// VerifyAdmission is the daemon's heavy-run admission queue: a dispatch
// of a step declaring `admission: heavy` asks it before it runs. Enter answers
// at once; the ticket's Ready closes when the run may start.
type VerifyAdmission interface {
	Enter(req AdmissionRequest) AdmissionTicket
}

// AdmissionRequest is one admission ask.
type AdmissionRequest struct {
	WI   string
	Step string
	// Priority is the work item's priority (its work-item.json value; the
	// first ordering key).
	Priority string
	// Since is when the run first queued (a restart passes the recorded
	// stamp, keeping its place); the second ordering key.
	Since time.Time
}

// AdmissionTicket is one place in the admission queue.
type AdmissionTicket interface {
	// Ready is closed once the run is admitted.
	Ready() <-chan struct{}
	// State answers the 1-based queue position and the gate holding the
	// run (0, "" once admitted).
	State() (position int, reason string)
	// Release gives the admitted slot back, or leaves the queue.
	Release()
}

// Forge is the daemon's binding to the task repos' forge. Production binds
// the daemon's (daemon/mergelock.go); tests bind fakes.
type Forge interface {
	// CommandEnv is the environment git and gh run with: the daemon's
	// own, the machine's gh account.
	CommandEnv() []string
	// LockRepo takes the per-repo merge lock of repo (a repository's
	// identity: its normalized origin URL, mergeLockKey) and answers its
	// release, waiting in arrival order until it is free or ctx ends. One
	// work item's check-then-merge holds it, and a lane's revert step.
	LockRepo(ctx context.Context, repo string) (release func(), err error)
}

// ResourceRequest is what one dispatch of a step holds while it runs.
type ResourceRequest struct {
	// Admission asks the heavy-run queue for a slot (Config.Admission;
	// nil = admitted at once).
	Admission bool
	// MergeKeys are the repositories whose per-repo merge lock the
	// dispatch holds (mergeLockKey each; Config.Forge takes them, in key
	// order, so two holders never wait on each other). An empty list
	// takes none.
	MergeKeys []string
}

// Driver events of the admission.
const (
	EvVerifyQueued   = "verify_queued"
	EvVerifyAdmitted = "verify_admitted"
)

// acquireResources takes the request's resources for one dispatch of
// stepID, in the fixed order admission then merge locks, and answers the
// release of them all (reverse order; idempotent). All or nothing: a merge
// lock that cannot be taken releases the admission already held. An
// ended ctx gives back whatever was taken and answers its error.
func (d *Driver) acquireResources(ctx context.Context, stepID string, req ResourceRequest) (release func(), err error) {
	var held []func()
	releaseAll := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
		held = nil
	}
	if req.Admission {
		r, err := d.awaitAdmission(ctx, stepID)
		if err != nil {
			return nil, err
		}
		held = append(held, r)
	}
	if len(req.MergeKeys) > 0 {
		r, err := d.takeMergeLocks(ctx, req.MergeKeys)
		if err != nil {
			releaseAll()
			return nil, err
		}
		held = append(held, r)
	}
	return releaseAll, nil
}

// awaitAdmission asks the daemon's heavy-run queue for this dispatch and
// waits for the grant. A denial is announced to the supervisor (no stall
// clock while queued); nothing else happens while it waits — no process,
// no budget, no job slot (the daemon's slot count leaves a queued run
// out). The answer is the slot's release.
func (d *Driver) awaitAdmission(ctx context.Context, stepID string) (release func(), err error) {
	if d.cfg.Admission == nil {
		return func() {}, nil
	}
	since := d.now()
	t := d.cfg.Admission.Enter(AdmissionRequest{
		WI: d.wi.ID, Step: stepID, Priority: d.priority(), Since: since,
	})
	select {
	case <-t.Ready():
		return t.Release, nil
	default:
	}
	pos, reason := t.State()
	d.emit(EvVerifyQueued, map[string]any{"step": stepID, "reason": reason, "position": pos, "since": since.UTC().Format(time.RFC3339Nano)})
	d.fact(supervise.StepQueued{Step: stepID, Reason: reason})
	select {
	case <-t.Ready():
		d.emit(EvVerifyAdmitted, map[string]any{"step": stepID, "queued_ms": d.now().Sub(since).Milliseconds()})
		return t.Release, nil
	case <-ctx.Done():
		t.Release()
		return nil, fmt.Errorf("driver: step %s left the verification queue: %w", stepID, ctx.Err())
	}
}

// priority answers the work item's priority (work-item.json; "" =
// normal).
func (d *Driver) priority() string {
	if doc, err := readWorkItemDoc(d.wi.CapsuleDir); err == nil {
		return doc.Priority
	}
	return ""
}

// takeMergeLocks takes the per-repo merge lock of every key, in the
// keys' order (one global order: two holders never wait on each other),
// and answers the release of them all. A key listed twice is taken once.
func (d *Driver) takeMergeLocks(ctx context.Context, keys []string) (func(), error) {
	if d.cfg.Forge == nil {
		return nil, errors.New("this daemon has no forge binding: the per-repo merge lock cannot be taken")
	}
	ordered := make([]string, 0, len(keys))
	for _, k := range keys {
		if !slices.Contains(ordered, k) {
			ordered = append(ordered, k)
		}
	}
	sort.Strings(ordered)
	var held []func()
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
	}
	for _, k := range ordered {
		r, err := d.cfg.Forge.LockRepo(ctx, k)
		if err != nil {
			release()
			return nil, fmt.Errorf("taking the merge lock of %s: %w", k, err)
		}
		held = append(held, r)
	}
	return release, nil
}

// acquireStepResources takes what an agent step declares (`resources`,
// contract 2) for one dispatch: a heavy-run admission slot
// (`admission: heavy`); the per-repo merge locks of the task repos when
// the locks name `merge`. A step declaring nothing holds nothing (a no-op
// release). The daemon's merge-lock keys are the clones' origins
// (mergeLockKey); a clone whose origin cannot be read, or a daemon without
// the forge binding, is an error the caller parks on.
func (d *Driver) acquireStepResources(ctx context.Context, stepID string) (release func(), err error) {
	s := d.workflow.step(stepID)
	if s == nil || s.Resources == nil || (!s.Resources.Heavy() && len(s.Resources.Locks) == 0) {
		return func() {}, nil
	}
	req := ResourceRequest{Admission: s.Resources.Heavy()}
	if s.Resources.Holds(workflow.LockMerge) {
		if d.cfg.Forge == nil {
			return nil, errors.New("the step declares the merge lock and this daemon has no forge binding")
		}
		var dirs []string
		for _, r := range d.claimRepos {
			dirs = append(dirs, d.repoDir(r))
		}
		keys, kerr := mergeLockKeys(ctx, d.cfg.Forge.CommandEnv(), dirs)
		if kerr != nil {
			return nil, fmt.Errorf("the step declares the merge lock: %w", kerr)
		}
		req.MergeKeys = keys
	}
	return d.acquireResources(ctx, stepID, req)
}

// stepDeclaresResources reports whether a dispatch of stepID holds
// declared resources (acquireStepResources takes something): such a
// dispatch keeps no warm window while it awaits the owner (Q9 ③: the
// admission and the locks are released while the owner is asked, and
// taken again before the owner's message is delivered — by the resume
// dispatch, which acquires them like any other).
func (d *Driver) stepDeclaresResources(stepID string) bool {
	s := d.workflow.step(stepID)
	return s != nil && s.Resources != nil && (s.Resources.Heavy() || len(s.Resources.Locks) > 0)
}

// blockOnResources parks the workflow when a step's declared resources
// cannot be taken (an environment park: fix the daemon's bindings or the
// clones, then `pf resume`). stop/err are Run's.
func (d *Driver) blockOnResources(stepID string, cause error) (stop bool, err error) {
	reason := fmt.Sprintf("step %s declares resources the daemon could not take: %v; fix the cause, then resume the work item (`pf resume <wi>`)", stepID, cause)
	if terr := d.transition(TransitionRequest{Kind: TransBlocked, StepID: stepID, Reason: reason, SpawnN: d.currentSpawnN()}); terr != nil {
		return true, fmt.Errorf("driver: resource park write failed: %w", terr)
	}
	d.emit("blocked", map[string]any{"step": stepID, "reason": reason, "report": false})
	return false, nil
}

// originURLTimeout bounds one origin URL read.
const originURLTimeout = time.Minute

// originURL answers the origin URL of the clone at dir: `git remote
// get-url origin` through taskrepo.Command under env (the forge binding's
// command environment, nil = the process's; git resolved on its PATH)
// made a task-repo read by taskrepo.Env, never prompting.
func originURL(ctx context.Context, env []string, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, originURLTimeout)
	defer cancel()
	c := taskrepo.Command(ctx, taskrepo.Env(env), dir, "remote", "get-url", "origin")
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git remote get-url origin: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// mergeLockKey answers the merge-lock identity of the clone at dir: its
// origin URL normalized (originKey), so every clone of one forge
// repository — over https or ssh, with or without `.git` — takes the same
// lock. env is the forge binding's command environment (originURL).
func mergeLockKey(ctx context.Context, env []string, dir string) (string, error) {
	out, err := originURL(ctx, env, dir)
	if err != nil {
		return "", err
	}
	k := originKey(out)
	if k == "" {
		return "", fmt.Errorf("%s has no origin URL", dir)
	}
	return k, nil
}

// originKey normalizes a remote URL to the repository it names: a URL
// (`https://`, `ssh://`, `git://`…) or an scp-like address
// (`[user@]host:path`) to `host/path` — no scheme, user, port, trailing `/`
// or `.git`, lower-cased (forge paths are case-insensitive; two repos
// differing in case only would share a lock, which only serializes them);
// a local path (or `file://` URL) to the path cleaned, without `.git`.
func originKey(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	host, path := "", u
	if scheme, rest, ok := strings.Cut(u, "://"); ok {
		parsed, err := url.Parse(u)
		switch {
		case err == nil && parsed.Host != "":
			host, path = parsed.Hostname(), parsed.Path
		case err == nil:
			path = parsed.Path // file:///path
		case strings.EqualFold(scheme, "file"):
			path = rest
		}
	} else if i := strings.Index(u, ":"); i > 0 && !strings.ContainsRune(u[:i], '/') {
		host, path = u[:i], u[i+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	if host == "" {
		return filepath.Clean(path)
	}
	return strings.ToLower(host + "/" + strings.Trim(path, "/"))
}

// mergeLockKeys answers the merge-lock keys (mergeLockKey) of the clones
// in dirs (the primitive orders them and takes each once).
func mergeLockKeys(ctx context.Context, env []string, dirs []string) ([]string, error) {
	keys := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		k, err := mergeLockKey(ctx, env, dir)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}
