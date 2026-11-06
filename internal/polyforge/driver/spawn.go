package driver

// spawn.go: the production StepSpawner adapter. It bridges the driver's
// StepSpawn order onto the runtime derivation mechanism and owns:
//
//   - PROMPT ASSEMBLY: the six fixed parts of workflow contract 4
//     (assembleStepPrompt) — the dispatch, the prohibited actions, the
//     step header, the step definition, the skill and the continuation;
//   - PROCESS DERIVATION through spawn.Params -> spawn.Spawner: the model
//     pin rides argv, the resume session the spawn journal's latest
//     transcript; isolation and env sanitizing stay in runtime/spawn.go;
//   - RESULT COLLECTION (workflow contract 4): the step ends with `pf step
//     handoff`, which the daemon checks and records on the capsule's call
//     ledger (runtime-state/step-calls.jsonl); the run polls the ledger
//     for the dispatch's accepted hand-off and tears the process down. The
//     record is keyed by the spawn ordinal and every re-dispatch takes a
//     fresh one (B2), so another dispatch's hand-off is never this one's.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const (
	// dirStepDefs is the step-definition directory inside a
	// materialized bundle: workflows/<bundle>/steps/<id>.md (the R9
	// path convention).
	dirStepDefs = "steps"
	// defaultEnvelopePoll is the default hand-off collection cadence (the
	// call ledger's poll).
	defaultEnvelopePoll = 200 * time.Millisecond
	// teardownBound bounds the post-hand-off process teardown.
	teardownBound = 5 * time.Second
)

// StepSpawnAdapter is the production StepSpawner. Build one per work
// item (NewStepSpawnAdapter) and hand it to the Driver's Config; the
// driver serializes, so the adapter itself keeps no locks.
type StepSpawnAdapter struct {
	WI WIInfo
	// BundleDir is the materialized workflow bundle root (workflow.yaml
	// + steps/). Default: <CapsuleDir>/workflows/<BundleName>.
	BundleDir string
	// Workflow is the parsed workflow.yaml (workflow.go), shared with the
	// driver; nil → loaded from BundleDir at construction.
	Workflow *Workflow
	// Spawner derives the step processes. Required; production binds
	// runtime.DefaultSpawner(&runtime.Config{...}) so isolation, env
	// sanitization and the model pin keep their single home.
	Spawner spawn.Spawner
	// Link is the per-WI supervision link: the harness translator of
	// every dispatch folds the step process's stdout records into the
	// dispatch's Tracker on it. nil = no harness tracking.
	Link *supervise.Link
	// ClaudeConfigDir is the step claude processes' CLAUDE_CONFIG_DIR the
	// spawner hard-set (polyforge#171: <harness-root>/claude-config). The
	// claude resume decision locates the previous dispatch's native
	// session explicitly under it — never by the daemon's process env.
	ClaudeConfigDir string
	// PollInterval is the envelope-collection cadence (tests tighten
	// it; default defaultEnvelopePoll).
	PollInterval time.Duration
	// Now overrides the clock for journal stamps (tests).
	Now func() time.Time
	// Language answers the live agent language (`[daemon] agent_language`,
	//) at dispatch time; nil = the default. A new
	// conversation takes it; a resumed one keeps its own (SpawnStep).
	Language func() string
}

// NewStepSpawnAdapter validates the configuration, resolves the bundle
// directory and loads (and validates) the extended workflow parse —
// fail-fast at daemon assembly rather than mid-dispatch.
func NewStepSpawnAdapter(a StepSpawnAdapter) (*StepSpawnAdapter, error) {
	if a.WI.ID == "" {
		return nil, fmt.Errorf("driver: spawn adapter without a work item id")
	}
	if a.WI.CapsuleDir == "" {
		return nil, fmt.Errorf("driver: spawn adapter without a capsule dir")
	}
	if a.Spawner == nil {
		return nil, fmt.Errorf("driver: spawn adapter without a runtime spawner")
	}
	if a.BundleDir == "" {
		if a.WI.BundleName == "" {
			return nil, fmt.Errorf("driver: spawn adapter without a bundle name or dir")
		}
		a.BundleDir = filepath.Join(a.WI.CapsuleDir, capsule.DirWorkflows, a.WI.BundleName)
	}
	if a.Workflow == nil {
		wf, err := LoadWorkflow(a.BundleDir)
		if err != nil {
			return nil, err
		}
		a.Workflow = wf
	}
	if a.PollInterval <= 0 {
		a.PollInterval = defaultEnvelopePoll
	}
	return &a, nil
}

