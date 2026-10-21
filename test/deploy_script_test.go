package test

// Safety-rail tests for .polyforge/deploy.sh: mandatory --confirm, the
// server-side flock, and the automatic rollback with distinct exit codes.
// Same fake environment as deploy_image_test.go (stub ssh/docker/gh/scp on PATH);
// the "server" is a local temp tree, so the lock is a real flock on a real file.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (d *deployWorld) composeText() string {
	b, _ := os.ReadFile(filepath.Join(d.w, "srv", "compose", "docker-compose.yml"))
	return string(b)
}

// assertNothingMutated: no server mutation, compose untouched, no lock/backup work.
func (d *deployWorld) assertNothingMutated(oldVer string) {
	d.t.Helper()
	if m := d.serverDockerMutations(); len(m) != 0 {
		d.t.Errorf("server docker was changed: %v", m)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:"+oldVer) {
		d.t.Errorf("compose file changed:\n%s", d.composeText())
	}
	sshLog := d.sshLog()
	for _, bad := range []string{"flock", "docker cp", "rsync", "sed -i"} {
		if strings.Contains(sshLog, bad) {
			d.t.Errorf("mutating ssh call %q recorded:\n%s", bad, sshLog)
		}
	}
	if _, err := os.Stat(filepath.Join(d.w, "backups")); err == nil {
		d.t.Errorf("a local backup dir was created")
	}
}

// sshLog is the ssh call log without the read-only server tool preflight line
// (it names flock/rsync only to check that they exist).
func (d *deployWorld) sshLog() string {
	var keep []string
	for _, ln := range strings.Split(d.logOf("log-ssh"), "\n") {
		if !strings.HasPrefix(ln, "ssh for c in docker python3 rsync flock curl;") {
			keep = append(keep, ln)
		}
	}
	return strings.Join(keep, "\n")
}

func (d *deployWorld) lockFree() bool {
	return exec.Command("flock", "-n", filepath.Join(d.w, "srv", "deploy.lock"), "true").Run() == nil
}

func newReadyWorld(t *testing.T) *deployWorld {
	d := newDeployWorld(t, "v2.2.1")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	return d
}

func TestDeployConfirmMissingOrMismatchRefusedBeforeAnyWork(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":      {"v2.3.0"},
		"mismatch":     {"v2.3.0", "--confirm", "v2.2.9"},
		"dry-mismatch": {"v2.3.0", "--dry-run", "--confirm", "v9.9.9"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			out, err := d.run("deploy\n", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") || !strings.Contains(out, "--confirm") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker"); got != "" {
				t.Errorf("network/tool work happened before the refusal:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
		})
	}
}

func TestDeployDryRunNeedsNoConfirmAndTakesNoLock(t *testing.T) {
	d := newReadyWorld(t)
	out, err := d.run("", "v2.3.0", "--dry-run", "--confirm", "v2.3.0")
	if err != nil || !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
		t.Fatalf("dry-run with a matching --confirm failed: %v\n%s", err, out)
	}
	d.assertNothingMutated("v2.2.1")
	if strings.Contains(out, "deployed=") {
		t.Errorf("dry-run must not print a deployed= line\n%s", out)
	}
}

func TestDeployLockHeldFailsFastNothingMutated(t *testing.T) {
	d := newReadyWorld(t)
	lock := filepath.Join(d.w, "srv", "deploy.lock")
	// Blocking flock, not -n: lockFree's probe takes the lock for an
	// instant, and a non-blocking holder that starts in that instant would
	// exit at once and never hold it (the test then failed "could not hold").
	holder := exec.Command("flock", lock, "sleep", "120")
	ownGroup(holder) // flock forks `sleep`: killing flock alone orphans it
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { killGroup(holder); _ = holder.Wait() }()
	for i := 0; i < 50 && d.lockFree(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if d.lockFree() {
		t.Fatal("could not hold the fake lock")
	}
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 3 || !strings.Contains(out, "LOCK HELD") {
		t.Fatalf("want exit 3 + LOCK HELD, got %v\n%s", err, out)
	}
	if strings.Contains(out, "deployed=") {
		t.Errorf("unexpected deployed= line\n%s", out)
	}
	if m := d.serverDockerMutations(); len(m) != 0 {
		t.Errorf("server docker was changed: %v", m)
	}
	if strings.Contains(d.sshLog(), "docker cp") || strings.Contains(d.sshLog(), "rsync") || !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("mutation recorded despite the held lock:\n%s", d.logOf("log-ssh"))
	}
	if _, err := os.Stat(filepath.Join(d.w, "backups")); err == nil {
		t.Errorf("a local backup dir was created")
	}
}

