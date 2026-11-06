// carries: internal/polyforge/cellcfg/*, internal/polyforge/cli/*, internal/polyforge/daemon/*
package test

// w350_defect_groups_acceptance_test.go: red-first end-to-end acceptance for
//, authored against artifacts/spec.md before code lands. Every
// scenario drives the project's real faces (the pf binary, real aihub
// processes, the Host's UDS face, real config files and processes) and uses
// an oracle outside the executor: file bytes, Hub-side project reads, real
// admission counters and CLI exit/output.
//
// The file compiles and vets against baseline 3d3c4ef9: it names no symbol
// the pre-implementation tree lacks.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w350RunSplit runs the real pf binary with stdout and stderr separated, so
// the whitelisted-verb notice can be asserted exactly.
func w350RunSplit(t *testing.T, e w348Env, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if e.home == "" {
		e.home = t.TempDir()
	}
	if e.xdg == "" {
		e.xdg = filepath.Join(e.home, ".config")
	}
	if err := os.MkdirAll(e.xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + e.home, "XDG_CONFIG_HOME=" + e.xdg}
	if e.sock != "" {
		env = append(env, "PF_DAEMON_SOCKET="+e.sock)
	}
	if e.dataDir != "" {
		env = append(env, "POLYFORGE_DATA_DIR="+e.dataDir)
	}
	env = append(env, e.extra...)
	cmd := exec.Command(filepath.Join(v1Bins(t), "pf"), args...)
	cmd.Env, cmd.Dir = env, dir
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
			errb.WriteString("\n[run error: " + err.Error() + "]")
		}
	}
	return out.String(), errb.String(), code
}

// w350LockHeld reports whether another process holds the data dir's Host lock
// (it takes and releases the same flock the Host and the migration use).
func w350LockHeld(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// w350Tasks reads the Cell's task list through the real CLI face.
func w350Tasks(t *testing.T, h *w348Host, profile string) []map[string]any {
	t.Helper()
	out, code := h.pf("", "--profile", profile, "task", "list", "--json")
	if code != 0 {
		t.Fatalf("pf --profile %s task list --json: exit %d (%q)", profile, code, out)
	}
	var doc struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &doc); err != nil {
		t.Fatalf("task list is not a {tasks:[...]} document: %v (%q)", err, out)
	}
	return doc.Tasks
}

// w350QueueRunning answers a Cell's real admission count from GET /status
// (the profile header forwards to that Cell), never from a config file.
func w350QueueStats(t *testing.T, h *w348Host, profile string) map[string]any {
	t.Helper()
	status, body := w348UDS(t, h.sock, "GET", "/status", map[string]string{"X-PF-Profile": profile}, "")
	if status != 200 {
		t.Fatalf("GET /status for %s: %d (%s)", profile, status, body)
	}
	q, ok := w348Field(w348JSON(t, "status", body), "queue")
	if !ok {
		t.Fatalf("GET /status for %s carries no queue block: %s", profile, body)
	}
	m, ok := q.(map[string]any)
	if !ok {
		t.Fatalf("queue is not an object: %v", q)
	}
	return m
}

func w350QueueRunning(t *testing.T, h *w348Host, profile string) int {
	t.Helper()
	n, ok := w350QueueStats(t, h, profile)["running"].(float64)
	if !ok {
		t.Fatalf("queue.running is not a number")
	}
	return int(n)
}

func w350QueuePending(t *testing.T, h *w348Host, profile string) int {
	t.Helper()
	n, ok := w350QueueStats(t, h, profile)["pending"].(float64)
	if !ok {
		t.Fatalf("queue.pending is not a number")
	}
	return int(n)
}

// w350WriteDefaultTaskPlan makes one-off tasks run long enough to stay
// running while the test observes admission and the idle gate.
func w350WriteDefaultTaskPlan(t *testing.T, plans string, ms int) {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{{"do": "envelope", "id": "task", "outcome": "completed", "ms": ms}})
	if err != nil {
		t.Fatal(err)
	}
	w348WriteFile(t, filepath.Join(plans, "default.json"), string(raw)+"\n")
}

