package test

// w136_install_dev_test.go (polyforge#136): tools/install.sh --dev — the
// local development channel — run hermetically end to end on the w174
// install harness. The fixture is a REAL git repository as origin (a
// full history: a release tag v0.2.3, client commits after it, and an
// annotated green tag whose message is the marker JSON tools/green-tag
// parses), cloned by the gh stub with the real git; the build tools are
// shims first on PATH (go, npm answer presence, node its major, make a
// dev-build stub that journals its argv and writes the pf wrapper a
// real make dev-build would stamp). Every refusal is judged on the
// machine's state: the pf bytes, release/current and the daemon are as
// they were.
//
// polyforge#188 grades the AIHub-side preflight: the client-server
// interface list tools/aihub-interface-paths.conf refuses on its own;
// the total list tools/aihub-paths.conf's remaining entries pass with
// ONE WARN naming the formal tag, the green tag and every hit path.
// Both lists are this repository's own files, copied into the fixture
// origin (one list, both readers — never a second copy), and the
// representative changed paths are generated from the lists themselves.
//
// Pinned (AC1): the channel takes origin's latest green tag only (its
// commit an ancestor of origin/main and a descendant of the latest
// release tag, and not that tag's own commit), derives
// v<next-patch>-dev.<N> with N the first-parent count
// release-tag..green-commit, builds through make dev-build
// VERSION=<dev version> COMMIT=<green commit> OUT=<staged beside pf>,
// proves the staged binary answers --version with the dev version
// (which matches the release shape and carries no +build metadata,
// AC3), and installs the green commit's tree as release/<dev version>.
// A machine without pf, a missing green tag, a wrong ancestry, missing
// build tools (reported all at once) or a failed build refuses with
// nothing changed. AC4: the gh/git calls the channel makes are reads.
//
// carries: tools/install.sh, tools/green-tag, tools/aihub-paths.conf, tools/aihub-interface-paths.conf, Makefile, tools/package-pi-web.sh, test/w136_install_dev_test.go

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// w136DevCfg shapes one dev-channel fixture world.
type w136DevCfg struct {
	green      string   // "" = default: green tag at main HEAD; "tag" | "base" | "none" | "offmain"
	tools      bool     // the build-tool shims (go, make, node, npm) are on PATH
	touch      []string // repo-relative paths ONE commit after the release tag touches (the AIHub-side change shape, whatever the client commits beside it)
	piweb      bool     // the origin carries an installable pi-web package and npm/node model it
	aheadPath  string   // a repo-relative path ONE commit on main AFTER the green tag touches (main ahead of the tag)
	aheadNvmrc string   // a new .nvmrc that same post-green commit writes (main's pin diverging from the green tree's)
	// polyforge#188: the fixture's two path lists, committed below the
	// release tag so the green commit's tree (and only it) carries them.
	// "" copies the repository's own file; a *Missing flag omits the
	// file (a green tag too old to carry the list); a custom value
	// writes it verbatim (a fixture world of its own).
	ifaceConf    string
	ifaceMissing bool
	totalConf    string
	totalMissing bool
	// renameFrom/renameTo: a file seeded at the base commit that the
	// touch commit moves (the rename the preflight must judge on both
	// its ends)
	renameFrom string
	renameTo   string
	// aheadIfaceConf: the interface list a commit on main AFTER the
	// green tag writes (main's list diverging from the green tree's)
	aheadIfaceConf string
}

// w136Dev is one hermetic dev-channel machine on top of w174Install.
type w136Dev struct {
	*w174Install
	origin    string // the real git repository the gh stub clones
	src       string // the repo root (for copying the real tools into the fixture)
	npmPrefix string // the scratch npm prefix (the piweb fixture)
	tagSHA    string // the commit the v0.2.3 release tag points at
	mainSHA   string // the green tag's commit (main's tip when nothing moves past it)
	aheadSHA  string // the commit main added AFTER the green tag ("" when main did not move)
}

