package daemon

// stephandoff.go: `pf step handoff` (workflow contract 4, design #483
// §5.1 and §6; stage C1). The dispatch ends with one status
// and a body; the daemon checks the call's shape on the spot — the
// refusal is the step's to fix, and nothing is written — then stamps the
// section's header from its own records and appends the section to
// artifacts/handoff.md, and the call's result line records the accepted
// hand-off: the ledger, not the document, is the daemon's record of it.
//
// What the daemon does NOT do: judge the body (its shape only: the seven
// template sections, the re-entry section, the length), or read the
// owner's words — --per-owner is checked for existence in the record (an
// owner message to this step in this round, by its stamp) and copied
// verbatim. Reacting to the status (the next step, a rewind, a cancel) is
// the driver's, from workflow contract 4 on; C1 records.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// handoffVerb checks, stamps and appends one hand-off.
func (sc *stepCall) handoffVerb(w http.ResponseWriter, raw json.RawMessage) {
	var a stepcall.HandoffArgs
	if !decodeArgs(w, raw, &a) {
		return
	}
	j := sc.j
	lines, err := stepcall.Read(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: "+err.Error())
		return
	}
	refuse := func(format string, args ...any) {
		writeErr(w, api.ErrDaemonBadInput, "handoff refused: "+fmt.Sprintf(format, args...))
	}
	if prev := stepcall.HandoffOf(lines, j.step, int64(j.attempt)); prev != nil {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("handoff refused: this dispatch (step %s, spawn %d) already handed off %s (call %d) — a dispatch hands off once: end your turn", j.step, j.attempt, prev.Handoff.Status, prev.Call))
		return
	}
	switch {
	case !stepcall.IsStatus(a.Status):
		refuse("--status %q is none of %s", a.Status, strings.Join(stepcall.Statuses, ", "))
		return
	case strings.ContainsAny(a.Reason, "\r\n"):
		refuse("--reason is one line")
		return
	case a.Status == stepcall.StatusRewind && strings.TrimSpace(a.To) == "":
		refuse("rewind needs --to <step> (the step to run again; this step's own id re-runs it)")
		return
	case a.Status != stepcall.StatusRewind && a.To != "":
		refuse("--to belongs to rewind only")
		return
	case stepcall.NeedsOwner(a.Status) && strings.TrimSpace(a.PerOwner) == "":
		refuse("%s carries out the owner's word: --per-owner <at> names the owner message it rests on (its stamp in runtime-state/asks.jsonl)", a.Status)
		return
	}
	if a.Status != stepcall.StatusBlocked && a.Status != stepcall.StatusCancel {
		why, readable := sc.laneRefusal(a, lines)
		if !readable {
			writeErr(w, api.ErrDaemonInternal, "handoff: the capsule's lane snapshot is unreadable, so "+a.Status+" cannot be checked")
			return
		}
		if why != "" {
			refuse("%s", why)
			return
		}
	}
	var st *capsule.Step
	if sc.ex != nil {
		st = sc.ex.Steps[j.step]
	}
	asks, err := capsule.ReadAsks(j.capsuleDir)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: "+err.Error())
		return
	}
	owner := driver.OwnerMessagesOfRound(asks, j.step, st)
	var perOwner *capsule.AskLine
	if a.PerOwner != "" {
		for i := range owner {
			if owner[i].At == a.PerOwner {
				perOwner = &owner[i]
			}
		}
		if perOwner == nil {
			refuse("--per-owner %s names no owner message to step %s in its current round (runtime-state/asks.jsonl: the reply and message lines of this step since the round opened)", a.PerOwner, j.step)
			return
		}
	}
	if err := stepcall.CheckBody(a.Body, len(stepcall.StepHandoffs(lines, j.step)) > 0, capsule.DispatchLanguage(j.capsuleDir, j.step, int64(j.attempt))); err != nil {
		refuse("%v", err)
		return
	}
	evidence, err := evidencePaths(j.capsuleDir, a.Evidence)
	if err != nil {
		refuse("%v", err)
		return
	}
	h, rec := sc.stamp(lines, st, a, evidence, owner, perOwner)
	if err := stepcall.AppendHandoff(j.capsuleDir, stepcall.RenderSection(h, a.Body)); err != nil {
		writeErr(w, api.ErrDaemonInternal, "handoff: the section could not be appended: "+err.Error())
		return
	}
	sc.handoff = rec
	writeJSON(w, http.StatusOK, map[string]any{
		"call": sc.call.Seq, "step": j.step, "round": h.Round, "spawn": j.attempt,
		"status": a.Status, "to": a.To, "path": capsule.HandoffMD,
	})
}