// ---------------------------------------------------------------------------
// D1 — a completed migration never overwrites post-migration state; a
// completed layout re-applies as a byte-identical no-op.
// ---------------------------------------------------------------------------

func TestW350D1ReapplyKeepsPostMigrationWrites(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	marker := filepath.Join(h.dataDir, "cells", "default", "migration-accepted-work")
	event := filepath.Join(h.dataDir, "cells", "default", "events", "post-migration.json")
	w348WriteFile(t, marker, "accepted after the migration\n")
	w348WriteFile(t, event, `{"kind":"post-migration","seq":2}`+"\n")
	before := w348TreeHashes(t, filepath.Join(h.dataDir, "cells", "default"))

	out := w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	if got := w348ReadString(t, event); got != `{"kind":"post-migration","seq":2}`+"\n" {
		t.Fatalf("re-apply replaced the post-migration event: %q", got)
	}
	if !w348Exists(marker) {
		t.Fatalf("re-apply deleted the accepted-work marker")
	}
	w348SameTree(t, before, w348TreeHashes(t, filepath.Join(h.dataDir, "cells", "default")), "a re-apply after a post-migration write")
	if !strings.Contains(strings.ToLower(out), "already on the cell layout") {
		t.Fatalf("a completed layout must re-apply as a no-op saying it is already on the cell layout: %q", out)
	}
	// A second re-apply changes no byte either.
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	w348SameTree(t, before, w348TreeHashes(t, filepath.Join(h.dataDir, "cells", "default")), "the second no-op re-apply")
	fmt.Println("PASS S01")
}

func TestW350D1RollbackKeepsPostMigrationWrites(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	w348RunWant(t, 0, h.env(), "", "migrate", "cell-layout", "--apply")
	event := filepath.Join(h.dataDir, "cells", "default", "events", "post-migration.json")
	w348WriteFile(t, event, `{"kind":"post-migration"}`+"\n")
	before := w348TreeHashes(t, h.dataDir)

	out, code := h.pf("", "migrate", "cell-layout", "--rollback")
	after := w348TreeHashes(t, h.dataDir)
	if code == 0 {
		if !w348Exists(event) {
			t.Fatalf("rollback deleted the post-migration event: %q", out)
		}
		for name, sum := range after {
			if before[name] != sum {
				t.Fatalf("a seemingly successful rollback changed %s", name)
			}
		}
	} else {
		low := strings.ToLower(out)
		if !strings.Contains(low, "refus") || !strings.Contains(low, "events") {
			t.Fatalf("a refused rollback must name the state that blocks it (the events member): %q", out)
		}
		w348SameTree(t, before, after, "a refused rollback")
	}
	fmt.Println("PASS S02")
}

