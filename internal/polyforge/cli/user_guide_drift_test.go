package cli

// user_guide_drift_test.go: the automated drift check over
// docs/user-guide.md (polyforge#241). The guide is the layer above the
// CLI: it names commands in code spans and fenced blocks (its own format
// contract) and links to the surfaces that own the detail. This check is
// the no-silent-knobs corollary made mechanical: a guide naming a pf
// verb or flag that does not exist, or carrying a relative link that
// resolves to no file, is lying docs, and it fails here — naming file
// and line — instead of in a new member's session. The comparison is
// against the real surface, not a curated list: the sets are built the
// same way skill_flags_test.go builds its own (the commands registry,
// the flag sets this package parses, os.Stat for links), so a renamed or
// deleted verb/flag/link fails here the moment the guide still names it.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// guidePath is the guide's repo-relative path (the cli package dir sits
// three levels below the repo root).
var guidePath = filepath.Join("..", "..", "..", "docs", "user-guide.md")

var (
	guidePfRe   = regexp.MustCompile(`\bpf ([a-z][a-z-]*)`)
	guideFlagRe = regexp.MustCompile(`(?:^|[\s\[(|"'=])(--[a-z][a-z-]*)`)
	// guideSpanRe is one single-backtick code span on a non-fence line
	// (the guide's authoring rule: commands never sit in bare prose).
	guideSpanRe = regexp.MustCompile("`([^`\n]+)`")
	// guideInlineRe is an inline or image markdown link's target
	// ([text](target), ![alt](target)); a title after the target is
	// ignored (the capture stops at whitespace).
	guideInlineRe = regexp.MustCompile(`!?\[[^\]]*\]\(\s*<?([^)\s>]*)`)
	// guideRefDefRe is a reference-definition link's target
	// (^  [label]: target).
	guideRefDefRe = regexp.MustCompile(`(?m)^[ \t]{0,3}\[[^\]]+\]:[ \t]*<?([^>\s]+)`)
)

// guideBody answers the guide's raw body. A missing or empty guide
// fails: the repo MUST carry it — it is the drift check's subject.
func guideBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("docs/user-guide.md is the drift check's subject and the repo must carry it: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("docs/user-guide.md is empty: the drift check's subject is missing")
	}
	return string(raw)
}

// guideScanned answers the guide's scanned text, one entry per scanned
// line, each carrying its 1-based guide line number: fenced-block lines
// whole, code spans per span. That is where the guide names commands
// (its format contract); prose like "the pf binary" names no verb.
func guideScanned(body string) [][2]string {
	var out [][2]string
	inFence := false
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, [2]string{strconv.Itoa(i + 1), line})
			continue
		}
		for _, m := range guideSpanRe.FindAllStringSubmatch(line, -1) {
			out = append(out, [2]string{strconv.Itoa(i + 1), m[1]})
		}
	}
	return out
}

// guideVisible answers the guide's link-visible text: the whole
// body, line structure preserved (only line contents are blanked,
// never a newline), with fenced-block lines and single-backtick code
// spans blanked. That is the prose a markdown renderer sees — where
// links live; a '](' inside a fence or a code span is literal text,
// not a link. Preserving the line structure keeps every offset's line
// number the guide's own (guideLine).
func guideVisible(body string) string {
	lines := strings.Split(body, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			lines[i] = ""
			continue
		}
		if inFence {
			lines[i] = ""
			continue
		}
		lines[i] = guideSpanRe.ReplaceAllString(line, "")
	}
	return strings.Join(lines, "\n")
}

// guideLine answers the 1-based line number of an offset into a text
// whose line structure matches the guide's (guideVisible preserves
// it, so a visible-body offset names the guide's own line).
func guideLine(text string, offset int) int {
	return 1 + strings.Count(text[:offset], "\n")
}

// guideSlug answers a GitHub-style heading slug: lowercase, word
// characters and hyphens kept, spaces to hyphens, other punctuation
// dropped. The guide's headings are ASCII words and hyphens (its own
// authoring rule), so the ASCII walk is exact.
func guideSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// TestUserGuideNoDrift is the drift check over docs/user-guide.md:
//
//   - every `pf <verb>` the guide names in a code span or fenced block

