package test

// w25_verify_scope_frozen_contrast_test.go: the real-history byte-identical
// contrast for. The frozen seed script and the release-shipped
// engine (driven by this repository's `.polyforge/verify-scope.conf`) must
// print the same report for the same range, including the last ten real
// first-parent merge ranges of this repository.
//
// The engine has gained two selection rules over the frozen seed
// (polyforge#111, owner-confirmed): the Go analyzer consumes the declared
// go_harness directory's carries: declarations, and the conf's
// scoped_command= lines. Both divergences are modelled by an independent
// derivation (w111SelectionAppended, the audit's w320AuditAppended pattern):
// the additions are computed from the worktree's conf and harness headers
// and the frozen report's own path lines, never from the engine's output,
// so everything else must still match byte for byte — and a malformed
// carries declaration or scoped_command key, whose fail-closed divergence
// the derivation does not model, still reds the contrast as the guard it is.
//
// A throwaway worktree has no front-end build, so the console's
// `//go:embed all:dist` matches no file there. The engine tolerates that (an
// embed pattern changes no package's dependencies) while the frozen seed's
// plain `go list` fails on it and falls back to `full`, so each worktree gets
// a placeholder dist file first — what the front-end build every bar run
// starts with gives — and both sides list the same package graph. Both still
// fall back to `full` identically when `go list` cannot run for any other
// reason; the stdout comparison is the oracle either way.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25FrozenContrast' -count=1

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// w25ioMerges lists the last n first-parent merge commits, newest first.
func w25ioMerges(t *testing.T, root string, n int) []string {
	t.Helper()
	c := exec.Command("git", "-C", root, "log", "--first-parent", "--merges", "--format=%H", "-n", "10")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git log merges: %v", err)
	}
	var merges []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			merges = append(merges, line)
		}
	}
	if len(merges) > n {
		merges = merges[:n]
	}
	return merges
}

// w25ioWorktree adds a detached throwaway worktree at sha and answers its dir.
func w25ioWorktree(t *testing.T, root, sha string) (dir string, cleanup func()) {
	t.Helper()
	base := t.TempDir()
	dir = filepath.Join(base, "wt")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", dir, sha).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", sha, err, out)
	}
	cleanup = func() {
		_, _ = exec.Command("git", "-C", root, "worktree", "remove", "--force", dir).CombinedOutput()
		_, _ = exec.Command("git", "-C", root, "worktree", "prune").CombinedOutput()
	}
	return dir, cleanup
}

