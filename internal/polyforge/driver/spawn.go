package driver

// spawn.go: the production StepSpawner adapter. It bridges the driver's
// StepSpawn order onto the runtime derivation mechanism and owns:
//
//   - PROMPT ASSEMBLY: the step definition (workflows/<bundle>/steps/<id>.md)
//     plus the consumed gate-FAIL rewind verdict, the step's archived Q&A,
//     the producing step's handoff (inherit steps only) and the
//     result-delivery contract;
//   - PROCESS DERIVATION through spawn.Params -> spawn.Spawner: the model
//     pin rides argv, the resume session the spawn journal's latest
//     transcript; isolation and env sanitizing stay in runtime/spawn.go;
//   - RESULT COLLECTION: the step submits its envelope with `pf result
//     submit` and the daemon lands it at
//     runtime-state/pending-results/<spawn_n>.json; the run collects it,
//     stamps it to artifacts/steps/<step>/s<spawn_n>/result.json, consumes
//     the drop and tears the process down. The drop path is keyed by the
//     spawn ordinal and every re-dispatch takes a fresh one (B2), so a
//     stale drop is never mistaken for a live derivation's result.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

const (
	// dirStepDefs is the step-definition directory inside a
	// materialized bundle: workflows/<bundle>/steps/<id>.md (the R9
	// path convention).
	dirStepDefs = "steps"
	// dirPendingResults is the runtime-state subdirectory a step's R13
	// envelope is delivered into (by the daemon's /step/result, one file per spawn
	// ordinal — the ordinal IS the fence, so a superseded derivation's
	// leftover file is never collected).
	dirPendingResults = "pending-results"
	// defaultEnvelopePoll is the default envelope-collection cadence.
	defaultEnvelopePoll = 200 * time.Millisecond
	// teardownBound bounds the post-envelope process teardown.
	teardownBound = 5 * time.Second
)