// laneRefusal checks a status against the capsule's lane snapshot and the
// work item's record: done needs every wrap attr the step declares
// (`wrap_attrs`, owner ruling R7) registered in its round; fail is a
// gate's, and once the budget the FAIL's reason spends is spent — the
// lane-declared uncounted class's own cap when the reason falls in it
// (polyforge#67), else the lane's max_rewinds — a fail must cite the
// owner (--per-owner: the gate asks first, design #483 §5.1); waive is a
// waivable gate's; rewind's --to a step of the lane at or before this one.
// why "" = the status stands; readable false = the lane snapshot's
// workflow.yaml cannot be read (nothing can be checked). Only the step graph
// is read: a broken definition of another step is the driver's to meet
// at its dispatch, never a reason to refuse this hand-off.
func (sc *stepCall) laneRefusal(a stepcall.HandoffArgs, lines []stepcall.Line) (why string, readable bool) {
	wf := capsuleLaneGraph(sc.j.capsuleDir)
	if wf == nil {
		return "", false
	}
	self := -1
	for i, s := range wf.Steps {
		if s.ID == sc.j.step {
			self = i
		}
	}
	if self < 0 {
		return fmt.Sprintf("step %s is no step of lane %s", sc.j.step, wf.Name), true
	}
	spec := wf.Steps[self]
	switch a.Status {
	case stepcall.StatusDone:
		registered := map[string]bool{}
		for _, op := range stepcall.PendingOps(lines, spec.ID, sc.call.Round) {
			if op.Verb == stepcall.HubWrapAttrs {
				registered[op.Name] = true
			}
		}
		var missing []string
		for _, name := range spec.WrapAttrs {
			if !registered[name] {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf("step %s registers the wrap attr(s) %s before it hands off done (the lane's wrap_attrs): run `pf step hub wrap-attrs <name> @<file>` for each, then hand off again", spec.ID, strings.Join(missing, ", ")), true
		}
		// the owner's explicit release (plan-review ruling 1): a done citing
		// an owner message of this round skips the proof gate; handoffVerb
		// checks the at and stamp records it on the section. The same path
		// the spent rewind budget gives a gate's fail.
		if a.PerOwner == "" {
			if why := sc.fastCheckRefusal(spec); why != "" {
				return why, true
			}
		}
	case stepcall.StatusFail:
		if !spec.Gate {
			return fmt.Sprintf("fail is a gate's verdict and %s is no gate: a step that cannot go on hands off blocked", spec.ID), true
		}
		if spent := driver.FailRewindBudgetSpent(wf, sc.ex, a.Reason); a.PerOwner == "" && spec.OnFail != nil && spent != "" {
			return spent + ": a fail without the owner's word can rewind no more — ask the owner first (`pf step ask`: one more round, accepting the FAIL on a waivable gate, or cancelling the work item) and hand off as the reply says, citing it with --per-owner", true
		}
	case stepcall.StatusWaive:
		if !spec.Gate || !spec.Waivable {
			return fmt.Sprintf("waive is a waivable gate's and %s is not one (the lane does not declare it waivable)", spec.ID), true
		}
	case stepcall.StatusRewind:
		for i := 0; i <= self; i++ {
			if wf.Steps[i].ID == a.To {
				return "", true
			}
		}
		var ids []string
		for i := 0; i <= self; i++ {
			ids = append(ids, wf.Steps[i].ID)
		}
		return fmt.Sprintf("--to %s names no step at or before %s in lane %s (%s)", a.To, spec.ID, wf.Name, strings.Join(ids, ", ")), true
	}
	return "", true
}

// proofSpec is one proof key of a step's lane: the result file's path
// template, the one command that writes it, and the two labels a refusal
// uses (the key's name and what the result is called in prose).
type proofSpec struct {
	key, label      string
	result, command string
}

// proofSpecs answers a step's declared proofs in the order a refusal names
// them: the fast-check proof, then the mechanical facts summary
// (polyforge#211, #241).
func proofSpecs(spec driver.StepSpec) []proofSpec {
	var out []proofSpec
	if spec.FastCheckProof != nil {
		out = append(out, proofSpec{key: "fast_check_proof", label: "fast-check result",
			result: spec.FastCheckProof.Result, command: spec.FastCheckProof.Command})
	}
	if spec.FactsProof != nil {
		out = append(out, proofSpec{key: "facts_proof", label: "mechanical facts result",
			result: spec.FactsProof.Result, command: spec.FactsProof.Command})
	}
	return out
}

// fastCheckRefusal answers why a step declaring `fast_check_proof` or
// `facts_proof` may not hand off done ("" when it may): every task repo
// whose round's HEAD moved needs the lane's result file, a
// pf-step-result/1 whose verdict is pass and whose heads carry the repo at
// its current HEAD. A repo whose round start is its HEAD at hand-off is no
// change (polyforge#211, C3'). The text starts with the step — the
// hand-off's refuse() adds its own prefix — and quotes the lane's command,
// so the daemon holds no repository's command.
func (sc *stepCall) fastCheckRefusal(spec driver.StepSpec) string {
	proofs := proofSpecs(spec)
	if len(proofs) == 0 {
		return ""
	}
	reposDir := filepath.Join(sc.j.capsuleDir, capsule.DirRepos)
	var starts map[string]string
	if sc.ex != nil {
		if st := sc.ex.Steps[sc.j.step]; st != nil && st.StartHeads != nil && st.StartHeads.Round == stepRound(sc.ex, sc.j.step) {
			starts = st.StartHeads.Heads
		}
	}
	var problems []string
	var keys []string
	for _, pr := range proofs {
		keys = append(keys, pr.key)
		for _, name := range taskrepo.Names(reposDir) {
			dir := filepath.Join(reposDir, name)
			head := repoHead(dir)
			if head == "" {
				continue // no HEAD, no commit of this step's: nothing to prove (Q11)
			}
			start, known := starts[name]
			if known && start == head {
				continue // the round did not move this repo
			}
			if known && !hasLocalOnlyCommit(dir) {
				continue // a fast-forward: HEAD carries no commit of this work item's
			}
			if problem := sc.proofProblem(pr, name, head); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return fmt.Sprintf("step %s hands off done only with a passing check of each repo it changed (the lane's %s): %s — then hand off again",
		spec.ID, strings.Join(keys, ", "), strings.Join(problems, "; "))
}

// proofProblem answers the failing check of one changed repo ("" when its
// result is a readable pf-step-result/1 file at HEAD).
func (sc *stepCall) proofProblem(pr proofSpec, name, head string) string {
	cmd := strings.ReplaceAll(pr.command, "{repo}", name)
	rel := strings.ReplaceAll(pr.result, "{repo}", name)
	abs := filepath.Join(sc.j.capsuleDir, filepath.FromSlash(rel))
	if !insideCapsule(sc.j.capsuleDir, abs) {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (the path leaves the capsule); run `%s` from the capsule root", name, rel, cmd)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("repos/%s: no %s at %s; run `%s` from the capsule root", name, pr.label, rel, cmd)
		}
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (unreadable path: %v); run `%s` from the capsule root", name, rel, err, cmd)
	}
	if !insideCapsule(sc.j.capsuleDir, real) {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (the path leaves the capsule); run `%s` from the capsule root", name, rel, cmd)
	}
	raw, err := os.ReadFile(real)
	if err != nil {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (%v); run `%s` from the capsule root", name, rel, err, cmd)
	}
	var r struct {
		Schema  string             `json:"schema"`
		Verdict *string            `json:"verdict"`
		Heads   *map[string]string `json:"heads"`
		Summary string             `json:"summary"`
		Details struct {
			Hits []proofHit `json:"hits"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (%v); run `%s` from the capsule root", name, rel, err, cmd)
	}
	if r.Schema != "pf-step-result/1" {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (schema %q); run `%s` from the capsule root", name, rel, r.Schema, cmd)
	}
	if r.Heads == nil {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (no heads); run `%s` from the capsule root", name, rel, cmd)
	}
	if got, ok := (*r.Heads)[name]; !ok {
		return fmt.Sprintf("repos/%s: %s ran at no head of repos/%s, but HEAD is %s; run `%s` from the capsule root", name, rel, name, head, cmd)
	} else if got != head {
		return fmt.Sprintf("repos/%s: %s ran at %s, but HEAD is %s; run `%s` from the capsule root", name, rel, got, head, cmd)
	}
	if r.Verdict == nil {
		return fmt.Sprintf("repos/%s: %s is not a pf-step-result/1 result (no verdict); run `%s` from the capsule root", name, rel, cmd)
	}
	if *r.Verdict != "pass" {
		msg := fmt.Sprintf("repos/%s: %s has verdict %s, not pass (%s)", name, rel, *r.Verdict, resultSummary(r.Summary))
		if hits := hitLines(r.Details.Hits); hits != "" {
			msg += "; hits: " + hits
		}
		return msg + fmt.Sprintf("; fix it, then run `%s` from the capsule root", cmd)
	}
	return ""
}

// proofHit is one deterministic hit a facts.py handoff result carries in
// `details.hits`.
type proofHit struct {
	Item   string `json:"item"`
	Repo   string `json:"repo"`
	Unit   string `json:"unit"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// maxRefusalHits bounds the hits a refusal returns inline (the whole list
// stays in the result file the refusal names).
const maxRefusalHits = 6

// hitLines answers a result's hits one line each, the tail named by count
// ("" when none): the refusal returns what the build must fix, not a
// summary of it.
func hitLines(hits []proofHit) string {
	if len(hits) == 0 {
		return ""
	}
	var lines []string
	for i, h := range hits {
		if i == maxRefusalHits {
			lines = append(lines, fmt.Sprintf("… and %d more", len(hits)-maxRefusalHits))
			break
		}
		bits := []string{h.Item}
		if h.Repo != "" {
			bits = append(bits, "repos/"+h.Repo)
		}
		if h.Unit != "" {
			bits = append(bits, "unit "+h.Unit)
		}
		if h.Path != "" {
			bits = append(bits, h.Path)
		}
		lines = append(lines, clipRunes(strings.Join(bits, " ")+": "+h.Reason, 240))
	}
	return strings.Join(lines, "; ")
}

// clipRunes clips s to n runes, marking the cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// resultSummary is the one-line part of a result's summary a refusal
// quotes: newlines become spaces and the text stops at 200 characters.
func resultSummary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		return string(r[:199]) + "…"
	}
	return s
}

// hasLocalOnlyCommit reports whether HEAD carries a commit no
// remote-tracking ref carries: a commit this work item made. A fast-forward
// onto the default branch leaves it empty; an unreadable answer counts as a
// local commit, so the proof is demanded (never a silent waiver).
func hasLocalOnlyCommit(dir string) bool {
	out, err := taskrepo.Git(dir, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes")
	return err != nil || strings.TrimSpace(out) != ""
}

// repoHead answers a task repo's HEAD ("" when it has none or git cannot
// read it).
func repoHead(dir string) string {
	out, err := taskrepo.Git(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// insideCapsule reports whether path stays inside the capsule root.
func insideCapsule(capsuleDir, path string) bool {
	rel, err := filepath.Rel(capsuleDir, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// stamp gathers the header's facts from the daemon's own records — the
// execution record (round, step start, candidate, the round's attempts,
// the HEADs its round started from), the spawn journal (the dispatch's
// start and harness when the record has none), the owner messages of the
// round, the task repos (each repo from its round start commit, to its
// HEAD now; a repo with no recorded start is "start unknown") and the
// language the dispatch was told (its spawn-journal line: the header is
// stamped in it, never the live value) — and answers it with the ledger's
// record of the hand-off.
func (sc *stepCall) stamp(lines []stepcall.Line, st *capsule.Step, a stepcall.HandoffArgs, evidence []string, owner []capsule.AskLine, perOwner *capsule.AskLine) (stepcall.Header, *stepcall.Handoff) {
	j := sc.j
	now := time.Now().UTC()
	h := stepcall.Header{
		Call: sc.call.Seq, Step: j.step, Round: stepRound(sc.ex, j.step), Spawn: int64(j.attempt),
		EndedAt: now.Format(time.RFC3339), Status: a.Status, To: a.To, Reason: a.Reason,
		OwnerMessages: owner, PerOwner: perOwner, Evidence: evidence,
		Language: capsule.DispatchLanguage(j.capsuleDir, j.step, int64(j.attempt)),
	}
	h.Usage = handoffUsage(sc.d, j.capsuleDir, j.step, int64(j.attempt))
	l := stepcall.LabelsOf(h.Language)
	if st != nil {
		h.StartedAt, h.Model = st.StepStartedAt, st.Candidate
	}
	if h.StartedAt == "" || h.Model == "" {
		entries, _ := capsule.ReadSpawnJournal(j.capsuleDir)
		for _, e := range entries {
			if e.StepID == j.step && int64(e.SpawnN) == int64(j.attempt) {
				if h.StartedAt == "" {
					h.StartedAt = e.At
				}
				if h.Model == "" {
					h.Model = e.Harness
				}
			}
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, h.StartedAt); err == nil {
		h.Duration = now.Sub(t).Round(time.Second).String()
	}
	for _, at := range driver.RoundAttempts(st) {
		h.Events = append(h.Events, attemptEvent(at))
	}
	reposDir := filepath.Join(j.capsuleDir, capsule.DirRepos)
	heads := map[string]string{}
	for _, name := range taskrepo.Names(reposDir) {
		out, err := taskrepo.Git(filepath.Join(reposDir, name), "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if sha := strings.TrimSpace(out); err == nil && sha != "" {
			heads[name] = sha
		}
	}
	// each repo's start commit is the step round's own (the driver records
	// the HEADs when the round's first dispatch starts): commits made
	// before the round — another step's, a command's between hand-offs —
	// are never this section's
	var starts map[string]string
	if st != nil && st.StartHeads != nil && st.StartHeads.Round == h.Round {
		starts = st.StartHeads.Heads
	}
	names := make([]string, 0, len(heads))
	for n := range heads {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		start, end := starts[n], heads[n]
		h.Repos = append(h.Repos, stepcall.RepoChange{Repo: n, Start: start, End: end, Stat: diffStat(filepath.Join(reposDir, n), start, end, l)})
	}
	rec := &stepcall.Handoff{Status: a.Status, To: a.To, Reason: a.Reason, Evidence: evidence, Heads: heads, Candidate: h.Model, StartedAt: h.StartedAt}
	if perOwner != nil {
		rec.PerOwner = perOwner.At
	}
	return h, rec
}

// handoffUsage gathers the current dispatch transcript and its persisted
// subagent sidecars. The header states inclusion explicitly even when the
// sidecar directory is empty.
func handoffUsage(d *Daemon, capsuleDir, step string, spawn int64) stepcall.UsageSummary {
	entries, _ := capsule.ReadSpawnJournal(capsuleDir)
	var current capsule.SpawnJournalEntry
	for _, e := range entries {
		if e.StepID == step && int64(e.SpawnN) == spawn {
			current = e
		}
	}
	if d == nil || d.locateTranscript == nil {
		return stepcall.ReadUsage(nil, false)
	}
	live := d.locateTranscript(capsuleDir, entries)
	if live == "" {
		return stepcall.ReadUsage(nil, false)
	}
	paths := []string{live}
	includes := false
	if rel, err := filepath.Rel(capsuleDir, live); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		for _, dir := range []string{capsule.SubagentDir(live)} {
			items, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, item := range items {
				if item.IsDir() || !strings.HasSuffix(item.Name(), ".jsonl") {
					continue
				}
				paths = append(paths, filepath.Join(dir, item.Name()))
				includes = true
			}
		}
	}
	return stepcall.ReadUsage(paths, includes, current.StartedAt)
}

func attemptEvent(at capsule.Attempt) string {
	stamp := at.ArchivedAt
	if stamp == "" {
		stamp = at.InterruptedAt
	}
	ev := stamp + " " + at.Cause
	if at.FailureCause != "" {
		ev += "/" + at.FailureCause
	}
	if at.Candidate != "" {
		ev += " (" + at.Candidate + ")"
	}
	if d := strings.TrimSpace(strings.SplitN(at.Detail, "\n", 2)[0]); d != "" {
		if len(d) > 160 {
			d = d[:160] + "…"
		}
		ev += ": " + d
	}
	return ev
}

// diffStat answers a repo's change between two commits, as git's
// --shortstat says it (its other answers in the section's labels l).
func diffStat(dir, start, end string, l stepcall.Labels) string {
	switch {
	case start == "":
		return l.StatStartUnknown
	case start == end:
		return l.StatNoCommits
	}
	out, err := taskrepo.Git(dir, "diff", "--shortstat", start, end)
	if err != nil {
		return l.StatUnavailable
	}
	if s := strings.TrimSpace(out); s != "" {
		return s
	}
	return l.StatNoChanges
}

// evidencePaths resolves each --evidence path (absolute, or relative to
// the capsule root) to an existing regular file inside the capsule, and
// answers them capsule-relative.
func evidencePaths(capsuleDir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := filepath.EvalSymlinks(capsuleDir)
	if err != nil {
		return nil, fmt.Errorf("the capsule root is unreadable: %v", err)
	}
	var out []string
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, errors.New("--evidence names an empty path")
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(capsuleDir, abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("--evidence %s: no such file in the capsule", p)
		}
		rel, err := filepath.Rel(root, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("--evidence %s is outside the capsule", p)
		}
		if fi, err := os.Stat(real); err != nil || !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("--evidence %s is not a file", p)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out, nil
}
