package cli

// verbs_supervise_current_test.go (polyforge#175, AC1): the machine's
// run mode and the supervise plan's current resolution — the image
// judgment reads ONE fact (the seed manifest the image's final layer
// carries; a releases/ directory without a manifest is not an image),
// the daemon and aihub argv launch the components volume's current pf
// as a literal path, Pi Web's environment carries that same pf as
// PF_BIN (a stale value never survives), and the flag defaults follow
// the layout.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

// layoutSeedFixture plants the image's seed (a manifest under
// releases/<ver>) at a scratch root, swaps workstationSeedRoot to it
// for the test and answers the root it planted at. deferPlanted=false
// plants the version directory WITHOUT the manifest — the fact the
// judgment reads is the manifest, not the directory.
func layoutSeedFixture(t *testing.T, withManifest bool) string {
	t.Helper()
	t.Setenv("POLYFORGE_DATA_DIR", "")
	root := t.TempDir()
	dir := filepath.Join(root, "releases", "v0.2.4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withManifest {
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := workstationSeedRoot
	workstationSeedRoot = root
	t.Cleanup(func() { workstationSeedRoot = old })
	return root
}

// TestSuperviseCurrentRoots: the mode judgment and the current root —
// one fact (the seed manifest), two layouts (the image's components
// volume, the host installer's release checkout).
func TestSuperviseCurrentRoots(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	// a host: an injected EMPTY seed root (review N2 — never this
	// machine's real /opt/polyforge/seed), so the host half holds inside
	// an image too
	oldSeed := workstationSeedRoot
	workstationSeedRoot = t.TempDir()
	t.Cleanup(func() { workstationSeedRoot = oldSeed })
	// a host: no seed manifest anywhere — the release checkout root
	if workstationImage() {
		t.Fatal("a machine without the image's seed manifest must not be judged the workstation image")
	}
	if got := currentRoot(filepath.Join("/root", ".local", "share", "polyforge")); got != filepath.Join("/root", ".local", "share", "polyforge", "release", "current") {
		t.Fatalf("the host current root = %q, want <data-dir>/release/current", got)
	}

	// the image: the seed manifest is there — everything resolves
	// through the components volume
	layoutSeedFixture(t, true)
	if !workstationImage() {
		t.Fatal("the seed manifest the image carries must judge the machine the workstation image")
	}
	if got := currentRoot("/root"); got != "/components/current" {
		t.Fatalf("the image current root = %q, want /components/current", got)
	}
	pf, err := currentPfBin()
	if err != nil {
		t.Fatal(err)
	}
	if pf != "/components/current/bin/pf" {
		t.Fatalf("the image pf = %q, want the components volume's current pf as a literal path", pf)
	}

	// a seed directory without the manifest is not the fact: no
	// probing, no guessing from a directory someone left
	layoutSeedFixture(t, false)
	if workstationImage() {
		t.Fatal("a releases/ directory without the seed manifest must not be judged the workstation image")
	}
	pf, err = currentPfBin()
	if err != nil {
		t.Fatal(err)
	}
	if pf == "/components/current/bin/pf" {
		t.Fatal("without the seed manifest the pf must not resolve through the components volume")
	}
}

// TestSupervisePlanCurrentPfBin: with the image's current pf, the
// daemon and the aihub server launch THAT binary (Argv[0] the literal
// current path — never an os.Executable resolution, which would pin the
// version directory and keep running the old version after an upgrade
// switch), and Pi Web's environment carries it as PF_BIN — a stale
// PF_BIN never survives into the child.
func TestSupervisePlanCurrentPfBin(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cli.toml")
	data := filepath.Join(dir, "data")
	piweb := filepath.Join(dir, "pi-web")
	if err := os.MkdirAll(piweb, 0o755); err != nil {
		t.Fatal(err)
	}
	const current = "/components/current/bin/pf"
	rs, err := planSupervise(superviseOpts{pfBin: current, home: "/root", configPath: cfg,
		dataDir: data, workflowsDir: filepath.Join(dir, "wf"), piWebDir: piweb,
		withAIHub: true, grace: supervisor.MinStopGrace,
		environ: []string{"HOME=/root", "PATH=/usr/bin:/bin", "PF_BIN=/stale/old/pf"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"daemon", "aihub"} {
		c := superviseChild(rs, name)
		if c == nil {
			t.Fatalf("no %s child", name)
		}
		if c.Argv[0] != current {
			t.Fatalf("the %s argv[0] = %q, want the current pf %q", name, c.Argv[0], current)
		}
	}
	piWeb := superviseChild(rs, "pi-web")
	if piWeb == nil {
		t.Fatal("no pi-web child though its checkout exists")
	}
	env := strings.Join(piWeb.Env, "\n")
	if n := strings.Count(env, "PF_BIN="); n != 1 || !strings.Contains(env, "PF_BIN="+current) {
		t.Fatalf("the pi-web environment must carry exactly one PF_BIN, the current pf:\n%s", env)
	}
	if strings.Contains(env, "/stale/old/pf") {
		t.Fatalf("a stale PF_BIN must not survive into the pi-web child:\n%s", env)
	}
}

// TestSuperviseDefaultDirsCurrent: the --workflows-dir / --pi-web-dir
// defaults follow the layout — the image's components current, the
// host's <data-dir>/release/current.
func TestSuperviseDefaultDirsCurrent(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	layoutSeedFixture(t, true)
	wf, web := superviseDefaultDirs("/root/.local/share/polyforge")
	if wf != "/components/current/workflows" || web != "/components/current/pi-web" {
		t.Fatalf("the image defaults = (%q, %q), want the components current's workflows/ and pi-web/", wf, web)
	}
	// a host: the defaults stay the release checkout under the data dir
	layoutSeedFixture(t, false)
	wf, web = superviseDefaultDirs("/root/.local/share/polyforge")
	want := filepath.Join("/root", ".local", "share", "polyforge", "release", "current")
	if wf != filepath.Join(want, "workflows") || web != filepath.Join(want, "pi-web") {
		t.Fatalf("the host defaults = (%q, %q), want <data-dir>/release/current's workflows/ and pi-web/", wf, web)
	}
}

// TestSuperviseResolveDirsKeepsEachGivenFlag (review r1#1): the two
// directory flags take their defaults INDEPENDENTLY — a --pi-web-dir
// the caller set is never replaced because --workflows-dir was not
// given, and the same the other way round.
func TestSuperviseResolveDirsKeepsEachGivenFlag(t *testing.T) {
	t.Setenv("POLYFORGE_DATA_DIR", "")
	layoutSeedFixture(t, true) // the image: the defaults come from the components current
	const given = "/an/operator/checkout"
	wf, web := superviseResolveDirs("", given, "/root/.local/share/polyforge")
	if web != given {
		t.Fatalf("a given --pi-web-dir must be kept, got %q", web)
	}
	if wf != "/components/current/workflows" {
		t.Fatalf("the unset --workflows-dir takes the layout default, got %q", wf)
	}
	wf, web = superviseResolveDirs(given, "", "/root/.local/share/polyforge")
	if wf != given {
		t.Fatalf("a given --workflows-dir must be kept, got %q", wf)
	}
	if web != "/components/current/pi-web" {
		t.Fatalf("the unset --pi-web-dir takes the layout default, got %q", web)
	}
	wf, web = superviseResolveDirs("a", "b", "/root")
	if wf != "a" || web != "b" {
		t.Fatalf("two given flags must both be kept, got (%q, %q)", wf, web)
	}
	wf, web = superviseResolveDirs("", "", "/root/.local/share/polyforge")
	if wf != "/components/current/workflows" || web != "/components/current/pi-web" {
		t.Fatalf("no flag given must take both defaults, got (%q, %q)", wf, web)
	}
}

// TestSuperviseUsageNamesUpgradeRestartExit: the usage states the 75
// semantics — an upgrade-restart relaunch at once, no failure backoff.
func TestSuperviseUsageNamesUpgradeRestartExit(t *testing.T) {
	for _, want := range []string{
		"exiting 75",
		"no failure backoff",
	} {
		if !strings.Contains(superviseUsage, want) {
			t.Fatalf("the supervise usage must state the 75 semantics (%q):\n%s", want, superviseUsage)
		}
	}
}

// TestSuperviseFlagHelpNamesTheCurrentDefaults (review r1#3): the flag
// help `pf supervise -h` and a parse error print states the same current
// default as the usage — never the stale host-only path.
func TestSuperviseFlagHelpNamesTheCurrentDefaults(t *testing.T) {
	var out, errb bytes.Buffer
	if code := SuperviseMain([]string{"--no-such-flag"}, nil, &out, &errb); code != 2 {
		t.Fatalf("an unknown flag: exit %d, want 2 (the help rides the usage it prints)", code)
	}
	help := errb.String()
	for _, want := range []string{
		"default <current>/workflows",
		"default <current>/pi-web",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("the flag help must state the current-root default (%q):\n%s", want, help)
		}
	}
}