// SpawnStep derives one step process for the driver's dispatch order.
// The context policy comes from the shared workflow parse. The dispatch's
// agent language is fixed here: a dispatch that resumes
// the previous conversation keeps the language that conversation was told
// (its journal line); every new conversation takes the live value.
func (a *StepSpawnAdapter) SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error) {
	spec := a.Workflow.step(sp.StepID)
	if spec == nil {
		return nil, fmt.Errorf("driver: step %q is not in the workflow bundle", sp.StepID)
	}
	def, err := readStepDefinition(a.BundleDir, sp.StepID, spec.Vars)
	if err != nil {
		return nil, err
	}
	header, err := stepHeader(a.BundleDir)
	if err != nil {
		return nil, fmt.Errorf("driver: %s/%s: %w", a.WI.ID, sp.StepID, err)
	}
	parts := promptParts{
		WIID:                    a.WI.ID,
		StepID:                  sp.StepID,
		SpawnN:                  sp.SpawnN,
		Round:                   sp.Round,
		CapsuleRoot:             a.WI.CapsuleDir,
		StepHeader:              header,
		Definition:              def,
		PriorEndedWithoutResult: sp.PriorEndedWithoutResult,
		Language:                a.liveLanguage(),
	}
	// the step's skill (the lane snapshot's markdown, the loader judged it)
	if spec.Skill != "" {
		sk, err := workflow.ReadSkill(a.BundleDir, spec.Skill)
		if err != nil {
			return nil, fmt.Errorf("driver: %s/%s: skill %s: %w", a.WI.ID, sp.StepID, spec.Skill, err)
		}
		parts.Skill = renderSkill(sk)
	}
	h := harness.Of(sp.Harness)
	parts.NoSubagents = subagentsFact(a.BundleDir, sp.StepID, h)
	var transcript, hsession string
	resume := sp.Resume
	// degraded names why a resume the driver ordered became a new
	// conversation ("" = it resumed, or none was ordered).
	degraded := ""
	if resume {
		// Same-step continuation (an answered question, the first retry
		// after a crash, a rewind re-entry —) resumes the
		// previous dispatch's conversation only when it can. The
		// degradation conditions, each a new conversation with the previous
		// transcript as read-only reference when one is readable (a rewind
		// re-entry's round is in artifacts/handoff.md):
		//   1. another harness: the candidate in effect runs on a harness
		//      other than the previous dispatch's (an owner list edit, a
		//      failover) — a session never crosses harnesses;
		//   2. the session is gone: no journal line for the step, a claude
		//      native session that is not on disk under the adapter's
		//      claude-config dir, or an unreadable pi transcript;
		//   3. (rewind re-entry only) the previous dispatch never handed
		//      off: the call ledger records no hand-off of it, so its
		//      conversation ended in an infra failure rather than in the
		//      round the verdict judged.
		prev, err := a.latestJournalEntry(sp.StepID)
		if err != nil {
			return nil, err
		}
		switch {
		case harness.Of(prev.Harness) != h:
			resume = false
			degraded = "the candidate in effect runs on another harness than the previous dispatch"
		case h == harness.Claude:
			// claude resumes its NATIVE session (outside the capsule) by
			// the uuid the daemon minted; a session that never reached
			// disk, or an id that is not ours in shape, starts fresh.
			// polyforge#171: the session is looked up under the
			// claude-config dir the adapter carries (the spawner's
			// CLAUDE_CONFIG_DIR) — never by the daemon's process env.
			if harness.LocateClaudeSession(prev.HarnessSession, a.ClaudeConfigDir, a.WI.CapsuleDir) != "" {
				hsession = prev.HarnessSession
			} else {
				resume = false
				degraded = "the previous dispatch's session is gone"
			}
		case !usableTranscript(prev.Transcript):
			// (also the no-journal-line-for-this-step shape: "" is unusable)
			// nothing readable to resume (the previous dispatch died before
			// the harness wrote its session): start the conversation fresh
			// rather than hand the harness a path that cannot resume
			resume = false
			degraded = "the previous dispatch's session is gone"
		default:
			transcript = prev.Transcript
		}
		if resume && sp.RewindResume && !a.deliveredResult(sp.StepID, int64(prev.SpawnN)) {
			resume = false
			degraded = "the previous dispatch delivered no result"
			transcript, hsession = "", ""
		}
		if resume {
			// the resumed conversation already holds its prompt: it keeps
			// the language it was told
			parts.Language = dispatchLanguage(prev.Language)
		} else {
			// the continuation cannot resume (another harness, or the
			// native session is gone): the fresh conversation gets the
			// previous capsule transcript as read-only reference (§2.4)
			parts.Reference = a.referenceTranscript(prev)
		}
	}
	// the first ended_without_result retry that really
	// resumes the same harness session re-enters a conversation that
	// already holds the full step prompt. Re-sending it (~21KB) made the
	// agent treat the retry as a new task, re-reading artifacts and
	// re-running recall (observed on the internal project-32: a 32-minute
	// dispatch). On that one dispatch send only the short reminder.
	parts.ShortEwrResume = resume && sp.PriorEndedWithoutResult && sp.RetryTriggered
	// the owner messages this dispatch carries (relay.go): alone when it
	// resumes the conversation that ended awaiting the owner, as a section
	// of the full prompt otherwise (a degraded resume, a retry)
	parts.OwnerMessages = sp.OwnerMessages
	parts.ShortMessageResume = resume && sp.MessageResume && len(sp.OwnerMessages) > 0
	if sp.Fresh && parts.Reference == "" {
		// a retry starts a NEW conversation; the previous transcript, when
		// readable, rides the prompt as a read-only reference (it may be
		// another harness's JSONL: reference material only)
		if prev, perr := a.latestJournalEntry(sp.StepID); perr == nil {
			parts.Reference = a.referenceTranscript(prev)
		}
	}
	if sp.RewindResume && !resume {
		fmt.Fprintf(os.Stderr, "driver: %s/%s: the rewind re-entry starts a new conversation: %s\n", a.WI.ID, sp.StepID, degraded)
	}
	// the fast path. A new-conversation re-dispatch (a
	// 2nd+ failure, a candidate switch, or a resume that degraded to a
	// new conversation) whose current tree hash equals the hash the
	// step's latest failed attempt archived keeps the reference but
	// tells the new conversation to continue in place. A non-empty
	// reference, a non-empty recorded hash, the death-trigger flag and
	// the hash match are all required; a mismatch, a missing hash, a
	// hash error or an answer continuation all keep today's wording. A
	// re-claim (a fresh capsule materialization after that attempt) is
	// excluded as well: its ignored build directories did not survive,
	// so the fast path's environment claim would be false.
	if (sp.Fresh || (sp.Resume && !resume)) && sp.RetryTriggered && parts.Reference != "" && sp.RecordedTreeHash != "" &&
		!capsuleReclaimed(a.WI.CapsuleDir, sp.RecordedAttemptAt) {
		if cur, herr := treeHashFn(a.WI.CapsuleDir); herr == nil && cur == sp.RecordedTreeHash {
			parts.ContinueInPlace = true
		}
	}
	return a.launch(ctx, sp, parts, h, transcript, hsession, resume)
}