// w136Git runs the real git in dir (the fixture setup itself never
// touches the machine's git config).
func w136Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	full := append([]string{"-C", dir, "-c", "user.name=w136", "-c", "user.email=w136@example.com"}, args...)
	out, err := exec.Command(realGit, full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v (%s)", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w136CommitSHA answers a commit's full sha.
func w136CommitSHA(t *testing.T, dir, ref string) string {
	t.Helper()
	return w136Git(t, dir, "rev-parse", ref+"^{commit}")
}

// w136Write lays out one file of the fixture origin.
func w136Write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// w136NewDev builds the fixture: a real origin repository whose main
// carries the release layout plus the real green-tag tool and path
// list, the release tag v0.2.3 one commit below main's HEAD, a green
// tag by cfg, and the dev shims (gh clone, journaling git, go/npm/node,
// the make dev-build stub) first on PATH.
func w136NewDev(t *testing.T, cfg w136DevCfg) *w136Dev {
	t.Helper()
	in := w174NewInstall(t)
	d := &w136Dev{w174Install: in, origin: filepath.Join(shortTmp(t, "o"), "origin")}
	root := v1Root(t)
	d.src = root
	if err := os.MkdirAll(d.origin, 0o755); err != nil {
		t.Fatal(err)
	}
	w136Git(t, d.origin, "init", "-q", "-b", "main")
	// the release layout (what the release checkout checks), the green
	// tag reader and the AIHub path list — the repository's own files
	for _, dir := range []string{"skills", "workflows"} {
		if out, err := exec.Command("cp", "-R", filepath.Join(root, dir), d.origin+"/").CombinedOutput(); err != nil {
			t.Fatalf("fixture origin tree: %v %s", err, out)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		w136Write(t, filepath.Join(d.origin, "package.json"), string(raw), 0o644)
	}
	for _, tool := range []string{"tools/green-tag", "tools/package-pi-web.sh"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tool)))
		if err != nil {
			t.Fatalf("fixture origin: read %s: %v", tool, err)
		}
		w136Write(t, filepath.Join(d.origin, filepath.FromSlash(tool)), string(raw), 0o755)
	}
	// the two path lists (polyforge#188): the repository's own files —
	// the same files the installer reads from the green commit's
	// checkout — or the cfg's fixture worlds (a custom body, or the
	// file's absence: a green tag too old to carry the list)
	w136ConfFixture(t, root, d.origin, "tools/aihub-paths.conf", cfg.totalConf, cfg.totalMissing)
	w136ConfFixture(t, root, d.origin, "tools/aihub-interface-paths.conf", cfg.ifaceConf, cfg.ifaceMissing)
	w136Write(t, filepath.Join(d.origin, ".nvmrc"), "24\n", 0o644)
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli\n", 0o644)
	if cfg.piweb {
		// the green commit's pi-web tree: the repository's own installable
		// package fixture (the lane-stubs tree: the real bin/ with the
		// install checker, a patch-package stub and a node-pty stand-in
		// whose native module only its install script builds)
		nodeBin, err := exec.LookPath("node")
		if err != nil {
			t.Skip("node is not on the test's PATH")
		}
		pkgDir := filepath.Join(d.origin, "pi-web")
		if out, err := exec.Command(nodeBin, filepath.Join(root, "pi-web", "e2e", "lane-stubs.mjs"), "piweb-package-tree", pkgDir, "--scripts=skipped").CombinedOutput(); err != nil {
			t.Fatalf("lane-stubs piweb-package-tree: %v\n%s", err, out)
		}
		var repoPkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal([]byte(w174ReadFile(t, filepath.Join(root, "pi-web", "package.json"))), &repoPkg); err != nil {
			t.Fatal(err)
		}
		manifest, err := json.MarshalIndent(map[string]any{
			"name":    "@silgrid/pi-web",
			"version": "0.2.0-dev.0",
			"bin":     map[string]string{"pi-web": "bin/pi-web.js"},
			"scripts": map[string]string{"postinstall": repoPkg.Scripts["postinstall"]},
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), append(manifest, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		d.npmPrefix = filepath.Join(shortTmp(t, "n"), "npm-prefix")
	}
	if cfg.renameFrom != "" {
		// the rename's source, seeded below the release tag so the
		// touch commit's move is a real rename in the diff
		w136Write(t, filepath.Join(d.origin, filepath.FromSlash(cfg.renameFrom)), "// w136 rename seed\n", 0o644)
	}
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "base")
	base := w136CommitSHA(t, d.origin, "HEAD")
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli // r1\n", 0o644)
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "the released line")
	w136Git(t, d.origin, "tag", "-a", "-m", "release v0.2.3", "v0.2.3")
	d.tagSHA = w136CommitSHA(t, d.origin, "v0.2.3")
	if len(cfg.touch) > 0 || cfg.renameFrom != "" {
		// one commit INSIDE the release-tag..green range touches the
		// paths (and moves the rename): the AIHub-side change shape the
		// preflight must judge, whatever the client commits beside it
		for _, p := range cfg.touch {
			w136Write(t, filepath.Join(d.origin, filepath.FromSlash(p)), "// w136 AIHub-side change\n", 0o644)
		}
		if cfg.renameFrom != "" {
			w136Git(t, d.origin, "mv", cfg.renameFrom, cfg.renameTo)
		}
		w136Git(t, d.origin, "add", "-A")
		w136Git(t, d.origin, "commit", "-q", "-m", "a change the dev channel must judge")
	}
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli // r2\n", 0o644)
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "client change 1")
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli // r3\n", 0o644)
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "client change 2")
	d.mainSHA = w136CommitSHA(t, d.origin, "HEAD")

	// the green tag: cfg's choice of commit (default: main's HEAD)
	greenAt := d.mainSHA
	switch cfg.green {
	case "none":
		greenAt = ""
	case "tag":
		greenAt = d.tagSHA
	case "base":
		greenAt = base
	case "offmain":
		w136Git(t, d.origin, "checkout", "-q", "-b", "side", d.tagSHA)
		w136Write(t, filepath.Join(d.origin, "internal", "aihub", "core", "side.go"), "package core\n", 0o644)
		w136Git(t, d.origin, "add", "-A")
		w136Git(t, d.origin, "commit", "-q", "-m", "off the main line")
		greenAt = w136CommitSHA(t, d.origin, "HEAD")
		w136Git(t, d.origin, "checkout", "-q", "main")
	}
	if greenAt != "" {
		msg := fmt.Sprintf(`{"commit":%q,"gate_id":"%064d","runner":"gate.py","runner_version":"2","plan_digest":"%064d","engine_source":"tree","gate_id_source":"tree","verified_at":"2026-11-18T00:00:00Z","summary":"w136 fixture gate"}`, greenAt, 1, 2)
		w136Git(t, d.origin, "tag", "-a", "-m", msg, "green/20261118-1", greenAt)
	}
	// main moving past the green tag (the normal state: the gate tags a
	// batch, then development goes on) — whatever this commit touches must
	// never reach the dev channel's build
	if cfg.aheadPath != "" || cfg.aheadNvmrc != "" || cfg.aheadIfaceConf != "" {
		if cfg.aheadPath != "" {
			w136Write(t, filepath.Join(d.origin, filepath.FromSlash(cfg.aheadPath)), "// w136 main moved on\n", 0o644)
		}
		if cfg.aheadNvmrc != "" {
			w136Write(t, filepath.Join(d.origin, ".nvmrc"), cfg.aheadNvmrc, 0o644)
		}
		if cfg.aheadIfaceConf != "" {
			w136Write(t, filepath.Join(d.origin, "tools", "aihub-interface-paths.conf"), cfg.aheadIfaceConf, 0o644)
		}
		w136Git(t, d.origin, "add", "-A")
		w136Git(t, d.origin, "commit", "-q", "-m", "main moved past the green tag")
		d.aheadSHA = w136CommitSHA(t, d.origin, "HEAD")
	}

	// the released version the gh stub serves (the formal channel's
	// latest release — the dev channel's base tag)
	in.release(t, "v0.2.3")

	// the dev shims, first on PATH
	realGit, _ := exec.LookPath("git")
	gh := `#!/bin/sh
echo "gh $*" >> "$W174_JOURNAL"
case "$1 $2" in
"release list") cat "$W174_TAG_FILE"; echo ;;
"release download")
  dir=; pat=; prev=
  for a in "$@"; do
    case "$prev" in
      --dir) dir=$a ;;
      --pattern) pat=$a ;;
    esac
    prev=$a
  done
  cp "$W174_REL/SHA256SUMS" "$dir/" 2>/dev/null
  [ -z "$pat" ] || [ -f "$W174_REL/$pat" ] || exit 1
  [ -z "$pat" ] || cp "$W174_REL/$pat" "$dir/$pat"
  ;;
"repo clone")
  [ -n "$W136_ORIGIN" ] || { echo "gh: no fixture origin" >&2; exit 1; }
  ` + realGit + ` clone -q "$W136_ORIGIN" "$4" || exit 1
  ;;
*) echo "gh: unsupported $*" >&2; exit 1 ;;
esac
exit 0
`
	files := map[string]string{"gh": gh, "git": "#!/bin/sh\necho \"git $*\" >> \"$W174_JOURNAL\"\nexec " + realGit + " \"$@\"\n"}
	if cfg.tools {
		files["go"] = "#!/bin/sh\necho \"go $*\" >> \"$W174_JOURNAL\"\n[ \"$1\" = version ] && echo go1.26.0\nexit 0\n"
		files["npm"] = "#!/bin/sh\necho \"npm $*\" >> \"$W174_JOURNAL\"\nexit 0\n"
		files["node"] = "#!/bin/sh\necho \"node $*\" >> \"$W174_JOURNAL\"\n[ \"$1\" = --version ] && { echo \"${W136_NODE_VER:-v24.1.0}\"; exit 0; }\nexit 0\n"
		files["make"] = `#!/bin/sh
dir=.
prev=
for a in "$@"; do
  case "$prev" in -C) dir=$a ;; esac
  prev=$a
done
tree=$("${W136_REALGIT:-git}" -C "$dir" rev-parse HEAD 2>/dev/null)
echo "make $* built-tree=$tree" >> "$W174_JOURNAL"
[ "${W136_MAKE_FAIL:-}" = 1 ] && { echo "make: dev-build failed" >&2; exit 1; }
case " $* " in
*" dev-build "*)
  ver=; out=
  for a in "$@"; do
    case "$a" in
      VERSION=*) ver=${a#VERSION=} ;;
      OUT=*) out=${a#OUT=} ;;
    esac
  done
  [ -n "$ver" ] && [ -n "$out" ] || { echo "make: dev-build needs VERSION and OUT" >&2; exit 1; }
  cat > "$out" <<'WEOF'
#!/bin/sh
echo "pf $*" >> "$W174_JOURNAL"
{ [ "$1" = --version ] || [ "$1" = version ]; } && { echo DEVVER; exit 0; }
exec "$W136_PF" "$@"
WEOF
  sed -i "s/DEVVER/$(printf '%s' "$ver" | sed 's/&/\\\\&/g')/" "$out"
  chmod 755 "$out"
  echo "dev-build: stamped $ver into $out"
  exit 0 ;;
esac
echo "make: unsupported target: $*" >&2
exit 1
`
	}
	if cfg.piweb {
		// node answers the version check itself and execs the real node
		// for everything else (the install check and the served probe);
		// npm models the shared script's packaging steps and the install:
		// ci and run succeed as no-ops, version rewrites the cwd's
		// package.json, pack tars the cwd as an npm-shaped package, root
		// answers the scratch prefix, install -g unpacks into it, rebuild
		// builds node-pty only when its scripts are allowed (the w344
		// shape)
		nodeBin, err := exec.LookPath("node")
		if err != nil {
			t.Skip("node is not on the test's PATH")
		}
		files["node"] = "#!/bin/sh\necho \"node $*\" >> \"$W174_JOURNAL\"\n[ \"$1\" = --version ] && { echo \"${W136_NODE_VER:-v24.1.0}\"; exit 0; }\nexec " + nodeBin + " \"$@\"\n"
		files["npm"] = `#!/bin/sh
echo "npm $*" >> "$W174_JOURNAL"
last=
for a; do last=$a; done
prefix="$W136_NPM_PREFIX"
case "$1" in
ci|run) exit 0 ;;
version)
  sed -i 's/"version": *"[^"]*"/"version": "'"$2"'"/' package.json
  exit 0 ;;
pack)
  dest=.
  [ "$2" = --pack-destination ] && dest=$3
  v=$(sed -n 's/.*"version": *"\([^"]*\)".*//p' package.json)
  st=$(mktemp -d)
  cp -R . "$st/package"
  tar -czf "$dest/silgrid-pi-web-$v.tgz" -C "$st" package
  rm -rf "$st"
  exit 0 ;;
root) printf '%s
' "$prefix/lib/node_modules"; exit 0 ;;
install)
  pkg="$prefix/lib/node_modules/@silgrid/pi-web"
  rm -rf "$pkg" && mkdir -p "$pkg" && tar -xzf "$last" --strip-components=1 -C "$pkg" || exit 1
  echo 'npm warn install-scripts 3 packages have install scripts not yet covered by allowScripts: @silgrid/pi-web (postinstall), node-pty (install: node scripts/install.js)' >&2
  exit 0 ;;
rebuild)
  ign=0; allow=0
  for a; do case "$a" in --ignore-scripts=false) ign=1 ;; --dangerously-allow-all-scripts) allow=1 ;; esac; done
  [ -d "$PWD/node_modules/node-pty" ] || exit 1
  if [ "$ign" = 1 ] && [ "$allow" = 1 ]; then cd "$PWD/node_modules/node-pty" && exec node scripts/install.js; fi
  echo 'npm warn install-scripts 1 package has install scripts not yet covered by allowScripts: node-pty (install: node scripts/install.js)' >&2
  exit 0 ;;
esac
exit 0
`
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(in.shim, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if !cfg.tools {
		// a sanitized tools directory: the system tools without the
		// build ones, so the one-report refusal names them all
		clean := shortTmp(t, "p")
		seen := map[string]bool{}
		for _, srcdir := range []string{"/usr/bin", "/bin"} {
			ents, err := os.ReadDir(srcdir)
			if err != nil {
				continue
			}
			for _, e := range ents {
				name := e.Name()
				if seen[name] || name == "go" || name == "make" || name == "node" || name == "npm" {
					continue
				}
				seen[name] = true
				_ = os.Symlink(filepath.Join(srcdir, name), filepath.Join(clean, name))
			}
		}
		in.path = in.shim + ":" + clean
	}
	in.extraEnv = append(in.extraEnv, "W136_ORIGIN="+d.origin, "W136_PF="+in.pf)
	if cfg.piweb {
		in.extraEnv = append(in.extraEnv, "W136_NPM_PREFIX="+d.npmPrefix)
	}
	return d
}

// run executes tools/install.sh --dev once (an upgrade needs no key
// argument shape beyond the w174 default).
func (d *w136Dev) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	full := append(append([]string{"--dev"}, args...), "w136key", "127.0.0.1")
	return d.runArgs(t, full...)
}

