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
	"errors"
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
	// StreamingBehavior is pi's rule for a prompt that arrives while a
	// run is active: "steer" queues it as a steering message (delivered
	// after the current assistant turn, before the next model call),
	// "followUp" after the run; absent, such a prompt is REFUSED (pi
	// 1.4.0 docs/rpc-commands.md "During streaming"; verified live, see
	// SendMessage). Idle, pi ignores it and starts a run.
	StreamingBehavior string `json:"streamingBehavior,omitempty"`
}

// stdout record probe shape: the harness's acknowledgement of a stdin
// command. pi: response records repeat our command ids; claude: a
// command_lifecycle record names the user message's uuid and its state
// (queued / started / completed — claudeUserMessage).
type stdoutRecord struct {
	Type    string  `json:"type"`
	Subtype string  `json:"subtype"` // claude system records (init = a turn starts)
	Command string  `json:"command"` // "prompt" on response records
	ID      *string `json:"id"`
	Success *bool   `json:"success"`
	Error   string  `json:"error"`
	// command_lifecycle (claude)
	CommandUUID string `json:"command_uuid"`
	State       string `json:"state"`
	// Data carries a prompt response's disposition: "started", "queued"
	// or "handled" (pi 1.4.0 docs/rpc-commands.md "prompt").
	Data struct {
		Disposition string `json:"disposition"`
	} `json:"data"`
	// turnRunning is the run state as the stream stood when the reader
	// read this record (observeLifecycle stamps it on the records it
	// hands a waiter): the harness's own order, not the waiter's wake-up.
	turnRunning bool
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

// SanitizedEnv answers the daemon's environment as every step process
// inherits it (the sanitize point below: no POLYFORGE_*/PF_* variable and
// no value equal to a secret) — the environment of the commands a bar the
// daemon runs in-process starts, so they see exactly what
// an agent's commands would.
func SanitizedEnv(secrets ...string) []string {
	return newEnvSanitizer(secrets...).childEnv()
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
	own := make(map[string]bool, len(additions))
	for _, kv := range additions {
		name, _, _ := strings.Cut(kv, "=")
		own[name] = true
	}
	out := make([]string, 0, len(os.Environ())+len(additions))
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		// an inherited copy of a name the additions set never reaches the
		// child (the step env's PATH replaces the daemon's)
		if strings.HasPrefix(name, "POLYFORGE_") || strings.HasPrefix(name, "PF_") || own[name] {
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
		// the daemon's tool rule — the step's own window when its lane
		// declares one — so verify-run can bound its whole run without a
		// pi-specific shell setting
		toolWindowEnv(stepToolWindow(s.cfg, p)),
	}
	// the daemon-owned step environment and the round (stepenv.go)
	additions = append(additions, stepEnvAdditions(s.cfg, p, s.bin())...)
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

// Spawn derives one persistent agent process: a new session (the agent
// leads it, so it leads its own process group — the cleanup layers collect
// step agents and command children by it — and has no controlling terminal:
// nothing the step runs can open /dev/tty and wait on the daemon's
// terminal), the dedicated step agent directory's allowlisted
// members refreshed (never pruned — see stepagentdir.go) BEFORE
// the process is born (a torn credential copy is a spawn-refusing error,
// never a silently degraded step agent), identity journal BEFORE the
// spawn returns, then the initial prompt as the session's first command.
//
// The process is RESIDENT across turns (contract v2, design §5.2): pi in
// rpc mode is a long-lived subprocess that idles after each run, and
// nothing here ends it at a turn boundary. Later owner messages reach it
// through the handle's SendMessage; the dispatch's owner (the driver)
// terminates it when the step's result envelope is accepted or rejected,
// or a supervision verdict retires the dispatch (lifecycle (iv),
// proc.Terminate closes the command channel first).
func (s *piSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	if p.InitialPrompt == "" {
		return nil, fmt.Errorf("runtime: spawn without an initial prompt")
	}
	if s.cfg.DataDir != "" {
		if err := s.cfg.refreshStepAgentDir(); err != nil {
			return nil, err
		}
	}
	proc, err := startHarness(s.buildCmd(p), p)
	if err != nil {
		return nil, err
	}
	if err := proc.writeCommand(piCommand{ID: "pf-init", Type: "prompt", Message: p.InitialPrompt}); err != nil {
		return nil, injectionFailed(proc, err)
	}
	return proc, nil
}

// startHarness starts a built step command — either harness — as the
// leader of a new session (Setsid, never Setctty): so its own process-group
// leader (the cleanup layers collect step agents and command children by
// the group) with no controlling terminal (the daemon runs on tmux's pty; a
// step inheriting it hung on an interactive shell or a /dev/tty prompt),
// with its own stdout/stderr pipes, wraps it in a
// supervised proc and writes the identity journal before returning. The
// stdin pipe is the proc's command channel for the whole dispatch (pi's
// RPC commands, claude's stream-json user messages): the harness decides
// how a later message is framed and acknowledged (SendMessage).
func startHarness(cmd *exec.Cmd, p SpawnParams) (*proc, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Own pipes (not cmd.StdoutPipe): cmd.Wait would close the read end
	// the instant the process exits and could drop the tail of the
	// stream — exactly the records (the error turn's agent_end) the exit
	// classification needs. With plain *os.File sinks Wait copies
	// nothing, and the reader sees EOF once every writer is gone.
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return nil, fmt.Errorf("runtime: harness start failed: %w", err)
	}
	afterHarnessStartFn()
	outW.Close()
	errW.Close()
	proc := newProcOpts(cmd, stdin, outR, procOpts{
		taps:    recordTaps(p),
		stderr:  errR,
		onExit:  p.OnExit,
		harness: harness.Of(p.Harness),
		spawnN:  p.SpawnN,
	})
	// Identity journal before Spawn returns: a crash between derivation and
	// the runtime's persisted record must never leave an undiscoverable
	// orphan. Its failure tears the fresh process down.
	if p.Journal != nil {
		if jerr := p.Journal(cmd.Process.Pid, proc.startedAt); jerr != nil {
			_ = proc.Terminate(5 * time.Second)
			return nil, fmt.Errorf("runtime: spawn journal failed (process torn down): %w", jerr)
		}
	}
	return proc, nil
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
	// the exit is announced once the stdout reader and the stderr copy
	// caught up (or their equal bounds passed): wait past that bound, so a
	// slow copy still lands here
	select {
	case <-proc.exited:
	case <-time.After(stderrDrainBound + 2*time.Second):
	}
	// the catch-up first, then the tail (exitInfo's order)
	note := ""
	if !proc.stderrCopyCaughtUp() {
		note = " [" + spawn.StderrIncompleteNote + "]"
	}
	if detail := strings.TrimSpace(proc.tail.String()); detail != "" {
		return fmt.Errorf("runtime: initial prompt injection failed (process torn down): %w; harness stderr: %s%s", err, detail, note)
	}
	return fmt.Errorf("runtime: initial prompt injection failed (process torn down): %w%s", err, note)
}

// ---------------------------------------------------------------------------
// Proc: the supervised handle.

const (
	maxRecordLineBytes = 4 << 20
	// stdoutDrainBound bounds how long the exit announcement waits, after
	// the reap, for the stdout reader to catch up with what the dead
	// process left in its pipe — every complete record through the taps
	// (the error turn's agent_end decides the exit's class). The rule is
	// stderr's (pipeReader): a descendant merely holding the pipe open
	// costs nothing — the catch-up is an empty pipe, not EOF. The bound is
	// spent while the reader cannot keep up (a loaded host, slow taps), or
	// while a descendant floods stdout after the exit.
	stdoutDrainBound = 5 * time.Second
	// stderrDrainBound bounds how long it waits for the stderr copy to
	// catch up with what the dead process left in its pipe (
	// its own bound, never a share of stdout's). A descendant merely
	// holding the pipe open costs nothing here — the catch-up is an empty
	// pipe, not EOF. The bound is spent while the copy itself cannot run,
	// or while a descendant keeps writing stderr after the exit faster
	// than the copy drains it (the pipe never reads empty): that holds the
	// announcement up to this bound and marks the exit incomplete.
	stderrDrainBound = 5 * time.Second
)

var errRecordTooLong = fmt.Errorf("runtime: stdout line exceeded the bounded reader limit")

// procOpts carries the optional wiring of newProcOpts.
type procOpts struct {
	taps []func(string)
	// stderr, when set, is the read end of the harness's stderr pipe
	// (os.Pipe): copied into a bounded tail, mirrored to os.Stderr.
	stderr *os.File
	// stderrBound overrides stderrDrainBound (tests; 0 = the default).
	stderrBound time.Duration
	// stdoutBound overrides stdoutDrainBound (tests; 0 = the default).
	stdoutBound time.Duration
	// afterEmptyStderrRead, when set, runs after a stderr read found the
	// pipe empty and before the copy acts on it: a seam so a test can hold
	// the copy there, descheduled, across the harness's exit (tests only).
	afterEmptyStderrRead func(*proc)
	onExit               func(spawn.ExitInfo)
	// harness is the step harness the process runs (harness.Pi /
	// harness.Claude): it decides how SendMessage frames a later message
	// and which stdout records acknowledge it.
	harness string
	// spawnN is the dispatch's number within its session: the per-dispatch
	// identity every message id carries (messageCommandID), as the initial
	// prompt's id does (claudeInitMessageID).
	spawnN int
}

// proc wraps one derived harness process.
type proc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt string
	taps      []func(string) // stdout record taps (the translator binding)
	harness   string
	spawnN    int

	// mu guards the stdin field (the channel's open state) and is never
	// held across a Write: a Write parks on a full pipe (a harness not
	// draining its stdin, a message near the 64 KiB pipe buffer) and no
	// ctx interrupts it, so the teardown's closeStdin must never wait on
	// one — it swaps the field out under mu and closes the file outside
	// it, which unblocks a parked Write (the poller answers it ErrClosed).
	mu sync.Mutex
	// wmu serialises the stdin writes so two framed lines never
	// interleave; closeStdin never takes it.
	wmu  sync.Mutex
	pend sync.Mutex // guards pending ids (own lock: stdin writes may block)

	// pending maps an in-flight command id to its waiter: nil for a
	// fire-and-forget command (the initial prompt; a failed response is
	// logged), a channel for sendCommand's caller (the response record
	// is handed over). The entry is removed when the response arrives.
	pending map[string]chan stdoutRecord
	// msgSeq numbers the owner messages sent through SendMessage within
	// this process; messageCommandID pairs it with spawnN, so a message id
	// is unique within the harness SESSION, not just the process: claude
	// keeps the id in its native session record, and on `--resume` a
	// message reusing an id the session already holds is silently dropped
	// (sendClaudeMessage).
	msgSeq atomic.Int64
	// turnRunning follows the harness's run boundary from its own stream.
	// pi: set at agent_start, cleared at agent_settled (the moment pi
	// stops continuing on its own — retries, compaction, queued steering
	// and follow-up messages all happen before it). Between the two a
	// prompt is queued, not started (pi's isStreaming is exactly this
	// window). claude: set at system/init (every turn a message starts
	// opens with one), cleared at the turn's result record; a message
	// absorbed mid-turn opens no init of its own.
	turnRunning atomic.Bool
	// reaped is set the moment cmd.Wait returns: until then the kernel
	// holds the child's pid (and with it the pgid), so the number cannot
	// have been handed to anyone else. Terminate's identity and provenance
	// proof is needed only once it is true.
	reaped atomic.Bool
	exited chan struct{}
	tail   *tailBuffer
	// stdoutCaughtUp is closed once every complete record the reaped
	// process wrote on stdout has been through the taps (readStdout), or
	// the reader ended.
	stdoutCaughtUp chan struct{}
	// stderrCaughtUp is closed once the stderr copy holds everything the
	// reaped process wrote (copyStderr), or the copy ended.
	stderrCaughtUp chan struct{}
	// afterEmptyStderrRead: procOpts' test seam (nil in production).
	afterEmptyStderrRead func(*proc)
}

