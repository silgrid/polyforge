// carries: .polyforge/deploy.sh
package test

// deploy_targets_test.go (polyforge#76): one release work item deploys
// every target of this machine at once. The daemon states every serving
// Cell's facts as PF_CELL_TARGETS lines (profile, server, deploy
// configuration file); every Cell whose deploy configuration file EXISTS
// is one deploy target - a Cell without one (a client only) is not - and
// the work item's own Cell deploys first, the rest by name, each in a
// clean subprocess of this script that reads only its own Cell's
// configuration. With fewer than two targets nothing of this exists: the
// run is exactly polyforge#75's single-target path, and a work item whose
// Cell has no deploy configuration refuses before any work as #75 did.
// The per-target checks (the configuration must point at that Cell's own
// server, the work item's Cell must be a target, the backup roots must
// differ) all run before any network work.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cellTargetsVar answers the daemon's PF_CELL_TARGETS injection for the
// given rows (profile, server, deploy configuration file), one line per
// serving Cell in the registry's order.
func cellTargetsVar(rows ...[3]string) string {
	var lines []string
	for _, r := range rows {
		lines = append(lines, r[0]+"\t"+r[1]+"\t"+r[2])
	}
	return strings.Join(lines, "\n")
}

// secondTarget lays a second fake deploy target (the host fakehost2) with
// its own world (data tree, compose file, deploy lock, backup root) beside
// the world's own fakehost, reached through the ssh stub's per-host
// dispatch (FAKE_W2): its container runs old (the consolidated layout),
// its compose service image lives under IMAGE_REPO=polyforge/aihub2 (so
// the two targets' images and backups never collide), and its deploy
// configuration lands in the Cell public's file deploy-public.env.
func (d *deployWorld) secondTarget(t *testing.T, old string) string {
	t.Helper()
	w2 := t.TempDir()
	sdk := filepath.Join(w2, "dk-server")
	wf(t, filepath.Join(sdk, "img", "aihub-app_aihub"), fmt.Sprintf("sha256:old%s\n%s\n\npf\n", old, old), 0o644)
	wf(t, filepath.Join(sdk, "container"), "aihub-app_aihub\n", 0o644)
	data := filepath.Join(w2, "srv", "data", "aihub")
	for _, sub := range []string{"memory", "git"} {
		if err := os.MkdirAll(filepath.Join(data, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	py := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
for t in "work_items memories projects users api_keys machines".split(): c.execute("create table %s(id integer)"%t)
c.commit()`
	if out, err := exec.Command("python3", "-I", "-c", py, filepath.Join(data, "aihub.db")).CombinedOutput(); err != nil {
		t.Fatalf("sqlite fixture: %v\n%s", err, out)
	}
	compose := filepath.Join(w2, "srv", "compose", "docker-compose.yml")
	wf(t, compose, "services:\n  aihub:\n    image: polyforge/aihub2:"+old+"\n    ports:\n      - \"8080:8080\"\n  other:\n    image: busybox\n", 0o644)
	env := filepath.Join(d.w, "cfg", "deploy-public.env")
	wf(t, env, "AIHUB_HOST=root@fakehost2\n"+
		"AIHUB_DATA="+data+"\n"+
		"COMPOSE_FILE="+compose+"\n"+
		"COMPOSE_PROJECT=aihub-app2\n"+
		"DEPLOY_LOCK_FILE="+filepath.Join(w2, "srv", "deploy.lock")+"\n"+
		"BACKUP_ROOT="+filepath.Join(w2, "backups")+"\n"+
		"IMAGE_REPO=polyforge/aihub2\n", 0o600)
	d.env = append(d.env, "FAKE_W2="+w2)
	return env
}

// defaultCellDeployEnv writes the default Cell's deploy configuration
// (the world's own fakehost) beside the named Cells' files, so the
// default Cell is a deploy target too.
func (d *deployWorld) defaultCellDeployEnv(t *testing.T) string {
	t.Helper()
	env := filepath.Join(d.w, "cfg", "deploy.env")
	wf(t, env, "AIHUB_HOST=root@fakehost\n"+
		"AIHUB_DATA="+filepath.Join(d.w, "srv", "data", "aihub")+"\n"+
		"COMPOSE_FILE="+filepath.Join(d.w, "srv", "compose", "docker-compose.yml")+"\n"+
		"COMPOSE_PROJECT=aihub-app\n"+
		"DEPLOY_LOCK_FILE="+filepath.Join(d.w, "srv", "deploy.lock")+"\n"+
		"BACKUP_ROOT="+filepath.Join(d.w, "backups")+"\n", 0o600)
	return env
}

// twoTargetEnv answers d's environment restated as a step process of the
// Cell cell (its name and server), carrying the daemon's all-Cells
// injection: the deploy topology lives ONLY in the two Cells' deploy
// configuration files - the ambient environment keeps no deploy variable,
// so a multi-target child must read its own file and one target's values
// can never reach another's (the polyforge#60 leak).
func (d *deployWorld) twoTargetEnv(t *testing.T, cell, cellServer string) *deployWorld {
	t.Helper()
	return d.twoTargetEnvAt(t, cell, cellServer, "v2.2.1")
}

// twoTargetEnvAt is twoTargetEnv with the second target (public,
// fakehost2) running secondOld.
func (d *deployWorld) twoTargetEnvAt(t *testing.T, cell, cellServer, secondOld string) *deployWorld {
	t.Helper()
	defEnv := d.defaultCellDeployEnv(t)
	pubEnv := d.secondTarget(t, secondOld)
	d.env = withoutEnv(d.env, "AIHUB_HOST", "AIHUB_DATA", "COMPOSE_FILE", "COMPOSE_PROJECT", "DEPLOY_LOCK_FILE", "BACKUP_ROOT", "PF_DEPLOY_ENV", "PF_CELL", "PF_CELL_SERVER", "PF_CELL_TARGETS")
	d.env = append(d.env,
		"PF_CELL="+cell, "PF_CELL_SERVER="+cellServer,
		"PF_CELL_TARGETS="+cellTargetsVar(
			[3]string{"default", "http://fakehost:8080", defEnv},
			[3]string{"public", "http://fakehost2:8080", pubEnv}))
	return d
}

// secondWorld answers the second target's world directory (FAKE_W2).
func (d *deployWorld) secondWorld() string {
	for _, kv := range d.env {
		if v, ok := strings.CutPrefix(kv, "FAKE_W2="); ok {
			return v
		}
	}
	return ""
}

// logOf2 is logOf for the second target's world.
func (d *deployWorld) logOf2(name string) string {
	b, _ := os.ReadFile(filepath.Join(d.secondWorld(), name))
	return string(b)
}

// assertSecondUntouched: the second target's server was not mutated and
// its compose file still runs oldVer.
func (d *deployWorld) assertSecondUntouched(oldVer string) {
	d.t.Helper()
	b, err := os.ReadFile(filepath.Join(d.secondWorld(), "srv", "compose", "docker-compose.yml"))
	if err != nil || !strings.Contains(string(b), "image: polyforge/aihub2:"+oldVer) {
		d.t.Errorf("the second target's compose changed:\n%s", b)
	}
	for _, ln := range strings.Split(d.logOf2("log-docker"), "\n") {
		if !strings.HasPrefix(ln, "server docker ") {
			continue
		}
		for _, verb := range []string{" load", " tag ", " rmi ", " build ", " up -d"} {
			if strings.Contains(ln, verb) {
				d.t.Errorf("the second target's server was changed: %s", ln)
			}
		}
	}
}

// TestDeployPreflightListsEveryTarget (polyforge#76, AC1): with more than
// one Cell carrying a deploy configuration the preflight names every
// deploy target up front - the list the run is about to walk - and checks
// each target, one preflight OK line per target; the targets derive from
// the Cells: only a Cell whose deploy configuration file exists is one.
func TestDeployPreflightListsEveryTarget(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnv(t, "default", "http://fakehost:8080")
	out, err := d.run("", "--preflight")
	if err != nil {
		t.Fatalf("a two-target preflight failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deploy: targets (2): default public") {
		t.Errorf("the preflight must name every target up front:\n%s", out)
	}
	for _, want := range []string{
		"deploy: preflight OK for component aihub (target root@fakehost)",
		"deploy: preflight OK for component aihub (target root@fakehost2)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the preflight must check every target, missing %q:\n%s", want, out)
		}
	}
	d.assertNothingMutated("v2.2.1")
	d.assertSecondUntouched("v2.2.1")
}

// TestDeployTargetsSingleTargetBehavesAsPolyforge75 (AC1): only the work
// item's Cell carries a deploy configuration (the other stated Cell is a
// client only) - one target, and the run is exactly polyforge#75's
// single-target path: no target list, the #75 preflight wording and the
// #75 dry-run last line, unchanged.
func TestDeployTargetsSingleTargetBehavesAsPolyforge75(t *testing.T) {
	d := newReadyWorld(t)
	pubEnv := d.secondTarget(t, "v2.2.1")
	// a step process carries no deploy variable of its own: the topology
	// comes from the work item's Cell's configuration file only
	d.env = withoutEnv(d.env, "AIHUB_HOST", "AIHUB_DATA", "COMPOSE_FILE", "COMPOSE_PROJECT", "DEPLOY_LOCK_FILE", "BACKUP_ROOT", "PF_DEPLOY_ENV")
	d.env = append(d.env, "PF_CELL=public", "PF_CELL_SERVER=http://fakehost2:8080",
		"PF_DEPLOY_ENV="+pubEnv,
		"PF_CELL_TARGETS="+cellTargetsVar(
			[3]string{"default", "http://fakehost:8080", filepath.Join(d.w, "cfg", "deploy-default.env")},
			[3]string{"public", "http://fakehost2:8080", pubEnv}))
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err != nil || !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
		t.Fatalf("one target must run the polyforge#75 path: %v\n%s", err, out)
	}
	if strings.Contains(out, "targets (") {
		t.Errorf("one target is no list: nothing of the multi-target mode may show:\n%s", out)
	}
	d.assertSecondUntouched("v2.2.1")
	out, err = d.run("", "--preflight")
	if err != nil || !strings.Contains(out, "deploy: preflight OK for component aihub (target root@fakehost2)") {
		t.Fatalf("one target keeps the polyforge#75 preflight wording: %v\n%s", err, out)
	}
	if strings.Contains(out, "targets (") {
		t.Errorf("one target is no list:\n%s", out)
	}
}

// TestDeployTargetsNoDeployEnvRefusesAsPolyforge75 (AC1): no stated Cell
// has a deploy configuration - zero targets - and the polyforge#75
// refusal stands: the work item's Cell never falls back to another
// Cell's configuration, and nothing was touched.
func TestDeployTargetsNoDeployEnvRefusesAsPolyforge75(t *testing.T) {
	d := newReadyWorld(t)
	missing := filepath.Join(d.w, "cfg", "deploy-public.env")
	d.env = append(d.env, "PF_CELL=public", "PF_CELL_SERVER=http://fakehost2:8080",
		"PF_DEPLOY_ENV="+missing,
		"PF_CELL_TARGETS="+cellTargetsVar(
			[3]string{"default", "http://fakehost:8080", filepath.Join(d.w, "cfg", "deploy.env")},
			[3]string{"public", "http://fakehost2:8080", missing}))
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 || !strings.Contains(out, "REFUSED - PF_DEPLOY_ENV names "+missing) || !strings.Contains(out, "does not exist") {
		t.Fatalf("zero targets must refuse exactly as polyforge#75: %v\n%s", err, out)
	}
	if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker"); got != "" {
		t.Errorf("the refusal came after network/tool work:\n%s", got)
	}
	d.assertNothingMutated("v2.2.1")
}

// TestDeployTargetsCellNotInListRefused (polyforge#76, AC2): the work
// item's own Cell must be one of the deploy targets - a release work item
// never deploys targets its Cell does not own - and the refusal names the
// Cell and every target, before any network work, in all three modes.
func TestDeployTargetsCellNotInListRefused(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
		"preflight":   {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			d.twoTargetEnv(t, "staging", "http://stage.example:8080")
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			for _, want := range []string{"staging", "default", "public"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name the work item's Cell and every target, missing %q:\n%s", want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker") +
				d.logOf2("log-ssh") + d.logOf2("log-gh") + d.logOf2("log-docker"); got != "" {
				t.Errorf("the refusal came after network/tool work:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
			d.assertSecondUntouched("v2.2.1")
		})
	}
}

// TestDeployTargetsWrongServerRefused (polyforge#76, AC2): every target's
// deploy configuration must point at ITS OWN Cell's server - the #60
// shape, one Cell's file naming another Cell's host, is refused naming
// both values before ANY network work (a per-target child's own check
// would refuse too, but only after the wrapper had started walking the
// list: the preflight row shows the difference - no ssh, gh or docker
// call happens at all).
func TestDeployTargetsWrongServerRefused(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
		"preflight":   {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			d.twoTargetEnv(t, "default", "http://fakehost:8080")
			pubEnv := filepath.Join(d.w, "cfg", "deploy-public.env")
			b, err := os.ReadFile(pubEnv)
			if err != nil {
				t.Fatal(err)
			}
			wf(t, pubEnv, strings.Replace(string(b), "AIHUB_HOST=root@fakehost2", "AIHUB_HOST=root@fakehost", 1), 0o600)
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			for _, want := range []string{"public", "root@fakehost", "fakehost2"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name the target and both sides, missing %q:\n%s", want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker") +
				d.logOf2("log-ssh") + d.logOf2("log-gh") + d.logOf2("log-docker"); got != "" {
				t.Errorf("the refusal came after network/tool work:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
			d.assertSecondUntouched("v2.2.1")
		})
	}
}

// targetLines answers the output's per-target result lines ("target: ..."),
// in the order they were printed.
func targetLines(out string) []string {
	var got []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "target: ") {
			got = append(got, ln)
		}
	}
	return got
}

// lastLine answers the output's last non-empty line.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

// secondCompose answers the second target's compose file text.
func (d *deployWorld) secondCompose() string {
	b, _ := os.ReadFile(filepath.Join(d.secondWorld(), "srv", "compose", "docker-compose.yml"))
	return string(b)
}

// TestDeployTargetsSequentialTwoHosts (polyforge#76, AC3): two deploy
// targets, the work item's Cell (public) first, the rest by name; each
// target is deployed by its own clean subprocess reading only its own
// Cell's configuration (the second world's data, compose file and backup
// root - nothing of the first target's leaks into it), each reports one
// "target:" line, and the last line sums every target up.
func TestDeployTargetsSequentialTwoHosts(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnv(t, "public", "http://fakehost2:8080")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("a two-target deploy failed: %v\n%s", err, out)
	}
	tl := targetLines(out)
	if len(tl) != 2 || !strings.HasPrefix(tl[0], "target: public ") || !strings.HasPrefix(tl[1], "target: default ") {
		t.Fatalf("want one target line each, the work item's Cell (public) first then default, got %q\n%s", tl, out)
	}
	for i, want := range []string{"status=deployed", "status=deployed"} {
		if !strings.Contains(tl[i], want) {
			t.Errorf("target line %q lacks %q", tl[i], want)
		}
	}
	if got, want := lastLine(out), "targets: public=deployed default=deployed"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.3.0") {
		t.Errorf("the default target's compose is not on v2.3.0:\n%s", d.composeText())
	}
	if !strings.Contains(d.secondCompose(), "image: polyforge/aihub2:v2.3.0") {
		t.Errorf("the public target's compose is not on v2.3.0 (its own IMAGE_REPO):\n%s", d.secondCompose())
	}
	// each target backs up into its own BACKUP_ROOT - one backup each
	for _, root := range []string{filepath.Join(d.w, "backups"), filepath.Join(d.secondWorld(), "backups")} {
		if baks, _ := filepath.Glob(filepath.Join(root, "aihub-backup-*")); len(baks) != 1 {
			t.Errorf("backups under %s = %v, want one", root, baks)
		}
	}
	// the public target went first: its server's lock was taken before
	// the default target's server saw any mutation
	if !strings.Contains(d.logOf2("log-ssh"), "flock -n") || !strings.Contains(d.logOf("log-ssh"), "flock -n") {
		t.Errorf("each target must take its own server's lock")
	}
}

// TestDeployTargetsFailureStopsAndMarksRest (polyforge#76, AC3): the
// first target (the work item's Cell, public) fails after its swap and
// rolls back - the run stops there: default is not_attempted and its
// server untouched, the summary names both, and the exit code is the
// failed target's (10, rolled back).
func TestDeployTargetsFailureStopsAndMarksRest(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnv(t, "public", "http://fakehost2:8080")
	d.env = append(d.env, "FAKE_FAIL_UP_IMG=polyforge/aihub2:v2.3.0")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 10 {
		t.Fatalf("want exit 10 (the failed target's), got %v\n%s", err, out)
	}
	if got, want := lastLine(out), "targets: public=rolled_back default=not_attempted"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
	tl := targetLines(out)
	if len(tl) != 2 || !strings.Contains(tl[0], "public") || !strings.Contains(tl[0], "status=rolled_back") ||
		!strings.Contains(tl[1], "default") || !strings.Contains(tl[1], "status=not_attempted") {
		t.Errorf("target lines = %q", tl)
	}
	d.assertNothingMutated("v2.2.1")
	if got := d.logOf("log-ssh"); got != "" {
		t.Errorf("a target after the failed one was contacted:\n%s", got)
	}
}

// TestDeployTargetsSuccessNotRolledBackOnLaterFailure (polyforge#76, AC3):
// the first target deploys, the second fails before its swap (exit 1) -
// the first stays on the new version (no rollback of a target that
// succeeded), the second is not_changed, the exit code is 1.
func TestDeployTargetsSuccessNotRolledBackOnLaterFailure(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnv(t, "default", "http://fakehost:8080")
	// the second target's container is gone: its preflight fails before any change
	if err := os.Remove(filepath.Join(d.secondWorld(), "dk-server", "container")); err != nil {
		t.Fatal(err)
	}
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 {
		t.Fatalf("want exit 1 (the failed target's), got %v\n%s", err, out)
	}
	if got, want := lastLine(out), "targets: default=deployed public=not_changed"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
	if !strings.Contains(d.composeText(), "image: polyforge/aihub:v2.3.0") {
		t.Errorf("the target that succeeded was rolled back:\n%s", d.composeText())
	}
}

// TestDeployTargetsAlreadyDeployedSkip (polyforge#76, AC3): in the
// multi-target mode a target already running the version is recorded
// already_deployed and skipped - no failure, nothing changed on it.
func TestDeployTargetsAlreadyDeployedSkip(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnvAt(t, "default", "http://fakehost:8080", "v2.3.0")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("an already-deployed target must not fail the run: %v\n%s", err, out)
	}
	if got, want := lastLine(out), "targets: default=deployed public=already_deployed"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
	d.assertSecondUntouched("v2.3.0")
	if strings.Contains(d.logOf2("log-ssh"), "flock -n") {
		t.Errorf("an already-deployed target took its deploy lock:\n%s", d.logOf2("log-ssh"))
	}
}

// TestDeployForeignVersionLineRefused (polyforge#76, AC4): a target whose
// running version carries a pre-release suffix this repo has no tag for
// runs a foreign version line (another fork's build): it is refused
// before any change - a real deploy and a --dry-run alike, the single
// target too - naming the running version and asking for a person to
// handle it. The suffix is the world's own, never a name the script
// knows: two different suffixes are both refused.
func TestDeployForeignVersionLineRefused(t *testing.T) {
	for _, running := range []string{"v2.2.1-gmi.1", "v2.2.0-acme.7"} {
		for name, args := range map[string][]string{
			"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
			"dry-run":     {"v2.3.0", "--dry-run"},
		} {
			t.Run(running+" "+name, func(t *testing.T) {
				d := newDeployWorld(t, running)
				d.release("v2.3.0", true, digNew, digNew)
				out, err := d.run("", args...)
				if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
					t.Fatalf("want exit 1 + REFUSED for a foreign running line, got %v\n%s", err, out)
				}
				for _, want := range []string{running, "by hand"} {
					if !strings.Contains(out, want) {
						t.Errorf("the refusal must name the running version and ask for manual handling, missing %q:\n%s", want, out)
					}
				}
				d.assertNothingMutated(running)
			})
		}
	}
}

// TestDeployTargetsForeignLineMarksNotChanged (polyforge#76, AC4): in a
// multi-target run the target on a foreign version line is not_changed -
// the targets before it deployed as usual - and the run fails (exit 1).
func TestDeployTargetsForeignLineMarksNotChanged(t *testing.T) {
	d := newReadyWorld(t)
	d.twoTargetEnvAt(t, "default", "http://fakehost:8080", "v2.2.1-gmi.1")
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if exitCode(err) != 1 {
		t.Fatalf("want exit 1 (the refused target's), got %v\n%s", err, out)
	}
	if got, want := lastLine(out), "targets: default=deployed public=not_changed"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
	if !strings.Contains(out, "v2.2.1-gmi.1") || !strings.Contains(out, "by hand") {
		t.Errorf("the refused target must name its running version and ask for manual handling:\n%s", out)
	}
	d.assertSecondUntouched("v2.2.1-gmi.1")
}

// TestDeployTargetsSameBackupRootRefused (polyforge#76, AC4): two targets
// backing up into the same BACKUP_ROOT would mix (and prune) each other's
// backups - refused before any network work in all three modes, naming
// the directory and the targets that share it.
func TestDeployTargetsSameBackupRootRefused(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
		"preflight":   {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			d.twoTargetEnv(t, "default", "http://fakehost:8080")
			shared := filepath.Join(d.w, "backups")
			pubEnv := filepath.Join(d.w, "cfg", "deploy-public.env")
			b, err := os.ReadFile(pubEnv)
			if err != nil {
				t.Fatal(err)
			}
			wf(t, pubEnv, strings.Replace(string(b), "BACKUP_ROOT="+filepath.Join(d.secondWorld(), "backups"), "BACKUP_ROOT="+shared, 1), 0o600)
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			for _, want := range []string{shared, "default", "public"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name the shared directory and both targets, missing %q:\n%s", want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker") +
				d.logOf2("log-ssh") + d.logOf2("log-gh") + d.logOf2("log-docker"); got != "" {
				t.Errorf("the refusal came after network/tool work:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
			d.assertSecondUntouched("v2.2.1")
		})
	}
}

// TestDeployTargetsWrongServerRefusalNamesOwnCell (polyforge#76, AC2;
// review r1#1): the multi-target refusal of a configuration pointing at
// another Cell's host names the TARGET's own Cell and its PF_CELL_TARGETS
// row - the side it was checked against - never "this work item's Cell
// server" (here the work item's Cell is default, the misconfigured target
// is public: the polyforge#75 single-target wording would name the wrong
// side to the operator reading the refusal).
func TestDeployTargetsWrongServerRefusalNamesOwnCell(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
		"preflight":   {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			d.twoTargetEnv(t, "default", "http://fakehost:8080")
			pubEnv := filepath.Join(d.w, "cfg", "deploy-public.env")
			b, err := os.ReadFile(pubEnv)
			if err != nil {
				t.Fatal(err)
			}
			wf(t, pubEnv, strings.Replace(string(b), "AIHUB_HOST=root@fakehost2", "AIHUB_HOST=root@fakehost", 1), 0o600)
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			for _, want := range []string{"its own Cell public's server fakehost2", "PF_CELL_TARGETS row public", "AIHUB_HOST root@fakehost"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name the target's own Cell and its PF_CELL_TARGETS row, missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "this work item's Cell server") {
				t.Errorf("the multi-target refusal names the work item's Cell, not the target's own:\n%s", out)
			}
		})
	}
}

// TestDeployTargetsMissingHostRefusedBeforeNetwork (polyforge#76, AC2;
// review r1#2): a target whose merged configuration sets no AIHUB_HOST
// does not point at its own Cell's server either - it is refused with
// the other multi-target checks, before ANY network work in all three
// modes, naming the target, the unset AIHUB_HOST and its Cell's server;
// no earlier target is deployed first (which would leave a partial run
// the pre-network checks exist to prevent).
func TestDeployTargetsMissingHostRefusedBeforeNetwork(t *testing.T) {
	for name, args := range map[string][]string{
		"real deploy": {"v2.3.0", "--confirm", "v2.3.0"},
		"dry-run":     {"v2.3.0", "--dry-run"},
		"preflight":   {"--preflight"},
	} {
		t.Run(name, func(t *testing.T) {
			d := newReadyWorld(t)
			d.twoTargetEnv(t, "default", "http://fakehost:8080")
			pubEnv := filepath.Join(d.w, "cfg", "deploy-public.env")
			b, err := os.ReadFile(pubEnv)
			if err != nil {
				t.Fatal(err)
			}
			wf(t, pubEnv, strings.Replace(string(b), "AIHUB_HOST=root@fakehost2\n", "", 1), 0o600)
			out, err := d.run("", args...)
			if exitCode(err) != 1 || !strings.Contains(out, "REFUSED") {
				t.Fatalf("want exit 1 + REFUSED, got %v\n%s", err, out)
			}
			for _, want := range []string{"target public", "AIHUB_HOST", "fakehost2"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal must name the target, the unset AIHUB_HOST and its Cell's server, missing %q:\n%s", want, out)
				}
			}
			if got := d.logOf("log-ssh") + d.logOf("log-gh") + d.logOf("log-docker") +
				d.logOf2("log-ssh") + d.logOf2("log-gh") + d.logOf2("log-docker"); got != "" {
				t.Errorf("the refusal came after network/tool work:\n%s", got)
			}
			d.assertNothingMutated("v2.2.1")
			d.assertSecondUntouched("v2.2.1")
		})
	}
}
