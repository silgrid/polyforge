// spawn.go: the WI-agent spawner for the pi harness. Ports the v0
// supervisor's LIVE-TESTED RPC protocol layer: stdin carries one JSON
// command object per line ({"id":..,"type":"prompt","message":..}), stdout
// is a record stream whose response records correlate command ids.
// Isolation, env sanitization and the spawn journal land here.
package runtime

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
)

// piIsolationFlags is the WI-agent birth isolation profile: the named flag
// set that keeps a spawned agent from inheriting the OPERATOR's global
// harness instruction surfaces (global skills, context files and prompt
// templates). The agent's only instruction of record is the initial prompt
// injected at derivation; a capsule's materialized step-agent definitions
// are a different resource family and stay reachable.
//
// --no-extensions is DELIBERATELY ABSENT and must stay absent: the
// subagent tool is not a pi builtin but an EXTENSION — on the operator's
// machine the one at <operator agent dir>/extensions/subagent/ — and
// --no-extensions would drop it, and with it the whole step-agent
// dispatch path.
//
// The residual exposure this profile could never close — extension-
// registered HARNESS BACKGROUND TASKS (the operator's user-level
// packages, e.g. npm:pi-background-tasks, declared in the agent
// directory's settings.json) — is closed MECHANICALLY instead
// (addendum 2, owner ruling A): the step process runs with
// PI_CODING_AGENT_DIR pointing at the dedicated daemon-owned step
// agent directory (runtime/stepagentdir.go), a minimal snapshot —
// auth.json, models.json, agents/ and exactly ONE extension,
// extensions/subagent/ (pi auto-loads global extensions from
// <agent dir>/extensions/, which is how the subagent tool reaches the
// step process) — and never settings.json, so no package loads. The
// allowlisted members are refreshed before every spawn; a global
// reinstall lands in the operator's directory and never reaches the
// step process. The isolation is the harness adapter layer's guarantee
// (see docs/design/polyforge-vnext/v2-converge.md §2.7), not an
// instruction.
var piIsolationFlags = []string{
	"--no-skills",
	"--no-context-files",
	"--no-prompt-templates",
}

// piCommand is one stdin command of the real pi RPC protocol. Every
// command MAY carry a string id; the matching response record repeats it.
type piCommand struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"` // "prompt"
	Message string `json:"message"`
}

// stdout record probe shape: response records repeat our command ids.
type stdoutRecord struct {
	Type    string  `json:"type"`
	Command string  `json:"command"` // "prompt" on response records
	ID      *string `json:"id"`
	Success *bool   `json:"success"`
	Error   string  `json:"error"`
}

// ---------------------------------------------------------------------------
// Env sanitize point: the ONE place every spawned agent's environment is
// built. AIHub credentials must never reach a WI agent's env/cwd/argv, so
// the inherited environ is filtered (blacklist, not an invented allowlist —
// the harness needs PATH/HOME and its own config vars): every POLYFORGE_*
// and PF_* variable is stripped, and belt-and-braces, any variable whose
// VALUE equals a configured secret is stripped too, so even a RENAMED
// variable cannot smuggle it.

type envSanitizer struct {
	secrets []string // configured secret values (api key, push credential)
}

func newEnvSanitizer(secrets ...string) *envSanitizer {
	var kept []string
	for _, s := range secrets {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return &envSanitizer{secrets: kept}
}

func (e *envSanitizer) childEnv(additions ...string) []string {
	var secrets []string
	if e != nil {
		secrets = e.secrets
	}
	out := make([]string, 0, len(os.Environ())+len(additions))
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "POLYFORGE_") || strings.HasPrefix(name, "PF_") {
			continue
		}
		leak := false
		for _, s := range secrets {
			if subtle.ConstantTimeCompare([]byte(val), []byte(s)) == 1 {
				leak = true
				break
			}
		}
		if !leak {
			out = append(out, kv)
		}
	}
	return append(out, additions...)
}

// ---------------------------------------------------------------------------
// SpawnParams / AgentHandle

// piSpawner is the production spawner for the pi harness. It holds the
// config BY POINTER (see DefaultSpawner): fields are read at spawn time
// from the shared config, never from a copy.
type piSpawner struct {
	cfg *Config
}

func newPiSpawner(cfg *Config) *piSpawner {
	return &piSpawner{cfg: cfg}
}