// tailBuffer keeps the last stderrTailBytes bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const stderrTailBytes = 4096

// tailWriteHook, when set, runs at the top of every tailBuffer.Write,
// before it appends: a seam so the slow-copy tests can stand in for a
// loaded host, where the stderr copy is descheduled past the harness's
// exit (a sleep), or hold the copy until they release it (a gate).
// Production never sets it.
var tailWriteHook atomic.Pointer[func()]

func (t *tailBuffer) Write(b []byte) (int, error) {
	if h := tailWriteHook.Load(); h != nil {
		(*h)()
	}
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

// newProcOpts wraps a started harness process. stdout (and o.stderr, when
// set) is the read end of an os.Pipe: the post-exit catch-up (pipeReader)
// reads it through its raw descriptor.
func newProcOpts(cmd *exec.Cmd, stdin io.WriteCloser, stdout *os.File, o procOpts) *proc {
	p := &proc{
		cmd:   cmd,
		stdin: stdin,
		// the kernel's birth record, read before the exit watcher below
		// can reap the child: the identity every teardown proof checks
		startedAt:      processStartStamp(cmd.Process.Pid),
		pending:        map[string]chan stdoutRecord{},
		harness:        o.harness,
		spawnN:         o.spawnN,
		exited:         make(chan struct{}),
		taps:           o.taps,
		tail:           &tailBuffer{},
		stdoutCaughtUp: make(chan struct{}),
		stderrCaughtUp: make(chan struct{}),

		afterEmptyStderrRead: o.afterEmptyStderrRead,
	}
	go p.readStdout(stdout)
	if o.stderr != nil {
		go p.copyStderr(o.stderr)
	} else {
		close(p.stderrCaughtUp)
	}
	stdoutBound := o.stdoutBound
	if stdoutBound <= 0 {
		stdoutBound = stdoutDrainBound
	}
	stderrBound := o.stderrBound
	if stderrBound <= 0 {
		stderrBound = stderrDrainBound
	}
	go func() {
		// cmd.Stdout/Stderr are *os.File pipe ends (Spawn's own pipes), so
		// cmd.Wait never blocks on a copy: it returns when the direct
		// child exits. A grandchild (model provider connection, spawned
		// tool) that inherited a pipe keeps it open past that exit (#92):
		// the catch-ups below never wait for its EOF, and the work item's
		// teardown (killAgentTree at step end) releases it.
		_ = cmd.Wait()
		// the kernel may reuse the pid only from here on: Terminate runs its
		// identity/provenance proofs only after this (R1)
		p.reaped.Store(true)
		reapedAt := time.Now()
		// wake a reader parked on an empty pipe: it re-reads it now that
		// the process is gone, to report whether it caught up
		_ = stdout.SetReadDeadline(reapedAt)
		if o.stderr != nil {
			_ = o.stderr.SetReadDeadline(reapedAt)
		}
		// Let the stdout reader AND the stderr copy catch up with what the
		// dead process left in its pipes before the exit is announced: the
		// last records (the error turn's agent_end) and the stderr tail
		// decide the exit's class. Each has its own bound, measured from
		// the reap.
		if !closedBy(p.stdoutCaughtUp, reapedAt.Add(stdoutBound)) {
			fmt.Fprintf(os.Stderr, "runtime: harness %d exited (%s) but its stdout reader had not caught up %s later: the exit's last stdout records may be missing\n",
				cmd.Process.Pid, cmd.ProcessState, stdoutBound)
		}
		if !closedBy(p.stderrCaughtUp, reapedAt.Add(stderrBound)) {
			fmt.Fprintf(os.Stderr, "runtime: harness %d exited (%s) but its stderr copy had not caught up %s later: the exit's stderr tail may be incomplete\n",
				cmd.Process.Pid, cmd.ProcessState, stderrBound)
		}
		if o.onExit != nil {
			o.onExit(p.exitInfo())
		}
		close(p.exited)
	}()
	return p
}

// readStdout reads the harness's stdout records until EOF, handing every
// complete line to the taps and then to classify, and closes
// stdoutCaughtUp once its pipeReader caught up after the reap. bufio asks
// the pipeReader for more bytes only when no complete line is buffered,
// and each line has been through every tap before the next is asked for:
// so when a read started after the reap finds the pipe empty, every
// complete record the dead process wrote has reached the taps. A
// descendant still holding the pipe may add more; the reader keeps
// handing that on until EOF. Ending (EOF, a failed read) also closes it,
// after the last line was handed on.
func (p *proc) readStdout(f *os.File) {
	defer f.Close()
	r, err := newPipeReader(f, &p.reaped, p.stdoutCaughtUp, nil)
	if err != nil {
		close(p.stdoutCaughtUp)
		fmt.Fprintf(os.Stderr, "runtime: harness stdout cannot be read: %v\n", err)
		return
	}
	defer r.catchUp()
	br := bufio.NewReaderSize(r, 64*1024)
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
}

// closedBy waits for ch to close until deadline and reports whether it did.
func closedBy(ch <-chan struct{}, deadline time.Time) bool {
	select {
	case <-ch:
		return true
	default:
	}
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-ch:
		return true
	case <-t.C:
		return false
	}
}