func TestDeployHappyPathHoldsAndReleasesLock(t *testing.T) {
	d := newReadyWorld(t)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := lines[len(lines)-1]; last != "deployed=v2.3.0 previous=v2.2.1" {
		t.Errorf("last line = %q", last)
	}
	if !strings.Contains(d.logOf("log-ssh"), "flock -n") {
		t.Errorf("no lock taken:\n%s", d.logOf("log-ssh"))
	}
	if !d.lockFree() {
		t.Errorf("the lock is still held after the script exited")
	}
	// the SIGKILL deadline outlasts aihub's own 10s drain (repo review
	// 2026-10-16 D-15), in the compose file and in the stop itself
	if !strings.Contains(d.composeText(), "    stop_grace_period: 15s\n") {
		t.Errorf("compose stop_grace_period is not 15s:\n%s", d.composeText())
	}
	// a normal drain-length stop must not trip the downtime warning
	if !strings.Contains(out, "warn > 20s") {
		t.Errorf("the downtime warning threshold is not 20s (above the 15s drain):\n%s", out)
	}
	if !strings.Contains(d.logOf("log-docker"), "stop -t 15 ") {
		t.Errorf("the switch-window stop does not wait 15s:\n%s", d.logOf("log-docker"))
	}
	// the off-host copy is 0700 (repo review 2026-10-16 V-12)
	baks, _ := filepath.Glob(filepath.Join(d.w, "backups", "aihub-backup-*"))
	for _, p := range append(baks, filepath.Join(d.w, "backups")) {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v (%v), want 0700", p, fi.Mode().Perm(), err)
		}
	}
	if len(baks) != 1 {
		t.Errorf("local backups = %v, want one", baks)
	}
	// the repo's reference compose file says the same
	if b, err := os.ReadFile(filepath.Join(v1Root(t), "deploy", "docker-compose.yml")); err != nil || !strings.Contains(string(b), "stop_grace_period: 15s") {
		t.Errorf("deploy/docker-compose.yml stop_grace_period is not 15s (%v)", err)
	}
}

func TestDeployVerificationFailureAutoRollsBack(t *testing.T) {
	d := newReadyWorld(t)
	d.env = append(d.env, "FAKE_BAD_IMG=polyforge/aihub:v2.3.0") // new image: container never reports healthy
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10, got %v\n%s", err, out)
	}
	for _, want := range []string{"ROLLED BACK", "AUTOMATIC ROLLBACK to v2.2.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := lines[len(lines)-1]; last != "deployed=ROLLED_BACK previous=v2.2.1 attempted=v2.3.0" {
		t.Errorf("last line = %q", last)
	}
	if n := strings.Count(d.logOf("log-docker"), " up -d --no-deps aihub"); n != 2 {
		t.Errorf("want 2 compose ups (new, rollback), got %d\n%s", n, d.logOf("log-docker"))
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") || strings.Contains(d.composeText(), "aihub:v2.3.0") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
	if !strings.Contains(d.logOf("log-ssh"), "sed -i 's|image: polyforge/aihub:v2.3.0|image: polyforge/aihub:v2.2.1|'") {
		t.Errorf("no compose edit back to the old image:\n%s", d.logOf("log-ssh"))
	}
	if c, _ := os.ReadFile(filepath.Join(d.w, "dk-server", "container")); strings.TrimSpace(string(c)) != "polyforge/aihub:v2.2.1" {
		t.Errorf("container is not on the old image: %q", c)
	}
	if !d.lockFree() {
		t.Errorf("the lock is still held after the script exited")
	}
}

