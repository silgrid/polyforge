package memory

// modulemap.go: the doc.module-map convention. A module map is a
// revisioned shared document: one memory per revision, titled
// "<repo>/<module> rev<N>", tagged with the repo and the module, and
// carrying a "baseline @<commit> <YYYY-MM-DD>" line in its body. Create
// refuses an entry that misses any of these (checkModuleMap).

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/silgrid/polyforge/internal/common/api"
)

// moduleMapType is the one type the convention applies to.
const moduleMapType = "doc.module-map"

var (
	moduleTitleRe = regexp.MustCompile(`^(?s)(.+) rev([0-9]+)$`)
	baselineRe    = regexp.MustCompile(`^baseline @[0-9a-f]{7,40} [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// moduleKey is a module map's identity: the title's repo and module.
type moduleKey struct{ repo, module string }

// parseModuleTitle reads "<repo>/<module> rev<N>" (surrounding space
// trimmed; the key cut at the first "/"; both segments non-empty and
// free of whitespace; N a decimal >= 1). ok is false for any other
// title: no key is ever guessed.
func parseModuleTitle(title string) (key moduleKey, rev int, ok bool) {
	m := moduleTitleRe.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		return moduleKey{}, 0, false
	}
	repo, module, cut := strings.Cut(m[1], "/")
	if !cut || !segmentOK(repo) || !segmentOK(module) {
		return moduleKey{}, 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n < 1 {
		return moduleKey{}, 0, false
	}
	return moduleKey{repo, module}, n, true
}

func segmentOK(s string) bool {
	return s != "" && !strings.ContainsFunc(s, unicode.IsSpace)
}

// hasBaselineLine reports whether some body line reads
// "baseline @<commit> <YYYY-MM-DD>" (position free; the date by shape).
func hasBaselineLine(body string) bool {
	for line := range strings.Lines(body) {
		if baselineRe.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// checkModuleMap enforces the convention for doc.module-map (every other
// type passes untouched) and names every missing piece in one MEM-400.
// body is the document body Create stores (client frontmatter dropped).
func checkModuleMap(typ string, title *string, tags []string, body string) error {
	if typ != moduleMapType {
		return nil
	}
	var missing []string
	got := "nothing"
	if title != nil && strings.TrimSpace(*title) != "" {
		got = strconv.Quote(*title)
	}
	var key moduleKey
	ok := false
	if title != nil {
		key, _, ok = parseModuleTitle(*title)
	}
	if !ok {
		missing = append(missing,
			`title must have the form "<repo>/<module> rev<N>" (N a decimal >= 1), got `+got,
			"tags must contain the repo and the module named by the title")
	} else {
		for _, name := range slices.Compact([]string{key.repo, key.module}) {
			if !slices.Contains(tags, name) {
				missing = append(missing, "missing tag "+strconv.Quote(name))
			}
		}
	}
	if !hasBaselineLine(body) {
		missing = append(missing, `body must carry a line "baseline @<commit> <YYYY-MM-DD>"`)
	}
	if len(missing) == 0 {
		return nil
	}
	return errf(api.ErrMemBadInput, "", "doc.module-map convention: %s", strings.Join(missing, "; "))
}

// rankRevisions sets every candidate's anchor and revRank (sortScored's
// leading keys). A doc.module-map row whose title parses has a module
// key (from the row's title column, never the body); among the
// candidates sharing a key, the one with the latest updated_at (ties:
// id ascending) is the current revision, and every revision of the key
// takes the current one's score as its anchor and its own place by
// updated_at descending as its revRank. So a key's revisions sit
// together where its current revision ranks, newest first, whatever
// their bm25; every other candidate keeps anchor = score, revRank 0.
// The values are fixed per candidate over the whole set, so the order
// is a total preorder and identical on every page cut from it.
func rankRevisions(all []scoredCand) {
	groups := map[moduleKey][]int{}
	for i := range all {
		all[i].anchor, all[i].revRank = all[i].score, 0
		if all[i].typ != moduleMapType {
			continue
		}
		if key, _, ok := parseModuleTitle(all[i].title); ok {
			groups[key] = append(groups[key], i)
		}
	}
	for _, idx := range groups {
		slices.SortFunc(idx, func(x, y int) int {
			a, b := all[x], all[y]
			if c := b.updated.Compare(a.updated); c != 0 {
				return c
			}
			return strings.Compare(a.id, b.id)
		})
		anchor := all[idx[0]].score
		for r, i := range idx {
			all[i].anchor, all[i].revRank = anchor, r
		}
	}
}
