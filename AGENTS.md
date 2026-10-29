# polyforge — project rules

Rules for any agent working in this repository. They add to the generic
principles every workflow step carries (`workflows/_shared/principles.md`);
`workflows/` itself stays project-neutral, so a rule that only holds for
polyforge belongs here, not there. A review treats a violation of any rule
below as a blocking finding.

- **Scope.** The core is task + memory + workflow. The internal line keeps iterating
  and evolving; its changes reach the public repository as PRs filed in batches, each
  gated by the owner. Keep the mental model simple.
- **Resumable capsules.** Every work item has a complete capsule and can resume
  from any stage. Shutdown is designed as crash-equals-shutdown; the kill -9
  chaos tests must keep passing.
- **Runtime locking.** One actor (mailbox + goroutine) per work item owns
  its state. The job-table lock only guards shared data and no I/O happens
  while it is held.
- **No aihub credentials in step agents.** Memory reads and writes go through
  the daemon. A new daemon or aihub entry point states whether a step agent
  can reach it; an unstated one is a finding.
- **Supervision.** The daemon is kept alive by tmux (containers have no
  systemd) and inherits only allowlisted environment variables.
- **No backward-compatibility code.** No compat shims, dual paths or
  legacy fallbacks: when behavior changes, the old shape is deleted. The one
  exception is transition code a safe rollback needs, and it must state the
  condition under which it is deleted.
- **doctor and workspace layout.** `pf doctor`'s checks and the `pf init` /
  workspace conventions stay; a simplification changes only their
  implementation.
- **Release and deploy.** aihub ships before `tools/install.sh`;
  `.polyforge/release.sh` and `.polyforge/deploy.sh` are the only entry points.
- **Coupled obligations.** A plan names, for every file it writes, the
  documents, tables and projection rules that must change with it (or an
  explicit none with a reason). A new file has no incoming references, so a
  grep for its name cannot find these; check this list before deciding:
  - a new `pf doctor` check needs its row in the doctor verb's enumeration,
    `skills/polyforge/verbs/doctor.md`.
  - `docs/upgrade.md` is append-only: a change adds a section at its end and
    rewrites no existing byte, and every append moves the file's sha256 pin in
    `test/w254_docs_set_acceptance_test.go` in the same commit (a pin left on
    the old bytes is a red full bar).

  Review treats a coupled obligation named here that the plan omits, or that
  the delivered diff leaves unchanged, as a blocking finding.

Contributor guide (filing work items, change coverage): docs/contributing.md.
