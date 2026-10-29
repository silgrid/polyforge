package verifyrun

// units.go: reading failure units out of a command's output. Only what
// can be parsed is parsed — Go (`--- FAIL: X`, `FAIL\t<pkg>`,
// `ok\t<pkg>`), the e2e harness's `FAIL <Test>` lines, pytest node ids,
// unittest's `FAIL: test_x (mod.Class)`, Rust libtest's
// `test x ... FAILED` and the `PASS: <title>` / `FAIL: <title>` marker
// lines of a node `.mjs` suite. A failed command none of whose failures
// can be located is ONE unit — the whole command — and that unit is
// never pre-existing (fail closed). A Go package whose binary did not
// finish normally (a panic, a timeout, an os.Exit, -failfast) is a
// failing unit of its own beside its failed tests: tests after the stop
// never ran. Every failed unit carries its failure signature: every
// assertion line of the failure WITH its body (a multi-line diff, a
// testify report), source positions normalized, repo root and temp dirs
// as tokens, message content kept.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The unit kinds.
const (
	KindGo       = "go"     // a top-level Go test (also from the harness's FAIL lines)
	KindGoPkg    = "go-pkg" // a Go package's own result
	KindPytest   = "pytest"
	KindUnittest = "unittest"
	KindLibtest  = "libtest"
	KindMjs      = "mjs"
	KindCommand  = "command" // an unlocatable failure: the whole command
)

// unit is one parsed result.
type unit struct {
	Kind        string
	Name        string
	Pkg         string
	Failed      bool
	BuildFailed bool   // a Go package that did not build (never flaky)
	Marker      string // the output line that identified it
	Signature   string // the failure's normalized assertion lines (failures)
}

// parseOpts are what a parse needs to know of the command it reads.
type parseOpts struct {
	// failfast: the go test run stopped at its first failure, so a
	// failing package's later tests never ran.
	failfast bool
}

var (
	reGoFail      = regexp.MustCompile(`^--- FAIL: (\S+) \(`)
	reGoPass      = regexp.MustCompile(`^--- PASS: (\S+) \(`)
	reGoPkgFail   = regexp.MustCompile(`^FAIL\t(\S+)(?:\t| \[|$)`)
	reGoPkgOK     = regexp.MustCompile(`^ok\s+(\S+)(?:\s|$)`)
	reHarnessFail = regexp.MustCompile(`^FAIL (Test\S*)$`)
	reGoRunHead   = regexp.MustCompile(`^=== (?:RUN|CONT|NAME|PAUSE)\s+(\S+)`)
	reGoAssert    = regexp.MustCompile(`^\s*(?:[\w./-]+\.go:\d+: |panic: |Error:|Error Trace:)`)
	reGoFrame     = regexp.MustCompile(`^\s+\S+\(.*\)$`)
	reHexArg      = regexp.MustCompile(`0x[0-9a-f]+`)
	// the e2e harness trailer: ran/listed, the E2E_SKIP exclusions and
	// the failed tests it names
	reHarnessSummary = regexp.MustCompile(`^FAIL\te2e\t(\d+)/(\d+) tests ran(?:, (\d+) excluded by E2E_SKIP)?, failed: ([^\t]*)`)

	rePytestSummary = regexp.MustCompile(`^(FAILED|ERROR) (\S+::\S+?)(?: - (.*))?$`)
	rePytestVerbose = regexp.MustCompile(`^(\S+::\S+) (PASSED|FAILED|ERROR)\b`)
	rePytestSection = regexp.MustCompile(`^_{3,} (.+?) _{3,}$`)

	reUnittestFail = regexp.MustCompile(`^(FAIL|ERROR): (\w+) \(([\w.]+)\)$`)
	reUnittestPass = regexp.MustCompile(`^(\w+) \(([\w.]+)\)(?: \S.*)? \.\.\. ok$`)
	reUnittestErr  = regexp.MustCompile(`^\w*(?:Error|Exception|Failure)\b`)

	reLibtest       = regexp.MustCompile(`^test (\S+) \.\.\. (ok|FAILED)$`)
	reLibtestStdout = regexp.MustCompile(`^---- (\S+) stdout ----$`)

	reMjs = regexp.MustCompile(`^(PASS|FAIL): (.+)$`)
)

// maxTextBytes caps a signature or a detail string: both travel into the
// step envelope and the capsule chain.
const maxTextBytes = 4 << 10

