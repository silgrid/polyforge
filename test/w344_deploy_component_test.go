// carries: .polyforge/deploy.sh

package test

// .polyforge/deploy.sh is component-aware behind the one
// deploy entry. --component pi-web drives the package channel
// pi-web/deploy-piweb.sh (PIWEB_INSTALL_REPO / PIWEB_INSTALL_VERSION) and runs
// no AIHub step at all — no AIHub required variable, ssh, docker, gh, scp or
// curl; --component aihub (the default) keeps the AIHub path unchanged; the
// confirmation rule and --dry-run hold for both. Fake world only: the real
// deploy.sh runs from a scratch tree whose pi-web/deploy-piweb.sh is a stub,
// with npm/ssh/docker/gh/scp/curl stubs first on PATH.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type w344DeployWorld struct {
	t       *testing.T
	root    string
	bin     string
	calls   string
	channel string
	home    string
}

func w344NewDeployWorld(t *testing.T, installed string) *w344DeployWorld {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", ".polyforge", "deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	w := &w344DeployWorld{t: t, root: filepath.Join(d, "repo"), bin: filepath.Join(d, "bin"),
		calls: filepath.Join(d, "calls.log"), channel: filepath.Join(d, "channel.log"), home: filepath.Join(d, "home")}
	for _, dir := range []string{filepath.Join(w.root, ".polyforge"), filepath.Join(w.root, "pi-web"), w.bin, w.home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w.write(filepath.Join(w.root, ".polyforge", "deploy.sh"), string(src), 0o755)
	w.write(filepath.Join(d, "installed"), installed, 0o644)
	// the package channel: records its install environment; W344_CHANNEL_RC
	// is its exit code, W344_CHANNEL_INSTALLS=1 installs before that exit.
	w.write(filepath.Join(w.root, "pi-web", "deploy-piweb.sh"), `#!/bin/sh
echo "PIWEB_INSTALL_REPO=$PIWEB_INSTALL_REPO PIWEB_INSTALL_VERSION=$PIWEB_INSTALL_VERSION" >> "$W344_CHANNEL_LOG"
if [ "${W344_CHANNEL_RC:-0}" = 0 ] || [ "${W344_CHANNEL_INSTALLS:-}" = 1 ]; then
  printf '%s' "${PIWEB_INSTALL_VERSION#v}" > "$W344_INSTALLED"
fi
exit "${W344_CHANNEL_RC:-0}"
`, 0o755)
	w.write(filepath.Join(w.bin, "npm"), `#!/bin/sh
if [ "$1 $2" = "ls -g" ]; then
  v=$(cat "$W344_INSTALLED")
  echo /usr/lib
  [ -z "$v" ] || echo "└── @silgrid/pi-web@$v"
  exit 0
fi
echo "npm $*" >> "$W344_CALLS"
exit 0
`, 0o755)
	for _, tool := range []string{"ssh", "docker", "gh", "scp", "curl", "rsync"} {
		w.write(filepath.Join(w.bin, tool), "#!/bin/sh\necho \""+tool+" $*\" >> \"$W344_CALLS\"\nexit 0\n", 0o755)
	}
	w.write(w.calls, "", 0o644)
	w.write(w.channel, "", 0o644)
	return w
}

func (w *w344DeployWorld) write(p, body string, mode os.FileMode) {
	w.t.Helper()
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		w.t.Fatal(err)
	}
}

