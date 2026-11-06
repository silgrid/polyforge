package driver

// static_c4_test.go: the two static checks of workflow contract 4's stage
// C3.
//
// ① The execution plane — the daemon, the driver and the runtime — names
// no lane's vocabulary: no identifier and no string literal of its
// non-test Go code carries the integration lane's pool, effects, fix
// tickets, integration moves, hold clears, attributions, landing or
// integration batch. Those live in the lane (its skills and the `pf step
// hub` writes they register) and reach the daemon only as opaque wrap
// attrs. An identifier counts when one of its words (camelCase or
// snake_case) is the term; a string literal when it is the term itself or
// a JSON tag of it, or holds one of the multi-word terms anywhere —
// English prose ("the pool of workers") is no lane vocabulary. Exempt are
// the literals listed in laneVocabCoincidences with what they are.
//
// ② No step-side code reads execution.json, the daemon's private progress
// record: neither the code `pf step` runs in a step process (the CLI's
// step face) nor the pre-written step commands' packages, nor any file
// under a lane's scripts (workflows/**/scripts).

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// laneVocab are the integration lane's terms ①: single words and the
// multi-word ones (snake_case).
var laneVocab = []string{"pool", "effects", "fix_tickets", "integration_moves", "hold_clears", "attributions", "landing", "integration_batch"}

// laneVocabCoincidences are literals that are a term but name something
// else (file:literal → what it is).
var laneVocabCoincidences = map[string]string{
	"driver/supervision.go:pool": "a model error's infrastructure noun (modelInfraNouns: \"model pool not found\")",
}

