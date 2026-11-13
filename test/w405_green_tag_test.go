package test

// w405_green_tag_test.go: the test gate's identity (tools/gate-id) and its
// releasable marker: written only by the gate (`gate.py --mark-green`;
// pf-gate before) after a pass,
// read by tools/green-tag check / latest, the format both the release and
// the integrate workflow use. Every remote is a local bare
// repository; no real remote is ever touched.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW405(GateID|GreenTag)' -count=1

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// w405Repo is a clone of a fresh bare origin carrying the gate tree.
type w405Repo struct {
	t      *testing.T
	origin string
	work   string
}

func newW405Repo(t *testing.T) *w405Repo {
	t.Helper()
	for _, bin := range []string{"git", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	r := &w405Repo{t: t, origin: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work")}
	relGit(t, root, "init", "-q", "--bare", "-b", "main", r.origin)
	relGit(t, root, "clone", "-q", r.origin, r.work)
	relGit(t, r.work, "checkout", "-q", "-b", "main")
	relGateTree(t, r.work)
	relGit(t, r.work, "add", "-A")
	relGit(t, r.work, "commit", "-q", "-m", "first")
	relGit(t, r.work, "push", "-q", "origin", "main")
	return r
}

// sh runs a gate tool in the clone: its stdout, stderr and exit code.
func (r *w405Repo) sh(args ...string) (string, string, int) {
	r.t.Helper()
	c := exec.Command("sh", args...)
	c.Dir = r.work
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		r.t.Fatalf("sh %v: %v", args, err)
	}
	return strings.TrimSpace(out.String()), errb.String(), code
}

func (r *w405Repo) commit(file, body, msg string) string {
	r.t.Helper()
	relWrite(r.t, filepath.Join(r.work, file), body, 0o644)
	relGit(r.t, r.work, "add", "-A")
	relGit(r.t, r.work, "commit", "-q", "-m", msg)
	relGit(r.t, r.work, "push", "-q", "origin", "main")
	return relGit(r.t, r.work, "rev-parse", "HEAD")
}

func (r *w405Repo) gateID(rev string) string {
	r.t.Helper()
	out, errb, code := r.sh("tools/gate-id", rev)
	if code != 0 {
		r.t.Fatalf("gate-id %s: %d %s", rev, code, errb)
	}
	return out
}

// TestW405GateIDHashesTheGateLinesAndTheDockerfile pins what the id is:
// sha256 over the documented text — a header, the Dockerfile's sha256 and
// the conf's gate lines in order — of one commit's tree. A gate line or the
// Dockerfile moves it; a comment or a non-gate key does not.
func TestW405GateIDHashesTheGateLinesAndTheDockerfile(t *testing.T) {
	r := newW405Repo(t)
	base := r.gateID("HEAD")
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(base) {
		t.Fatalf("gate id %q", base)
	}
	show, _, code := r.sh("tools/gate-id", "--show")
	if code != 0 {
		t.Fatal("gate-id --show failed")
	}
	dsum := sha256Hex(relGateDockerfile)
	wantShow := "polyforge-gate-id 1\ndockerfile " + dsum + "\nstack=none\nfast_gate=echo gate-fast\nfull_command=echo gate-full"
	if show != wantShow {
		t.Fatalf("gate-id --show =\n%s\nwant\n%s", show, wantShow)
	}
	if got := sha256Hex(wantShow + "\n"); got != base {
		t.Fatalf("the id %s is not the sha256 of the shown text (%s)", base, got)
	}
	first := relGit(t, r.work, "rev-parse", "HEAD")
	// a comment, indentation and a non-gate key leave the gate unchanged
	r.commit(".polyforge/verify-scope.conf", "# a comment\nstack=none\n  fast_gate=echo gate-fast\nforced_full=go.mod\ntest_glob=*_test.go\nfull_command=echo gate-full\n", "cosmetic")
	if got := r.gateID("HEAD"); got != base {
		t.Fatalf("a comment or a non-gate key moved the gate id: %s -> %s", base, got)
	}
	// a gate line moves it, and so does the Dockerfile
	r.commit(".polyforge/verify-scope.conf", relGateConf+"full_command=echo more\n", "gate line")
	moved := r.gateID("HEAD")
	if moved == base {
		t.Fatal("a new full_command did not move the gate id")
	}
	r.commit(".polyforge/Dockerfile.verify", relGateDockerfile+"RUN true\n", "image")
	if got := r.gateID("HEAD"); got == moved {
		t.Fatal("a Dockerfile.verify change did not move the gate id")
	}
	// the id is the commit's, never the working tree's
	if got := r.gateID(first); got != base {
		t.Fatalf("gate-id %s = %s, want %s", first, got, base)
	}
	relWrite(t, filepath.Join(r.work, ".polyforge", "verify-scope.conf"), "stack=none\nfast_gate=echo dirty\n", 0o644)
	if got := r.gateID("HEAD"); got == base || got == moved {
		t.Fatalf("unexpected id %s", got)
	} else if got2 := r.gateID("HEAD"); got2 != got {
		t.Fatal("unstable id")
	}
	relGit(t, r.work, "checkout", "-q", "--", ".")
	// no Dockerfile at the commit: no gate, exit 2
	relGit(t, r.work, "rm", "-q", ".polyforge/Dockerfile.verify")
	relGit(t, r.work, "commit", "-q", "-m", "no image")
	if _, errb, code := r.sh("tools/gate-id"); code != 2 || !strings.Contains(errb, "Dockerfile.verify is not in the tree") {
		t.Fatalf("gate-id without a Dockerfile: %d %s", code, errb)
	}
}

func sha256Hex(s string) string {
	c := exec.Command("sha256sum")
	c.Stdin = strings.NewReader(s)
	out, err := c.Output()
	if err != nil {
		panic(err)
	}
	return string(out[:64])
}

// w405Tool runs a gate tool in the release world's clone: stdout, stderr,
// exit code.
func w405Tool(t *testing.T, w *relWorld, env []string, args ...string) (string, string, int) {
	t.Helper()
	c := exec.Command("sh", args...)
	c.Dir = w.work
	c.Env = w.env(env...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("sh %v: %v", args, err)
	}
	return strings.TrimSpace(out.String()), errb.String(), code
}

// w405Record is a green tag's message.
type w405Record struct {
	Commit        string `json:"commit"`
	GateID        string `json:"gate_id"`
	Runner        string `json:"runner"`
	RunnerVersion string `json:"runner_version"`
	PlanDigest    string `json:"plan_digest"`
	VerifiedAt    string `json:"verified_at"`
	Summary       string `json:"summary"`
}

func (r w405Record) json(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// w405TagRecord reads the record of a tag on the world's origin.
func w405TagRecord(t *testing.T, w *relWorld, name string) w405Record {
	t.Helper()
	body := relGit(t, w.origin, "for-each-ref", "--format=%(contents)", "refs/tags/"+name)
	var r w405Record
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("tag %s message %q: %v", name, body, err)
	}
	if strings.Contains(strings.TrimSpace(body), "\n") {
		t.Fatalf("the message is not one line: %q", body)
	}
	return r
}

// TestW405GreenTagOnlyTheGateWritesItAndCheckReadsIt: green-tag has no
// create; `gate.py --mark-green` pushes the one annotated tag (no branch
// moves) after a pass, its record naming the runner and the plan digest;
// check finds it for HEAD, recomputing the digest with the tree's own
// gate.py --plan-only; latest reads it; a second --mark-green tags nothing
// new.
func TestW405GreenTagOnlyTheGateWritesItAndCheckReadsIt(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	head := relGit(t, w.work, "rev-parse", "HEAD")
	before := relGit(t, w.origin, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")

	if _, errb, code := w405Tool(t, w, nil, "tools/green-tag", "create", head, "--summary", "x"); code != 2 {
		t.Fatalf("green-tag create: exit %d, want a usage error (only the gate writes a green tag)\n%s", code, errb)
	}
	if _, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 1 || !strings.Contains(errb, "carries no green tag on origin") {
		t.Fatalf("check before any tag: %d %s", code, errb)
	}
	if _, errb, code := w405RunGate(t, w, nil, "--mark-green"); code != 2 || !strings.Contains(errb, "--summary") {
		t.Fatalf("--mark-green without --summary: %d %s", code, errb)
	}
	out, errb, code := w405RunGate(t, w, nil, "--mark-green", "--summary", "by hand after review")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	name := lines[len(lines)-1]
	if code != 0 || !regexp.MustCompile(`^green/[0-9]{8}-1$`).MatchString(name) {
		t.Fatalf("gate.py --mark-green: %d %q\n%s", code, out, errb)
	}
	if got := relGit(t, w.origin, "for-each-ref", "--format=%(refname) %(objecttype) %(*objectname)", "refs/tags/green"); got != "refs/tags/"+name+" tag "+head {
		t.Fatalf("origin green tags: %q", got)
	}
	if after := relGit(t, w.origin, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"); after != before {
		t.Fatalf("--mark-green moved a branch: %q -> %q", before, after)
	}
	rec := w405TagRecord(t, w, name)
	if rec.Commit != head || rec.GateID != w405GateID(t, w.work) || rec.Runner != "gate.py" || rec.RunnerVersion != "2" ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(rec.PlanDigest) || !strings.HasSuffix(rec.Summary, "by hand after review") {
		t.Fatalf("record %+v", rec)
	}
	if got, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 0 || got != name {
		t.Fatalf("check after --mark-green: %d %q %s", code, got, errb)
	}
	if got, _, code := w405Tool(t, w, nil, "tools/green-tag", "latest"); code != 0 || got != name+" "+head+" "+rec.GateID {
		t.Fatalf("latest: %d %q", code, got)
	}
	// again: the existing tag, nothing new
	out, errb, code = w405RunGate(t, w, nil, "--mark-green", "--summary", "again")
	if code != 0 || !strings.HasSuffix(strings.TrimSpace(out), name) || !strings.Contains(out, "not tagged again") {
		t.Fatalf("second --mark-green: %d %q %s", code, out, errb)
	}
	if n := len(strings.Fields(relGit(t, w.origin, "tag", "-l", "green/*"))); n != 1 {
		t.Fatalf("%d green tags after the second --mark-green", n)
	}
	// check judges HEAD only, on a clean tree
	if _, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", "HEAD^1"); code != 2 || !strings.Contains(errb, "judges HEAD") {
		t.Fatalf("check of another commit: %d %s", code, errb)
	}
	relWrite(t, filepath.Join(w.work, "stray"), "x\n", 0o644)
	if _, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 2 || !strings.Contains(errb, "not clean") {
		t.Fatalf("check on a dirty tree: %d %s", code, errb)
	}
	runs := strings.Count(strings.Join(w.calls(), "\n"), "docker run ")
	if _, errb, code := w405RunGate(t, w, nil, "--mark-green", "--summary", "s"); code != 2 || !strings.Contains(errb, "not clean") {
		t.Fatalf("--mark-green on a dirty tree: %d %s", code, errb)
	}
	if n := strings.Count(strings.Join(w.calls(), "\n"), "docker run "); n != runs {
		t.Fatal("--mark-green on a dirty tree started the gate: it must refuse before")
	}
	if err := os.Remove(filepath.Join(w.work, "stray")); err != nil {
		t.Fatal(err)
	}
	// an unreadable remote is an error, never "not green"
	if _, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head, "--remote", filepath.Join(w.root, "no-such-remote")); code != 2 || !strings.Contains(errb, "cannot read the green tags") {
		t.Fatalf("check against an unreadable remote: %d %s", code, errb)
	}
}

