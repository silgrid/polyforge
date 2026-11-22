// carries: internal/polyforge/cellcfg/*, test/testdata/light-replay/cases.json

package test

// polyforge84_light_replay_corpus_contract_test.go (polyforge#84 fix build,
// B1): the w111 historical miss-replay corpus's polyforge-74 case must allow
// the internal packages that import internal/polyforge/cellcfg today. #190
// made internal/polyforge/components and internal/polyforge/components/seedtool
// import it, but the case's allowed additions were not updated, so
// TestLightHistoricalReplay/polyforge-74 reds on the epic tree.
//
// The reverse dependencies are derived from the Go sources, not pinned as
// literals: a package that joins the cellcfg closure and is not covered by
// the case's old set or allowed additions fails here, so a later change must
// update the corpus in the same commit instead of silently reddening the
// replay. The judgment lives in the corpus, never in this guard.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pf84lrDoc is the slice of cases.json this guard reads.
type pf84lrDoc struct {
	Cases []pf84lrCase `json:"cases"`
}

// pf84lrCase is one corpus case's allowed additions.
type pf84lrCase struct {
	Id         string              `json:"id"`
	Old        map[string][]string `json:"old"`
	AllowedNew map[string][]struct {
		Name string `json:"name"`
	} `json:"allowed_new"`
}

// pf84lrPkgDirs splits one package command into its directory items, the
// same shape the replay's judge uses (w111lrPkgDirs).
func pf84lrPkgDirs(cmd string) []string {
	var out []string
	for _, f := range strings.Fields(cmd) {
		if strings.HasPrefix(f, ".") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

func TestLightReplayCorpusCoversCellcfgReverseDeps(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "test", "testdata", "light-replay", "cases.json"))
	if err != nil {
		t.Fatalf("the replay corpus must ship: %v", err)
	}
	var doc pf84lrDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the replay corpus must parse: %v", err)
	}
	var c *pf84lrCase
	for i := range doc.Cases {
		if doc.Cases[i].Id == "polyforge-74" {
			c = &doc.Cases[i]
			break
		}
	}
	if c == nil {
		t.Fatal("the replay corpus must carry the polyforge-74 case")
	}

	allowed := map[string]bool{}
	for _, cmd := range c.Old["go_pkg_cmds"] {
		for _, dir := range pf84lrPkgDirs(cmd) {
			allowed[dir] = true
		}
	}
	for _, add := range c.AllowedNew["go_pkg_cmds"] {
		for _, dir := range pf84lrPkgDirs(add.Name) {
			allowed[dir] = true
		}
	}

	// Every internal package that imports cellcfg is a reverse dependency of
	// the polyforge-74 change (it touched cellcfg/schema.go), so the case must
	// already carry it in the old set or its allowed additions.
	importers := map[string]bool{}
	err = filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(body), `"github.com/silgrid/polyforge/internal/polyforge/cellcfg"`) {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		importers["./"+filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("the internal packages must be readable: %v", err)
	}

	dirs := make([]string, 0, len(importers))
	for dir := range importers {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if !allowed[dir] {
			t.Errorf("the polyforge-74 case must allow the cellcfg reverse dependency %s: its allowed additions cover %v", dir, sortedKeys(allowed))
		}
	}
}

// sortedKeys answers a set's keys, sorted, for a stable failure message.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
