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
// carries: tools/install.sh, tools/green-tag, tools/aihub-paths.conf, Makefile, tools/package-pi-web.sh, test/w136_install_dev_test.go

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w136DevCfg shapes one dev-channel fixture world.
type w136DevCfg struct {
	green string // "" = default: green tag at main HEAD; "tag" | "base" | "none" | "offmain"
	tools bool   // the build-tool shims (go, make, node, npm) are on PATH
}

// w136Dev is one hermetic dev-channel machine on top of w174Install.
type w136Dev struct {
	*w174Install
	origin  string // the real git repository the gh stub clones
	src     string // the repo root (for copying the real tools into the fixture)
	tagSHA  string // the commit the v0.2.3 release tag points at
	mainSHA string
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
	if raw, err := os.ReadFile(filepath.Join(root, "tools", "aihub-paths.conf")); err != nil {
		t.Fatal(err)
	} else {
		w136Write(t, filepath.Join(d.origin, "tools", "aihub-paths.conf"), string(raw), 0o644)
	}
	w136Write(t, filepath.Join(d.origin, ".nvmrc"), "24\n", 0o644)
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli\n", 0o644)
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "base")
	base := w136CommitSHA(t, d.origin, "HEAD")
	w136Write(t, filepath.Join(d.origin, "internal", "polyforge", "cli", "verbs_version.go"), "package cli // r1\n", 0o644)
	w136Git(t, d.origin, "add", "-A")
	w136Git(t, d.origin, "commit", "-q", "-m", "the released line")
	w136Git(t, d.origin, "tag", "-a", "-m", "release v0.2.3", "v0.2.3")
	d.tagSHA = w136CommitSHA(t, d.origin, "v0.2.3")
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
		w136Git(t, d.origin, "tag", "-a", "-m", msg, "green/20261118-1")
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
echo "make $*" >> "$W174_JOURNAL"
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
	return d
}

// run executes tools/install.sh --dev once (an upgrade needs no key
// argument shape beyond the w174 default).
func (d *w136Dev) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	full := append([]string{"--dev"}, args...)
	if len(full) == 1 {
		full = append(full, "w136key", "127.0.0.1")
	}
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
// no staged binary was left behind.
func w136Unchanged(t *testing.T, d *w136Dev, binBefore, curBefore string) {
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
	if daemons := d.daemons(); len(daemons) != 0 {
		t.Fatalf("the refused dev run started a daemon: %v", daemons)
	}
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
			[]string{"go", "make", "node", "npm", "missing required tools"}},
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
			if !tc.noBin {
				w136Unchanged(t, d, binBody, curBefore)
			}
		})
	}
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
	// diff read is pinned by TestW136InstallDevRefusesAIHubChange)
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
