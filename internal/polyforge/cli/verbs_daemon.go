package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ---------------------------------------------------------------------------
// submit — interactive submit of one WI (a plain claim).
// ---------------------------------------------------------------------------

var cmdSubmit = &Command{
	Name:    "submit",
	Group:   GroupExec,
	Summary: "Submit a WI for execution on this machine (claim)",
	Usage:   "pf submit <id> [--priority low|normal|high|urgent] [--models <harness>:<model>[:<effort>],...] [--allow-no-acceptance]",
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("allow-no-acceptance", false, "submit an execution ticket whose content lists no acceptance criterion (under `## Acceptance` or `## 验收`) on a lane that requires acceptance; without it such a ticket is refused")
		fs.String("priority", "normal", "queue priority (low|normal|high|urgent)")
		fs.String("models", "", "manual model candidates for EVERY step of this WI, comma-separated, in order; "+
			"each is <harness>:<model>[:<effort>] with harness pi or claude, e.g. claude:claude-opus-5-5:high,pi:example-gateway/model-a "+
			"(the prefix is mandatory; a candidate on another harness than the previous one starts a fresh session; "+
			"effort is pi off|minimal|low|medium|high|xhigh|max (--thinking) or claude low|medium|high|xhigh|max (--effort), omitted = the harness default) "+
			"(precedence: --models > workspace yaml steps.<project>.<step-id>.models > bundle step model_candidates > "+
			"yaml steps.<project>.default.models > bundle defaults.model_candidates)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("submit: expected exactly one work item id")
		}
		var models []string
		if fs.Lookup("models").Value.String() != "" {
			var merr error
			models, merr = workspace.NormalizeModels(strings.Split(stringFlag(fs, "models"), ","))
			if merr != nil {
				return fmt.Errorf("submit: --models: %w", merr)
			}
		}
		name, wi, err := submitPrecheck(ctx, args[0])
		if err != nil {
			return err
		}
		if wi != nil && !boolFlag(fs, "allow-no-acceptance") {
			if err := submitAcceptanceCheck(ctx, wi, name); err != nil {
				return err
			}
		}
		raw, err := ctx.daemon().ClaimWithModels(args[0], stringFlag(fs, "priority"), models)
		if err != nil {
			return submitRefusal(ctx, name, err)
		}
		finishOrRaw(ctx, raw, func(sr *SubmitResponse) bool {
			if !sr.Accepted {
				return false
			}
			fmt.Fprintf(ctx.Stdout, "submitted %s (accepted by the local daemon)\n", name)
			// The placement (the claim's target machine
			// + capsule landing path) when the daemon resolved it, then one
			// warning line naming every repo whose clone is missing from
			// the mapped workspace — the claim clones fresh, so the submit
			// stays accepted; no override flag exists. An unknown placement
			// prints nothing beyond the acceptance line (nothing invented).
			// The daemon states the placement on a claimed answer only
			//; a queued one says why it waits instead.
			if sr.MachineID != "" && sr.CapsulePath != "" {
				fmt.Fprintf(ctx.Stdout, "placement: claimed by machine %s, capsule %s\n", sr.MachineID, sr.CapsulePath)
			}
			if sr.State == SubmitStateQueued {
				fmt.Fprintf(ctx.Stdout, "queued: %s — nothing is claimed yet; follow it, and any later refusal, with `pf watch %s`\n", sr.QueuedReason, name)
			}
			if len(sr.MissingRepos) > 0 {
				fmt.Fprintf(ctx.Stdout, "warning: no local clone under .repo/ for repo(s) %s — the claim clones fresh\n", strings.Join(sr.MissingRepos, ", "))
			}
			return true
		})
		return nil
	},
}

// submitPrecheck asks aihub — with the CALLER's own api key, not the
// daemon's machine key — for the work item before the local daemon is
// involved: a typed CORE-404 (unknown or hidden id) and an explicit
// terminal status refuse early; every other read outcome defers to the
// daemon's claim, which stays the authority. It answers the work item's
// display name (its slug; the given spelling when the read failed) and
// the work item it read (nil when the read failed).
func submitPrecheck(ctx *Context, wiID string) (string, *api.WorkItem, error) {
	wi, err := ctx.aihub().GetWorkItem(wiID)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.ErrCoreNotFound {
			return "", nil, err
		}
		return wiID, nil, nil
	}
	name := api.DisplayName(wi.Slug, wiID)
	switch wi.Status {
	case api.StatusWrapped, api.StatusFailed, api.StatusCanceled:
		return "", nil, fmt.Errorf("submit: %s is terminal (%s); no claim or resume can start", name, wi.Status)
	}
	return name, wi, nil
}

// submitRefusal states aihub's typed claim refusal (a CORE- code the
// daemon passed through,) as a failed submit: the same
// code and extra, a detail that says nothing was submitted or kept
// queued, and — for an unmerged blocker (DEPENDENCY_BLOCKED) — the
// blockers and what to do. A new *api.Error rather than a %w wrap:
// renderError prints the innermost *api.Error only. Any other error is
// returned as is.
func submitRefusal(ctx *Context, name string, err error) error {
	var ae *api.Error
	if !errors.As(err, &ae) || !strings.HasPrefix(ae.Code, "CORE-") {
		return err
	}
	detail := fmt.Sprintf("%s was not submitted — aihub refused the claim; nothing stays queued: %s", name, ae.Detail)
	var extra struct {
		Reason string `json:"reason"`
	}
	if len(ae.Extra) > 0 && json.Unmarshal(ae.Extra, &extra) == nil && extra.Reason == "DEPENDENCY_BLOCKED" {
		detail += "; " + submitBlockedGuidance(ctx, name)
	}
	return &api.Error{Code: ae.Code, Detail: detail, Extra: ae.Extra}
}