// liveLanguage answers the live agent language a new conversation takes
// (the default without a source or for a value outside the vocabulary).
func (a *StepSpawnAdapter) liveLanguage() string {
	if a.Language == nil {
		return stepcall.DefaultLanguage
	}
	return dispatchLanguage(a.Language())
}

// dispatchLanguage normalizes a recorded or live language: anything
// outside the vocabulary (a journal line that predates)
// reads as the default.
func dispatchLanguage(v string) string {
	if stepcall.IsLanguage(v) {
		return v
	}
	return stepcall.DefaultLanguage
}

// subagentsRulesVar is the shared fragment carrying the sub-agent rules a
// step definition may include ({{build_subagents}},).
const subagentsRulesVar = "build_subagents"

// piNoSubagents is the fact a pi dispatch of such a step carries (PR #869
// review, blocking 2): pi's subagent extension starts its worker as
// `pi --mode json -p --no-session …` without the step's isolation flags
// (spawn.go piIsolationFlags), and pi offers no environment or settings
// switch for context files, so the worker would load the operator's
// AGENTS.md/CLAUDE.md, skills and prompt templates the step runs
// without. Until a worker can run isolated, a pi step does the work
// itself.
const piNoSubagents = "This dispatch runs on pi, whose sub-agents do not run with this step's isolation: " +
	"a worker the `subagent` tool starts loads the context files, skills and prompt templates this " +
	"step was started without. Dispatch no sub-agent: do the analysis and build every unit yourself, " +
	"record each unit's `implemented_by` in `artifacts/build/units.json` as `main`, and say in your hand-off that this harness offers no isolated sub-agent."

// subagentsFact answers the sub-agent fact of one dispatch: piNoSubagents
// on a pi dispatch of a step whose definition includes the sub-agent
// rules, else "".
func subagentsFact(bundleDir, stepID, h string) string {
	if h != harness.Pi {
		return ""
	}
	raw, err := os.ReadFile(workflow.StepDefinitionPath(bundleDir, stepID))
	if err != nil || !strings.Contains(string(raw), "{{"+subagentsRulesVar+"}}") {
		return ""
	}
	return piNoSubagents
}

// launch derives the step process for an assembled prompt: the dispatch's
// transcript and native session, the harness translator, the spawn
// journal line, the process and the run handle.
func (a *StepSpawnAdapter) launch(ctx context.Context, sp StepSpawn, parts promptParts, h, transcript, hsession string, resume bool) (StepRun, error) {
	handle := a.WI.SessionHandle
	if transcript == "" {
		// every claude dispatch — a resume included — copies its native
		// session onto its OWN capsule file (the native file holds the
		// whole conversation); pi writes a fresh dispatch's here
		transcript = filepath.Join(a.WI.CapsuleDir, capsule.DirSession, "session-"+spawnToken(handle, sp.SpawnN)+".jsonl")
	}
	if h == harness.Claude && hsession == "" {
		id, err := harness.NewSessionID()
		if err != nil {
			return nil, fmt.Errorf("driver: step %s: claude session id: %w", sp.StepID, err)
		}
		hsession = id
	}
	translator, sink := a.newTranslator(h, sp.SpawnN)
	p := spawn.Params{
		WorkItemID:     a.WI.ID,
		CapsuleDir:     a.WI.CapsuleDir,
		LaneDir:        a.BundleDir,
		SessionHandle:  handle,
		SpawnN:         int(sp.SpawnN),
		TranscriptPath: transcript,
		Resume:         resume,
		Harness:        h,
		HarnessSession: hsession,
		InitialPrompt:  assembleStepPrompt(parts),
		Model:          sp.Model,
		Effort:         sp.Effort,
		Round:          sp.Round,
		StepID:         sp.StepID,
		ToolWindow:     sp.ToolWindow,
	}
	var taps []func(string)
	if translator != nil {
		taps = append(taps, func(line string) { translator.Feed([]byte(line)) })
	}
	// the owner-message relay's delivery proof rides the same stream
	// (relay.go: the marker in a user message, claude's started
	// lifecycle, the first turn start)
	watch := newDeliveryWatch(h)
	taps = append(taps, watch.feed)
	if h == harness.Pi {
		// the subagent conversations ride the same stdout
		// into the capsule, beside the conversation's transcript
		taps = append(taps, newPiSubagentRecorder(a.WI.CapsuleDir, transcript).Feed)
	}
	if len(taps) > 0 {
		p.OnRecord = func(line string) {
			for _, tap := range taps {
				tap(line)
			}
		}
	}
	if translator != nil {
		p.OnExit = func(info spawn.ExitInfo) {
			translator.Exit(supervise.ExitInfo{Code: info.Code, Signal: info.Signal, LastError: tailLine(info.StderrTail)})
		}
	}
	token := spawnToken(handle, sp.SpawnN)
	p.Journal = func(pid int, startedAt string) error {
		return a.appendSpawnJournal(p, sp.StepID, token, parts.Language, pid, startedAt)
	}
	proc, err := a.Spawner.Spawn(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("driver: step %s spawn %d derivation failed: %w", sp.StepID, sp.SpawnN, err)
	}
	meta := RunMeta{
		PID: proc.PID(), TranscriptPath: transcript, Harness: h, HarnessSession: hsession,
		Granularity: supervise.GranularityToken, Resumed: resume,
	}
	meta.StartedAt = time.Now()
	if translator != nil {
		meta.Granularity = translator.Granularity()
	}
	return &stepRun{
		capsuleRoot: a.WI.CapsuleDir,
		wiID:        a.WI.ID,
		sp:          sp,
		handle:      proc,
		poll:        a.PollInterval,
		meta:        meta,
		sink:        sink,
		watch:       watch,
	}, nil
}