// StepSpawnAdapter is the production StepSpawner. Build one per work
// item (NewStepSpawnAdapter) and hand it to the Driver's Config; the
// driver serializes, so the adapter itself keeps no locks.
type StepSpawnAdapter struct {
	WI WIInfo
	// BundleDir is the materialized workflow bundle root (workflow.md
	// + steps/). Default: <CapsuleDir>/workflows/<BundleName>.
	BundleDir string
	// Workflow is the parsed workflow.md (workflow.go), shared with the
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
// The context policy comes from the shared workflow parse.
func (a *StepSpawnAdapter) SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error) {
	spec := a.Workflow.step(sp.StepID)
	if spec == nil {
		return nil, fmt.Errorf("driver: step %q is not in the workflow bundle", sp.StepID)
	}
	policy := spec.ContextPolicy
	switch policy {
	case ContextInherit, ContextFresh: // parseWorkflow defaults inherit
	default:
		return nil, fmt.Errorf("driver: step %q has unknown context_policy %q", sp.StepID, policy)
	}
	def, err := readStepDefinition(a.BundleDir, sp.StepID, spec.Vars)
	if err != nil {
		return nil, err
	}
	parts := promptParts{
		WIID:                    a.WI.ID,
		StepID:                  sp.StepID,
		SpawnN:                  sp.SpawnN,
		CapsuleRoot:             a.WI.CapsuleDir,
		ContextMode:             policy,
		Definition:              def,
		Rewind:                  sp.RewindInput,
		Answers:                 sp.Answers,
		HandoffDue:              spec.Handoff,
		Gate:                    spec.Gate,
		PriorRejection:          sp.PriorRejection,
		PriorEndedWithoutResult: sp.PriorEndedWithoutResult,
		RequireCommandEvidence:  spec.RequiresCommandEvidence,
		RequiresHumanAnswer:     spec.RequiresHumanAnswer,
		HumanAnswerIfAsked:      spec.HumanAnswerIfAsked,
		Memories:                recalledMemoriesSection(a.WI.CapsuleDir),
		MemoryDuty:              memoryDutySection(a.BundleDir, a.WI.CapsuleDir, a.Workflow.lastStep(sp.StepID)),
	}
	// polyforge#260: the armed verification prompt carries the shared
	// classification duty as its own section (an unarmed lane carries
	// none - its FAIL rewinds exactly as today, so the agent is never
	// told to classify).
	if spec.MechanicalFix && sp.StepID == verificationStepID {
		parts.MechanicalClassify = mechanicalFixSection(a.BundleDir, "Classification duty (an armed full verification FAIL)")
	}
	// H3 (polyforge#164): the declared output schema rides the prompt —
	// the same schema the driver validates the completed output against.
	if schema, err := loadStepOutputSchema(a.BundleDir, sp.StepID, spec.Vars); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s/%s: %v\n", a.WI.ID, sp.StepID, err)
	} else if schema != nil {
		parts.OutputSchema = schema.Text
	}
	if policy == ContextInherit {
		h, err := a.resolveHandoff(sp.StepID, sp.Done)
		if err != nil {
			return nil, err
		}
		parts.Handoff = h
	}

	handle := a.WI.SessionHandle
	h := harness.Of(sp.Harness)
	var transcript, hsession string
	resume := sp.Resume
	if resume {
		// Same-step continuation (an answered question, the first retry
		// after a crash) resumes the previous dispatch's conversation only
		// on the SAME harness: another harness's session cannot be resumed.
		prev, err := a.latestJournalEntry(sp.StepID)
		if err != nil {
			return nil, err
		}
		switch {
		case harness.Of(prev.Harness) != h:
			resume = false
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
			}
		case !usableTranscript(prev.Transcript):
			// (also the no-journal-line-for-this-step shape: "" is unusable)
			// nothing readable to resume (the previous dispatch died before
			// the harness wrote its session): start the conversation fresh
			// rather than hand the harness a path that cannot resume
			resume = false
		default:
			transcript = prev.Transcript
		}
		if !resume {
			// the continuation cannot resume (another harness, or the
			// native session is gone): the fresh conversation gets the
			// previous capsule transcript as read-only reference (§2.4)
			parts.Reference = a.referenceTranscript(prev)
		}
	}
	if sp.Fresh && parts.Reference == "" {
		// a retry starts a NEW conversation; the previous transcript, when
		// readable, rides the prompt as a read-only reference (it may be
		// another harness's JSONL: reference material only)
		if prev, perr := a.latestJournalEntry(sp.StepID); perr == nil {
			parts.Reference = a.referenceTranscript(prev)
		}
	}
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
		SessionHandle:  handle,
		SpawnN:         int(sp.SpawnN),
		TranscriptPath: transcript,
		Resume:         resume,
		Harness:        h,
		HarnessSession: hsession,
		InitialPrompt:  assembleStepPrompt(parts),
		Model:          sp.Model,
		Effort:         sp.Effort,
	}
	if translator != nil {
		p.OnRecord = func(line string) { translator.Feed([]byte(line)) }
		p.OnExit = func(info spawn.ExitInfo) {
			translator.Exit(supervise.ExitInfo{Code: info.Code, Signal: info.Signal, LastError: tailLine(info.StderrTail)})
		}
	}
	token := spawnToken(handle, sp.SpawnN)
	p.Journal = func(pid int, startedAt string) error {
		return a.appendSpawnJournal(p, sp.StepID, token, pid, startedAt)
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
		dropPath:    PendingResultPath(a.WI.CapsuleDir, sp.SpawnN),
		poll:        a.PollInterval,
		meta:        meta,
		sink:        sink,
	}, nil
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

// promptParts is the assembled prompt's material.
type promptParts struct {
	WIID   string
	StepID string
	SpawnN int64
	// CapsuleRoot is the capsule's absolute root: every capsule-relative
	// path the prompt names resolves against it (polyforge#244).
	CapsuleRoot string
	ContextMode string // ContextInherit | ContextFresh
	Definition  string
	Rewind      *capsule.RewindInput
	Answers     []capsule.ArchivedAnswer
	Handoff     *handoffSection
	// HandoffDue marks a step that must produce a handoff artifact at
	// the conventional path (the delivery contract states the duty only
	// then).
	HandoffDue bool
	// Gate marks a workflow-declared gate step (gate: true): the
	// delivery contract states the verdict binding for it — a fail
	// verdict in the output REQUIRES outcome gate_failed (the driver's
	// only rewind trigger), and a fail verdict delivered under
	// completed is re-bound to gate_failed fail-closed.
	Gate bool
	// PriorRejection is the verbatim validation error from the previous
	// dispatch's rejected envelope (the in-process retry lane): the
	// author sees exactly what to fix instead of blindly re-authoring.
	PriorRejection string
	// PriorEndedWithoutResult: the previous dispatch's turn ended without
	// the result envelope being written (retry feedback).
	PriorEndedWithoutResult bool
	// RequireCommandEvidence marks a step whose completed result must
	// carry command_evidence (verification / requires_command_evidence):
	// the contract states the entry shape and the rule.
	RequireCommandEvidence bool
	// RequiresHumanAnswer marks a requires_human_answer step: the
	// contract states that a human answer must precede completion.
	RequiresHumanAnswer bool
	// HumanAnswerIfAsked marks a requires_human_answer: if_asked step:
	// the contract states that its questions are a human's to answer and
	// that a zero-question completion is recorded and notified.
	HumanAnswerIfAsked bool
	// OutputSchema is the step definition's declared output schema
	// (YAML), shown ahead of the definition body; "" = none declared.
	OutputSchema string
	// MechanicalClassify is the shared fast-lane material's
	// classification duty (workflows/_shared/mechanical-fix.md), rendered
	// into an ARMED verification dispatch's prompt as its own section
	// (polyforge#260); "" = the lane is unarmed (or this is not the
	// verification step) and no classification duty rides.
	MechanicalClassify string
	// Reference is the previous attempt's transcript path (a fresh retry
	// carries it as read-only reference material; "" = none).
	Reference string
	// Memories is the rendered claim-time recalled-memories section
	// ("" = no snapshot / no hits: no section).
	Memories string
	// MemoryDuty is the rendered recall/remember duty of the step (the
	// shared memory.md, plus memory-wrap.md for the workflow's last
	// step); "" = no section.
	MemoryDuty string
}

