package sessionjsonl

// sessionjsonl_test.go: the reader half of the package (Tail + Record)
// — restored with the summarize lane (polyforge#224; deleted as dead
// code in eeac39d9 when the old watch lane went, live again as the
// prompt's one bounded-tail reader). Parsing never fails per record:
// torn and malformed lines are data; only I/O fails.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write file helper: content exactly as given.
func writeT(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func raws(recs []Record) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Raw)
	}
	return out
}

// Tail bounds: the last n records, ordinals preserved, n<=0 = whole file.
func TestTailBounds(t *testing.T) {
	p := writeT(t, "l1\nl2\nl3\nl4\nl5\n")
	got, err := Tail(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"l3", "l4", "l5"}
	if strings.Join(raws(got), ",") != strings.Join(want, ",") {
		t.Fatalf("Tail(3) = %v, want %v", raws(got), want)
	}
	for _, r := range got {
		if r.Torn {
			t.Fatalf("record %d must not be torn in a newline-terminated file", r.Line)
		}
	}
	if got[0].Line != 3 || got[2].Line != 5 {
		t.Fatalf("ordinals must be file-based: got %d..%d", got[0].Line, got[2].Line)
	}
	all, err := Tail(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("Tail(0) = %d records, want the whole file (5)", len(all))
	}
	neg, err := Tail(p, -7)
	if err != nil || len(neg) != 5 {
		t.Fatalf("Tail(-7) must be the whole file: %d records, err %v", len(neg), err)
	}
	over, err := Tail(p, 99)
	if err != nil || len(over) != 5 {
		t.Fatalf("Tail(99) must clamp to the whole file: %d records, err %v", len(over), err)
	}
}

// A torn trailing line is data: carried with Torn set, never an error.
func TestTornLastLine(t *testing.T) {
	p := writeT(t, "{\"type\":\"turn\"}\n{\"type\":\"spe")
	recs, err := Tail(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	last := recs[len(recs)-1]
	if !last.Torn || last.Raw != "{\"type\":\"spe" {
		t.Fatalf("torn record must carry the partial bytes: %+v", last)
	}
	// The tail also works bounded across the torn line.
	bounded, err := Tail(p, 1)
	if err != nil || len(bounded) != 1 || !bounded[0].Torn {
		t.Fatalf("Tail(1) must return only the torn record: %+v err %v", bounded, err)
	}
}

// Malformed lines are records, never errors; typed lines classify; the
// timestamp extraction only takes string-typed fields.
func TestMalformedAndTyping(t *testing.T) {
	p := writeT(t, "not json at all\n"+
		"{\"type\":\"message_end\"}\n"+
		"{\"kind\":\"ctl\"}\n"+
		"{\"timestamp\":\"2026-01-02T03:04:05Z\",\"type\":\"turn\"}\n"+
		"{\"ts\":42}\n"+
		"[1,2,3]\n")
	recs, err := Tail(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 6 {
		t.Fatalf("want 6 records, got %d", len(recs))
	}
	wantTypes := []string{"raw", "message_end", "ctl", "turn", "object", "json"}
	for i, w := range wantTypes {
		if recs[i].Type != w {
			t.Fatalf("record %d type = %q, want %q", i+1, recs[i].Type, w)
		}
	}
	if recs[0].At != "" {
		t.Fatalf("malformed line must carry no timestamp: %q", recs[0].At)
	}
	if recs[3].At != "2026-01-02T03:04:05Z" {
		t.Fatalf("timestamp extraction failed: %q", recs[3].At)
	}
	if recs[4].At != "" {
		t.Fatalf("non-string ts must be ignored: %q", recs[4].At)
	}
}

// A missing file is the one error posture: only I/O fails.
func TestMissingFileIsError(t *testing.T) {
	if _, err := Tail(filepath.Join(t.TempDir(), "absent.jsonl"), 5); err == nil {
		t.Fatal("reading a missing file must fail")
	}
}
