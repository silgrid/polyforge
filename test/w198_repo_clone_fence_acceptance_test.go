package test

// w198_repo_clone_fence_acceptance_test.go: the polyforge#198 acceptance
// suite — authored against the SPEC (the capsule's
// artifacts/e2e-scenarios.md mirrors spec.md's Acceptance criteria),
// BEFORE the implementation lands. It drives only real surfaces: the
// pf/aihub binaries, `pf --json doctor`, cli.toml, real git clones under
// <workspace>/.repo/, and the shipped skill markdown.
//
// The doctor row's NAME is a plan-level choice the spec does not pin, so
// every assertion locates rows by content: the unique clone directory
// name ("cl-…") inside a row's detail, plus the row's status. Absence
// assertions ("never warns") are always paired with a positive anchor
// clone in the same workspace that MUST warn, so they cannot pass
// vacuously on a tree without the check.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w198Install is the standard install with `pi-web` registered as a repo
// of the e2e project (today's real repo name), so clone `.repo/pi-web`
// belongs to a mapped project and any other clone name does not.
func w198Install(t *testing.T) *v1Install {
	return newV1InstallRepos(t, []api.ProjectRepo{{Name: "pi-web", URL: "file:///nonexistent/pi-web.git"}})
}

// w198Clone makes <ws>/.repo/<name> a real git clone of a fresh bare
// remote, on branch, with that branch pushed (remote-tracking ref set):
// clean, and with no unpushed commit, until the caller dirties it.
func w198Clone(t *testing.T, ws, name, branch string) string {
	t.Helper()
	bare := t.TempDir()
	gitRun(t, bare, "init", "-q", "--bare", "-b", "main")
	dir := filepath.Join(ws, ".repo", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	w198Write(t, dir, "deploy-piweb.sh", "#!/bin/sh\necho deploy\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	gitRun(t, dir, "remote", "add", "origin", bare)
	gitRun(t, dir, "push", "-q", "-u", "origin", "main")
	if branch != "main" {
		gitRun(t, dir, "checkout", "-q", "-b", branch)
		gitRun(t, dir, "push", "-q", "-u", "origin", branch)
	}
	return dir
}

func w198Write(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w198Dirty modifies the tracked deploy-piweb.sh (` M deploy-piweb.sh`).
func w198Dirty(t *testing.T, dir string) {
	w198Write(t, dir, "deploy-piweb.sh", "#!/bin/sh\necho hand edit\n")
}

// w198Doctor runs `pf --json doctor [extra…]` with a config mapping ws to
// the e2e project (server/key from the install unless server overrides).
func w198Doctor(t *testing.T, inst *v1Install, ws, server, sock string, extra ...string) (*w113DoctorDoc, string, int) {
	t.Helper()
	env := w113Env{json: true}
	if server == "" {
		server = inst.url
	} else {
		env.noConn = true // the config's dead server is the value under test
	}
	if sock == "" {
		sock = filepath.Join(t.TempDir(), "dead.sock")
	}
	env.config = w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"), server, inst.adminKey, sock,
		[]w113WS{{key: ws, projects: []string{v1Project}}})
	out, code := w113Pf(t, inst, env, append([]string{"doctor"}, extra...)...)
	return w113DoctorJSON(t, out), out, code
}

// w198Rows returns the rows with the given status (any status when "")
// whose detail names the clone.
func w198Rows(doc *w113DoctorDoc, status, clone string) []w113DoctorCheck {
	var rows []w113DoctorCheck
	for _, c := range doc.Checks {
		if (status == "" || c.Status == status) && strings.Contains(c.Detail, clone) {
			rows = append(rows, c)
		}
	}
	return rows
}

// w198MustWarn fails unless some warn row names the clone; returns it.
func w198MustWarn(t *testing.T, doc *w113DoctorDoc, out, clone string) w113DoctorCheck {
	t.Helper()
	rows := w198Rows(doc, "warn", clone)
	if len(rows) == 0 {
		t.Fatalf("pf doctor: no warn row names clone %q (want an uncovered-change warning; output %q)", clone, out)
	}
	return rows[0]
}

// w198MustNotWarn fails when any warn or fail row names the clone.
func w198MustNotWarn(t *testing.T, doc *w113DoctorDoc, out, clone string) {
	t.Helper()
	for _, st := range []string{"warn", "fail"} {
		if rows := w198Rows(doc, st, clone); len(rows) > 0 {
			t.Fatalf("pf doctor: clone %q must produce no warning, got %s row %+v (output %q)", clone, st, rows[0], out)
		}
	}
}

// S1 — criteria 4 + 12: today's real pi-web shape warns, and the warning
// names the remediation (pf file create + delivery branch, or discard).
func TestW198DirtyReleaseCloneWarns(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	w198Dirty(t, w198Clone(t, ws, "pi-web", "release/v0.0.27"))
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	row := w198MustWarn(t, doc, out, "pi-web")
	for _, tok := range []string{"pf file create", "polyforge/", "discard"} {
		if !strings.Contains(row.Detail, tok) {
			t.Fatalf("pf doctor: the pi-web warning does not name remediation token %q (row %+v)", tok, row)
		}
	}
}

// S2 — criterion 5: a clean clone whose HEAD commit is on no remote ref
// warns like uncommitted changes.
func TestW198UnpushedLocalCommitWarns(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	dir := w198Clone(t, ws, "cl-unpushed", "feature/w198")
	w198Write(t, dir, "local.txt", "hand work\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "local only")
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	w198MustWarn(t, doc, out, "cl-unpushed")
}

// S3 — criterion 6: a dirty clone on polyforge/<project>-<seq> whose WI
// is queued warns for nothing (anchor: an uncovered dirty clone warns).
func TestW198CoveredCloneNoWarning(t *testing.T) {
	inst := w198Install(t)
	wi := inst.newWI(t, "w198 covered", nil, nil)
	if st := inst.wiStatus(t, wi); st != "queued" {
		t.Fatalf("fixture: work item %s is %q, want queued (no daemon runs here)", wi, st)
	}
	ws := t.TempDir()
	w198Dirty(t, w198Clone(t, ws, "cl-covered", "polyforge/"+wiPathName(t, inst, wi)))
	w198Dirty(t, w198Clone(t, ws, "cl-anchor", "release/v0.0.27"))
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	w198MustWarn(t, doc, out, "cl-anchor")
	w198MustNotWarn(t, doc, out, "cl-covered")
}

// S4 — criterion 7: the delivery branch of a canceled WI, and a detached
// HEAD, are uncovered and warn.
func TestW198TerminalOrDetachedWarns(t *testing.T) {
	inst := w198Install(t)
	wi := inst.newWI(t, "w198 canceled", nil, nil)
	if _, err := inst.client.CancelWorkItem(wi); err != nil {
		t.Fatalf("cancel %s: %v", wi, err)
	}
	inst.waitWIStatus(t, wi, api.StatusCanceled, 30*time.Second)
	ws := t.TempDir()
	w198Dirty(t, w198Clone(t, ws, "cl-canceled", "polyforge/"+wiPathName(t, inst, wi)))
	det := w198Clone(t, ws, "cl-detached", "main")
	gitRun(t, det, "checkout", "-q", "--detach")
	w198Dirty(t, det)
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	w198MustWarn(t, doc, out, "cl-canceled")
	w198MustWarn(t, doc, out, "cl-detached")
}

// S5 — criterion 8: a dirty clone of a repo in no mapped project warns
// with the "not registered in any mapped project" detail.
func TestW198UnregisteredRepoCloneWarns(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	w198Dirty(t, w198Clone(t, ws, "cl-stray", "main"))
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	row := w198MustWarn(t, doc, out, "cl-stray")
	if !strings.Contains(row.Detail, "not registered in any mapped project") {
		t.Fatalf("pf doctor: the cl-stray warning lacks the not-registered detail (row %+v)", row)
	}
}

// S6 — criterion 9: clean clones (on any branch) and non-git directories
// under .repo/ never warn (anchor: a dirty clone in the same .repo warns).
func TestW198CleanAndNonGitNeverWarn(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	w198Clone(t, ws, "cl-clean", "release/v0.0.27")
	plain := filepath.Join(ws, ".repo", "cl-plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	w198Write(t, plain, "notes.txt", "not a repo\n")
	w198Dirty(t, w198Clone(t, ws, "cl-anchor", "main"))
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	w198MustWarn(t, doc, out, "cl-anchor")
	w198MustNotWarn(t, doc, out, "cl-clean")
	w198MustNotWarn(t, doc, out, "cl-plain")
}

// S7 — criterion 10: a linked worktree (living in a non-git wrapper dir
// under .repo/, the pi-web-worktrees shape) is scanned and its dirt is
// reported under the worktree's own name.
func TestW198LinkedWorktreeScanned(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	parent := w198Clone(t, ws, "cl-parent", "main")
	wt := filepath.Join(ws, ".repo", "cl-wrap", "cl-wtree")
	gitRun(t, parent, "worktree", "add", "-q", "-b", "feature/wt", wt)
	w198Write(t, wt, "scratch-edit.txt", "untracked hand work\n")
	doc, out, _ := w198Doctor(t, inst, ws, "", "")
	w198MustWarn(t, doc, out, "cl-wtree")
}

// S8 — criterion 11: on an otherwise healthy machine the warning keeps
// exit 0 / ok=true, and `pf doctor --harness` does not run the check.
func TestW198WarnOnlyAndNotInHarness(t *testing.T) {
	inst := w198Install(t)
	ws := t.TempDir()
	w198Dirty(t, w198Clone(t, ws, "cl-exit", "release/v0.0.27"))
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	d := w113NewDaemon(t, inst, w113DOptConfig(cfgPath))
	w113ConfigFile(t, cfgPath, inst.url, inst.adminKey, d.sock,
		[]w113WS{{key: ws, projects: []string{v1Project}}})
	d.start()
	defer d.stop()
	out, code := w113Pf(t, inst, w113Env{config: cfgPath, json: true}, "doctor")
	doc := w113DoctorJSON(t, out)
	w198MustWarn(t, doc, out, "cl-exit")
	if code != 0 || !doc.OK {
		t.Fatalf("pf doctor: exit %d ok=%t with only a clone warning, want 0/true (output %q)", code, doc.OK, out)
	}
	hout, _ := w113Pf(t, inst, w113Env{config: cfgPath, json: true}, "doctor", "--harness")
	if strings.Contains(hout, "cl-exit") {
		t.Fatalf("pf doctor --harness ran the clone check (output %q)", hout)
	}
}

// S9 — criterion 13: aihub unreachable — a dirty delivery-branch clone
// gets a skip naming it, never an "uncovered" warning; a dirty clone on
// release/* still warns (locally decidable).
func TestW198AihubUnreachableSkipVsLocalWarn(t *testing.T) {
	inst := w198Install(t)
	dead := "http://127.0.0.1:59997"

	wsA := t.TempDir()
	w198Dirty(t, w198Clone(t, wsA, "cl-delivery", "polyforge/e2e-7"))
	doc, out, _ := w198Doctor(t, inst, wsA, dead, "")
	w198MustNotWarn(t, doc, out, "cl-delivery")
	if len(w198Rows(doc, "skip", "cl-delivery")) == 0 {
		t.Fatalf("pf doctor (aihub down): no skip row names cl-delivery with the reason (output %q)", out)
	}

	wsB := t.TempDir()
	w198Dirty(t, w198Clone(t, wsB, "cl-local", "release/v0.0.27"))
	doc, out, _ = w198Doctor(t, inst, wsB, dead, "")
	w198MustWarn(t, doc, out, "cl-local")
}

// S10 — criteria 1–3, 14, 15 (doc contract): the shipped skill carries
// the chat-agent session rule, and doctor.md documents the new check.
func TestW198SkillAndDoctorDocsTeachFence(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ToLower(string(b))
	}
	skill := read("skills/polyforge/SKILL.md")
	for _, tok := range []string{"no ticket", ".repo/", "pf file create", "chat"} {
		if !strings.Contains(skill, tok) {
			t.Fatalf("SKILL.md lacks the chat-agent .repo write rule token %q", tok)
		}
	}
	doctor := read("skills/polyforge/verbs/doctor.md")
	for _, tok := range []string{"polyforge/<project>-<seq>", "unpushed", "worktree"} {
		if !strings.Contains(doctor, tok) {
			t.Fatalf("verbs/doctor.md does not document the clone check (token %q missing)", tok)
		}
	}
}
