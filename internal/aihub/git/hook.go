package git

// The pre-receive hook installed into every bare repo. It enforces the
// v1 capsule-chain invariants (reference.md §8; secret scanning is NOT
// among them — dropped by v1 ruling C1 (archive/v1-architecture-spec.md)):
//
//   - FF-only: a pushed ref must move fast-forward (refs/pf-wip/*
//     excepted, see below);
//   - no ref deletion (refs/pf-wip/* excepted);
//   - no gitlink (mode 160000) anywhere in a pushed commit's tree;
//   - blob size ≤ MaxBlobBytes (via the GIT_PF_MAX_BLOB env the proxy
//     sets; the default matches capsule.MaxBlobBytes) — for the
//     refs/pf-task/* carrier refs the limit is MaxTaskBlobBytes instead
//     (GIT_PF_MAX_TASK_BLOB; default capsule.MaxTaskBlobBytes).
//
// Session fencing is the proxy's: only the work item's live session
// secret authenticates a push (core.CheckSessionCredential).
//
// The refs/pf-task/<repo> namespace is the task-repo durability lane
// (polyforge#108): the daemon's publish boundary mirrors each task
// repo's LOCAL head there so a bounded-fail re-claim on a fresh session
// can restore it. The history under these refs is the TASK repo's own —
// it may legitimately contain submodules — so the chain-only gitlink
// invariant does not apply to it. Deletion, fast-forward and the blob limit stay enforced
// for every ref; the carrier refs get the larger task-repo blob limit
// (polyforge#158). The daemon normally pushes the carrier as a shallow
// push rooted at the WI line's upstream boundary, which cuts a foreign
// upstream asset (even one above the limit) out of the carrier's
// reachable history so it cannot block the push. The cut
// falls back to the hub's current carrier tip, or the recorded baseline,
// when the upstream walk answers nothing: the commits between that cut
// and the head (and their side history) are then carried, so an oversize
// upstream asset on that path can still refuse the push. The walk is
// empty only when the head is already reachable from origin's upstream
// refs, so the WI's work is already in origin and a re-claim loses
// nothing.
//
// The refs/pf-wip/<repo> namespace (polyforge#167) carries a snapshot
// of a task repo's UNCOMMITTED work (one daemon-made commit on top of the
// carried head) across a re-claim. It is scratch by design — every close
// supersedes it with a force push or deletes it — so it is exempt from
// deletion, fast-forward and gitlink; its new objects face the
// carrier blob limit (MaxTaskBlobBytes).
//
// The hook is a bash script exec'd by the real git receive-pack; the
// server process never interprets pack data itself.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// hookTemplate is the hook body. Kept as a function so the blob limit
// defaults can be baked in (the env overrides still win at runtime).
//
// The hook is fail-closed: `set -o pipefail` plus an
// explicit error check on every pipeline/command means any failing
// check command (git rev-list / cat-file / ls-tree, or the awk in the
// pipeline) rejects the push — an inconclusive check never reads as
// "no violation found".
func hookTemplate(defaultMaxBlob, defaultMaxTaskBlob int64) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
# aihub pre-receive (v1). Rejects the WHOLE push on any violation.
# Fail-closed: any check command that errors rejects the push.
set -u -o pipefail
MAXBLOB="${GIT_PF_MAX_BLOB:-%d}"
MAXTASKBLOB="${GIT_PF_MAX_TASK_BLOB:-%d}"
zero=0000000000000000000000000000000000000000
reject() { echo "aihub: $*" >&2; exit 1; }

while read -r old new ref; do
  # Task-repo WIP namespace (polyforge#167): a scratch snapshot of a task
  # repo's uncommitted work, superseded (non-fast-forward) or deleted at
  # every close. Deletion and force are allowed; only the objects new to
  # the repo face the carrier blob limit; no gitlink invariant.
  case "$ref" in refs/pf-wip/*)
    [ "$new" = "$zero" ] && continue
    badblob=$(git rev-list --objects "$new" --not --all | awk '{print $1}' \
      | git cat-file --batch-check='%%(objecttype) %%(objectsize) %%(objectname)' \
      | awk -v max="$MAXTASKBLOB" '$1=="blob" && $2>max {print $3" ("$2" bytes)"}') \
      || reject "blob size check failed for $ref (rev-list/cat-file pipeline)"
    [ -z "$badblob" ] || reject "blob exceeds the ${MAXTASKBLOB}-byte limit: $badblob"
    continue ;;
  esac

  [ "$new" = "$zero" ] && reject "ref deletion is not allowed ($ref)"

  if [ "$old" != "$zero" ]; then
    git merge-base --is-ancestor "$old" "$new" \
      || reject "non-fast-forward push to $ref is not allowed"
    range="$old..$new"
  else
    range="$new"
  fi

  # blob size limit (every ref; the carrier namespace has its own,
  # larger limit: task-repo history includes the upstream's).
  limit="$MAXBLOB"
  case "$ref" in refs/pf-task/*) limit="$MAXTASKBLOB" ;; esac
  badblob=$(git rev-list --objects "$range" | awk '{print $1}' \
    | git cat-file --batch-check='%%(objecttype) %%(objectsize) %%(objectname)' \
    | awk -v max="$limit" '$1=="blob" && $2>max {print $3" ("$2" bytes)"}') \
    || reject "blob size check failed for $ref (rev-list/cat-file pipeline)"
  [ -z "$badblob" ] || reject "blob exceeds the ${limit}-byte limit: $badblob"

  # Task-repo carrier namespace: foreign (task-repo) history — the
  # chain-only gitlink invariant stops here. Deletion,
  # fast-forward and the blob limit above stay for every ref.
  case "$ref" in refs/pf-task/*) continue ;; esac

  # gitlink: mode 160000 entries are submodule commits; the capsule chain
  # must stay a single flat history. Both commands are checked explicitly
  # (fail-closed), and the ls-tree output is judged line by line — a
  # failed grep's exit status would be indistinguishable from "no match".
  commits=$(git rev-list "$range") \
    || reject "gitlink check failed for $ref (git rev-list)"
  for c in $commits; do
    entries=$(git ls-tree -r "$c") \
      || reject "gitlink check failed for commit $c (git ls-tree)"
    while IFS= read -r entry; do
      case "$entry" in
      160000\ *) reject "gitlink (mode 160000) in commit $c is not allowed" ;;
      esac
    done <<ENTRIES
$entries
ENTRIES
  done

done
exit 0
`, defaultMaxBlob, defaultMaxTaskBlob)
}

// writeHook writes the pre-receive hook and makes it executable.
func writeHook(path string, maxBlob, maxTaskBlob int64) error {
	body := hookTemplate(maxBlob, maxTaskBlob)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// tmp + rename: a push running the hook, or a crash mid-write, never
	// sees a torn script
	return atomicfile.Write(path, []byte(body), 0o755)
}