// handoffSection is one producing step's handoff artifact, injected
// into an inherit step's prompt.
type handoffSection struct {
	FromStep string
	Path     string
	Content  []byte
}

// assembleStepPrompt builds the step process's one instruction of
// record: identity, the context-policy contract, the step definition,
// the rewind verdict being consumed, the Q&A history a resumed step
// continues from, the handoff (inherit only), the declared inputs and
// the result-delivery contract.
func assembleStepPrompt(p promptParts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are executing step %q of polyforge work item %q (derivation %d).\n\n", p.StepID, p.WIID, p.SpawnN)
	switch p.ContextMode {
	case ContextInherit:
		b.WriteString("Context policy: INHERIT — this step continues the previous step's work; the handoff below (when present) is the upstream continuation.\n\n")
	case ContextFresh:
		b.WriteString("Context policy: FRESH — this step starts from a clean context; nothing from previous steps is carried in: read whatever upstream artifacts your instructions name, by their path.\n\n")
	}
	// The capsule-root anchor (polyforge#244): the step is born at the
	// capsule root, but a `cd repos/<name>` can persist across its shell
	// calls (Claude Code keeps one shell cwd), so a bare `artifacts/<x>`
	// write would land in the repo worktree. Binding every
	// capsule-relative path to the absolute root removes that ambiguity.
	fmt.Fprintf(&b, "Capsule root: `%[1]s`. Every capsule-relative path in this prompt (`artifacts/…`, `runtime-state/…`, `workflows/…`, `repos/…`) "+
		"resolves against this root, NEVER against your current working directory — a `cd` can persist across your shell calls. "+
		"Read and write them by absolute path: `artifacts/<name>` means `%[1]s/artifacts/<name>`.\n\n", p.CapsuleRoot)
	if p.OutputSchema != "" {
		b.WriteString("# Output schema\n\n")
		b.WriteString("The `output` object of a `completed` result envelope must validate against this JSON Schema (draft-07, written as YAML; omit an optional property rather than writing null). ")
		if outputSchemaEnforced(p.StepID) {
			b.WriteString("The driver validates it and rejects a non-conforming envelope:\n\n```yaml\n")
		} else {
			b.WriteString("The driver validates it and records every violation for the owner:\n\n```yaml\n")
		}
		b.WriteString(p.OutputSchema)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("# Step definition\n\n")
	b.WriteString(p.Definition)
	b.WriteString("\n")
	if p.Rewind != nil && strings.TrimSpace(p.Rewind.Reason) != "" {
		b.WriteString("\n# Gate FAIL verdict being consumed (rewind input)\n\n")
		b.WriteString("A downstream gate failed and the workflow rewound to this step. The verdict in full:\n\n")
		b.WriteString(p.Rewind.Reason)
		b.WriteString("\n")
	}
	// The light re-entry instruction (polyforge#222): a rewind input whose
	// Note is set rides beside the verdict as its own section - the resumed
	// session sees the re-entry boundary duty and the needs_code_change
	// self-report channel without re-deriving them from the verdict (the
	// note itself states which kind of light rewind this is).
	if p.Rewind != nil && strings.TrimSpace(p.Rewind.Note) != "" {
		b.WriteString("\n# Light rewind re-entry\n\n")
		b.WriteString(p.Rewind.Note)
		b.WriteString("\n")
	}
	// The mechanical classification duty (polyforge#260): an armed
	// verification dispatch's own section, the rewind-note pattern - the
	// agent sees the static-only boundary, the all-or-nothing rule and the
	// bound contract without re-deriving them from the workflow.
	if p.MechanicalClassify != "" {
		b.WriteString("\n# Mechanical classification (this lane is armed)\n\n")
		b.WriteString(p.MechanicalClassify)
		b.WriteString("\n")
	}
	if p.PriorRejection != "" {
		b.WriteString("\n# Previous result envelope REJECTED — fix and rewrite\n\n")
		b.WriteString("Your previous envelope for this step was rejected by the driver's validation:\n\n")
		b.WriteString(p.PriorRejection)
		b.WriteString("\n\nFix the defect and write a corrected envelope. Compute artifact digests programmatically (e.g. sha256sum + jq into the JSON) — never hand-transcribe them; re-check every required field the error names.\n")
	}
	if p.PriorEndedWithoutResult {
		b.WriteString("\n# Previous turn ended WITHOUT a result envelope\n\n")
		b.WriteString("Your previous turn on this step ended without you writing the result envelope. Finishing your reply is not delivering: the step only completes when the result envelope file is written (see the delivery contract below). Do the remaining work, then WRITE the envelope before you stop.\n")
	}
	if p.Reference != "" {
		b.WriteString("\n# Previous attempt (read-only reference)\n\n")
		fmt.Fprintf(&b, "A previous attempt of this step ended without delivering a result. Its conversation transcript is at `%s` (JSONL, capsule path). "+
			"Read it ONLY for context about what was already tried; do not modify it, and start this attempt's work from scratch rather than replaying it.\n", p.Reference)
	}
	if len(p.Answers) > 0 {
		b.WriteString("\n# Answered questions (this step's history — continue from it)\n\n")
		for _, a := range p.Answers {
			fmt.Fprintf(&b, "- %s (answered by %s): %s\n", a.QuestionID, a.AnsweredBy, a.Answer)
		}
	}
	b.WriteString(p.Memories)
	if p.Handoff != nil {
		fmt.Fprintf(&b, "\n# Handoff from step %q (%s)\n\n", p.Handoff.FromStep, p.Handoff.Path)
		b.Write(p.Handoff.Content)
		b.WriteString("\n")
	}
	b.WriteString(p.MemoryDuty)
	b.WriteString(deliveryContract(p))
	return b.String()
}

// Bounds of the recalled-memories section: at most memoryListMax lines,
// each title cut to memoryTitleRunes (ids and excerpts stay in the
// snapshot file).
const (
	memoryListMax    = 10
	memoryTitleRunes = 120
	memoryTypeRunes  = 80
)

// memoryNoise matches the titles of id-only memories a step cannot use.
var memoryNoise = regexp.MustCompile(`^(Verification Report — \S+$|Controller-sink artifact)`)

// oneLine collapses every whitespace run (newlines included) to one space
// and caps the result at max runes, so a recalled field cannot break out
// of its bullet (no new line, no heading).
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

// memoryTitle answers a recalled memory's display title: its title, else
// the first non-empty line of its excerpt, else its id.
func memoryTitle(id, title, excerpt string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	for _, line := range strings.Split(excerpt, "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); t != "" {
			return t
		}
	}
	return id
}