// recordTaps wires the dispatch's OnRecord callback into the process's
// stdout tap chain (both harnesses): the driver's harness translator hangs
// on it, so every raw stdout record the spawner reads also reaches the
// supervisor's per-dispatch state.
func recordTaps(p SpawnParams) []func(string) {
	if p.OnRecord == nil {
		return nil
	}
	return []func(string){p.OnRecord}
}

func (s *piSpawner) bin() string {
	if s.cfg.PiBin != "" {
		return s.cfg.PiBin
	}
	return "pi"
}

// buildCmd constructs (without starting) the argv/env/cwd of one
// derivation. Exported shape for the isolation regression: argv carries
// the transport first, then the isolation flags, then the resume session,
// then — when the bundle pinned a candidate chain — the model pin.
//
// WHY ARGV, NOT CAPSULE-LOCAL PROJECT SETTINGS (measured, F4/P6): the
// harness's project-level settings file (<CapsuleDir>/.pi/settings.json)
// is NOT disabled by --no-context-files (that flag only stops the
// agent-instruction context files' discovery) — it is gated by PROJECT
// TRUST instead. In rpc
// mode the harness has no UI, and with the default trust policy ("ask"),
// no --approve override and no saved decision, protected project settings
// are SKIPPED ENTIRELY (verified live: a bogus defaultModel in the
// capsule's .pi/settings.json was ignored; harness source: the trust
// resolver answers false when it cannot prompt). Even when trusted and
// loaded, a settings defaultModel that fails to resolve SILENTLY falls
// back to automatic selection, while argv --model fails LOUDLY — and a
// pinned bundle candidate that cannot resolve is exactly the visible
// infrastructure failure the recovery engine should see. So the pin
// rides argv.
//
// The operator's agent-directory settings do not reach the step process
// at all (addendum 2): the dedicated step agent directory
// (PI_CODING_AGENT_DIR, refreshed before every spawn) holds only the
// minimal set — credentials, model configuration, subagent definitions,
// the subagent extension — and NO settings.json, so no operator
// preference (a defaultModel override, a package declaration) applies.
// The argv pin stays for the loud-failure property above.
func (s *piSpawner) buildCmd(p SpawnParams) *exec.Cmd {
	args := []string{"--mode", "rpc"}
	args = append(args, piIsolationFlags...)
	if p.Resume {
		args = append(args, "--session", p.TranscriptPath)
	}
	if p.Model != "" {
		// the candidate ids are provider-qualified "<provider>/<model>",
		// exactly the form the harness's --model pattern accepts
		args = append(args, "--model", p.Model)
	}
	// polyforge#176: the candidate's effort rides --thinking (never
	// the --model "<id>:<level>" shorthand — --model stays the id)
	args = append(args, harness.Candidate{Harness: harness.Pi, Effort: p.Effort}.EffortArgs()...)
	cmd := exec.Command(s.bin(), args...)
	cmd.Dir = p.CapsuleDir // cwd = capsule root
	additions := []string{
		"PF_WI_ID=" + p.WorkItemID, // layer-4 cleanup marker (deliberate addition)
		// second marker half: the sweep needs BOTH, so a daemon never
		// reaps another daemon's step process for the same WI id.
		"PF_DAEMON_ID=" + s.cfg.MachineID,
		// the daemon's tool rule, so verify-run can bound its whole run
		// without a pi-specific shell setting
		toolWindowEnv(s.cfg.ToolWindow),
	}
	if s.cfg.DaemonSocket != "" {
		additions = append(additions, "PF_DAEMON_SOCKET="+s.cfg.DaemonSocket)
	}
	// addendum 2 (owner ruling A): the step process's harness identity is
	// the DEDICATED agent directory — not the operator's — plus its own
	// session store — the capsule's own directory in it (piSessionDir,
	// the same dir the live-transcript locator scans; polyforge#211).
	// Only a real runtime (DataDir set) redirects: a bare Config (unit
	// tests of the argv shape) has no data dir to own.
	if s.cfg.DataDir != "" {
		additions = append(additions,
			"PI_CODING_AGENT_DIR="+s.cfg.StepAgentDir(),
			"PI_CODING_AGENT_SESSION_DIR="+piSessionDir(s.cfg.StepAgentSessionsDir(), p.CapsuleDir),
		)
	}
	cmd.Env = newEnvSanitizer(s.cfg.EnvSecrets...).childEnv(additions...)
	return cmd
}

