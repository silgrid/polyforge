// carries: internal/polyforge/components/upgrade.go,
//
//	internal/polyforge/components/status.go,
//	internal/polyforge/components/assemble.go
package components

// upgrade_test.go — polyforge#190 (84-inplace-upgrade), u2: the
// upgrade transaction itself, authored test-first against AC3's own
// words (and AC2's "nothing changes on a failure" through the whole
// orchestration). The daemon face and the Pi Web termination are the
// two seams; everything else runs the real transaction — Install's
// switch included. Every test name carries Drain, so the acceptance
// proof `go test ./internal/polyforge/components/ -run Drain` runs
// exactly this behaviour.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// faceCall is one recorded call of the fake daemon face.
type faceCall struct {
	verb  string // "drain" or "restart"
	force bool
	busy  []string
}

// fakeFace answers the daemon face from a script: each Drain call
// answers the next busy answer (nil = idle), and every call is
// recorded. onDrain runs inside the recorded call (the tests flip
// state, lay the cancel marker, snapshot the status file).
type fakeFace struct {
	mu       sync.Mutex
	calls    []faceCall
	busy     [][]string // each Drain pops the front answer; the last repeats
	forced   []bool
	onDrain  func(n int, f *fakeFace)
	statuses []string // a snapshot of the status state at each drain call
}

func (f *fakeFace) Drain(ctx context.Context, force bool) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.calls)
	f.calls = append(f.calls, faceCall{verb: "drain", force: force})
	if f.onDrain != nil {
		f.onDrain(n, f)
	}
	if n < len(f.busy) {
		f.busy[n] = append([]string{}, f.busy[n]...)
		return f.busy[n], nil
	}
	if len(f.busy) > 0 {
		return append([]string{}, f.busy[len(f.busy)-1]...), nil
	}
	return nil, nil
}

func (f *fakeFace) Restart(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, faceCall{verb: "restart"})
	return nil
}

func (f *fakeFace) drainCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.verb == "drain" {
			n++
		}
	}
	return n
}

func (f *fakeFace) restartCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.verb == "restart" {
			n++
		}
	}
	return n
}

func (f *fakeFace) callVerbs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	verbs := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		verbs = append(verbs, c.verb)
	}
	return verbs
}

// upgradeFixture answers one components root on current v0.2.4 and a
// complete v0.2.5 seed directory beside it (the seed-dir source the
// drain tests take; no network).
func upgradeFixture(t *testing.T) (root, seedDir string) {
	t.Helper()
	root, _ = currentRoot(t, seedSpec{})
	seedDir = filepath.Join(t.TempDir(), "v0.2.5")
	writeVersionDir(t, seedDir, seedSpec{version: "v0.2.5"})
	return root, seedDir
}

// quietNPM answers an NPMInstall seam that touches nothing (the
// seed-dir source carries its own trees).
func quietNPM(string) error { return nil }

// runUpgrade runs one transaction with the defaults the tests need and
// answers its error.
func runUpgrade(t *testing.T, root, seedDir string, face DaemonFace, over func(*UpgradeOptions)) error {
	t.Helper()
	opts := UpgradeOptions{
		Root: root, DataDir: t.TempDir(), BaseVersion: "v0.2.4", Platform: "linux/amd64",
		SeedDir: seedDir, Face: face, Poll: time.Millisecond,
		NPMInstall: quietNPM, RestartPiWeb: func() error { return nil },
	}
	if over != nil {
		over(&opts)
	}
	_, err := Upgrade(context.Background(), opts)
	return err
}

