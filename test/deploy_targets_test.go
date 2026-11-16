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
	defEnv := d.defaultCellDeployEnv(t)
	pubEnv := d.secondTarget(t, "v2.2.1")
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
