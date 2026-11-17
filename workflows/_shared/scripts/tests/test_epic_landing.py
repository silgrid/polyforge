# carries: workflows/_shared/scripts/pfstep/children.py, workflows/_shared/scripts/merge.py, workflows/_shared/scripts/pfstep/targetbranch.py, workflows/_shared/scripts/sync.py, internal/common/capsule/epicchildren.go, internal/polyforge/runtime/epicchildren.go, internal/polyforge/runtime/targetbranch.go
"""The epic's unified landing record (polyforge#117, AC3/Q6) and its sync:
an epic's capsule carries a checkout record (no target — it merges into
the default branch) and the children snapshot the claim wrote, so

- merge.py lands the epic's PRs into the default branch as ONE landed
  record that carries the snapshot's children verbatim (each child's
  landed_on_target evidence stays on its own work item and never enters
  the pool; the epic's record does), the merge_commits the actual
  default-branch landing computed;
- a sub-ticket's capsule (a target record, no snapshot) is untouched:
  landed_on_target, no children key;
- a snapshot that is there but cannot be read is not judged;
- sync.py merges the DEFAULT branch into the epic branch (the checkout
  record's capsule has no target), exactly what it does for a capsule
  without a fixed target — the children (sub-tickets) never sync the
  default branch into theirs (test_target_branch.py pins that half)."""

import json
import os
import unittest
from typing import Any, Dict, List, Optional

from pfstep import targetbranch

from .test_scripts import HAVE_GIT, World, repo_of, write
from .test_target_branch import fix_target, inputs_on, publish_on, target_branch_on


def fix_epic(w: World, children: Optional[List[Dict[str, Any]]] = None,
             version: int = 1, checkout: str = "epic/2") -> None:
    """The epic's own capsule records: the checkout branch (no target) and
    the children snapshot the claim wrote."""
    write(w.capsule, "runtime-state/" + targetbranch.FILE, json.dumps({"checkout": checkout}))
    if children is not None:
        write(w.capsule, "runtime-state/children.json",
              json.dumps({"version": version, "children": children}))