// TestW405GreenTagNeverReusesAnotherRunnersOrPlansResult: a tag recording
// another runner (the daemon's verification), another runner version,
// another plan digest or another gate id is not green for HEAD — and a
// genuine tag stops matching when the plan it recorded is not the plan
// HEAD's gate computes now under the same tree (here: a gate run under
// other container limits than the gate's defaults, which check plans with).
func TestW405GreenTagNeverReusesAnotherRunnersOrPlansResult(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	head := relGit(t, w.work, "rev-parse", "HEAD")
	out, errb, code := w405RunGate(t, w, nil, "--mark-green", "--summary", "s")
	if code != 0 {
		t.Fatalf("--mark-green: %d %s %s", code, out, errb)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	genuine := w405TagRecord(t, w, lines[len(lines)-1])
	relGit(t, w.origin, "update-ref", "-d", "refs/tags/"+lines[len(lines)-1])
	relGit(t, w.work, "tag", "-d", lines[len(lines)-1])
	for name, mod := range map[string]func(r *w405Record){
		"the daemon's runner": func(r *w405Record) { r.Runner = "verifyrun.Run" },
		"another version":     func(r *w405Record) { r.RunnerVersion = "0" },
		"another plan":        func(r *w405Record) { r.PlanDigest = strings.Repeat("0", 64) },
		"another gate id":     func(r *w405Record) { r.GateID = strings.Repeat("1", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			r := genuine
			mod(&r)
			w405PlantTag(t, w.work, "green/20261108-1", head, r.json(t), true)
			defer func() {
				relGit(t, w.origin, "update-ref", "-d", "refs/tags/green/20261108-1")
				relGit(t, w.work, "tag", "-d", "green/20261108-1")
			}()
			if got, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 1 || got != "" || !strings.Contains(errb, "HEAD's are") {
				t.Fatalf("check = %d %q %s, want not green", code, got, errb)
			}
		})
	}
	// the genuine record, replanted: green; under other limits: not
	w405PlantTag(t, w.work, "green/20261108-1", head, genuine.json(t), true)
	if got, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 0 || got != "green/20261108-1" {
		t.Fatalf("check of the genuine record: %d %q %s", code, got, errb)
	}
	relGit(t, w.origin, "update-ref", "-d", "refs/tags/green/20261108-1")
	relGit(t, w.work, "tag", "-d", "green/20261108-1")
	out, errb, code = w405RunGate(t, w, nil, "--mark-green", "--summary", "under other limits", "--pids", "99")
	if code != 0 {
		t.Fatalf("--mark-green --pids 99: %d %s %s", code, out, errb)
	}
	if got, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 1 || got != "" || !strings.Contains(errb, "HEAD's are") {
		t.Fatalf("check of a gate run under other container limits = %d %q %s, want not green", code, got, errb)
	}
}

// TestW405GreenTagIgnoresWhatIsNotAMarker: a lightweight tag, a malformed
// message, a message naming another commit, a record lacking the runner or
// the plan digest, or a tag only in the local clone is never green.
func TestW405GreenTagIgnoresWhatIsNotAMarker(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	head := relGit(t, w.work, "rev-parse", "HEAD")
	prev := relGit(t, w.work, "rev-parse", "HEAD^1")
	gid := w405GateID(t, w.work)
	good := w405Record{Commit: head, GateID: gid, Runner: "gate.py", RunnerVersion: "2", PlanDigest: strings.Repeat("a", 64), VerifiedAt: "2026-11-08T00:00:00Z", Summary: "x"}
	other := good
	other.Commit = prev
	badTime := good
	badTime.VerifiedAt = "yesterday"
	for name, plant := range map[string]func(){
		"lightweight":                   func() { w405PlantTag(t, w.work, "green/20261108-1", head, "", true) },
		"not JSON":                      func() { w405PlantTag(t, w.work, "green/20261108-1", head, "green", true) },
		"another commit in the message": func() { w405PlantTag(t, w.work, "green/20261108-1", head, other.json(t), true) },
		"a bad verified_at":             func() { w405PlantTag(t, w.work, "green/20261108-1", head, badTime.json(t), true) },
		"the old record without runner or plan": func() {
			w405PlantTag(t, w.work, "green/20261108-1", head, `{"commit":"`+head+`","gate_id":"`+gid+`","verified_at":"2026-11-08T00:00:00Z","summary":"x"}`, true)
		},
		"local only":                func() { w405PlantTag(t, w.work, "green/20261108-1", head, good.json(t), false) },
		"a name outside the format": func() { w405PlantTag(t, w.work, "green/2026-11-08", head, good.json(t), true) },
	} {
		t.Run(name, func(t *testing.T) {
			for _, ref := range strings.Fields(relGit(t, w.origin, "for-each-ref", "--format=%(refname)", "refs/tags/green")) {
				relGit(t, w.origin, "update-ref", "-d", ref)
			}
			for _, tg := range strings.Fields(relGit(t, w.work, "tag", "-l", "green*")) {
				relGit(t, w.work, "tag", "-d", tg)
			}
			plant()
			if got, errb, code := w405Tool(t, w, nil, "tools/green-tag", "check", head); code != 1 || got != "" {
				t.Fatalf("check = %d %q %s, want not green", code, got, errb)
			}
			if got, _, code := w405Tool(t, w, nil, "tools/green-tag", "latest"); code != 1 || got != "" {
				t.Fatalf("latest = %d %q, want none", code, got)
			}
		})
	}
}