func TestDeploySwapUpFailureAutoRollsBack(t *testing.T) {
	d := newReadyWorld(t)
	d.env = append(d.env, "FAKE_FAIL_UP_IMG=polyforge/aihub:v2.3.0") // compose up of the new image fails
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 || !strings.Contains(out, "ROLLED BACK") {
		t.Fatalf("want exit 10 + ROLLED BACK, got %v\n%s", err, out)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
}

func TestDeployRollbackFailureIsLoudExit11(t *testing.T) {
	d := newReadyWorld(t)
	d.env = append(d.env, "FAKE_BAD_IMG=polyforge/aihub:v2.3.0", "FAKE_FAIL_UP_IMG=polyforge/aihub:v2.2.1")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 11 {
		t.Fatalf("want exit 11, got %v\n%s", err, out)
	}
	for _, want := range []string{"ROLLBACK FAILED", "ACT BY HAND", "docker compose -f", "docker stop aihub", "deployed=UNKNOWN previous=v2.2.1 attempted=v2.3.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "deployed=ROLLED_BACK") {
		t.Errorf("must not claim a rollback\n%s", out)
	}
}

// ---- review fixes: lock monitoring, server tools, log scan, dry-run, cleanup

func TestDeployLockSessionDyingBeforeSwapDiesNothingSwapped(t *testing.T) {
	d := newReadyWorld(t)
	// the lock ssh session dies while the NEW image is being loaded (step 6)
	d.env = append(d.env, "FAKE_KILL_LOCK_ON_LOAD=ghcr.io/silgrid/aihub:v2.3.0")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 || !strings.Contains(out, "deploy lock session died") {
		t.Fatalf("want exit 1 + lock session died, got %v\n%s", err, out)
	}
	if strings.Contains(out, "deployed=") {
		t.Errorf("unexpected deployed= line\n%s", out)
	}
	if n := strings.Count(d.logOf("log-docker"), " up -d --no-deps aihub"); n != 0 {
		t.Errorf("a compose up ran although the lock was gone (%d)\n%s", n, d.logOf("log-docker"))
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") || strings.Contains(d.composeText(), "aihub:v2.3.0") {
		t.Errorf("compose was edited without the lock:\n%s", d.composeText())
	}
	if c, _ := os.ReadFile(filepath.Join(d.w, "dk-server", "container")); strings.Contains(string(c), "v2.3.0") {
		t.Errorf("container was recreated on the new image: %q", c)
	}
}

func TestDeployPreflightRequiresServerTools(t *testing.T) {
	for _, tool := range []string{"rsync", "flock", "python3"} {
		t.Run(tool, func(t *testing.T) {
			d := newReadyWorld(t)
			d.env = append(d.env, "FAKE_MISSING_TOOL="+tool)
			out, err := d.run("", "v2.3.0", "--dry-run") // read-only mode too
			if exitCode(err) != 1 || !strings.Contains(out, "missing on fakehost: "+tool) {
				t.Fatalf("want exit 1 + missing %s, got %v\n%s", tool, err, out)
			}
			d.assertNothingMutated("v2.2.1")
		})
	}
}

func TestDeployLogScanOnlyGoRuntimeSignatures(t *testing.T) {
	logs := func(t *testing.T, d *deployWorld, body string) {
		p := filepath.Join(d.w, "service.log")
		wf(t, p, body, 0o644)
		d.env = append(d.env, "FAKE_LOGS_FILE="+p)
	}
	t.Run("git proxy fatal is not a crash", func(t *testing.T) {
		d := newReadyWorld(t)
		logs(t, d, "2026/10/13 git proxy: fatal: the remote end hung up unexpectedly\nfatal: repository not found\nerror: some warning\n")
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if err != nil || !strings.Contains(out, "deployed=v2.3.0 previous=v2.2.1") {
			t.Fatalf("a git fatal: line must not fail the deploy: %v\n%s", err, out)
		}
		if strings.Contains(out, "ROLLED BACK") {
			t.Errorf("rolled back on a git fatal: line\n%s", out)
		}
	})
	for name, line := range map[string]string{
		"panic":       "panic: runtime error: index out of range",
		"fatal error": "fatal error: all goroutines are asleep - deadlock!",
		"goroutine":   "goroutine 17 [running]:",
	} {
		t.Run(name+" rolls back", func(t *testing.T) {
			d := newReadyWorld(t)
			logs(t, d, "starting\n"+line+"\n")
			out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
			if exitCode(err) != 10 || !strings.Contains(out, "ROLLED BACK") {
				t.Fatalf("want exit 10 + ROLLED BACK, got %v\n%s", err, out)
			}
		})
	}
}

func TestDeployDryRunComposeEditFailureIsFatal(t *testing.T) {
	d := newReadyWorld(t)
	// the compose file validates but has no "  aihub:" service block to edit
	wf(t, filepath.Join(d.w, "srv", "compose", "docker-compose.yml"), "services:\n  web:\n    image: busybox\n", 0o644)
	out, err := d.run("", "v2.3.0", "--dry-run")
	if exitCode(err) != 1 || !strings.Contains(out, "cannot compute the compose edit") {
		t.Fatalf("want exit 1 + compose edit failure, got %v\n%s", err, out)
	}
	if strings.Contains(out, "Nothing was changed.") && strings.Contains(out, "dry-run: steps 0-1 ran") {
		t.Errorf("the dry-run claimed success\n%s", out)
	}
}

func TestDeployTarSpaceCheckedInPreflight(t *testing.T) {
	d := newReadyWorld(t)
	d.env = append(d.env, "FAKE_IMAGE_SIZE=999999999999999")
	out, err := d.run("", "v2.3.0", "--dry-run")
	if exitCode(err) != 1 || !strings.Contains(out, "not enough free space in") || !strings.Contains(out, "docker save tar") {
		t.Fatalf("want exit 1 + tar space refusal, got %v\n%s", err, out)
	}
	d.assertNothingMutated("v2.2.1")
}

func TestDeployUnexpectedExitAfterSwapRollsBackAndCleansUp(t *testing.T) {
	d := newReadyWorld(t)
	d.env = append(d.env, "FAKE_TERM_ON_LOGS=1") // SIGTERM reaches the script in step 7
	cmd := exec.Command("sh", filepath.Join("..", ".polyforge", "deploy.sh"), "v2.3.0", "--confirm", "v2.3.0")
	cmd.Env = d.env
	var b strings.Builder
	cmd.Stdout, cmd.Stderr = &b, &b
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wf(t, filepath.Join(d.w, "deploypid"), fmt.Sprint(cmd.Process.Pid), 0o644)
	err := cmd.Wait()
	out := b.String()
	if exitCode(err) != 10 || !strings.Contains(out, "unexpected exit") || !strings.Contains(out, "ROLLED BACK") {
		t.Fatalf("want exit 10 + rollback after the signal, got %v\n%s", err, out)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose not rolled back:\n%s", d.composeText())
	}
	if left, _ := filepath.Glob(filepath.Join(d.w, "pf-deploy.*")); len(left) != 0 {
		t.Errorf("the work dir leaked: %v", left)
	}
	if !d.lockFree() {
		t.Errorf("the lock is still held after the script exited")
	}
}

// seedBackups creates backup-shaped dirs (and decoys) under root; a
// verified one carries the .verified marker a passed step 5 writes.
func seedBackups(t *testing.T, root string, verified bool, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n, "memory"), 0o755); err != nil {
			t.Fatal(err)
		}
		if verified {
			wf(t, filepath.Join(root, n, ".verified"), "", 0o644)
		}
	}
}