// submitBlockedGuidance names the unmerged blockers of name, read with
// the caller's own key (its waiting_for list), and what to do; the
// generic guidance alone when the read fails or names none.
func submitBlockedGuidance(ctx *Context, name string) string {
	blocker := "<blocker>"
	waiting := ""
	if raw, err := ctx.aihub().GetWorkItemRaw(name); err == nil {
		var v WIView
		if json.Unmarshal(raw, &v) == nil && len(v.WaitingFor) > 0 {
			waiting = "waiting for " + waitingText(v.WaitingFor) + " — "
			if len(v.WaitingFor) == 1 {
				blocker = v.WaitingFor[0].Slug
			}
		}
	}
	return fmt.Sprintf("%swait until the blocker is merged (wrapped), then pf submit %s again, or remove the edge: pf file dep rm %s %s",
		waiting, name, blocker, name)
}

// submitAcceptanceCheck refuses an execution ticket (one with a bundle)
// whose content lists no acceptance criterion (workflow.HasAcceptance,
// the driver's own rule) while its lane requires one (the lane's
// `acceptance` declaration; a lane that cannot be read requires one): it
// needs interaction first. A registration ticket is left to the daemon.
func submitAcceptanceCheck(ctx *Context, wi *api.WorkItem, name string) error {
	if wi.BundleName == nil || strings.TrimSpace(*wi.BundleName) == "" {
		return nil
	}
	content := ""
	if wi.Content != nil {
		content = *wi.Content
	}
	if workflow.HasAcceptance(content) || !laneOf(ctx, wi.ProjectName, *wi.BundleName).RequiresAcceptance() {
		return nil
	}
	return fmt.Errorf("submit: %s has no acceptance section (no criterion listed under `## Acceptance` or `## 验收`), "+
		"and its lane requires one (workflow.yaml `acceptance`, default required), so it was not submitted.\n"+
		"This ticket needs interaction first: settle its acceptance in the session and write it into the ticket "+
		"(pf file edit %s --content @file), then submit.\n"+
		"To submit it without acceptance anyway: pf submit %s --allow-no-acceptance", name, name, name)
}

// wiDisplayName answers the display name (slug) of a work item for a
// verb's human output, read with the caller's key; the given spelling
// when the read fails — the output never blocks on it.
func wiDisplayName(ctx *Context, idOrSlug string) string {
	wi, err := ctx.aihub().GetWorkItem(idOrSlug)
	if err != nil || wi == nil {
		return idOrSlug
	}
	return api.DisplayName(wi.Slug, idOrSlug)
}

// ---------------------------------------------------------------------------
// answer — speak to the current step's agent (workflow contract v2 §5.2):
// `pf answer <id> --text "<words>"` at any time — the daemon records the
// message and relays it into the step's conversation (a reply while the
// step waits on its `pf ask`, a message while it runs). There is no
// question id, option or reserved word (the v1 question's answer left
// with workflow contract v2 change 19; `pf cancel` cancels).
//
// A parked work item (blocked or paused) is woken by the message
// (workflow contract 4, design #483 §7): the daemon names the park, this
// command opens the door `pf resume` opens with the caller's own key, and
// the daemon then records the message and wakes the step the park belongs
// to. A park whose agent cannot start is refused, naming why and `pf
// resume` / `pf cancel`.
// ---------------------------------------------------------------------------

var cmdAnswer = &Command{
	Name:    "answer",
	Group:   GroupExec,
	Summary: "Speak to a WI's current step agent (reply to its question, interject, or wake a parked WI)",
	Usage:   "pf answer <id> --text \"...\" [--by user]",
	Setup: func(fs *flag.FlagSet) {
		fs.String("text", "", "your words, verbatim (required)")
		fs.String("by", "", "answerer user id (default: config user_id / PF_USER_ID)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("answer: expected exactly one work item id")
		}
		text := stringFlag(fs, "text")
		if text == "" {
			return fmt.Errorf("answer: --text is required")
		}
		who := stringFlag(fs, "by")
		if who == "" {
			who = ctx.Cfg.UserID
		}
		if who == "" {
			return fmt.Errorf("answer: answered_by identity required (--by or PF_USER_ID / user_id in config)")
		}
		raw, err := ctx.daemon().Answer(args[0], AnswerPayload{AnswerText: text, AnsweredBy: who})
		if err != nil {
			return err
		}
		var ack protocol.OwnerMessageAck
		_ = json.Unmarshal(raw, &ack)
		if ack.Status == ackParked {
			// the owner's door first, with the caller's key; the daemon
			// never reopens a parked work item at aihub on its own
			if err := openParkDoor(ctx, args[0], ack.Park); err != nil {
				fmt.Fprintf(ctx.Stderr, "answer: %s is parked %s; opening its door was refused, so nothing was recorded:\n", args[0], ack.Park)
				return err
			}
			raw, err = ctx.daemon().Answer(args[0], AnswerPayload{AnswerText: text, AnsweredBy: who, Wake: ack.Park})
			if err != nil {
				fmt.Fprintf(ctx.Stderr, "answer: %s's %s door is open, but the wake did not complete:\n", args[0], ack.Park)
				return err
			}
			ack = protocol.OwnerMessageAck{}
			_ = json.Unmarshal(raw, &ack)
		}
		ctx.finishLine(raw, "%s\n", ownerMessageLine(ack))
		return nil
	},
}