// docDriftSets answers the known-surface sets every documented page's
// drift check compares against — built exactly the way
// skill_flags_test.go builds its own: the commands registry (plus
// help, the one verb Run special-cases; that registry is exactly what
// `pf help` prints) for verbs; the global flag set of Run, each verb's
// own Setup flags, and this package's sub-action flag literals
// (srcFlagDefRe / srcFlagVarRe / srcFlagLitRe / srcFlagCaseRe over the
// non-test sources) for flags.
func docDriftSets() (knownVerbs, global, pkg map[string]bool, verbFlags map[string]map[string]bool) {
	knownVerbs = map[string]bool{"help": true}
	for _, c := range commands {
		knownVerbs[c.Name] = true
	}
	global = map[string]bool{}
	for _, n := range []string{"json", "config", "server", "api-key", "socket", "user", "version", "help", "h"} {
		global["--"+n] = true
	}
	verbFlags = map[string]map[string]bool{}
	for _, c := range commands {
		fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
		if c.Setup != nil {
			c.Setup(fs)
		}
		set := map[string]bool{}
		fs.VisitAll(func(f *flag.Flag) { set["--"+f.Name] = true })
		verbFlags[c.Name] = set
	}
	// pkg collects the flag literals this package's sub-actions define
	// or match themselves (file create, ops end-session, chat init,
	// watch subcommands, ...) — the same scan skill_flags_test.go runs.
	pkg = map[string]bool{}
	srcs, err := filepath.Glob("*.go")
	if err != nil {
		panic(err) // a build broke: the scan cannot proceed
	}
	for _, p := range srcs {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			panic(err)
		}
		for _, re := range []*regexp.Regexp{srcFlagDefRe, srcFlagVarRe} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				pkg["--"+m[1]] = true
			}
		}
		for _, re := range []*regexp.Regexp{srcFlagLitRe, srcFlagCaseRe} {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				pkg[m[1]] = true
			}
		}
	}
	return knownVerbs, global, pkg, verbFlags
}

