# polyforge — project rules

Rules for any agent working in this repository. They add to the generic
principles every workflow step carries (`workflows/_shared/principles.md`);
`workflows/` itself stays project-neutral, so a rule that only holds for
polyforge belongs here, not there. A review treats a violation of any rule
below as a blocking finding.

- **Scope.** The core is task + memory + workflow, and the feature set is frozen.
  Keep the mental model simple.
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