def epic_children() -> List[Dict[str, Any]]:
    """Two wrapped children, the first with its landing record, the
    second without one (its own verify-children step judges it)."""
    return [
        {"id": "wi_c1", "slug": "proj#1", "status": "wrapped",
         "landing": {"state": "landed_on_target", "target": "epic/2",
                     "repos": [{"repo": "app", "pr": "https://github.com/fake/app/pull/1",
                                "head": "aaaa", "light_verified_head": "aaaa"}],
                     "merge_commits": {"app": [{"sha": "bbbb", "merge": True}]}}},
        {"id": "wi_c2", "slug": "proj#3", "status": "wrapped", "landing": None},
    ]


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class EpicLandingTest(unittest.TestCase):
    """merge.py on an epic's capsule: the one landed record, its children
    attached, the sub-ticket's shape untouched, the snapshot read
    strictly."""

    def test_epic_lands_on_the_default_branch_and_carries_its_children(self) -> None:
        import integrate
        w = World(self, "app")
        fix_epic(w, epic_children())
        head = w.change("app", {"feature.txt": "f\n"}, "epic-section")
        # the epic's PR targets the DEFAULT branch
        entry = publish_on(w, "app", "main")
        publish, verified, out = inputs_on(w, entry, "main")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified,
                               "--landing-out", landing, "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        r = repo_of(res, "app")
        self.assertEqual(code, 0, err)
        self.assertEqual(r["how"], "merged", r)
        with open(landing, encoding="utf-8") as f:
            rec = json.load(f)
        # ONE epic record, in the integrate pool's own state
        self.assertEqual(rec["state"], "landed", rec)
        self.assertEqual(rec["state"], integrate.LANDED_FILTER.split("=", 1)[1])
        self.assertNotIn("target", rec, rec)
        # the children the claim snapshotted ride verbatim: each child's
        # landed_on_target evidence with its source
        self.assertEqual(rec["children"], epic_children(), rec)
        # the merge_commits are the actual default-branch landing
        self.assertEqual(rec["merge_commits"]["app"][0]["merge"], True, rec)
        w.git(w.seeds["app"], "fetch", "-q", "origin")
        self.assertEqual(w.git(w.seeds["app"], "rev-parse", "refs/remotes/origin/main"),
                         rec["repos"][0]["merge_commit"])
        self.assertEqual(rec["repos"][0]["head"], head)

    def test_a_sub_ticket_capsule_is_untouched(self) -> None:
        w = World(self, "app")
        fix_target(w)  # a target record, no children snapshot
        target_branch_on(w, "app")
        w.change("app", {"feature.txt": "f\n"}, "child-section")
        entry = publish_on(w, "app", "epic/1")
        publish, verified, out = inputs_on(w, entry, "epic/1")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified,
                               "--landing-out", landing, "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 0, err)
        with open(landing, encoding="utf-8") as f:
            rec = json.load(f)
        self.assertEqual(rec["state"], "landed_on_target", rec)
        self.assertEqual(rec["target"], "epic/1", rec)
        self.assertNotIn("children", rec, rec)

    def test_an_unreadable_children_snapshot_is_not_judged(self) -> None:
        w = World(self, "app")
        fix_epic(w)
        write(w.capsule, "runtime-state/children.json", "{not json")
        w.change("app", {"feature.txt": "f\n"}, "epic-section")
        entry = publish_on(w, "app", "main")
        publish, verified, out = inputs_on(w, entry, "main")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified,
                               "--landing-out", landing, "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 2, err)
        self.assertIn("children.json", res["summary"], res["summary"])
        self.assertFalse(os.path.exists(landing))

    def test_an_epic_without_a_snapshot_lands_without_children(self) -> None:
        w = World(self, "app")
        fix_epic(w, None)  # the checkout record alone (no snapshot yet)
        w.change("app", {"feature.txt": "f\n"}, "epic-section")
        entry = publish_on(w, "app", "main")
        publish, verified, out = inputs_on(w, entry, "main")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified,
                               "--landing-out", landing, "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 0, err)
        with open(landing, encoding="utf-8") as f:
            rec = json.load(f)
        self.assertEqual(rec["state"], "landed", rec)
        self.assertNotIn("children", rec, rec)


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class EpicSyncTest(unittest.TestCase):
    """sync.py on an epic's capsule: the DEFAULT branch is merged into
    the checked-out epic branch (the checkout record carries no target,
    so the default branch is the base, exactly as for a capsule without a
    fixed one)."""

    def test_sync_merges_the_default_branch_into_the_epic_branch(self) -> None:
        w = World(self, "app")
        fix_epic(w)
        seed, d = w.seeds["app"], w.dir("app")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        # the epic branch exists on origin (the children landed on it) and
        # the clone is checked out on it: the claim's own rule
        w.git(seed, "fetch", "-q", "origin")
        w.git(seed, "checkout", "-q", "-B", "epic/2", "origin/main")
        w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/epic/2")
        w.git(d, "fetch", "-q", "origin")
        w.git(d, "checkout", "-q", "-B", "epic/2", "refs/remotes/origin/epic/2")
        # the default branch moves after the epic branch's own work (in a
        # file the branch's work does not touch: a real conflict is the
        # sync agent's, not this pin's)
        head = w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "branch-section")
        main_sha = w.main_moves("app", {"main-side.txt": "the default branch moved\n"}, "main-section")
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual(code, 0, err)
        self.assertEqual((app["default_branch"], app["default_head"]), ("main", main_sha), app)
        self.assertEqual(app["conflicts"], [], app["conflicts"])
        # HEAD is the merge of the branch's work and the default branch
        self.assertEqual(w.git(d, "log", "-1", "--format=%P"), head + " " + main_sha)

    def test_sync_merges_the_checkout_branch_before_the_default(self) -> None:
        # The epic's sync takes the children's commits on its own branch
        # in FIRST (a fix child landed on origin/epic/2 while the epic
        # was parked; the resumed lane judges a tree that contains them),
        # then the default branch. Red before polyforge#117 r2: the sync
        # merged the default branch only, so the child's commit never
        # reached the epic's tree.
        w = World(self, "app")
        fix_epic(w)
        seed, d = w.seeds["app"], w.dir("app")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        # the epic branch on origin, from main's tip; the clone moves onto
        # it (the claim's own checkout) and does the epic's own work
        w.git(seed, "fetch", "-q", "origin")
        w.git(seed, "checkout", "-q", "-B", "epic/2", "origin/main")
        w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/epic/2")
        w.git(d, "fetch", "-q", "origin")
        w.git(d, "checkout", "-q", "-B", "epic/2", "refs/remotes/origin/epic/2")
        head = w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "branch-section")
        # the fix child lands on origin/epic/2 (on the branch point, in a
        # file the epic's own work does not touch): a commit the clone's
        # HEAD does not contain
        w.git(seed, "checkout", "-q", "-B", "child-work", "refs/remotes/origin/epic/2")
        write(seed, "child-fix.txt", "the child's fix\n")
        w.git(seed, "add", "-A")
        w.git(seed, "commit", "-q", "-m", "the child lands on the epic branch")
        w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/epic/2")
        child_sha = w.git(seed, "rev-parse", "HEAD")
        # the default branch moves too (in its own file)
        main_sha = w.main_moves("app", {"main-side.txt": "the default branch moved\n"}, "main-section")
        w.git(d, "fetch", "-q", "origin")
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual(code, 0, err)
        # the child's commit and the default branch's are both in HEAD
        for sha, what in ((child_sha, "the child's"), (main_sha, "the default branch's")):
            check = w.git(d, "merge-base", "--is-ancestor", sha, "HEAD")
            self.assertEqual(check, "", "%s commit %s never reached the epic's tree" % (what, sha))
        self.assertEqual(w.git(d, "merge-base", "--is-ancestor", head, "HEAD"), "")
        # the record's default_branch/default_head keep naming the default
        # branch's merge (the last one in)
        self.assertEqual((app["default_branch"], app["default_head"]), ("main", main_sha), app)
