// Package watcher is the Go implementation of `pf watch` (v2-converge §3)
// after the one-shot WI snapshot render and the fixed
// decision table the daemon's decision feed classifies with. It never
// calls a model and never forwards text written by a step agent into a
// decision.
package watcher

import (
	"encoding/json"
	"sort"
	"strings"
)

// Rule classifies one decision-worthy kind. Class is the CLOSED name of the
// decision; Phrase is a fixed sentence; Attention marks a class that needs
// a human (the Pi Web panel counts the open ones). None of them ever
// carries text from an event body or from execution.json.
type Rule struct {
	Class     string
	Phrase    string
	Attention bool
}

// Decision is one classified decision: its rule, the raw kind or status it
// came from (Source), and the two identifiers a decision may name — the
// pending question id of a needs_input decision and the task repo of a
// carrier_push_failed one, both reduced to a safe charset. Status marks a
// work-item state decision (completed, failed, canceled, blocked, paused,
// terminal): a later one supersedes an earlier open needs_input, blocked or
// paused decision of the same work item. Final marks a state that ends the
// work item (statusRules; terminal is final too).
type Decision struct {
	Rule
	Source     string
	QuestionID string
	Repo       string
	Status     bool
	Final      bool
}

// THE DECISION TABLE. Event kinds are open strings the daemon and driver
// may add at any time; a kind absent here is simply not decision-worthy.
// To make a new kind reach the human, add it to eventRules (and, if it is
// a new workflow_status, to statusRules). Nothing else needs to change.
var eventRules = map[string]Rule{
	// a step's agent asked the owner with `pf ask` and waits for the reply
	// (workflow contract v2 §5.2): the needs_input class — the attention
	// channel Pi Web's orange button and card read; Classify reads the
	// ask's stamp (its id) from the body. (The v1 needs_input event left
	// with the v1 question, workflow contract v2 change 19.)
	"awaiting_owner": {Class: "needs_input", Phrase: "a step asked the owner a question and is waiting for the reply", Attention: true},
	// a step was judged dead / the model candidates ran out
	"step_dead":            {Class: "step_dead", Phrase: "a step was judged dead and the daemon handled it"},
	"candidates_exhausted": {Class: "candidates_exhausted", Phrase: "all model candidates are exhausted", Attention: true},
	// daemon-side submit and restart signals
	"submit_rejected":  {Class: "submit_rejected", Phrase: "a submit was accepted but did not start", Attention: true},
	"submit_dropped":   {Class: "submit_dropped", Phrase: "a queued submit was dropped", Attention: true},
	"daemon_restarted": {Class: "daemon_restarted", Phrase: "the daemon restarted"},
	// a task repo's carrier ref (refs/pf-task/<repo>) could not be pushed
	// although the capsule chain landed: a resume restores the repo
	// without the WI's commits since its last carried head
	// (polyforge#158). The daemon raises it once per repo per job until a
	// carrier push of that repo succeeds again; Classify reads the repo.
	"carrier_push_failed": {Class: "carrier_push_failed", Phrase: "this task repo cannot be saved; a resume may lose its commits", Attention: true},
}

// statusRules maps a status to its state decision: the driver's
// execution.json workflow_status (completed, failed_gate, blocked —
// carried by the terminal and blocked events) and the aihub status a
// manual close or a hub read reports (wrapped, failed, canceled, paused,
// blocked — the driver never writes those words to execution.json).
// Final statuses end the work item; blocked
// and paused do not (the human can continue it): the decision feed keeps
// re-reading a non-final one's aihub status, and Pi Web's needs-input
// discussion suspends rather than ends on one (pi-web
// HUB_RESUMABLE_STATUSES; test/w413_discussion_question_ids_test.go holds
// the lists equal).
var statusRules = map[string]struct {
	Rule
	Final bool
}{
	"completed":   {Rule{Class: "completed", Phrase: "the workflow completed"}, true},
	"failed_gate": {Rule{Class: "failed", Phrase: "the workflow failed", Attention: true}, true},
	"blocked":     {Rule{Class: "blocked", Phrase: "the workflow is blocked and needs a human", Attention: true}, false},
	"wrapped":     {Rule{Class: "completed", Phrase: "the workflow completed"}, true},
	"failed":      {Rule{Class: "failed", Phrase: "the workflow failed", Attention: true}, true},
	"canceled":    {Rule{Class: "canceled", Phrase: "the work item was canceled"}, true},
	"paused":      {Rule{Class: "paused", Phrase: "the work item was paused"}, false},
}