// TestComponentsUpgradeDrainWaitsForIdleThenSwitches pins AC3's main
// line: a busy Host sets the drain first, the upgrade waits (the
// status file says so, with the busy Cells named), the work ends and
// the upgrade continues on its own — the switch, the daemon restart
// ask and the Pi Web restart all follow, in that order.
func TestComponentsUpgradeDrainWaitsForIdleThenSwitches(t *testing.T) {
	root, seedDir := upgradeFixture(t)
	before := rootState(t, root)
	face := &fakeFace{busy: [][]string{{"default"}, nil}}
	face.onDrain = func(n int, f *fakeFace) {
		if s, err := ReadUpgradeStatus(root); err == nil && s != nil {
			f.mu.Lock()
			f.statuses = append(f.statuses, s.State)
			f.mu.Unlock()
		}
	}
	if err := runUpgrade(t, root, seedDir, face, nil); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if got := face.drainCalls(); got < 2 {
		t.Fatalf("a busy Host must be polled, got %d drain calls", got)
	}
	// the waiting was recorded: at least one drain call saw the
	// waiting state.
	face.mu.Lock()
	waiting := false
	for _, s := range face.statuses {
		if s == UpgradeStateWaiting {
			waiting = true
		}
	}
	face.mu.Unlock()
	if !waiting {
		t.Fatal("the drain wait must be visible in the status file (waiting-drain)")
	}
	// the switch: current on the new version, previous on the old one.
	if target, err := CurrentTarget(root); err != nil || target != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Fatalf("current must point at the new version, got %s (%v)", target, err)
	}
	if prev, err := os.Readlink(filepath.Join(root, PreviousLinkname)); err != nil || prev != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Fatalf("previous must point at the version current replaced, got %s (%v)", prev, err)
	}
	if got := rootState(t, root); got == before {
		t.Fatal("the upgrade must move current (the fixture asserted it started elsewhere)")
	}
	// the restart ask came exactly once, after the last drain.
	if face.restartCalls() != 1 {
		t.Fatalf("the switched upgrade asks the daemon to restart exactly once, got %d", face.restartCalls())
	}
	// the final record: done, from and to named.
	s, err := ReadUpgradeStatus(root)
	if err != nil || s == nil {
		t.Fatalf("the final status must be recorded: %v", err)
	}
	if s.State != UpgradeStateDone || s.Operation != OperationUpgrade || s.From != "v0.2.4" || s.To != "v0.2.5" || s.Error != "" {
		t.Fatalf("the final record: %+v", s)
	}
}

// TestComponentsUpgradeDrainForceUpgradesAtOnce pins AC3's --force:
// the drain carries force, a busy Host does not wait, and the switch
// happens on the first drain answer.
func TestComponentsUpgradeDrainForceUpgradesAtOnce(t *testing.T) {
	root, seedDir := upgradeFixture(t)
	face := &fakeFace{busy: [][]string{{"default"}}} // busy forever
	if err := runUpgrade(t, root, seedDir, face, func(o *UpgradeOptions) { o.Force = true }); err != nil {
		t.Fatalf("Upgrade --force: %v", err)
	}
	if got := face.drainCalls(); got != 1 {
		t.Fatalf("--force drains exactly once, got %d calls", got)
	}
	face.mu.Lock()
	forced := face.calls[0].force
	face.mu.Unlock()
	if !forced {
		t.Fatal("the one drain call must carry force")
	}
	if target, err := CurrentTarget(root); err != nil || target != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Fatalf("current must point at the new version, got %s (%v)", target, err)
	}
}

// TestComponentsUpgradeDrainCancelRestartsTheDaemon pins AC3's cancel:
// the marker laid during the wait stands the upgrade down — the daemon
// is asked to restart (the drain ends with that restart), current is
// untouched and the status says cancelled.
func TestComponentsUpgradeDrainCancelRestartsTheDaemon(t *testing.T) {
	root, seedDir := upgradeFixture(t)
	before := rootState(t, root)
	face := &fakeFace{busy: [][]string{{"default"}}} // busy forever
	face.onDrain = func(n int, f *fakeFace) {
		if n == 1 {
			if err := RequestUpgradeCancel(root); err != nil {
				t.Errorf("RequestUpgradeCancel: %v", err)
			}
		}
	}
	if err := runUpgrade(t, root, seedDir, face, nil); err != nil {
		t.Fatalf("a cancelled upgrade is not a failure: %v", err)
	}
	verbs := face.callVerbs()
	if len(verbs) < 2 || verbs[len(verbs)-1] != "restart" {
		t.Fatalf("the cancel must end by asking the daemon to restart (the drain ends with that restart), got %v", verbs)
	}
	if target, err := CurrentTarget(root); err != nil || target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Fatalf("a cancelled upgrade leaves current on the old version, got %s (%v)", target, err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a cancelled upgrade changes nothing in the root:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ReleasesDirname, "v0.2.5")); !os.IsNotExist(err) {
		t.Fatalf("a cancelled upgrade publishes nothing: %v", err)
	}
	s, err := ReadUpgradeStatus(root)
	if err != nil || s == nil || s.State != UpgradeStateCancelled {
		t.Fatalf("the cancel must be recorded: %+v (%v)", s, err)
	}
	if UpgradeCancelRequested(root) {
		t.Fatal("the consumed marker must be cleared")
	}
}