// materializeMarkerRel is the path of the runtime's per-claim
// materialization marker (runtime.RelMaterializeMarker). The driver
// cannot import runtime (cycle), so the path is named here in one place;
// sync/ is daemon-private and never enters the capsule chain.
const materializeMarkerRel = "sync/materialized.json"

// capsuleReclaimed reports whether the capsule was (re-)materialized
// after the recorded failure attempt — a re-claim. A re-claim restores
// only the carrier HEAD and the WIP ref (tracked edits and non-ignored
// untracked files), so every .gitignore'd build directory (node_modules/,
// build outputs, venvs) is gone; the fast path's "its environment is
// intact" claim would be false ( owner ruling: owner
// `pf resume` / any re-claim keeps today's cold path). The per-claim
// marker sync/materialized.json carries the claim's completed_at, which
// postdates the recorded attempt on a re-claim and stays untouched across
// an in-place daemon-restart recovery. No new persisted state. A missing
// or unparsable stamp answers true (fail closed, cold); an absent recorded
// stamp answers false (no attempt to compare, and the fast path's
// non-empty recorded hash already gates that case).
func capsuleReclaimed(capsuleRoot, recordedAt string) bool {
	if recordedAt == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(capsuleRoot, materializeMarkerRel))
	if err != nil {
		return true
	}
	var m struct {
		CompletedAt string `json:"completed_at"`
	}
	if json.Unmarshal(raw, &m) != nil || m.CompletedAt == "" {
		return true
	}
	claim, cerr := time.Parse(time.RFC3339Nano, m.CompletedAt)
	attempt, aerr := time.Parse(time.RFC3339Nano, recordedAt)
	if cerr != nil || aerr != nil {
		return true
	}
	return claim.After(attempt)
}

// newTranslator builds the dispatch's harness translator over its
// Tracker (nil/nil without a link or for a harness without a translator).
func (a *StepSpawnAdapter) newTranslator(h string, spawnN int64) (supervise.Translator, *supervise.Tracker) {
	if a.Link == nil {
		return nil, nil
	}
	tr := a.Link.Tracker(spawnN)
	switch h {
	case harness.Pi:
		return supervise.NewPi(tr), tr
	case harness.Claude:
		return supervise.NewClaude(tr), tr
	}
	return nil, nil
}

// usableTranscript reports whether a transcript path names a non-empty
// regular file.
// referenceTranscript answers a previous dispatch's capsule transcript
// as a read-only reference ("" when there is none readable). A claude
// dispatch's capsule file is a copy of its native session that the
// commit-boundary sync writes asynchronously; it is copied here first,
// from the native session, so the reference never depends on whether
// that push has run.
func (a *StepSpawnAdapter) referenceTranscript(prev capsule.SpawnJournalEntry) string {
	if prev.Transcript == "" {
		return ""
	}
	if harness.Of(prev.Harness) == harness.Claude {
		if src := harness.LocateClaudeSession(prev.HarnessSession, a.ClaudeConfigDir, a.WI.CapsuleDir); src != "" {
			if err := sessionjsonl.CopyReadonly(src, prev.Transcript); err != nil {
				fmt.Fprintf(os.Stderr, "driver: %s: copy the previous claude session for reference: %v\n", a.WI.ID, err)
			}
		}
	}
	if !usableTranscript(prev.Transcript) {
		return ""
	}
	return prev.Transcript
}

func usableTranscript(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

// tailLine reduces a stderr tail to its last few non-empty lines.
func tailLine(tail string) string {
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < 3; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			keep = append([]string{l}, keep...)
		}
	}
	out := strings.Join(keep, " | ")
	if len(out) > 500 {
		out = out[len(out)-500:]
	}
	return out
}

// ---------------------------------------------------------------------------
// Prompt assembly (pure — table-tested below).

