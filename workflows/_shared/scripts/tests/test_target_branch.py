# carries: workflows/_shared/scripts/pfstep/targetbranch.py, workflows/_shared/scripts/sync.py, workflows/_shared/scripts/merge.py, workflows/_shared/scripts/pfstep/gitmerge/git.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/facts.py, workflows/_shared/scripts/verify-scope, workflows/_shared/scripts/tests/test_verify_tiers.py
"""The sub-ticket's fixed target branch (runtime-state/target-branch.json)
through every step script that reads it: the reader's own rules, sync's
merge, merge's base and landing record, verify's diff base, the facts'
change base and the scope engine's default base. Before the change every
script bases the work on the origin's default branch and none reads the
file, so each test below is red on the tree it was first committed on:
a target-branch capsule gets main merged, its PRs judged against main,
its diff measured from merge-base HEAD origin/main."""

import json
import os
import shutil
import tempfile
import unittest

from pfstep import targetbranch

from .test_scripts import HAVE_GIT, World, read, repo_of, write


class TargetBranchReadTest(unittest.TestCase):
    """pfstep.targetbranch.read's own rules (the one reader every step
    script's base resolution goes through)."""

    def setUp(self) -> None:
        self.tmp = tempfile.mkdtemp(prefix="pfstep-tb-")
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def capsule(self, body: str = "") -> str:
        d = os.path.join(self.tmp, "capsule")
        if body != "":
            write(d, os.path.join("runtime-state", targetbranch.FILE), body)
        return d

    def test_no_file_no_target(self) -> None:
        self.assertEqual(targetbranch.read(self.capsule()), "")

    def test_the_fixed_target_and_its_parent(self) -> None:
        d = self.capsule(json.dumps({"target": "epic/1", "parent": "epic#1"}))
        self.assertEqual(targetbranch.read(d), "epic/1")

    def test_a_missing_or_empty_target_is_no_target(self) -> None:
        self.assertEqual(targetbranch.read(self.capsule(json.dumps({"parent": "epic#1"}))), "")
        self.assertEqual(targetbranch.read(self.capsule('{"target": ""}')), "")

    def test_the_target_is_taken_stripped(self) -> None:
        d = self.capsule('{"target": " epic/1\\n"}')
        self.assertEqual(targetbranch.read(d), "epic/1")

    def test_a_file_that_cannot_be_read_names_why(self) -> None:
        # a directory in the file's place: open() fails, never a fall-back
        d = self.capsule()
        os.makedirs(os.path.join(d, "runtime-state", targetbranch.FILE), exist_ok=True)
        with self.assertRaises(targetbranch.TargetBranchError) as ctx:
            targetbranch.read(d)
        self.assertIn("unreadable", str(ctx.exception))

    def test_invalid_json_names_why(self) -> None:
        with self.assertRaises(targetbranch.TargetBranchError) as ctx:
            targetbranch.read(self.capsule("{not json"))
        self.assertIn("not valid JSON", str(ctx.exception))

    def test_a_non_object_names_why(self) -> None:
        for body in ('["epic/1"]', '"epic/1"', "3"):
            with self.subTest(body):
                with self.assertRaises(targetbranch.TargetBranchError) as ctx:
                    targetbranch.read(self.capsule(body))
                self.assertIn("not the JSON object expected", str(ctx.exception))

    def test_a_non_string_target_names_why(self) -> None:
        with self.assertRaises(targetbranch.TargetBranchError) as ctx:
            targetbranch.read(self.capsule('{"target": 3}'))
        self.assertIn("target is not a string", str(ctx.exception))


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class SyncTargetTest(unittest.TestCase):
    """sync.py on a capsule whose target branch is fixed: it fetches and
    merges origin/<target> only — never the default branch — and takes the
    conflicts the TARGET's integrate.conf declares mechanical."""

    def fix_target(self, w: World, target: str = "epic/1") -> None:
        write(w.capsule, "runtime-state/" + targetbranch.FILE,
              json.dumps({"target": target, "parent": "epic#1"}))

    def test_sync_merges_the_target_branch_not_the_default(self) -> None:
        w = World(self, "app")
        self.fix_target(w)
        seed, d = w.seeds["app"], w.dir("app")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        # the branch's own change: code, the upgrade pair and side.txt
        head = w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n",
                                "side.txt": "branch side\n"}, "branch-section")
        # the target branch is created from main's tip, widens its own
        # integrate.conf (append_only=side.txt) and carries its own commit
        w.git(seed, "fetch", "-q", "origin")
        w.git(seed, "checkout", "-q", "-B", "epic/1", "origin/main")
        write(seed, ".polyforge/integrate.conf", read(seed, ".polyforge/integrate.conf") + "append_only=side.txt\n")
        for rel, body in dict(w.upgrade(seed, "epic-section"), **{"side.txt": "epic side\n"}).items():
            write(seed, rel, body)
        target_head = w.commit(seed, "the epic branch's commit")
        w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/epic/1")
        # the default branch moves on origin after that: never merged in
        main_sha = w.main_moves("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "main-section")
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual(code, 0, err)
        # the record's default_branch/default_head fields keep their names
        # and carry the branch that was merged: the target's
        self.assertEqual((app["default_branch"], app["default_head"]), ("epic/1", target_head), err)
        # side.txt is mechanical only by the TARGET's integrate.conf: with
        # the default branch's it is a real conflict and the sync fails
        self.assertIn("side.txt", app["mechanical"], app["mechanical"])
        self.assertEqual(app["conflicts"], [], app["conflicts"])
        # HEAD is the merge of the branch and the target branch's commit
        self.assertEqual(w.git(d, "log", "-1", "--format=%P"), head + " " + target_head)
        # the default branch's later commit is fetched by nobody, merged by nobody
        self.assertEqual(w.git(d, "rev-list", "--count", main_sha, "--not", "HEAD"), "1")

    def test_an_unreadable_fixed_file_refuses_the_sync(self) -> None:
        w = World(self, "app")
        w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"})
        write(w.capsule, "runtime-state/" + targetbranch.FILE, "{not json")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        self.assertEqual(code, 2, err)
        self.assertIn(targetbranch.FILE, res["summary"], res["summary"])
        self.assertIn("not valid JSON", res["summary"], res["summary"])

    def test_a_target_missing_on_origin_refuses_the_sync(self) -> None:
        w = World(self, "app")
        self.fix_target(w)  # origin carries no epic/1
        w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"})
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        self.assertEqual(code, 2, err)
        self.assertIn("fetching epic/1", res["summary"], res["summary"])
