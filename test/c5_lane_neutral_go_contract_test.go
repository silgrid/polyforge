package test

// c5_lane_neutral_go_contract_test.go (workflow contract 5): integrate is a
// lane like any other, so the code that runs lanes and stores work items
// names it nowhere. No non-test Go file under the daemon, the driver, the
// runtime, the CLI, the daemon's aihub client, the step call ledger, the
// pending writes, the workspace yaml, internal/common or internal/aihub
// mentions it: "integrat" in any case mix, in an identifier, a string
// literal or a comment alike — a comment naming the lane is the first
// step back to special-casing it. Test files are out of scope (a test may
// load the shipped lane set, integrate among it, as its fixture), and so
// is testdata.
//
// The exceptions are c5IntegratAllowed: per file, the lines that may still
// mention the term — each line mentioning it must match one of its file's
// patterns, each pattern with the reason — so a new mention elsewhere in an
// exempt file is red like any other. The list is data: a pattern no line
// matches any more is red too, naming the entry to delete, so the list
// only shrinks.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// c5IntegratRoots are the trees scanned, relative to the repository root.
var c5IntegratRoots = []string{
	"internal/polyforge/daemon",
	"internal/polyforge/driver",
	"internal/polyforge/runtime",
	"internal/polyforge/cli",
	"internal/polyforge/aihubclient",
	"internal/polyforge/stepcall",
	"internal/polyforge/pendingwrites",
	"internal/polyforge/workspace",
	"internal/common",
	"internal/aihub",
}

// c5Allowed is one exempt line shape of a file: a line mentioning the term
// may when it matches Line (a regular expression), for Why.
type c5Allowed struct {
	Line, Why string
}

// c5IntegratAllowed are the lines that may still mention the term, per
// file (see the file comment).
var c5IntegratAllowed = map[string][]c5Allowed{
	"internal/polyforge/daemon/keyedfile.go": {
		{`"integration-ticket-key: "`, "fileKeyed's ticket-key line: it is a data format already written into the content of existing tickets, and renaming it would make the next batch re-file duplicate fix tickets"},
	},
	"internal/polyforge/workspace/workspace.go": {
		{"IntegrateKey|IntegrateRemovedHint|`integrate:`", "the removed root `integrate:` section of .polyforge.yaml: refused whole with its rewrite hint, which names the section and how a batch is filed now"},
		{`^const KeyRenameSection = `, "the title of the contract 4 upgrade section the stale-key hint cites: docs/upgrade.md is append-only, so the title stands"},
	},
}

var c5Integrat = regexp.MustCompile(`(?i)integrat`)

func TestC5GoCodeNamesNoIntegrateLane(t *testing.T) {
	root := ".."
	used := map[string]bool{} // file + "\x00" + pattern → matched a line
	var bad []string
	for _, r := range c5IntegratRoots {
		scanned := 0
		err := filepath.WalkDir(filepath.Join(root, r), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			scanned++
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for n, line := range strings.Split(string(raw), "\n") {
				if !c5Integrat.MatchString(line) {
					continue
				}
				ok := false
				for _, a := range c5IntegratAllowed[rel] {
					if regexp.MustCompile(a.Line).MatchString(line) {
						used[rel+"\x00"+a.Line], ok = true, true
					}
				}
				if !ok {
					bad = append(bad, rel+":"+strconv.Itoa(n+1))
				}
			}
			return nil
		})
		if err != nil || scanned == 0 {
			t.Fatalf("the scanned tree %s: %d Go files, %v", r, scanned, err)
		}
	}
	var stale []string
	for f, as := range c5IntegratAllowed {
		for _, a := range as {
			if !used[f+"\x00"+a.Line] {
				stale = append(stale, f+" `"+a.Line+"`")
			}
		}
	}
	sort.Strings(bad)
	sort.Strings(stale)
	if len(bad) > 0 {
		t.Errorf("the integrate lane is named in code that runs every lane (\"integrat\", any case, comments included) — name the lane's concept generically, or move it into the lane: %s", strings.Join(bad, ", "))
	}
	if len(stale) > 0 {
		t.Errorf("these exempt lines match no line any more — delete their c5IntegratAllowed entries: %s", strings.Join(stale, ", "))
	}
}
