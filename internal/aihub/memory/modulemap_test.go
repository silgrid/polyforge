package memory

import (
	"strings"
	"testing"
)

const mmBody = "intro line\nbaseline @abc1234 2026-11-01\n\nthe module map\n"

func mmCreate(e *env, title *string, tags []string, body string) (*Entry, error) {
	return e.store.Create(CreateRequest{
		Project: "alpha", Type: moduleMapType, Title: title,
		Visibility: "project", Content: body, Tags: tags,
	}, e.user)
}

func mmRefusal(t *testing.T, name string, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: Create accepted; want MEM-400", name)
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Code != "MEM-400" || ae.Reason != "" {
		t.Fatalf("%s: error %v, want MEM-400 without a reason code", name, err)
	}
	if !strings.HasPrefix(ae.Detail, "doc.module-map convention: ") {
		t.Fatalf("%s: detail %q lacks the convention prefix", name, ae.Detail)
	}
	return ae.Detail
}

func mmCount(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM memories WHERE type=?1`, moduleMapType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestModuleMapConformingAccepted(t *testing.T) {
	e := newEnv(t)
	// No supersedes anywhere, baseline not on the first line, client
	// frontmatter dropped before the check.
	for _, c := range []struct{ title, body string }{
		{"demo/widget rev1", mmBody},
		{"  demo/widget rev01  ", "---\nfoo: bar\n---\nbaseline @abcdef1234567 2026-11-02\n"},
		{"demo/pkg/sub rev3", "  baseline @0123456789abcdef0123456789abcdef01234567 2026-11-03  \n"},
	} {
		tags := []string{"demo", "widget"}
		if strings.HasPrefix(c.title, "demo/pkg") {
			tags = []string{"demo", "pkg/sub"}
		}
		entry, err := mmCreate(e, strPtr(c.title), tags, c.body)
		if err != nil {
			t.Fatalf("%q: conforming create refused: %v", c.title, err)
		}
		if _, err := e.store.Get(entry.ID); err != nil {
			t.Fatalf("%q: stored entry not readable: %v", c.title, err)
		}
	}
	if n := mmCount(t, e); n != 3 {
		t.Fatalf("stored %d module maps, want 3", n)
	}
}

func TestModuleMapTitleRefused(t *testing.T) {
	e := newEnv(t)
	tags := []string{"demo", "widget"}
	for _, c := range []struct {
		name  string
		title *string
		got   string
	}{
		{"absent", nil, "got nothing"},
		{"blank", strPtr("   "), "got nothing"},
		{"no rev", strPtr("demo/widget"), `got "demo/widget"`},
		{"no slash", strPtr("demowidget rev1"), `got "demowidget rev1"`},
		{"empty repo", strPtr("/widget rev1"), `got "/widget rev1"`},
		{"empty module", strPtr("demo/ rev1"), `got "demo/ rev1"`},
		{"space in module", strPtr("demo/wid get rev1"), `got "demo/wid get rev1"`},
		{"non-numeric", strPtr("demo/widget revX"), `got "demo/widget revX"`},
		{"rev0", strPtr("demo/widget rev0"), `got "demo/widget rev0"`},
		{"two spaces", strPtr("demo/widget  rev1"), `got "demo/widget  rev1"`},
	} {
		_, err := mmCreate(e, c.title, tags, mmBody)
		d := mmRefusal(t, c.name, err)
		if !strings.Contains(d, `title must have the form "<repo>/<module> rev<N>"`) || !strings.Contains(d, c.got) {
			t.Fatalf("%s: detail %q does not name the title requirement and %s", c.name, d, c.got)
		}
		if !strings.Contains(d, "tags must contain the repo and the module named by the title") {
			t.Fatalf("%s: detail %q does not name the tag requirement", c.name, d)
		}
		if strings.Contains(d, "baseline") {
			t.Fatalf("%s: detail %q names a baseline the body carries", c.name, d)
		}
	}
	if n := mmCount(t, e); n != 0 {
		t.Fatalf("refusals stored %d rows", n)
	}
}

func TestModuleMapTagsRefused(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		name          string
		tags          []string
		absent, there []string
	}{
		{"both", nil, []string{`"demo"`, `"widget"`}, nil},
		{"repo", []string{"widget"}, []string{`"demo"`}, []string{`"widget"`}},
		{"module", []string{"demo", "other"}, []string{`"widget"`}, []string{`"demo"`}},
		{"case-sensitive", []string{"Demo", "widget"}, []string{`"demo"`}, []string{`"widget"`}},
	} {
		_, err := mmCreate(e, strPtr("demo/widget rev2"), c.tags, mmBody)
		d := mmRefusal(t, c.name, err)
		for _, a := range c.absent {
			if !strings.Contains(d, "missing tag "+a) {
				t.Fatalf("%s: detail %q does not name missing tag %s", c.name, d, a)
			}
		}
		for _, p := range c.there {
			if strings.Contains(d, "missing tag "+p) {
				t.Fatalf("%s: detail %q names present tag %s", c.name, d, p)
			}
		}
		if strings.Contains(d, "title") || strings.Contains(d, "baseline") {
			t.Fatalf("%s: detail %q names a piece that is present", c.name, d)
		}
	}
	if n := mmCount(t, e); n != 0 {
		t.Fatalf("refusals stored %d rows", n)
	}
}

func TestModuleMapBaselineRefused(t *testing.T) {
	e := newEnv(t)
	for _, body := range []string{
		"no pin line at all\n",
		"baseline abc1234 2026-11-01\n",        // no @
		"baseline @ABC1234 2026-11-01\n",       // upper-case hex
		"baseline @abc12 2026-11-01\n",         // too short
		"baseline @abc1234 2026/11/01\n",       // date shape
		"see baseline @abc1234 2026-11-01\n",   // not the whole line
		"baseline @abc1234 2026-11-01 extra\n", // trailing text
	} {
		_, err := mmCreate(e, strPtr("demo/widget rev1"), []string{"demo", "widget"}, body)
		d := mmRefusal(t, body, err)
		if d != `doc.module-map convention: body must carry a line "baseline @<commit> <YYYY-MM-DD>"` {
			t.Fatalf("%q: detail %q", body, d)
		}
	}
	if n := mmCount(t, e); n != 0 {
		t.Fatalf("refusals stored %d rows", n)
	}
}

func TestModuleMapNamesEveryMissingPiece(t *testing.T) {
	e := newEnv(t)
	_, err := mmCreate(e, strPtr("not a module title"), nil, "no pin\n")
	d := mmRefusal(t, "all three", err)
	want := `doc.module-map convention: title must have the form "<repo>/<module> rev<N>" (N a decimal >= 1), got "not a module title"; ` +
		`tags must contain the repo and the module named by the title; ` +
		`body must carry a line "baseline @<commit> <YYYY-MM-DD>"`
	if d != want {
		t.Fatalf("detail\n %q\nwant\n %q", d, want)
	}
	_, err = mmCreate(e, strPtr("demo/widget rev1"), []string{"x"}, "no pin\n")
	d = mmRefusal(t, "tags and baseline", err)
	for _, p := range []string{`missing tag "demo"`, `missing tag "widget"`, "baseline"} {
		if !strings.Contains(d, p) {
			t.Fatalf("detail %q lacks %s", d, p)
		}
	}
	if n := mmCount(t, e); n != 0 {
		t.Fatalf("refusals stored %d rows", n)
	}
}

func TestParseModuleTitle(t *testing.T) {
	for _, c := range []struct {
		title        string
		repo, module string
		rev          int
		ok           bool
	}{
		{"demo/widget rev1", "demo", "widget", 1, true},
		{"demo/widget rev01", "demo", "widget", 1, true},
		{"demo/widget rev12", "demo", "widget", 12, true},
		{"demo/a/b rev2", "demo", "a/b", 2, true},
		{"demo/widget rev0", "", "", 0, false},
		{"demo/widget", "", "", 0, false},
		{"workbench follow-ups ledger", "", "", 0, false},
	} {
		k, rev, ok := parseModuleTitle(c.title)
		if ok != c.ok || k.repo != c.repo || k.module != c.module || rev != c.rev {
			t.Fatalf("%q: got %+v rev %d ok %v", c.title, k, rev, ok)
		}
	}
}

// Other types keep today's validation exactly.
func TestModuleMapOtherTypesUnchanged(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "doc.track", Title: strPtr("ledger"),
		Visibility: "project", Content: "no baseline here",
	}, e.user); err != nil {
		t.Fatalf("doc.track without baseline/revision refused: %v", err)
	}
	_, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "module-map", Title: strPtr("demo/widget rev1"),
		Visibility: "project", Content: mmBody, Tags: []string{"demo", "widget"},
	}, e.user)
	ae, ok := err.(*APIError)
	if !ok || ae.Code != "MEM-400" || !strings.Contains(ae.Detail, "experience|fact|doc|rule|methodology") {
		t.Fatalf("unknown kind: %v, want today's namespace MEM-400", err)
	}
}

func strPtr(s string) *string { return &s }