// TestW25FrozenContrastOnRealRanges replays the last ten merge ranges in
// throwaway worktrees and requires byte-identical stdout.
func TestW25FrozenContrastOnRealRanges(t *testing.T) {
	root := w25engRoot(t)
	frozen := filepath.Join(root, "test", "testdata", "verify-scope", "verify-scope-go.sh")
	conf := filepath.Join(root, ".polyforge", "verify-scope.conf")
	if _, err := os.Stat(frozen); err != nil {
		t.Fatalf("the frozen seed must exist: %v", err)
	}
	if _, err := os.Stat(conf); err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	merges := w25ioMerges(t, root, 10)
	if len(merges) == 0 {
		t.Skip("no merge history in this clone (shallow checkout)")
	}
	for _, merge := range merges {
		merge := merge
		t.Run(merge[:12], func(t *testing.T) {
			base := strings.TrimSpace(w25engGit(t, root, "rev-parse", merge+"^1"))
			dir, cleanup := w25ioWorktree(t, root, merge)
			defer cleanup()
			// The placeholder front-end build (see the header); dist is ignored,
			// so it is in no diff either side reads.
			if fi, err := os.Stat(filepath.Join(dir, "webui")); err == nil && fi.IsDir() {
				if err := os.MkdirAll(filepath.Join(dir, "webui", "dist"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "webui", "dist", "index.html"), []byte("<!doctype html>\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			w25engCopy(t, frozen, filepath.Join(dir, ".polyforge", "verify-scope.sh"), 0o755)
			w25engCopy(t, conf, filepath.Join(dir, ".polyforge", "verify-scope.conf"), 0o644)
			// The engine is new in this change, so an older range's worktree does
			// not carry it; install the current release tree's engine there.
			w25engCopy(t, filepath.Join(root, "workflows", "_shared", "scripts", "verify-scope"), filepath.Join(dir, "workflows", "_shared", "scripts", "verify-scope"), 0o755)
			// the current engine's library travels with it, like the release
			// tree's: an older range's worktree carries an older pfstep.
			w25engCopyLib(t, dir)
			frozenOut, frozenErr, frozenCode := w25engRun(t, dir, filepath.Join(dir, ".polyforge", "verify-scope.sh"), nil, "--base", base)
			if frozenCode != 0 {
				t.Fatalf("frozen seed exited %d: %s", frozenCode, frozenErr)
			}
			engineOut, engineErr, engineCode := w25engRun(t, dir, filepath.Join(dir, "workflows", "_shared", "scripts", "verify-scope"), nil, "--base", base)
			if engineCode != 0 {
				t.Fatalf("engine exited %d: %s", engineCode, engineErr)
			}
			// The frozen seed predates the dependency-audit gate: the engine's
			// full report is the seed's plus exactly the appended audit line.
			frozenOut = w320AuditAppended(t, frozenOut, dir, filepath.Join(dir, "workflows", "_shared", "scripts"))
			// and it predates polyforge#111's two selection rules: the scoped
			// report gains the harness carries names and the conf's
			// scoped_command commands (see the file header).
			frozenOut = w111SelectionAppended(t, frozenOut, dir)
			if frozenOut != engineOut {
				t.Fatalf("engine stdout differs from the frozen seed\n--- frozen ---\n%s--- engine ---\n%s", frozenOut, engineOut)
			}
			_ = w25engParse(t, engineOut)
		})
	}
}

// ---------------------------------------------------------------------------
// The polyforge#111 known-divergence derivation: the two selection rules the
// engine gained over the frozen seed, recomputed here from the inputs the
// seed itself saw (the worktree's conf and harness headers, the frozen
// report's path lines), never from the engine's output.
// ---------------------------------------------------------------------------

// w111GlobRE translates one shell-case glob the way the engine's
// pfstep/goglob case_match does: `*` crosses `/`, `?` is one character,
// `[...]` a class negated by `!` (RE2 read both sides), `\\` escapes, the
// whole path matched. The globs this derivation reads (conf keys and
// carries: headers) use no other syntax; a pattern RE2 refuses matches
// nothing on either side.
func w111GlobRE(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		switch c := runes[i]; c {
		case '*':
			b.WriteString(`(?s:.*)`)
		case '?':
			b.WriteString(`.`)
		case '\\':
			if i+1 < len(runes) {
				i++
				b.WriteString(regexp.QuoteMeta(string(runes[i])))
			}
		case '[':
			j := strings.IndexRune(glob[i+1:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			cls := glob[i+1 : i+1+j]
			if strings.HasPrefix(cls, "!") {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + cls + "]")
			i += 1 + j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}

// w111CaseMatch is case_match's twin for the paths this derivation sees.
func w111CaseMatch(glob, p string) bool {
	re, err := w111GlobRE(glob)
	return err == nil && re.MatchString(p)
}

// w111CarriesGlobs answers the carries: globs a file's opening comment
// block declares (nil for none and for a malformed declaration — whose
// fail-closed divergence the contrast lets red by itself). The
// classification is pfstep's parse_header: a `carries` word followed by
// blanks and a colon declares (several lines union); one followed by any
// other non-word character or the line's end is malformed; any other tail
// is no carries line at all.
func w111CarriesGlobs(src string) []string {
	var globs []string
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") {
			break
		}
		c := strings.TrimLeft(strings.TrimLeft(line, "/#!"), " \t")
		rest, ok := strings.CutPrefix(c, "carries")
		if !ok {
			continue
		}
		val, hasColon := strings.CutPrefix(strings.TrimLeft(rest, " \t"), ":")
		if !hasColon {
			// `carries` at the line's end, or followed by a character that is
			// no word character, is the malformed word-without-colon form.
			if rest == "" {
				return nil
			}
			r0 := rest[0]
			word := (r0 >= 'A' && r0 <= 'Z') || (r0 >= 'a' && r0 <= 'z') || (r0 >= '0' && r0 <= '9') || r0 == '_' || r0 == '-'
			if !word {
				return nil
			}
			continue
		}
		v := strings.TrimSpace(val)
		if v == "" {
			return nil // malformed: an empty carries value
		}
		for _, g := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' }) {
			globs = append(globs, g)
		}
	}
	return globs
}

// w111TestNames answers the `func Test...` names a file declares.
func w111TestNames(src string) []string {
	re := regexp.MustCompile(`(?m)^func (Test[^ (]*)`)
	var names []string
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		names = append(names, m[1])
	}
	return names
}

// w111Conf reads the worktree conf's go_harness, exclude= and
// scoped_command= lines (file order; a malformed scoped_command line is
// dropped — its fail-closed divergence reds the contrast unmodelled).
func w111Conf(t *testing.T, dir string) (harness string, excludes []string, scoped []struct{ glob, cmd string }) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the worktree conf must be readable: %v", err)
	}
	for _, raw := range strings.Split(string(raw), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if v, ok := strings.CutPrefix(line, "go_harness="); ok && harness == "" {
			harness = v
		} else if v, ok := strings.CutPrefix(line, "exclude="); ok {
			excludes = append(excludes, v)
		} else if v, ok := strings.CutPrefix(line, "scoped_command="); ok {
			glob, cmd, ok := strings.Cut(v, "|")
			if ok && glob != "" && cmd != "" {
				scoped = append(scoped, struct{ glob, cmd string }{glob, cmd})
			}
		}
	}
	return harness, excludes, scoped
}

