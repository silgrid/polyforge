package verifyrun

import (
	"strings"
	"testing"
	"time"
)

// TestClassifyBase pins the pre-existing judgment: the positive shape and
// each false-positive shape that must stay a new failure.
func TestClassifyBase(t *testing.T) {
	same := baseObservation{Exit: 1, Found: true, Failed: true, Signature: "x_test.go:N: boom"}
	cases := []struct {
		name    string
		headSig string
		exists  bool
		runs    []baseObservation
		want    bool
		why     string
	}{
		{"same failure on every base run", "x_test.go:N: boom", true, []baseObservation{same, same}, true, "same way on all 2"},
		{"absent at base", "x_test.go:N: boom", false, nil, false, "base does not have this test"},
		{"environmental at base", "x_test.go:N: boom", true, []baseObservation{{Exit: 2}, same}, false, "environmental"},
		{"different signature", "x_test.go:N: boom", true, []baseObservation{same, {Exit: 1, Found: true, Failed: true, Signature: "x_test.go:N: other"}}, false, "fails differently"},
		{"passes at base", "x_test.go:N: boom", true, []baseObservation{{Exit: 0, Found: true}, same}, false, "passes on the base"},
		{"no HEAD signature", "", true, []baseObservation{same, same}, false, "no targeted re-run at HEAD"},
		{"base failure without a signature", "x_test.go:N: boom", true, []baseObservation{same, {Exit: 1, Found: true, Failed: true}}, false, "environmental"},
		{"budget cut the runs", "x_test.go:N: boom", true, []baseObservation{same}, false, "only 1 of 2"},
	}
	// an order-dependent failure: the bar fails one way, the isolated
	// re-runs at HEAD and base another — the base proves nothing
	if got, why := classifyBase("x_test.go:N: in-suite M1", "x_test.go:N: boom", true, "", []baseObservation{same, same}, 2); got || !strings.Contains(why, "differs from its isolated re-run") {
		t.Errorf("bar vs HEAD re-run mismatch: %v %q", got, why)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := classifyBase(c.headSig, c.headSig, c.exists, "base does not have this test", c.runs, 2)
			if got != c.want || !strings.Contains(why, c.why) {
				t.Errorf("classifyBase = %v, %q; want %v containing %q", got, why, c.want, c.why)
			}
		})
	}
}

// TestDeriveTestBar: the bar is every command that ran, verbatim, a
// pre-existing failure excluded in the command's own skip form; a command
// without one keeps its run form and is noted; skipped commands and
// auxiliary runs are never in it.
func TestDeriveTestBar(t *testing.T) {
	pre := func(kind, name string) *unitRun {
		return &unitRun{unit: &unit{Kind: kind, Name: name, Failed: true}, PreExisting: true}
	}
	rr := &repoRun{name: "app", runs: []*cmdRun{
		{repo: "app", cmd: "GOWORK=off go build ./..."},
		{repo: "app", cmd: "GOWORK=off go test -count=1 ./...", units: []*unitRun{pre(KindGo, "TestA"), pre(KindGo, "TestB")}},
		{repo: "app", cmd: "go test ./... -skip '^TestZ$'", units: []*unitRun{pre(KindGo, "TestC")}},
		{repo: "app", cmd: "GOWORK=off make test", units: []*unitRun{pre(KindGo, "TestD")}},
		{repo: "app", cmd: "python3 -m pytest -q", units: []*unitRun{pre(KindPytest, "tests/t.py::test_x")}},
		{repo: "app", cmd: "needs-env", skipped: "declared precondition unmet: missing env X"},
	}, authored: []*cmdRun{{repo: "app", cmd: "go test ./test/ -run '^TestW1' -count=1 -v"}}}
	bar, notes := deriveTestBar([]*repoRun{rr})
	want := []string{
		"GOWORK=off go build ./...",
		"GOWORK=off go test -count=1 ./... -skip '^(TestA|TestB)$'",
		"go test ./... -skip '^TestZ$'",
		"GOWORK=off make test",
		"python3 -m pytest -q --deselect tests/t.py::test_x",
		"go test ./test/ -run '^TestW1' -count=1 -v",
	}
	if len(bar) != len(want) {
		t.Fatalf("bar = %+v", bar)
	}
	for i, w := range want {
		if bar[i].Repo != "app" || bar[i].Command != w {
			t.Errorf("bar[%d] = %+v, want %q", i, bar[i], w)
		}
		if strings.HasPrefix(bar[i].Command, "cd ") {
			t.Errorf("bar[%d] carries a leading cd", i)
		}
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "TestC") || !strings.Contains(notes[1], "TestD") {
		t.Errorf("notes = %v", notes)
	}
}