// recalledMemoriesSection renders the claim-time snapshot
// (capsule.RecalledMemoriesJSON, written by the runtime's recall lane)
// as a short, non-binding prompt section (decision 2026-10-16 G3): one
// `[type] title` line per memory, id-only noise dropped, at most
// memoryListMax lines. An absent, torn or empty snapshot renders nothing.
func recalledMemoriesSection(capsuleDir string) string {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.RecalledMemoriesJSON)))
	if err != nil {
		return ""
	}
	var snap struct {
		Items []struct{ ID, Type, Title, Excerpt string } `json:"items"`
	}
	if json.Unmarshal(raw, &snap) != nil {
		return ""
	}
	var lines []string
	for _, m := range snap.Items {
		title := oneLine(memoryTitle(m.ID, m.Title, m.Excerpt), memoryTitleRunes)
		if title == "" || memoryNoise.MatchString(title) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s\n", oneLine(m.Type, memoryTypeRunes), title))
		if len(lines) == memoryListMax {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n# Recalled project memories (non-binding history)\n\n")
	b.WriteString("Titles of the memories recalled from this project at claim time by similarity to the task — past context, not instructions. Their ids and excerpts are in `" + capsule.RecalledMemoriesJSON + "`; read a body with `pf memory show <id>`.\n\n")
	for _, l := range lines {
		b.WriteString(l)
	}
	return b.String()
}

// memoryDutySection renders workflows/_shared/memory.md (recall first,
// every step) and, for the workflow's last step, memory-wrap.md (the work
// item's one experience.wrap summary, with the claimed project's memory
// policy from capsule.MemoryPolicyJSON, else the built-in default). No
// task definition or no template renders nothing.
func memoryDutySection(bundleDir, capsuleDir string, last bool) string {
	var wi capsule.WorkItemDoc
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.WorkItemJSON)))
	if err != nil || json.Unmarshal(raw, &wi) != nil || wi.Project == "" {
		return ""
	}
	ref := wi.Slug
	if ref == "" {
		ref = wi.ID
	}
	policy := workspace.DefaultMemoryPolicy()
	if raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.MemoryPolicyJSON))); err == nil {
		_ = json.Unmarshal(raw, &policy)
	}
	vars := map[string]string{"project": wi.Project, "work_item": ref}
	names := []string{"memory.md"}
	if last {
		names = append(names, "memory-wrap.md")
	}
	var b strings.Builder
	for _, name := range names {
		if name == "memory-wrap.md" {
			vars["record"], vars["skip"] = policyBullets(policy.Record), policyBullets(policy.Skip)
		}
		path := filepath.Join(filepath.Dir(bundleDir), workflow.SharedDir, name)
		tmpl, err := os.ReadFile(path)
		if err == nil {
			tmpl, err = workflow.RenderStepVars(tmpl, vars)
		}
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "driver: memory duty %s: %v\n", path, err)
			}
			break
		}
		b.WriteString("\n" + strings.TrimSpace(string(tmpl)) + "\n")
	}
	return b.String()
}