// TestW350D1MigrationHoldsHostLock probes the data-dir lock while a real
// migration runs: the lock must be held for the whole run, so a concurrent
// Host start cannot interleave (and write Cell state) with it.
func TestW350D1MigrationHoldsHostLock(t *testing.T) {
	h := w348NewHost(t)
	w348OldLayout(t, h.dataDir)
	// Enough members that the migration lasts well past the probe's settle
	// delay; the copy fsyncs every file.
	for i := 0; i < 3000; i++ {
		w348WriteFile(t, filepath.Join(h.dataDir, "events", fmt.Sprintf("bulk-%05d.json", i)), strings.Repeat("x", 2048)+"\n")
	}
	lockPath := filepath.Join(h.dataDir, "daemon.lock")
	cmd := exec.Command(filepath.Join(v1Bins(t), "pf"), "migrate", "cell-layout", "--apply")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + h.home, "XDG_CONFIG_HOME=" + h.xdg,
		"POLYFORGE_DATA_DIR=" + h.dataDir}
	cmd.Dir = h.dataDir
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(120 * time.Second)
	settle := time.Now().Add(40 * time.Millisecond)
	sawHeld := false
	for {
		if time.Now().After(settle) && w350LockHeld(lockPath) {
			sawHeld = true
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("migrate --apply failed: %v", err)
			}
			if !sawHeld {
				t.Fatalf("the migration never held the data-dir Host lock while it ran; a Host start can interleave with it")
			}
			if !w348Exists(filepath.Join(h.dataDir, "layout-version")) {
				t.Fatalf("the migration did not publish layout-version")
			}
			fmt.Println("PASS S03")
			return
		default:
		}
		if time.Now().After(deadline) {
			killGroup(cmd)
			<-done
			t.Fatalf("migrate --apply did not finish within %v", 120*time.Second)
		}
		time.Sleep(time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// D2 — routing reads the Host's effective index, never a file on disk, and
// fails closed when that index is unreadable.
// ---------------------------------------------------------------------------

func TestW350D2EffectiveIndexSurvivesRemovedFile(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	if _, err := alpha.client.CreateProject(aihubclient.ProjectCreateRequest{Name: "alpha-only"}); err != nil {
		t.Fatalf("create alpha-only project: %v", err)
	}
	wsAlpha := t.TempDir()
	h := w348NewHost(t)
	h.writeHost(def.url, def.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	// The Host already holds the effective index; the file disappears later.
	if err := os.Remove(h.cellConfigPath("alpha")); err != nil {
		t.Fatal(err)
	}
	out, code := h.pf(wsAlpha, "--json", "projects", "list")
	if code != 0 {
		t.Fatalf("business command from an effective cwd mapping failed: exit %d (%q)", code, out)
	}
	if !strings.Contains(out, "alpha-only") {
		t.Fatalf("the removed cli-alpha.toml demoted the command away from the Host's effective alpha mapping: %q", out)
	}
	if strings.Contains(out, `"name": "e2e"`) && !strings.Contains(out, "alpha-only") {
		t.Fatalf("the command fell back to the default Hub: %q", out)
	}
	fmt.Println("PASS S04")
}

func TestW350D2UnreadableIndexFailsClosed(t *testing.T) {
	dir := shortTmp(t, "w350uds")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "fake.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	other := 0
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-mu
		if r.URL.Path != "/profiles" {
			other++
		}
		mu <- struct{}{}
		if r.URL.Path == "/profiles" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"effective index unreadable"}`)
			return
		}
		fmt.Fprint(w, `{"items":[]}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })

	home := t.TempDir()
	env := w348Env{home: home, xdg: filepath.Join(home, ".config"), sock: sock}
	out, code := w348Run(t, env, home, "", "--json", "projects", "list")
	if code == 0 {
		t.Fatalf("a business command succeeded with an unreadable effective index: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "effective workspace index") {
		t.Fatalf("the refusal must name the unreadable effective index: %q", out)
	}
	<-mu
	got := other
	mu <- struct{}{}
	if got != 0 {
		t.Fatalf("%d business request(s) reached the fake daemon after the index read failed", got)
	}
	fmt.Println("PASS S05")
}

// ---------------------------------------------------------------------------
// D3 — a reloaded Cell ceiling governs real admission.
// ---------------------------------------------------------------------------