// ackParked is the answer's status for a parked work item: nothing was
// recorded; the caller opens the park's door and asks again naming it.
const ackParked = "parked"

// openParkDoor opens, with the caller's own key, the door `pf resume`
// opens for the park: a blocked work item's resume-blocked door at aihub
// (its reporter / maintainer / admin check is about the human), a paused
// one's clean_pause cleared. The daemon's claim follows in the wake
// request.
func openParkDoor(ctx *Context, wiID, park string) error {
	switch park {
	case "blocked":
		_, err := ctx.aihub().ResumeBlockedWorkItemWith(wiID, api.ResumeBlockedRequest{})
		return err
	case "paused":
		return patchCleanPause(ctx, wiID, false)
	}
	return fmt.Errorf("answer: the daemon named an unknown park %q of %s", park, wiID)
}

// ownerMessageLine renders the relay's answer: what was recorded, for
// which step and dispatch, and how far the delivery got.
func ownerMessageLine(a protocol.OwnerMessageAck) string {
	what := "message"
	if a.Kind == "reply" {
		what = "reply to ask " + a.AskAt
	}
	head := fmt.Sprintf("%s recorded for step %s (dispatch %d, at %s): ", what, a.Step, a.SpawnN, a.At)
	switch {
	case a.Disposition == "wake":
		return head + "the parked work item is woken — " + wakeLine(a.Wake)
	case a.Delivered:
		return head + "delivered to the step's agent"
	case a.Disposition == "resume" && a.Note != "":
		return head + "the step process did not take it (" + a.Note + "); the step's next dispatch carries it"
	case a.Disposition == "resume":
		return head + "no step process is alive; the daemon resumes the session to deliver it"
	case a.Disposition == "queued":
		return head + "queued by the step process (a turn is running); delivered at the turn's next boundary"
	default:
		return head + "accepted by the step process; delivery confirmation pending"
	}
}

// wakeLine says how a woken park continues (protocol.OwnerMessageAck.Wake).
func wakeLine(wake string) string {
	switch wake {
	case "step_blocked":
		return "the step that stopped resumes its session with your message"
	case "write_failed":
		return "its failed hub writes are replayed, then it continues"
	case "paused":
		return "the paused step continues in place with your message"
	default:
		return "the parked step is dispatched again with your message"
	}
}

// ---------------------------------------------------------------------------
// pause — operator pause of a running WI: the daemon closes the job
// locally AND the hub record is marked clean_pause=true so a daemon
// restart never auto-resumes an operator-paused work item (M3).
// ---------------------------------------------------------------------------

var cmdPause = &Command{
	Name:    "pause",
	Group:   GroupExec,
	Summary: "Pause a running WI at the next safe boundary (operator pause: clean_pause on the hub)",
	Usage:   "pf pause <id>",
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("pause: expected exactly one work item id")
		}
		// Merge amend: the early hub PATCH + undo dance is GONE — the
		// pause is one atomic fenced transition now. The daemon's pause
		// close carries Status=paused + CleanPause=true on the SAME
		// SessionEnd that flips the status, so there is no crash window
		// in which an operator-paused WI could be auto-resumed, and no
		// undo path that could strand a stale marker on a non-paused WI.
		raw, err := ctx.daemon().Pause(args[0])
		if err != nil {
			return err
		}
		ctx.finishLine(raw, "pause requested for %s (clean_pause set on the hub; restart will not auto-resume it)\n", closeName(raw, args[0]))
		return nil
	},
}

// closeName answers the display name a pause/wrap answer carries (the
// daemon's job-record slug), else the operator's own spelling.
func closeName(raw []byte, arg string) string {
	var ans struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(raw, &ans)
	return api.DisplayName(ans.Slug, arg)
}

// patchCleanPause PATCHes the work item's clean_pause field (a
// patchable field: the operator resume face, reference.md §3.3).
func patchCleanPause(ctx *Context, wiID string, value bool) error {
	_, err := ctx.aihub().PatchWorkItem(wiID, map[string]any{"clean_pause": value})
	return err
}

// ---------------------------------------------------------------------------
// resume — one verb, two doors (polyforge#116). The hub status picks the
// door:
//
//   - a workflow parked BLOCKED on an honest incomplete result (§6.5
//     step 5: workflow_status=blocked, position frozen, the owner holds
//     the next move) gets the §6.4 owner move: the CLI lands the hub's
//     resume-blocked door with the human's own credential (authorization
//     is reporter / maintainer / admin, or the blocked-only holder
//     operator: the holding machine's current operator, a project writer
//     or maintainer), then the daemon's route claims
//     it and re-establishes execution over the parked capsule — the frozen current step
//     re-dispatches in place as a fresh derivation. It must never answer
//     the pause copy again (nothing here was ever paused).
//   - every other status keeps the operator-pause door — with the
//     live-holder continuation of wi_seWTgHeKtaa8 (v2-converge §7): the
//     CLI still PATCHes clean_pause=false on the hub (the resume's
//     hub-side effect), but then TELLS the local daemon (the
//     resume-paused route). When this machine's daemon is the paused
//     work item's holder it re-validates the session (hub: session
//     matches, status paused, clean_pause cleared), re-attaches it
//     (SessionAttach) and continues the current step IN PLACE — the
//     exact semantics attaching after a restart has, the pending-question
//     re-pose included. A daemon that does not hold the work item
//     (DAEMON-404) or an unreachable socket keeps the holder-machine
//     guidance (end-session + submit when the holder is gone); a typed
//     mid-flight failure answers BLOCKED (the missing-capsule /
//     unmapped-project copy), never "reattaches at next start".
// ---------------------------------------------------------------------------