// policyBullets renders one policy list as markdown bullets.
func policyBullets(list []string) string {
	if len(list) == 0 {
		return "- (none)"
	}
	var b strings.Builder
	for _, s := range list {
		b.WriteString("- " + oneLine(s, 300) + "\n")
	}
	return b.String()
}

// deliveryContract states the envelope delivery channel and shape: the
// step submits its envelope with `pf result submit` — the ONE delivery
// path (polyforge#214) — and the daemon stamps the dispatch identity and
// lands it at the drop path the driver collects from, so the prompt
// names no path and no identity field. The MINIMAL CONTRACT
// (polyforge#147): the envelope is the outcome + output pair (needs_input
// adds the question pair) — no protocol version stamp, no envelope id,
// no artifact manifest, no handoff digest reference; downstream and
// handoff readers work by path convention. A helper that cannot deliver
// ends the turn without a result (the exited-without-result recovery
// lane), never a hand-written file. For a gate step it also states the
// verdict binding (the #106 lesson): the output's top-level fail verdict
// and the envelope outcome must agree — a fail verdict requires
// gate_failed, and the driver re-binds a completed envelope carrying one
// fail-closed.
func deliveryContract(p promptParts) string {
	evidenceLine := ""
	if p.RequireCommandEvidence {
		evidenceLine = ",\n  \"command_evidence\": []"
	}
	var b strings.Builder
	b.WriteString("\n# Result delivery\n\n" +
		"When this step reaches an outcome, deliver exactly ONE JSON object — the polyforge result envelope — " +
		"with `pf result submit`, then end your turn. This command is the ONLY way to deliver a result: " +
		"the daemon recognizes this step from your process, stamps the work item, step and spawn itself " +
		"and lands the envelope where the execution driver collects it; nothing else you print is read as a result. " +
		"Pass the envelope inline, on stdin (`pf result submit - <<'EOF'` … `EOF`) or from a file (`pf result submit @<path>`):\n\n")
	fmt.Fprintf(&b, "```json\n{\n"+
		"  \"outcome\": \"completed\" | \"gate_failed\" | \"needs_input\" | \"incomplete\",\n"+
		"  \"output\": { }%s\n"+
		"}\n```\n\n", evidenceLine)
	b.WriteString("If `pf result submit` refuses the envelope it exits non-zero and names the defect: correct the envelope and submit it again. " +
		"If it cannot deliver at all (the daemon unreachable or refusing this process), report that and end your turn — " +
		"never write a result file yourself.\n\n")
	b.WriteString("Rules: `output` is a JSON object; every file this step produced that downstream steps or the owner consume is written under `artifacts/` (that is, `" + p.CapsuleRoot + "/artifacts/`) and named by its path in the step's own instructions — the driver and downstream readers work by path convention, never by manifest")
	if p.HandoffDue {
		b.WriteString("; this step DECLARES handoff, so write the handoff file at `" + p.CapsuleRoot + "/artifacts/handoff-" + p.StepID + ".md` — the next inherit step reads it by that path convention")
	}
	b.WriteString("; a `needs_input` result carries `question_id` (1–64 chars of `A-Za-z0-9._-`) and `question` instead. End your turn once the submit succeeds.\n")
	if p.RequireCommandEvidence {
		b.WriteString("\nCommand evidence: this step requires `command_evidence` — a list of OBJECTS " +
			"`{\"argv\": [\"go\", \"test\", \"./...\"], \"cwd\": \"repos/<name>\", \"exit_code\": 0, \"duration_ms\": 1200}`, never strings. " +
			"`cwd` is the directory the command ran in, capsule-relative: record each command where it ran — with that `cwd`, " +
			"or with the `cd` you ran it after (`[\"bash\", \"-c\", \"cd repos/<name> && go test ./...\"]`); " +
			"a command recorded with neither counts as run at the capsule root, not in a repo. " +
			"Run at least one real check command and report every command you ran, in the order you ran it, with its measured `duration_ms` (> 0); " +
			"an empty list, an entry without `argv`, or one without a positive `duration_ms` is rejected. Never fabricate an entry.\n")
	}
	if p.RequiresHumanAnswer {
		b.WriteString("\nHuman answer: this step requires a human answer before completion: return `needs_input` first; " +
			"a `completed` or `gate_failed` envelope without an answer in this round is rejected.\n")
	}
	if p.HumanAnswerIfAsked {
		b.WriteString("\nHuman questions: every question this step raises is answered by a human only — " +
			"ask through `needs_input` and never answer it yourself (the daemon refuses `pf answer` from a step process). " +
			"Completing with zero questions is allowed, but it is recorded and the owner is notified.\n")
	}
	if p.Gate {
		b.WriteString("\nGate verdict binding: this step is a GATE (the workflow declares it `gate: true`), " +
			"and the top-level `verdict` field of your `output` is the business verdict. " +
			"A fail verdict REQUIRES `outcome: \"gate_failed\"` — that outcome is the driver's only rewind trigger, " +
			"and the fail verdict rides `output` verbatim as the rewind input. " +
			"A fail verdict delivered under `outcome: \"completed\"` is a mislabeled gate report: " +
			"the driver re-binds it to `gate_failed` fail-closed (the gate is never archived done on it). " +
			"Deliver `outcome: \"completed\"` only for a pass verdict (or when the step owes no verdict).\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Step definition + capsule file reads.

// readStepDefinition loads workflows/<bundle>/steps/<id>.md (the R9
// path convention; falling back to workflows/_shared/steps/<id>.md when
// the bundle carries no own file), renders its {{name}} placeholders
// from the step entry's vars and strips any leading frontmatter block:
// the body is the step's instruction of record.
func readStepDefinition(bundleDir, stepID string, vars map[string]string) (string, error) {
	raw, err := workflow.ReadStepDefinition(bundleDir, stepID, vars)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("driver: step definition for %q unreadable (expected %s under the bundle, or %s beside it): %w",
				stepID, dirStepDefs+"/"+stepID+".md", "../"+workflow.SharedDir+"/"+dirStepDefs+"/"+stepID+".md", err)
		}
		return "", fmt.Errorf("driver: step definition for %q: %w", stepID, err)
	}
	body := stepDefBody(raw)
	if body == "" {
		return "", fmt.Errorf("driver: step definition for %q is empty", stepID)
	}
	return body, nil
}

// stepDefBody strips a leading YAML frontmatter block (the same fence
// shape workflow.md uses) via the shared splitter. A file without a
// closed frontmatter block is body in full.
func stepDefBody(raw []byte) string {
	if _, body, ok := workflow.SplitFrontmatter(raw); ok {
		raw = body
	}
	return strings.TrimSpace(string(raw))
}

// mechanicalFixFile is the shared fast-lane material's conventional path
// beside the bundle (workflows/_shared/mechanical-fix.md).
func mechanicalFixFile(bundleDir string) string {
	return filepath.Join(filepath.Dir(bundleDir), workflow.SharedDir, "mechanical-fix.md")
}

// mechanicalFixSection answers one H2 section of the shared fast-lane
// material's body (the text under "## <heading>" up to the next H2),
// trimmed; "" when the file, its frontmatter or the section is absent -
// an armed lane without the material classifies nothing (fail-closed,
// never a dispatch error).
func mechanicalFixSection(bundleDir, heading string) string {
	raw, err := os.ReadFile(mechanicalFixFile(bundleDir))
	if err != nil {
		return ""
	}
	_, body, ok := workflow.SplitFrontmatter(raw)
	if !ok {
		return ""
	}
	return h2Section(body, heading)
}

// h2Section answers the text under one "## <heading>" line up to the
// next H2 heading ("" when the heading is absent).
func h2Section(body []byte, heading string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") {
			if in {
				break // the next H2 ends the section
			}
			in = t == "## "+heading
			continue
		}
		if in {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

// checkRelPath asserts one capsule-relative path: non-empty, not
// absolute, no parent escape (the capsule jail of every file the
// prompt assembler reads).
func checkRelPath(rel string) error {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return fmt.Errorf("empty artifact path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("path %q must be capsule-relative", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return fmt.Errorf("parent escape in path %q", rel)
		}
	}
	return nil
}

// readCapsuleFile reads one capsule-relative artifact, jailed to the
// capsule root by the same path rules the artifact verifier applies.
func readCapsuleFile(root, rel string) ([]byte, error) {
	if err := checkRelPath(rel); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
}

// ---------------------------------------------------------------------------
// Handoff resolution.

// resolveHandoff finds the handoff an inherit step continues from: the
// NEAREST earlier step declaring handoff whose execution record is
// done — its handoff artifact is read from the CONVENTION PATH
// artifacts/handoff-<step>.md (polyforge#147: path convention, no
// manifest, no digest verification). No earlier handoff-declaring step
// at all answers (nil, nil) (a first step may legitimately inherit
// nothing), and so does a done producer that left no file at the
// convention path: the handoff section is simply omitted, never a
// dispatch failure (01-workflow-skills.md §4.4a).
func (a *StepSpawnAdapter) resolveHandoff(stepID string, done map[string]bool) (*handoffSection, error) {
	idx, _ := a.Workflow.indexOf(stepID) // -1 when absent
	if idx <= 0 {
		return nil, nil // no earlier step declares anything
	}
	for i := idx - 1; i >= 0; i-- {
		prod := &a.Workflow.Steps[i]
		if !prod.Handoff {
			continue
		}
		if !done[prod.ID] {
			return nil, fmt.Errorf("driver: step %q declares handoff but is not done (execution.json), needed by inherit step %q",
				prod.ID, stepID)
		}
		rel := capsule.DirArtifacts + "/handoff-" + prod.ID + ".md"
		content, err := readCapsuleFile(a.WI.CapsuleDir, rel)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("driver: handoff artifact of step %q unreadable at %s (path convention): %w", prod.ID, rel, err)
		}
		return &handoffSection{FromStep: prod.ID, Path: rel, Content: content}, nil
	}
	return nil, nil
}

