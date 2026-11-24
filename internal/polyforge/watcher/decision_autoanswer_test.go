package watcher

// decision_autoanswer_test.go: the automatic reply is its own decision
// class with a fixed phrase (polyforge#239) — the decision feed shows
// 「已自动按推荐继续」 as an information entry, never as a question that
// still needs a human.

import "testing"

func TestAutoAnswerIsItsOwnDecisionClass(t *testing.T) {
	d, ok := Classify("auto_answered", nil)
	if !ok {
		t.Fatal("auto_answered is not decision-worthy")
	}
	if d.Class != "auto_answered" || d.Phrase != "已自动按推荐继续" || d.Attention {
		t.Fatalf("auto_answered = %+v, want class auto_answered, the fixed phrase and no attention", d)
	}
	found := false
	for _, k := range Kinds() {
		if k == "auto_answered" {
			found = true
		}
	}
	if !found {
		t.Fatal("Kinds does not name auto_answered")
	}
}