// pipeReader is an io.Reader over the read end of a harness's pipe that
// knows when its consumer has caught up with the exited process: it
// closes caughtUp the first time a read STARTED after the reap finds the
// pipe empty. A pipe write is in the pipe once it returns, and a consumer
// that handles each chunk before it asks for the next (the stderr copy,
// the stdout reader) then holds everything the dead process wrote. Only a
// descendant still holding the pipe can add more; the reader keeps
// returning that until EOF. A descendant that pauses lets the pipe read
// empty and costs nothing; one that keeps writing so fast that no read
// after the reap finds the pipe empty holds the exit announcement up to
// the pipe's bound. The consumer calls catchUp itself when it ends (EOF, a
// failed read), after it handled the last bytes.
//
// All its state is touched only by the consumer's goroutine; reaped is
// the proc's flag, caughtUp the proc's channel the reap waits on.
type pipeReader struct {
	f        *os.File
	rc       syscall.RawConn
	reaped   *atomic.Bool
	caughtUp chan struct{}
	caught   bool
	// afterEmptyRead, when set, runs after a read found the pipe empty and
	// before the reader acts on it (a test seam; nil in production).
	afterEmptyRead func()
}

func newPipeReader(f *os.File, reaped *atomic.Bool, caughtUp chan struct{}, afterEmptyRead func()) (*pipeReader, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &pipeReader{f: f, rc: rc, reaped: reaped, caughtUp: caughtUp, afterEmptyRead: afterEmptyRead}, nil
}