// newBackupDirs answers the backup dirs of this world's running version
// (aihub-backup-v2.2.1-<TS>) under root.
func newBackupDirs(t *testing.T, root string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(root, "aihub-backup-v2.2.1-*"))
	return m
}

func dirNames(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// retentionWorld seeds four old server backups, three old local ones and
// decoys that must never be touched (wrong name shape, a file, the old
// binary).
func retentionWorld(t *testing.T) (*deployWorld, string, string) {
	d := newReadyWorld(t)
	srv := filepath.Join(d.w, "srv", "data")
	local := filepath.Join(d.w, "backups")
	seedBackups(t, srv, true,
		"aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z",
		"aihub-backup-v2.2.0-20260301T000000Z", "aihub-backup-v1.9.0-20260401T000000Z",
		"aihub-backup-notes", "aihub-backup-v2.0.0-2026", "aihub-bin-v2.0.0-20260101T000000Z")
	// newest of all, but unverified (a failed run, or pre-marker): never
	// counted, never pruned, reported
	seedBackups(t, srv, false, "aihub-backup-v2.2.5-20260501T000000Z")
	wf(t, filepath.Join(srv, "aihub-backup-v0.1.0-20250101T000000Z"), "a file, not a backup dir\n", 0o644)
	seedBackups(t, local, true, "aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z",
		"aihub-backup-v2.2.0-20260301T000000Z", "keep-me")
	// a one-time bootstrap key file in the data dir: never copied into a backup
	wf(t, filepath.Join(srv, "aihub", "bootstrap-admin.key"), "k_secret\n", 0o600)
	return d, srv, local
}

// TestDeployBackupRetention (owner decision on repo review 2026-10-16
// V-12): after this run's backup verified, the newest 3 aihub-backup-*
// dirs (this one included, age = the timestamp suffix) are kept on the
// server and locally; older ones are pruned and named; nothing that does
// not match the exact name pattern is touched.
func TestDeployBackupRetention(t *testing.T) {
	d, srv, local := retentionWorld(t)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	srvNames := strings.Join(dirNames(t, srv), " ")
	for _, gone := range []string{"aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z"} {
		if strings.Contains(srvNames, gone) {
			t.Errorf("server still has %s: %s", gone, srvNames)
		}
	}
	for _, kept := range []string{"aihub-backup-v2.2.0-20260301T000000Z", "aihub-backup-v1.9.0-20260401T000000Z",
		"aihub-backup-v2.2.1-", "aihub-backup-notes", "aihub-backup-v2.0.0-2026", "aihub-bin-v2.0.0-20260101T000000Z",
		"aihub-backup-v0.1.0-20250101T000000Z", "aihub"} {
		if !strings.Contains(srvNames, kept) {
			t.Errorf("server lost %s: %s", kept, srvNames)
		}
	}
	if !strings.Contains(out, "retention: pruned on the server ("+srv+"): aihub-backup-v2.1.0-20260201T000000Z aihub-backup-v2.0.0-20260101T000000Z") {
		t.Errorf("the server prune is not reported:\n%s", out)
	}
	localNames := strings.Join(dirNames(t, local), " ")
	if strings.Contains(localNames, "aihub-backup-v2.0.0-20260101T000000Z") ||
		!strings.Contains(localNames, "aihub-backup-v2.2.0-20260301T000000Z") || !strings.Contains(localNames, "keep-me") ||
		!strings.Contains(localNames, "aihub-backup-v2.2.1-") {
		t.Errorf("local backups after retention: %s", localNames)
	}
	if !strings.Contains(out, "retention: pruned locally ("+local+"): aihub-backup-v2.0.0-20260101T000000Z") {
		t.Errorf("the local prune is not reported:\n%s", out)
	}
	if !strings.Contains(srvNames, "aihub-backup-v2.2.5-20260501T000000Z") ||
		!strings.Contains(out, "unverified, kept; remove by hand: server ("+srv+"): aihub-backup-v2.2.5-20260501T000000Z") {
		t.Errorf("the unverified server backup is not kept and reported:\n%s", out)
	}
	// this run's backup is marked on both sides and carries no bootstrap key
	for _, root := range []string{srv, local} {
		nb := newBackupDirs(t, root)
		if len(nb) != 1 {
			t.Fatalf("this run's backup under %s: %v", root, nb)
		}
		if _, err := os.Stat(filepath.Join(nb[0], ".verified")); err != nil {
			t.Errorf("%s is not marked verified: %v", nb[0], err)
		}
		if _, err := os.Stat(filepath.Join(nb[0], "bootstrap-admin.key")); !os.IsNotExist(err) {
			t.Errorf("%s carries bootstrap-admin.key (%v)", nb[0], err)
		}
	}
}

// TestDeployBackupRetentionFailedRunsNotCounted: backups of failed runs
// carry no marker, so two failed runs followed by a success push no
// verified backup out — all older verified backups stay — and the failed
// runs' dirs are kept and reported as unverified.
func TestDeployBackupRetentionFailedRunsNotCounted(t *testing.T) {
	d := newReadyWorld(t)
	srv := filepath.Join(d.w, "srv", "data")
	seedBackups(t, srv, true, "aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z")
	orphan := filepath.Join(srv, "aihub", "memory", "p", "mem_x", "memory.md")
	for i := 0; i < 2; i++ {
		wf(t, orphan, "orphan\n", 0o644) // step 4 never verifies the backup
		out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
		if exitCode(err) != 1 {
			t.Fatalf("run %d: want exit 1 at step 4, got %v\n%s", i, err, out)
		}
		time.Sleep(1100 * time.Millisecond) // a distinct backup timestamp per run
	}
	failed := newBackupDirs(t, srv)
	if len(failed) != 2 {
		t.Fatalf("failed runs left %v, want two backup dirs", failed)
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	names := strings.Join(dirNames(t, srv), " ")
	for _, kept := range append([]string{"aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z"}, failed...) {
		if !strings.Contains(names, filepath.Base(kept)) {
			t.Errorf("lost %s: %s", filepath.Base(kept), names)
		}
	}
	if strings.Contains(out, "retention: pruned on the server") {
		t.Errorf("a prune ran although only 3 verified backups exist:\n%s", out)
	}
	if !strings.Contains(out, "unverified, kept; remove by hand: server") {
		t.Errorf("the failed runs' backups are not reported unverified:\n%s", out)
	}
	for _, f := range failed {
		if _, err := os.Stat(filepath.Join(f, ".verified")); !os.IsNotExist(err) {
			t.Errorf("a failed run's backup %s is marked verified (%v)", f, err)
		}
	}
}

// TestDeployBackupRetentionDryRunAndUnverified: a dry run prints what
// would be pruned and deletes nothing; a run whose backup fails
// verification (step 4 here) prunes nothing.
func TestDeployBackupRetentionDryRunAndUnverified(t *testing.T) {
	d, srv, local := retentionWorld(t)
	before := strings.Join(dirNames(t, srv), " ") + " | " + strings.Join(dirNames(t, local), " ")
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"server " + srv + ": aihub-backup-v2.1.0-20260201T000000Z aihub-backup-v2.0.0-20260101T000000Z",
		"local " + local + ": aihub-backup-v2.0.0-20260101T000000Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run plan lacks %q:\n%s", want, out)
		}
	}
	if after := strings.Join(dirNames(t, srv), " ") + " | " + strings.Join(dirNames(t, local), " "); after != before {
		t.Errorf("dry-run changed the backup dirs:\n%s\n%s", before, after)
	}

	// a memory file without its row: step 4 never verifies the backup
	wf(t, filepath.Join(srv, "aihub", "memory", "p", "mem_x", "memory.md"), "orphan\n", 0o644)
	out, err = d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 {
		t.Fatalf("want exit 1 at step 4, got %v\n%s", err, out)
	}
	srvNames := strings.Join(dirNames(t, srv), " ")
	for _, n := range []string{"aihub-backup-v2.0.0-20260101T000000Z", "aihub-backup-v2.1.0-20260201T000000Z"} {
		if !strings.Contains(srvNames, n) {
			t.Errorf("an unverified run pruned %s: %s", n, srvNames)
		}
	}
	if strings.Contains(out, "retention: pruned") {
		t.Errorf("an unverified run reported a prune:\n%s", out)
	}
}

