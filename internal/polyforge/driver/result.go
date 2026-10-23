// result.go: the step result envelope and its pre-commit validation —
// the STAGING half of the done commit point. The atomic execution.json
// acceptance belongs to transition.go; this file only answers "is this
// envelope acceptable".
//
// A result envelope is the one channel a step process uses to report
// its outcome. The driver trusts nothing but the envelope, and the
// envelope itself is trusted only after three gates, in order:
//
//  1. shape     — the outcome is in the closed set
//  2. binding   — wi_id/step_id identity and the spawn_n fence
//  3. substance — the needs_input question pair, the gate verdict
//     binding, the human-gate guarantee, the deploy-only defer rule
//     and the declared output schema
//
// The MINIMAL CONTRACT (polyforge#147 审计减法 A): an envelope is the
// outcome + output pair (needs_input adds question_id + question),
// written to the unchanged drop path; the driver judges only the
// fields above. There is no protocol version stamp, no envelope id,
// no artifact manifest, no handoff digest reference, and no hash or
// manifest verification anywhere — downstream and handoff readers work
// by path convention.
//
// For a step the workflow declares gate: true there is a further rule,
// the VERDICT BINDING (the #106 lesson): a gate's business verdict —
// the top-level "verdict" string in the output JSON — and the
// envelope's routing outcome must agree in the fail direction. A
// completed envelope carrying a fail verdict is a mislabeled gate-fail
// report: it is never accepted as a completed step. ValidateStepResult
// re-binds it to gate_failed — the only outcome whose routing (the
// gate's on_fail policy) the driver honors — so a gate's fail verdict
// can never advance a workflow, whatever the envelope's outcome field
// says. Fail-closed by construction.
package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// EvEnvelopeRejected is the durable event kind for the in-process
// envelope-rejection retry lane (one event per rejection, carrying the
// validation error verbatim — the operator sees exactly what the step
// author got wrong).
const EvEnvelopeRejected = "envelope_rejected"

// EvGateVerdictBound is the durable event kind for the verdict binding
// (the #106 lesson): one event per envelope the driver re-classified
// from completed to gate_failed because a workflow-declared gate step
// delivered a fail verdict under a completed outcome. The delivered
// envelope is archived verbatim where it always is (the R14 staging
// result.json); this event is the news that its ROUTING was bound to
// the verdict.
const EvGateVerdictBound = "gate_verdict_bound"

// EvHumanGateUnasked is the durable event kind recording that a
// requires_human_answer: if_asked step (autopilot grill) completed with
// zero human answers (polyforge#164 H11). The completion is accepted —
// zero questions is a legitimate outcome — but never silent: the event
// is the record and the watcher's decision table notifies the owner.
const EvHumanGateUnasked = "human_gate_unasked"

// EvLightRecheckEscalated is the durable event kind of the mechanical
// fast lane's in-place escalation (polyforge#260): one event per
// escalation — pre-dispatch (a malformed classification or a broken
// repair bound) or at the light recheck's settlement (a recheck that
// did not pass) — carrying the reason. The escalation is not a rewind
// and charges no slot; the audit record is the attempt the same write
// archives (cause light_recheck_escalated).
const EvLightRecheckEscalated = "light_recheck_escalated"

// releaseStepID and deployStepID are the step ids of the `release`
// workflow bundle (the only place a release or deploy happens). Their
// declared output schemas are enforced (outputSchemaEnforced): `release`
// owns the closed vocabulary [pass, fail] (a release WI with no procedure
// is an error, never a defer) and its pass must name the published
// artifacts; `deploy` owns [pass, defer, fail] — defer validates on it
// and nowhere else (rule 3c below), because a project may release
// without deploying. They mirror the bundle's step declarations the same
// way verificationStepID does in observe.
const (
	releaseStepID = "release"
	deployStepID  = "deploy"
)

// maxEnvelopeRejects bounds the in-process re-dispatch lane: a step
// whose envelopes keep failing validation this many times CONSECUTIVELY
// goes BLOCKED with the result_invalid cause code (no driver relaunch,
// no recovery lane, no on_fail routing — rewind stays reserved for
// review FAIL verdicts). The #97/#107 history: hand-transcribed digest
// typos and missing handoff refs — the retries fix the author, the bound
// keeps a genuinely broken author from looping forever.
const maxEnvelopeRejects = 3