// Spawn derives one persistent agent process: process group (the agent is
// its own group leader so the cleanup layers can collect step agents and
// command children), the dedicated step agent directory's allowlisted
// members refreshed (never pruned — see stepagentdir.go) BEFORE
// the process is born (a torn credential copy is a spawn-refusing error,
// never a silently degraded step agent), identity journal BEFORE the
// spawn returns, then the initial prompt as the session's first command.
func (s *piSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	if p.InitialPrompt == "" {
		return nil, fmt.Errorf("runtime: spawn without an initial prompt")
	}
	if s.cfg.DataDir != "" {
		if err := s.cfg.refreshStepAgentDir(); err != nil {
			return nil, err
		}
	}
	proc, _, err := startHarness(s.buildCmd(p), p, true)
	if err != nil {
		return nil, err
	}
	if err := proc.writeCommand(piCommand{ID: "pf-init", Type: "prompt", Message: p.InitialPrompt}); err != nil {
		return nil, injectionFailed(proc, err)
	}
	return proc, nil
}

// startHarness starts a built step command — either harness — as its own
// process-group leader (so the cleanup layers can collect step agents and
// command children) with its own stdout/stderr pipes, wraps it in a
// supervised proc and writes the identity journal before returning.
// keepStdin hands the stdin pipe to the proc (pi's RPC command channel);
// otherwise the caller owns the returned pipe (claude writes the prompt
// once and closes it).
func startHarness(cmd *exec.Cmd, p SpawnParams, keepStdin bool) (*proc, io.WriteCloser, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	// Own pipes (not cmd.StdoutPipe): cmd.Wait would close the read end
	// the instant the process exits and could drop the tail of the
	// stream — exactly the records (the error turn's agent_end) the exit
	// classification needs. With plain *os.File sinks Wait copies
	// nothing, and the reader sees EOF once every writer is gone.
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, nil, err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return nil, nil, fmt.Errorf("runtime: harness start failed: %w", err)
	}
	afterHarnessStartFn()
	outW.Close()
	errW.Close()
	var procStdin io.WriteCloser
	if keepStdin {
		procStdin = stdin
	}
	proc := newProcOpts(cmd, procStdin, outR, procOpts{
		taps:   recordTaps(p),
		stderr: errR,
		onExit: p.OnExit,
	})
	// Identity journal before Spawn returns: a crash between derivation and
	// the runtime's persisted record must never leave an undiscoverable
	// orphan. Its failure tears the fresh process down.
	if p.Journal != nil {
		if jerr := p.Journal(cmd.Process.Pid, proc.startedAt); jerr != nil {
			if !keepStdin {
				stdin.Close()
			}
			_ = proc.Terminate(5 * time.Second)
			return nil, nil, fmt.Errorf("runtime: spawn journal failed (process torn down): %w", jerr)
		}
	}
	return proc, stdin, nil
}

// afterHarnessStartFn runs right after a harness process is born, before
// its identity is stamped: a seam so the stamp-delay test can stall the
// Go side there (a loaded host, a GC pause); production never changes it.
var afterHarnessStartFn = func() {}

// injectionFailed tears down a harness whose initial prompt could not be
// delivered and returns the spawn error. A harness that died at startup
// (an unresolvable pinned model, a bad credential) never read the prompt:
// its stderr is the only explanation, and the driver classifies the
// failure from it (a model-class text advances the candidate chain, F4).
func injectionFailed(proc *proc, err error) error {
	_ = proc.Terminate(5 * time.Second)
	select {
	case <-proc.exited:
	case <-time.After(2 * time.Second):
	}
	if detail := strings.TrimSpace(proc.tail.String()); detail != "" {
		return fmt.Errorf("runtime: initial prompt injection failed (process torn down): %w; harness stderr: %s", err, detail)
	}
	return fmt.Errorf("runtime: initial prompt injection failed (process torn down): %w", err)
}

// ---------------------------------------------------------------------------
// Proc: the supervised handle.

const (
	maxRecordLineBytes = 4 << 20
)

var errRecordTooLong = fmt.Errorf("runtime: stdout line exceeded the bounded reader limit")

// procOpts carries the optional wiring of newProcOpts.
type procOpts struct {
	taps   []func(string)
	stderr io.ReadCloser // when set, drained into a bounded tail (mirrored to os.Stderr)
	onExit func(spawn.ExitInfo)
}

