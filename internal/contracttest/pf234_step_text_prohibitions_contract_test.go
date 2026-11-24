package test

// carries: workflows/_shared/step-header.md, workflows/_shared/build_subagents.md

// pf234_step_text_prohibitions_contract_test.go (polyforge#234, AC2): the
// two prompt texts every dispatch reads — the shared step header and the
// build sub-agent fragment — keep their never / do-not prohibitions to
// the allowlist below and stay shorter than the word counts they carried
// before this change. A prohibition the text cannot state positively is
// allowed only with the positive goal that replaces it in the same
// paragraph, so a bare "Never X" added later turns this red; the word
// count is the ticket's second half of AC2, measured against the
// pre-change files rather than against whatever the rewrite produced.
//
// The behaviour it observes: the shipped text a step agent is prompted
// with (the files' own sentences and word counts), not the code that
// renders them. The expected values are independent of the implementation:
// 1752 and 1000 are the `wc -w` counts of the two files at da82f499, the
// change's base, and the allowlist is empty because every prohibition in
// both files was rephrased positively. The change it catches: one new
// "Never …" / "do not …" / "不要 …" sentence, a kept hard guard whose
// positive goal was dropped, or text growth back past the baseline.
// First red: on the unrewritten tree the prohibition assertion fails with
// the 15 (step header) and 10 (build fragment) hits, an assertion failure,
// not a compile error.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pf234ProhibitionRe is the ticket's counting rule (owner ruling Q3): the
// never / do-not forms, case-insensitively, plus the Chinese forms.
var pf234ProhibitionRe = regexp.MustCompile(`(?i)\b(?:never|do not|don't)\b|不要|不得`)

// pf234Guard is one allowed prohibition: the sentence that carries it
// (whitespace-flattened) and the positive goal that must sit in the same
// paragraph beside it.
type pf234Guard struct {
	sentence string
	goal     string
}

// pf234Allow is the allowlist. #234 rephrased every prohibition in both
// files into a positive sentence, so it is empty; a future hard guard
// that genuinely cannot be stated positively is added here with its goal.
var pf234Allow = []pf234Guard{}

// pf234WordBaselines are the pre-change word counts (`wc -w` on
// da82f499), never a number read back from the rewritten files.
var pf234WordBaselines = map[string]int{
	"workflows/_shared/step-header.md":     1752,
	"workflows/_shared/build_subagents.md": 1000,
}

// pf234Sentences splits a whitespace-flattened paragraph at sentence ends;
// a trailing fragment without one stays its own sentence.
func pf234Sentences(flat string) []string {
	var out []string
	start := 0
	for i := 0; i < len(flat); i++ {
		switch flat[i] {
		case '.', '!', '?', '。':
			if i+1 < len(flat) && flat[i+1] != ' ' {
				continue
			}
			if s := strings.TrimSpace(flat[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(flat[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// pf234Violations answers, per paragraph, each sentence that carries a
// prohibition with no allowlist entry whose goal is present in that same
// paragraph.
func pf234Violations(paragraphs []string, allow []pf234Guard) []string {
	var out []string
	for _, para := range paragraphs {
		flat := strings.Join(strings.Fields(para), " ")
		for _, sentence := range pf234Sentences(flat) {
			if !pf234ProhibitionRe.MatchString(sentence) {
				continue
			}
			allowed := false
			for _, g := range allow {
				if sentence == g.sentence && strings.Contains(flat, g.goal) {
					allowed = true
					break
				}
			}
			if !allowed {
				out = append(out, sentence)
			}
		}
	}
	return out
}

// TestPF234StepTextProhibitionsAllowlisted is AC2's text assertion: the
// shipped files carry no unallowlisted prohibition and each is shorter
// than its pre-change word count.
func TestPF234StepTextProhibitionsAllowlisted(t *testing.T) {
	for rel, baseline := range pf234WordBaselines {
		raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("polyforge#234: read %s: %v", rel, err)
		}
		body := string(raw)
		if words := len(strings.Fields(body)); words >= baseline {
			t.Errorf("polyforge#234: %s is %d words, not below the %d-word pre-change baseline", rel, words, baseline)
		}
		paragraphs := strings.Split(body, "\n\n")
		for _, bad := range pf234Violations(paragraphs, pf234Allow) {
			t.Errorf("polyforge#234: %s carries an unallowlisted never/do-not prohibition: %q", rel, bad)
		}
	}
}

// TestPF234StepTextProhibitionsCounterexample pins the scanner's coverage
// with fixtures, so a silently loosened rule fails here: a bare English
// and a bare Chinese prohibition are caught, an allowlisted one with its
// goal in the same paragraph passes, and the same guard without its goal
// is caught again.
func TestPF234StepTextProhibitionsCounterexample(t *testing.T) {
	if got := pf234Violations([]string{"Never push to the default branch."}, nil); len(got) != 1 {
		t.Fatalf("the scanner misses a bare English prohibition: %v", got)
	}
	if got := pf234Violations([]string{"不要使用 parallel。"}, nil); len(got) != 1 {
		t.Fatalf("the scanner misses a bare Chinese prohibition: %v", got)
	}
	guard := []pf234Guard{{sentence: "Never push to the default branch.", goal: "keep the branch protected"}}
	if got := pf234Violations([]string{"Never push to the default branch. Keep the branch protected."}, guard); len(got) != 0 {
		t.Fatalf("an allowlisted guard with its positive goal must pass: %v", got)
	}
	if got := pf234Violations([]string{"Never push to the default branch."}, guard); len(got) != 1 {
		t.Fatalf("an allowlisted guard without its positive goal must fail: %v", got)
	}
}
