package test

// v1_harness_test.go: the v1 end-to-end dogfood harness. Mirrors the v0
// usage_* discipline: build the REAL binaries (pf, fakepi) once,
// exec them as real OS processes, and assert against real wire/git/
// process state only — aihub as a `pf aihub` process on a free loopback port, the
// daemon as a `pf daemon` process on its own UDS, the pf CLI as the real
// binary, fakepi as the WI-agent stand-in the daemon derives via
// --pi-bin, and the capsule git chain verified with real git against
// the aihub's on-disk bare repos.
//
// Hermetic: every test gets its own temp install (aihub data dir, daemon
// data dir + workspace, socket, plan dir), a free loopback port, and
// cleanups that kill every process it started — even on failure. No
// default ports, no shared state, no network beyond loopback.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/testenv"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// ---------------------------------------------------------------------------
// Binary build cache: one build per test-binary run, shared by every
// v1_s* scenario.

var (
	v1BinOnce sync.Once
	v1BinDir  string
	v1BinErr  error
)

// v1Root is the module root (the tests run with cwd = test/).
func v1Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// v1Bins builds pf (which also serves `pf aihub`) and fakepi into a
// shared temp dir. When PFV1_COVER_BINS is set, the binaries are built
// INSTRUMENTED (-cover -coverpkg=./internal/...) and GOCOVERDIR is
// pointed at <dir>/covdata so every spawned pf aihub/daemon/CLI/agent
// process emits its coverage there — the manual instrumented round for
// the e2e coverage report (merge with `go tool covdata textfmt`).
//
// PFV1_PREBUILT_BINS=<dir> (test/e2e-parallel.sh) skips the build: the
// two binaries are hard-linked (copied across filesystems) into this
// run's own dir, so the bin sweep's exe-path match still sees only the
// processes this test binary started.
func v1Bins(t *testing.T) string {
	t.Helper()
	v1BinOnce.Do(func() {
		// the instrumented round keeps its coverage data in the bin dir:
		// outside the run's private TMPDIR, which must end empty
		parent := ""
		if os.Getenv("PFV1_COVER_BINS") != "" {
			parent = testenv.OrigTempDir()
		}
		dir, err := os.MkdirTemp(parent, "pfv1-bins")
		if err != nil {
			v1BinErr = err
			return
		}
		if pre := os.Getenv("PFV1_PREBUILT_BINS"); pre != "" && os.Getenv("PFV1_COVER_BINS") == "" {
			for _, name := range []string{"pf", "fakepi"} {
				if v1BinErr = linkOrCopy(filepath.Join(pre, name), filepath.Join(dir, name)); v1BinErr != nil {
					return
				}
			}
			v1BinDir = dir
			return
		}
		build := exec.Command("go", "build", "-o", dir+"/",
			"./cmd/pf", "./test/fakepi")
		if os.Getenv("PFV1_COVER_BINS") != "" {
			build = exec.Command("go", "build", "-cover", "-coverpkg=./...",
				"-o", dir+"/", "./cmd/pf", "./test/fakepi")
			covDir := filepath.Join(dir, "covdata")
			if err := os.MkdirAll(covDir, 0o755); err != nil {
				v1BinErr = err
				return
			}
			os.Setenv("GOCOVERDIR", covDir) // every spawned process inherits it
		}
		build.Dir = v1Root(t)
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			v1BinErr = fmt.Errorf("build binaries: %v: %s", err, out)
			return
		}
		v1BinDir = dir
	})
	if v1BinErr != nil {
		t.Fatalf("binary build cache: %v", v1BinErr)
	}
	registerBinSweep(t, v1BinDir)
	return v1BinDir
}

