package driver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBarRunsAppendReadClear: entries append in order, a torn final line
// is skipped and the next append stays readable, each work item reads only
// its own file, and clear removes it (idempotently).
func TestBarRunsAppendReadClear(t *testing.T) {
	dir := t.TempDir()
	a := BarRun{Repo: "demo", SpawnN: 3, Argv: []string{"go test ./... -run 'A|B'"}, Cwd: "/c/repos/demo", Exit: 0, DurationMS: 5, Head: "h1", At: "t"}
	b := BarRun{Repo: "demo", SpawnN: 4, Argv: []string{"npm test"}, Exit: 2, Head: "h1"}
	if err := AppendBarRun(dir, "wi_a", a); err != nil {
		t.Fatal(err)
	}
	if err := AppendBarRun(dir, "wi_other", b); err != nil {
		t.Fatal(err)
	}
	// a torn write: a partial line without its newline
	f, err := os.OpenFile(BarRunsPath(dir, "wi_a"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"repo":"demo","argv":["tor`)
	_ = f.Close()
	if err := AppendBarRun(dir, "wi_a", b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBarRuns(dir, "wi_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Argv[0] != a.Argv[0] || got[0].Head != "h1" || got[1].Exit != 2 || got[1].SpawnN != 4 {
		t.Fatalf("entries = %+v", got)
	}
	other, _ := ReadBarRuns(dir, "wi_other")
	if len(other) != 1 || other[0].Argv[0] != "npm test" {
		t.Fatalf("per-WI isolation: %+v", other)
	}
	if err := ClearBarRuns(dir, "wi_a"); err != nil {
		t.Fatal(err)
	}
	if err := ClearBarRuns(dir, "wi_a"); err != nil {
		t.Fatalf("a second clear: %v", err)
	}
	if got, _ := ReadBarRuns(dir, "wi_a"); len(got) != 0 {
		t.Fatalf("after clear: %+v", got)
	}
	if other, _ := ReadBarRuns(dir, "wi_other"); len(other) != 1 {
		t.Fatalf("clear touched another work item: %+v", other)
	}
}

// TestBarRunsPathRefusesOddIDs: the record is disabled without a data dir
// and an id that is not one file name names no file.
func TestBarRunsPathRefusesOddIDs(t *testing.T) {
	for _, wi := range []string{"", "../x", "a/b", ".hidden"} {
		if p := BarRunsPath("/d", wi); p != "" {
			t.Errorf("BarRunsPath(%q) = %q", wi, p)
		}
	}
	if p := BarRunsPath("", "wi_a"); p != "" {
		t.Errorf("no data dir: %q", p)
	}
	if p := BarRunsPath("/d", "wi_a"); p != filepath.Join("/d", "bar-runs", "wi_a.jsonl") {
		t.Errorf("path %q", p)
	}
	if err := AppendBarRun("", "wi_a", BarRun{}); err == nil {
		t.Error("append without a data dir must fail")
	}
}