// capText cuts s to maxTextBytes on a rune boundary, marking the cut.
func capText(s string) string {
	if len(s) <= maxTextBytes {
		return s
	}
	cut := maxTextBytes - len(" …[cut]")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " …[cut]"
}

var (
	// a real source position: a file with a known source extension
	reSrcPos = regexp.MustCompile(`\b([\w./-]+\.(?:go|py|rs|js|mjs|ts)):\d+(?::\d+)?`)
	rePyLine = regexp.MustCompile(`\bline \d+\b`)
	// a Go t.TempDir() under a run token: `<tmp>/<TestName><random>/<NNN>`
	reTempDir = regexp.MustCompile(`(<tmp>|<root>)/([A-Za-z_]\w*?)\d+/(\d+)\b`)
)

// normSig normalizes one failure line for the signature comparison:
// whitespace collapsed, real source positions (`x_test.go:12`, a
// traceback's `line 7`) read as N, and a Go t.TempDir() path's random
// suffix dropped (`<tmp>/TestX2938475/001` reads `<tmp>/TestX/001`) —
// nothing else: a message's own content (`got 3`, `user.id:42`,
// `latency 12ms`) is the failure and stays.
func normSig(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = reTempDir.ReplaceAllString(s, "${1}/${2}/${3}")
	s = reSrcPos.ReplaceAllString(s, "${1}:N")
	return capText(rePyLine.ReplaceAllString(s, "line N"))
}

// parseUnits reads every unit one output carries. A Go test belongs to
// the package whose summary line (`ok`/`FAIL\t<pkg>`) follows it: Go
// tests are collected per package block and keyed by package and name, so
// a test name two packages share stays two units. Inside one block a test
// named twice (the harness's FAIL line and the dumped `--- FAIL:` header)
// is one unit.
func parseUnits(lines []string) []*unit { return parseUnitsOpts(lines, parseOpts{}) }