// daemonExecutionDisabled reads the addressed Cell's execution_disabled
// reason from GET /status ("" = open, or the status is unreadable).
func daemonExecutionDisabled(ctx *Context) string {
	raw, err := ctx.daemon().Status()
	if err != nil {
		return ""
	}
	var st StatusResponse
	if json.Unmarshal(raw, &st) != nil {
		return ""
	}
	return st.ExecutionDisabled
}

var cmdResume = &Command{
	Name:    "resume",
	Group:   GroupExec,
	Summary: "Continue a paused WI (clean_pause=false) or a blocked one (reporter, maintainer, admin, or the holding machine's operator as a writer); abandon with pf cancel",
	Usage:   "pf resume <id> [--accept-lost-work]",
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("accept-lost-work", false, "blocked WI only: accept losing the work the sealing machine never pushed, so any machine continues from aihub's chain (clears the blocked seal; recorded as a blocked_seal_abandoned event)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("resume: expected exactly one work item id")
		}
		if _, _, err := submitPrecheck(ctx, args[0]); err != nil {
			return err
		}
		wi, err := ctx.aihub().GetWorkItem(args[0])
		if err != nil {
			return err
		}
		name := api.DisplayName(wi.Slug, args[0])
		acceptLostWork := boolFlag(fs, "accept-lost-work")
		if acceptLostWork && wi.Status != api.StatusBlocked {
			return fmt.Errorf("resume: --accept-lost-work applies only to a blocked work item; %s is %s", name, wi.Status)
		}
		// Dispatch order, decided by WI state (v2 merge of D1 + D2):
		//  1. hub status blocked  -> the human-key resume-blocked door, then
		//     the daemon's claim/start (below).
		//  2. otherwise (paused) -> clear clean_pause on the hub, then tell
		//     the local daemon: a holder that is live continues IN PLACE
		//     (wi_seWTgHeKtaa8, v2-converge §7) — the resume must be
		//     observable by the online holder, not only by the startup
		//     reconciliation.
		// end_pending (D2) is a distinct local state with no door of its own:
		// the daemon retries its SessionEnd itself.
		if wi.Status != api.StatusBlocked {
			// An execution-disabled Cell refuses the resume before the hub
			// flag is touched: its execution_disabled reason is read from
			// GET /status (the same routing as the resume itself), so
			// clean_pause stays set and the work item stays paused. An
			// unreadable status (an unreachable socket) keeps the flow
			// below; the disabled Cell's restart reconciliation keeps the
			// record paused anyway.
			if why := daemonExecutionDisabled(ctx); why != "" {
				return &api.Error{Code: api.ErrDaemonConflict, Detail: "execution is disabled on this Cell: " + why +
					"; nothing was changed — clean_pause stays set at the hub and " + name + " stays paused; run pf resume again once this Cell admits it"}
			}
			if err := patchCleanPause(ctx, args[0], false); err != nil {
				return err
			}
			res, err := ctx.daemon().ResumePaused(args[0])
			if err != nil {
				var ae *api.Error
				if errors.As(err, &ae) && ae.Code == api.ErrDaemonNotFound {
					// the hub flag is cleared and stays cleared: the holder
					// machine's daemon continues on ITS operator resume, or on
					// its next start (the restart reconciliation — the working
					// fallback that has always existed).
					fmt.Fprintf(ctx.Stdout, "resumed %s: clean_pause cleared; this machine's daemon does not hold the paused session\n", name)
					fmt.Fprintln(ctx.Stdout, "(resume it on the holder machine's daemon to continue in place; a work item whose holder is gone moves with `pf ops end-session <id> --reason ...` then `pf submit <id>`)")
					return nil
				}
				if !errors.As(err, &ae) {
					// the daemon socket is unreachable (a transport error, no
					// typed envelope): the flag clear stands at the hub; the
					// holder daemon picks the resume up at its next start.
					fmt.Fprintf(ctx.Stdout, "resumed %s: clean_pause cleared; the daemon socket is unreachable (%v)\n", name, err)
					fmt.Fprintln(ctx.Stdout, "(once the holder daemon starts, its restart reconciliation attaches the paused session — unless its Cell's execution is disabled, which keeps it paused until pf resume is run again once the Cell admits it; to continue without it use `pf ops end-session <id> --reason ...` then `pf submit <id>`)")
					return nil
				}
				// typed refusal — including the mid-flight block: a missing
				// capsule / unmapped project answers blocked (the typed
				// cause names the remediation). The hub side already
				// succeeded, so the answer says so (same code, so the exit
				// status and --json envelope are unchanged).
				return &api.Error{Code: ae.Code, Extra: ae.Extra, Detail: ae.Detail +
					"; the hub side succeeded (clean_pause is cleared) — the daemon refused the in-place resume, so the holder daemon's next start reconciles it (it resumes, unless the refusal above blocked the work item or names a pending close, which is replayed instead)"}
			}
			raw, _ := json.Marshal(res)
			ctx.finish(raw, func() {
				if res.Outcome == "already_live" {
					fmt.Fprintf(ctx.Stdout, "resumed %s: the work item is already running on this daemon\n", name)
					return
				}
				if res.Outcome == "parked" {
					fmt.Fprintf(ctx.Stdout, "resumed %s: clean_pause cleared; the holder daemon re-attached the paused session, but its recovery is parked — the previous driver Run did not stop within its teardown bound, so nothing continues next to it yet; it continues by itself once that Run exits\n", name)
					fmt.Fprintln(ctx.Stdout, "(`pf watch "+args[0]+" --once` shows recovery_parked or close_parked; a daemon restart also continues it)")
					return
				}
				if res.Outcome == "closed" {
					fmt.Fprintf(ctx.Stdout, "resumed %s: clean_pause cleared; the holder daemon re-attached the paused session, but its recovery closed the work item (terminal verdict or exhausted relaunch) — it does not continue in place\n", name)
					fmt.Fprintln(ctx.Stdout, "(see `pf watch "+args[0]+" --once` for the close)")
					return
				}
				fmt.Fprintf(ctx.Stdout, "resumed %s: clean_pause cleared; the holder daemon re-attached the paused session and continues the current step in place\n", name)
				fmt.Fprintln(ctx.Stdout, "(a pending question is re-posed and waits for your answer; watch it with `pf watch "+args[0]+" --once`)")
			})
			return nil
		}
		return resumeBlockedWorkItem(ctx, args[0], name, wi, acceptLostWork)
	},
}