// proc wraps one derived harness process.
type proc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt string
	taps      []func(string) // stdout record taps (the translator binding)

	mu   sync.Mutex // guards stdin writes
	pend sync.Mutex // guards pending ids (own lock: stdin writes may block)

	pending map[string]bool
	// reaped is set the moment cmd.Wait returns: until then the kernel
	// holds the child's pid (and with it the pgid), so the number cannot
	// have been handed to anyone else. Terminate's identity and provenance
	// proof is needed only once it is true.
	reaped atomic.Bool
	exited chan struct{}
	exit   spawn.ExitInfo // valid once exited is closed
	tail   *tailBuffer
}

// tailBuffer keeps the last stderrTailBytes bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailBytes = 4096

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > stderrTailBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-stderrTailBytes:]...)
	}
	t.mu.Unlock()
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func newProcOpts(cmd *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser, o procOpts) *proc {
	p := &proc{
		cmd:   cmd,
		stdin: stdin,
		// the kernel's birth record, read before the exit watcher below
		// can reap the child: the identity every teardown proof checks
		startedAt: processStartStamp(cmd.Process.Pid),
		pending:   map[string]bool{},
		exited:    make(chan struct{}),
		taps:      o.taps,
		tail:      &tailBuffer{},
	}
	readerDone := make(chan struct{})
	stderrDone := make(chan struct{})
	if o.stderr != nil {
		go func() {
			defer close(stderrDone)
			_, _ = io.Copy(io.MultiWriter(os.Stderr, p.tail), o.stderr)
			o.stderr.Close()
		}()
	} else {
		close(stderrDone)
	}
	go func() {
		// cmd.Stdout/Stderr are *os.File pipe ends (Spawn's own pipes), so
		// cmd.Wait never blocks on a copy: it returns when the direct
		// child exits. A grandchild (model provider connection, spawned
		// tool) that inherited a pipe keeps it open past that exit (#92):
		// the drain below waits for it only a bounded moment, and the
		// work item's teardown (killAgentTree at step end) releases it.
		_ = cmd.Wait()
		// the kernel may reuse the pid only from here on: Terminate runs its
		// identity/provenance proofs only after this ( R1)
		p.reaped.Store(true)
		// Let the stdout reader AND the stderr copier drain what the
		// dead process left in its pipes before the exit is announced:
		// the last records (the error turn's agent_end) and the stderr
		// tail decide the exit's class. Bounded: a grandchild holding a
		// pipe must not hold the exit back.
		drainDeadline := time.Now().Add(500 * time.Millisecond)
		for _, ch := range []chan struct{}{readerDone, stderrDone} {
			wait := time.Until(drainDeadline)
			if wait < 0 {
				wait = 0
			}
			select {
			case <-ch:
			case <-time.After(wait):
			}
		}
		p.exit = p.exitInfo()
		if o.onExit != nil {
			o.onExit(p.exit)
		}
		close(p.exited)
	}()
	go func() {
		defer close(readerDone)
		br := bufio.NewReaderSize(stdout, 64*1024)
		for {
			line, tooLong, rerr := readBoundedLine(br, maxRecordLineBytes)
			if rerr != nil {
				if rerr != io.EOF {
					fmt.Fprintf(os.Stderr, "runtime: harness stdout read failed: %v\n", rerr)
				}
				return
			}
			if tooLong {
				fmt.Fprintf(os.Stderr, "runtime: stdout line over %d bytes dropped: %v\n", maxRecordLineBytes, errRecordTooLong)
				continue // keep draining as bulk, never block the child
			}
			for _, t := range p.taps {
				t(line)
			}
			p.classify(line)
		}
	}()
	return p
}

// exitInfo reads the finished process's status.
func (p *proc) exitInfo() spawn.ExitInfo {
	info := spawn.ExitInfo{Code: -1, StderrTail: strings.TrimSpace(p.tail.String())}
	if ps := p.cmd.ProcessState; ps != nil {
		info.Code = ps.ExitCode()
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			info.Signal = ws.Signal().String()
		}
	}
	return info
}

// ExitInfo implements spawn.ExitReporter; meaningful once Done().
func (p *proc) ExitInfo() spawn.ExitInfo {
	select {
	case <-p.exited:
		return p.exit
	default:
		return spawn.ExitInfo{Code: -1}
	}
}

func (p *proc) PID() int          { return p.cmd.Process.Pid }
func (p *proc) StartedAt() string { return p.startedAt }