// parseUnitsOpts is parseUnits knowing the command's own stop rule.
func parseUnitsOpts(lines []string, o parseOpts) []*unit {
	var out []*unit
	index := map[string]*unit{}
	merge := func(prev, u *unit) *unit {
		if u.Failed && !prev.Failed {
			prev.Failed, prev.Marker = true, u.Marker
		}
		if prev.Signature == "" {
			prev.Signature = u.Signature
		}
		return prev
	}
	add := func(u *unit) *unit {
		k := u.Kind + "\x00" + u.Pkg + "\x00" + u.Name
		if prev, ok := index[k]; ok {
			return merge(prev, u)
		}
		index[k] = u
		out = append(out, u)
		return u
	}
	// the current package block's Go tests, by name
	var pending []*unit
	pendingIdx := map[string]*unit{}
	addPending := func(u *unit) {
		if prev, ok := pendingIdx[u.Name]; ok {
			merge(prev, u)
			return
		}
		pendingIdx[u.Name] = u
		pending = append(pending, u)
	}
	flush := func(pkg string) bool {
		anyFailed := false
		for _, u := range pending {
			u.Pkg = pkg
			anyFailed = anyFailed || u.Failed
			add(u)
		}
		pending, pendingIdx = nil, map[string]*unit{}
		return anyFailed
	}
	// the current package block's completion signals: the testing
	// framework's bare `FAIL` line closes a binary that finished
	// normally; a top-level panic (a crash, a timeout) never does
	bareFail, panicLine := false, ""
	settle := func(pkg string, failed, buildFailed bool, marker string) {
		anyFailed := flush(pkg)
		stopped := failed && (!bareFail || panicLine != "" || o.failfast)
		switch {
		case failed && (!anyFailed || stopped):
			why := marker
			if anyFailed {
				why = "the package's test binary did not finish normally (" + marker + ")"
			}
			add(&unit{Kind: KindGoPkg, Name: pkg, Failed: true, BuildFailed: buildFailed, Marker: why,
				Signature: normSig(panicLine)})
		case !failed:
			add(&unit{Kind: KindGoPkg, Name: pkg, Marker: marker})
		}
		bareFail, panicLine = false, ""
	}
	// the e2e harness trailer: every test it names failing is a failing
	// unit (a test it failed without a FAIL line of its own included), and
	// a run that lost tests (ran + excluded < listed) fails on its own
	settleHarness := func(m []string, marker string) {
		flush("e2e")
		for _, name := range strings.Fields(m[4]) {
			if u := index[KindGo+"\x00e2e\x00"+name]; u == nil || !u.Failed {
				add(&unit{Kind: KindGo, Name: name, Pkg: "e2e", Failed: true, Marker: marker})
			}
		}
		ran, _ := strconv.Atoi(m[1])
		listed, _ := strconv.Atoi(m[2])
		excluded, _ := strconv.Atoi(m[3])
		if ran+excluded != listed {
			add(&unit{Kind: KindGoPkg, Name: "e2e", Failed: true, Marker: marker})
		}
		bareFail, panicLine = false, ""
	}
	for i, line := range lines {
		if line == "FAIL" {
			bareFail = true
		}
		if strings.HasPrefix(line, "panic: ") && panicLine == "" {
			panicLine = line
		}
		switch {
		case reHarnessSummary.MatchString(line):
			settleHarness(reHarnessSummary.FindStringSubmatch(line), line)
		case reGoFail.MatchString(line):
			name := reGoFail.FindStringSubmatch(line)[1]
			addPending(&unit{Kind: KindGo, Name: name, Failed: true, Marker: line, Signature: goSignature(lines, i, name)})
		case reGoPass.MatchString(line):
			addPending(&unit{Kind: KindGo, Name: reGoPass.FindStringSubmatch(line)[1], Marker: line})
		case reHarnessFail.MatchString(line):
			addPending(&unit{Kind: KindGo, Name: reHarnessFail.FindStringSubmatch(line)[1], Failed: true, Marker: line})
		case reGoPkgFail.MatchString(line):
			settle(reGoPkgFail.FindStringSubmatch(line)[1], true,
				strings.Contains(line, "[build failed]") || strings.Contains(line, "[setup failed]"), line)
		case reGoPkgOK.MatchString(line):
			settle(reGoPkgOK.FindStringSubmatch(line)[1], false, false, line)
		case rePytestSummary.MatchString(line):
			m := rePytestSummary.FindStringSubmatch(line)
			sig := m[3]
			if sig == "" {
				sig = pytestSection(lines, m[2])
			}
			add(&unit{Kind: KindPytest, Name: m[2], Failed: true, Marker: line, Signature: normSig(sig)})
		case rePytestVerbose.MatchString(line):
			m := rePytestVerbose.FindStringSubmatch(line)
			if m[2] == "PASSED" {
				add(&unit{Kind: KindPytest, Name: m[1], Marker: line})
			} else {
				add(&unit{Kind: KindPytest, Name: m[1], Failed: true, Marker: line, Signature: normSig(pytestSection(lines, m[1]))})
			}
		case reUnittestFail.MatchString(line):
			m := reUnittestFail.FindStringSubmatch(line)
			add(&unit{Kind: KindUnittest, Name: unittestID(m[2], m[3]), Failed: true, Marker: line, Signature: unittestSignature(lines, i)})
		case reUnittestPass.MatchString(line):
			m := reUnittestPass.FindStringSubmatch(line)
			add(&unit{Kind: KindUnittest, Name: unittestID(m[1], m[2]), Marker: line})
		case reLibtest.MatchString(line):
			m := reLibtest.FindStringSubmatch(line)
			if m[2] == "ok" {
				add(&unit{Kind: KindLibtest, Name: m[1], Marker: line})
			} else {
				add(&unit{Kind: KindLibtest, Name: m[1], Failed: true, Marker: line, Signature: libtestSignature(lines, m[1])})
			}
		case reMjs.MatchString(line):
			m := reMjs.FindStringSubmatch(line)
			if m[1] == "PASS" {
				add(&unit{Kind: KindMjs, Name: m[2], Marker: line})
			} else {
				add(&unit{Kind: KindMjs, Name: m[2], Failed: true, Marker: line, Signature: mjsSignature(lines, i)})
			}
		}
	}
	flush("") // tests no summary line closed (a cut-off output)
	return out
}

