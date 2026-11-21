package runtime

// stepagent_lifecycle_test.go: the daemon-owned subagent extension's
// process lifecycle (polyforge#200 AC1/AC2), driven for real: a Go
// wrapper loads the EMBEDDED extension bytes into a throwaway node
// project (node's native type stripping runs the .ts as shipped; the
// bare imports resolve through symlinks to the installed pi package) and
// calls the registered tool against a stub ExtensionAPI, while a fake
// `pi` on PATH plays the child with scriptable signal behaviour. The
// assertions read REAL process state (/proc pids, pgids, signal logs) —
// never just the tool's own report.
//
// Linux-only by contract (the owner's Q7 ruling: Linux must run, macOS is
// not required in the same round): the /proc reads have no darwin twin in
// this harness, and the production host is a Linux container.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// extDriverMjs is the node side of the wrapper: it imports the real
// extension, captures the registered tool, runs one scripted scenario and
// prints one PFEXT-RESULT line. The execute call is raced against a
// deadline so a hung extension (the very defect this pins) still yields a
// bounded, non-zero answer — the run never relies on an outer timeout.
const extDriverMjs = `import * as fs from "node:fs";
const scenario = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
for (const [k, v] of Object.entries(scenario.env || {})) process.env[k] = String(v);
const mod = await import("./index.ts");
let tool;
mod.default({ registerTool: (t) => (tool = t) });
function pgidOf(pid) {
	const stat = fs.readFileSync("/proc/" + pid + "/stat", "utf8");
	const rest = stat.slice(stat.lastIndexOf(")") + 2).split(" ");
	return parseInt(rest[2], 10); // field 5 of stat, 3rd after (state, ppid)
}
const out = { ownPgid: pgidOf(process.pid) };
const emit = () => { console.log("PFEXT-RESULT " + JSON.stringify(out)); process.exit(0); };
try {
	const ac = new AbortController();
	if (scenario.preAborted) ac.abort();
	if (scenario.abortAfterMs) setTimeout(() => ac.abort(), scenario.abortAfterMs);
	const ctx = { cwd: process.cwd(), model: undefined, thinkingLevel: undefined, hasUI: false, isProjectTrusted: () => true };
	const t0 = Date.now();
	const hangTimer = setTimeout(() => {
		out.hung = true;
		out.resolvedMs = Date.now() - t0;
		emit();
	}, scenario.deadlineMs || 30000);
	hangTimer.unref();
	let raced;
	try {
		const r = await Promise.race([
			tool.execute("c1", scenario.params, ac.signal, undefined, ctx).then((v) => ({ v }), (e) => ({ e })),
			new Promise((res) => setTimeout(() => res({ hung: true }), scenario.deadlineMs || 30000)),
		]);
		raced = r;
	} finally {
		clearTimeout(hangTimer);
	}
	out.resolvedMs = Date.now() - t0;
	if (raced.hung) { out.hung = true; emit(); }
	if (raced.e) out.thrown = String((raced.e && raced.e.message) || raced.e);
	else out.result = raced.v;
	if (scenario.lateAbortMs) {
		await new Promise((res) => setTimeout(res, scenario.lateAbortMs));
		ac.abort();
		await new Promise((res) => setTimeout(res, 200));
	}
	emit();
} catch (e) {
	out.driverError = String((e && e.stack) || e);
	emit();
}
`