// promptParts is the assembled prompt's material: the six fixed parts of
// workflow contract 4 (design #483 §8) — the dispatch (WIID, StepID,
// Round, SpawnN, CapsuleRoot and this dispatch's facts: NoSubagents),
// the prohibited actions, the step header (StepHeader), the step
// definition (Definition), the skill (Skill) and the continuation
// (OwnerMessages, PriorEndedWithoutResult, Reference, ContinueInPlace).
type promptParts struct {
	WIID   string
	StepID string
	SpawnN int64
	// Round is the step round the dispatch belongs to (PF_ROUND).
	Round int64
	// CapsuleRoot is the capsule's absolute root: every capsule-relative
	// path the prompt names resolves against it (polyforge#244).
	CapsuleRoot string
	// StepHeader is the lane snapshot's _shared/step-header.md followed by
	// its _shared/handoff.md (the hand-off body template), verbatim
	// (stepHeader).
	StepHeader string
	Definition string
	// NoSubagents is the driver's fact that this dispatch dispatches no
	// sub-agent (subagentsFact); "" = none.
	NoSubagents string
	// PriorEndedWithoutResult: the previous dispatch's turn ended without
	// `pf step handoff` (retry feedback).
	PriorEndedWithoutResult bool
	// Skill is the rendered `# Skill` section of a step declaring `skill`
	// (renderSkill); "" = none.
	Skill string
	// ShortEwrResume: this dispatch resumes the SAME harness
	// session and follows an ended_without_result, so it sends only the
	// short reminder (finish the remaining work, then `pf step handoff`)
	// instead of the assembled step prompt the resumed conversation
	// already holds. False = the full prompt (every other resume, a fresh
	// retry, or a resume that degraded to fresh).
	ShortEwrResume bool
	// OwnerMessages are the undelivered owner messages this dispatch's
	// first message carries (StepSpawn.OwnerMessages, relay.go);
	// ShortMessageResume sends them ALONE — the dispatch resumes the
	// conversation that ended awaiting the owner, which holds the prompt
	// and the question already — while every other dispatch renders them
	// as the `## Owner messages` section of part 6.
	OwnerMessages      []capsule.AskLine
	ShortMessageResume bool
	// Reference is the previous attempt's transcript path (a fresh retry
	// carries it as read-only reference material; "" = none).
	Reference string
	// Language is the dispatch's agent language: part 1
	// carries its instruction (languageInstruction); "" reads as the
	// default.
	Language string
	// ContinueInPlace selects the fast-path reference
	// instruction: the tree is byte-identical to the previous attempt's
	// death, so the new conversation continues in place instead of
	// starting from scratch. False = today's start-from-scratch wording.
	ContinueInPlace bool
}

// The six parts' headings (the step header and the prohibited actions
// carry their own).
const (
	dispatchHeading     = "# This dispatch"
	definitionHeading   = "# Step definition"
	continuationHeading = "# Continuing this step"
)

// assembleStepPrompt builds the step process's one instruction of record
// from the six fixed parts of workflow contract 4 (design #483 §8), in
// order: (1) the dispatch — its identity, the language it works in, the
// capsule root and this dispatch's facts (a pi dispatch runs no sub-agent;
// where raw downloads go); (2) the prohibited actions (harness.StepProhibitedActions);
// (3) the step header and the hand-off body template
// (_shared/step-header.md, _shared/handoff.md); (4) the step definition;
// (5) the skill; (6) on a continuation, what it continues from — the owner
// messages no dispatch delivered, that the previous turn ended without
// `pf step handoff`, the previous attempt's transcript. A resume of the
// same conversation sends only part 6 (the conversation holds the rest):
// the owner's messages alone (relay.go), or the short ended-without-
// hand-off reminder.
func assembleStepPrompt(p promptParts) string {
	if p.ShortMessageResume {
		// the owner's words continue the conversation they were written
		// to (relay.go): exactly what a live delivery sends
		return renderOwnerMessages(p.OwnerMessages)
	}
	if p.ShortEwrResume {
		return ewrResumeReminder(p) + continuationPart(promptParts{OwnerMessages: p.OwnerMessages})
	}
	var b strings.Builder
	// 1. the dispatch
	b.WriteString(dispatchPart(p))
	// 2. the prohibited actions, beside the capsule-root
	// binding they scope
	b.WriteString("\n")
	b.WriteString(harness.StepProhibitedActions(p.CapsuleRoot))
	// 3. how a step runs, and the hand-off body
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(p.StepHeader))
	b.WriteString("\n")
	// 4. the step definition
	b.WriteString("\n" + definitionHeading + "\n\n")
	b.WriteString(strings.TrimSpace(p.Definition))
	b.WriteString("\n")
	// 5. the skill
	b.WriteString(p.Skill)
	// 6. the continuation
	b.WriteString(continuationPart(p))
	return b.String()
}

// The one instruction sentence per agent language part 1 carries.
const (
	languageInstructionEN = "Language: English. Work in English throughout this step: your replies and narration, every question to the owner, " +
		"the prose of the files you write, and your hand-off body under the English headings of the hand-off body template below."
	languageInstructionZhCN = "Language: Simplified Chinese (zh-CN). Work in Simplified Chinese (简体中文) throughout this step: your replies and narration, " +
		"every question to the owner, the prose of the files you write, and your hand-off body under the Chinese headings of the hand-off body template below. " +
		"Code, commands, identifiers and commit messages follow the project's conventions."
)

// languageInstruction answers part 1's sentence for a dispatch language.
func languageInstruction(lang string) string {
	if dispatchLanguage(lang) == stepcall.LangZhCN {
		return languageInstructionZhCN
	}
	return languageInstructionEN
}