// linkOrCopy hard-links src to dst, copying (mode kept) when a link is
// impossible.
func linkOrCopy(src, dst string) error {
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------------------
// One real aihub install + the pf CLI face.

const v1Project = "e2e"

// v1Install is a scratch install of the real system.
type v1Install struct {
	t        *testing.T
	binDir   string
	root     string
	dataDir  string // aihub data dir (bare repos under dataDir/git)
	url      string
	port     int
	adminKey string
	aihubLog string
	aihubCmd *exec.Cmd
	// aihubExited closes when aihubCmd's process has exited (its one
	// waiter goroutine; see startAihubProc).
	aihubExited <-chan struct{}
	client      *aihubclient.Client
	planDir     string
	pfPath      string
	fakepiPath  string

	// microMaxRewinds, when set, replaces micro's max_rewinds in the
	// daemons' workflows copy: the gate-fail ladder scenario (TestV1S07)
	// runs a multi-rewind ladder micro's shipped budget (2) cannot reach.
	microMaxRewinds int
	// keepDeliver keeps the delivery bundles' `deliver` step in the
	// daemons' workflows copy (stripped by default: see workflowsPath).
	keepDeliver bool
	strippedWF  string
	// withoutLane makes the next workflows copy the shipped tree with this
	// one lane left out (the scenario: a daemon whose release
	// no longer installs a work item's bundle). Reset strippedWF when
	// setting it mid-scenario.
	withoutLane string
	// stripWallBudgets removes every step-level `budget: { wall_minutes }`
	// override from the daemon's workflow copy, so the daemon-wide
	// --step-timeout flag (T) governs the scenario (a step's own
	// wall_minutes is minutes-granular and would otherwise win).
	stripWallBudgets bool

	// wis holds every work item this scenario created (through newWI or
	// trackWI): the post-teardown orphan sweep (sweepOrphans) kills and
	// then asserts gone every live process carrying one of these WIs'
	// PF_WI_ID env marker at the scenario's final cleanup.
	wis []string

	// daemons are the scenario's started daemon processes (every
	// v1Daemon.start registers): the terminal-settle wait asks each one
	// whether a finished work item's close has fully drained.
	dmu     sync.Mutex
	daemons []*v1Daemon
}

// newV1Install boots a real aihub on a free loopback port, harvests the
// first-boot bootstrap admin key, and creates the e2e project.
func newV1Install(t *testing.T) *v1Install {
	return newV1InstallRepos(t, nil)
}

// taskRemote is one local fake task-repo remote: a real git repo (one
// commit on main) the daemon can clone over file:// — no network.
type taskRemote struct {
	dir  string
	url  string
	head string
}

func newTaskRemote(t *testing.T) taskRemote {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	head := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	return taskRemote{dir: dir, url: "file://" + dir, head: head}
}

// newV1InstallTaskRepo boots the standard install with the e2e project
// configured with one local fake task-repo remote, so a scenario can
// exercise the claim→clone lane (repos land in the capsule before the
// agent spawns).
func newV1InstallTaskRepo(t *testing.T) (*v1Install, taskRemote) {
	t.Helper()
	remote := newTaskRemote(t)
	inst := newV1InstallRepos(t, []api.ProjectRepo{{Name: "demo-src", URL: remote.url}})
	return inst, remote
}

// newV1InstallRepos is newV1Install with an explicit task-repo inventory
// on the e2e project (nil = none).
func newV1InstallRepos(t *testing.T, repos []api.ProjectRepo) *v1Install {
	t.Helper()
	bins := v1Bins(t)
	inst := &v1Install{
		t:          t,
		binDir:     bins,
		root:       v1Root(t),
		dataDir:    t.TempDir(),
		aihubLog:   filepath.Join(t.TempDir(), "aihub.log"),
		planDir:    t.TempDir(),
		pfPath:     filepath.Join(bins, "pf"),
		fakepiPath: filepath.Join(bins, "fakepi"),
	}
	// The post-teardown orphan sweep is registered FIRST of the
	// scenario's cleanups so Go's LIFO cleanup order runs it LAST —
	// after every daemon teardown registered later (newV1Daemon's own
	// d.stop, struct-literal daemons' stop, the SIGKILL fallback inside
	// stop, the inproc rig's terminate) and thus over the final process
	// state, whatever teardown path was taken.
	t.Cleanup(func() { inst.sweepOrphans() })

	logf, err := os.Create(inst.aihubLog)
	if err != nil {
		t.Fatal(err)
	}
	// The port is probed free and then bound by the aihub, so another test
	// process can take it in between (e2e-parallel.sh): a lost port is
	// retried on a fresh port with a fresh data dir.
	for attempt := 1; ; attempt++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		inst.port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		inst.url = "http://127.0.0.1:" + strconv.Itoa(inst.port)
		cmd := exec.Command(filepath.Join(bins, "pf"), "aihub",
			"--data-dir", inst.dataDir, "--listen", "127.0.0.1:"+strconv.Itoa(inst.port))
		cmd.Stdout, cmd.Stderr = logf, logf
		inst.startAihubProc(t, cmd, logf)
		if inst.waitHTTP(t) {
			break
		}
		if attempt == 5 {
			t.Fatalf("aihub lost its port %d times in a row:\n%s", attempt, v1ReadString(inst.aihubLog))
		}
		if err := os.RemoveAll(inst.dataDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(inst.dataDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	inst.adminKey = v1HarvestBootstrapKey(t, inst.dataDir, inst.aihubLog)
	inst.client = aihubclient.New(inst.url, inst.adminKey)
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: v1Project, Description: "v1 e2e scratch project", Repos: repos,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return inst
}

// startAihubProc starts one `pf aihub` process in its own process group and
// registers its teardown (also on every t.Fatal path after this point):
// SIGTERM first — the server's own graceful path, which also flushes any
// instrumented-coverage data at exit — then a group SIGKILL fallback.
// The exit is observed once, by one waiter goroutine, shared with
// stopAihub.
func (u *v1Install) startAihubProc(t *testing.T, cmd *exec.Cmd, logf *os.File) {
	t.Helper()
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	u.aihubCmd, u.aihubExited = cmd, exited
	t.Cleanup(func() {
		stopGroup(cmd, exited, 5*time.Second)
		logf.Close()
	})
}

// waitHTTP waits until THIS install's aihub process listens on u.port —
// not merely until something answers there: another test process's
// aihub may hold a port this one probed free. It reports false when the
// process exited first (the port was taken).
func (u *v1Install) waitHTTP(t *testing.T) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if v1ListensOn(u.aihubCmd.Process.Pid, u.port) {
			return true
		}
		select {
		case <-u.aihubExited:
			return false
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("aihub never came up on %d:\n%s", u.port, v1ReadString(u.aihubLog))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// v1ListensOn reports whether process pid holds the LISTEN socket on
// 127.0.0.1:port: /proc/<pid>/net/tcp names the socket's inode and one
// of the process's fds must be that socket.
func v1ListensOn(pid, port int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", pid))
	if err != nil {
		return false
	}
	local, inode := fmt.Sprintf("0100007F:%04X", port), ""
	for _, line := range strings.Split(string(raw), "\n") {
		// sl local_address rem_address st ... inode (field 9); st 0A = LISTEN
		if f := strings.Fields(line); len(f) > 9 && f[1] == local && f[3] == "0A" {
			inode = f[9]
			break
		}
	}
	if inode == "" {
		return false
	}
	ents, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return false
	}
	for _, e := range ents {
		if tgt, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, e.Name())); tgt == "socket:["+inode+"]" {
			return true
		}
	}
	return false
}

// v1HarvestBootstrapKey reads the bootstrap admin key the aihub writes
// to <data-dir>/bootstrap-admin.key (0600) on the first boot of an empty
// deployment, and pins that the key never reaches the log (repo review
// 2026-10-16 V-10).
func v1HarvestBootstrapKey(t *testing.T, dataDir, logPath string) string {
	t.Helper()
	keyPath := filepath.Join(dataDir, "bootstrap-admin.key")
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(keyPath)
		if key := strings.TrimSpace(string(raw)); err == nil && key != "" {
			if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
				t.Fatalf("bootstrap key file mode = %v (%v), want 0600", fi.Mode().Perm(), err)
			}
			// the log line naming the file is written right after it
			for !strings.Contains(v1ReadString(logPath), "bootstrap-admin.key") && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if log := v1ReadString(logPath); strings.Contains(log, key) {
				t.Fatalf("the bootstrap admin key leaked into the aihub log:\n%s", log)
			} else if !strings.Contains(log, keyPath) {
				t.Fatalf("the aihub log does not name the bootstrap key file %s:\n%s", keyPath, log)
			}
			return key
		}
		if time.Now().After(deadline) {
			t.Fatalf("no bootstrap admin key at %s; aihub log:\n%s", keyPath, v1ReadString(logPath))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// v1ReadString answers a file's content ("" when unreadable).
func v1ReadString(p string) string {
	raw, _ := os.ReadFile(p)
	return string(raw)
}

// stopAihub terminates the REAL `pf aihub` process: SIGTERM (the server's
// own graceful shutdown path) with a hard-kill fallback. Idempotent.
// The data dir survives, so the bare repos and the whole hub state are
// exactly what the restart re-opens.
func (u *v1Install) stopAihub() {
	if u.aihubCmd == nil || u.aihubCmd.Process == nil {
		return
	}
	stopGroup(u.aihubCmd, u.aihubExited, 15*time.Second)
	u.aihubCmd, u.aihubExited = nil, nil
}

// restartAihub re-execs `pf aihub` on the SAME data dir and port (the
// outage-scenario recovery lever) and waits for its HTTP face. The data
// dir persists across stopAihub, so the bare repos the agents push to
// keep their history and their pre-receive seq bookkeeping.
func (u *v1Install) restartAihub() {
	u.t.Helper()
	if u.aihubCmd != nil && u.aihubCmd.Process != nil {
		u.t.Fatal("restartAihub: aihub still running; stopAihub first")
	}
	logf, err := os.OpenFile(u.aihubLog, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		u.t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(u.binDir, "pf"), "aihub",
		"--data-dir", u.dataDir, "--listen", "127.0.0.1:"+strconv.Itoa(u.port))
	cmd.Stdout, cmd.Stderr = logf, logf
	u.startAihubProc(u.t, cmd, logf)
	if !u.waitHTTP(u.t) {
		u.t.Fatalf("the restarted aihub could not bind its port %d again (another process took it during the outage):\n%s", u.port, v1ReadString(u.aihubLog))
	}
}

// hermeticConfigPath writes an EMPTY scratch cli.toml and returns its
// path: the harness hands it to every pf CLI invocation via --config so
// no operator config file (~/.config/polyforge/cli.toml — this machine
// is a live polyforge install) leaks into a run. The config surface is
// contract under test, never ambient host state. HOME and the global git
// config are the package TestMain's scratch ones (internal/testenv): no
// operator git setting reaches a run — pushes go to the local aihub or
// file:// remotes and gh is always a stub.
func hermeticConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hermetic-cli.toml")
	if err := os.WriteFile(path, []byte("# hermetic harness config: deliberately empty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mappingConfigPath writes the daemon's cli.toml (#113): one
// [workspace."<path>"] section mapping workspace → projects. This file
// is the daemon's ONLY workspace source; the connection settings ride
// their flags. ws/paths are used literally (post-~expansion shape).
func mappingConfigPath(t *testing.T, ws string, projects []string) string {
	t.Helper()
	quoted := make([]string, 0, len(projects))
	for _, p := range projects {
		quoted = append(quoted, strconv.Quote(p))
	}
	body := "# harness mapping config\n\n[workspace.\"" + ws + "\"]\nprojects = [" + strings.Join(quoted, ", ") + "]\n"
	path := filepath.Join(t.TempDir(), "mapping-cli.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pf runs the REAL pf CLI against this install (env overrides carry the
// configuration; no config file is involved). wantExit < 0 means the
// command must succeed.
func (u *v1Install) pf(t *testing.T, args ...string) string {
	t.Helper()
	return u.pfExit(t, -1, "", args...)
}

func (u *v1Install) pfExit(t *testing.T, wantExit int, sock string, args ...string) string {
	t.Helper()
	cmd := exec.Command(u.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+u.url,
		"PF_API_KEY="+u.adminKey,
		"PF_USER_ID=usr_root",
	)
	if sock != "" {
		cmd.Env = append(cmd.Env, "PF_DAEMON_SOCKET="+sock)
	}
	v1BusinessCmd(t, cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	if wantExit < 0 && code != 0 {
		t.Fatalf("pf %v: exit %d (stdout %q stderr %q)", args, code, stdout.String(), stderr.String())
	}
	if wantExit >= 0 && code != wantExit {
		t.Fatalf("pf %v: exit %d, want %d (stdout %q stderr %q)", args, code, wantExit, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// vehicleLane is the shipped lane the suite's lane-independent scenarios
// run on: micro, whose deliver step workflowsPath strips by
// default — `fix` (the first agent step) then the `review` gate, whose FAIL
// rewinds to fix. Scenarios about delivery or the develop lane name their
// lane themselves.
const vehicleLane = "micro"

// v1AcceptanceContent is the content the harness's creators give an
// execution ticket: an acceptance section with one criterion, so the
// daemon's auto-pull claims it (it skips a ticket listing none)
// and `pf queue` shows it without the needs-interaction
// mark.
const v1AcceptanceContent = "## Acceptance\n\n- AC1 the scenario's ticket runs — carried by build\n"

// newWI files one execution ticket (bundle vehicleLane, no human session
// required) and scripts its agent with the given plan actions.
// resumeActions, when non-nil, script the crash-continuation derivation
// (the recovery engine's resume spawn).
func (u *v1Install) newWI(t *testing.T, goal string, actions, resumeActions []map[string]any) string {
	t.Helper()
	raw, err := u.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    v1Project,
		Goal:       goal,
		Content:    strPtr(v1AcceptanceContent),
		BundleName: strPtr(vehicleLane),
		Attrs:      nil,
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	u.setPlanFull(t, wi.ID, actions, resumeActions)
	u.trackWI(wi.ID)
	return wi.ID
}

// setPlan (re)writes the plan file the fake agent reads at process start.
func (u *v1Install) setPlan(t *testing.T, wi string, actions []map[string]any) {
	t.Helper()
	u.setPlanFull(t, wi, actions, nil)
}

func (u *v1Install) setPlanFull(t *testing.T, wi string, actions, resumeActions []map[string]any) {
	t.Helper()
	if actions == nil {
		actions = []map[string]any{}
	}
	body := map[string]any{"actions": actions}
	if resumeActions != nil {
		body["resume_actions"] = resumeActions
	}
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.planDir, wi+".json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// wiStatus reads the aihub-side work item status.
func (u *v1Install) wiStatus(t *testing.T, wi string) string {
	t.Helper()
	item, err := u.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("get work item %s: %v", wi, err)
	}
	return item.Status
}

// wiSession is the v1.5 m4 PUBLIC session audit block of one work item
// (read through the raw wire: Phase 0's api.WorkItem struct is frozen
// and carries none of these fields — the block rides alongside it at
// the server boundary as core.WIView). NEVER a hash, never the secret.
type wiSession struct {
	Fingerprint string `json:"session_fingerprint"`
	Machine     string `json:"session_machine"`
	StartedAt   string `json:"session_started_at"`
	CleanPause  bool   `json:"clean_pause"`
}

// sessionOf reads the work item's public session block.
func (u *v1Install) sessionOf(t *testing.T, wi string) wiSession {
	t.Helper()
	raw, err := u.client.GetWorkItemRaw(wi)
	if err != nil {
		t.Fatalf("get work item (raw) %s: %v", wi, err)
	}
	var view struct {
		wiSession
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("work item view undecodable: %v (%s)", err, raw)
	}
	return view.wiSession
}

// sessionClaimReq builds a well-formed claim request for wire-level
// gate assertions (a fresh random 64-hex secret; the
// caller only ever inspects the typed refusal, never a success).
func sessionClaimReq(wi string) api.SessionClaimRequest {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return api.SessionClaimRequest{
		WorkItemID: wi,
		SessionID:  hex.EncodeToString(b),
		MachineID:  "m-e2e-probe",
	}
}

// apiReason extracts the typed refusal REASON (the server's Fail(code,
// reason, ...) tag — e.g. ALREADY_HELD / NOT_CLAIMABLE) out of a
// client-side *api.Error; the wire Error struct carries it in Extra.
func apiReason(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("apiReason: nil error")
	}
	ae, ok := err.(*api.Error)
	if !ok {
		t.Fatalf("error is not a typed *api.Error: %v", err)
	}
	var r struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ae.Extra, &r)
	return r.Reason
}

// refuseClaim asserts the claim endpoint's typed refusal of wi: the
// HTTP code family and the reason tag (the M1 gate's vocabulary).
func refuseClaim(t *testing.T, inst *v1Install, wi, wantReason string) {
	t.Helper()
	_, err := inst.client.SessionClaim(sessionClaimReq(wi))
	if err == nil {
		t.Fatalf("claim of %s was accepted, want the typed %s refusal", wi, wantReason)
	}
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrCoreConflict {
		t.Fatalf("claim refusal of %s wrong: %v (want %s)", wi, err, api.ErrCoreConflict)
	}
	if got := apiReason(t, err); got != wantReason {
		t.Fatalf("claim refusal of %s reason %q, want %q (%v)", wi, got, wantReason, err)
	}
}

func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// The daemon as a real process.

// v1Daemon is one `pf daemon` OS process.
type v1Daemon struct {
	t          *testing.T
	inst       *v1Install
	cmd        *exec.Cmd
	launchedAt time.Time // just before cmd.Start: this process's events come after it
	logPath    string
	sock       string
	dataDir    string
	workspace  string
	machineID  string
	apiKey     string // optional dedicated aihub key (optAPIKey); "" = bootstrap admin key
	extraEnv   []string
	extraArgs  []string // extra `pf daemon` flags appended to the argv
	daemonKeys []string // `key = value` lines of the config's [daemon] table (optDaemonKeys)
	client     *cli.DaemonClient
	waitedExit chan error
	serverURL  string // --server override (optServerURL)
}

type daemonOpt func(*v1Daemon)

func optMachineID(id string) daemonOpt {
	return func(d *v1Daemon) { d.machineID = id }
}

// optAPIKey boots the daemon on a dedicated aihub api key (e.g. a
// machine user's key the scenario revokes mid-run) instead of the
// install's bootstrap admin key.
// optEnv adds environment entries to the daemon process (and so to every
// step process it spawns), e.g. a PATH with a fake gh first.
func optEnv(kv ...string) daemonOpt {
	return func(d *v1Daemon) { d.extraEnv = append(d.extraEnv, kv...) }
}

func optAPIKey(key string) daemonOpt {
	return func(d *v1Daemon) { d.apiKey = key }
}

// optDaemonKeys writes `key = value` lines into the daemon config's
// [daemon] table (e.g. `command_path = "..."`).
func optDaemonKeys(lines ...string) daemonOpt {
	return func(d *v1Daemon) { d.daemonKeys = append(d.daemonKeys, lines...) }
}

// supBounds are the supervision bounds a scenario injects through the
// real `pf daemon` flags (v2-converge 2.4). A zero duration omits its
// flag, leaving that bound at the production default.
type supBounds struct {
	silence, tool, stepTimeout, stall, modelErrorGrace time.Duration
}

// optSupervision injects the supervision bounds through the daemon's own
// flags — the same real path an operator uses, so the whole production
// chain (flag → config → driver thresholds → supervisor rules → driver
// verdict handling) is the thing under test.
func optSupervision(b supBounds) daemonOpt {
	return func(d *v1Daemon) {
		for _, f := range []struct {
			name string
			val  time.Duration
		}{
			{"--silence-window", b.silence},
			{"--tool-window", b.tool},
			{"--step-timeout", b.stepTimeout},
			{"--stall-window", b.stall},
			{"--model-error-grace", b.modelErrorGrace},
		} {
			if f.val > 0 {
				d.extraArgs = append(d.extraArgs, f.name, f.val.String())
			}
		}
	}
}

// optAuto starts the daemon straight in auto-pull (`pf daemon --auto`).
func optAuto() daemonOpt {
	return func(d *v1Daemon) { d.extraArgs = append(d.extraArgs, "--auto") }
}

// optServerURL points the daemon's aihub API face (--server) at url
// instead of the install itself — the s17 hold-proxy front. In that
// case git pushes are pinned DIRECT to the install via --git-remote-base
// so the proxy only ever sees the API client's traffic. Without the opt
// the daemon args are exactly today's.
func optServerURL(url string) daemonOpt {
	return func(d *v1Daemon) { d.serverURL = url }
}

// configPath writes the daemon's cli.toml: the workspace mapping
// (mappingConfigPath) and the [daemon] keys of optDaemonKeys.
func (d *v1Daemon) configPath() string {
	path := mappingConfigPath(d.t, d.workspace, []string{v1Project})
	if len(d.daemonKeys) == 0 {
		return path
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		d.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n[daemon]\n" + strings.Join(d.daemonKeys, "\n") + "\n"); err != nil {
		d.t.Fatal(err)
	}
	return path
}

// shortTmp makes a short temp path (unix socket paths are bound to 107
// bytes on Linux; t.TempDir() paths can exceed that with nested test names) in the
// run's private TMPDIR (internal/testenv: short, under /tmp), removed when
// t ends — after the cleanups registered later, so after the daemon that
// binds a socket in it has stopped.
func shortTmp(t *testing.T, prefix string) string {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("shortTmp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// newV1Daemon prepares (without starting) a daemon process bound to its
// own socket, data dir and workspace.
func newV1Daemon(t *testing.T, inst *v1Install, opts ...daemonOpt) *v1Daemon {
	t.Helper()
	sockBase := shortTmp(t, "pfv1sock")
	sock := filepath.Join(sockBase, "d.sock")
	dataDir := t.TempDir()
	workspace := t.TempDir()
	d := &v1Daemon{
		t:         t,
		inst:      inst,
		sock:      sock,
		dataDir:   dataDir,
		workspace: workspace,
		machineID: "m-e2e",
		logPath:   filepath.Join(t.TempDir(), "daemon.log"),
	}
	for _, o := range opts {
		o(d)
	}
	t.Cleanup(func() { d.stop() })
	return d
}

// start launches the daemon process and waits until its UDS face answers.
func (d *v1Daemon) start() {
	d.t.Helper()
	d.launch()
	d.waitUp()
}

// launch starts the daemon process without waiting for its UDS face.
func (d *v1Daemon) launch() {
	d.t.Helper()
	// polyforge#106: no engine selection exists — the daemon argv carries
	// no engine flag because the driver IS the engine; the legacy-lane
	// hatch and its guard are retired with the lane they gated.
	logf, err := os.Create(d.logPath)
	if err != nil {
		d.t.Fatal(err)
	}
	// --server: the install itself, or the test's hold-proxy front
	// (optServerURL) — in the latter case git pushes are pinned direct
	// to the install so only the API client is proxied.
	serverURL := d.inst.url
	gitRemoteBase := ""
	if d.serverURL != "" {
		serverURL = d.serverURL
		gitRemoteBase = d.inst.url + "/git"
	}
	// optAPIKey: a dedicated aihub key (e.g. a machine user's key the
	// scenario revokes mid-run) instead of the bootstrap admin key.
	apiKey := d.apiKey
	if apiKey == "" {
		apiKey = d.inst.adminKey
	}
	// The retired executor prompt knob is gone (polyforge#106); the daemon
	// argv carries no prompt flag and no prompt file selection.
	args := []string{
		"daemon",
		"--socket", d.sock,
		"--server", serverURL,
		"--api-key", apiKey,
		"--machine-id", d.machineID,
		"--data-dir", d.dataDir,
		"--workflows-dir", d.inst.workflowsPath(),
		"--pi-bin", d.inst.fakepiPath,
		// never the host's real claude (a test opts in with its own --claude-bin)
		"--claude-bin", "/nonexistent/claude-test-guard",
		// polyforge#113: the [workspace] mapping config is the ONLY
		// workspace source (and it doubles as the config isolation file —
		// the daemon never reads an operator config file).
		"--config", d.configPath(),
	}
	if gitRemoteBase != "" {
		args = append(args, "--git-remote-base", gitRemoteBase)
	}
	args = append(args, d.extraArgs...)
	cmd := exec.Command(d.inst.pfPath, args...)
	cmd.Env = append(os.Environ(),
		"FAKEPI_PLANS="+d.inst.planDir,
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	)
	for _, kv := range d.extraEnv {
		if strings.HasPrefix(kv, "__PFV1_MODE__=") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Dir = d.dataDir
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	d.launchedAt = time.Now()
	if err := cmd.Start(); err != nil {
		d.t.Fatal(err)
	}
	d.cmd = cmd
	if d.inst != nil {
		d.inst.dmu.Lock()
		d.inst.daemons = append(d.inst.daemons, d)
		d.inst.dmu.Unlock()
	}
	d.waitedExit = make(chan error, 1)
	go func() { d.waitedExit <- cmd.Wait() }()
	t := d.t
	t.Cleanup(func() {
		_ = logf.Close()
		if t.Failed() {
			t.Logf("daemon log tail (%s):\n%s", d.logPath, d.logTail())
			t.Logf("daemon events tail:\n%s", eventsTail(v1CellStateDir(d.dataDir)))
		}
	})
}

// waitUp waits until the launched daemon answers on its UDS face. A bare
// dial is not readiness: listenUDS binds the socket before startup
// recovery runs (daemon.Run), so a test that returned on the dial and then
// seeded the data dir could race Recover. One answered GET /status means
// the face is serving, which happens only after recovery finished.
func (d *v1Daemon) waitUp() {
	d.t.Helper()
	answered, exited, werr := awaitAnsweredFace(d.sock, time.Now().Add(20*time.Second), d.waitedExit)
	switch {
	case exited:
		d.cmd = nil // self-exited: the cleanup stop() must no-op
		d.t.Fatalf("daemon exited during startup: %v (log tail: %s)", werr, d.logTail())
	case !answered:
		d.t.Fatalf("daemon UDS never answered on %s (log tail: %s)", d.sock, d.logTail())
	}
	// A fresh client, deliberately not the probe: the probe's short
	// per-request timeout must never constrain the test's later requests.
	d.client = cli.NewDaemonClient(d.sock)
}

// awaitAnsweredFace is the one readiness probe every daemon harness
// shares (see waitUp for why a bare dial is not readiness): it polls one
// GET /status on sock every 25ms until a request is answered, the process
// exit signal fires (exited, with its Wait error) or deadline passes
// (neither). The probe client's 1s timeout bounds ONE probe request,
// never readiness — deadline is the readiness bound. The probe is never
// handed to the caller: a caller that needs a client builds its own.
func awaitAnsweredFace(sock string, deadline time.Time, exit <-chan error) (answered, exited bool, werr error) {
	probe := cli.NewDaemonClient(sock)
	probe.HTTP.Timeout = time.Second
	for {
		if _, err := probe.Status(); err == nil {
			return true, false, nil
		}
		select {
		case werr := <-exit:
			return false, true, werr
		default:
		}
		if time.Now().After(deadline) {
			return false, false, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// logTail returns the last ~4KB of the daemon log for failure messages.
func (d *v1Daemon) logTail() string {
	raw, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("(unreadable log: %v)", err)
	}
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	return string(raw)
}

// stop terminates the daemon process: SIGTERM first (the graceful path
// when the process is mid-run), group SIGKILL fallback, then a group kill
// for any straggler left in the daemon's process group. Idempotent.
func (d *v1Daemon) stop() {
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.waitedExit:
	case <-time.After(10 * time.Second):
		killGroup(d.cmd)
		<-d.waitedExit
	}
	killGroup(d.cmd)
	d.cmd = nil
}

// crash kills the daemon process hard (SIGKILL) — the crash-restart
// scenarios need the agents to survive.
func (d *v1Daemon) crash() {
	d.t.Helper()
	if d.cmd == nil || d.cmd.Process == nil {
		d.t.Fatal("crash: daemon not running")
	}
	// a push the daemon's monitor had in flight is a git child that
	// outlives the SIGKILL and keeps writing the WI's bare repo: collect
	// those children first and let them drain after the kill
	gits := gitDescendants(d.cmd.Process.Pid)
	_ = d.cmd.Process.Kill()
	<-d.waitedExit
	d.cmd = nil
	waitPIDsGone(gits, 10*time.Second)
}

// gitDescendants lists the live git processes below pid (any depth).
func gitDescendants(pid int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	parent, comm := map[int]int{}, map[int]string{}
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may contain spaces/parens
		str := string(raw)
		lp, rp := strings.IndexByte(str, '('), strings.LastIndexByte(str, ')')
		if lp < 0 || rp < lp {
			continue
		}
		f := strings.Fields(str[rp+1:])
		if len(f) < 2 {
			continue
		}
		pp, _ := strconv.Atoi(f[1])
		parent[n], comm[n] = pp, str[lp+1:rp]
	}
	var out []int
	var walk func(p int)
	walk = func(p int) {
		for n, pp := range parent {
			if pp == p {
				if strings.HasPrefix(comm[n], "git") {
					out = append(out, n)
				}
				walk(n)
			}
		}
	}
	walk(pid)
	return out
}

// waitPIDsGone waits (softly) until every pid has exited.
func waitPIDsGone(pids []int, bound time.Duration) {
	deadline := time.Now().Add(bound)
	for _, p := range pids {
		for time.Now().Before(deadline) {
			if _, err := os.Stat("/proc/" + strconv.Itoa(p)); err != nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

// exitedWithin reports whether the daemon process exited within bound
// (the graceful-shutdown contract: Run returns once the sequence ends).
// The exit is consumed here: a later stop() becomes a no-op.
func (d *v1Daemon) exitedWithin(bound time.Duration) bool {
	if d.cmd == nil || d.cmd.Process == nil {
		return true // already collected (e.g. by a previous call)
	}
	select {
	case <-d.waitedExit:
		d.cmd = nil
		return true
	case <-time.After(bound):
		return false
	}
}

// pf runs the real pf CLI against THIS daemon's socket (must succeed).
func (d *v1Daemon) pf(t *testing.T, args ...string) string {
	t.Helper()
	return d.inst.pfExit(t, -1, d.sock, args...)
}

// status reads GET /status.
func (d *v1Daemon) status(t *testing.T) cli.StatusResponse {
	t.Helper()
	if d.client == nil {
		t.Fatal("daemon not started")
	}
	raw, err := d.client.Status()
	if err != nil {
		t.Fatalf("daemon status: %v (log tail: %s)", err, d.logTail())
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status undecodable: %v (%s)", err, raw)
	}
	return st
}

// watch reads the daemon's watch snapshot for wi (Execution is non-nil:
// the snapshot must know the work item).
func (d *v1Daemon) watch(t *testing.T, wi string) *protocol.WatchSnapshot {
	t.Helper()
	snap, err := d.client.WatchSnapshot(wi, 0, false)
	if err != nil {
		t.Fatalf("watch %s: %v (log tail: %s)", wi, err, d.logTail())
	}
	if snap.Execution == nil {
		t.Fatalf("watch %s: no execution in %+v (log tail: %s)", wi, snap, d.logTail())
	}
	return snap
}

// capsuleDir is this daemon's capsule root for wi.
func (d *v1Daemon) capsuleDir(wi string) string {
	return filepath.Join(d.workspace, "capsules", wiPathName(d.t, d.inst, wi))
}

// wiPathName answers the capsule directory / delivery branch name of wi —
// "<project>-<seq>" (polyforge#144) — from its aihub record.
func wiPathName(t *testing.T, inst *v1Install, wi string) string {
	t.Helper()
	item, err := inst.client.GetWorkItem(wi)
	if err != nil {
		t.Fatalf("get work item %s: %v", wi, err)
	}
	return api.PathName(item.Slug, item.ID)
}

// wiSlug answers wi's "<project>#<seq>" slug — the name the human faces
// (ops status, watch, wake-up lines) show (polyforge#144).
func wiSlug(t *testing.T, inst *v1Install, wi string) string {
	t.Helper()
	item, err := inst.client.GetWorkItem(wi)
	if err != nil || item.Slug == "" {
		t.Fatalf("get work item %s slug: %+v %v", wi, item, err)
	}
	return item.Slug
}

// eventsRaw concatenates the daemon's durable events files (one JSONL
// file per work item under <data-dir>/events/); "" before the first.
func eventsRaw(t *testing.T, dataDir string) string {
	t.Helper()
	dir := filepath.Join(v1CellStateDir(dataDir), "events")
	des, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("events dir: %v", err)
	}
	var b strings.Builder
	for _, de := range des {
		raw, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			t.Fatalf("events file: %v", err)
		}
		b.Write(raw)
	}
	return b.String()
}

// eventsOf reads the daemon's durable events and returns the entries of
// one work item.
func (d *v1Daemon) eventsOf(t *testing.T, wi string) []protocol.EventEntry {
	t.Helper()
	raw := eventsRaw(t, d.dataDir)
	var out []protocol.EventEntry
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e protocol.EventEntry
		if json.Unmarshal([]byte(line), &e) == nil && (e.WI == wi || e.WI == "") {
			if e.WI == wi {
				out = append(out, e)
			}
		}
	}
	return out
}

// eventsTail is the last 8 KiB of a data dir's durable events (every work
// item, with bodies), for a failed scenario's log: a timed-out wait names
// what it waited for, the events name what happened instead (a verdict's
// cause, a recovery, a block reason). It never fails the test.
func eventsTail(dataDir string) string {
	dir := filepath.Join(dataDir, "events")
	des, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("(no events: %v)", err)
	}
	var b strings.Builder
	for _, de := range des {
		if raw, err := os.ReadFile(filepath.Join(dir, de.Name())); err == nil {
			b.Write(raw)
		}
	}
	out := b.String()
	if len(out) > 8192 {
		out = out[len(out)-8192:]
	}
	return out
}

// eventKinds is the ordered kind list of one work item's events.
func (d *v1Daemon) eventKinds(t *testing.T, wi string) []string {
	t.Helper()
	var kinds []string
	for _, e := range d.eventsOf(t, wi) {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// ---------------------------------------------------------------------------
// Pollers.

// waitUntil polls cond every 100ms until it holds or the bound expires;
// the failure message carries desc so a red is diagnosable from the log.
func waitUntil(t *testing.T, bound time.Duration, desc string, cond func() bool) {
	t.Helper()
	waitDeadline(t, bound, func() string { return desc }, cond)
}

// waitDeadline is waitUntil with the failure text built at the deadline: a
// description that samples live state must report the state at the timeout,
// not the one read before the first poll (six tickets read a
// stale "queued" that way while the work item was already "blocked").
func waitDeadline(t *testing.T, bound time.Duration, desc func() string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout (%v): %s", bound, desc())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitWIStatus polls the aihub-side status of wi.
func (u *v1Install) waitWIStatus(t *testing.T, wi, status string, bound time.Duration) {
	t.Helper()
	waitDeadline(t, bound,
		func() string {
			return fmt.Sprintf("work item %s never reached status %q (now %q)", wi, status, u.wiStatus(t, wi))
		},
		func() bool { return u.wiStatus(t, wi) == status })
	if status == "wrapped" {
		// the server-side wrap lands before the daemon's own terminal close
		// (job release, monitor stop, final push) has drained: a scenario
		// that returns right here races the teardown of the aihub data dir
		u.settleClose(wi)
	}
}

// settleClose waits (softly: never fails the scenario) until every
// started daemon has drained the terminal close of wi — the job gone from
// its status face and its monitor stopped — so a test that ends at the
// terminal state does not race the final push against temp-dir cleanup.
func (u *v1Install) settleClose(wi string) {
	u.dmu.Lock()
	ds := append([]*v1Daemon(nil), u.daemons...)
	u.dmu.Unlock()
	for _, d := range ds {
		d.settleClose(wi)
	}
}

// settleClose is the per-daemon half of v1Install.settleClose.
func (d *v1Daemon) settleClose(wi string) {
	if d.cmd == nil || d.client == nil {
		return
	}
	held := func() bool {
		raw, err := d.client.Status()
		if err != nil {
			return false // daemon gone: nothing left to drain
		}
		var st cli.StatusResponse
		if json.Unmarshal(raw, &st) != nil {
			return false
		}
		for _, j := range st.Jobs {
			if j.WorkItemID == wi {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(12 * time.Second)
	for held() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// a monitor that started must also have stopped (job_stopped is its
	// last act; the final published push precedes it). Only this daemon
	// process's monitors count: one that started before its launch died
	// with a previous process and never writes its job_stopped.
	stopped := func() bool {
		started, stopped := false, false
		for _, e := range d.eventsOf(d.t, wi) {
			if at, err := time.Parse(time.RFC3339Nano, e.At); err == nil && at.Before(d.launchedAt) {
				continue
			}
			switch e.Kind {
			case "job_started":
				started, stopped = true, false
			case "job_stopped":
				stopped = true
			}
		}
		return !started || stopped
	}
	deadline = time.Now().Add(6 * time.Second)
	for !stopped() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForEvent polls the daemon's durable events until one event of
// the given kind appears for wi, and returns it.
func waitForEvent(t *testing.T, d *v1Daemon, wi, kind string, bound time.Duration) protocol.EventEntry {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == kind {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: no %q event for %s (saw %v)", kind, wi, d.eventKinds(t, wi))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Real-git assertions against the aihub's on-disk bare repos.

// bareRepo is the aihub bare repo path of one work item.
func (u *v1Install) bareRepo(wi string) string {
	return filepath.Join(u.dataDir, "git", wi+".git")
}

// gitRun runs git with a bounded output capture.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pf-e2e", "GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e", "GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir %s): %v: %s", args, dir, err, out)
	}
	return string(out)
}

// bareCommitCount is the number of commits on the bare repo's main
// (0 when the repo is absent or still unborn).
func (u *v1Install) bareCommitCount(t *testing.T, wi string) int {
	t.Helper()
	if _, err := os.Stat(filepath.Join(u.bareRepo(wi), "HEAD")); err != nil {
		return 0
	}
	cmd := exec.Command("git", "--git-dir", u.bareRepo(wi), "rev-list", "--count", "main")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "ambiguous argument") ||
			strings.Contains(string(out), "unknown revision") {
			return 0 // unborn branch: the chain has not started yet
		}
		t.Fatalf("rev-list count: %v: %s", err, out)
	}
	n, perr := strconv.Atoi(strings.TrimSpace(string(out)))
	if perr != nil {
		t.Fatalf("rev-list count %q: %v", out, perr)
	}
	return n
}

// waitBareCommits waits until the server-side chain holds at least n
// commits on main.
func (u *v1Install) waitBareCommits(t *testing.T, wi string, n int, bound time.Duration) {
	t.Helper()
	waitUntil(t, bound, fmt.Sprintf("bare repo of %s never reached %d commits (has %d)", wi, n, u.bareCommitCount(t, wi)),
		func() bool { return u.bareCommitCount(t, wi) >= n })
}

// capsuleCommitCount is the LOCAL chain length of one capsule: commits
// on main of the daemon-side external git dir (<dataDir>/git/<wi>.git).
// A failed push leaves its commit here — the backpressure scenarios'
// catch-up baseline (what the server-side bare repo must reach once the
// aihub is back).
func capsuleCommitCount(t *testing.T, dataDir, wi string) int {
	t.Helper()
	gitDir := filepath.Join(v1CellStateDir(dataDir), "git", wi+".git")
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err != nil {
		return 0
	}
	cmd := exec.Command("git", "--git-dir", gitDir, "rev-list", "--count", "main")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "ambiguous argument") ||
			strings.Contains(string(out), "unknown revision") {
			return 0 // unborn branch: the chain has not started yet
		}
		t.Fatalf("capsule rev-list count: %v: %s", err, out)
	}
	n, perr := strconv.Atoi(strings.TrimSpace(string(out)))
	if perr != nil {
		t.Fatalf("capsule rev-list count %q: %v", out, perr)
	}
	return n
}

// bareHasPath reports whether the server-side chain's main tip carries
// the given path (the capsule revision chain content check).
func (u *v1Install) bareHasPath(t *testing.T, wi, path string) bool {
	t.Helper()
	if u.bareCommitCount(t, wi) == 0 {
		return false
	}
	out, err := func() (string, error) {
		cmd := exec.Command("git", "--git-dir", u.bareRepo(wi), "ls-tree", "--name-only", "-r", "main", path)
		cmd.Env = os.Environ()
		b, err := cmd.CombinedOutput()
		return string(b), err
	}()
	if err != nil {
		t.Fatalf("ls-tree: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out)) != ""
}

// bareFileAtMain dumps one file of the server-side chain's main tip.
func (u *v1Install) bareFileAtMain(t *testing.T, wi, path string) string {
	t.Helper()
	return gitRun(t, u.bareRepo(wi), "show", "main:"+path)
}

// ---------------------------------------------------------------------------
// Fake-agent journal + spawn journal readers (capsule-side ledgers).

// fakeJournal parses logs/fakepi.jsonl of one capsule.
func fakeJournal(t *testing.T, capsuleDir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capsuleDir, "logs", "fakepi.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("fakepi journal: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// spawnJournal parses runtime-state/spawn-journal.jsonl of one capsule.
func spawnJournal(t *testing.T, capsuleDir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capsuleDir, "runtime-state", "spawn-journal.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("spawn journal: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Orphan scan: no process may survive carrying a work item's identity.

// orphanPIDs lists live pids whose environ carries PF_WI_ID=<wi> (the
// layer-4 cleanup marker every derived agent is born with).
func orphanPIDs(wi string) []int {
	marker := "PF_WI_ID=" + wi
	var out []int
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/environ")
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(string(raw), "\x00") {
			if kv == marker {
				out = append(out, pid)
				break
			}
		}
	}
	return out
}

// killOrphans hard-kills any surviving process carrying the work item's
// env marker (crash-scenario hygiene: a crashed daemon's agents are
// deliberately left alive by the scenario and must not leak past it).
func killOrphans(wi string) {
	for _, pid := range orphanPIDs(wi) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// trackWI records wi in the install's sweep set: after the scenario's
// whole teardown, every live process carrying PF_WI_ID=<wi> is killed and
// asserted gone (sweepOrphans). newWI registers automatically; scenarios
// that file a ticket through the raw aihub client (s12's priority wave
// member, s13's refusal probe) register here so the sweep covers every
// WI of the scenario.
func (u *v1Install) trackWI(wi string) {
	for _, w := range u.wis {
		if w == wi {
			return
		}
	}
	u.wis = append(u.wis, wi)
}

// sweepOrphans is the harness-level post-teardown orphan sweep: after
// every daemon process of the scenario has been fully torn down (graceful
// stop, crash, the SIGKILL fallback, struct-literal daemon structs, the
// inproc rig alike), it scans /proc for any live process still carrying
// one of the scenario's PF_WI_ID markers — the same L4 env-marker
// semantics as orphanPIDs, so grandchildren of the daemon's agent tree
// (born with the inherited environ) are caught too — SIGKILLs every
// survivor, and then, with a short bounded poll (a SIGKILLed process
// takes a moment to leave /proc), asserts that none is left. Any
// survivor at that point fails the test with the leaked pids, so a red
// is diagnosable; the assertion lands even when the body already
// failed. Registered by newV1InstallRepos as the scenario's FIRST
// cleanup, so LIFO order runs it after all daemon teardown cleanups.
func (u *v1Install) sweepOrphans() {
	t := u.t
	if len(u.wis) == 0 {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, wi := range u.wis {
			killOrphans(wi)
		}
		var leaks []string
		for _, wi := range u.wis {
			if pids := orphanPIDs(wi); len(pids) > 0 {
				leaks = append(leaks, fmt.Sprintf("%s: %v", wi, pids))
			}
		}
		if len(leaks) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("post-teardown orphan sweep: leaked agent processes survived the scenario's teardown (PF_WI_ID markers still live after SIGKILL): %s",
				strings.Join(leaks, "; "))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// httpStatusOf is a one-shot HTTP helper for raw wire assertions.
func httpStatusOf(method, url, user, secret string) (int, http.Header, string) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return -1, nil, err.Error()
	}
	if user != "" {
		req.SetBasicAuth(user, secret)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, nil, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, string(body)
}

// ---------------------------------------------------------------------------
// Driver/envelope test faces (polyforge#105). The migrated v1_s*/v1_t7
// scenarios assert on the DRIVER's observable faces: execution.json
// (steps / budget_consumed / version progression), the archived
// artifacts/steps/<step>/s<n>/result.json envelopes, the ops status
// QUESTION column, `pf answer` round-trips, the spawn journal and the
// fakepi journal. The helpers below are those faces, once.

// execStepView is the loose read shape of one execution.json step record
// (a subset that keeps every unmarshal honest — the same discipline as
// fakepi's own execStep).
type execStepView struct {
	Status string `json:"status"`
	// Candidate is the `<harness>:<model>[:effort]` the step last ran
	// (or will next run) on — the one model fact of a step record.
	Candidate       string           `json:"candidate"`
	StepStartedAt   string           `json:"step_started_at"`
	Attempts        []map[string]any `json:"attempts"`
	Answers         []map[string]any `json:"answers"`
	PendingQuestion *map[string]any  `json:"pending_question"`
	BudgetConsumed  map[string]any   `json:"budget_consumed"`
	// Supervision is the driver-owned failure ledger and relaxed bounds
	// (v2-converge 2.3/2.4).
	Supervision map[string]any `json:"supervision"`
	// Dispatch is the ordinal of the dispatch the step awaits (set by
	// its step_start, cleared once settled).
	Dispatch int64 `json:"dispatch"`
}

// model and effort answer the step's candidate parts ("" when none).
func (st *execStepView) model() string {
	c, _ := harness.ParseCandidate(st.Candidate)
	return c.Model
}

func (st *execStepView) effort() string {
	c, _ := harness.ParseCandidate(st.Candidate)
	return c.Effort
}

// executionView is the loose read shape of runtime-state/execution.json.
type executionView struct {
	WorkflowStatus string                   `json:"workflow_status"`
	BlockedReason  string                   `json:"blocked_reason"`
	CurrentStep    string                   `json:"current_step"`
	Version        int64                    `json:"version"`
	BudgetConsumed map[string]any           `json:"budget_consumed"`
	RewindStats    map[string]any           `json:"rewind_stats"`
	Steps          map[string]*execStepView `json:"steps"`
}

// readExecutionFile reads and parses one capsule's execution.json; the
// error is "the file is not there (yet)" or "torn" — callers decide.
func readExecutionFile(capDir string) (*executionView, error) {
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		return nil, err
	}
	var x executionView
	if err := json.Unmarshal(raw, &x); err != nil {
		return nil, err
	}
	return &x, nil
}

// readExecution reads one capsule's execution.json, failing the test on
// absence or a torn write past the bound (callers inside waitExecution
// poll through both).
func readExecution(t *testing.T, capDir string) *executionView {
	t.Helper()
	x, err := readExecutionFile(capDir)
	if err != nil {
		t.Fatalf("execution.json of %s: %v", capDir, err)
	}
	return x
}

// waitExecution polls the capsule's execution.json until cond holds (or
// the bound expires with desc naming what never happened). Absent and
// torn reads are retried, never fatal — the driver's writes are atomic
// but the file may not exist yet.
func waitExecution(t *testing.T, capDir string, bound time.Duration, desc string, cond func(*executionView) bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if x, err := readExecutionFile(capDir); err == nil && cond(x) {
			return
		}
		if time.Now().After(deadline) {
			x, _ := readExecutionFile(capDir)
			t.Fatalf("timeout (%v): %s (execution.json: %s)", bound, desc, execDigest(x))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// execDigest is a one-line summary of an execution view for failure
// messages.
func execDigest(x *executionView) string {
	if x == nil {
		return "<absent>"
	}
	desc := fmt.Sprintf("status=%s version=%d", x.WorkflowStatus, x.Version)
	for id, st := range x.Steps {
		desc += fmt.Sprintf(" %s=%s", id, st.Status)
	}
	return desc
}

// waitWorkflowStatus polls the capsule's execution.json until the
// workflow_status reaches want (the driver-era completion face).
func waitWorkflowStatus(t *testing.T, d *v1Daemon, wi, want string, bound time.Duration) {
	t.Helper()
	waitExecution(t, d.capsuleDir(wi), bound,
		fmt.Sprintf("workflow_status never reached %q", want),
		func(x *executionView) bool { return x.WorkflowStatus == want })
	if want == "completed" {
		// completed is the driver's last write; the daemon's terminal
		// close (final push, wrap, job release) still follows — drain it
		// before the scenario can end and tear the data dir down
		d.settleClose(wi)
	}
}

// waitStepStatus polls the capsule's execution.json until one step's
// status reaches want.
func waitStepStatus(t *testing.T, d *v1Daemon, wi, step, want string, bound time.Duration) {
	t.Helper()
	waitExecution(t, d.capsuleDir(wi), bound,
		fmt.Sprintf("step %s never reached %q", step, want),
		func(x *executionView) bool {
			st := x.Steps[step]
			return st != nil && st.Status == want
		})
}

// questionIDOf reads the pending question id of one work item off the
// daemon status face (the QUESTION column's source; "" when none).
func questionIDOf(st *cli.StatusResponse, wi string) string {
	if st == nil {
		return ""
	}
	for _, j := range st.Jobs {
		if j.WorkItemID == wi {
			return j.QuestionID
		}
	}
	return ""
}

// statusQuiet polls the daemon status face without failing (nil on any
// transport hiccup — the caller's waitUntil retries).
func statusQuiet(d *v1Daemon) *cli.StatusResponse {
	if d.client == nil {
		return nil
	}
	raw, err := d.client.Status()
	if err != nil {
		return nil
	}
	var st cli.StatusResponse
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	return &st
}

// waitQuestion polls the daemon status face until the work item's
// QUESTION column shows qid (the D6 owner face of a parked question).
func waitQuestion(t *testing.T, d *v1Daemon, wi, qid string, bound time.Duration) {
	t.Helper()
	waitDeadline(t, bound, func() string {
		// the work item's events name what happened instead (a verdict, a
		// recovery, a workflow that went past the question)
		return fmt.Sprintf("QUESTION column never showed %s (events: %v)", qid, d.eventKinds(t, wi))
	}, func() bool {
		return questionIDOf(statusQuiet(d), wi) == qid
	})
}

// askAction is a plan action whose step asks the owner (`pf step ask`, workflow
// contract v2 §5.2) and ends its turn; onMessage (nil = none) is the turn
// the still-resident process answers the owner's reply with — a reply to a
// process that is gone resumes the session as the plan's next dispatch.
func askAction(step, question string, onMessage map[string]any) map[string]any {
	a := map[string]any{"do": "envelope", "id": step, "end_turn": true, "run": []string{"pf step ask " + shellQuote(question)}}
	if onMessage != nil {
		a["on_message"] = onMessage
	}
	return a
}

// waitAsk polls the daemon status face until the work item's QUESTION
// column shows an open ask and answers its id (the ask's own stamp).
func waitAsk(t *testing.T, d *v1Daemon, wi string, bound time.Duration) string {
	t.Helper()
	var at string
	waitDeadline(t, bound, func() string {
		return fmt.Sprintf("QUESTION column never showed an open ask (events: %v)", d.eventKinds(t, wi))
	}, func() bool {
		at = questionIDOf(statusQuiet(d), wi)
		return at != ""
	})
	return at
}

// waitNewAsk is waitAsk for an ask other than prev (a later dispatch's).
func waitNewAsk(t *testing.T, d *v1Daemon, wi, prev string, bound time.Duration) string {
	t.Helper()
	var at string
	waitDeadline(t, bound, func() string {
		return fmt.Sprintf("QUESTION column never showed an ask other than %q (events: %v)", prev, d.eventKinds(t, wi))
	}, func() bool {
		at = questionIDOf(statusQuiet(d), wi)
		return at != "" && at != prev
	})
	return at
}

// openAskOn answers the open ask of (step, spawn) in the capsule's
// asks.jsonl — the durable record behind the QUESTION column waitAsk read.
func openAskOn(t *testing.T, capDir, step string, spawn int64) capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(capDir)
	if err != nil {
		t.Fatalf("asks.jsonl: %v", err)
	}
	open := capsule.OpenAsk(lines, step, spawn)
	if open == nil {
		t.Fatalf("no open ask of %s/%d in asks.jsonl: %+v", step, spawn, lines)
	}
	return *open
}

// replyOwner relays the owner's words to the step's agent through the
// real CLI (`pf answer --text`) and asserts the relay recorded them.
func replyOwner(t *testing.T, d *v1Daemon, wi, text string) string {
	t.Helper()
	out := d.pf(t, "answer", wi, "--text", text, "--by", "usr_root")
	if !strings.Contains(out, "recorded for step") {
		t.Fatalf("pf answer: %q", out)
	}
	return out
}

// exhaustThenPark waits for a spent rewind budget to end in a park
// (workflow contract 4, design #483 §5.1): the gate's fail that cites no
// owner message is refused at the call (`pf step handoff`, nothing
// recorded), the gate's resumed session hands off blocked (the scripted
// stand-in for asking the owner), and the owner cancels it through the
// real CLI; it waits for the canceled close.
func exhaustThenPark(t *testing.T, d *v1Daemon, wi, gate string, bound time.Duration) {
	t.Helper()
	waitWorkflowStatus(t, d, wi, "blocked", bound)
	refused := false
	for _, l := range ledgerLines(t, d.capsuleDir(wi)) {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHandoff && l.Step == gate && l.Outcome == stepcall.OutcomeRefused &&
			strings.Contains(l.Error, "rewind budget is spent") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("a spent rewind budget must refuse %s's fail at the call (no refused hand-off naming the budget on the ledger)", gate)
	}
	inst := d.inst
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	out := d.pf(t, "cancel", wi)
	if !strings.Contains(out, "cancel") {
		t.Fatalf("pf cancel: %q", out)
	}
	inst.waitWIStatus(t, wi, "canceled", 60*time.Second)
}

// ledgerLines reads the capsule's call ledger
// (runtime-state/step-calls.jsonl, workflow contract 4).
func ledgerLines(t *testing.T, capDir string) []stepcall.Line {
	t.Helper()
	lines, err := stepcall.Read(capDir)
	if err != nil {
		t.Fatalf("the call ledger of %s: %v", capDir, err)
	}
	return lines
}

// acceptedHandoff answers the dispatch (step, spawn)'s accepted hand-off
// on the capsule's call ledger — the record the driver reacts to (workflow
// contract 4; contract 3's archived envelope,
// artifacts/steps/<step>/s<spawn>/result.json, is gone).
func acceptedHandoff(t *testing.T, capDir, step string, spawn int) stepcall.Line {
	t.Helper()
	l := stepcall.HandoffOf(ledgerLines(t, capDir), step, int64(spawn))
	if l == nil || l.Handoff == nil {
		t.Fatalf("no accepted hand-off of %s dispatch %d on the call ledger", step, spawn)
	}
	return *l
}

// waitSpawnCount polls the capsule's spawn journal until it carries n
// entries (the derivation-count face), returning the entries.
func waitSpawnCount(t *testing.T, d *v1Daemon, wi string, n int, bound time.Duration) []map[string]any {
	t.Helper()
	waitUntil(t, bound, fmt.Sprintf("spawn journal never reached %d entries", n), func() bool {
		return len(spawnJournal(t, d.capsuleDir(wi))) >= n
	})
	return spawnJournal(t, d.capsuleDir(wi))
}

// waitSpawnTranscript waits until the n-th spawn's agent child has
// written its harness transcript (the file the spawn journal names, or
// the fakepi journal's n-th "spawn" record when no path is recorded).
// The spawn-journal line is written by the PARENT before the child
// starts, but the driver only resumes from a transcript that exists and
// is non-empty — a kill landing in that gap silently downgrades the
// retry to a fresh dispatch. Tests that kill/crash right after
// waitSpawnCount and assert on resume must wait here first.
func waitSpawnTranscript(t *testing.T, d *v1Daemon, wi string, n int, bound time.Duration) {
	t.Helper()
	capDir := d.capsuleDir(wi)
	waitUntil(t, bound, fmt.Sprintf("spawn %d never wrote its transcript", n), func() bool {
		entries := spawnJournal(t, capDir)
		if len(entries) < n {
			return false
		}
		path, _ := entries[n-1]["transcript"].(string)
		if path == "" {
			spawns := 0
			for _, r := range fakeJournal(t, capDir) {
				if r["type"] == "spawn" {
					spawns++
				}
			}
			return spawns >= n
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(capDir, path)
		}
		st, err := os.Stat(path)
		return err == nil && st.Mode().IsRegular() && st.Size() > 0
	})
}

// latestSpawnPID reads the newest spawn-journal entry's pid — the live
// step process a scenario SIGKILLs (driver WIs carry no agent pid on the
// status face; the step process IS the supervised unit).
func latestSpawnPID(t *testing.T, d *v1Daemon, wi string) int {
	t.Helper()
	entries := spawnJournal(t, d.capsuleDir(wi))
	if len(entries) == 0 {
		t.Fatalf("no spawn-journal entries for %s", wi)
	}
	pid, _ := entries[len(entries)-1]["pid"].(float64)
	return int(pid)
}

// envelopeMicro is the canonical plan of the vehicle lane (micro, its
// deliver step stripped): one envelope action per workflow step in
// declaration order. The Nth envelope action fires for spawn ordinal N.
func envelopeMicro() []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// ---------------------------------------------------------------------------
// Shared small helpers. These used to live in whichever scenario file
// defined them first; the migration rewrites every scenario file, so the
// cross-file ones moved here once (compile-neutral re-homing — the
// users are unchanged).

// contains reports whether list carries s.
func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// countOf counts occurrences of s in list.
func countOf(list []string, s string) int {
	n := 0
	for _, e := range list {
		if e == s {
			n++
		}
	}
	return n
}

// watchSoft is watch that tolerates the not-yet-known window: until the
// auto wave claims the ticket the snapshot is not Known, and a poll must
// retry rather than fail.
func watchSoft(d *v1Daemon, wi string) *protocol.WatchSnapshot {
	snap, err := d.client.WatchSnapshot(wi, 0, false)
	if err != nil || !snap.Known {
		return nil
	}
	return snap
}

// writePlanExtra rewrites the fake agent's plan file with extra top-level
// fields beyond actions/resume_actions (the first process start reads
// THIS version; later rewrites steer re-derivations only).
func writePlanExtra(t *testing.T, inst *v1Install, wi string, actions, resume []map[string]any, extra map[string]any) {
	t.Helper()
	body := map[string]any{"actions": actions}
	if resume != nil {
		body["resume_actions"] = resume
	}
	for k, v := range extra {
		body[k] = v
	}
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.planDir, wi+".json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// restartSameDaemon re-execs a daemon on the SAME socket, data dir and
// workspace (so the events and capsules are
// exactly what the new process re-opens) — the s08 restart shape, with
// the observe-window flags carried over.
func restartSameDaemon(t *testing.T, d *v1Daemon, logName string) *v1Daemon {
	t.Helper()
	d2 := &v1Daemon{
		t: t, inst: d.inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, extraArgs: d.extraArgs,
		logPath: filepath.Join(t.TempDir(), logName),
	}
	t.Cleanup(func() { d2.stop() })
	d2.start()
	return d2
}

// listWorkItems reads GET /api/v1/work-items through the raw client and
// decodes the items (test-only; the CLI itself consumes the raw body).
func listWorkItems(inst *v1Install, project, status string) ([]api.WorkItem, error) {
	raw, err := inst.client.ListWorkItemsRaw(project, status, 0)
	if err != nil {
		return nil, err
	}
	var out api.ListResponse[api.WorkItem]
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// v1BusinessEnv rewrites a pf child's environment for the daemon-mediated
// data plane: the CLI refuses PF_SERVER/PF_API_KEY (a Cell's connection is
// configured, and every business call goes through the daemon's
// ANY /aihub/{path...} proxy), so a harness that names the install's Hub
// that way gets a unix-socket front standing in for that proxy — /aihub/…
// forwarded to the Hub with the key as the Cell credential, every other
// path relayed to the env's PF_DAEMON_SOCKET daemon (when it answers). The
// last spelling of each variable wins, as for exec. Without PF_SERVER the
// env is returned unchanged. The real daemon's proxy is pinned by the
// w348 acceptance suite; this front keeps the per-verb suites on the
// install's admin identity.
func v1BusinessEnv(t *testing.T, env []string) []string {
	t.Helper()
	return v1BusinessEnvWith(t, env, "")
}

// v1BusinessCmd applies v1BusinessEnv to a prepared pf command; with no
// PF_DAEMON_SOCKET in its env, the daemon the front relays to is the one
// its --config file names (daemon_socket), when it names one.
func v1BusinessCmd(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	fileSock := ""
	for i, a := range cmd.Args {
		if a == "--config" && i+1 < len(cmd.Args) {
			if raw, err := os.ReadFile(cmd.Args[i+1]); err == nil {
				if m := regexp.MustCompile(`(?m)^\s*daemon_socket\s*=\s*"([^"]+)"`).FindSubmatch(raw); m != nil {
					fileSock = string(m[1])
				}
			}
		}
	}
	cmd.Env = v1BusinessEnvWith(t, cmd.Env, fileSock)
	cmd.Args = append(cmd.Args[:1:1], submitAllowNoAcceptance(cmd.Args[1:])...)
}

// pfGlobalValueFlags are pf's global flags that take a value (cli.Run's
// global flag set); --json and --version take none.
var pfGlobalValueFlags = map[string]bool{"config": true, "server": true, "api-key": true, "socket": true, "user": true, "profile": true}

// submitAllowNoAcceptance answers pf's argv (without the program) with
// `--allow-no-acceptance` appended when its verb — the first positional
// after the global flags and their values — is `submit` and the switch is
// absent. The e2e scenarios submit content-less tickets to exercise the
// daemon, not `pf submit`'s acceptance check (pinned by the cli package's
// TestW460SubmitAcceptanceCheck).
func submitAllowNoAcceptance(args []string) []string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--" {
		if pfGlobalValueFlags[strings.TrimLeft(args[i], "-")] {
			i++ // the flag's value
		}
		i++
	}
	if i < len(args) && args[i] == "--" {
		i++
	}
	if i >= len(args) || args[i] != "submit" || slices.Contains(args[i+1:], "--allow-no-acceptance") {
		return args
	}
	return append(slices.Clip(args), "--allow-no-acceptance")
}

// v1BusinessEnvWith is v1BusinessEnv with the fallback daemon socket to
// relay to when the env names none.
func v1BusinessEnvWith(t *testing.T, env []string, fallbackSock string) []string {
	t.Helper()
	var hub, key, inner string
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "PF_SERVER="):
			hub = strings.TrimPrefix(kv, "PF_SERVER=")
		case strings.HasPrefix(kv, "PF_API_KEY="):
			key = strings.TrimPrefix(kv, "PF_API_KEY=")
		case strings.HasPrefix(kv, "PF_DAEMON_SOCKET="):
			inner = strings.TrimPrefix(kv, "PF_DAEMON_SOCKET=")
		default:
			out = append(out, kv)
		}
	}
	if hub == "" {
		if inner != "" {
			out = append(out, "PF_DAEMON_SOCKET="+inner)
		}
		return out
	}
	if inner == "" {
		inner = fallbackSock
	}
	return append(out, "PF_DAEMON_SOCKET="+v1CellFront(t, hub, key, inner))
}

// v1CellFront serves the stand-in proxy socket (v1BusinessEnv). A daemon
// socket that does not answer stays as it is: no daemon, no business.
func v1CellFront(t *testing.T, hub, key, inner string) string {
	t.Helper()
	if inner != "" {
		if conn, err := net.DialTimeout("unix", inner, time.Second); err != nil {
			return inner
		} else {
			conn.Close()
		}
	}
	hubURL, err := url.Parse(hub)
	if err != nil {
		t.Fatalf("hub URL %q: %v", hub, err)
	}
	toHub := httputil.NewSingleHostReverseProxy(hubURL)
	base := toHub.Director
	toHub.Director = func(r *http.Request) {
		base(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/aihub")
		r.URL.RawPath = ""
		r.Header.Del("X-PF-Profile")
		r.Header.Set("Authorization", "Bearer "+key)
	}
	toHub.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		b, _ := json.Marshal(map[string]string{"code": "DAEMON-503", "detail": "Cell \"default\": its AIHub " + hub + " is unreachable: " + err.Error()})
		_, _ = w.Write(b)
	}
	var toDaemon http.Handler = http.NotFoundHandler()
	if inner != "" {
		toDaemon = &httputil.ReverseProxy{
			Director: func(r *http.Request) { r.URL.Scheme, r.URL.Host = "http", "daemon" },
			// no idle connection outlives a relayed request: the daemon's fd
			// table (the leak probes) must see only the CLI's own traffic
			Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", inner)
			}},
		}
	}
	sock := filepath.Join(shortTmp(t, "pffront"), "f.sock")
	lst, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("front listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/aihub/") {
			toHub.ServeHTTP(w, r)
			return
		}
		if inner == "" && r.Method == http.MethodGet && r.URL.Path == "/profiles" {
			// a front with no daemon behind it still answers the CLI
			// resolver like a one-Cell Host (an empty effective index)
			_, _ = w.Write([]byte(`{"profiles":[],"index":[]}`))
			return
		}
		toDaemon.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(lst) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// v1CellStateDir answers the default Cell's state dir under a daemon data
// dir: <data-dir>/cells/default for a Host (every Cell writes under
// cells/<profile>), the data dir itself for the in-process rig, which runs
// one runtime straight on its dir.
func v1CellStateDir(dataDir string) string {
	cell := filepath.Join(dataDir, "cells", "default")
	if _, err := os.Stat(cell); err == nil {
		return cell
	}
	return dataDir
}