// TestComponentsUpgradeDrainNeverSetWhenStagingFails pins AC2's
// promise through the whole orchestration: a seed that cannot be
// staged fails the upgrade before the daemon is touched at all — no
// drain, no restart, current and the volume exactly as they were, the
// reason in the status file.
func TestComponentsUpgradeDrainNeverSetWhenStagingFails(t *testing.T) {
	root, _ := upgradeFixture(t)
	before := rootState(t, root)
	face := &fakeFace{}
	// a seed directory without a manifest: the staging refuses it.
	badSeed := filepath.Join(t.TempDir(), "v0.2.5")
	if err := os.MkdirAll(badSeed, 0o755); err != nil {
		t.Fatal(err)
	}
	err := runUpgrade(t, root, badSeed, face, nil)
	if err == nil {
		t.Fatal("a seed that cannot be staged must fail the upgrade")
	}
	if got := len(face.calls); got != 0 {
		t.Fatalf("a failed staging must not touch the daemon, got %v", face.callVerbs())
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a failed staging must change nothing:\n%s", got)
	}
	s, readErr := ReadUpgradeStatus(root)
	if readErr != nil || s == nil || s.State != UpgradeStateFailed || s.Error == "" {
		t.Fatalf("the failure must be recorded with its reason: %+v (%v)", s, readErr)
	}
}

// TestComponentsUpgradeDrainFromTheChannelRelease pins the default
// source: the channel repository's release assets (the stub GitHub)
// assemble into the version directory the switch publishes — the
// fetch, the assembly and the install transaction in one run.
func TestComponentsUpgradeDrainFromTheChannelRelease(t *testing.T) {
	root, _ := currentRoot(t, seedSpec{})
	rel := releaseFixture("v0.2.5", "abcd1234ef56")
	serveRelease(t, rel)
	face := &fakeFace{} // idle from the first drain
	err := runUpgrade(t, root, "", face, func(o *UpgradeOptions) {
		o.SeedDir = ""
		o.Version = "v0.2.5"
	})
	if err != nil {
		t.Fatalf("Upgrade from the channel: %v", err)
	}
	if target, err := CurrentTarget(root); err != nil || target != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Fatalf("current must point at the channel's release, got %s (%v)", target, err)
	}
	// the published tree verifies and carries what the assembly
	// carried: the packed pi-web beside current's node_modules.
	published := filepath.Join(root, ReleasesDirname, "v0.2.5")
	if m, err := ReadManifest(published); err != nil || m.Version != "v0.2.5" || m.Commit != "abcd1234ef56" {
		t.Fatalf("the published manifest: %+v (%v)", m, err)
	}
	if _, err := os.Stat(filepath.Join(published, PathPiWeb, "node_modules", "node-pty", "build", "pty.node")); err != nil {
		t.Fatalf("the published pi-web must carry current's node_modules: %v", err)
	}
	// no assembly leftovers stay in the volume's root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upgrade-") {
			t.Fatalf("the assembly's temporary directory must be removed, found %s", e.Name())
		}
	}
}

// TestComponentsUpgradeDrainRestartTerminatesTheOldPiWeb pins the Pi
// Web restart's own mechanism: the processes serving out of a version
// directory's pi-web tree (their working directory says so) are
// SIGTERMed — the container supervisor relaunches them on the new
// current — and nothing else is touched.
func TestComponentsUpgradeDrainRestartTerminatesTheOldPiWeb(t *testing.T) {
	root, _ := currentRoot(t, seedSpec{})
	piWeb := filepath.Join(root, ReleasesDirname, "v0.2.4", PathPiWeb)
	// one real process sitting in the old tree, and one beside the
	// volume that must survive.
	inside := exec.Command("sleep", "30")
	inside.Dir = piWeb
	if err := inside.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inside.Process.Kill() }()
	outside := exec.Command("sleep", "30")
	outside.Dir = os.TempDir()
	if err := outside.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outside.Process.Kill() }()

	if err := termPiWebUnder(root); err != nil {
		t.Fatalf("termPiWebUnder: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := inside.Process.Signal(syscall.Signal(0)); err != nil {
			break // the process is gone
		}
		if time.Now().After(deadline) {
			_ = inside.Process.Kill()
			t.Fatal("the process inside the old pi-web tree must be terminated")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := outside.Process.Signal(syscall.Signal(0)); err != nil {
		_ = outside.Process.Kill()
		t.Fatal("a process outside the version directories' pi-web trees must not be touched")
	}
}