// dispatchPart is part 1: the dispatch's identity, the language it works
// in, the capsule root and the facts of this dispatch the step cannot read
// anywhere else.
func dispatchPart(p promptParts) string {
	var b strings.Builder
	b.WriteString(dispatchHeading + "\n\n")
	fmt.Fprintf(&b, "You are step `%s` of polyforge work item `%s`: round %d, dispatch %d (`PF_STEP`, `PF_ROUND`, `PF_SPAWN` in your environment).\n\n", p.StepID, p.WIID, p.Round, p.SpawnN)
	b.WriteString(languageInstruction(p.Language) + "\n\n")
	// The capsule-root anchor (polyforge#244): the step is born at the
	// capsule root, but a `cd repos/<name>` can persist across its shell
	// calls (Claude Code keeps one shell cwd), so a bare `artifacts/<x>`
	// write would land in the repo worktree.
	fmt.Fprintf(&b, "Capsule root: `%[1]s`. Every capsule-relative path (`artifacts/…`, `runtime-state/…`, `workflows/…`, `repos/…`) "+
		"resolves against this root, NEVER against your current working directory — a `cd` can persist across your shell calls. "+
		"Read and write them by absolute path: `artifacts/<name>` means `%[1]s/artifacts/<name>`.\n\n", p.CapsuleRoot)
	// where raw downloads go. The capsule IS the chain, and
	// the hub refuses a blob over capsule.MaxBlobBytes; the downloads
	// directory is capsule-gitignored, and the commit guard leaves any
	// other file over the limit out (gitops/oversize.go).
	fmt.Fprintf(&b, "Raw downloads (CI artifacts, traces, screenshots and video bundles) go under `%s/`, which the capsule chain never commits; "+
		"`%s/` keeps conclusions and small excerpts. A file over %d MiB elsewhere is left out of the chain too and listed in `%s`.\n",
		capsule.DirDownloads, capsule.DirArtifacts, capsule.MaxBlobBytes>>20, capsule.OversizeManifest)
	if p.NoSubagents != "" {
		// the harness this dispatch runs on (PR #869 review, blocking 2)
		b.WriteString("\n")
		b.WriteString(p.NoSubagents)
		b.WriteString("\n")
	}
	return b.String()
}

// continuationPart is part 6: what a dispatch that continues the step
// continues from ("" for a first dispatch) — the owner messages no
// dispatch delivered (relay.go), that the previous turn ended without
// `pf step handoff`, and the previous attempt's transcript when this is a
// new conversation.
func continuationPart(p promptParts) string {
	var b strings.Builder
	if len(p.OwnerMessages) > 0 {
		b.WriteString("## Owner messages\n\n")
		b.WriteString("The owner wrote these to this step while it ran, in order; a reply names the question it answers by that question's stamp. ")
		b.WriteString("Each is verbatim; the bracketed `[pf-msg:…]` line after a message is the daemon's delivery marker, not the owner's words. ")
		b.WriteString("Act on them; one that decides more than this step's own work is carried out by the matching hand-off status citing it (`--per-owner <at>`).\n\n")
		b.WriteString(renderOwnerMessages(p.OwnerMessages))
		b.WriteString("\n")
	}
	if p.PriorEndedWithoutResult {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Your previous turn ended without `pf step handoff`\n\n")
		b.WriteString("The previous turn on this step ended without handing off. Finishing your reply is not handing off: the step only completes with one `pf step handoff`. Do the remaining work, then hand off before you stop.\n")
	}
	if p.Reference != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Previous attempt (read-only reference)\n\n")
		if p.ContinueInPlace {
			// the tree matched the failed attempt's hash, so
			// the environment is intact — continue instead of re-laying it.
			fmt.Fprintf(&b, "A previous attempt of this step ended without handing off. Its conversation transcript is at `%s` (JSONL, capsule path). "+
				"The capsule tree is byte-identical to the moment that attempt died, so its environment is intact (dependencies installed, half-run work preserved). "+
				"Read the transcript (start from the transcript tail to verify the resume point: the last unfinished action), then continue the previous attempt's work from where it stopped instead of starting over. "+
				"Do not redo environment setup that is already completed (for example `npm ci`). Do not modify the transcript.\n", p.Reference)
		} else {
			fmt.Fprintf(&b, "A previous attempt of this step ended without handing off. Its conversation transcript is at `%s` (JSONL, capsule path). "+
				"Read it ONLY for context about what was already tried; do not modify it, and start this attempt's work from scratch rather than replaying it.\n", p.Reference)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n" + continuationHeading + "\n\n" + b.String()
}

// ewrResumeReminder is the whole instruction of the first retry after a
// turn that ended without `pf step handoff`, when it resumes the same
// harness session: the resumed conversation already holds
// every part of the prompt, so re-sending it would restart the work. The
// reminder carries only the hand-off duty.
func ewrResumeReminder(p promptParts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are resuming step `%s` of polyforge work item `%s` (round %d, dispatch %d).\n\n", p.StepID, p.WIID, p.Round, p.SpawnN)
	b.WriteString("Your previous turn on this step ended without `pf step handoff`. Handing off is the only way the step completes:\n\n")
	b.WriteString("- If the work is already done, hand off now with `pf step handoff` and end your turn.\n")
	b.WriteString("- Otherwise finish only the remaining work, then hand off.\n")
	b.WriteString("- Do not start over and do not redo work you already completed.\n\n")
	b.WriteString("The instructions you already have still apply.\n")
	return b.String()
}

// skillHeading is part 5's heading.
const skillHeading = "# Skill"

// renderSkill renders part 5, the `# Skill` section ("" when the step
// declares no skill): the skill's body verbatim, after how to follow it.
func renderSkill(sk *workflow.Skill) string {
	if sk == nil || strings.TrimSpace(sk.Body) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + skillHeading + "\n\n")
	b.WriteString("Follow this skill as written: run each command line exactly as given (never rewrite or add arguments, never pass a Bash timeout), route on the exit codes it names, and do nothing it forbids.")
	if len(sk.Commands) > 0 {
		b.WriteString(" Its commands: `" + strings.Join(sk.Commands, "`, `") + "`.")
	}
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(sk.Body))
	b.WriteString("\n")
	return b.String()
}