// TestDeployRefusesNonPlainPaths: AIHUB_DATA / BACKUP_ROOT travel through
// ssh's re-joined remote command line, so a blank or quote is a usage
// error before any work.
func TestDeployRefusesNonPlainPaths(t *testing.T) {
	for _, kv := range []string{"AIHUB_DATA=/data/ai hub", "BACKUP_ROOT=/tmp/it's"} {
		d := newReadyWorld(t)
		d.env = append(d.env, kv)
		out, err := d.run("", "v2.3.0", "--dry-run")
		if exitCode(err) != 2 || !strings.Contains(out, "must be plain paths") {
			t.Errorf("%s: want exit 2 + plain-path refusal, got %v\n%s", kv, err, out)
		}
		if strings.Contains(d.sshLog(), "rsync") {
			t.Errorf("%s: work ran before the refusal", kv)
		}
	}
}

// dbOnlyMemories gives the world's server database the memories columns
// the step-4 check reads (content, status, authority_path) with one filled
// row, plus the given rows without content: "active" or "redacted", each
// with its memory.md in the data dir when withFile.
func (d *deployWorld) dbOnlyMemories(rows ...nullRow) {
	d.t.Helper()
	py := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
for col in ("content text", "status text", "authority_path text"): c.execute("alter table memories add column " + col)
c.execute("insert into memories(id, content, status, authority_path) values (1, 'doc', 'active', 'memory/p/mem_1')")
for i, st in enumerate(sys.argv[2:]): c.execute("insert into memories(id, status, authority_path) values (?, ?, ?)", (i + 2, st, "memory/p/mem_%d" % (i + 2)))
c.commit()`
	data := filepath.Join(d.w, "srv", "data", "aihub")
	args := []string{"-I", "-c", py, filepath.Join(data, "aihub.db")}
	for i, r := range rows {
		args = append(args, r.status)
		if r.withFile {
			wf(d.t, filepath.Join(data, "memory", "p", fmt.Sprintf("mem_%d", i+2), "memory.md"), "doc\n", 0o644)
		}
	}
	if out, err := exec.Command("python3", args...).CombinedOutput(); err != nil {
		d.t.Fatalf("memories fixture: %v\n%s", err, out)
	}
}

type nullRow struct {
	status   string
	withFile bool
}

// TestDeployConsistencyDBOnlyMemories: on a database with
// memories.content, step 4 no longer counts files (a stray file does not
// block); rows without content pass when the backup holds their file
// (a rollback to an older aihub wrote them; the new one fills them) or
// when they are redacted (never served); an active row with neither stops
// the deploy before anything changed, naming the row.
func TestDeployConsistencyDBOnlyMemories(t *testing.T) {
	d := newReadyWorld(t)
	d.dbOnlyMemories()
	wf(t, filepath.Join(d.w, "srv", "data", "aihub", "memory", "p", "mem_x", "memory.md"), "left over\n", 0o644)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "snapshot memories rows without content: 0") || strings.Contains(out, "memory.md files in the backup:") {
		t.Errorf("step 4 did not check the content column:\n%s", out)
	}

	d = newReadyWorld(t)
	d.dbOnlyMemories(nullRow{"active", true}, nullRow{"redacted", false})
	out, err = d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy with fillable / redacted rows failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "every active row without content has its memory.md in the backup") {
		t.Errorf("step 4 did not check the files of rows without content:\n%s", out)
	}

	d = newReadyWorld(t)
	d.dbOnlyMemories(nullRow{"active", true}, nullRow{"active", false})
	out, err = d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 || !strings.Contains(out, "1 active memories rows have neither content nor a memory.md in the backup (3)") {
		t.Fatalf("want exit 1 at step 4 naming row 3, got %v\n%s", err, out)
	}
	if strings.Contains(out, "delete the row") {
		t.Errorf("step 4 advises deleting rows:\n%s", out)
	}
	if strings.Contains(d.logOf("log-docker"), "stop -t") {
		t.Errorf("the service was stopped although step 4 refused:\n%s", d.logOf("log-docker"))
	}
}

// deployRequiredVars are the five variables deploy.sh has no default for
// (polyforge#212: the deployment's topology is operator-owned, not in the repo).
var deployRequiredVars = []string{"AIHUB_HOST", "AIHUB_DATA", "COMPOSE_FILE", "COMPOSE_PROJECT", "DEPLOY_LOCK_FILE"}

// withoutEnv answers env minus every entry setting one of names.
func withoutEnv(env []string, names ...string) []string {
	var keep []string
	for _, kv := range env {
		drop := false
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				drop = true
			}
		}
		if !drop {
			keep = append(keep, kv)
		}
	}
	return keep
}

// envValue answers the value env sets for name ("" if none).
func envValue(env []string, name string) string {
	v := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			v = strings.TrimPrefix(kv, name+"=")
		}
	}
	return v
}

// TestDeployRefusesMissingRequiredVars (polyforge#212): with any of the five
// required variables unset (no env, no deploy.env), a real run AND a dry-run
// are a usage error (exit 2) naming exactly the missing variables - never a
// value - before any ssh / gh / docker work. Before #212 they had built-in
// defaults (the private production topology) and the run went ahead.
func TestDeployRefusesMissingRequiredVars(t *testing.T) {
	cases := map[string]struct {
		args    []string
		missing []string
	}{
		"real-all":        {[]string{"v2.3.0", "--confirm", "v2.3.0"}, deployRequiredVars},
		"dry-all":         {[]string{"v2.3.0", "--dry-run"}, deployRequiredVars},
		"dry-one-missing": {[]string{"v2.3.0", "--dry-run"}, []string{"COMPOSE_PROJECT"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			full := d.env
			d.env = withoutEnv(d.env, c.missing...)
			out, err := d.run("", c.args...)
			if exitCode(err) != 2 {
				t.Fatalf("want exit 2 (usage error) for missing %v, got %v\n%s", c.missing, err, out)
			}
			for _, v := range deployRequiredVars {
				missing := false
				for _, m := range c.missing {
					missing = missing || m == v
				}
				if missing != strings.Contains(out, v) {
					t.Errorf("the refusal must name exactly the missing variables %v (%s named=%v)\n%s", c.missing, v, !missing, out)
				}
				if val := envValue(full, v); !missing && strings.Contains(out, val) {
					t.Errorf("the refusal must not print values (%s=%s)\n%s", v, val, out)
				}
			}
			if !strings.Contains(out, ".config/polyforge/deploy.env") {
				t.Errorf("the refusal must point at the env file\n%s", out)
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker"); got != "" {
				t.Errorf("network/tool work happened before the refusal:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
		})
	}
}

// TestDeployEnvFileReadEnvWins (polyforge#212): deploy.sh reads
// $HOME/.config/polyforge/deploy.env when it exists, and a variable the
// environment already set wins over the file. The file supplies four of the
// five required values and a WRONG COMPOSE_FILE; the environment supplies
// only the right COMPOSE_FILE: the dry-run passes its read-only preflight
// (which checks the compose file on the server) only if both hold.
func TestDeployEnvFileReadEnvWins(t *testing.T) {
	d := newReadyWorld(t)
	var file strings.Builder
	file.WriteString("# operator-owned deploy topology\n")
	for _, v := range deployRequiredVars {
		if v == "COMPOSE_FILE" {
			file.WriteString("COMPOSE_FILE=" + filepath.Join(d.w, "no-such", "docker-compose.yml") + "\n")
			continue
		}
		file.WriteString(v + "=" + envValue(d.env, v) + "\n")
	}
	wf(t, filepath.Join(envValue(d.env, "HOME"), ".config", "polyforge", "deploy.env"), file.String(), 0o600)
	d.env = withoutEnv(d.env, "AIHUB_HOST", "AIHUB_DATA", "COMPOSE_PROJECT", "DEPLOY_LOCK_FILE")
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err != nil || !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
		t.Fatalf("the dry-run must take the four values from deploy.env and COMPOSE_FILE from the environment: %v\n%s", err, out)
	}
	if strings.Contains(out, "no-such") {
		t.Errorf("the file's COMPOSE_FILE was used although the environment set it\n%s", out)
	}
	d.assertNothingMutated("v2.2.1")
}