// goSignature answers Go test name's failure signature: every
// assertion-shaped line (`file.go:N: …`, `panic: …`, testify's `Error:`)
// the test and all its subtests printed, each WITH its body (the deeper
// indented lines under it: a multi-line diff, testify's expected/actual),
// and a data race's report head and first frame — taken from the -v
// blocks opened by an `=== RUN/CONT/NAME` marker of the test or a subtest
// and from the indented block under its `--- FAIL:` header (the
// non-verbose shape), each line normalized, in order.
func goSignature(lines []string, at int, name string) string {
	var sig []string
	take := func(j, end int) int {
		sig = append(sig, normSig(lines[j]))
		ind := indentOf(lines[j])
		k := j + 1
		for ; k < end; k++ {
			l := lines[k]
			if strings.TrimSpace(l) == "" || indentOf(l) <= ind {
				break
			}
			sig = append(sig, normSig(l))
		}
		return k - 1
	}
	cur := ""
	for j := 0; j < at; j++ {
		l := lines[j]
		if m := reGoRunHead.FindStringSubmatch(l); m != nil {
			cur = m[1]
			continue
		}
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "ok ") || strings.HasPrefix(l, "FAIL") {
			cur = "" // a top-level verdict or summary closes the block
			continue
		}
		if cur != name && !strings.HasPrefix(cur, name+"/") {
			continue
		}
		switch {
		case l == "WARNING: DATA RACE":
			sig = append(sig, l)
			for k := j + 1; k < at && k < j+6; k++ {
				if reGoFrame.MatchString(lines[k]) {
					sig = append(sig, normSig(reHexArg.ReplaceAllString(lines[k], "0x?")))
					break
				}
			}
		case reGoAssert.MatchString(l):
			j = take(j, at)
		}
	}
	for j := at + 1; j < len(lines); j++ {
		l := lines[j]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' && !strings.HasPrefix(l, "panic: ") {
			break
		}
		if reGoAssert.MatchString(l) {
			j = take(j, len(lines))
		}
	}
	return capText(strings.Join(sig, " | "))
}

// indentOf answers the width of a line's leading whitespace.
func indentOf(l string) int {
	return len(l) - len(strings.TrimLeft(l, " \t"))
}

// pytestSection answers the first `E ` line of a failing node's section.
func pytestSection(lines []string, nodeID string) string {
	name := nodeID
	if i := strings.LastIndex(nodeID, "::"); i >= 0 {
		name = nodeID[i+2:]
	}
	for i, l := range lines {
		m := rePytestSection.FindStringSubmatch(l)
		if m == nil || (m[1] != name && !strings.HasSuffix(m[1], "."+name)) {
			continue
		}
		for _, x := range lines[i+1:] {
			if rePytestSection.MatchString(x) {
				break
			}
			if strings.HasPrefix(x, "E ") {
				return strings.TrimSpace(strings.TrimPrefix(x, "E"))
			}
		}
	}
	return ""
}

// unittestID is the dotted id of a unittest case: Python ≥ 3.11 prints
// the method inside the parentheses already.
func unittestID(method, owner string) string {
	if strings.HasSuffix(owner, "."+method) {
		return owner
	}
	return owner + "." + method
}

// unittestSignature answers the exception line of the traceback block
// under a `FAIL:`/`ERROR:` header.
func unittestSignature(lines []string, at int) string {
	for _, l := range lines[at+1:] {
		if strings.HasPrefix(l, "=====") {
			break
		}
		if reUnittestErr.MatchString(l) {
			return normSig(l)
		}
	}
	return ""
}

// libtestSignature answers the panic message of a failing Rust test.
func libtestSignature(lines []string, name string) string {
	for i, l := range lines {
		m := reLibtestStdout.FindStringSubmatch(l)
		if m == nil || m[1] != name {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if reLibtestStdout.MatchString(lines[j]) {
				break
			}
			if strings.Contains(lines[j], "panicked at") {
				for k := j + 1; k < len(lines); k++ {
					if t := strings.TrimSpace(lines[k]); t != "" && !reLibtestStdout.MatchString(lines[k]) {
						return normSig(t)
					}
				}
				return normSig(lines[j])
			}
		}
	}
	return ""
}

// mjsSignature answers the first line after a suite's `FAIL: <title>`.
func mjsSignature(lines []string, at int) string {
	for _, l := range lines[at+1:] {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if reMjs.MatchString(l) {
			return ""
		}
		return normSig(t)
	}
	return ""
}

// findUnit answers the parsed unit of the given kind and name (nil = absent).
func findUnit(us []*unit, kind, name string) *unit {
	for _, u := range us {
		if u.Kind == kind && u.Name == name {
			return u
		}
	}
	return nil
}