// catchUp closes caughtUp once.
func (r *pipeReader) catchUp() {
	if !r.caught {
		r.caught = true
		close(r.caughtUp)
	}
}

// Read blocks until the pipe has bytes (returned), every writer is gone
// (io.EOF) or the read fails. An empty read after the reap declares the
// catch-up and keeps waiting; the reap's read deadline (its wake-up for a
// reader parked on the empty pipe) is dropped and the pipe re-read.
func (r *pipeReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for {
		var n int
		var rerr error
		empty := false
		err := r.rc.Read(func(fd uintptr) bool {
			// read the flag BEFORE the read: an empty read proves the
			// catch-up only if it was taken after the reap — one taken
			// while the process was alive is stale by the time the reader
			// acts on it (it may have written and exited in between)
			wasReaped := r.reaped.Load()
			n, rerr = syscall.Read(int(fd), b)
			for rerr == syscall.EINTR {
				n, rerr = syscall.Read(int(fd), b)
			}
			if rerr == syscall.EAGAIN {
				if r.afterEmptyRead != nil {
					r.afterEmptyRead()
				}
				if !r.caught && wasReaped {
					empty = true
					return true
				}
				return false // park until the pipe is readable
			}
			return true
		})
		switch {
		case errors.Is(err, os.ErrDeadlineExceeded):
			// the reap's wake-up: drop the deadline and read again
			_ = r.f.SetReadDeadline(time.Time{})
			continue
		case err != nil:
			return 0, err
		case empty:
			r.catchUp()
			continue
		case rerr != nil:
			return 0, rerr
		case n <= 0:
			return 0, io.EOF // every writer is gone
		}
		return n, nil
	}
}