func TestW350D3ReloadedCellLimitGovernsAdmission(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	beta := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wsAlpha := t.TempDir()
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 8, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 4, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "beta", beta.url, beta.adminKey, 2, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	for i := 0; i < 7; i++ {
		wi := alpha.newWI(t, fmt.Sprintf("w350 cell limit %d", i), w348LongPlan(30000), nil)
		if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
			t.Fatalf("submit job %d: exit %d (%q)", i, code, out)
		}
	}
	waitUntil(t, 30*time.Second, "alpha never reached its configured limit 4", func() bool {
		return w350QueueRunning(t, h, "alpha") == 4
	})
	if out, code := h.pf("", "--profile", "alpha", "config", "set", "cell_max_concurrent_jobs", "7"); code != 0 {
		t.Fatalf("formal config writer: exit %d (%q)", code, out)
	}
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("config reload: exit %d (%q)", code, out)
	}
	waitUntil(t, 20*time.Second, "real admission never reached the reloaded Cell limit 7", func() bool {
		return w350QueueRunning(t, h, "alpha") == 7
	})
	profileAlpha := func() map[string]any {
		status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
		if status != 200 {
			t.Fatalf("GET /profiles: %d (%s)", status, body)
		}
		return w348ProfileRow(t, body, "alpha")
	}
	if got := w348Num(t, profileAlpha(), "max_concurrent"); got != 7 {
		t.Fatalf("the reloaded effective Cell limit = %v, want 7", got)
	}
	// Two more jobs queue above the limit; lowering it stops new starts but
	// kills none of the seven already running.
	for i := 7; i < 9; i++ {
		wi := alpha.newWI(t, fmt.Sprintf("w350 cell limit %d", i), w348LongPlan(30000), nil)
		if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
			t.Fatalf("submit queued job %d: exit %d (%q)", i, code, out)
		}
	}
	waitUntil(t, 10*time.Second, "the two extra jobs never queued", func() bool {
		return w350QueuePending(t, h, "alpha") == 2
	})
	if out, code := h.pf("", "--profile", "alpha", "config", "set", "cell_max_concurrent_jobs", "2"); code != 0 {
		t.Fatalf("formal config writer (lower): exit %d (%q)", code, out)
	}
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("config reload (lower): exit %d (%q)", code, out)
	}
	waitUntil(t, 10*time.Second, "the lowered Cell limit never became effective", func() bool {
		return w348Num(t, profileAlpha(), "max_concurrent") == 2
	})
	if got := w350QueueRunning(t, h, "alpha"); got < 7 {
		t.Fatalf("lowering the Cell limit killed running jobs: running %d, want the 7 already admitted", got)
	}
	if got := w350QueuePending(t, h, "alpha"); got != 2 {
		t.Fatalf("the lowered limit admitted queued work: pending %d, want 2", got)
	}
	// Removing the Cell limit falls back to the Host hard cap.
	if out, code := h.pf("", "--profile", "alpha", "config", "unset", "cell_max_concurrent_jobs"); code != 0 {
		t.Fatalf("formal config writer (unset): exit %d (%q)", code, out)
	}
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("config reload (unset): exit %d (%q)", code, out)
	}
	waitUntil(t, 10*time.Second, "the removed Cell limit never fell back to the Host cap 8", func() bool {
		return w348Num(t, profileAlpha(), "max_concurrent") == 8
	})
	// A second Cell's own limit and admitted count are unchanged by any of
	// alpha's raises, lowers or removals.
	statusB, bodyB := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if statusB != 200 {
		t.Fatalf("GET /profiles: %d (%s)", statusB, bodyB)
	}
	if got := w348Num(t, w348ProfileRow(t, bodyB, "beta"), "max_concurrent"); got != 2 {
		t.Fatalf("alpha's Cell limit changed beta's effective limit to %v, want beta's own 2", got)
	}
	if got := w350QueueRunning(t, h, "beta"); got != 0 {
		t.Fatalf("alpha's limit changes admitted work in beta: running %d", got)
	}
	fmt.Println("PASS S06")
}

// ---------------------------------------------------------------------------
// D4 — pending rotation refuses new tasks immediately and a running task is
// not idle.
// ---------------------------------------------------------------------------

func TestW350D4PendingRefusesNewTaskSpawn(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	hold := alpha.newWI(t, "w350 pending hold", w348LongPlan(30000), nil)
	wsAlpha := t.TempDir()
	w348WorkspaceYAML(t, wsAlpha, "e2e")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 4, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", hold); code != 0 {
		t.Fatalf("submit hold job: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", hold, "running", 30*time.Second)
	rotated := w348RotatedKey(t, alpha, "w350-pending-task")
	w348WriteCellWithModel(t, h, "alpha", alpha.url, rotated, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("rotation reload: exit %d (%q)", code, out)
	}
	w348WaitProfiles(t, h, "alpha pending", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		return row != nil && row["draining"] == true
	}, 10*time.Second)
	before := len(w350Tasks(t, h, "alpha"))
	out, code := h.pf("", "--profile", "alpha", "task", "spawn", "--project", "e2e", "w350 pending refusal")
	if code == 0 || !strings.Contains(out, "DAEMON-409") {
		t.Fatalf("a new task was admitted while the rotation was pending: exit %d (%q)", code, out)
	}
	if low := strings.ToLower(out); !strings.Contains(low, "pending connection rotation") && !strings.Contains(low, "draining for a connection change") {
		t.Fatalf("the refusal must name the pending connection rotation: %q", out)
	}
	if after := len(w350Tasks(t, h, "alpha")); after != before {
		t.Fatalf("the refused task created a record (%d -> %d)", before, after)
	}
	fmt.Println("PASS S07")
}