// docDriftCheck checks one documented page's body against the pf
// surface — the core of TestUserGuideNoDrift and TestDocsSetNoDrift
// (docs_drift_test.go), so the doc set and the guide meet the same bar:
//
//   - every `pf <verb>` the page names in a code span or fenced block
//     is a verb of the CLI (docDriftSets' registry);
//   - every `--flag` inside a `pf <verb>` segment (to the next `pf `
//     start or a shell boundary |;#, skill_flags_test.go's
//     segmentation) is known to the CLI: the global flag set, the
//     verb's own Setup flags, a sub-action flag literal of this
//     package, or — for pf daemon — the daemon help text
//     (daemonUsage);
//   - every markdown link target — inline, image or reference
//     definition — resolves: absolute URLs are skipped, a same-file
//     #anchor must slug-match one of the page's own #-headings, and a
//     path target (its #fragment stripped) must exist under the page's
//     own directory (dir, a path from this package's cwd). Cross-file
//     fragments are not checked: path existence is the bar.
//
// name is the page's repo-relative path (error messages carry it).
// Sub-action names (the word after file, ops, chat, result) are
// intentionally not checked: the spec's bar is verbs and flags, and no
// sub-action registry exists as data — they are switch arms.
func docDriftCheck(name, dir, body string) (checkedVerbs, checkedFlags, checkedLinks, skippedLinks int, bad []string) {
	knownVerbs, global, pkg, verbFlags := docDriftSets()
	var checkedVerbsN, checkedFlagsN int
	for _, entry := range guideScanned(body) {
		lineNo, line := entry[0], entry[1]
		locs := guidePfRe.FindAllStringSubmatchIndex(line, -1)
		for i, loc := range locs {
			verb := line[loc[2]:loc[3]]
			if !knownVerbs[verb] {
				bad = append(bad, name+":"+lineNo+": pf "+verb+" is not a pf verb (not in `pf help`) — the page names a verb that does not exist")
				continue
			}
			checkedVerbsN++
			end := len(line)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			seg := line[loc[1]:end]
			// stop at a shell boundary: anything after |, ; or # belongs
			// to another word (another command, a slug, a comment).
			if j := strings.IndexAny(seg, "|;#"); j >= 0 && !strings.Contains(seg[:j], "[") {
				seg = seg[:j]
			}
			for _, m := range guideFlagRe.FindAllStringSubmatch(seg, -1) {
				f := m[1]
				checkedFlagsN++
				known := global[f] || pkg[f] || verbFlags[verb][f]
				if verb == "daemon" {
					known = known || strings.Contains(daemonUsage, f+" ") || strings.Contains(daemonUsage, f+"\n") || strings.Contains(daemonUsage, f+"]")
				}
				if !known {
					bad = append(bad, name+":"+lineNo+": pf "+verb+" "+f+" is not a flag of that verb (not in `pf help "+verb+"`) — the page names a flag that does not exist")
				}
			}
		}
	}

	// The page's own #-headings, as slugs, for same-file anchors.
	// Fenced lines are skipped: a `#` line inside a fence is shell text,
	// not a heading.
	anchors := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if h := strings.TrimLeft(line, " \t"); strings.HasPrefix(h, "#") {
			anchors[guideSlug(strings.TrimSpace(strings.TrimLeft(h, "#")))] = true
		}
	}
	// Links are scanned over the whole visible body, not per line: a
	// link whose [text] wraps onto the next line is still one link, and
	// its line number comes from its offset (guideLine).
	visible := guideVisible(body)
	// checkTarget verifies one relative target: a same-file #anchor
	// must slug-match one of the page's own headings; a path target
	// (its #fragment stripped) must resolve under the page's dir.
	// Absolute URLs never reach here (the callers skip them).
	checkTarget := func(where, target string) {
		if strings.HasPrefix(target, "#") {
			if !anchors[guideSlug(target[1:])] {
				bad = append(bad, where+": #"+target[1:]+" matches no heading of the page — the link is dead")
			}
			return
		}
		path := target
		if i := strings.Index(path, "#"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			return
		}
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			bad = append(bad, where+": "+target+" resolves to no file or directory under "+filepath.ToSlash(dir)+" — the link is dead")
		}
	}
	for _, m := range guideInlineRe.FindAllStringSubmatchIndex(visible, -1) {
		target := visible[m[2]:m[3]]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			skippedLinks++ // absolute URL: not the page's business
			continue
		}
		checkedLinks++
		checkTarget(name+":"+strconv.Itoa(guideLine(visible, m[0])), target)
	}
	for _, m := range guideRefDefRe.FindAllStringSubmatchIndex(visible, -1) {
		target := visible[m[2]:m[3]]
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue // absolute URL: not the page's business
		}
		checkTarget(name+":"+strconv.Itoa(guideLine(visible, m[0]))+" (reference definition)", target)
	}
	// The links guard is exact, not a floor: every '](' a renderer
	// sees outside fences and code spans is one inline or image link,
	// and the scan must have examined exactly that many (checked, or
	// skipped as an absolute URL). A link shape the regex missed fails
	// here instead of passing vacuously.
	if n := strings.Count(visible, "]("); n != checkedLinks+skippedLinks {
		bad = append(bad, fmt.Sprintf("%s carries %d '](' link shapes outside fences and code spans, but the scan examined %d (%d checked, %d skipped as absolute URLs) — the scan lost links", name, n, checkedLinks+skippedLinks, checkedLinks, skippedLinks))
	}
	return checkedVerbsN, checkedFlagsN, checkedLinks, skippedLinks, bad
}

// TestUserGuideNoDrift is the drift check over docs/user-guide.md:
// every pf verb and flag the guide names exists in the real surface and
// every link resolves (docDriftCheck; the sanity floors below guard
// the scan against losing its subject).
func TestUserGuideNoDrift(t *testing.T) {
	body := guideBody(t)
	checkedVerbs, checkedFlags, _, _, bad := docDriftCheck("docs/user-guide.md", filepath.Dir(guidePath), body)
	// Sanity floor: a scan that lost its subject (a broken extraction)
	// would pass vacuously — the counts guard it, the same way
	// skill_flags_test.go's `checked < 20` does.
	if checkedVerbs < 10 {
		t.Errorf("only %d pf commands scanned in docs/user-guide.md — the scan lost its subject", checkedVerbs)
	}
	if checkedFlags < 10 {
		t.Errorf("only %d flags scanned in docs/user-guide.md — the scan lost its subject", checkedFlags)
	}
	if len(bad) > 0 {
		t.Fatalf("docs/user-guide.md drifted from the pf surface:\n%s", strings.Join(bad, "\n"))
	}
}