// terminalRule is the terminal event whose status the table does not know.
var terminalRule = Rule{Class: "terminal", Phrase: "the workflow reached an end state"}

// taskRules maps a background task's terminal state (task.State) to its
// decision.
var taskRules = map[string]Rule{
	"completed":   {Class: "completed", Phrase: "the background task completed"},
	"failed":      {Class: "failed", Phrase: "the background task failed", Attention: true},
	"capped":      {Class: "capped", Phrase: "the background task hit its budget", Attention: true},
	"interrupted": {Class: "interrupted", Phrase: "the background task was interrupted", Attention: true},
}

// supersededRule: the runtime released a work item another session holds
// now (a supersede, a hub cancel of a held job), or finished a close
// locally that the hub never confirmed — the work item's state is aihub's.
var supersededRule = Rule{Class: "superseded", Phrase: "this machine released the work item; its state is aihub's"}

// ReleaseDecision answers the decision of the runtime's release reason
// (observe's job_finished): a close's state through the status table
// (wrapped, failed, blocked, paused, and canceled for a record dropped at
// restart), superseded as its own informational, final decision; ok=false
// for anything else (a re-queued work item).
func ReleaseDecision(reason string) (Decision, bool) {
	if reason == "superseded" {
		return Decision{Rule: supersededRule, Source: reason, Status: true, Final: true}, true
	}
	return StatusDecision(reason)
}

// Classify answers the decision one appended daemon event carries
// (ok=false: the kind is not decision-worthy). A terminal event is
// classified by its body's status; a blocked event is the blocked state.
func Classify(kind string, body json.RawMessage) (Decision, bool) {
	switch kind {
	case "terminal":
		st := bodyField(body, "status")
		if d, ok := StatusDecision(st); ok {
			return d, true
		}
		return Decision{Rule: terminalRule, Source: "terminal", Status: true, Final: true}, true
	case "blocked":
		return StatusDecision("blocked")
	}
	r, ok := eventRules[kind]
	if !ok {
		return Decision{}, false
	}
	d := Decision{Rule: r, Source: kind}
	switch kind {
	case "awaiting_owner":
		d.QuestionID = safeID(bodyField(body, "question_id"))
	case "carrier_push_failed":
		d.Repo = safeID(bodyField(body, "repo"))
	}
	return d, true
}

// StatusDecision answers the state decision of a workflow or aihub status
// (ok=false: not a decision — running, queued, …).
func StatusDecision(status string) (Decision, bool) {
	r, ok := statusRules[status]
	if !ok {
		return Decision{}, false
	}
	return Decision{Rule: r.Rule, Source: status, Status: true, Final: r.Final}, true
}

// TaskDecision answers the decision of a background task's terminal state
// (ok=false: not terminal).
func TaskDecision(state string) (Decision, bool) {
	r, ok := taskRules[state]
	if !ok {
		return Decision{}, false
	}
	return Decision{Rule: r, Source: "task"}, true
}

// Kinds answers every source kind the table classifies (events, workflow
// and hub statuses), sorted — the documentation and tests enumerate it.
func Kinds() []string {
	seen := map[string]bool{"terminal": true, "blocked": true}
	for k := range eventRules {
		seen[k] = true
	}
	for k := range statusRules {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// safeID keeps only [A-Za-z0-9._:#-] so an id or slug can never smuggle
// text.
func safeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == ':', r == '#':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 80 {
			break
		}
	}
	return b.String()
}

// bodyField reads one string field of an event body ("" when absent or
// unreadable).
func bodyField(body json.RawMessage, field string) string {
	var b map[string]any
	if len(body) == 0 || json.Unmarshal(body, &b) != nil {
		return ""
	}
	s, _ := b[field].(string)
	return s
}