// resumeBlockedWorkItem is `pf resume` of a blocked work item (hub
// status blocked). The authorization-bearing step runs with the HUMAN's own api key (data
// plane, like `pf cancel`): the hub's resume-blocked door checks reporter /
// project maintainer / admin against the caller, and the daemon's machine
// key is not that caller. Only then does the local daemon claim and start
// the WI.
func resumeBlockedWorkItem(ctx *Context, wiID, name string, wi *api.WorkItem, acceptLostWork bool) error {
	door, err := ctx.aihub().ResumeBlockedWorkItemWith(wiID, api.ResumeBlockedRequest{AcceptLostWork: acceptLostWork})
	if err != nil {
		return err
	}
	if acceptLostWork {
		// Judge the override by the hub's answer, not the earlier read:
		// a hub that ignores accept_lost_work leaves the seal standing.
		var after struct {
			Attrs map[string]any `json:"attrs"`
		}
		_ = json.Unmarshal(door, &after)
		if _, still := after.Attrs["blocked_seal"]; still {
			return fmt.Errorf("resume: aihub did not clear the blocked seal of %s (the hub is too old for --accept-lost-work); the work item is queued again but a continue on another machine is still refused (DAEMON-518) — upgrade aihub, or continue on the sealing machine", name)
		}
		seal, _ := wi.Attrs["blocked_seal"].(map[string]any)
		if head, _ := seal["head"].(string); head != "" {
			fmt.Fprintf(ctx.Stderr, "accepted lost work on %s: the blocked seal (capsule head %s on machine %v) is cleared; the work it never pushed is abandoned\n", name, head, seal["machine"])
		}
	}
	raw, err := ctx.daemon().ResumeBlocked(wiID)
	if err != nil {
		return err
	}
	var ans struct {
		Deferred bool `json:"deferred"`
	}
	_ = json.Unmarshal(raw, &ans)
	ctx.finish(raw, func() {
		if ans.Deferred {
			fmt.Fprintf(ctx.Stdout, "resumed %s: the hub is unreachable right now; the claim is queued in the daemon and will run when the hub is reachable (retried with backoff; watch it with `pf watch <id> --once`)\n", name)
			return
		}
		fmt.Fprintf(ctx.Stdout, "resumed %s: the workflow was parked blocked (incomplete result) — the §6.4 owner move re-established execution over the parked capsule\n", name)
		fmt.Fprintln(ctx.Stdout, "(the frozen current step re-dispatches in place as a fresh derivation; watch it with `pf watch <id> --once`)")
	})
	return nil
}

// ---------------------------------------------------------------------------
// wrap — wrap (or fail) a running WI via the daemon.
// ---------------------------------------------------------------------------

var cmdWrap = &Command{
	Name:    "wrap",
	Group:   GroupExec,
	Summary: "Wrap a running WI, or a WI with no bundle, as terminal success (--fail = the owner's failed verdict; to abandon a blocked WI use pf cancel)",
	Usage:   "pf wrap <id> [--fail] [--note TEXT]",
	Setup: func(fs *flag.FlagSet) {
		fs.Bool("fail", false, "end the session with reason=fail instead of wrap (the owner's failed verdict; to abandon a blocked WI use pf cancel)")
		fs.String("note", "", "why it is done; recorded by aihub when it wraps a work item with no bundle")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("wrap: expected exactly one work item id")
		}
		reason := "wrap"
		if boolFlag(fs, "fail") {
			reason = "fail"
		}
		// A WI parked blocked has no hub door for a failed verdict: the
		// blocked exits are continue (`pf resume`) and abandon
		// (`pf cancel`). The daemon refuses `wrap` and `wrap --fail` on a
		// parked-blocked WI with DAEMON-409 naming those two moves; when it
		// cannot tell (no local job, DAEMON-404) `--fail` prints the same
		// owner-move hint below and still exits non-zero.
		raw, err := ctx.daemon().Wrap(args[0], reason)
		if err != nil {
			var ae *api.Error
			// No daemon here, or no local job: the work item may be a
			// ticket with no bundle. Any other daemon answer stands.
			if !errors.As(err, &ae) || ae.Code == api.ErrDaemonNotFound {
				if done, herr := hubWrapNoBundle(ctx, args[0], reason == "fail", stringFlag(fs, "note")); done {
					return herr
				}
			}
			if reason == "fail" && errors.As(err, &ae) && ae.Code == api.ErrDaemonNotFound {
				// A WI parked blocked has no
				// local job to fail. The owner's moves are exactly two —
				// continue with `pf resume`, abandon with `pf cancel` — and
				// this command does neither: it says so, touches nothing
				// (no hub door, no termination) and fails — the wrap did
				// not happen, so the exit is the typed DAEMON-404's.
				if !ctx.JSON {
					fmt.Fprintf(ctx.Stderr, "%s has no running job on this daemon; if it is parked blocked, continue it with `pf resume %s` or abandon it with `pf cancel %s`\n", args[0], args[0], args[0])
				}
				return err
			}
			return err
		}
		ctx.finishLine(raw, "%s requested for %s\n", reason, closeName(raw, args[0]))
		if stringFlag(fs, "note") != "" {
			fmt.Fprintln(ctx.Stderr, "note not recorded: --note is kept only when aihub wraps a work item with no bundle; this wrap went through the daemon")
		}
		return nil
	},
}