// Outcome is the closed result-verdict set of the envelope.
type Outcome string

const (
	OutcomeCompleted  Outcome = "completed"
	OutcomeGateFailed Outcome = "gate_failed"
	OutcomeNeedsInput Outcome = "needs_input"
	OutcomeIncomplete Outcome = "incomplete"
)

// Valid reports whether o belongs to the closed outcome set.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeCompleted, OutcomeGateFailed, OutcomeNeedsInput, OutcomeIncomplete:
		return true
	}
	return false
}

// StepResult is the result envelope: the step process's complete and
// only report. The MINIMAL CONTRACT (polyforge#147 审计减法 A): the
// outcome + output pair — needs_input adds the question pair — plus the
// identity/fence fields the driver judges the envelope against. There
// is no protocol version stamp, no envelope id, no artifact manifest
// and no handoff digest reference: downstream and handoff readers work
// by path convention, and an envelope missing any retired field is
// never judged invalid for its absence.
type StepResult struct {
	WIID       string          `json:"wi_id"`
	StepID     string          `json:"step_id"`
	SpawnN     int64           `json:"spawn_n"` // fencing: only the current spawn's results count
	Outcome    Outcome         `json:"outcome"`
	Output     json.RawMessage `json:"output"`                // step-specific output JSON, schema-checked
	QuestionID string          `json:"question_id,omitempty"` // set when Outcome == needs_input
	Question   string          `json:"question,omitempty"`    // the question text for needs_input
	// CommandEvidence is the V4 command-evidence list, kept verbatim: it
	// must be a list whose every item decodes as a CommandEvidence object
	// (decodeCommandEvidence, any step), and a step that owes evidence
	// (ResultSpec.RequireCommandEvidence) must carry well-formed entries.
	CommandEvidence json.RawMessage `json:"command_evidence,omitempty"`
	SubmittedAt     time.Time       `json:"submitted_at,omitempty"`

	// rebaseline is set by ValidateStepResult when the step changed
	// authored test files with a human's approval (the authored-tests
	// gate): the accepted output is stamped with the new baseline.
	rebaseline bool
	// rebaselined names the authored test files a human-approved change
	// touched (the authored_tests_rebaselined event body).
	rebaselined []string
	// schemaViolation is the declared-output-schema validation error of
	// an accepted result on a step whose schema is recorded, not
	// enforced (outputSchemaEnforced false); "" = conforming.
	schemaViolation string
}

// verificationStepID is the conventional id of the verification gate:
// its accepted run anchors the deliver merge rule (verified heads, test
// bar) and its pre-existing-failure boundary. Its command-evidence duty is
// declared in the bundle (requires_command_evidence), not implied by the id.
const verificationStepID = "verification"