func (p *proc) Done() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

func (p *proc) Alive() bool {
	if p.Done() {
		return false
	}
	return p.cmd.Process != nil && p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// writeCommand frames one stdin JSON command.
func (p *proc) writeCommand(c piCommand) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin == nil {
		return fmt.Errorf("runtime: agent stdio closed")
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := p.stdin.Write(append(blob, '\n')); err != nil {
		return fmt.Errorf("runtime: agent not accepting commands: %w", err)
	}
	if c.ID != "" {
		p.pend.Lock()
		p.pending[c.ID] = true
		p.pend.Unlock()
	}
	return nil
}

// classify sorts one stdout line: response records repeating OUR command
// ids are observed (success=false is logged) and dropped; everything else
// is bulk.
func (p *proc) classify(line string) {
	var rec stdoutRecord
	if json.Unmarshal([]byte(line), &rec) != nil || rec.Type == "" {
		return // non-record noise: bulk
	}
	if rec.Type == "response" {
		p.observeResponse(rec)
	}
}

func (p *proc) observeResponse(rec stdoutRecord) {
	if rec.ID == nil || *rec.ID == "" {
		return
	}
	p.pend.Lock()
	_, ours := p.pending[*rec.ID]
	delete(p.pending, *rec.ID)
	p.pend.Unlock()
	if ours && rec.Success != nil && !*rec.Success {
		fmt.Fprintf(os.Stderr, "runtime: harness command %s failed: %s\n", *rec.ID, rec.Error)
	}
}

// Terminate kills the WHOLE process group and proves it empty: SIGTERM to
// -pgid, bounded wait for the group (not merely the leader), SIGKILL, wait
// again. While the leader is unreaped the group is ours by construction:
// the kernel cannot reuse a pid before its parent's wait collects it, so
// the group is signaled directly — the recorded startedAt is stamped after
// cmd.Start and a stall between fork and stamp must never turn a live
// step into "pid reuse; not signaled". Once reaped, the pid/pgid is just a
// number the kernel may have reused, and the group is signaled only when
// it is provably this dispatch's: the same identity and provenance proof
// Cleanup's L2 applies. (The window between the reap inside cmd.Wait and
// the reaped store is a few instructions; a reused pid would also have to
// become a group leader inside it.)
func (p *proc) Terminate(bound time.Duration) error {
	if p.cmd.Process == nil {
		return nil
	}
	// Setpgid: the child IS the group leader.
	pid := p.cmd.Process.Pid
	if !p.reaped.Load() {
		return killGroup(pid, bound)
	}
	target := CleanupTarget{PID: pid, Pgid: pid, StartedAt: p.startedAt}
	if !rootIdentityHolds(target) {
		return fmt.Errorf("runtime: step process %d no longer matches its recorded identity (pid reuse); process group not signaled", pid)
	}
	if !groupProvableOurs(target) {
		return fmt.Errorf("runtime: process group %d cannot be proven ours (leader gone, member predates the step); not signaled", pid)
	}
	return killGroup(pid, bound)
}

// readBoundedLine reads one newline-terminated line, never accumulating
// more than max bytes of CONTENT: an overlong line is drained and reported
// as tooLong so a hostile harness cannot balloon the reader's memory.
func readBoundedLine(br *bufio.Reader, max int) (line string, tooLong bool, err error) {
	const termSlack = 2
	var buf []byte
	for {
		chunk, rerr := br.ReadSlice('\n')
		if rerr == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > max+termSlack {
				for rerr == bufio.ErrBufferFull {
					chunk, rerr = br.ReadSlice('\n')
				}
				return "", true, nil
			}
			buf = append(buf, chunk...)
			continue
		}
		if rerr != nil {
			if rerr == io.EOF && len(buf)+len(chunk) > 0 {
				if len(buf)+len(chunk) > max {
					return "", true, nil
				}
				buf = append(buf, chunk...)
				return strings.TrimRight(string(buf), "\r"), false, nil
			}
			return "", false, rerr
		}
		buf = append(buf, chunk...)
		content := buf
		if len(content) > 0 && content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
			if len(content) > 0 && content[len(content)-1] == '\r' {
				content = content[:len(content)-1]
			}
		}
		if len(content) > max {
			return "", true, nil
		}
		return strings.TrimRight(string(content), "\r"), false, nil
	}
}