// w136DevVersion is the dev version the default fixture derives: the
// next patch of v0.2.3 over the two client commits after the tag
// (N = the first-parent count release-tag..green-commit).
const w136DevVersion = "v0.2.4-dev.2"

// w136ReadLink answers the release/current target, "" when the link is
// absent (a fresh fixture has no release layout yet — w174NewInstall
// removes the one w174NewEnv laid out).
func w136ReadLink(t *testing.T, d *w136Dev) string {
	t.Helper()
	cur, err := os.Readlink(filepath.Join(d.data, "release", "current"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("readlink release/current: %v", err)
	}
	return cur
}

// w136Unchanged asserts the refusal invariant: the pf bytes, the
// release/current link and the daemon population are as they were, and
// no staged binary was left behind. daemonsBefore lists the pids of the
// daemons the machine already ran (none when the machine carried no
// running daemon).
func w136Unchanged(t *testing.T, d *w136Dev, binBefore, curBefore string, daemonsBefore ...int) {
	t.Helper()
	if got := w174ReadFile(t, d.bin); got != binBefore {
		t.Fatal("the refused dev run replaced the installed pf")
	}
	if left, _ := filepath.Glob(d.bin + ".new.*"); len(left) != 0 {
		t.Fatalf("a staged binary was left behind: %v", left)
	}
	if cur := w136ReadLink(t, d); cur != curBefore {
		t.Fatalf("release/current = %q, want %q", cur, curBefore)
	}
	for _, l := range d.journalLines() {
		if strings.Contains(l, "ops shutdown") {
			t.Fatalf("the refused dev run stopped the daemon: %s", l)
		}
	}
	if daemons := d.daemons(); !w136SamePids(daemons, daemonsBefore) {
		t.Fatalf("the refused dev run changed the daemon population: %v, want %v", daemons, daemonsBefore)
	}
}

// w136SamePids answers whether the two pid lists hold the same pids.
func w136SamePids(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Ints(a)
	sort.Ints(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// w136InstallBin writes an installed pf answering --version with ver.
func w136InstallBin(t *testing.T, d *w136Dev, ver string) string {
	t.Helper()
	body := "#!/bin/sh\n[ \"$1\" = --version ] && { echo " + ver + "; exit 0; }\nexec \"" + d.pf + "\" \"$@\"\n"
	if err := os.MkdirAll(filepath.Dir(d.bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return body
}

// w136InstallCurrent lays out an installed release layout —
// release/<ver> carrying a tree, release/current pointing at it — and
// answers the current link's target: the installed state a refusal
// must leave untouched (an empty machine protects nothing).
func w136InstallCurrent(t *testing.T, d *w136Dev, ver string) string {
	t.Helper()
	rel := filepath.Join(d.data, "release", ver)
	if err := os.MkdirAll(filepath.Join(rel, "skills", "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "skills", "polyforge", "marker"), []byte(ver), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ver, filepath.Join(d.data, "release", "current")); err != nil {
		t.Fatal(err)
	}
	return ver
}

// w136Live starts the fixture machine's daemon — the real daemon over
// this scratch data dir, through `pf daemon ensure` — and answers its
// pid: the running daemon a refusal must leave serving.
func w136Live(t *testing.T, d *w136Dev) int {
	t.Helper()
	if code, out, errb := d.ensure(t); code != 0 {
		t.Fatalf("pf daemon ensure: exit %d\n%s%s", code, out, errb)
	}
	return d.oneDaemon(t, 30*time.Second)
}

// TestW136InstallDevGreenBuildsAndInstalls (AC1, AC3): the channel takes
// origin's latest green tag, derives v0.2.4-dev.2 on the fixture's
// history, builds through make dev-build with that version and the
// green commit, installs the built pf and the green commit's tree as
// release/<dev version>, and a second run changes nothing.
func TestW136InstallDevGreenBuildsAndInstalls(t *testing.T) {
	d := w136NewDev(t, w136DevCfg{green: "", tools: true})
	w136InstallBin(t, d, "v0.2.3")
	d.resetJournal(t)

	out, code := d.run(t)
	if code != 0 {
		t.Fatalf("install.sh --dev: exit %d\n%s", code, out)
	}
	// the build call: make dev-build VERSION=<dev version> COMMIT=<green
	// commit> OUT=<staged> — N is the first-parent count, the version the
	// next patch of the release tag (AC1), release-shaped with no +
	// metadata (AC3)
	var makeLine string
	for _, l := range d.journalLines() {
		if strings.HasPrefix(l, "make -C ") && strings.Contains(l, " dev-build ") {
			makeLine = l
			break
		}
	}
	if makeLine == "" {
		t.Fatalf("no make dev-build call in the journal:\n%s", strings.Join(d.journalLines(), "\n"))
	}
	if !strings.Contains(makeLine, "VERSION="+w136DevVersion) || !strings.Contains(makeLine, "COMMIT="+d.mainSHA) {
		t.Fatalf("make dev-build argv %q must name VERSION=%s COMMIT=%s (the green commit)", makeLine, w136DevVersion, d.mainSHA)
	}
	if strings.Contains(w136DevVersion, "+") || !strings.HasPrefix(w136DevVersion, "v0.2.4-dev.") {
		t.Fatalf("the dev version %q must be release-shaped and carry no +build metadata", w136DevVersion)
	}
	// the installed pf answers the dev version on both spellings
	for _, arg := range []string{"--version", "version"} {
		got, err := exec.Command(d.bin, arg).Output()
		if err != nil || strings.TrimSpace(string(got)) != w136DevVersion {
			t.Fatalf("pf %s after the dev install = %q (%v), want %s", arg, got, err, w136DevVersion)
		}
	}
	// the green commit's tree is release/<dev version> and current points at it
	rel := filepath.Join(d.data, "release", w136DevVersion)
	if _, err := os.Stat(filepath.Join(rel, "skills", "polyforge", "SKILL.md")); err != nil {
		t.Fatalf("the dev release checkout %s does not carry the green commit's tree: %v", rel, err)
	}
	if cur, err := os.Readlink(filepath.Join(d.data, "release", "current")); err != nil || cur != w136DevVersion {
		t.Fatalf("release/current = %q (%v), want %s", cur, err, w136DevVersion)
	}

	// a second run changes nothing: the same green tag derives the same
	// version, and no build runs
	d.resetJournal(t)
	out2, code2 := d.run(t)
	if code2 != 0 {
		t.Fatalf("the second dev run: exit %d\n%s", code2, out2)
	}
	for _, l := range d.journalLines() {
		if strings.Contains(l, " dev-build ") {
			t.Fatalf("the second run rebuilt: %s", l)
		}
	}
}

// TestW136InstallDevRefusals (AC1): every prepare-phase refusal leaves
// the machine exactly as it was.
func TestW136InstallDevRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   w136DevCfg
		env   string
		noBin bool
		want  []string // substrings of the refusal
	}{
		{"no green tag", w136DevCfg{green: "none", tools: true}, "", false,
			[]string{"no green tag on origin"}},
		{"green tag at the release tag's commit", w136DevCfg{green: "tag", tools: true}, "", false,
			[]string{"use the formal channel"}},
		{"green tag before the release tag", w136DevCfg{green: "base", tools: true}, "", false,
			[]string{"not a descendant of the release tag"}},
		{"green tag off main", w136DevCfg{green: "offmain", tools: true}, "", false,
			[]string{"not an ancestor of origin/main"}},
		{"no installed pf", w136DevCfg{green: "", tools: true}, "", true,
			[]string{"the formal channel"}},
		{"build tools missing", w136DevCfg{green: "", tools: false}, "", false,
			// the one report names every missing build tool with its own
			// obtain hint line — a bare "go" substring would match almost
			// any output (review r1 N5)
			[]string{"missing required tools",
				"go      - (--dev)", "make    - (--dev)",
				"node    - (--dev)", "npm     - (--with-pi-web, --dev)"}},
		{"build fails", w136DevCfg{green: "", tools: true}, "W136_MAKE_FAIL=1", false,
			[]string{"building pf", "failed"}},
		{"node major mismatch", w136DevCfg{green: "", tools: true}, "W136_NODE_VER=v22.0.0", false,
			[]string{"is not major 24"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := w136NewDev(t, tc.cfg)
			binBody := "not installed"
			if !tc.noBin {
				binBody = w136InstallBin(t, d, "v0.2.3")
			}
			curBefore := w136ReadLink(t, d)
			if tc.env != "" {
				k, v, _ := strings.Cut(tc.env, "=")
				d.extraEnv = append(d.extraEnv, tc.env)
				_ = k + v
			}
			d.resetJournal(t)
			out, code := d.run(t)
			if code == 0 {
				t.Fatalf("the dev run over %q must refuse; it installed:\n%s", tc.name, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal lacks %q:\n%s", want, out)
				}
			}
			if tc.noBin {
				// no pf to keep: the refusal must build and start nothing
				if _, err := os.Stat(d.bin); !os.IsNotExist(err) {
					t.Fatal("the refused dev run created a pf")
				}
				if daemons := d.daemons(); len(daemons) != 0 {
					t.Fatalf("the refused dev run started a daemon: %v", daemons)
				}
				for _, l := range d.journalLines() {
					if strings.Contains(l, " dev-build ") {
						t.Fatalf("the refused dev run built: %s", l)
					}
				}
			} else {
				w136Unchanged(t, d, binBody, curBefore)
			}
			_ = binBody
		})
	}
}

// w136ConfFixture lays out one path list of the fixture origin from the
// repository's own file — the same file the installer reads from the
// green commit's checkout, one list, never a second copy — or from the
// cfg's fixture world: a custom body, or the file's absence (a green
// tag too old to carry the list).
func w136ConfFixture(t *testing.T, root, origin, rel, custom string, missing bool) {
	t.Helper()
	if missing {
		return
	}
	body := custom
	if body == "" {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("fixture origin: read %s: %v", rel, err)
		}
		body = string(raw)
	}
	w136Write(t, filepath.Join(origin, filepath.FromSlash(rel)), body, 0o644)
}

// w136ConfPaths reads one of the repository's own path lists (the same
// file the installer reads from the green commit's checkout) and
// answers one representative changed path per entry: the directory
// prefix plus a nested file (a /** entry covers any depth), the exact
// path itself otherwise.
func w136ConfPaths(t *testing.T, rel string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasSuffix(l, "/**") {
			entries[l] = strings.TrimSuffix(l, "/**") + "/sql/v0.2.4/upgrade.sql"
		} else {
			entries[l] = l
		}
	}
	if len(entries) == 0 {
		t.Fatal(rel + " declares no entry")
	}
	return entries
}

// w136ServerOnlyPaths answers one representative changed path per entry
// of the total list that is no interface entry — the AIHub-side changes
// that pass the dev channel with a WARN since polyforge#188. The paths
// are generated from the repository's own two lists, never a second
// copy: an entry the interface list names is excluded whatever its
// representative path.
func w136ServerOnlyPaths(t *testing.T) []string {
	t.Helper()
	total := w136ConfPaths(t, "tools/aihub-paths.conf")
	iface := w136ConfPaths(t, "tools/aihub-interface-paths.conf")
	var out []string
	for entry, path := range total {
		if _, hit := iface[entry]; !hit {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("tools/aihub-paths.conf carries no entry outside tools/aihub-interface-paths.conf")
	}
	return out
}

// w136DiffJournalPinned asserts the preflight's verdict was drawn from
// the release-tag..green-commit diff: the journal records the read.
func w136DiffJournalPinned(t *testing.T, d *w136Dev) {
	t.Helper()
	for _, l := range d.journalLines() {
		if strings.Contains(l, "diff --name-only") {
			return
		}
	}
	t.Fatal("the journal records no git diff --name-only: the AIHub-side verdict must come from the release-tag..green-commit diff")
}

// w136TouchedDevVersion is the dev version a fixture whose touch commit
// added one commit inside the release-tag..green range derives: N = 3
// (the touch commit beside the two client commits), the next patch of
// the v0.2.3 release tag — the same first-parent derivation the
// untouched fixture's v0.2.4-dev.2 pins.
const w136TouchedDevVersion = "v0.2.4-dev.3"

// TestW136InstallDevRefusesInterfaceChange (AC1, polyforge#188): a
// green commit whose diff latest-release-tag..green-commit touches ANY
// path of the repository's interface list refuses the dev channel in
// prepare, naming the change and the list it matched against — alone or
// mixed with server-side hits, whose presence never softens the
// interface verdict — and the machine keeps its installed pf, its
// release/current and its running daemon exactly as they were. The
// list is read from the repository's own
// tools/aihub-interface-paths.conf — the same file the installer reads
// from the green commit's checkout, never a second copy in the test.
func TestW136InstallDevRefusesInterfaceChange(t *testing.T) {
	paths := w136ConfPaths(t, "tools/aihub-interface-paths.conf")
	entries := make([]string, 0, len(paths))
	for entry := range paths {
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		path := paths[entry]
		t.Run(entry, func(t *testing.T) {
			d := w136NewDev(t, w136DevCfg{green: "", tools: true, touch: []string{path}})
			binBody := w136InstallBin(t, d, "v0.2.3")
			curBefore := w136InstallCurrent(t, d, "v0.2.3")
			d.resetJournal(t)
			out, code := d.run(t)
			if code == 0 {
				t.Fatalf("a green commit touching %s must refuse the dev channel; it installed:\n%s", path, out)
			}
			for _, want := range []string{"interface changes", path, "tools/aihub-interface-paths.conf", "needs a formal release"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal lacks %q:\n%s", want, out)
				}
			}
			w136DiffJournalPinned(t, d)
			w136Unchanged(t, d, binBody, curBefore)
		})
	}

	// mixed: interface and server-side hits in ONE diff, over a machine
	// that carries an installed release/current AND a running daemon —
	// the interface verdict refuses the whole set and nothing changes
	t.Run("mixed hits over a live machine refuse and change nothing", func(t *testing.T) {
		touch := append([]string{}, w136ServerOnlyPaths(t)...)
		for _, entry := range entries {
			touch = append(touch, paths[entry])
		}
		d := w136NewDev(t, w136DevCfg{green: "", tools: true, touch: touch})
		binBody := w136InstallBin(t, d, "v0.2.3")
		curBefore := w136InstallCurrent(t, d, "v0.2.3")
		pid := w136Live(t, d)
		d.resetJournal(t)
		out, code := d.run(t)
		if code == 0 {
			t.Fatalf("a green commit touching the interface paths must refuse the dev channel; it installed:\n%s", out)
		}
		for _, entry := range entries {
			if !strings.Contains(out, paths[entry]) {
				t.Fatalf("the mixed refusal must name the interface hit %s:\n%s", paths[entry], out)
			}
		}
		if strings.Contains(out, "carries AIHub-side changes") {
			t.Fatalf("a refused run must not print the server-side WARN:\n%s", out)
		}
		w136DiffJournalPinned(t, d)
		w136Unchanged(t, d, binBody, curBefore, pid)
		if !d.answers() {
			t.Fatal("the refused dev run left the running daemon not answering")
		}
	})
}

// TestW136InstallDevWarnsOnAIHubInternalChange (AC1, polyforge#188): a
// green commit whose diff touches ONLY the AIHub-side paths OUTSIDE the
// interface list — the server's insides, its SQL, the deploy tree —
// passes the dev channel, and ONE WARN names the formal tag, the green
// tag and every hit path, stating the two AIHubs stay on their formal
// releases and the changes wait for one. The paths come from the
// repository's own list files (one list, both readers); the version is
// the channel's own first-parent derivation (one commit more than the
// untouched fixture's v0.2.4-dev.2).
func TestW136InstallDevWarnsOnAIHubInternalChange(t *testing.T) {
	assertInstalled := func(t *testing.T, d *w136Dev) {
		t.Helper()
		for _, arg := range []string{"--version", "version"} {
			got, err := exec.Command(d.bin, arg).Output()
			if err != nil || strings.TrimSpace(string(got)) != w136TouchedDevVersion {
				t.Fatalf("pf %s after the warned dev install = %q (%v), want %s", arg, got, err, w136TouchedDevVersion)
			}
		}
		if cur, err := os.Readlink(filepath.Join(d.data, "release", "current")); err != nil || cur != w136TouchedDevVersion {
			t.Fatalf("release/current = %q (%v), want %s", cur, err, w136TouchedDevVersion)
		}
	}
	for _, path := range w136ServerOnlyPaths(t) {
		t.Run(path, func(t *testing.T) {
			d := w136NewDev(t, w136DevCfg{green: "", tools: true, touch: []string{path}})
			w136InstallBin(t, d, "v0.2.3")
			out, code := d.run(t)
			if code != 0 {
				t.Fatalf("a green commit touching only %s must pass the dev channel with a WARN; exit %d:\n%s", path, code, out)
			}
			for _, want := range []string{"WARN:", path, "v0.2.3", "green/20261118-1", "tools/aihub-paths.conf", "formal release"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the WARN lacks %q:\n%s", want, out)
				}
			}
			assertInstalled(t, d)
		})
	}

	// every server-side hit of one diff in ONE WARN block, every path named
	t.Run("all hits in one WARN", func(t *testing.T) {
		all := w136ServerOnlyPaths(t)
		d := w136NewDev(t, w136DevCfg{green: "", tools: true, touch: all})
		w136InstallBin(t, d, "v0.2.3")
		out, code := d.run(t)
		if code != 0 {
			t.Fatalf("a green commit touching only the server-side paths must pass the dev channel; exit %d:\n%s", code, out)
		}
		if n := strings.Count(out, "carries AIHub-side changes"); n != 1 {
			t.Fatalf("the run printed %d AIHub-side WARN blocks, want exactly one:\n%s", n, out)
		}
		for _, path := range all {
			if !strings.Contains(out, path) {
				t.Fatalf("the WARN must name every hit path, lacks %q:\n%s", path, out)
			}
		}
		assertInstalled(t, d)
	})

	// the control: a client-only diff (the default fixture) proceeds
	// with no AIHub-side WARN — the graded lists must not warn on what
	// they do not name
	t.Run("client-only proceeds without the WARN", func(t *testing.T) {
		d := w136NewDev(t, w136DevCfg{green: "", tools: true})
		w136InstallBin(t, d, "v0.2.3")
		out, code := d.run(t)
		if code != 0 {
			t.Fatalf("a client-only green commit must install over the dev channel; exit %d:\n%s", code, out)
		}
		if strings.Contains(out, "carries AIHub-side changes") {
			t.Fatalf("a client-only diff must not print the AIHub-side WARN:\n%s", out)
		}
	})
}

// TestW136InstallDevReadOnlyRemote (AC4): the channel's gh and git calls
// are reads — no release is created, no asset uploaded, no tag or branch
// pushed, no write method called.
func TestW136InstallDevReadOnlyRemote(t *testing.T) {
	d := w136NewDev(t, w136DevCfg{green: "", tools: true})
	w136InstallBin(t, d, "v0.2.3")
	d.resetJournal(t)
	out, code := d.run(t)
	if code != 0 {
		t.Fatalf("install.sh --dev: exit %d\n%s", code, out)
	}
	forbidden := []string{
		"release create", "release upload", "release edit", "release delete",
		"repo create", "repo delete", "repo edit",
		"-X POST", "-X PATCH", "-X PUT", "-X DELETE",
		"git push", "git tag ", "git update-ref", "git commit", "git reset", "git rebase", "git merge ",
	}
	for _, l := range d.journalLines() {
		for _, f := range forbidden {
			if strings.Contains(l, f) {
				t.Fatalf("the dev channel made a remote write: %s (forbidden %q)", l, f)
			}
		}
	}
	// and the reads it needs did happen: the green tag was consulted on
	// the remote, the release tag resolved, the count ran (the AIHub-side
	// diff read is pinned by TestW136InstallDevRefusesInterfaceChange)
	for _, need := range []string{"gh release list", "ls-remote --tags", "rev-list --count --first-parent"} {
		found := false
		for _, l := range d.journalLines() {
			if strings.Contains(l, need) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the dev channel never made its read %q:\n%s", need, strings.Join(d.journalLines(), "\n"))
		}
	}
}

// TestW136InstallDevWithPiWeb (AC6): --dev --with-pi-web packages the
// green commit's pi-web tree LOCALLY through the one shared packaging
// script the formal release runs (never a gh download), stamps the dev
// version into the package, installs it and passes the package's own
// install check.
func TestW136InstallDevWithPiWeb(t *testing.T) {
	d := w136NewDev(t, w136DevCfg{green: "", tools: true, piweb: true})
	w136InstallBin(t, d, "v0.2.3")
	d.resetJournal(t)

	out, code := d.run(t, "--with-pi-web")
	if code != 0 {
		t.Fatalf("install.sh --dev --with-pi-web: exit %d\n%s", code, out)
	}
	var versioned, packed, installed, checked bool
	for _, l := range d.journalLines() {
		switch {
		case strings.HasPrefix(l, "npm version 0.2.4-dev.2 "):
			versioned = true
		case strings.HasPrefix(l, "npm pack --pack-destination"):
			packed = true
		case strings.HasPrefix(l, "npm install -g ") && strings.Contains(l, "/silgrid-pi-web-v0.2.4-dev.2.tgz"):
			installed = true
		case strings.Contains(l, "pi-web-install-check.js"):
			checked = true
		}
	}
	for what, ok := range map[string]bool{
		"the shared script stamped the dev version into the package":     versioned,
		"the shared script packed the tarball":                           packed,
		"npm installed the packed silgrid-pi-web-v0.2.4-dev.2.tgz":       installed,
		"the package's own install check ran over the installed package": checked,
	} {
		if !ok {
			t.Fatalf("%s; journal:\n%s", what, strings.Join(d.journalLines(), "\n"))
		}
	}
	// no gh download: the dev channel's Pi Web never touches the release
	for _, l := range d.journalLines() {
		if strings.HasPrefix(l, "gh release download") {
			t.Fatalf("the dev channel downloaded a release asset: %s", l)
		}
	}
	// the installed package carries the dev version and the run says so
	if pkg := w174ReadFile(t, filepath.Join(d.npmPrefix, "lib", "node_modules", "@silgrid", "pi-web", "package.json")); !strings.Contains(pkg, `"version": "0.2.4-dev.2"`) {
		t.Fatalf("the installed @silgrid/pi-web's package.json does not carry 0.2.4-dev.2: %s", pkg)
	}
	if !strings.Contains(out, "Pi Web installed from silgrid-pi-web-v0.2.4-dev.2.tgz") {
		t.Fatalf("the run does not report the dev Pi Web install:\n%s", out)
	}
}

// TestW136InstallDevBuildsTheGreenCommitsTree (AC1 review r1 #1/#2): the
// channel builds THE GREEN COMMIT's tree, never main's HEAD — main sits
// ahead of the green tag in the normal state (the gate tags a batch,
// development goes on), and whatever landed after the tag must not reach
// the build, the AIHub-side list, or the node pin.
func TestW136InstallDevBuildsTheGreenCommitsTree(t *testing.T) {
	// the make shim journals the tree its -C directory sits on
	builtTree := func(d *w136Dev) string {
		t.Helper()
		for _, l := range d.journalLines() {
			if strings.HasPrefix(l, "make -C ") && strings.Contains(l, " dev-build ") && strings.Contains(l, "built-tree=") {
				return l[strings.LastIndex(l, "built-tree=")+len("built-tree="):]
			}
		}
		return ""
	}

	t.Run("main ahead of the green tag: the build takes the green commit", func(t *testing.T) {
		d := w136NewDev(t, w136DevCfg{green: "", tools: true,
			aheadPath: "internal/polyforge/cli/verbs_version.go"})
		w136InstallBin(t, d, "v0.2.3")
		d.resetJournal(t)
		out, code := d.run(t)
		if code != 0 {
			t.Fatalf("main being one commit ahead of the green tag must install; exit %d:\n%s", code, out)
		}
		if got := builtTree(d); got != d.mainSHA {
			t.Fatalf("the dev build ran on tree %s, want the green commit %s (main's tip %s must not reach the build: the channel installs only what the gate verified)", got, d.mainSHA, d.aheadSHA)
		}
	})

	t.Run("an AIHub-side change after the green tag neither refuses nor reaches the build", func(t *testing.T) {
		d := w136NewDev(t, w136DevCfg{green: "", tools: true,
			aheadPath: "internal/aihub/core/after-the-tag.go"})
		w136InstallBin(t, d, "v0.2.3")
		d.resetJournal(t)
		out, code := d.run(t)
		if code != 0 {
			t.Fatalf("a change main made AFTER the green tag is not in the installed tree and must not refuse (the AIHub preflight judges the release-tag..green diff only); exit %d:\n%s", code, out)
		}
		if got := builtTree(d); got != d.mainSHA {
			t.Fatalf("the dev build ran on tree %s, want the green commit %s: main's post-green AIHub-side change must not be built", got, d.mainSHA)
		}
	})

	t.Run("the node pin comes from the green commit's tree", func(t *testing.T) {
		d := w136NewDev(t, w136DevCfg{green: "", tools: true,
			aheadPath: "internal/polyforge/cli/verbs_version.go", aheadNvmrc: "22\n"})
		w136InstallBin(t, d, "v0.2.3")
		d.resetJournal(t)
		out, code := d.run(t)
		if code != 0 {
			t.Fatalf("the node major must come from the GREEN commit's .nvmrc (24), not main's diverged one (22); exit %d:\n%s", code, out)
		}
		if got := builtTree(d); got != d.mainSHA {
			t.Fatalf("the dev build ran on tree %s, want the green commit %s", got, d.mainSHA)
		}
	})
}