func TestW350D4RunningTaskBlocksPendingSwitch(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wsAlpha := t.TempDir()
	// The fake harness interprets a one-shot task call as the summarize
	// persona; a "slow" model keeps it running long enough to observe the
	// idle gate (the real harness's equivalent is a live task process).
	w348WriteFile(t, filepath.Join(wsAlpha, ".polyforge.yaml"),
		"steps:\n  e2e:\n    task:\n      models: [\"pi:slow\"]\n")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 4, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "task", "spawn", "--project", "e2e", "w350 idle task"); code != 0 {
		t.Fatalf("task spawn (setup): exit %d (%q)", code, out)
	}
	waitUntil(t, 20*time.Second, "the setup task never started", func() bool {
		for _, row := range w350Tasks(t, h, "alpha") {
			if st, _ := row["state"].(string); st == "running" {
				return true
			}
		}
		return false
	})
	rotated := w348RotatedKey(t, alpha, "w350-idle-task")
	w348WriteCellWithModel(t, h, "alpha", alpha.url, rotated, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	if out, code := h.pf("", "--profile", "alpha", "config", "reload"); code != 0 {
		t.Fatalf("rotation reload: exit %d (%q)", code, out)
	}
	w348WaitProfiles(t, h, "alpha pending", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		return row != nil && row["draining"] == true
	}, 10*time.Second)
	for {
		tasks := w350Tasks(t, h, "alpha")
		running := false
		for _, row := range tasks {
			if st, _ := row["state"].(string); st == "running" {
				running = true
			}
		}
		rows := w348WaitProfiles(t, h, "alpha registered", func(rows map[string]map[string]any) bool { return rows["alpha"] != nil }, 5*time.Second)
		if rows["alpha"]["draining"] != true {
			t.Fatalf("the pending rotation switched while a task was still running: %v", rows["alpha"])
		}
		if !running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	w348WaitProfiles(t, h, "alpha switched after the task ended", func(rows map[string]map[string]any) bool {
		row := rows["alpha"]
		return row != nil && row["draining"] != true && row["pending_change"].(string) == ""
	}, 30*time.Second)
	fmt.Println("PASS S08")
}

// ---------------------------------------------------------------------------
// D5 — an execution-disabled Cell starts nothing, from a spawn or a resume.
// ---------------------------------------------------------------------------

func TestW350D5DisabledCellRefusesTaskSpawn(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	wsAlpha := t.TempDir()
	w348WorkspaceYAML(t, wsAlpha, "e2e")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 4, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	// A workspace is mapped but no [model] source is declared: the Cell's
	// execution plane is disabled for that reason.
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, t.TempDir(), alpha.workflowsPath(), 30*time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles: %d (%s)", status, body)
	}
	if reason, _ := w348ProfileRow(t, body, "alpha")["execution_disabled"].(string); !strings.Contains(reason, "no model-credential source") {
		t.Fatalf("the fixture Cell is not disabled for a missing model source: %v", reason)
	}
	for i := 0; i < 2; i++ {
		out, code := h.pf("", "--profile", "alpha", "task", "spawn", "--project", "e2e", "w350 disabled task")
		if code == 0 {
			t.Fatalf("an execution-disabled Cell accepted a task spawn: %q", out)
		}
		if low := strings.ToLower(out); !strings.Contains(low, "no model-credential source") && !strings.Contains(low, "execution is disabled") {
			t.Fatalf("the refusal must name the execution-disabled reason: %q", out)
		}
	}
	if got := len(w350Tasks(t, h, "alpha")); got != 0 {
		t.Fatalf("the refused spawns leaked %d task record(s)", got)
	}
	if out, code := h.pf("", "--profile", "alpha", "--json", "projects", "list"); code != 0 || !strings.Contains(out, `"e2e"`) {
		t.Fatalf("the disabled Cell's data plane stopped answering: exit %d (%q)", code, out)
	}
	fmt.Println("PASS S09")
}