// copyStderr copies the harness's stderr into the tail (then mirrors it
// to the daemon's stderr) until EOF; its pipeReader closes stderrCaughtUp
// once the tail holds everything the reaped process wrote (each chunk is
// in the tail before the next read). Ending (EOF, a failed read) also
// closes it.
func (p *proc) copyStderr(f *os.File) {
	defer f.Close()
	var hook func()
	if p.afterEmptyStderrRead != nil {
		hook = func() { p.afterEmptyStderrRead(p) }
	}
	r, err := newPipeReader(f, &p.reaped, p.stderrCaughtUp, hook)
	if err != nil {
		close(p.stderrCaughtUp)
		fmt.Fprintf(os.Stderr, "runtime: harness stderr cannot be read: %v\n", err)
		return
	}
	defer r.catchUp()
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "runtime: harness stderr read failed: %v\n", err)
			}
			return
		}
		_, _ = p.tail.Write(buf[:n])
		_, _ = os.Stderr.Write(buf[:n])
	}
}

// exitInfo reads the finished process's status and its stderr tail as it
// stands NOW: a copy that catches up after the exit announcement still
// reaches every later reader, and StderrIncomplete says whether it had;
// StdoutIncomplete likewise says whether the stdout reader had caught up
// (every record the dead process wrote through the taps).
func (p *proc) exitInfo() spawn.ExitInfo {
	// the catch-ups first, then the tail: a tail read after a closed
	// catch-up holds everything it promises
	info := spawn.ExitInfo{Code: -1,
		StdoutIncomplete: !p.stdoutReaderCaughtUp(),
		StderrIncomplete: !p.stderrCopyCaughtUp()}
	info.StderrTail = strings.TrimSpace(p.tail.String())
	if ps := p.cmd.ProcessState; ps != nil {
		info.Code = ps.ExitCode()
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			info.Signal = ws.Signal().String()
		}
	}
	return info
}

// stdoutReaderCaughtUp reports whether stdoutCaughtUp is closed.
func (p *proc) stdoutReaderCaughtUp() bool {
	select {
	case <-p.stdoutCaughtUp:
		return true
	default:
		return false
	}
}