// identWords splits an identifier into its lower-case words (camelCase,
// acronyms and snake_case).
func identWords(id string) []string {
	var out []string
	var cur []rune
	rs := []rune(id)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = nil
	}
	for i, r := range rs {
		if r == '_' {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// laneVocabHits answers the lane-vocabulary tokens of one Go source:
// "<line>: <token> (<term>)", and each coincidence key it met.
func laneVocabHits(src []byte, rel string, met map[string]bool) []string {
	fset := token.NewFileSet()
	file := fset.AddFile(rel, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	var hits []string
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		var term string
		switch tok {
		case token.IDENT:
			words := "_" + strings.Join(identWords(lit), "_") + "_"
			for _, w := range laneVocab {
				if strings.Contains(words, "_"+w+"_") {
					term = w
				}
			}
		case token.STRING:
			v, err := strconv.Unquote(lit)
			if err != nil {
				continue
			}
			for _, w := range laneVocab {
				multi := strings.Contains(w, "_") && strings.Contains(v, w)
				tag := regexp.MustCompile(`\b\w+:"` + w + `[,"]`).MatchString(v)
				if v == w || multi || tag {
					term = w
				}
			}
			if term != "" {
				if key := rel + ":" + v; laneVocabCoincidences[key] != "" {
					met[key] = true
					continue
				}
			}
		}
		if term != "" {
			hits = append(hits, rel+":"+strconv.Itoa(fset.Position(pos).Line)+": "+lit+" ("+term+")")
		}
	}
	return hits
}

// TestExecutionPlaneNamesNoLaneVocabulary (①).
func TestExecutionPlaneNamesNoLaneVocabulary(t *testing.T) {
	root := filepath.Join("..")
	var hits []string
	met := map[string]bool{}
	for _, pkg := range []string{"daemon", "driver", "runtime"} {
		files, err := filepath.Glob(filepath.Join(root, pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no Go file in %s (%v): the scan reads nothing", pkg, err)
		}
		for _, f := range files {
			rel := pkg + "/" + filepath.Base(f)
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			hits = append(hits, laneVocabHits(src, rel, met)...)
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		t.Errorf("the execution plane names the integration lane's vocabulary (workflow contract 4: the lane's skills and `pf step hub` own it):\n%s", strings.Join(hits, "\n"))
	}
	// the list names only what is there: a stale entry hides a new hit
	for key := range laneVocabCoincidences {
		if !met[key] {
			t.Errorf("listed literal %s no longer occurs: drop it", key)
		}
	}
}

// TestLaneVocabularyScanBites: the scan's own probe — each shape it must
// catch is caught, and prose is not.
func TestLaneVocabularyScanBites(t *testing.T) {
	for _, c := range []struct {
		src  string
		hits int
	}{
		{"package p\nvar x = out[\"fix_tickets\"]\n", 1},
		{"package p\nfunc writePool() {}\n", 1},
		{"package p\ntype T struct{ IntegrationBatch map[string]any `json:\"integration_batch,omitempty\"` }\n", 2},
		{"package p\nvar effects = 1\n", 1},
		{"package p\nconst s = \"landing\"\n", 1},
		{"package p\nconst s = \"a retained close without its SessionEnd landing\"\n", 0},
		{"package p\nvar spool, effective = 1, 2\n", 0},
	} {
		if got := len(laneVocabHits([]byte(c.src), "probe.go", map[string]bool{})); got != c.hits {
			t.Errorf("%q: %d hit(s), want %d", c.src, got, c.hits)
		}
	}
}

// stepSideCode are the Go sources a step process runs (②): the CLI's step
// face (`pf step`, step mode) — every other command a step runs is the
// lanes' own script (workflows/_shared/scripts, scanned below) — each
// "<dir>" whole or "<dir>/<file>".
var stepSideCode = []string{"cli/verbs_step.go", "cli/stepctx.go"}

// executionReads answers the code tokens of one Go source that reach
// execution.json: the capsule's path constant, the record's type, or a
// string naming the file.
func executionReads(src []byte, rel string) []string {
	fset := token.NewFileSet()
	file := fset.AddFile(rel, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	var hits []string
	prev := ""
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		hit := false
		switch tok {
		case token.IDENT:
			hit = lit == "ExecutionJSON" || (lit == "Execution" && prev == ".")
		case token.STRING:
			hit = strings.Contains(lit, filepath.Base(capsule.ExecutionJSON))
		}
		if hit {
			hits = append(hits, rel+":"+strconv.Itoa(fset.Position(pos).Line)+": "+lit)
		}
		if tok == token.PERIOD {
			prev = "."
		} else {
			prev = lit
		}
	}
	return hits
}

// TestStepSideCodeReadsNoExecutionRecord (②).
func TestStepSideCodeReadsNoExecutionRecord(t *testing.T) {
	root := filepath.Join("..")
	var hits []string
	for _, entry := range stepSideCode {
		files := []string{filepath.Join(root, entry)}
		if !strings.HasSuffix(entry, ".go") {
			files, _ = filepath.Glob(filepath.Join(root, entry, "*.go"))
		}
		if len(files) == 0 {
			t.Fatalf("%s holds no Go file: the scan reads nothing", entry)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			hits = append(hits, executionReads(src, strings.TrimPrefix(f, root+string(filepath.Separator)))...)
		}
	}
	// every file under a lane's scripts, whatever its language
	scripts := 0
	err := filepath.WalkDir(shippedWorkflowsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(filepath.ToSlash(p), "/scripts/") {
			return err
		}
		scripts++
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(raw), filepath.Base(capsule.ExecutionJSON)) {
			hits = append(hits, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		t.Errorf("step-side code reads execution.json, the daemon's private record (workflow contract 4: a step reads artifacts/handoff.md and its result files):\n%s", strings.Join(hits, "\n"))
	}
	t.Logf("scanned %d lane script file(s)", scripts)
}

// TestExecutionReadScanBites: the scan's own probe.
func TestExecutionReadScanBites(t *testing.T) {
	for _, c := range []struct {
		src  string
		hits int
	}{
		{"package p\nvar p = filepath.Join(dir, capsule.ExecutionJSON)\n", 1},
		{"package p\nvar ex capsule.Execution\n", 1},
		{"package p\nconst p = \"runtime-state/execution.json\"\n", 1},
		{"package p\n// a comment naming execution.json reads nothing\nvar Execution = 1\n", 0},
	} {
		if got := len(executionReads([]byte(c.src), "probe.go")); got != c.hits {
			t.Errorf("%q: %d hit(s), want %d", c.src, got, c.hits)
		}
	}
}