func TestW350D5DisabledCellRefusesResume(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wi := alpha.newWI(t, "w350 disabled resume", w348LongPlan(30000), nil)
	wsAlpha := t.TempDir()
	w348WorkspaceYAML(t, wsAlpha, "e2e")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 4, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "running", 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "pause", wi); code != 0 {
		t.Fatalf("pause: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "paused", 30*time.Second)
	h.stop()
	// Drop the model source from the Cell file; a Host restart applies it
	// (the existing reopen contract needs the restart, not a hot apply).
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles after restart: %d (%s)", status, body)
	}
	if reason, _ := w348ProfileRow(t, body, "alpha")["execution_disabled"].(string); !strings.Contains(reason, "no model-credential source") {
		t.Fatalf("the restarted Cell is not disabled for the missing model source: %v", reason)
	}
	out, code := h.pf("", "--profile", "alpha", "resume", wi)
	if code == 0 {
		t.Fatalf("resume started work in an execution-disabled Cell: %q", out)
	}
	if low := strings.ToLower(out); !strings.Contains(low, "no model-credential source") && !strings.Contains(low, "execution is disabled") {
		t.Fatalf("the resume refusal must name the execution-disabled reason: %q", out)
	}
	// The refused resume changed nothing: the Hub still holds clean_pause
	// and the paused status, the Cell still holds the job paused, and a
	// later Host start of the still-disabled Cell does not resume it.
	w350AssertStillPaused(t, h, alpha, wi, "after the refused resume")
	h.stop()
	h.start(30 * time.Second)
	w350AssertStillPaused(t, h, alpha, wi, "after a restart of the still-disabled Cell")
	fmt.Println("PASS S10")
}

