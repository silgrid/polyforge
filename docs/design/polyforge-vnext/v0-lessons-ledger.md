# v0 Lessons Ledger — every real-machine failure v0 ever hit
# (source: pilot history, HANDOFF, ticket records; the authoritative
# registry the v1 implementation should have been audited against)

> **Note (2026-10-12)**: `docs/pilot/` has been deleted; to see the pilot paths this document cites, look in git history (tag `v2.1.0`).
>
> **Superseded marks (repo review 2026-10-16 wave 4)**: entries whose *mechanism* no longer exists carry a
> `〔SUPERSEDED …〕` line naming what replaced it; the lesson itself still applies. Open entries fixed
> later carry a `〔FIXED …〕` line naming the change, and one still open in a changed form an `〔OPEN …〕`
> line giving its current form and ticket (marked 2026-10-18). Paths such as
> `03-execution-capsule` now live under [archive/](archive/); the current system is [reference.md](reference.md).

## Protocol/harness class
P1  Real pi rejects invented RPC envelopes (v0 first pilot ticket:
    "Unknown command: undefined"). v1 spawn envelope must be the v0
    live-tested prompt-command/record-stream shape.
P2  Probe argv must be faithful: --no-tools was NOT a real pi flag (v0
    probe found it; removed). Never invent harness flags.
P3  PI_SESSION_DIR does NOT control pi's transcript location. Real
    transcript: ~/.pi/agent/sessions/<dashify(cwd)>--/<ts>-<uuid>.jsonl.
    pi CAN resume from any .jsonl path via --session.
P4  cc protocol: UUID sessions, stream-json bidirectional stdin,
    --resume <uuid>, zero-turn crash fallback (same UUID fresh).
P5  WI Agent inherits the operator's GLOBAL pi config unless isolated:
    skills/context-files/prompt-templates (3 flags), env (PF_*/
    POLYFORGE_* strip + secret value strip), and ambient MCP/extension
    registrations leak other systems' toolfaces (the operator's
    mcp-adapter.json). --no-extensions must STAY absent (subagent tool
    is extension-provided) — residual exposure closed by instruction.
    〔SUPERSEDED (2026-10-16): the WI Agent is deleted; step agents run with a dedicated step-agent dir (no settings.json, only extensions/subagent) and PF_* stripped — reference.md §7.6. The lesson stands.〕
P6  Model selection is per-step bundle business (model_candidates with
    ordinal advancement on INFRA failure only — business FAIL never
    advances). The agent must not silently run on the operator's
    global default model/settings (settings pollution: e.g. max_tokens
    values the operator set for another provider break the inherited
    provider with 400s).

## Provider/infra class
I1  Provider 400/503/connection-hangs are normal weather. Detection:
    message.stopReason=="error" + errorMessage records.
I2  Candidate advancement must be bounded and the ordinal persisted
    in execution state (crash-safe).