// w111SelectionAppended answers the line report the frozen seed's output
// becomes with the two polyforge#111 selection rules applied: the harness
// carries names merged into (or starting) the E2E_RUN command, then the
// matched scoped_command commands appended in conf order. A full report is
// returned unchanged (neither rule reaches the full tier).
func w111SelectionAppended(t *testing.T, frozenOut, dir string) string {
	t.Helper()
	if !strings.HasPrefix(frozenOut, "mode: scoped\n") {
		return frozenOut
	}
	lines := strings.Split(strings.TrimSuffix(frozenOut, "\n"), "\n")
	var paths, cmds []string
	for _, ln := range lines[1:] {
		if p, ok := strings.CutPrefix(ln, "path: "); ok {
			paths = append(paths, p)
		} else if c, ok := strings.CutPrefix(ln, "command: "); ok {
			cmds = append(cmds, c)
		} else {
			t.Fatalf("frozen line %q is neither a path nor a command line", ln)
		}
	}
	harness, excludes, scopedCmds := w111Conf(t, dir)

	// The harness directory's carries: declarations against the changed paths.
	var carried []string
	if harness != "" {
		listing := w25engGit(t, dir, "-c", "core.quotePath=off", "ls-files", "-co", "--exclude-standard", "--", path.Dir(harness))
		for _, f := range strings.Split(listing, "\n") {
			if f == "" || !strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
			if err != nil {
				t.Fatalf("harness file %s must be readable: %v", f, err)
			}
			globs := w111CarriesGlobs(string(src))
			if len(globs) == 0 {
				continue
			}
			for _, p := range paths {
				if slices.ContainsFunc(globs, func(g string) bool { return w111CaseMatch(g, p) }) {
					carried = append(carried, w111TestNames(string(src))...)
					break
				}
			}
		}
	}
	carried = w111SortedUnique(carried)

	// The conf's scoped_command lines, in file order, on the first matching
	// changed path an exclude= match has not removed.
	var added []string
	for _, sc := range scopedCmds {
		for _, p := range paths {
			if slices.ContainsFunc(excludes, func(g string) bool { return w111CaseMatch(g, p) }) {
				continue
			}
			if w111CaseMatch(sc.glob, p) {
				added = append(added, sc.cmd)
				break
			}
		}
	}

	// Merge the carried names into the E2E_RUN command (replacing the seed's
	// in place, or appending one the seed never had), then the scoped_command
	// commands after it — the engine's scoped order.
	prefix := "E2E_RUN='^("
	at := -1
	for i, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			at = i
		}
	}
	if len(carried) > 0 {
		if at < 0 {
			cmds = append(cmds, "E2E_RUN='^("+strings.Join(carried, "|")+")$' sh "+harness)
		} else {
			inner := strings.TrimSuffix(strings.TrimPrefix(cmds[at], prefix), ")$' sh "+harness)
			cmds[at] = "E2E_RUN='^(" + strings.Join(w111SortedUnique(append(strings.Split(inner, "|"), carried...)), "|") + ")$' sh " + harness
		}
	}
	cmds = append(cmds, added...)

	out := "mode: scoped\n"
	for _, p := range paths {
		out += "path: " + p + "\n"
	}
	for _, c := range cmds {
		out += "command: " + c + "\n"
	}
	return out
}

// w111SortedUnique sorts and dedups.
func w111SortedUnique(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