// spawnTokenDir is the archive token directory for spawn ordinal n.
func spawnTokenDir(n int64) string { return "s" + strconv.FormatInt(n, 10) }

// ---------------------------------------------------------------------------
// Session identity + spawn journal.

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
// session_handle, spawn_n, resume, transcript), so the B2 counter
// stays continuous across both spawners and every reader (loose by
// contract) keeps parsing.
func (a *StepSpawnAdapter) appendSpawnJournal(p spawn.Params, stepID, token string, pid int, startedAt string) error {
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

// PendingResultPath is the absolute drop path of one derivation's
// envelope.
func PendingResultPath(capsuleDir string, n int64) string {
	return filepath.Join(capsuleDir, capsule.DirRuntimeState, dirPendingResults, strconv.FormatInt(n, 10)+".json")
}

// ---------------------------------------------------------------------------
// stepRun: the StepRun face over one derived step process.

// stepRun awaits one step process's result envelope. Wait polls the
// drop path until a well-formed, fence-correct envelope lands (then
// archives it and tears the process down), the process exits without
// one (an infrastructure-class error — the recovery lane owns what
// follows), or ctx ends. A present-but-torn drop file is treated as a
// write still in flight and re-read on later polls; once the process
// is gone the defect is surfaced, not swallowed.
type stepRun struct {
	capsuleRoot string
	wiID        string
	sp          StepSpawn
	handle      spawn.Handle
	dropPath    string
	poll        time.Duration
	meta        RunMeta
	sink        *supervise.Tracker
}

// Meta implements MetaRun.
func (r *stepRun) Meta() RunMeta { return r.meta }

// exited builds the ExitedError of a process that is gone without a
// usable envelope: exit status, stderr tail and the harness's last
// signal (whether it ended on an error turn).
func (r *stepRun) exited(defect error) error {
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
	if defect != nil {
		ee.Detail = "unusable result envelope: " + defect.Error()
	}
	return ee
}

func (r *stepRun) Wait(ctx context.Context) (*StepResult, error) {
	var lastDefect error
	for {
		res, defect := r.collect()
		if res == nil {
			// the CURRENT drop's defect (nil once it was removed or is
			// mid-rewrite): a stale defect is never reported
			lastDefect = defect
		}
		if res != nil {
			if err := r.archive(res); err != nil {
				_ = r.handle.Terminate(teardownBound)
				return nil, fmt.Errorf("driver: step %s spawn %d result staging failed: %w", r.sp.StepID, r.sp.SpawnN, err)
			}
			// Consume the drop (the #97 lesson): collection is single-shot,
			// the archived copy is the durable record — a drop left in place
			// would be re-collected by the next dispatch on this ordinal. A
			// failed remove is logged, never fatal (the fence still guards).
			if rerr := os.Remove(r.dropPath); rerr != nil && !os.IsNotExist(rerr) {
				fmt.Fprintf(os.Stderr, "driver: step %s spawn %d drop removal failed (stale re-read still fenced): %v\n",
					r.sp.StepID, r.sp.SpawnN, rerr)
			}
			if terr := r.handle.Terminate(teardownBound); terr != nil {
				fmt.Fprintf(os.Stderr, "driver: step %s spawn %d teardown after result failed (envelope already collected): %v\n",
					r.sp.StepID, r.sp.SpawnN, terr)
			}
			return res, nil
		}
		if r.handle.Done() {
			// one last look: the envelope may have landed between the
			// collect above and the exit
			if res2, defect2 := r.collect(); res2 != nil {
				continue // collected on the next pass
			} else if defect2 != nil {
				defect = defect2
			}
			return nil, r.exited(defect)
		}
		select {
		case <-ctx.Done():
			if lastDefect != nil {
				return nil, &EnvelopeDefectError{Defect: lastDefect, Cause: ctx.Err()}
			}
			return nil, ctx.Err()
		case <-time.After(r.poll):
		}
	}
}

func (r *stepRun) Terminate(bound time.Duration) error {
	return r.handle.Terminate(bound)
}

// collect reads the drop path once: a non-nil envelope passed the
// light shape/fence checks; (nil, nil) means nothing usable yet
// (absent, or a write in flight); (nil, err) is the last observed
// defect, surfaced by Wait once the process is gone.
func (r *stepRun) collect() (*StepResult, error) {
	raw, err := os.ReadFile(r.dropPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var res StepResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("envelope at %s is not valid JSON: %w", r.dropPath, err)
	}
	// Light completion checks — the identity fields a minimal-contract
	// envelope may leave to the dispatch are filled from the dispatch
	// identity; the fence and the closed sets are enforced (full
	// validation is the driver core's accept gate).
	if res.StepID == "" {
		res.StepID = r.sp.StepID
	}
	if res.StepID != r.sp.StepID {
		return nil, fmt.Errorf("%w: envelope names step %q, dispatch is %q",
			ErrIdentityMismatch, clipAgentText(res.StepID), r.sp.StepID)
	}
	if res.WIID == "" {
		res.WIID = r.wiID
	}
	if res.WIID != r.wiID {
		return nil, fmt.Errorf("%w: envelope names work item %q, dispatch is %q",
			ErrIdentityMismatch, clipAgentText(res.WIID), r.wiID)
	}
	if res.SpawnN == 0 {
		res.SpawnN = r.sp.SpawnN
	}
	if res.SpawnN != r.sp.SpawnN {
		return nil, fmt.Errorf("%w: envelope spawn %d, dispatch %d",
			ErrStaleSpawn, res.SpawnN, r.sp.SpawnN)
	}
	if !res.Outcome.Valid() {
		return nil, fmt.Errorf("driver: envelope outcome %q is not in the closed set", clipAgentText(string(res.Outcome)))
	}
	return &res, nil
}

// archive lands the collected envelope at the R14 staging path —
// artifacts/steps/<step-id>/s<spawn_n>/result.json — atomically. The
// handoff chain reads the next inherit step's injection from exactly
// here, and a later accept-time re-validation lane finds the bytes in
// place.
func (r *stepRun) archive(res *StepResult) error {
	blob, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(r.archivePath(), blob, 0o644)
}

// archivePath is the dispatch's R14 staging path.
func (r *stepRun) archivePath() string {
	return filepath.Join(r.capsuleRoot, capsule.DirArtifacts, "steps", r.sp.StepID, spawnTokenDir(r.sp.SpawnN), "result.json")
}

// maxQuotedAgentText bounds agent-written text quoted into a defect: the
// defect travels into step_dead evidence, the events log and the retry
// prompt.
const maxQuotedAgentText = 256

// clipAgentText cuts agent-written text to maxQuotedAgentText bytes (on
// a rune boundary), marking the cut.
func clipAgentText(s string) string {
	if len(s) <= maxQuotedAgentText {
		return s
	}
	cut := maxQuotedAgentText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}