func (w *w344DeployWorld) read(p string) string {
	w.t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// run executes deploy.sh with no AIHub variable set at all.
func (w *w344DeployWorld) run(extra []string, args ...string) (string, int) {
	w.t.Helper()
	cmd := exec.Command("sh", append([]string{filepath.Join(w.root, ".polyforge", "deploy.sh")}, args...)...)
	cmd.Dir = w.root
	var env []string
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(k, "AIHUB_") || strings.HasPrefix(k, "COMPOSE_") || strings.HasPrefix(k, "PIWEB_") ||
			k == "DEPLOY_LOCK_FILE" || k == "GH_REPO" || k == "PATH" || k == "HOME" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PATH="+w.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+w.home,
		"W344_CALLS="+w.calls, "W344_CHANNEL_LOG="+w.channel, "W344_INSTALLED="+filepath.Join(filepath.Dir(w.root), "installed"),
		"DEPLOY_REMOTE_IS=silgrid/polyforge") // no origin here
	cmd.Env = append(env, extra...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			w.t.Fatalf("deploy.sh: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// noAihubStep: not one AIHub-side tool (ssh, docker, gh, scp, curl, rsync)
// was invoked — the recorded fake calls are empty.
func (w *w344DeployWorld) noAihubStep(out string) {
	w.t.Helper()
	if c := strings.TrimSpace(w.read(w.calls)); c != "" {
		w.t.Fatalf("the pi-web component ran an AIHub-side tool:\n%s\noutput:\n%s", c, out)
	}
	if strings.Contains(out, "missing required variable") {
		w.t.Fatalf("the pi-web component asked for an AIHub variable:\n%s", out)
	}
}

func TestW344DeployComponentPiWeb(t *testing.T) {
	t.Run("dry run plans and changes nothing", func(t *testing.T) {
		w := w344NewDeployWorld(t, "0.1.9")
		out, code := w.run(nil, "v0.2.0", "--component", "pi-web", "--dry-run")
		if code != 0 {
			t.Fatalf("dry run exit %d:\n%s", code, out)
		}
		for _, want := range []string{"PIWEB_INSTALL_REPO=silgrid/polyforge PIWEB_INSTALL_VERSION=v0.2.0 bash pi-web/deploy-piweb.sh",
			"previous_version=0.1.9", "state=not_changed", "nothing is changed"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dry-run plan lacks %q:\n%s", want, out)
			}
		}
		if c := w.read(w.channel); c != "" {
			t.Fatalf("a dry run ran the channel: %q", c)
		}
		w.noAihubStep(out)
	})
	t.Run("deploy drives the channel with the pinned release", func(t *testing.T) {
		w := w344NewDeployWorld(t, "0.1.9")
		out, code := w.run(nil, "v0.2.0", "--confirm", "v0.2.0", "--component=pi-web")
		if code != 0 {
			t.Fatalf("deploy exit %d:\n%s", code, out)
		}
		if got := strings.TrimSpace(w.read(w.channel)); got != "PIWEB_INSTALL_REPO=silgrid/polyforge PIWEB_INSTALL_VERSION=v0.2.0" {
			t.Fatalf("channel environment = %q", got)
		}
		tail := strings.Split(strings.TrimSpace(out), "\n")
		if len(tail) < 3 || strings.Join(tail[len(tail)-3:], "\n") != "previous_version=0.1.9\nversion=v0.2.0\nstate=deployed" {
			t.Fatalf("state report:\n%s", out)
		}
		w.noAihubStep(out)
	})
	t.Run("GH_REPO names the release repository", func(t *testing.T) {
		w := w344NewDeployWorld(t, "")
		out, code := w.run([]string{"GH_REPO=example-org/polyforge", "DEPLOY_REMOTE_IS=example-org/polyforge"}, "v0.2.0", "--confirm", "v0.2.0", "--component", "pi-web")
		if code != 0 || !strings.Contains(w.read(w.channel), "PIWEB_INSTALL_REPO=example-org/polyforge ") || !strings.Contains(out, "previous_version=absent") {
			t.Fatalf("exit %d channel %q:\n%s", code, w.read(w.channel), out)
		}
		w.noAihubStep(out)
	})
	for _, c := range []struct {
		name, rc, installs, state string
	}{
		{"failure before the swap keeps the old service", "1", "", "state=not_changed"},
		{"a failed post-swap probe reports changed", "3", "", "state=changed"},
		{"a failure after the install reports changed", "1", "1", "state=changed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := w344NewDeployWorld(t, "0.2.0")
			out, code := w.run([]string{"W344_CHANNEL_RC=" + c.rc, "W344_CHANNEL_INSTALLS=" + c.installs},
				"v0.2.1", "--confirm", "v0.2.1", "--component", "pi-web")
			if code != 1 || !strings.Contains(out, "previous_version=0.2.0") || !strings.Contains(out, c.state) {
				t.Fatalf("exit %d, want 1 and %s:\n%s", code, c.state, out)
			}
			if c.state == "state=changed" && strings.Contains(out, "state=not_changed") {
				t.Fatalf("claimed not_changed after a swap or install:\n%s", out)
			}
			w.noAihubStep(out)
		})
	}
	for _, c := range []struct {
		name string
		args []string
		code int
	}{
		{"missing confirm", []string{"v0.2.0", "--component", "pi-web"}, 1},
		{"confirm mismatch", []string{"v0.2.0", "--confirm", "v0.2.1", "--component", "pi-web"}, 1},
		{"dry run with a mismatching confirm", []string{"v0.2.0", "--confirm", "v0.2.1", "--component", "pi-web", "--dry-run"}, 1},
		{"unknown component", []string{"v0.2.0", "--confirm", "v0.2.0", "--component", "webui"}, 2},
		{"component without a value", []string{"v0.2.0", "--confirm", "v0.2.0", "--component"}, 2},
		{"--force is aihub-only", []string{"v0.2.0", "--confirm", "v0.2.0", "--component", "pi-web", "--force"}, 2},
	} {
		t.Run("refused: "+c.name, func(t *testing.T) {
			w := w344NewDeployWorld(t, "0.1.9")
			out, code := w.run(nil, c.args...)
			if code != c.code {
				t.Fatalf("exit %d, want %d:\n%s", code, c.code, out)
			}
			if ch := w.read(w.channel); ch != "" {
				t.Fatalf("the channel ran before the refusal: %q", ch)
			}
			w.noAihubStep(out)
			if w.read(filepath.Join(filepath.Dir(w.root), "installed")) != "0.1.9" {
				t.Fatal("the refusal changed the installed package")
			}
		})
	}
}

// The aihub component is the default and its path is unchanged: with no
// --component (and with --component aihub) the AIHub preflight still asks
// for its required variables, and the pi-web channel never runs.
func TestW344DeployComponentAihubDefault(t *testing.T) {
	for _, extra := range [][]string{nil, {"--component", "aihub"}} {
		w := w344NewDeployWorld(t, "0.1.9")
		out, code := w.run(nil, append([]string{"v0.2.0", "--confirm", "v0.2.0"}, extra...)...)
		if code != 2 || !strings.Contains(out, "missing required variable(s): AIHUB_HOST AIHUB_DATA COMPOSE_FILE COMPOSE_PROJECT DEPLOY_LOCK_FILE") {
			t.Fatalf("component %v: the aihub path must keep its required-variable preflight; exit %d:\n%s", extra, code, out)
		}
		if ch := w.read(w.channel); ch != "" {
			t.Fatalf("the aihub component ran the pi-web channel: %q", ch)
		}
	}
}

// TestW344DeployAihubVersionLineCutover pins the named version-line cutover
// on the aihub component: a running v2.20.x -> a v0.2.x
// target is an upgrade (no --force), whose SQL plan is line-aware — it runs
// v0.2.0's dir and never the retired v2.20.x dirs below the running version;
// an ordinary downgrade (v0.2.2 -> v0.2.0, v2.20.22 -> v0.1.9) is still
// refused without --force, before the server changes.
func TestW344DeployAihubVersionLineCutover(t *testing.T) {
	t.Run("accepted v2.20.22 -> v0.2.0", func(t *testing.T) {
		d := newSQLWorldVers(t, "v2.20.22", []string{"v2.20.8", "v0.2.0"}, []sqlVer{
			{"v2.20.8", "ALTER TABLE projects DROP COLUMN col_a;\n", "ALTER TABLE projects ADD COLUMN col_a TEXT;\n", colProbe("col_a")},
			{"v0.2.0", "ALTER TABLE projects DROP COLUMN col_b;\n", "ALTER TABLE projects ADD COLUMN col_b TEXT;\n", colProbe("col_b")},
		}, []string{"col_a", "col_b"})
		out, err := d.run("", "v0.2.0", "--confirm", "v0.2.0")
		if err != nil {
			t.Fatalf("the cutover deploy v2.20.22 -> v0.2.0 MUST succeed without --force: %v\n%s", err, out)
		}
		if strings.Contains(out, "OLDER") || strings.Contains(out, "downgrade") {
			t.Fatalf("the cutover was treated as a downgrade:\n%s", out)
		}
		if !strings.Contains(out, "upgrade SQL:    applied v0.2.0") {
			t.Errorf("the line-aware SQL plan did not apply v0.2.0\n%s", out)
		}
		if n := sqlRuns(d, "v2.20.8-upgrade.sql"); n != 0 {
			t.Errorf("the retired v2.20.8 SQL below the running version ran (%d)\n%s", n, out)
		}
		if !dbHasCol(t, d.dbPath(), "col_a") || dbHasCol(t, d.dbPath(), "col_b") {
			t.Errorf("want col_a kept (v2.20.8 not replayed) and col_b dropped (v0.2.0 applied)")
		}
	})
	for _, c := range []struct{ from, to string }{
		{"v0.2.2", "v0.2.0"},
		{"v2.20.22", "v0.1.9"},
	} {
		t.Run("refused "+c.from+" -> "+c.to, func(t *testing.T) {
			d := newDeployWorld(t, c.from)
			d.release(c.from, true, digOld, digOld)
			d.release(c.to, true, digNew, digNew)
			out, err := d.run("", c.to, "--confirm", c.to)
			if exitCode(err) != 1 || !strings.Contains(out, c.to+" is OLDER than the running "+c.from+" - a downgrade needs --force") {
				t.Fatalf("an ordinary downgrade %s -> %s MUST still be refused; %v\n%s", c.from, c.to, err, out)
			}
			d.assertNothingMutated(c.from)
		})
	}
}

// TestW344DeployAihubSuffixVersions (AC1) pins deploy.sh's
// order of suffixed release versions vX.Y.Z-<name>.N on the aihub
// component: SemVer 2.0 precedence (a pre-release below its core release,
// numeric identifiers as numbers), with the version-line mapping kept. Each
// case names the AC1 table row it carries (the suffix name in the fixtures
// is the neutral `fork`). An upgrade deploys without --force; a downgrade is
// refused without --force before the server changes; the SQL plan is the
// release tags in (running, target] by that order. AC1 row 7 (a running
// version that is not a release) holds for the installer only: deploy.sh
// still refuses when it cannot read a release tag from the running aihub.
func TestW344DeployAihubSuffixVersions(t *testing.T) {
	for _, c := range []struct {
		row      int
		from, to string
	}{
		{1, "v0.2.0-fork.1", "v0.2.0-fork.2"},
		{3, "v0.2.0-fork.9", "v0.2.0-fork.10"},
		{4, "v0.2.0-fork.5", "v0.2.1-fork.1"},
	} {
		t.Run(fmt.Sprintf("AC1 row %d allowed %s -> %s", c.row, c.from, c.to), func(t *testing.T) {
			d := newDeployWorld(t, c.from)
			d.release(c.from, true, digOld, digOld)
			d.release(c.to, true, digNew, digNew)
			out, err := d.run("", c.to, "--confirm", c.to)
			if err != nil {
				t.Fatalf("AC1 row %d: the deploy %s -> %s MUST succeed without --force: %v\n%s", c.row, c.from, c.to, err, out)
			}
			if strings.Contains(out, "OLDER") || strings.Contains(out, "downgrade") || strings.Contains(out, "already running") {
				t.Fatalf("AC1 row %d: the upgrade %s -> %s was not treated as an upgrade:\n%s", c.row, c.from, c.to, out)
			}
			if !strings.Contains(out, "deployed="+c.to+" previous="+c.from) {
				t.Fatalf("AC1 row %d: output lacks deployed=%s previous=%s\n%s", c.row, c.to, c.from, out)
			}
		})
	}
	for _, c := range []struct {
		row      int
		from, to string
	}{
		{2, "v0.2.0-fork.2", "v0.2.0-fork.1"},
		{5, "v0.2.1", "v0.2.0"},
		{6, "v0.2.12", "v0.2.0-fork.1"},
	} {
		t.Run(fmt.Sprintf("AC1 row %d refused %s -> %s", c.row, c.from, c.to), func(t *testing.T) {
			d := newDeployWorld(t, c.from)
			d.release(c.from, true, digOld, digOld)
			d.release(c.to, true, digNew, digNew)
			out, err := d.run("", c.to, "--confirm", c.to)
			if exitCode(err) != 1 || !strings.Contains(out, c.to+" is OLDER than the running "+c.from+" - a downgrade needs --force") {
				t.Fatalf("AC1 row %d: the downgrade %s -> %s MUST be refused without --force; %v\n%s", c.row, c.from, c.to, err, out)
			}
			d.assertNothingMutated(c.from)
		})
	}
	// AC1 row 3, the SQL plan: (running, target] by SemVer order runs the
	// target's dir only — not a lower pre-release's below the running one,
	// nor the core release's above the target.
	t.Run("AC1 row 3 SQL plan v0.2.0-fork.9 -> v0.2.0-fork.10", func(t *testing.T) {
		d := newSQLWorldVers(t, "v0.2.0-fork.9", []string{"v0.2.0-fork.2", "v0.2.0-fork.10", "v0.2.0"}, []sqlVer{
			{"v0.2.0-fork.2", "ALTER TABLE projects DROP COLUMN col_a;\n", "ALTER TABLE projects ADD COLUMN col_a TEXT;\n", colProbe("col_a")},
			{"v0.2.0-fork.10", "ALTER TABLE projects DROP COLUMN col_b;\n", "ALTER TABLE projects ADD COLUMN col_b TEXT;\n", colProbe("col_b")},
			{"v0.2.0", "ALTER TABLE projects DROP COLUMN col_c;\n", "ALTER TABLE projects ADD COLUMN col_c TEXT;\n", colProbe("col_c")},
		}, []string{"col_a", "col_b", "col_c"})
		out, err := d.run("", "v0.2.0-fork.10", "--confirm", "v0.2.0-fork.10")
		if err != nil {
			t.Fatalf("AC1 row 3: the deploy v0.2.0-fork.9 -> v0.2.0-fork.10 MUST succeed without --force: %v\n%s", err, out)
		}
		if !strings.Contains(out, "upgrade SQL:    applied v0.2.0-fork.10 (") {
			t.Errorf("AC1 row 3: the SQL plan did not apply exactly v0.2.0-fork.10\n%s", out)
		}
		for _, f := range []string{"v0.2.0-fork.2-upgrade.sql", "v0.2.0-upgrade.sql"} {
			if n := sqlRuns(d, f); n != 0 {
				t.Errorf("AC1 row 3: %s outside (v0.2.0-fork.9, v0.2.0-fork.10] ran (%d)\n%s", f, n, out)
			}
		}
		if !dbHasCol(t, d.dbPath(), "col_a") || dbHasCol(t, d.dbPath(), "col_b") || !dbHasCol(t, d.dbPath(), "col_c") {
			t.Errorf("AC1 row 3: want col_a and col_c kept and col_b dropped")
		}
	})
	// AC1 row 7 does not reach deploy.sh (owner ruling: installer only): a
	// running dev build is still refused before anything changes.
	t.Run("AC1 row 7 not for deploy: running dev refused", func(t *testing.T) {
		d := newDeployWorld(t, "dev")
		d.release("v0.2.0-fork.1", true, digNew, digNew)
		out, err := d.run("", "v0.2.0-fork.1", "--confirm", "v0.2.0-fork.1")
		if exitCode(err) != 1 || !strings.Contains(out, "could not read the running version") {
			t.Fatalf("a running dev aihub MUST still be refused (could not read the running version); %v\n%s", err, out)
		}
		d.assertNothingMutated("dev")
	})
}