// stepHeaderFiles are part 3 of every prompt: the shared step header and
// the hand-off body template, beside the bundle in the capsule snapshot.
var stepHeaderFiles = []string{"step-header", "handoff"}

// stepHeader answers part 3 of the prompt: the lane snapshot's
// _shared/step-header.md followed by its _shared/handoff.md, verbatim. A
// missing or unreadable file is an error: a step without them cannot know
// how it hands off, so the dispatch fails closed.
func stepHeader(bundleDir string) (string, error) {
	var parts []string
	for _, name := range stepHeaderFiles {
		raw, err := os.ReadFile(workflow.SharedFragment(bundleDir, name))
		if err != nil {
			return "", fmt.Errorf("the step header %s/%s.md: %w", workflow.SharedDir, name, err)
		}
		parts = append(parts, strings.TrimSpace(string(raw)))
	}
	return strings.Join(parts, "\n\n"), nil
}

// ---------------------------------------------------------------------------
// Step definition + capsule file reads.

// readStepDefinition reads the step's definition through the one
// definition read (workflow.ReadDefinition: the lane's own
// steps/<id>.md, else workflows/_shared/steps/<id>.md, rendered with the
// step entry's vars) and answers its body: the step's instruction of
// record.
func readStepDefinition(bundleDir, stepID string, vars map[string]string) (string, error) {
	def, err := workflow.ReadDefinition(bundleDir, stepID, vars)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("driver: step definition for %q unreadable (expected %s under the bundle, or %s beside it): %w",
				stepID, dirStepDefs+"/"+stepID+".md", "../"+workflow.SharedDir+"/"+dirStepDefs+"/"+stepID+".md", err)
		}
		return "", fmt.Errorf("driver: step definition for %q: %w", stepID, err)
	}
	if def.Body == "" {
		return "", fmt.Errorf("driver: step definition for %q is empty", stepID)
	}
	return def.Body, nil
}

// ---------------------------------------------------------------------------
// Session identity + spawn journal.

// deliveredResult answers whether the step's dispatch n handed off (the
// call ledger records its accepted `pf step handoff`): the rewind re-entry
// resumes only a conversation that ended in the round a gate judged.
func (a *StepSpawnAdapter) deliveredResult(stepID string, n int64) bool {
	return ledgerHandoff(a.WI.CapsuleDir, a.WI.ID, stepID, n) != nil
}

// latestJournalEntry answers the step's highest-ordinal spawn-journal
// line declaring a transcript (the zero entry when none): the previous
// dispatch a same-step continuation resumes — its harness, its session
// id and its capsule transcript.
func (a *StepSpawnAdapter) latestJournalEntry(stepID string) (capsule.SpawnJournalEntry, error) {
	entries, err := capsule.ReadSpawnJournal(a.WI.CapsuleDir)
	if err != nil {
		return capsule.SpawnJournalEntry{}, fmt.Errorf("driver: resume ordered but the capsule spawn journal is unreadable: %w", err)
	}
	best := capsule.SpawnJournalEntry{SpawnN: -1}
	for _, e := range entries {
		if e.StepID == stepID && e.Transcript != "" && e.SpawnN > best.SpawnN {
			best = e
		}
	}
	if best.SpawnN < 0 {
		return capsule.SpawnJournalEntry{}, nil
	}
	return best, nil
}