func TestShellWords(t *testing.T) {
	cases := []struct {
		in     string
		words  []string
		simple bool
	}{
		{`GOWORK=off go test ./... -run 'A|B'`, []string{"GOWORK=off", "go", "test", "./...", "-run", "A|B"}, true},
		{`cd webui && npm ci`, []string{"cd", "webui"}, false},
		{`test -z "$(gofmt -l .)"`, []string{"test", "-z"}, false},
		{`echo "a b" c\ d`, []string{"echo", "a b", "c d"}, true},
		{`go test | tee log`, []string{"go", "test"}, false},
	}
	for _, c := range cases {
		w, s := shellWords(c.in)
		if strings.Join(w, "\x00") != strings.Join(c.words, "\x00") || s != c.simple {
			t.Errorf("shellWords(%q) = %q %v, want %q %v", c.in, w, s, c.words, c.simple)
		}
	}
	if got := strings.Join(leadingAssignments(`env GOWORK=off FOO='a b' go test`), " "); got != `GOWORK=off FOO='a b'` {
		t.Errorf("assignments = %q", got)
	}
}

// TestDecodeReportContract: the engine report is checked against the
// contract (schema 1, a mode, named commands with a known phase).
func TestDecodeReportContract(t *testing.T) {
	good := `{"schema":1,"stack":"go","analyzer":"go","conf":"absent","conf_changed_in_diff":false,"mode":"full","paths":[],` +
		`"commands":[{"name":"fast-1","phase":"fast","cmd":"go vet ./...","requires_env":[],"requires_cmd":[],"timeout":"20m","source":"default"}],` +
		`"test_glob":[],"base_setup":[],"runner":null,"diagnostics":[]}`
	if rep, err := decodeReport([]byte(good)); err != nil || rep.Commands[0].Timeout != "20m" {
		t.Fatalf("good report: %v %+v", err, rep)
	}
	for _, bad := range []string{
		`{"schema":2,"mode":"full","commands":[]}`,
		`{"schema":1,"mode":"partial","commands":[]}`,
		`{"schema":1,"mode":"full","commands":[{"name":"x","phase":"slow","cmd":"true"}]}`,
		`{"schema":1,"mode":"full","commands":[{"name":"","phase":"fast","cmd":"true"}]}`,
		`mode: full`,
	} {
		if _, err := decodeReport([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"pkg/a_test.go": true, "web/x.test.ts": true, "web/x.spec.js": true, "py/test_x.py": true,
		"test/fixtures/data.json": true, "tests/x.txt": true, "pkg/a.go": false, "docs/testing.md": false,
	} {
		if got := isTestFile(p, nil); got != want {
			t.Errorf("isTestFile(%q) = %v", p, got)
		}
	}
	if !isTestFile("e2e/flow.mjs", []string{"e2e/*.mjs"}) || !isTestFile("deep/x.check", []string{"*.check"}) {
		t.Error("conf test_glob must match like the engine's globs")
	}
}

// TestSubstitutionKeepsTheSkipForm: an unquoted command substitution is
// part of one simple command, so the bar's skip form still applies; an
// expansion inside quotes or escaped is not re-emittable and refuses.
func TestSubstitutionKeepsTheSkipForm(t *testing.T) {
	cmd := `GOWORK=off go test $(go list ./... | grep -v '/test$') -count=1`
	w, simple := shellWords(cmd)
	if !simple || len(w) != 5 || w[3] != `$(go list ./... | grep -v '/test$')` {
		t.Fatalf("shellWords = %q %v", w, simple)
	}
	pre := &unitRun{unit: &unit{Kind: KindGo, Name: "TestOld", Failed: true}, PreExisting: true}
	got, note := skipForm(&cmdRun{repo: "app", cmd: cmd, units: []*unitRun{pre}})
	if note != "" || got != cmd+" -skip '^(TestOld)$'" {
		t.Errorf("skipForm = %q (%q)", got, note)
	}
	if r, _ := goRerun(cmd, "^TestOld$", "./a/", time.Minute); r != "GOWORK=off go test -count=1 -v -timeout 1m0s -run '^TestOld$' ./a/" {
		t.Errorf("goRerun = %q", r)
	}
	for _, c := range []string{`echo "$(date)"`, "echo $(unclosed", "echo 'unclosed"} {
		if _, simple := shellWords(c); simple {
			t.Errorf("%q must not count as simple", c)
		}
	}
	// a literal dollar stays literal: the raw text is what gets re-emitted
	if w, raws, simple := shellWordsRaw(`X='a$b' go test \$y`); !simple || w[0] != "X=a$b" || raws[0] != `X='a$b'` || raws[3] != `\$y` {
		t.Errorf("raw words = %q %q %v", w, raws, simple)
	}
	if got := strings.Join(leadingAssignments(`X='a$b' go test`), " "); got != `X='a$b'` {
		t.Errorf("assignments re-emit raw: %q", got)
	}
	if w, simple := shellWords("FOO=${BAR} go test ./... -tags=$TAGS"); !simple || w[0] != "FOO=${BAR}" || w[4] != "-tags=$TAGS" {
		t.Errorf("expansions in words = %q %v", w, simple)
	}
	if got := strings.Join(leadingAssignments("FOO=$(pwd)/x go test"), " "); got != "FOO=$(pwd)/x" {
		t.Errorf("an assignment with an expansion is re-emitted raw: %q", got)
	}
}
