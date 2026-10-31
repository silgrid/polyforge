package test

// cancel_promise_contract_test.go: every "cancel and re-file" promise in
// the shipped workflows names the reachable path — the reserved `cancel`
// answer at an open question — and no promise depends on a cancel outside
// the workflow. The only re-file mentions without it follow a terminal
// end (failed_rewind_exhausted, the release lane's failed gate), where no
// question is open and nothing needs cancelling.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reFileWithoutCancel answers each "re-file" occurrence of text that is
// neither preceded (within the same promise) by the reserved `cancel`
// answer nor a post-terminal mention.
func reFileWithoutCancel(text string) []string {
	var bad []string
	for i := 0; ; {
		j := strings.Index(text[i:], "re-file")
		if j < 0 {
			return bad
		}
		at := i + j
		lo := at - 300
		if lo < 0 {
			lo = 0
		}
		after := at + 160
		if after > len(text) {
			after = len(text)
		}
		window := text[lo:at]
		switch {
		case strings.Contains(window, "`cancel`"), strings.Contains(text[at:after], "`cancel`"):
		case strings.Contains(window, "failed_rewind_exhausted"), strings.Contains(window, "a human decides"):
		default:
			hi := at + 60
			if hi > len(text) {
				hi = len(text)
			}
			bad = append(bad, text[lo+200:hi])
		}
		i = at + len("re-file")
	}
}

func TestCancelPromiseContract(t *testing.T) {
	root := filepath.Join(v1Root(t), "workflows")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		text := strings.Join(strings.Fields(string(raw)), " ")
		for _, ctx := range reFileWithoutCancel(text) {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s: a re-file promise without the reserved `cancel` answer: …%s…", rel, ctx)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// the negative fixture: the pre-change wording is caught
	if bad := reFileWithoutCancel(strings.Repeat("x ", 200) + "it was misrouted: cancel and re-file on `feature-full`."); len(bad) != 1 {
		t.Fatalf("the check must catch an unreachable cancel promise, got %v", bad)
	}
}