// appendSpawnJournal appends one spawn identity line through
// SpawnParams.Journal — the same document shape the runtime lane
// writes (at, work_item, step_id, spawn_token, pid, started_at,
// session_handle, spawn_n, resume, transcript), plus the dispatch's
// harness, harness_session and language (the agent
// language its prompt named), so the B2 counter
// stays continuous across both spawners and every reader (loose by
// contract) keeps parsing.
func (a *StepSpawnAdapter) appendSpawnJournal(p spawn.Params, stepID, token, language string, pid int, startedAt string) error {
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	entry := capsule.SpawnJournalEntry{
		At:             now.UTC().Format(time.RFC3339Nano),
		WorkItem:       p.WorkItemID,
		StepID:         stepID,
		SpawnToken:     token,
		PID:            pid,
		StartedAt:      startedAt,
		SessionHandle:  p.SessionHandle,
		SpawnN:         int(p.SpawnN),
		Resume:         p.Resume,
		Transcript:     p.TranscriptPath,
		Harness:        harness.Of(p.Harness),
		HarnessSession: p.HarnessSession,
		Language:       language,
	}
	blob, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(a.WI.CapsuleDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Torn-tail repair (astra r2 #2): a crash mid-append can leave the
	// journal without its trailing newline, and appending then would
	// GLUE the next record onto the half-written line — both becoming
	// unparsable, quietly erasing the ordinal from the loose readers'
	// view (the ordinal seed would under-seed, the next relaunch dispatching
	// an ordinal the crashed dispatch already used). A missing trailing
	// newline isolates the torn tail as its own (skipped) line and the
	// new record lands intact.
	if f, oerr := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644); oerr == nil {
		if fi, serr := f.Stat(); serr == nil && fi.Size() > 0 {
			buf := make([]byte, 1)
			if _, rerr := f.ReadAt(buf, fi.Size()-1); rerr == nil && buf[0] != '\n' {
				// WriteAt lands the isolating newline AT THE END — a plain
				// Write would follow the file offset (0) and clobber the
				// first record instead.
				_, _ = f.WriteAt([]byte("\n"), fi.Size())
			}
		}
		f.Close()
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(blob, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// spawnToken is the public display token for one derivation (the
// runtime's shape: the session fingerprint, "#N"-suffixed from the
// second derivation on).
func spawnToken(handle string, n int64) string {
	if handle == "" {
		return "spawn-" + strconv.FormatInt(n, 10)
	}
	if n > 1 {
		return fmt.Sprintf("%s#%d", handle, n-1)
	}
	return handle
}

// ---------------------------------------------------------------------------
// stepRun: the StepRun face over one derived step process.

// stepRun awaits one step process's hand-off. Wait polls the capsule's
// call ledger until the dispatch's accepted `pf step handoff` is there
// (then tears the process down), the process exits without one (an
// infrastructure-class error — the recovery lane owns what follows), or
// ctx ends. The ledger is the daemon's record: a hand-off it refused is
// not on it, so nothing half-written is ever collected.
type stepRun struct {
	capsuleRoot string
	wiID        string
	sp          StepSpawn
	handle      spawn.Handle
	poll        time.Duration
	meta        RunMeta
	sink        *supervise.Tracker
	// watch is the dispatch's delivery watch over its stdout stream
	// (relay.go), fed by launch's record tap.
	watch *deliveryWatch
}

// Meta implements MetaRun.
func (r *stepRun) Meta() RunMeta { return r.meta }

// SendMessage implements relayRun: one owner message into the resident
// step process through the harness's own channel (spawn.Messenger).
func (r *stepRun) SendMessage(ctx context.Context, m spawn.Message) (spawn.Delivery, error) {
	ms, ok := r.handle.(spawn.Messenger)
	if !ok {
		return spawn.Delivery{}, fmt.Errorf("driver: step %s spawn %d: the harness process takes no later message", r.sp.StepID, r.sp.SpawnN)
	}
	return ms.SendMessage(ctx, m)
}

// deliveryWatch implements relayRun.
func (r *stepRun) deliveryWatch() *deliveryWatch { return r.watch }

// carriedMessages implements relayRun: the owner messages the dispatch's
// first message carried (StepSpawn.OwnerMessages).
func (r *stepRun) carriedMessages() []capsule.AskLine { return r.sp.OwnerMessages }

// exited builds the ExitedError of a process that is gone without a
// hand-off: exit status, stderr tail and the harness's last signal
// (whether it ended on an error turn).
func (r *stepRun) exited() error {
	ee := &ExitedError{Step: r.sp.StepID, SpawnN: r.sp.SpawnN, ExitCode: -1}
	var parts []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		for _, p := range parts {
			if p == t {
				return
			}
		}
		parts = append(parts, t)
	}
	if er, ok := r.handle.(spawn.ExitReporter); ok {
		info := er.ExitInfo()
		ee.ExitCode, ee.Signal = info.Code, info.Signal
		ee.StderrIncomplete = info.StderrIncomplete
		ee.StdoutIncomplete = info.StdoutIncomplete
		add(tailLine(info.StderrTail))
	}
	if r.sink != nil {
		snap := r.sink.Snapshot(time.Now())
		ee.TurnError = snap.TurnError
		ee.TurnOK = snap.TurnEnded && !snap.TurnError
		if snap.Exit != nil {
			if ee.ExitCode < 0 {
				ee.ExitCode, ee.Signal = snap.Exit.Code, snap.Exit.Signal
			}
			add(snap.Exit.LastError)
		}
		add(snap.LastError)
	}
	ee.LastError = strings.Join(parts, " | ")
	return ee
}

func (r *stepRun) Wait(ctx context.Context) (*StepResult, error) {
	for {
		if res := r.collect(); res != nil {
			return r.deliver(res)
		}
		if r.handle.Done() {
			// one last look: the hand-off may have landed between the
			// collect above and the exit
			if res := r.collect(); res != nil {
				return r.deliver(res)
			}
			return nil, r.exited()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.poll):
		}
	}
}

func (r *stepRun) Terminate(bound time.Duration) error {
	return r.handle.Terminate(bound)
}

// deliver is the one delivery path of a collected hand-off: tear the
// process down (the step was told to stop once its hand-off is accepted).
// Wait's normal return and the verdict path's final look
// both end here.
func (r *stepRun) deliver(res *StepResult) (*StepResult, error) {
	if terr := r.handle.Terminate(teardownBound); terr != nil {
		fmt.Fprintf(os.Stderr, "driver: step %s spawn %d teardown after its hand-off failed (the hand-off is recorded): %v\n",
			r.sp.StepID, r.sp.SpawnN, terr)
	}
	return res, nil
}

// finalLook is the verdict path's one last look at the ledger after a
// supervisor verdict cancelled Wait: a hand-off already
// recorded at verdict time is a delivery, not a death, so it takes the
// same path as Wait's normal return. (nil, nil) when there is none.
func (r *stepRun) finalLook() (*StepResult, error) {
	res := r.collect()
	if res == nil {
		return nil, nil
	}
	return r.deliver(res)
}

// collect reads the ledger once for the dispatch's accepted hand-off
// (nil when there is none yet).
func (r *stepRun) collect() *StepResult {
	return ledgerHandoff(r.capsuleRoot, r.wiID, r.sp.StepID, r.sp.SpawnN)
}

// maxQuotedText bounds agent-written text quoted into a defect (a commit
// name, a path, a command a unit reported): bytes.
const maxQuotedText = 256

// clipAgentText cuts agent-written text quoted into a defect to
// maxQuotedText bytes (on a rune boundary), marking the cut.
func clipAgentText(s string) string {
	if len(s) <= maxQuotedText {
		return s
	}
	cut := maxQuotedText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}