// hubWrapNoBundle is wrap's second path: a work item with no bundle (a
// design, decision or tracking ticket) never has a daemon job, so when
// the daemon is unreachable or has no local job the CLI asks aihub, and
// when the work item is open and has no bundle, aihub records the verdict. done=false
// leaves the daemon's error standing (an execution ticket, a lookup
// failure, a step). A step never takes this path: step agents hold no
// aihub access.
func hubWrapNoBundle(ctx *Context, id string, fail bool, note string) (done bool, err error) {
	if ctx.Step != "" {
		return false, nil
	}
	hub := ctx.aihub()
	wi, gerr := hub.GetWorkItem(id)
	if gerr != nil || (wi.BundleName != nil && *wi.BundleName != "") {
		return false, nil
	}
	switch wi.Status {
	case api.StatusQueued, api.StatusPaused, api.StatusBlocked:
	default:
		return false, nil
	}
	raw, err := hub.WrapWorkItem(id, api.WrapRequest{Fail: fail, Note: note})
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == "HTTP-404" {
			return true, &api.Error{Code: ae.Code, Detail: fmt.Sprintf(
				"%s has no bundle, so aihub wraps it, but this aihub has no wrap route for it (older than this pf) — upgrade aihub first, then retry", api.DisplayName(wi.Slug, id))}
		}
		return true, err
	}
	finishAs(ctx, raw, func(w *api.WorkItem) {
		fmt.Fprintf(ctx.Stdout, "%s closed at aihub (no bundle): status %s\n", api.DisplayName(w.Slug, id), w.Status)
	})
	return true, nil
}

// ---------------------------------------------------------------------------
// ops — the daemon ops face: status / auto / max / shutdown.
// ---------------------------------------------------------------------------

var cmdOps = &Command{
	Name:    "ops",
	Group:   GroupExec,
	Summary: "Daemon ops: local job status, the read-only model profile, auto-pull switch, max concurrent jobs, graceful shutdown; end-session recovery",
	Usage: "pf ops status\n" +
		"pf ops models           # read-only model+effort x step profile over the events-retention window; --json re-emits GET /models\n" +
		"pf ops auto [on|off]    # off = only explicit pf submit; on = the daemon also pulls the ready queue itself (bare = show)\n" +
		"pf ops max [N]          # max concurrent jobs on this machine, N >= 1; lasts until the daemon restarts, and a `pf config set`/`pf config reload` that changes [daemon] max_concurrent_jobs overrides it (last action wins); bare = show\n" +
		"pf ops role [interactive|runner] [--force|--if-unset]  # the machine role the heartbeat negotiates: interactive (the operator's machine; --force takes it over from the machine holding it) or runner; --if-unset applies only while no operator chose a role on this Cell (the choice is kept across restarts) (the session-start adapters use it); bare = show; the Cell resolves like every Cell-scoped command (--profile, else PF_PROFILE, else the cwd's workspace, else default)\n" +
		"pf ops shutdown         # graceful shutdown (quiesce); the restart loop ends with it — start again with `pf daemon ensure`\n" +
		"pf ops end-session <wi> --reason <text>  # admin/maintainer: force-end a stuck holder's session (WI -> queued; then `pf submit <wi>` anywhere)",
	Setup: func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("ops: expected status|models|auto|max|role|shutdown|end-session")
		}
		if args[0] == "end-session" {
			return opsEndSession(ctx, args[1:])
		}
		// ops is Host-scoped: status/max/auto address the Host aggregate
		// unless an explicit --profile targets one Cell (PF_PROFILE and the
		// cwd never change a Host command's scope); shutdown is always the
		// whole Host and refuses a profile.
		if args[0] == "shutdown" && ctx.FlagProfile != "" {
			return fmt.Errorf("ops shutdown: shutdown is Host-level — it stops every Cell of the Host and takes no --profile (got %q); run `pf ops shutdown` alone", ctx.FlagProfile)
		}
		d := ctx.daemon()
		d.Profile = ctx.FlagProfile
		switch args[0] {
		case "status":
			raw, err := d.Status()
			if err != nil {
				return err
			}
			finishOrRaw(ctx, raw, func(s *StatusResponse) bool {
				renderStatus(ctx.Stdout, s)
				return true
			})
			return nil
		case "models":
			if len(args) != 1 {
				return fmt.Errorf("ops models: unexpected arguments %v", args[1:])
			}
			raw, err := d.Models()
			if err != nil {
				return err
			}
			finishOrRaw(ctx, raw, func(p *modelhealth.Profile) bool {
				renderModels(ctx.Stdout, p)
				return true
			})
			return nil
		case "auto":
			return opsAuto(ctx, d, args[1:])
		case "max":
			return opsMax(ctx, d, args[1:])
		case "role":
			// the machine role is a Cell's, resolved like every Cell-scoped
			// command (not the Host aggregate the other ops address)
			return opsRole(ctx, ctx.daemon(), args[1:])
		case "shutdown":
			raw, err := d.Shutdown()
			if err != nil {
				return err
			}
			ctx.finishLine(raw, "graceful shutdown requested; the daemon quiesces and exits\n")
			return nil
		default:
			return fmt.Errorf("ops: unknown action %q", args[0])
		}
	},
}

