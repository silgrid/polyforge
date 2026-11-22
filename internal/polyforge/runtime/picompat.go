package runtime

// picompat.go: the pi sub-agent compatibility gate (polyforge#227). The
// daemon-owned worker may run only when the switch is on AND the installed
// pi can actually load the daemon-owned extensions and register the
// subagent tool (owner rulings 2026-11-22 Q1/Q2/Q5, D3/D4/D5).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// piCapabilityProbeFn is the capability call the checker makes; a test
// replaces it to count probes without launching a real pi.
var piCapabilityProbeFn = piCapabilityProbe

// resetPiCompatCache drops every cached compatibility verdict (tests only).
func resetPiCompatCache() { piCompatVerdicts.reset() }

// piEmbeddedResourceDigest answers the digest of the built-in resource tree
// (the embed in production), the cache key's resource dimension: every file
// under its relative path, sorted, each folded in with its length and its
// exact bytes. The tree itself is the authority — a hand-written file list
// would silently stop covering a new or renamed resource.
func piEmbeddedResourceDigest() (string, error) {
	var files []string
	if err := fs.WalkDir(piCompatResourceFS, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !de.IsDir() {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	digest := sha256.New()
	for _, p := range files {
		raw, err := fs.ReadFile(piCompatResourceFS, p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(digest, "%s\x00%d\x00", p, len(raw))
		digest.Write(raw)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// piTargetIdentity pins WHICH executable was judged: the resolved path and
// the entry file's content fingerprint, so a same-path replacement is a
// different target and a symlink retarget invalidates the key (Q4 A).
func piTargetIdentity(bin string) (target, fingerprint string, err error) {
	found, err := exec.LookPath(bin)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return abs, hex.EncodeToString(sum[:]), nil
}

// piCompatFlight is one in-flight (then finished) capability call shared by
// every concurrent same-key caller.
type piCompatFlight struct {
	done chan struct{}
	err  error
}

// piCompatVerdicts is the process-level SUCCESS cache: a failed flight is
// deleted, so the next caller probes again (D4). It grows with distinct
// targets/versions/digests; no TTL or capacity was asked for.
type piCompatVerdictsCache struct {
	mu      sync.Mutex
	flights map[string]*piCompatFlight
}

var piCompatVerdicts = &piCompatVerdictsCache{flights: map[string]*piCompatFlight{}}

func (c *piCompatVerdictsCache) reset() {
	c.mu.Lock()
	c.flights = map[string]*piCompatFlight{}
	c.mu.Unlock()
}

// verdict runs the capability call once per key. A same-key caller joins the
// flight and waits for its answer; its own context cancellation only ends
// its wait (the shared flight stays bounded by the check's own 9s deadline,
// so a departed waiter leaves no unbounded work behind).
func (c *piCompatVerdictsCache) verdict(ctx context.Context, key string, probe func(context.Context) error) error {
	deadline, hasDeadline := ctx.Deadline()
	c.mu.Lock()
	if flight, ok := c.flights[key]; ok {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return flight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var probeCtx context.Context
	var cancel context.CancelFunc
	if hasDeadline {
		probeCtx, cancel = context.WithDeadline(context.Background(), deadline)
	} else {
		probeCtx, cancel = context.WithCancel(context.Background())
	}
	flight := &piCompatFlight{done: make(chan struct{})}
	c.flights[key] = flight
	c.mu.Unlock()

	err := probe(probeCtx)
	cancel()
	c.mu.Lock()
	flight.err = err
	if err != nil {
		delete(c.flights, key)
	}
	c.mu.Unlock()
	close(flight.done)
	return err
}

// piCompatResourceFS is the authority for the built-in resources the
// compatibility probe loads and the resource digest covers: production is
// the compiled-in embed; a test substitutes an in-memory twin to inject a
// loading defect without touching a production extension.
var piCompatResourceFS fs.FS = stepagentassets.FS

// PiDispatchReason is the stable class of one refused pi dispatch.
type PiDispatchReason string

const (
	PiReasonVersion          PiDispatchReason = "pi_version"
	PiReasonFlag             PiDispatchReason = "pi_flag"
	PiReasonAPI              PiDispatchReason = "pi_api"
	PiReasonToolRegistration PiDispatchReason = "pi_tool_registration"
	PiReasonTimeout          PiDispatchReason = "pi_timeout"
	PiReasonResources        PiDispatchReason = "pi_resources"
	PiReasonInvalidParam     PiDispatchReason = "pi_subagents_invalid"
)

// PiDispatchError is one refused pi dispatch: a stable reason, a fixed
// human-readable detail and the bounded facts (target, version, stage) that
// located it. It never carries a provider response payload or a credential.
type PiDispatchError struct {
	Kind    PiDispatchReason
	Detail  string
	Target  string
	Version string
	Stage   string
	Err     error
}

func (e *PiDispatchError) Reason() string { return string(e.Kind) }

func (e *PiDispatchError) Unwrap() error { return e.Err }

func (e *PiDispatchError) Error() string {
	text := fmt.Sprintf("pi compatibility rejected [%s]: %s", e.Kind, e.Detail)
	var where []string
	if e.Target != "" {
		where = append(where, "target="+e.Target)
	}
	if e.Version != "" {
		where = append(where, "version="+e.Version)
	}
	if e.Stage != "" {
		where = append(where, "stage="+e.Stage)
	}
	if len(where) > 0 {
		text += " (" + strings.Join(where, ", ") + ")"
	}
	return text
}

// piRequiredFlags is the flag set the daemon-owned step process needs
// (polyforge#193/P6). --no-approve joined it when the dedicated step agent
// directory replaced the operator's approvals path.
// The compatibility gate's one budget (owner rulings 2026-11-22 Q5/D5): the
// whole check — target/version discovery, the flag check, the capability
// call, and the process-group recovery — shares ten seconds. Capability work
// stops at nine; the last second bounds TERM, KILL and the waits.
const (
	piCompatTotalBudget      = 10 * time.Second
	piCompatCapabilityBudget = 9 * time.Second
)

var piRequiredFlags = []string{
	"--no-skills",
	"--no-context-files",
	"--no-prompt-templates",
	"--no-extensions",
	"--no-approve",
}

// piVersion answers the installed target's own version report: one bounded
// short command, the first non-empty line. An empty or failing report is a
// version-class refusal (no numeric comparison: D5 sets no version floor).
func piVersion(ctx context.Context, bin string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "--version")
	// a descendant holding the pipe must not outlive the deadline
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	version := firstNonEmptyLine(string(out))
	if version == "" {
		return "", errors.New("the version command printed no version")
	}
	return version, nil
}

// piFlagsCheck refuses a target whose --help does not advertise every
// required flag.
func piFlagsCheck(ctx context.Context, bin, version string) error {
	cmd := exec.CommandContext(ctx, bin, "--help")
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return piFlagRefusal(bin, version, "--help", err)
	}
	text := string(out)
	for _, flag := range piRequiredFlags {
		if !strings.Contains(text, flag) {
			return piFlagRefusal(bin, version, flag, nil)
		}
	}
	return nil
}

func piFlagRefusal(bin, version, flag string, err error) *PiDispatchError {
	return &PiDispatchError{
		Kind:    PiReasonFlag,
		Detail:  "required flag is unavailable: " + flag,
		Target:  bin,
		Version: version,
		Stage:   "flag",
		Err:     err,
	}
}

func piVersionRefusal(bin string, err error) *PiDispatchError {
	return &PiDispatchError{
		Kind:   PiReasonVersion,
		Detail: "cannot determine executable version",
		Target: bin,
		Stage:  "version",
		Err:    err,
	}
}

func piTimeoutRefusal(bin, version, stage string) *PiDispatchError {
	return &PiDispatchError{
		Kind:    PiReasonTimeout,
		Detail:  "compatibility probe exceeded 10s total budget",
		Target:  bin,
		Version: version,
		Stage:   stage,
	}
}

// validatePiCompatibility answers one compatibility verdict: the target's
// version, its required flags, and (from the capability probe on) the real
// load-and-call proof. Bounded by the caller's context.
func validatePiCompatibility(ctx context.Context, bin string) error {
	start := time.Now()
	workCtx, cancel := context.WithDeadline(ctx, start.Add(piCompatCapabilityBudget))
	defer cancel()
	target, fingerprint, err := piTargetIdentity(bin)
	if err != nil {
		if errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			return piTimeoutRefusal(bin, "", "target")
		}
		return piVersionRefusal(bin, err)
	}
	version, err := piVersion(workCtx, bin)
	if err != nil {
		if errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			return piTimeoutRefusal(target, "", "version")
		}
		return piVersionRefusal(target, err)
	}
	digest, err := piEmbeddedResourceDigest()
	if err != nil {
		return &PiDispatchError{Kind: PiReasonResources, Detail: "embedded resource validation failed: the built-in resource tree is unreadable", Target: target, Version: version, Stage: "resources", Err: err}
	}
	key := strings.Join([]string{target, fingerprint, version, digest}, "|")
	err = piCompatVerdicts.verdict(workCtx, key, func(probeCtx context.Context) error {
		if err := piFlagsCheck(probeCtx, target, version); err != nil {
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				return piTimeoutRefusal(target, version, "flag")
			}
			return err
		}
		return piCapabilityProbeFn(probeCtx, target, version)
	})
	// a success observed past the deadline is not a pass: the process
	// group's recovery still has to fit the one budget.
	if err == nil && (workCtx.Err() != nil || time.Since(start) > piCompatTotalBudget) {
		return piTimeoutRefusal(target, version, "capability")
	}
	return err
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