// extProject lays out the throwaway node project: node_modules symlinks
// into the installed pi package, the embedded extension bytes, a worker
// definition, the driver script and a bin/ for the fake pi.
func extProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required to drive the embedded subagent extension: %v", err)
	}
	pkg := piPackageDir(t)
	dir := t.TempDir()
	nm := filepath.Join(dir, "node_modules", "@earendil-works")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	// The pi package's own node_modules does not nest itself: the
	// pi-coding-agent entry links to the package directory itself.
	targets := map[string]string{
		"pi-agent-core":   filepath.Join(pkg, "node_modules", "@earendil-works", "pi-agent-core"),
		"pi-ai":           filepath.Join(pkg, "node_modules", "@earendil-works", "pi-ai"),
		"pi-tui":          filepath.Join(pkg, "node_modules", "@earendil-works", "pi-tui"),
		"pi-coding-agent": pkg,
	}
	for m, target := range targets {
		if err := os.Symlink(target, filepath.Join(nm, m)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(pkg, "node_modules", "typebox"), filepath.Join(dir, "node_modules", "typebox")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"index.ts", "agents.ts"} {
		b, err := stepagentassets.FS.ReadFile("extensions/subagent/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	agentDir := filepath.Join(dir, "agentdir", "agents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	worker := "---\nname: worker\ndescription: test worker\n---\ntest worker body\n"
	if err := os.WriteFile(filepath.Join(agentDir, "worker.md"), []byte(worker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(extDriverMjs), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// piPackageDir resolves the installed @earendil-works/pi-coding-agent
// package directory from the pi binary on PATH.
func piPackageDir(t *testing.T) string {
	t.Helper()
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Fatalf("pi is required to resolve the extension's dependencies: %v", err)
	}
	real, err := filepath.EvalSymlinks(piBin)
	if err != nil {
		t.Fatal(err)
	}
	for d := filepath.Dir(real); ; d = filepath.Dir(d) {
		b, err := os.ReadFile(filepath.Join(d, "package.json"))
		if err == nil && bytes.Contains(b, []byte(`"@earendil-works/pi-coding-agent"`)) {
			return d
		}
		if d == filepath.Dir(d) {
			t.Fatalf("could not locate the pi package above %s", real)
		}
	}
}

// extRun runs one scenario against the driver and returns the parsed
// PFEXT-RESULT object. The driver bounds its own wait (deadlineMs), so a
// hung extension answers bounded and non-zero instead of hanging the
// test.
type extRunOut struct {
	OwnPgid    int            `json:"ownPgid"`
	ResolvedMs int64          `json:"resolvedMs"`
	Hung       bool           `json:"hung"`
	Thrown     string         `json:"thrown"`
	DriverErr  string         `json:"driverError"`
	Result     map[string]any `json:"result"`
}

func extRun(t *testing.T, dir string, scenario map[string]any, fakePi string) extRunOut {
	t.Helper()
	scenarioPath := filepath.Join(dir, "scenario.json")
	blob, _ := json.Marshal(scenario)
	if err := os.WriteFile(scenarioPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pi"), []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	nodeBin, _ := exec.LookPath("node")
	cmd := exec.Command(nodeBin, "driver.mjs", scenarioPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"),
		"PI_CODING_AGENT_DIR="+filepath.Join(dir, "agentdir"),
	)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("driver still running after 60s; output:\n%s", outBuf.String())
	}
	line := ""
	for _, l := range strings.Split(outBuf.String(), "\n") {
		if strings.HasPrefix(l, "PFEXT-RESULT ") {
			line = strings.TrimPrefix(l, "PFEXT-RESULT ")
		}
	}
	if line == "" {
		t.Fatalf("no PFEXT-RESULT line; driver output:\n%s", outBuf.String())
	}
	var res extRunOut
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		t.Fatalf("bad PFEXT-RESULT %q: %v", line, err)
	}
	return res
}

// extGroupFact walks /proc for the node driver's live descendants — the
// child pi (and, later, its grandchildren) — with their identities.
func extDescendants(t *testing.T, pid int) []procIdent {
	t.Helper()
	pids, err := procListAll()
	if err != nil {
		t.Fatal(err)
	}
	children := map[int64][]int{}
	idents := map[int]procIdent{}
	for _, p := range pids {
		state, ppid, _, start, ok := procStat(p)
		if !ok || state == "Z" {
			continue
		}
		children[ppid] = append(children[ppid], p)
		idents[p] = procIdent{pid: p, start: start}
	}
	var out []procIdent
	queue := []int{pid}
	seen := map[int]bool{pid: true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[int64(p)] {
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, idents[c])
			queue = append(queue, c)
		}
	}
	return out
}

func extPgidOf(t *testing.T, pid int) int {
	t.Helper()
	_, _, pgrp, _, ok := procStat(pid)
	if !ok {
		t.Fatalf("no stat for pid %d", pid)
	}
	return int(pgrp)
}

func extAlive(pid int) bool {
	state, _, _, _, ok := procStat(pid)
	return ok && state != "Z"
}

func extWaitGone(t *testing.T, pid int, bound time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for extAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %d) still alive after %s", what, pid, bound)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fakePiIgnoreTERM records its facts, announces one assistant message,
// records TERM and survives it (the AC1 "ignores TERM" shape). It exits
// once its parent (the node driver) is gone, so a hung scenario never
// leaks it past the test.
const fakePiIgnoreTERM = `#!/bin/bash
echo "started" >> "$FAKEPI_DIR/started"
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}'
trap 'echo TERM >> "$FAKEPI_DIR/signals"' TERM
while kill -0 "$PPID" 2>/dev/null; do sleep 0.1; done
`

// fakePiHonorTERM exits cleanly on TERM after announcing one message.
const fakePiHonorTERM = `#!/bin/bash
echo "started" >> "$FAKEPI_DIR/started"
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}'
trap 'echo TERM >> "$FAKEPI_DIR/signals"; exit 0' TERM
while kill -0 "$PPID" 2>/dev/null; do sleep 0.1; done
`

// extReadLines reads a fake-pi record file (absent = empty).
func extReadLines(t *testing.T, dir, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestPiSubagentLifecycleAbortGroup pins AC1: the child pi stays in the
// step's process group (never detached / setsid), an aborted call
// escalates TERM to KILL after a bounded grace and resolves only once the
// child is REALLY gone (the old proc.killed check never escalated: an
// ignore-TERM child hung the call forever), and a pre-aborted call never
// spawns.
func TestPiSubagentLifecycleAbortGroup(t *testing.T) {
	dir := extProject(t)
	workerParams := map[string]any{"agent": "worker", "task": "do work"}

	// (a) the ignore-TERM child: abort must TERM, escalate to KILL after
	// the injected grace, and resolve only after the child died.
	scenario := map[string]any{
		"params":       workerParams,
		"abortAfterMs": 300,
		"deadlineMs":   20000,
		"env":          map[string]any{"PF_SUBAGENT_TERM_GRACE_MS": "400", "PF_SUBAGENT_KILL_WAIT_MS": "3000", "FAKEPI_DIR": dir},
	}
	nodeBin, _ := exec.LookPath("node")
	res := extRun(t, dir, scenario, fakePiIgnoreTERM)
	if res.Hung {
		t.Fatalf("the aborted call never resolved: the TERM escalation is missing (the proc.killed bug)")
	}
	if res.Thrown == "" || !strings.Contains(strings.ToLower(res.Thrown), "abort") {
		t.Fatalf("an aborted call must surface as an error, got thrown=%q result=%v", res.Thrown, res.Result)
	}
	if res.ResolvedMs < 400 {
		t.Fatalf("resolved after %dms: the KILL must wait out the TERM grace (400ms injected), not resolve on send", res.ResolvedMs)
	}
	if sigs := extReadLines(t, dir, "signals"); len(sigs) == 0 || sigs[0] != "TERM" {
		t.Fatalf("the child must have received TERM first, got %v", sigs)
	}

	// (b) same process group: while a call runs, the child's pgid equals
	// the driver's — observed live from /proc, not from the tool's report.
	scenario = map[string]any{
		"params":     workerParams,
		"deadlineMs": 20000,
		"env":        map[string]any{"FAKEPI_DIR": dir},
	}
	scenarioPath := filepath.Join(dir, "scenario.json")
	blob, _ := json.Marshal(scenario)
	if err := os.WriteFile(scenarioPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pi"), []byte(fakePiIgnoreTERM), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nodeBin, "driver.mjs", scenarioPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"),
		"PI_CODING_AGENT_DIR="+filepath.Join(dir, "agentdir"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	driverPgid := extPgidOf(t, cmd.Process.Pid)
	var childPgid int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range extDescendants(t, cmd.Process.Pid) {
			if _, _, pgrp, _, ok := procStat(d.pid); ok {
				childPgid = int(pgrp)
			}
		}
		if childPgid != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Wait()
	if childPgid == 0 {
		t.Fatalf("no child pi process observed under the driver")
	}
	if childPgid != driverPgid {
		t.Fatalf("the child pi left the step's process group: driver pgid %d, child pgid %d (AC1: same group)", driverPgid, childPgid)
	}

	// (c) TERM honoured: the child dies on TERM, the call resolves fast
	// (before the grace) with the abort error.
	scenario = map[string]any{
		"params":       workerParams,
		"abortAfterMs": 300,
		"deadlineMs":   20000,
		"env":          map[string]any{"PF_SUBAGENT_TERM_GRACE_MS": "5000", "FAKEPI_DIR": dir},
	}
	res = extRun(t, dir, scenario, fakePiHonorTERM)
	if res.Hung || res.Thrown == "" || !strings.Contains(strings.ToLower(res.Thrown), "abort") {
		t.Fatalf("TERM-honoured abort must resolve promptly as an error: hung=%v thrown=%q", res.Hung, res.Thrown)
	}
	if res.ResolvedMs >= 5000 {
		t.Fatalf("a TERM-honoured child must not wait out the whole grace (resolved %dms)", res.ResolvedMs)
	}

	// (d) pre-aborted: the controller is already aborted when the tool
	// runs — no child may be spawned at all.
	scenario = map[string]any{
		"params":     workerParams,
		"preAborted": true,
		"deadlineMs": 20000,
		"env":        map[string]any{"FAKEPI_DIR": dir},
	}
	os.Remove(filepath.Join(dir, "started"))
	res = extRun(t, dir, scenario, fakePiHonorTERM)
	if res.Hung {
		t.Fatalf("a pre-aborted call hung")
	}
	if res.Thrown == "" || !strings.Contains(strings.ToLower(res.Thrown), "abort") {
		t.Fatalf("a pre-aborted call must surface the abort, got thrown=%q", res.Thrown)
	}
	if lines := extReadLines(t, dir, "started"); len(lines) != 0 {
		t.Fatalf("a pre-aborted call must not spawn a child; the fake pi started %d time(s)", len(lines))
	}
}