// stderrCopyCaughtUp reports whether stderrCaughtUp is closed.
func (p *proc) stderrCopyCaughtUp() bool {
	select {
	case <-p.stderrCaughtUp:
		return true
	default:
		return false
	}
}

// ExitInfo implements spawn.ExitReporter; meaningful once Done(). Each
// call re-reads the stderr tail (exitInfo).
func (p *proc) ExitInfo() spawn.ExitInfo {
	select {
	case <-p.exited:
		return p.exitInfo()
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

// writeCommand frames one stdin JSON command (pi's RPC protocol). An id
// not registered by sendCommand is fire-and-forget: its failed response
// is logged.
func (p *proc) writeCommand(c piCommand) error {
	blob, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return p.writeLine(c.ID, append(blob, '\n'))
}

// writeClaudeUser frames one stream-json user message for claude's stdin
// (claudeUserMessage): the initial prompt (fire-and-forget) and the
// owner messages sendClaudeMessage awaits.
func (p *proc) writeClaudeUser(id, text string) error {
	line, err := claudeUserMessage(id, text)
	if err != nil {
		return err
	}
	return p.writeLine(id, line)
}

// writeLine writes one framed stdin line and registers its id (when it
// has one) as in flight; an id nobody awaits is fire-and-forget. A write
// that fails forgets the id again. The Write runs outside mu (wmu keeps
// the lines whole): a closeStdin racing it closes the file under it,
// and the Write fails instead of holding the teardown.
func (p *proc) writeLine(id string, line []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("runtime: agent stdio closed")
	}
	if id != "" {
		p.pend.Lock()
		if _, registered := p.pending[id]; !registered {
			p.pending[id] = nil
		}
		p.pend.Unlock()
	}
	if _, err := stdin.Write(line); err != nil {
		p.forget(id)
		return fmt.Errorf("runtime: agent not accepting commands: %w", err)
	}
	return nil
}

// sendCommand writes one command and waits for its response record,
// bounded by ctx; the process's exit ends the wait too.
func (p *proc) sendCommand(ctx context.Context, c piCommand) (stdoutRecord, error) {
	if c.ID == "" {
		return stdoutRecord{}, fmt.Errorf("runtime: a command awaiting its response needs an id")
	}
	ch := make(chan stdoutRecord, 1)
	p.pend.Lock()
	p.pending[c.ID] = ch
	p.pend.Unlock()
	if err := p.writeCommand(c); err != nil {
		return stdoutRecord{}, err
	}
	select {
	case rec := <-ch:
		return rec, nil
	case <-ctx.Done():
		p.forget(c.ID)
		return stdoutRecord{}, fmt.Errorf("runtime: harness command %s unanswered: %w", c.ID, ctx.Err())
	case <-p.exited:
		// the exit is announced after the stdout drain: a response that
		// landed in the same instant is still the answer
		select {
		case rec := <-ch:
			return rec, nil
		default:
		}
		p.forget(c.ID)
		return stdoutRecord{}, fmt.Errorf("runtime: harness exited before answering command %s", c.ID)
	}
}

// forget drops a pending command id (a write that failed, a wait that
// ended before the response).
func (p *proc) forget(id string) {
	if id == "" {
		return
	}
	p.pend.Lock()
	delete(p.pending, id)
	p.pend.Unlock()
}

// closeStdin closes the command channel (idempotent): the harness reads
// EOF — pi's orderly shutdown request — and every later writeCommand
// fails as "stdio closed". It never waits on an in-flight Write (mu is
// not held across one, wmu is not taken here): the Close unblocks a Write
// parked on a full pipe, which then fails.
func (p *proc) closeStdin() {
	p.mu.Lock()
	stdin := p.stdin
	p.stdin = nil
	p.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
}

// SendMessage implements spawn.Messenger: one owner message into the
// resident pi process, as a `prompt` command carrying
// streamingBehavior "steer". pi itself decides, atomically, what that
// means at the moment it reads the command — the client never has to
// guess whether a turn is running, and cannot lose the race it would
// otherwise be in (a turn starting or ending between a TurnRunning
// check and the write):
//
//   - idle: the message opens a new run (disposition "started");
//   - mid-run: the message is queued as a steering message and handed to
//     the model after the current assistant turn finishes its tool calls,
//     before the next model call (disposition "queued"), exactly what the
//     `steer` command does;
//   - a bare `prompt` mid-run (no streamingBehavior) is REFUSED with
//     success=false "Agent is already processing. Specify
//     streamingBehavior ('steer' or 'followUp') to queue the message." and
//     never reaches the model or the session file.
//
// Verified live on pi 1.4.0 (2026-11-10, `pi --mode rpc` on the cheap
// tier's model, a throwaway session): the queued message appeared
// in the stdout stream as a user message (`message_start` role=user, text
// verbatim) at the next turn boundary and in the session file; the
// refused one appeared in neither; a message sent after agent_settled was
// "started". The acknowledgement does not guarantee delivery (a queue can
// be cleared, the process can die before the turn boundary): the embedded
// marker (spawn.MessageMarker) in the transcript is the proof. The
// response is awaited bounded by ctx.
//
// A claude process takes the same message as a stream-json user message
// (sendClaudeMessage).
func (p *proc) SendMessage(ctx context.Context, m spawn.Message) (spawn.Delivery, error) {
	if p.Done() {
		return spawn.Delivery{}, fmt.Errorf("runtime: harness process %d is gone", p.cmd.Process.Pid)
	}
	id := messageCommandID(p.spawnN, p.msgSeq.Add(1))
	if p.harness == harness.Claude {
		return p.sendClaudeMessage(ctx, id, m)
	}
	rec, err := p.sendCommand(ctx, piCommand{
		ID: id, Type: "prompt", Message: spawn.MessageText(m), StreamingBehavior: "steer",
	})
	if err != nil {
		return spawn.Delivery{}, err
	}
	if rec.Success == nil || !*rec.Success {
		return spawn.Delivery{}, fmt.Errorf("runtime: harness refused message %s: %s", id, rec.Error)
	}
	return spawn.Delivery{CommandID: id, Disposition: rec.Data.Disposition}, nil
}

// messageCommandID is the command id (claude: the stream-json uuid) of
// the n-th owner message of dispatch spawnN: `pf-msg-<spawnN>-<n>`,
// unique within the harness session as claudeInitMessageID is for the
// prompt. A process-scoped `pf-msg-<n>` was measured to collide on
// `--resume` (Claude Code 2.1.322, PR 6a review): the resumed session
// already held `pf-msg-1`, and claude answered the new message of that
// uuid with a lone `command_lifecycle completed` — no turn, no reply.
func messageCommandID(spawnN int, n int64) string {
	return fmt.Sprintf("pf-msg-%d-%d", spawnN, n)
}

// sendClaudeMessage writes one owner message into the resident claude
// process as a stream-json user message whose uuid is the command id,
// and waits, bounded by ctx, for claude's acknowledgement: the
// command_lifecycle records naming that uuid (Claude Code 2.1.322,
// measured by the PR 6a probe). A message claude takes is acknowledged
// `queued` and then `started` the moment it hands it to the model —
// within the same millisecond when idle (a turn of its own), after the
// running turn's current tool call when a turn is running (the message
// is absorbed into that turn; one result closes it). So: `started` is
// DispositionStarted; `queued` is DispositionQueued at once while a turn
// is running (TurnRunning), else the wait goes on for `started` and
// ctx's end still answers queued — accepted either way; the embedded
// marker in the transcript is the proof of delivery, as for pi. "A turn
// is running" is read at the `queued` record's place in the stream (its
// turnRunning stamp), never at the moment this wait wakes: by then the
// reader may be past the `started` and the `system/init` of the turn the
// message opened itself, and an idle process would read as queued.
//
// Claude does refuse one message, silently: a uuid its session already
// holds (the PR 6a review, 2.1.322, on `--resume`) draws a lone
// `completed` — no `queued`, no `started`, no turn, no reply. The
// daemon's own ids no longer provoke it (messageCommandID), and it is
// the one shape this wait treats as a refusal: an error, by the
// Messenger contract (the message did not reach the model), never a
// Delivery. A
// `completed` after `queued` or `started` is the normal end of a taken
// message. No acknowledgement at all within ctx, or the process's exit,
// is an error too: the message did not reach claude.
func (p *proc) sendClaudeMessage(ctx context.Context, id string, m spawn.Message) (spawn.Delivery, error) {
	ch := make(chan stdoutRecord, 4)
	p.pend.Lock()
	p.pending[id] = ch
	p.pend.Unlock()
	if err := p.writeClaudeUser(id, spawn.MessageText(m)); err != nil {
		return spawn.Delivery{}, err
	}
	queued := false
	// taken reports what one acknowledgement record settles: the message
	// opened or joined a turn (started; completed after queued/started),
	// or claude dropped it (a lone completed) — an error.
	taken := func(rec stdoutRecord) (spawn.Delivery, error) {
		p.forget(id)
		if rec.State == claudeLifecycleCompleted && !queued {
			return spawn.Delivery{}, fmt.Errorf("runtime: harness dropped message %s: claude completed it without queuing or starting it (a uuid its session already holds?)", id)
		}
		return spawn.Delivery{CommandID: id, Disposition: spawn.DispositionStarted}, nil
	}
	for {
		select {
		case rec := <-ch:
			if rec.State != spawn.DispositionQueued {
				return taken(rec)
			}
			queued = true
			if rec.turnRunning {
				return spawn.Delivery{CommandID: id, Disposition: spawn.DispositionQueued}, nil
			}
		case <-ctx.Done():
			p.forget(id)
			if queued {
				return spawn.Delivery{CommandID: id, Disposition: spawn.DispositionQueued}, nil
			}
			return spawn.Delivery{}, fmt.Errorf("runtime: harness message %s unacknowledged: %w", id, ctx.Err())
		case <-p.exited:
			select {
			case rec := <-ch:
				if rec.State != spawn.DispositionQueued {
					return taken(rec)
				}
				queued = true
			default:
			}
			p.forget(id)
			if queued {
				return spawn.Delivery{CommandID: id, Disposition: spawn.DispositionQueued}, nil
			}
			return spawn.Delivery{}, fmt.Errorf("runtime: harness exited before acknowledging message %s", id)
		}
	}
}

// claudeLifecycleCompleted is the command_lifecycle state that closes a
// message's life in claude; the other two states are the Disposition
// words (queued, started).
const claudeLifecycleCompleted = "completed"

// TurnRunning implements spawn.TurnReporter.
func (p *proc) TurnRunning() bool { return p.turnRunning.Load() }

// classify sorts one stdout line: the harness's acknowledgements of OUR
// command ids are observed (pi response records handed to their waiter,
// or success=false logged; claude command_lifecycle records handed to
// their waiter) and dropped; the run boundary records move turnRunning;
// everything else is bulk.
func (p *proc) classify(line string) {
	var rec stdoutRecord
	if json.Unmarshal([]byte(line), &rec) != nil || rec.Type == "" {
		return // non-record noise: bulk
	}
	switch rec.Type {
	case "response":
		p.observeResponse(rec)
	case "command_lifecycle":
		p.observeLifecycle(rec)
	case "agent_start":
		p.turnRunning.Store(true)
	case "agent_settled":
		p.turnRunning.Store(false)
	case "system":
		if rec.Subtype == "init" {
			p.turnRunning.Store(true)
		}
	case "result":
		p.turnRunning.Store(false)
	}
}

// observeLifecycle hands claude's acknowledgement of one of OUR message
// uuids to its waiter (every state; the entry goes once the message is
// past queued) and drops the rest (the initial prompt's, a waiter that
// gave up).
func (p *proc) observeLifecycle(rec stdoutRecord) {
	if rec.CommandUUID == "" || rec.State == "" {
		return
	}
	p.pend.Lock()
	defer p.pend.Unlock()
	ch, ours := p.pending[rec.CommandUUID]
	if !ours {
		return
	}
	if rec.State != spawn.DispositionQueued {
		delete(p.pending, rec.CommandUUID)
	}
	// classify, the only writer of turnRunning, runs on this goroutine:
	// the load is the run state at this record's place in the stream
	rec.turnRunning = p.turnRunning.Load()
	if ch != nil {
		select {
		case ch <- rec:
		default:
		}
	}
}

func (p *proc) observeResponse(rec stdoutRecord) {
	if rec.ID == nil || *rec.ID == "" {
		return
	}
	p.pend.Lock()
	ch, ours := p.pending[*rec.ID]
	delete(p.pending, *rec.ID)
	p.pend.Unlock()
	if !ours {
		return
	}
	if ch != nil {
		ch <- rec // buffered: the one response per id
		return
	}
	if rec.Success != nil && !*rec.Success {
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
//
// The command channel is closed first (lifecycle (iv), design §5.2: the
// daemon "closes stdin / terminates the still-alive process"): pi reads
// the EOF as its orderly shutdown request (verified live on 1.4.0: exit 0
// within milliseconds, mid-run included); an idle claude exits 0 on it at
// once and one mid-turn after its turn (2.1.322); the signals below bound
// both, and no later message can be written into a process being torn
// down.
func (p *proc) Terminate(bound time.Duration) error {
	if p.cmd.Process == nil {
		return nil
	}
	p.closeStdin()
	// Setsid: the child leads a new session, so it IS the group leader.
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