// CommandEvidence is one recorded command the step process ran, kept in
// the envelope for audit (the operator face renders it). Validation of
// an evidence-owing step checks argv non-emptiness and duration_ms > 0
// (checkCommandEvidence); exit_code and started_at are not enforced.
type CommandEvidence struct {
	Argv       []string  `json:"argv"`
	Cwd        string    `json:"cwd"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	LogPath    string    `json:"log_path"`
}

// Validation verdicts.
var (
	ErrStaleSpawn       = fmt.Errorf("driver: result fenced out (spawn_n mismatch): %w", ErrStaleFence)
	ErrIdentityMismatch = errors.New("driver: result bound to another work item/step")
)

// ResultSpec carries everything ValidateStepResult judges an envelope
// against: the binding and fence of the CURRENT spawn, the gate verdict
// binding and the human-gate guarantee. It is a read-only snapshot: the
// mutations happen at the commit point, never here.
type ResultSpec struct {
	WIID   string
	StepID string
	SpawnN int64
	// Gate mirrors the step's gate: true declaration — the verdict
	// binding applies (a completed envelope carrying a fail verdict is
	// re-bound to gate_failed, never accepted as a completed step).
	Gate bool
	// RequiresHumanAnswer mirrors the step's requires_human_answer: true
	// declaration: a delivered completed or gate_failed result is rejected
	// unless HumanAnswers > 0 (the human-gate guarantee, polyforge#138).
	RequiresHumanAnswer bool
	// HumanAnswers is the number of human answers recorded in the step's
	// CURRENT attempt chain: st.Answers[st.AnswerBase:], where AnswerBase
	// is advanced to len(Answers) by every gate-fail rewind that archives
	// the step (see currentChainAnswers). Answers only enter the record
	// through the answer transition, so every entry is a human's.
	HumanAnswers int
	// RequireCommandEvidence mirrors requires_command_evidence: true: an
	// accepted completed result must carry at least one command_evidence
	// entry, each with a non-empty argv and duration_ms > 0 (the V4 gate;
	// exit_code presence is not checkable).
	RequireCommandEvidence bool
	// ReposDir is the capsule's task-repo root (<capsule>/repos). When
	// set, a completed result whose output declares a non-empty
	// test_files list is accepted only if every listed (repo-relative)
	// path exists in a task repo and is committed at HEAD in every task
	// repo where it exists (polyforge#156: a resume restores task repos
	// at their carrier ref, i.e. HEAD only, so an uncommitted authored
	// test would be lost).
	ReposDir string
	// OutputSchema is the step definition's declared output schema
	// (polyforge#164 H3); nil = none declared, nothing validated.
	OutputSchema *jsonschema.Schema
	// AuthoredTests is the authored-tests baseline of the done steps
	// before this one (polyforge#164 H4); nil = no authored tests.
	AuthoredTests *AuthoredTests
	// PreExisting is the pre-existing-failure boundary (polyforge#164
	// S6): the tests that pin the change itself — the authored test_files
	// of the done steps (e2e-author, repro) and bug-fix's regression_test
	// paths — and whether the repro reproduced the bug (checkPreExisting).
	PreExisting PreExistingRules
	// ReviewPassed: the workflow's independent review (the `review` step)
	// is done with a pass verdict and no blocking finding. The deliver
	// binding (deliver.go) merges without a human only when it holds.
	ReviewPassed bool
	// HasReview: the workflow declares a review step.
	HasReview bool
	// VerifiedHeads / VerifiedTestBar: the verification step's record
	// (task repo -> HEAD when it completed, driver-written; the test bar
	// it named) — deliver_facts.go.
	VerifiedHeads   map[string]string
	VerifiedTestBar []testBarCmd
	// LightRecheckHeads (polyforge#260): the recheck heads of a
	// light-passed verification record (mechanical_pass.recheck_heads,
	// driver-written) — the deliver merge rule's anchor on the light path
	// (a light pass never writes verified_heads, so the repo carries no
	// verified head at all).
	LightRecheckHeads map[string]string
	// LightRewind (polyforge#222, polyforge#260): the step's latest
	// non-infra_failure archived attempt is a light rewind
	// (gate_fail_rewind_text_only or gate_fail_rewind_mechanical) —
	// this dispatch is that rewind's re-entry, so the completed result
	// may carry the needs_code_change structural self-report. Assembled
	// under the driver's lock from the same ledger the transition's
	// self-report rewind reads (lightRewindContext).
	LightRewind bool
}

// ValidateStepResult runs the staging gates over one envelope:
//
//  1. shape: the outcome is in the closed set;
//  2. binding: wi_id/step_id match and spawn_n equals the current
//     spawn (a result from a superseded spawn is fenced out);
//  3. substance, in order:
//     3a. a needs_input envelope owes its question pair (nameable
//     before the process is gone — an in-process rewrite, never a
//     recovery-lane error at the commit point);
//     3b. verdict binding (gate steps only): a completed envelope whose
//     output carries a top-level fail verdict is re-bound to
//     gate_failed BEFORE anything else is judged. The re-bind is
//     visible to the caller: res.Outcome differs from the delivered outcome after
//     this call, and the Run loop logs it as an EvGateVerdictBound
//     event;
//     3b'. the human-gate guarantee (#138): judged on the DELIVERED
//     outcome so a gate-fail verdict cannot dodge the human either;
//     3c. judged on the DELIVERED outcome (a completed envelope): a
//     top-level defer verdict on any step but `deploy` is a validation
//     error (the gate binding above stays prefix-fail only, so a defer
//     would otherwise clear a gate); then the declared output schema
//     (OutputSchema set, polyforge#164 H3). On the release bundle's
//     `release` and `deploy` steps (outputSchemaEnforced) a violation
//     — or a schema that is absent or does not load — is an envelope
//     rejection — their schemas carry the verdict
//     contract: exact verdict enums, pass owes the version (and on
//     `release` the commit and published artifacts), fail owes a
//     fail_reason, a deploy defer carries neither version nor
//     artifacts. On every other step a violation of an accepted
//     completed result is recorded (res.schemaViolation), not rejected.
//     A rejection is the in-process retry lane, never a silent
//     reclassification;
//     3d. the needs_code_change self-report (polyforge#222), judged on
//     the DELIVERED completed outcome: a non-boolean value, or the
//     field without a light rewind preceding it in the step's ledger
//     (spec.LightRewind), is an envelope rejection — the field belongs
//     only to a light re-entry's structural self-report;
//  4. the command-evidence gate: every command_evidence item, on any
//     step and outcome, must be an object that decodes as a
//     CommandEvidence (decodeCommandEvidence); on a RequireCommandEvidence
//     step (the V4 gate) a completed result must carry at least one
//     entry, each with a non-empty argv and duration_ms > 0;
//  5. the committed-test-files gate (ReposDir set, any step): a
//     completed result whose output declares test_files must have every
//     listed path committed at HEAD in its task repo(s)
//     (checkTestFilesCommitted) — rejected like the evidence gate;
//  6. the pre-existing-failure boundary (any completed step,
//     checkPreExisting): no repro check or authored/regression test may
//     be classified pre_existing, a pre_existing check must name its base
//     commit and be listed in pre_existing_failures, and a bug-fix
//     verification pass must show the reproduced bug's repro check
//     passing (polyforge#164 S6);
//  7. the authored-tests gate (ReposDir and AuthoredTests set; the
//     driver never sets AuthoredTests for `deliver`): the test files an
//     earlier step authored must be unchanged since its commit, unless a
//     human answered in this attempt chain (then res.rebaseline is set)
//     (polyforge#164 H4);
//  8. the delivery binding (the `deliver` step only, deliverBinding):
//     recorded pushes target only the feature branch, merges use
//     --merge, and a completed pass that merged outside the merge rule
//     or left a PR unmerged owes a human answer — rejected like the
//     evidence gate.
//
// The minimal contract: an envelope missing the retired protocol
// fields is never judged invalid for its absence — there is no
// protocol version, no envelope id, no artifact manifest, no handoff
// digest reference and no hash or manifest verification anywhere.
//
// Non-completed outcomes (gate_failed, needs_input, incomplete) carry
// their disposition on other channels (the gate verdict, the pending
// question).
func ValidateStepResult(res *StepResult, spec ResultSpec) error {
	if res == nil {
		return errors.New("driver: nil step result")
	}
	// 1. shape.
	if !res.Outcome.Valid() {
		return fmt.Errorf("driver: unknown outcome %q", res.Outcome)
	}
	// 2. binding: identity first, then the fence.
	if res.WIID != spec.WIID || res.StepID != spec.StepID {
		return fmt.Errorf("%w: envelope binds %s/%s, expected %s/%s",
			ErrIdentityMismatch, res.WIID, res.StepID, spec.WIID, spec.StepID)
	}
	if res.SpawnN != spec.SpawnN {
		return fmt.Errorf("%w: envelope carries spawn_n %d, current spawn is %d",
			ErrStaleSpawn, res.SpawnN, spec.SpawnN)
	}
	// 3a. needs_input envelope shape: the question must be nameable
	// BEFORE the process is gone, so a missing question_id/question is an
	// envelope rejection (in-process rewrite), never a recovery-lane error
	// at the commit point.
	if res.Outcome == OutcomeNeedsInput {
		if _, _, err := needsInputQuestion(res); err != nil {
			return err
		}
	}
	// 3b. the verdict binding (the #106 lesson): for a gate step the
	// output's top-level fail verdict IS the routing. A completed
	// envelope carrying one is a mislabeled gate-fail report — re-bind
	// it to gate_failed so the gate's on_fail policy fires and the
	// verdict can never advance a workflow. The caller logs the bind;
	// the delivered envelope stays archived verbatim.
	delivered := res.Outcome
	if spec.Gate && delivered == OutcomeCompleted {
		if _, fail := failVerdictInOutput(res.Output); fail {
			res.Outcome = OutcomeGateFailed
		}
	}
	// 3b'. the human-gate guarantee (#138): a requires_human_answer step
	// cannot finish its round — neither completed nor gate_failed (which
	// would rewind or terminally fail the workflow) — until a human has
	// answered a question in this attempt. Judged on the DELIVERED
	// outcome, so a completed envelope re-bound to gate_failed above is
	// covered too (owner ruling on repo review 2026-10-16 V-8: before,
	// a native gate_failed envelope dodged the human entirely).
	if (delivered == OutcomeCompleted || delivered == OutcomeGateFailed) && spec.RequiresHumanAnswer && spec.HumanAnswers <= 0 {
		return fmt.Errorf("driver: step %q requires a human answer: return needs_input first (a %s result is refused until a human has answered a question in this attempt)",
			spec.StepID, delivered)
	}
	// 3c. judged on the DELIVERED outcome, so a fail verdict re-bound to
	// gate_failed above still owes what its schema declares.
	if delivered == OutcomeCompleted {
		if v := topVerdict(res.Output); spec.StepID != deployStepID && strings.EqualFold(strings.TrimSpace(v), "defer") {
			return fmt.Errorf("driver: step %q output carries verdict %q — defer validates nowhere but the %s step's schema",
				spec.StepID, v, deployStepID)
		}
		if spec.OutputSchema == nil && outputSchemaEnforced(spec.StepID) {
			return fmt.Errorf("driver: step %q output schema unloadable: its declared schema is the verdict contract and is enforced, so the result cannot be accepted until the bundle's step definition declares a schema that loads", spec.StepID)
		}
		if spec.OutputSchema != nil {
			if err := checkOutputSchema(spec.OutputSchema, res.Output); err != nil {
				if outputSchemaEnforced(spec.StepID) {
					return fmt.Errorf("driver: step %q completed but its %w", spec.StepID, err)
				}
				if res.Outcome == OutcomeCompleted {
					res.schemaViolation = err.Error()
				}
			}
		}
		// 3d. the needs_code_change self-report (polyforge#222): legal only
		// on a completed result under a light rewind — the field belongs
		// to the light re-entry's structural self-report, whose fresh
		// rewind reuses the light rewind's slot. A non-boolean value, or
		// the field without the light context, is an envelope rejection
		// (the in-process rewrite lane): never a silent ignore, and never
		// an uncharged rewind (that would be a budget hole).
		if out, perr := parseOutput(res.Output); perr == nil {
			present, _, malformed := needsCodeChangeOf(out)
			if malformed {
				return fmt.Errorf("driver: step %q completed with a non-boolean needs_code_change (the field is the light re-entry's structural self-report; it is a boolean or absent)", spec.StepID)
			}
			if present && !spec.LightRewind {
				return fmt.Errorf("driver: step %q completed carrying needs_code_change but no light rewind precedes it in its ledger — the field belongs only to a light re-entry's structural self-report; omit it on a normal completion (apply the fix and complete, or ask a human)", spec.StepID)
			}
		}
	}
	// 4. the command-evidence gate: an item the driver cannot read is
	// rejected, never dropped; then the V4 gate — a completed result
	// (after the verdict re-bind) of an evidence-owing step must carry
	// well-formed command_evidence.
	evidence, err := decodeCommandEvidence(res.CommandEvidence)
	if err != nil {
		return fmt.Errorf("driver: step %q envelope carries unreadable command evidence: %w", spec.StepID, err)
	}
	if res.Outcome == OutcomeCompleted && spec.RequireCommandEvidence {
		if err := checkCommandEvidence(evidence); err != nil {
			return fmt.Errorf("driver: step %q completed without verification evidence: %w", spec.StepID, err)
		}
	}
	// 5. the committed-test-files gate (polyforge#156): declared test
	// files must survive a resume, which restores task repos at HEAD only.
	if res.Outcome == OutcomeCompleted && spec.ReposDir != "" {
		if err := checkTestFilesCommitted(spec.ReposDir, res.Output); err != nil {
			return fmt.Errorf("driver: step %q completed with %w", spec.StepID, err)
		}
	}
	// 6. the pre-existing-failure boundary (polyforge#164 S6): the tests
	// that pin THIS change — the repro, the authored acceptance and
	// regression tests — can never be excused as failing on the base.
	if res.Outcome == OutcomeCompleted {
		if err := checkPreExisting(res.Output, spec.PreExisting, spec.StepID == verificationStepID); err != nil {
			return fmt.Errorf("driver: step %q completed with %w", spec.StepID, err)
		}
	}
	// 7. the authored-tests gate (polyforge#164 H4).
	if res.Outcome == OutcomeCompleted && spec.ReposDir != "" && spec.AuthoredTests != nil {
		changed, err := checkAuthoredTestsUnchanged(spec.ReposDir, spec.AuthoredTests, spec.HumanAnswers)
		if err != nil {
			return fmt.Errorf("driver: step %q completed with %w", spec.StepID, err)
		}
		res.rebaseline = len(changed) > 0
		res.rebaselined = changed
	}
	// 8. the delivery binding (polyforge#166, deliver.go): the deliver
	// step's recorded pushes target only the feature branch and merge only
	// with --merge; a merge outside the merge rule, or a PR left unmerged,
	// needs a human answer.
	if spec.StepID == deliverStepID {
		if err := deliverBinding(res, evidence, delivered, spec); err != nil {
			return err
		}
	}
	return nil
}

// checkCommandEvidence enforces the V4 evidence shape: at least one entry,
// every entry with a non-empty argv and duration_ms > 0. exit_code's
// zero value is legitimate and its presence cannot be checked on the
// decoded struct, so it is not enforced.
func checkCommandEvidence(ev []CommandEvidence) error {
	if len(ev) == 0 {
		return errors.New("command_evidence is empty — a verification step must carry at least one entry, each an OBJECT {\"argv\": [...], \"exit_code\": N, \"duration_ms\": N} (duration_ms > 0)")
	}
	for i, e := range ev {
		if len(e.Argv) == 0 || strings.TrimSpace(strings.Join(e.Argv, "")) == "" {
			return fmt.Errorf("command_evidence[%d] has an empty argv", i)
		}
		if e.DurationMs <= 0 {
			return fmt.Errorf("command_evidence[%d] (%s) has no duration_ms (must be > 0)", i, e.Argv[0])
		}
	}
	return nil
}

// decodeCommandEvidence decodes the envelope's command_evidence (absent
// or null = none). A value that is not a list, or an item that is not a
// JSON object or does not decode as a CommandEvidence, is an error naming
// it: the rejection feeds back into the retry prompt, so the rewrite
// knows what to fix.
func decodeCommandEvidence(raw json.RawMessage) ([]CommandEvidence, error) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("command_evidence is not a LIST — it is [{\"argv\": [...], \"exit_code\": N, \"duration_ms\": N}, ...]")
	}
	ev := make([]CommandEvidence, 0, len(items))
	for i, el := range items {
		if !bytes.HasPrefix(bytes.TrimSpace(el), []byte("{")) {
			return nil, fmt.Errorf("command_evidence[%d] is not an OBJECT — each entry is {\"argv\": [...], \"exit_code\": N, \"duration_ms\": N}, never a string", i)
		}
		var ce CommandEvidence
		if err := json.Unmarshal(el, &ce); err != nil {
			return nil, fmt.Errorf("command_evidence[%d] could not be decoded: %v (started_at must be RFC3339, argv a string list, numbers numeric)", i, err)
		}
		ev = append(ev, ce)
	}
	return ev, nil
}

// needsInputQuestion extracts the question of a needs_input envelope: it
// may live at envelope level (question_id/question) or embedded in the
// output object (step agents naturally put it there). Each half is taken
// from the envelope first, then the output. An error names what is missing.
func needsInputQuestion(res *StepResult) (id, text string, err error) {
	id, text = strings.TrimSpace(res.QuestionID), strings.TrimSpace(res.Question)
	if (id == "" || text == "") && len(res.Output) > 0 {
		var embedded struct {
			QuestionID string `json:"question_id"`
			Question   string `json:"question"`
		}
		if json.Unmarshal(res.Output, &embedded) == nil {
			if id == "" {
				id = strings.TrimSpace(embedded.QuestionID)
			}
			if text == "" {
				text = strings.TrimSpace(embedded.Question)
			}
		}
	}
	var missing []string
	if id == "" {
		missing = append(missing, "question_id")
	}
	if text == "" {
		missing = append(missing, "question")
	}
	if len(missing) > 0 {
		return id, text, fmt.Errorf("driver: needs_input result is missing %s (set them on the envelope or in the output object)",
			strings.Join(missing, " and "))
	}
	// D11: the id is agent-chosen and travels into the human-facing
	// wake-up line, the answer verb and the event log — so its shape is
	// part of the envelope contract (the watcher's safeID filter stays as
	// defense in depth).
	if !questionIDShape.MatchString(id) {
		return id, text, fmt.Errorf("driver: needs_input question_id %q is malformed (must match %s)", id, questionIDShape)
	}
	return id, text, nil
}

// CheckEnvelopeShape is the light shape check of a submitted envelope
// (`pf result submit`, the daemon's POST /step/result): the outcome is
// in the closed set, output (when present) is a JSON object, and a
// needs_input carries a well-formed question pair. It reuses the
// accept gate's own checks; everything deeper (output schema, command
// evidence, test files, …) stays with ValidateStepResult, the single
// authority.
func CheckEnvelopeShape(res *StepResult) error {
	if !res.Outcome.Valid() {
		return fmt.Errorf("driver: unknown outcome %q (one of completed, gate_failed, needs_input, incomplete)", clipAgentText(string(res.Outcome)))
	}
	if _, err := parseOutput(res.Output); err != nil {
		return fmt.Errorf("driver: envelope %w", err)
	}
	if res.Outcome == OutcomeNeedsInput {
		if _, _, err := needsInputQuestion(res); err != nil {
			return err
		}
	}
	return nil
}

// questionIDShape is the accepted needs_input question id: 1–64 chars of
// letters, digits, '.', '_' and '-'.
var questionIDShape = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// currentChainAnswers counts the human answers of a step's current attempt
// chain: answers recorded since the step's last gate-fail rewind
// (st.Answers[st.AnswerBase:]). A step never rewound counts all of them.
func currentChainAnswers(st *capsule.Step) int {
	base := st.AnswerBase
	if base < 0 || base > len(st.Answers) {
		base = len(st.Answers)
	}
	return len(st.Answers) - base
}

// failVerdictInOutput reports whether one result output carries a
// top-level "verdict" field whose STRING value asserts failure. The
// match is a case- and space-insensitive prefix on "fail" — the
// verdict vocabulary the gate steps declare (enum [pass, fail]) plus
// the prose-decorated shape the in-repo gate fixtures use ("FAIL:
// tests red"). Only the top-level scalar is read: a nested verdict or
// a non-string one is not a fail verdict this binding can interpret,
// and the declared-schema lane stays its authority.
func failVerdictInOutput(output json.RawMessage) (string, bool) {
	s := topVerdict(output)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "fail") {
		return s, true
	}
	return "", false
}

// topVerdict answers the top-level "verdict" string of one result output
// ("" when the output is not an object or its verdict is absent or not a
// string).
func topVerdict(output json.RawMessage) string {
	var probe struct {
		Verdict any `json:"verdict"`
	}
	if len(output) == 0 || json.Unmarshal(output, &probe) != nil {
		return ""
	}
	s, _ := probe.Verdict.(string)
	return s
}