// w350AssertStillPaused asserts the held work item was not resumed: the Hub
// keeps clean_pause and the paused status, and the Cell's job row stays
// paused for a settle window (no run started).
func w350AssertStillPaused(t *testing.T, h *w348Host, inst *v1Install, wi, when string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if sess := inst.sessionOf(t, wi); !sess.CleanPause {
			t.Fatalf("%s: the Hub's clean_pause was cleared: %+v", when, sess)
		}
		if st := inst.wiStatus(t, wi); st != "paused" {
			t.Fatalf("%s: the Hub work item is %q, want paused", when, st)
		}
		found := false
		for _, j := range w348Jobs(w348StatusDoc(t, h)) {
			if p, _ := j["profile"].(string); p != "alpha" {
				continue
			}
			if id, _ := j["work_item_id"].(string); id != wi {
				continue
			}
			found = true
			if st, _ := j["status"].(string); st != "paused" {
				t.Fatalf("%s: the Cell's job is %q, want paused (a run started)", when, st)
			}
		}
		if !found {
			t.Fatalf("%s: the Cell no longer holds the paused job %s", when, wi)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestW350D5DisabledCellRefusesAnswer(t *testing.T) {
	def := newV1Install(t)
	alpha := newV1Install(t)
	plans := t.TempDir()
	def.planDir, alpha.planDir = plans, plans
	wi := alpha.newWI(t, "w350 disabled answer", w348ParkedPlan("go on in a disabled Cell?"), nil)
	wsAlpha := t.TempDir()
	w348WorkspaceYAML(t, wsAlpha, "e2e")
	h := w348NewHost(t)
	w348WriteHostWithModel(t, h, def.url, def.adminKey, 4, []w348WS{{path: t.TempDir(), projects: []string{"e2e"}}})
	w348WriteCellWithModel(t, h, "alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	w348LaunchWithPlans(t, h, plans, alpha.workflowsPath(), 30*time.Second)
	if out, code := h.pf("", "--profile", "alpha", "submit", wi); code != 0 {
		t.Fatalf("submit: exit %d (%q)", code, out)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "needs_input", 30*time.Second)
	h.stop()
	// Drop the model source; the restart applies it (as in S10).
	h.writeCell("alpha", alpha.url, alpha.adminKey, 0, []w348WS{{path: wsAlpha, projects: []string{"e2e"}}})
	h.start(30 * time.Second)
	status, body := w348UDS(t, h.sock, "GET", "/profiles", nil, "")
	if status != 200 {
		t.Fatalf("GET /profiles after restart: %d (%s)", status, body)
	}
	if reason, _ := w348ProfileRow(t, body, "alpha")["execution_disabled"].(string); !strings.Contains(reason, "no model-credential source") {
		t.Fatalf("the restarted Cell is not disabled for the missing model source: %v", reason)
	}
	w348WaitJobStatus(t, h, "alpha", wi, "needs_input", 30*time.Second)
	out, code := h.pf("", "--profile", "alpha", "answer", wi, "--text", "yes")
	if code == 0 {
		t.Fatalf("an answer continued work in an execution-disabled Cell: %q", out)
	}
	if !strings.Contains(out, "DAEMON-409") || !strings.Contains(strings.ToLower(out), "no model-credential source") {
		t.Fatalf("the answer refusal must be DAEMON-409 naming the execution-disabled reason: %q", out)
	}
	// Nothing started: the ask stays open on the Cell's job.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j := w348WaitJobStatus(t, h, "alpha", wi, "needs_input", 5*time.Second)
		if st, _ := j["status"].(string); st != "needs_input" {
			t.Fatalf("the refused answer moved the job to %q", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("PASS S14")
}

// ---------------------------------------------------------------------------
// D6 — local repair verbs under an inherited connection environment.
// ---------------------------------------------------------------------------

func TestW350D6ConfigListSurvivesInheritedEnv(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	w348WriteFile(t, filepath.Join(xdg, "polyforge", "cli.toml"),
		"server = \"http://cell-hub.invalid\"\napi_key = \"cell-file-key\"\nuser_id = \"usr_root\"\n[workspace.\"/tmp/w350\"]\nprojects = [\"e2e\"]\n")
	env := w348Env{home: home, xdg: xdg, extra: []string{
		"PF_SERVER=http://inherited.invalid", "PF_API_KEY=inherited-secret-value",
	}}
	stdout, stderr, code := w350RunSplit(t, env, home, "config", "list")
	if code != 0 {
		t.Fatalf("pf config list under an inherited PF_SERVER: exit %d (stdout %q, stderr %q)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "cell-hub.invalid") {
		t.Fatalf("config list did not show the Cell file's server: %q", stdout)
	}
	if strings.Contains(stdout+stderr, "inherited.invalid") || strings.Contains(stdout+stderr, "inherited-secret-value") {
		t.Fatalf("config list printed the inherited value: stdout %q stderr %q", stdout, stderr)
	}
	if got := strings.Count(stderr, "PF_SERVER"); got != 1 {
		t.Fatalf("want exactly one stderr notice naming PF_SERVER, got %d (stderr %q)", got, stderr)
	}
	if !strings.Contains(stderr, "PF_API_KEY") {
		t.Fatalf("the one stderr notice must name the inherited variable(s), got %q", stderr)
	}
	fmt.Println("PASS S11")
}

func TestW350D6WhitelistAndExplicitFlagBoundary(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	dataDir := t.TempDir()
	w348WriteFile(t, filepath.Join(xdg, "polyforge", "cli.toml"),
		"server = \"http://cell-hub.invalid\"\napi_key = \"cell-file-key\"\nuser_id = \"usr_root\"\n")
	env := w348Env{home: home, xdg: xdg, dataDir: dataDir, extra: []string{
		"PF_SERVER=http://inherited.invalid", "PF_API_KEY=inherited-secret-value",
	}}
	initWS := t.TempDir()
	for _, args := range [][]string{
		{"profile"},
		{"version"},
		{"init", initWS},
		{"migrate", "cell-layout"},
	} {
		stdout, stderr, code := w350RunSplit(t, env, home, args...)
		if code != 0 {
			t.Fatalf("pf %v must run to its normal result under an inherited PF_SERVER: exit %d (stdout %q, stderr %q)", args, code, stdout, stderr)
		}
		if strings.Contains(strings.ToLower(stdout+stderr), "refused") && strings.Contains(stdout+stderr, "PF_SERVER") {
			t.Fatalf("pf %v still refuses the inherited variable as an override: %q / %q", args, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "inherited.invalid") || strings.Contains(stdout+stderr, "inherited-secret-value") {
			t.Fatalf("pf %v printed the inherited value: %q / %q", args, stdout, stderr)
		}
	}
	// Explicit flags stay refused on the whitelisted verbs (version included).
	for _, args := range [][]string{
		{"config", "list", "--server", "http://elsewhere.invalid"},
		{"version", "--server", "http://elsewhere.invalid"},
	} {
		stdout, stderr, code := w350RunSplit(t, env, home, args...)
		if code == 0 {
			t.Fatalf("pf %v accepted an explicit --server on a whitelisted verb: %q / %q", args, stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "--server") {
			t.Fatalf("the refusal of pf %v must name --server: %q / %q", args, stdout, stderr)
		}
	}
	// A non-whitelisted verb keeps refusing the inherited environment.
	stdout, stderr, code := w350RunSplit(t, env, home, "cell", "list")
	if code == 0 || !strings.Contains(stdout+stderr, "PF_SERVER") {
		t.Fatalf("pf cell still must refuse the inherited PF_SERVER: exit %d (%q / %q)", code, stdout, stderr)
	}
	fmt.Println("PASS S12")
}

func TestW350D6DoctorNeverDialsInheritedEnv(t *testing.T) {
	startSentinel := func() (string, *atomic.Int32, func()) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var hits atomic.Int32
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"sentinel"}`)
		})}
		go srv.Serve(ln)
		return "http://" + ln.Addr().String(), &hits, func() { _ = srv.Close() }
	}
	cellURL, _, stopCell := startSentinel()
	inheritedURL, inheritedHits, stopInherited := startSentinel()
	defer stopCell()
	defer stopInherited()
	home, xdg := t.TempDir(), t.TempDir()
	w348WriteFile(t, filepath.Join(xdg, "polyforge", "cli.toml"),
		fmt.Sprintf("server = %q\napi_key = \"cell-file-key\"\nuser_id = \"usr_root\"\n", cellURL))
	env := w348Env{home: home, xdg: xdg, dataDir: t.TempDir(), extra: []string{
		"PF_SERVER=" + inheritedURL, "PF_API_KEY=inherited-secret-value",
	}}
	stdout, stderr, code := w350RunSplit(t, env, home, "doctor")
	low := strings.ToLower(stdout + stderr)
	if strings.Contains(low, "pf_server") && strings.Contains(low, "refused") {
		t.Fatalf("pf doctor still refuses the inherited environment (exit %d): %q / %q", code, stdout, stderr)
	}
	if n := inheritedHits.Load(); n != 0 {
		t.Fatalf("pf doctor dialed the inherited server %d time(s) instead of the Cell-configured Hub", n)
	}
	if strings.Contains(stdout+stderr, inheritedURL) || strings.Contains(stdout+stderr, "inherited-secret-value") {
		t.Fatalf("pf doctor printed the inherited value: %q / %q", stdout, stderr)
	}
	if got := strings.Count(stderr, "PF_SERVER"); got != 1 {
		t.Fatalf("want exactly one stderr notice naming PF_SERVER, got %d (%q)", got, stderr)
	}
	fmt.Println("PASS S13")
}