// opsShow answers a bare `pf ops auto|max` from GET /status.
func opsShow(ctx *Context, d *DaemonClient, render func(s *StatusResponse) string) error {
	raw, err := d.Status()
	if err != nil {
		return err
	}
	finishOrRaw(ctx, raw, func(s *StatusResponse) bool {
		fmt.Fprintln(ctx.Stdout, render(s))
		return true
	})
	return nil
}

func opsApply(ctx *Context, d *DaemonClient, req OpsConfigRequest) error {
	raw, err := d.OpsConfig(req)
	if err != nil {
		return err
	}
	ctx.finish(raw, func() { printJSON(ctx.Stdout, raw) })
	return nil
}

// opsAuto handles `pf ops auto [on|off]`.
func opsAuto(ctx *Context, d *DaemonClient, args []string) error {
	switch len(args) {
	case 0:
		return opsShow(ctx, d, func(s *StatusResponse) string { return "auto: " + onOff(s.Auto) })
	case 1:
		switch args[0] {
		case "on", "off":
			on := args[0] == "on"
			return opsApply(ctx, d, OpsConfigRequest{Auto: &on})
		}
	}
	return fmt.Errorf("ops auto: expected on|off")
}

// opsMax handles `pf ops max [N]`.
func opsMax(ctx *Context, d *DaemonClient, args []string) error {
	switch len(args) {
	case 0:
		return opsShow(ctx, d, func(s *StatusResponse) string {
			return fmt.Sprintf("max: %d", s.Queue.MaxConcurrent)
		})
	case 1:
		if args[0] == "--reset" {
			return fmt.Errorf("ops max: --reset was removed; the cap returns to cli.toml's [daemon] max_concurrent_jobs on daemon restart")
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 {
			return fmt.Errorf("ops max: N must be an integer >= 1")
		}
		return opsApply(ctx, d, OpsConfigRequest{Max: &n})
	}
	return fmt.Errorf("ops max: expected a single N")
}

// opsRole handles `pf ops role [interactive|runner] [--force]`
// the machine-role move that used to ride `pf chat
// init`. Cell-scoped: --profile, else PF_PROFILE, else the cwd's
// workspace, else the default Cell (the resolver every Cell-scoped
// command uses).
func opsRole(ctx *Context, d *DaemonClient, args []string) error {
	if d.Profile == "" {
		d.Profile = cellcfg.DefaultProfile
	}
	force, ifUnset := false, false
	var rest []string
	for _, a := range args {
		switch a {
		case "--force":
			force = true
			continue
		case "--if-unset":
			ifUnset = true
			continue
		}
		rest = append(rest, a)
	}
	if force && ifUnset {
		return fmt.Errorf("ops role: --if-unset never takes the role over — drop --force or --if-unset")
	}
	if len(rest) == 0 {
		switch {
		case force:
			return fmt.Errorf("ops role: --force needs a role (pf ops role interactive --force)")
		case ifUnset:
			return fmt.Errorf("ops role: --if-unset needs a role (pf ops role interactive --if-unset)")
		}
		return opsShow(ctx, d, func(s *StatusResponse) string {
			role, _ := s.MachineFace["role"].(string)
			if role == "" {
				role = "unknown (the cross-machine face is not available)"
			}
			line := "role: " + role
			if by, _ := s.MachineFace["superseded_by"].(string); by != "" {
				line += " (superseded by " + by + ")"
			}
			return line
		})
	}
	if len(rest) != 1 || (rest[0] != "interactive" && rest[0] != "runner") {
		return fmt.Errorf("ops role: expected interactive|runner [--force]")
	}
	if force && rest[0] != "interactive" {
		return fmt.Errorf("ops role: --force takes the interactive role over; it does not apply to runner")
	}
	raw, err := d.MachineRole(rest[0], force, ifUnset)
	if err != nil {
		return err
	}
	var ans struct {
		Applied *bool `json:"applied"`
	}
	_ = json.Unmarshal(raw, &ans)
	msg := fmt.Sprintf("role %s requested for Cell %s; the next heartbeat negotiates it\n", rest[0], d.Profile)
	if ans.Applied != nil && !*ans.Applied {
		msg = fmt.Sprintf("Cell %s keeps the role an operator chose; %s not requested (--if-unset)\n", d.Profile, rest[0])
	}
	if force {
		msg = fmt.Sprintf("interactive role takeover requested for Cell %s; the next heartbeat supersedes the current holder\n", d.Profile)
	}
	ctx.finishLine(raw, "%s", msg)
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ---------------------------------------------------------------------------
// daemon — help-only CLI registration. The daemon IS assembled: cmd/pf/
// main.go intercepts `pf daemon` argv before CLI dispatch and calls
// internal/polyforge/daemon.Main directly. This command registration
// exists only so `pf help daemon` presents the real launch surface; its
// Run is unreachable in a correct build and merely reprints the help.
// ---------------------------------------------------------------------------

// daemonUsage is the help text for `pf help daemon`: the ensure and
// mapping semantics, then the launch flags rendered from
// DefineDaemonFlags — the flag set daemon.Main parses.
var daemonUsage = `pf daemon ensure [--config PATH] [--workflows-dir DIR] [--timeout D]
pf daemon [--flag value] ...

pf daemon ensure (polyforge#174) is how the daemon is started on a work
machine — by tools/install.sh, and by the harness adapters (pi's pf-chat
extension, the Claude Code SessionStart hook) when a human's session
starts or resumes. Idempotent: a daemon answering on the config's
daemon_socket means nothing to do (exit 0). Otherwise it starts the
restart loop in tmux session polyforge-daemon (pf daemon <canonical
argv>; a failed exit — crash, panic, kill — is restarted after 5s, a
clean exit — pf ops shutdown, SIGTERM — ends the loop, and so does exit
78, the daemon refusing to start for an operator-fixable reason (config,
daemon id, workspace, on-disk state, failed restart reconciliation): fix
the cause named in the log, then run pf daemon ensure again; the log is
<data-dir>/daemon.log) and waits up to
--timeout (default 30s) for the socket: exit 0 when it answers, 1 when
not. Concurrent calls are serialized by <data-dir>/ensure.lock: one
loop, one daemon (tools/install.sh holds the same lock during an
upgrade). Refused inside a step agent (PF_WI_ID set). Without tmux it starts one unsupervised daemon and
warns. The canonical argv: --config, -machine-id (the recorded
<data-dir>/daemon-id, else m-<short hostname>), -data-dir
($POLYFORGE_DATA_DIR or ~/.local/share/polyforge), -socket (the
config's daemon_socket), -workflows-dir (<data-dir>/release/current/
workflows unless --workflows-dir) — nothing else. The production knobs
live in the config's [daemon] table, which the daemon reads itself from
--config (decision 2026-10-16 G2); an explicit command-line flag
(tests/dev) beats the table, the table beats the built-in default:

  [daemon]
  step_timeout = "45m"          # --step-timeout (Go duration, > 0)
  stall_window = "10m"          # --stall-window (non-zero; negative disables)
  harness_root = "/abs/path"    # --harness-root
  operator_agent_dir = "/abs"   # --operator-agent-dir
  max_concurrent_jobs = 6       # --max-concurrent-jobs (pf ops max N
                                #   overrides it; a reload that changes
                                #   it wins last)

Every pf command, ensure and the daemon validate the table on load (a bad
value names the file, line and key). Change a key with pf config set;
the running daemon applies the live keys at once (pf config reload
applies a hand edit), takes the [workspace] mapping at the next claim
(no restart needed) and reports the restart-only keys as pending until
the next daemon start. A reload that changes max_concurrent_jobs
overrides pf ops max (last action wins). A config without a [workspace]
mapping is refused (the daemon would refuse to start). pf ops shutdown
stops the daemon for good: the loop ends with the clean exit.

pf daemon [--flag value] ... runs the local polyforge daemon in the
foreground (a UDS server hosting the runtime and observe projections). The real entry is assembled: cmd/pf/main.go
intercepts ` + "`pf daemon`" + ` argv before CLI dispatch and calls
internal/polyforge/daemon.Main. This CLI registration exists only to
serve ` + "`pf help daemon`" + ` presentation.

Workspace mapping (polyforge#113): the daemon reads the [workspace]
sections of the shared config — one [workspace."<path>"] section per
workspace, each carrying the projects whose capsules live under it.
Capsules are placed at <workspace>/capsules/<project>-<seq> per the claimed
work item's project; an unmapped project is refused at claim time; a
config with zero sections refuses startup. There is no launch-time
workspace flag and no default project: the mapping is the only source.
pf doctor checks the mapping and the connectivity faces.

Launch flags. Production sets only what pf daemon ensure passes (the
fixed launch flags) plus the [daemon] table above; a flag marked
test/dev-only is never set in production. The daemon reads no
POLYFORGE_* variable except POLYFORGE_DATA_DIR (the --data-dir default).` + "\n" + daemonFlagsHelp()

var cmdDaemon = &Command{
	Name:    "daemon",
	Group:   GroupProcess,
	Summary: "Run the local pf daemon; `pf daemon ensure` starts it under supervision unless it answers (main.go intercepts `pf daemon` before CLI dispatch; this verb serves `pf help daemon` only)",
	Usage:   daemonUsage,
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		// In a correct build this Run is unreachable: cmd/pf/main.go
		// intercepts `pf daemon` argv and calls daemon.Main. If it is
		// ever reached, the only honest answer is the launch-surface help.
		fmt.Fprint(ctx.Stdout, daemonUsage)
		return nil
	},
}

// opsEndSession handles `pf ops end-session <wi> --reason <text>`: the
// recovery door for a work item whose holder daemon is dead or stuck. It
// talks to aihub directly with the caller's key (admin or project
// maintainer, checked server-side): the live session is ended WITHOUT the
// holder's secret, the WI returns to queued and the old secret is fenced.
// Any daemon then claims it with a plain `pf submit <wi>`, restoring
// progress from the capsule revision chain.
func opsEndSession(ctx *Context, args []string) error {
	f := subFlags(ctx, "ops end-session")
	reason := f.String("reason", "", "why the holder's session is being ended (required; recorded in the session_superseded event)")
	rest, err := parseCommandArgs(f, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("ops end-session: expected exactly one work item id")
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("ops end-session: --reason is required (recorded in the audit event)")
	}
	name := wiDisplayName(ctx, rest[0])
	if err := ctx.aihub().ForceEndSession(rest[0], strings.TrimSpace(*reason)); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"work_item_id": rest[0], "status": "queued"})
	ctx.finish(raw, func() {
		fmt.Fprintf(ctx.Stdout, "ended the session of %s; it is queued again — claim it on any daemon with `pf submit %s`\n", name, rest[0])
		fmt.Fprintln(ctx.Stdout, "  the previous holder is fenced (its secret no longer matches)")
	})
	return nil
}
