# v2 Architecture Spec — Design Record

> **ARCHIVED 2026-10-16** (repo review 2026-10-16 wave 4): describes the v0/v1 design and is kept for history only — do not build from it. Current reference: [reference.md](../reference.md); decisions: [v2-converge.md](../v2-converge.md). Live mechanisms this file still held were moved there before archiving; section anchors are unchanged.

> **注意 / Note（2026-10-12）**：本文部分内容已被 [v2-converge.md](../v2-converge.md) 取代；两者冲突时以 v2-converge 为准。Parts of this document are superseded by v2-converge.md; where they conflict, v2-converge wins.

> Status: RULED (human-approved). This is the authoritative summary of the v2 architecture
> redesign. The full specification lives in the work item (polyforge#61 content field).
> Implementation is decomposed into 9 follow-up tickets (§13 of the spec).

## Scope

Replaces the custom publish engine with git, reorganizes the pf binary into
message-driven Runtime + Observe modules, and simplifies the control protocol
from 5 methods to 4. Validated through three review rounds (two model reviews
of the git proposal, one review of the message-driven model).

## Key Decisions

1. **Revision chain = git history**: capsule is a git worktree (`.gitignore`
   excludes `repos/` and `artifacts/release-*/`); aihub hosts bare repos per
   WI served via git-http-backend proxy through existing HTTP auth.

2. **git-dir outside capsule**: `--git-dir=/var/lib/polyforge/git/wi_xxx.git`
   prevents subagent hook injection (RCE via `.git/hooks/`).

3. **Control protocol = 4 methods**: `flush(reason)`, `answer`, `stop_notice`,
   `probe`. `quiesce`/`resume` deleted (step boundaries are natural pause
   points). `step_notify`/`publish_request` deleted (daemon watches
   `execution.json`).

4. **Agent is zero-blocking during normal execution**: writes execution.json,
   continues. Observe detects changes and triggers async git commit+push.

5. **Recovery engine** (renamed from "dual-track"): cascading resume→fresh→bounded
   fail. Discriminator: transcript available (not same/cross-machine).

6. **Message-driven Runtime/Observe**: 5 messages with attempt-id fencing.
   Runtime = lifecycle (start/stop/recover). Observe = per-WI monitoring goroutine.

7. **Transcripts NOT in git** (secret scanner would block wraps): side-channel
   upload to aihub `sessions/wi_xxx/`.

8. **Process cleanup**: 6 layers, cgroup v2 as layer 0 (atomic kill of all
   descendants). Layers 1-5 as fallback for non-cgroup environments.

9. **Module reorg**: `internal/aihub/` + `internal/polyforge/` + `internal/common/`.
   Dependency direction enforced by Go import paths.

10. **Code reduction**: ~17000 lines → ~5900 lines (est. 65% reduction).

## Implementation Tickets (§13 of full spec)

1. aihub git proxy + pre-receive hooks + reconciler
2. Runtime module (Start/Stop/Recover/Restart + process cleanup)
3. Observe module (monitor goroutine + events + answer relay)
4. Control protocol rework (flush(reason) + probe + answer + stop_notice)
5. pf-execute skill revision
6. Recovery engine (resume/fresh/session.json extension)
7. Module reorganization (9 packages → 3 directories)
8. Regression rewrite + new scenario e2e
9. Migration plan (SQLite revision chain → git revision chain)

## Consistency Check Rulings (2026-09-29)

Three blocking issues from the consistency check, resolved by human ruling:

### C1: Secret scanning removed; transcripts go into git
Pre-receive hook drops secret scanning. Transcripts enter the capsule git
repo normally (each step commit includes the transcript). Cross-machine
recovery gets transcripts automatically via git clone. No side-channel
upload needed. `.gitignore` reduced to: `repos/` + `artifacts/release-*/`.

### C2: Control protocol split into two channels
- **stdio control channel** (daemon→agent): `answer`, `stop_notice`, `probe` — 3 methods
- **UDS proxy** (agent→daemon): `flush(reason)` — replaces `publish_request`
Not one protocol; two transports with separate method sets.

### C3: INV-11 explicitly abolished
Old invariant (00 §4.3): "agent MUST proactively request publication at
four mandatory boundaries" — DELETED.
New invariant: agent is zero-blocking. Daemon observes execution.json
changes and triggers async commit+push autonomously. Agent does not know
git exists, does not know daemon is watching, does not initiate any
archival request. pf-execute §8 completely rewritten.

### Additional corrections from consistency check
- Resume IS already implemented (watchdog same-session re-derivation in v0
  code) — recovery engine is modification, not pure new work
- `repos/<name>/.git` injection risk persists even with capsule git-dir
  externalized — task repo hooks still need `core.hooksPath` neutralization
- `capsule` and `workflow` packages need assigned homes in the 3-directory
  reorg (recommend: `common/capsule` + `common/workflow`)
- Tickets 4+5 must land in one commit (drift guard coupling)
- Ticket 7 (module reorg) must land after tickets 1-3

### Version naming correction
This spec is **v1**, not v2. The current pilot code is **v0**. The old
aihub system (the existing aihub host) is not versioned in this scheme.
