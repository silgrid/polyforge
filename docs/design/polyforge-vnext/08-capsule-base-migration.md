# 08 — Capsule base migration runbook (a manual procedure)<!-- authority -->

> **Note (2026-10-12)**: parts of this document are superseded by
> [v2-converge.md](v2-converge.md); where they conflict, v2-converge wins.

| Authority item | Value |
|---|---|
| Document role | The manual migration procedure for the capsule base (the workspace): the shutdown → mapping rewrite → restart sequence for a machine/disk/base change (in-flight capsules are not moved), and the `capsules/` `.gitignore` guidance |
| This document is the sole authority | The daemon has **no** and will never have an automatic migration: no migration subcommand, no migration flag, and nothing moves any capsule at startup. This document is the only place that names the old base's default path |

## 1. Background <!-- §1 -->

Since polyforge#113 the daemon's capsule base is decided solely by **the shared
config's `[workspace."<path>"]` mapping**: one section per workspace
(`projects = [...]` lists the projects that land in that workspace; a leading
`~` in the key expands to the home directory, the key must be an absolute path
without a trailing separator, and a project can belong to only one workspace —
the inverse mapping must be a function). The CLI and the daemon read the same
config; **there is no startup-time workspace flag or environment variable**
(the historical `-workspace` flag and its environment mirror are deleted:
passing the old flag is an ordinary unknown-flag parse failure, and a flat
`project` key in the config is refused at load with migration guidance; the
even older default base `<data-dir>/workspace` and
`-workspace-root`/`POLYFORGE_WORKSPACE_ROOT` were deleted long before):

- **A config with zero `[workspace]` sections** (the fresh-install template
  shape) makes the daemon refuse to start, pointing at this shape and `pf
  doctor`.
- At startup the daemon validates **every** mapped workspace: the directory
  must **already exist** (the daemon never creates it on the machine's behalf)
  and `<workspace>/capsules/` must be creatable and writable. The daemon writes
  only inside the `capsules/` subtree of any workspace.
- A capsule is routed by the claimed WI's project through the inverse mapping
  to `<workspace>/capsules/<project>-<seq>/` (polyforge#144; before, the claimed
  capsule directory was named `<wi_id>`, and resuming follows the recorded
  path).
- **A base change does not need to move in-flight capsules**: keep the old base
  as a workspace section (with its projects) in the mapping and the in-flight
  capsules "stay where they are", still valid — restart reconciliation resumes
  them in place by the capsule path recorded in the job record, never moving
  anything, never rebuilding from the remote git chain (unpushed local capsule
  state must not be silently dropped).
- If a running WI's project has disappeared from the mapping (an operator
  deleted the section and restarted), the daemon no longer refuses to start as
  a whole: it puts **that WI** into aihub `blocked` only (the reason note names
  the project, `pf watch` shows a `project_unmapped` event), every other WI
  recovers as usual; the capsule is **never** moved to another workspace. The
  remedy: add the project's workspace section back into the mapping, then `pf
  resume <id>` (a fresh re-claim from the aihub capsule chain and the carrier
  ref, see v2-converge §14.1).
- Capsules of terminal WIs (wrapped/fail/canceled) stay where they are; the
  daemon's tool surface (`pf watch` etc.) looks for a capsule only in the
  workspace the WI's project **maps to**, as `capsules/<project>-<seq>` —
  adding the old base (with that project) back into the mapping makes them
  findable again. (The `pf ship` this paragraph used to name was deleted by
  polyforge#166: delivery is the workflow's deliver step.)

## 2. The migration sequence (manual, run in order) <!-- §2 -->

### 2a. Machine/disk change: in-flight capsules are not moved <!-- §2a -->

Restart reconciliation trusts only the capsule path in the job record
(`runtime/restart.go` `requireMidflightCapsule`): if the WI's project is still
in the mapping (any workspace) and the recorded directory exists, it resumes
in place. So **do not `mv` an in-flight capsule** — once moved, the recorded
path no longer exists, the WI goes `blocked` as `capsule_missing`, and the
moved directory is never used. When the old directory cannot be kept (a
machine change, a disk removal):

```
# 0. Create the target workspace first (the daemon never creates it)
mkdir -p /srv/polyforge/workspace

# 1. Stop this machine's daemon (a clean exit also ends the `pf daemon
#    ensure` tmux supervision loop; it is not re-raised)
pf ops shutdown
# (machines with a self-managed systemd unit: systemctl stop polyforge-daemon)

# 2. Rewrite the shared config's [workspace] mapping: one section for the
#    new base, listing the projects
#    (edit ~/.config/polyforge/cli.toml)
#      [workspace."/srv/polyforge/workspace"]
#      projects = ["p1", "p2"]

# 3. Re-raise the daemon on the same config (the mapping is the only source
#    of workspaces, there is no flag to pass; polyforge#174)
pf daemon ensure

# 4. In-flight WIs whose old directory is now gone are blocked
#    (capsule_missing): re-claim them from aihub into the new base
pf resume <id>
#    the capsule chain + carrier ref (refs/pf-task/<name>) + WIP ref
#    (refs/pf-wip/<name>) are restored; completed steps do not rerun,
#    local changes after the last push are lost
```

### 2b. Change the base without moving: keep the old base as a mapping section <!-- §2b -->

Leaving the old directory where it is suffices: in-flight capsules resume in
place by their recorded path (the only requirement is that their project is
still in the mapping, in whichever section). If the old base's projects should
keep landing new capsules there, keep it as a section in the mapping:

```
#  [workspace."/old/ws"]
#  projects = ["legacy-proj"]
#
#  [workspace."/srv/polyforge/workspace"]
#  projects = ["p1", "p2"]
```

### 2c. Verify <!-- §2c -->

After the restart, use `pf ops status` to verify that the `workspaces:` line
lists **every** workspace in the mapping (no longer just one); in-flight WIs
left in place resume in place during restart reconciliation, WIs whose old
directory is gone are `blocked` waiting for `pf resume`; `pf doctor` validates
the mapping item by item (a missing directory, an unknown project, an
ambiguous inverse mapping are each named).

## 3. The `capsules/` `.gitignore` guidance <!-- §3 -->

A workspace often sits inside a git repository or a git-tracked directory.
**The daemon never creates, appends to or rewrites
`<workspace>/.gitignore`** — ignoring `capsules/` is the installer's /
operator's own action:

```
# Add one line to the workspace's .gitignore:
capsules/
```

`tools/install.sh` adds it for you at install time (and prints what it did);
machines migrated by hand per this runbook add it themselves.