I3  wall_minutes parsing: float vs integer mismatch made budget gates
    misfire (v0 #46).
I4  Wait, hang and stream windows need injectable bounds or tests
    take hours (v0 shipped parameterized windows).

## Sync/ship class
S1  Ship once pushed the BASE sha instead of the sealed HEAD (v0 #46
    era). Ship must verify remote branch == sealed head.
S2  Sync roots excluded repos/ and release trees from the capsule
    revision chain (v1: .gitignore equivalents).
S3  Stale-generation quarantine: pushes with superseded credentials
    must be fenced server-side (attempt-scoped credentials; takeover
    revokes).
    〔SUPERSEDED (2026-10-16): generations/attempt credentials and takeover are deleted; pushes are fenced by the live session secret (GIT-401) and recovery is force-end-session + a normal claim — reference.md §3.4. The lesson stands.〕
S4  Publish/flush semantics: wrap/shutdown flushes are blocking with
    typed timeout (60s); shutdown with dead agents must not wedge
    (v0 #32: 20min wedge -> 5min bounded shutdown budget).
    〔SUPERSEDED (2026-10-16): the flush lane is deleted; pushes are event-triggered with a 60s bound and the shutdown has a 5-minute budget — reference.md §6.2, §8. The lesson stands.〕

## Daemon/lifecycle class
D1  Dead agent + detached bash descendants escape the process group —
    six-layer cleanup with (pid,starttime) guards; PF_WI_ID env marker.
D2  Agent death mid-answer-relay: answers must not be lost — idempotent
    3-state ledger, failure never consumes the answer id; new attempt
    must see the pending question (re-injection).
    〔SUPERSEDED (2026-10-16): the answer ledger is deleted; answers are archived in execution.json by the driver and a re-dispatch re-asks the pending question — reference.md §7.3. The lesson stands.〕
D3  Restart must reconcile with aihub before reviving (wrapped -> don't
    revive; running -> kill+recover; blocked -> leave).
D4  Claim idempotency: re-sending the same key returns the same attempt
    (network retries); takeover requeue idempotency.
    〔SUPERSEDED (2026-10-16): claim idempotency is keyed on the session secret: the holder's repeat answers the same claim, a superseded or ended secret is refused CORE-403 STALE_SESSION; takeover is deleted — reference.md §3.2. The lesson stands.〕
D5  Stale claim detection: a crash between claim and spawn must not
    orphan a generation forever (v0 known gap #Start-crash).
    〔SUPERSEDED (2026-10-16): the pre-claim claiming journal (claiming.json) is the fix — reference.md §3.5. The lesson stands.〕
D6  needs_input had NO proactive owner notification in v0 (watcher
    scripts filled the gap). v1 answer: check at turn start / events
    file — the events file exists; owner-facing surface?
D7  Shutdown sequence: stop_notice -> flush(shutdown) -> seal; never
    let observe re-derive an agent after seal.
    〔SUPERSEDED (2026-10-16): the shutdown is latch → cancel → kill agent trees → refs/pf-wip snapshot (parallel, 30s) → persist, no flush, no session end — reference.md §6.2. The lesson stands.〕

## Data/capsule class
C1' (v0 lesson, superseded by v1 C1 ruling) transcripts are valuable:
    in v1 they must actually REACH the capsule git (sync mechanism was
    the missing piece — see P3).
C2  Work-item context delivery: v0 delivered goal/content via daemon
    proxy_call (read_workflow_ref). When a contact surface is deleted,
    enumerate the business it carried before deleting it.
C3' execution.json is the only progress authority; Version must bump
    on every change the daemon should observe.
C4  Digest integrity: bundle digest pinned at claim must be verifiable
    (v0 verified server-side; v1 wires it at materialize).
    〔SUPERSEDED (2026-10-16): the bundle digest chain is deleted (polyforge#149 B9); bundles are referenced by name only — 01-workflow-skills.md §4.5. The lesson stands.〕

## Process class
X1  Smoke commands given to agents need timeout + explicit stdin close
    (#31 40min wedge).
X2  pkill/kill argv self-match traps: always script files, never
    inline patterns.
X3  One-shot agents must not start background tasks; tests run in
    foreground.
X4  The memory ledger exists precisely to prevent re-hits — every
    implementation/review lane MUST recall it first (v1 lanes did not;
    that is why F1/F3/F4 happened).

## v1 pilot findings (F1–F7, F11–F22) — the lessons the ledger itself predicted

X4 said every implementation/review lane MUST recall this ledger first; the
v1 pilot lanes did not, and every one of these findings is a re-hit of an
entry above. Recorded here so no future lane re-derives them. F8–F10 are
spent in git history and reserved for a future backfill — the numbering gap
is deliberate, not an error.

F1  Goal never reached the agent. v1 deleted v0's daemon proxy_call
    (read_workflow_ref) without enumerating the business it carried (C2):
    with no agent→server read path at all, the WI agent could not even know
    its own goal. Fixed: the claim answer's task definition is materialized
    in full as runtime-state/work-item.json (the ONE authoritative statement
    of the ask inside the capsule) and recovery repairs a missing copy.
    Fixed in main (v1-fix-pilot1, merged as 1059da5 / pilot commit 39dfd1c).

F2  Slug-built git paths. A submitted "<project>#<seq>" spelling leaked into
    the push remote URL (a 404 — "#" is the URL fragment separator) and the
    capsule directory name. Fixed: the claim answer's canonical work-item id
    is the authority; every path (capsule dir, external git-dir, push
    remote, session id, spawn journal) is keyed by it, the submitted
    spelling is registered as an alias only, and Restart migrates
    pre-canonical residue in place. Fixed in main (v1-fix-pilot1, 1059da5).

F3  Transcripts never reached the capsule. The v0-ported PI_SESSION_DIR env
    is dead for transcript placement (P3): a fresh derivation writes to the
    harness's own session store, nothing landed in capsule git (C1'). Fixed
    daemon-side: locate the live transcript after spawn, copy it into the
    capsule at every commit boundary (before the commit, so it rides the
    chain), and resume from the capsule copy with the live file as fallback.
    Fixed in main (v1-fix-pilot1, 1059da5).

F4  The candidate chain was a decoration and settings pollution was open
    (P6 + I2). The bundle's model_candidates were never consumed: the spawn
    pinned no model (the agent silently ran on the operator's global
    default), the ordinal was neither advanced nor persisted, business
    FAIL/rewind semantics were not wired (INV-1g), and the observe face's
    StepView.Model rendered permanently empty. Fixed: the minimal
    frontmatter read surface (defaults + per-step model_candidates) is back
    in common/workflow; materialize extracts the defaults chain onto the
    job; every derivation spawn is pinned to the candidate at the persisted
    ordinal (argv — measured: capsule-local project settings are
    trust-gated OFF in rpc mode, see spawn.go buildCmd); ONLY a
    provider-class recovery advances the ordinal (bounded, wraps —
    exhaustion is not terminal) and the advance is crash-safe in
    state.json; the executor records the effective model into
    steps.<id>.model and the observe face renders it.
    Fixed in this lane (v1-lessons-fix, base 1059da5).

F5  Fatal provider error handling and the idle watchdog (I1 re-hit): a fatal-level provider
    error was previously handled as an ordinary interruption (no self-recovery, no timely death
    judgement), and a long silent step had no watchdog.
    Fixed: fatal provider error recovers at once + idle watchdog (10-minute window +
    probe). Fixed, main d84115b (branch v1-f5-watchdog, branch commit
    2a0ab4a).

F6  The recall instruction carried in the goal was not executable (X4 re-hit): the agent had no agent→server
    memory read path and could not read the ledger before acting. Fixed: a static memory snapshot at materialize +
    a narrow UDS recall/remember channel + the CLI memory verbs (`pf recall` /
    `pf remember`). Fixed, main c740fb0 (branch v1-f6f7, commit e0db578).

F7  Step-definition materialization misplaced and the completion marker missing (03-execution-capsule §4.5 re-hit):
    materialization copied the step definitions into the capsule's `agents/` instead of Pi's discovery directory `.pi/agents/`,
    and wrote no completion marker. Fixed: the `.pi/agents` materialization target directory + the completion marker
    `sync/materialized.json` (RelMaterializeMarker) + trust gate
    (a snapshot is trusted only when the marker is complete and matches the pinned bundle identity). Fixed,
    main c740fb0 (branch v1-f6f7, commit e0db578).

F11 e2e daemon teardown leaked fakepi orphans (D1/X3 re-hit): after every e2e teardown path
    (graceful stop, crash, SIGKILL fallback and so on) tore the daemon down, fakepi orphan processes carrying the scenario's
    `PF_WI_ID` marker could still survive. Fix: after teardown scan /proc for live processes carrying
    that scenario's PF_WI_ID marker, SIGKILL all survivors, and assert zero residue by bounded polling
    (registered as the scenario's FIRST cleanup, so LIFO guarantees it runs after all teardown cleanups).
    Status note: fixed (main d586396, in HEAD feaf127; test changes
    test/v1_harness_test.go +70, s12/s13 each +3).

F12 Shutdown push race (S1/#46 re-hit): when the daemon shuts down, the flush(shutdown) push
    (observe/monitor.go flushShutdown, ~line 1333) races the asynchronous archival push
    (schedulePush, ~line 901) / the process exit, so the remote can lag the local sealed
    head — a v1 re-hit of the lesson class of v0 #46 "a push must end at the sealed HEAD, never a stale/base sha".
    Status note: compensated (backstopped by the verification surface that the #46 fix put in place:
    ship.go ls-remote verifies remote==sealed commit, and a moved branch is never
    force-pushed (~lines 160–200); restart reconciliation completes the seal — test fa9ed17 "s17
    SIGKILL inside the SEALING window").
    〔SUPERSEDED (2026-10-16): flush(shutdown) is deleted with the flush lane; see S4. The lesson stands.〕

F13 Two maxTokens incidents (P6 re-hit ×2): in two cases the operator's global model configuration set
    maxTokens above/equal to the model's contextWindow (163840 > 131072,
    131072 = 131072), and the inheriting providers crashed with 400 both times — a settings-pollution double hit.
    Discipline: the operator's model configuration must keep maxTokens << contextWindow. Status note: compensated
    (spawn.go deliberately sets no max_tokens — the pinned model's own default is the conservative choice, and a self-made
    cap is exactly the other face of the v0 settings-pollution failure shape (comment block ~line 234); F4's
    argv model pin removes the defaultModel inheritance vector; the residual per-model operator override
    is a documented, deliberate acceptance).

F14 boot.sh full-stack restart collateral damage: on 2026-09-30 an operator wanted to restart a single daemon and ran
    `/root/polyforge-pilot-v1/boot.sh` (the full-stack idempotent restarter: kill the old aihub +
    both daemons, then re-raise all three), and the whole v1 pilot stack was stopped along with it.
    Status note: compensated (a single-point restart script has been built: `boot-b-only.sh`, created 2026-09-30
    11:46 — it kills only daemon-b, polls ≤120s for `b/daemon.sock`
    to be released, and restarts daemon-b alone with setsid; recorded in the repo in
    `docs/pilot/ops-scripts.md` operations notes, lines 75–77).

Numbering reconciliation (the polyforge#51 appended batch): when this WI's goal was written as "ledger adds F13-F17"
F13/F14 did not exist yet, and #47 (commit `c1c55be`) later appended F13/F14, so this batch
numbers consecutively from F15 (the next free number); F8–F10 are still deliberately kept as the earlier ledger note says and not backfilled.
The phase-0 observation window's real-machine findings actually number 6 (F15–F20), more than the 5 slots
the goal implied — registered as the evidence shows, neither adding nor dropping to fit the count.

F15 Watchdog progress-semantics blind spot "UI noise is not progress" (a new failure shape of the watchdog F5
    introduced, not a re-hit of a v0 entry): a real-machine double hit — a subagent dispatch wedged for 2.7 hours
    under the doubly blind watchdog (the hang criterion requires silence, and streaming
    extension_ui noise kept refreshing lastRecordAt and blocked it; the idle criterion requires no open
    turn, and a genuinely open turn blocked it — the two criteria blind to each other, neither firing). Fix: records are split into progress (message_*/toolResult/lifecycle/response) vs noise
    (extension_ui_request/unclassified/unparseable), and Silent() and the idle criterion anchor on
    LastProgressAt (the three shapes A/B/C + an s21 e2e against a real daemon pin it down; also added the
    no_progress owner-visible event and the LAST-PROGRESS column of ops status).
    Status note: fixed (main `ef67aa7`, branch v1-f15 merge `d485c72`; before the fix
    landed, during #46, a manual shim `/root/polyforge-pilot-v1/f15-shim.sh`
    filled in — when the capsule transcript was silent >15min and the WI was still running it killed the process tree and handed over to recovery,
    and exited on its own at the WI's terminal state).

F16 Hole in the flush-fence adoption window (same type as S3: a superseded generation must be blocked by the fence): if the monitor's
    lagging current-id still equals the flush party's id, it is in fact already stale when the hub's latest known has pointed at the successor
    — only a zombie speaks in a predecessor's name, yet the old fence let such
    "current-id hit" zombie flushes through. Exposed by the timing perturbation the F15 fix introduced
    (s10 red, deterministically reproduced after the wait fix). Status note: fixed (main `a91ef69`:
    the flush fence gains a cross-check against the hub's latest-known adoption window; s10 now waits for the monitor's
    adoption event (wi_agent_rederived) instead of the spawn journal;
    the flush_recovery_window HTTP test pins the tightened semantics; the same commit also treats s19
    ShipPR's gh repo-creation rate limit as an availability gate — skip, not fail).
    〔SUPERSEDED (2026-10-16): the flush fence is deleted with the flush lane; dispatch fencing is spawn_n in the driver — v2-converge.md §2.2. The lesson stands.〕

F17 M4 evidence chain broken (same type as C3': evidence that should have landed in the execution authority file never did): of 39
    executed verification steps, 17 had no structured command evidence (12 old-schema
    execution.json files with no commands field + 5 whose commands had no readable entry),
    M4 = 22/39 = 56.41%, the PILOT-V4 veto triggered — the cause was a gap in the evidence surface,
    not a false claim being proven, and old-schema tickets cannot be retroactively scored. Status note: fixed (the V4/V8
    fix lane, ledger F21: the verification evidence contract hard-enforced in three layers + wrap-side
    fail-closed; branch v1-v4v8, commit to be backfilled after merge).

F18 Push storm under persistent rejection (the S4 bounded-ness ruling missing on the publish-retry surface):
    wi_VCyBYf7uYeb2 (polyforge#48) pushed the same capsule 185 times in a row at a fixed ~10s cadence, all
    push_failed (first the aihub gitlink mode-160000 rejection, then the
    commit c82791fd… rejection, plus 2 wrap_blocked), with no backoff, no circuit breaker, no
    escalation, the storm lasting about 1 hour; that sample batch alone pushed M8 to 188/647 =
    29.06% > 1%, triggering the PILOT-V8 veto (M7 counted only 1 segment ≤ 3, not triggered).
    Status note: fixed (the V4/V8 fix lane, ledger F22: poisoned-chain local precheck
    GIT-412 + the push_poisoned_local classification + a same-error retry cap; branch
    v1-v4v8, commit to be backfilled after merge).

F19 The M6 observation field was never emitted on the event surface: in the whole window events.log held 33
    memory_remember events, 0 of them carrying a write_ms field (the old-schema event surface did not contain
    that observation field; the migration CLI's direct writes and the server-side reconcile write-back produce no events — an already
    registered boundary of the sample set), PILOT-V6 had no samples this window and took no part in the verdict: the search real-time
    surface (the pre-2026-10-16 version's 06-memory-search §4.5 p99 ≤ 5s, see git history) was unobservable throughout. Status note:
    not fixed — registered as a phase-1 input (`docs/pilot/v1-phase-0-report.md`
    §5.2 "M6 sample-set boundary": it needs a new event kind or the sample surface completed, and is not touched before authorization).
    〔SUPERSEDED (2026-10-18): since v1 (b1839e23) memories are written through aihub only — Store.Create inserts the row and its FTS row in one transaction (06-memory-search.md §2), so a memory is searchable the moment `pf remember` returns; no write-to-search latency is left to observe, and the p99 ≤ 5s goal (060c9f9a), the M6 gate and docs/pilot are deleted. The lesson stands.〕

F20 The M3 heartbeat surface was not wired up: in the whole window the aihub.db machines table had 0 rows — the machine heartbeat surface
    was never written, so M3 had only the step-duration surface (in_progress > 24h) to judge by, the machine-lost-contact
    surface (last_heartbeat_at beyond the liveness threshold) had no data, and `--m3-heartbeat-max-age`
    had no real data to configure. Status note: not fixed — registered as a phase-1 input
    (`docs/pilot/v1-phase-0-report.md` §5.2 "M3 heartbeat surface not wired up").
    〔FIXED (2026-10-02, 65b33708 "daemon+aihub: wire the M3 heartbeat face"): the daemon heartbeats aihub on its own goroutine (internal/polyforge/daemon/heartbeat.go) and `pf machines list` reads machines.last_heartbeat_at — e2e test/v1_s24_machines_cli_test.go.〕

F21 The V4 evidence contract mechanically enforced (the fix surface of the PILOT-V4 veto threshold; corresponds to finding F17):
    the same predicate hard-enforces the verification step's structured command evidence in three layers — the driver contract
    (`skills/pf-execute/SKILL.md` §4: the same write that marks a verification step `done`
    must already carry at least one readable `{argv, exit_code, duration_ms}`, and with no evidence
    done is not allowed; the Chinese mirror `references/agent-contract.md` §11 synced in step; the `agents/verification.md` of the four
    reference bundles gains the hard constraint "Evidence obligation (hard
    gate)"), the daemon-side fail-closed (observe's flush(wrap) sub-goroutine
    validates every executed verification step of execution.json, and a seal lacking evidence is answered
    `DAEMON-400 VERIFICATION_EVIDENCE_MISSING` (citing the ticket + the step name), non-final, and passes
    a re-flush after the evidence is supplied; the event surface `wrap_evidence_refused` leaves a trace), the predicate being bit-for-bit identical with
    the M4 reader (`commandEvidence` of `tools/gate-metrics.go`)
    (step id `verification`, executed = not pending / non-empty, argv non-empty +
    exit_code/duration_ms readable) — the gate and the metric judge with the same face, so in future an evidence-less
    verification cannot pass wrap again. Regression: observe unit tests + e2e
    `v1_s22_evidence_gitlink_test.go` S22a. Status note: fixed
    (branch v1-v4v8; commit to be backfilled after merge).
    〔SUPERSEDED (2026-10-16): the evidence gate lives in the driver envelope check (requires_command_evidence), not in observe flush(wrap); skills/pf-execute and tools/gate-metrics are deleted — reference.md §7.3. The lesson stands.〕

F22 Poisoned-chain local precheck + same-error retry cap (the fix surface of the PILOT-V8 veto threshold; corresponds to
    finding F18): gitops' CommitPush scans `git ls-files --stage` after `git add -A` and before commit,
    and a mode-160000 entry gives the typed error
    `GIT-412 LOCAL_GITLINK_DETECTED` (no commit is produced — a poisoned chain can never enter
    local history, eliminating at the source the #48 incident's shape "a poisoned commit has already been produced and can only be pushed again and again"; the error message gives the polluted path and removal guidance 〔2026-10-16 correction, repo review B-25/V-13: the removal guidance **never** tells anyone to delete a task repo's
    .git — a gitlink under `repos/` or `artifacts/release-*` only shows that the capsule `.gitignore` lost the corresponding line, and the guidance is to restore
    `.gitignore` and retry, stating explicitly not to delete that `.git` (it would destroy the task repo history, unpushed work included); a nested repo elsewhere is
    an agent leftover, and the guidance is to move it out of the capsule or write it into the capsule `.gitignore`, deleting its `.git` only when its history is disposable,
    then `git rm --cached` and verify with `git ls-files --stage | grep ^160000` being empty〕;
    the same type of error, where add fails outright when a nested .git has no commit, is classified the same way); observe's
    push path classifies it as the new event `push_poisoned_local` (kept apart from push_failed
    — the local precheck rejected "a commit that never legitimately existed", not "a
    failed boundary-sync request that did happen", and it enters neither the numerator nor the denominator of M8; a hook-side rejection, if it still occurs, still counts
    as push_failed), while the automatic retry gains a bound: 5 consecutive identical errors stop that round of retries and emit the
    `push_backpressure(reason=same_error)` degradation event, resuming at the next boundary
    (execution.json Version bump) or after one successful push — preventing the 185-push storm from recurring. Regression: gitops unit test (a local nested .git → GIT-412 produces no commit)
    + e2e `v1_s22_evidence_gitlink_test.go` S22b (stops after 5 rejections, after cleanup the
    chain recovers and the bare repo's history has no gitlink). Status note: fixed (branch v1-v4v8;
    commit to be backfilled after merge).

Numbering reconciliation (the V4/V8 fix-lane appended batch): when this batch's goal originally wrote "F18/F19", F18
    (push storm) and F19 (M6 observation field) were already occupied by the #51 appended batch (F15–F20 numbered
    consecutively), so this batch registers from F21 (the next free number) and maps the two fix surfaces respectively
    to the findings they really fix: F21 fixes F17 (the M4 evidence chain broken), F22 fixes F18
    (the push storm); the status notes of F17/F18 point at this batch in step.

## Ratification record (2026-10-01, confirmed one by one by the owner)

14 design/operations-level rulings made by the operator agent on the owner's behalf during the phase-0 observation window
were ratified to the owner one by one: 14/14 passed, zero overturned.
Covering: tightening the flush fence (F16), no back door to read the git surface, syncing transcripts before commit,
answers persisted before delivery, a shutdown of two processes reconciling first and then opening the UDS, grabbing version numbers,
a release authorization batch (alpha.4-30, create new only, no overwrite, no upgrade), tags not pushed to origin,
e2e gaps all opened as real tickets, minimal instrumentation of M6/M8, the V4 wrap evidence hard gate, the V8 poisoned-chain local
precheck + defence not counted as a failure, needs_input proxy answering (from this item on, the owner answers personally),
and #52 takeover brought forward.

## v1.5 appendix (2026-10-01, the session-model implementation trail)

The design rulings, review trail and residual unfixed items of the v1.5 session-model implementation, appended in
this ledger's house style (append-only; no entry of the earlier ledger is touched).

1. Overview: the v1.5 session-ID ownership model (session_id CAS claim,
   fencing-on-use, spawn_n, quarantine) replaced the generation/attempt/
   instance lineage as the coordination layer, merged into main @ b5ab060; the lineage was entirely deleted
   — aihub (b1dba93), wire types (03dc291), runtime (5e440db),
   observe re-keyed onto spawn_n (41da6e7), daemon/cli render spawn_n
   + session_fingerprint (da250ef). The design authority record points at
   `v1.5-session-model.md` (with the industry-research appendix: the epoch+
   fencing family, invalidation-on-claim vs fencing-on-use, the ZooKeeper
   GUID ambiguous-claim lesson) and `v1.5-api-contract.md` §1.5 AMEND.
   Status note: landed.

2. B1/B2/B3 rulings and resolution: three mandatory fable BLOCKER additions, all RESOLVED
   with evidence in the implementation review
   (commit 3158d25, verdict APPROVE-WITH-FINDINGS) — B1 the pre-claim durable journal (crash-safe, the D5
   #Start-crash fix and also the CAS retry-idempotency anchor, answering the ZooKeeper
   GUID ambiguous-claim lesson head-on); B2 the flush fence = the (session_id,
   spawn_n) double key (replacing the adoption-window fence, the successor of the F16 fix); B3
   identity/credential split (PF_SESSION_HANDLE shows only a fingerprint vs the
   fence-protected push credential, GIT_ASKPASS single delivery). Status note: landed.

3. M1–M5 rulings landed (short entries one by one):
   - M1 an empty session being both terminal and claimable → the CAS changed to a joint (status, session) judgement,
     and the claim gates restored. Status note: landed.
   - M2 bounded fail: session cleared + status=blocked, restart leaves it alone,
     and blocked is excluded from the CAS-claimable set until unblock. Status note: landed.
   - M3 paused-with-session: SessionEnd's 4th state `paused`,
     StatePaused added to the runtime state machine, the clean_pause marker, operator
     pause retaining the job record (the secret stays alive locally, and pf resume honors the resume
     promise). Status note: landed.
   - M4 periodic session re-validation: re-validated on the monitor tick (~30 seconds; constants in
     `internal/polyforge/observe/session.go`: defaultRevalidateEvery
     30s / verifyErrorBackoff 30s / verifyCallBound 70s), the Lane D gap wired
     in by 3158d25; the `--revalidate-every` flag landed by the sister
     capsule wi_brFjFISWrAFC (fa2e091, 4045576, 0b6ac8a).
     Status note: landed.
   - M5 same-machine two-daemon TOCTOU → a flock on the daemon data dir.
     Status note: landed.

   Also recorded, the post-implementation deltas approved by §1.5 AMEND: the 4th state paused, StatePaused
   retain, POST /work-items/{id}/unblock + `pf unblock`
   (admin-or-maintainer, the exit gate of M2).

4. Review trail (three rounds): first the fable design review — REQUEST-CHANGES, mandating
   B1/B2/B3 + M1–M5; the implementation review @ 3158d25 — APPROVE-WITH-FINDINGS
   (B1/B2/B3 all RESOLVED; 3 new MAJORs: OBSuperseded lost, pause
   destroys the record, no unblock gate; plus 2 MINORs); the fix lane e2137f5 +
   merged as 856ff5c — CONFIRM-APPROVE.

5. NEW-1: the fix fencing a stale OBSuperseded against the successor job's session fingerprint — commit
   3114915 "v1.5 NEW-1: OBSuperseded fenced on the session
   fingerprint" (+ gofmt b5ab060). Status note: fixed.

6. NEW-2: sister capsule wi_y50ZfykBewso (already wrapped) — commits 7786e99
   "runtime: bounded release lane for observe releases (NEW-2)" +
   f1fb1ee "daemon: close the runtime release lane at shutdown",
   released with v0.3.2 (tag at f1fb1ee). It bounds the resend cap of the terminal RTFinished
   observe release — when the observe channel is full, it defers rather than destroys a superseded/
   paused release. Status note: fixed — but one non-blocking residual remains (an unfixed
   candidate follow-up): the observe-side RTFinished handler
   (internal/polyforge/observe/observe.go ~296-307) is not aware of
   spawnN, and inside a compound race window a deferred RTFinished(paused) may arrive after the successor's
   RTStarted and unconditionally delete latestSpawn, Forget the answer ledger,
   and stop the new monitor; also noted: if a long-lived non-daemon Runtime is not Closed the lane
   goroutine stays parked.
   〔SUPERSEDED (2026-10-18): the residual is moot — latestSpawn and the answer ledger are deleted (b1f336e8; 34fdf867), the release lane became unbounded FIFO mailboxes (f0f8cd36, v2.2.0), and since the per-work-item actor (bd17b6af, #190) all of a work item's RT messages are pushed from its one actor goroutine into a single-consumer FIFO, so a stale RTFinished cannot arrive after its successor's RTStarted. The observe handler is still spawn-agnostic and relies on that ordering. The lesson stands.〕

7. NEW-3: sister capsule wi_YONafuu9hiy9 (already wrapped) — commits 01ddc26
   "runtime: replay a pending terminal close of a retained Paused
   record at restart (NEW-3)" + c297133 "docs: mark NEW-3 resolved
   in the v1.5 session-model record", released with v0.3.3.
   Status note: fixed — but one non-blocking SIBLING window remains (an unfixed candidate follow-up):
   restartResume's StatePaused branch still ignores the pending terminal close
   (the fix modelled on the retain branch is needed, consulting isTerminalStopReason); a FAILED
   pending-close replay has no separate failure event (surfaced on stderr only).
   〔OPEN (2026-10-18): restartResume is deleted (bd17b6af, #190), but reconcileRecord still resumes a Paused record carrying a terminal stop_reason when the hub answers running, or paused without clean_pause — only paused + clean_pause replays the close — and #190 dropped 735a9ad9's resume guard; a failed fromPaused SessionEnd is not retried during the run, and a failed replay still emits no failure event. Tracked in polyforge#197.〕
   〔FIXED (2026-10-18, PR #205 "a pending terminal close is replayed, never resumed"): a pending close replays whenever the hub shows the session live, whatever clean_pause says; pf resume and a repeat close are refused while it is pending; a failed close of a retained Paused record is retried during the run; a failed replay emits pending_close_replay_failed.〕

8. NEW-9: the cross-machine spawn_n ordinal collision, kept open, cosmetic/audit-only.
   Status note: not fixed (deliberately accepted, audit surface only).

9. Closing note: the operations drills D1–D5 all PASS (commit 8b2a249, the report in
   docs/pilot/v1.5-drills-runbook.md af3e4a5); this ledger remains the only authority of
   failure memory — exactly the reason this appendix exists (a continuation of the X4 discipline).
