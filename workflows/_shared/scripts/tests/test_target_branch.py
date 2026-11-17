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
from typing import Dict, Optional, Tuple

from pfstep import targetbranch

from .test_scripts import HAVE_GIT, World, read, repo_of, write


def fix_target(w: World, target: str = "epic/1") -> None:
    """The capsule's fixed target branch (the daemon's own file shape)."""
    write(w.capsule, "runtime-state/" + targetbranch.FILE,
          json.dumps({"target": target, "parent": "epic#1"}))


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
        # the file the target's conf declares append-only, on the base first
        w.main_moves("app", {"side.txt": "base\n"})
        w.git(d, "fetch", "-q", "origin")
        w.git(d, "reset", "-q", "--hard", "origin/main")
        main_before = w.git(d, "rev-parse", "refs/remotes/origin/main")
        # the branch's own change: code, the upgrade pair and side.txt's tail
        head = w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n",
                                "side.txt": "base\nbranch side\n"}, "branch-section")
        # the target branch is created from main's tip, widens its own
        # integrate.conf (append_only=side.txt) and carries its own commit
        w.git(seed, "fetch", "-q", "origin")
        w.git(seed, "checkout", "-q", "-B", "epic/1", "origin/main")
        write(seed, ".polyforge/integrate.conf", read(seed, ".polyforge/integrate.conf") + "append_only=side.txt\n")
        for rel, body in dict(w.upgrade(seed, "epic-section"), **{"side.txt": "base\nepic side\n"}).items():
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
        # the default branch's later commit never reached this clone: its
        # ref stays where the sync found it (only the target is fetched)
        self.assertEqual(w.git(d, "rev-parse", "refs/remotes/origin/main"), main_before)

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


def target_branch_on(w: World, repo: str, target: str = "epic/1") -> str:
    """origin's <target> created from main's tip (the daemon's own rule):
    answers the sha the clone sees for refs/remotes/origin/<target>."""
    seed = w.seeds[repo]
    w.git(seed, "fetch", "-q", "origin")
    w.git(seed, "checkout", "-q", "-B", target, "origin/main")
    w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/" + target)
    w.git(w.dir(repo), "fetch", "-q", "origin")
    return w.git(w.dir(repo), "rev-parse", "refs/remotes/origin/" + target)


def publish_on(w: World, repo: str, base: str) -> Dict[str, str]:
    """Push the branch, open its PR at the fake gh targeting base."""
    w.git(w.dir(repo), "push", "-q", "origin", "HEAD:refs/heads/" + World.BRANCH)
    prs = os.path.join(w.gh, repo, "prs")
    n = len(os.listdir(prs)) + 1 if os.path.isdir(prs) else 1
    url = "https://github.com/fake/%s/pull/%d" % (repo, n)
    for k, v in {"head": World.BRANCH, "base": base, "state": "OPEN", "url": url}.items():
        write(prs, str(n), k, v + "\n")
    return {"name": repo, "branch": World.BRANCH, "pr_url": url, "head": w.head(repo)}


def inputs_on(w: World, entry: Dict[str, str], base: str,
              heads: Optional[Dict[str, str]] = None) -> Tuple[str, str, str]:
    """The merge's inputs against base: publish.json and the verified
    result (its details.repos[].base = origin/<base> as verification
    recorded it), answering (publish, verified, result-out)."""
    publish = os.path.join(w.capsule, "artifacts", "publish.json")
    write(w.capsule, "artifacts/publish.json", json.dumps({"repos": [entry]}))
    verified = os.path.join(w.capsule, "artifacts", "verify", "result.json")
    base_sha = w.git(w.dir(entry["name"]), "rev-parse", "refs/remotes/origin/" + base)
    write(w.capsule, "artifacts/verify/result.json", json.dumps({
        "schema": "pf-step-result/1",
        "heads": heads or {entry["name"]: entry["head"]},
        "details": {"repos": [{"name": entry["name"], "base": base_sha}]}}))
    return publish, verified, os.path.join(w.capsule, "artifacts", "merge", "result.json")


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class MergeTargetTest(unittest.TestCase):
    """merge.py on a capsule whose target branch is fixed: both modes take
    the target as the base (the PR's target, the freshness check, the
    landing rules) and the landing record never enters the integrate
    pool (its state is landed_on_target, not landed)."""

    def test_merge_lands_on_the_target_and_registers_landed_on_target(self) -> None:
        import integrate
        w = World(self, "app")
        fix_target(w)
        target_branch_on(w, "app")
        head = w.change("app", {"feature.txt": "f\n"}, "app-section")
        entry = publish_on(w, "app", "epic/1")
        publish, verified, out = inputs_on(w, entry, "epic/1")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        r = repo_of(res, "app")
        self.assertEqual((code, r["how"], r["by_this_run"], r["branch_side"]), (0, "merged", True, head), err)
        with open(landing, encoding="utf-8") as f:
            rec = json.load(f)
        self.assertEqual((rec["state"], rec["target"]), ("landed_on_target", "epic/1"), rec)
        # the integrate pool's filter (integrate.py's LANDED_FILTER, the
        # same integration.state=landed Pi Web matches) takes landed
        # records only: this state never answers it
        self.assertNotEqual(rec["state"], integrate.LANDED_FILTER.split("=", 1)[1])
        self.assertEqual(rec["repos"][0]["head"], head)
        self.assertEqual(rec["merge_commits"]["app"][0]["merge"], True)
        # the landing is on the target branch, not the default branch
        w.git(w.seeds["app"], "fetch", "-q", "origin")
        self.assertEqual(w.git(w.seeds["app"], "rev-parse", "refs/remotes/origin/epic/1"),
                         rec["repos"][0]["merge_commit"])
        self.assertNotEqual(w.git(w.seeds["app"], "rev-parse", "refs/remotes/origin/epic/1"),
                            w.git(w.seeds["app"], "rev-parse", "refs/remotes/origin/main"))

    def test_a_moved_target_branch_fails_inside_the_uncounted_substring(self) -> None:
        w = World(self, "app")
        fix_target(w)
        target_branch_on(w, "app")
        w.change("app", {"feature.txt": "f\n"}, "app-section")
        entry = publish_on(w, "app", "epic/1")
        publish, verified, out = inputs_on(w, entry, "epic/1")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        # a sibling sub-ticket lands on the target branch after verification
        seed = w.seeds["app"]
        w.git(seed, "fetch", "-q", "origin")
        w.git(seed, "checkout", "-q", "-B", "epic/1", "origin/epic/1")
        w.git(seed, "commit", "-q", "--allow-empty", "-m", "a sibling lands on the target")
        w.git(seed, "push", "-q", "origin", "HEAD:refs/heads/epic/1")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 1, err)
        self.assertIn("the target branch moved since verification (epic/1: ", res["summary"], res["summary"])
        # the reason carries the substring the lane's uncounted rewind
        # class matches, and never the default branch's wording
        self.assertIn("branch moved since verification", res["summary"], res["summary"])
        self.assertNotIn("default branch moved", res["summary"], res["summary"])
        self.assertFalse(os.path.exists(landing))

    def test_a_pr_not_targeting_the_target_branch_is_refused(self) -> None:
        w = World(self, "app")
        fix_target(w)
        target_branch_on(w, "app")
        w.change("app", {"feature.txt": "f\n"}, "app-section")
        entry = publish_on(w, "app", "main")  # the PR targets the default branch
        publish, verified, out = inputs_on(w, entry, "main")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified,
                               "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 1, err)
        self.assertIn("targets main, not the target branch epic/1", res["summary"], res["summary"])

    def test_the_human_mode_takes_a_landing_made_on_the_target(self) -> None:
        import subprocess
        w = World(self, "app")
        fix_target(w)
        target_branch_on(w, "app")
        head = w.change("app", {"feature.txt": "f\n"}, "app-section")
        entry = publish_on(w, "app", "epic/1")
        # the person merges the PR: the fake gh's real --no-ff merge into
        # origin's target branch
        done = subprocess.run([os.path.join(w.bin, "gh"), "pr", "merge", entry["pr_url"],
                               "--merge", "--match-head-commit", head],
                              cwd=w.dir("app"), env=w.env(), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertEqual(done.returncode, 0, done.stdout.decode())
        # the claim froze this step human
        write(w.capsule, "runtime-state/step-modes.json", json.dumps({"merge": "human"}))
        publish, verified, out = inputs_on(w, entry, "epic/1")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--json", "--out", out, PF_STEP="merge")
        assert res is not None
        r = repo_of(res, "app")
        self.assertEqual((code, r["how"]), (0, "merged_by_human"), err)
        with open(landing, encoding="utf-8") as f:
            rec = json.load(f)
        self.assertEqual((rec["state"], rec["target"]), ("landed_on_target", "epic/1"), rec)

    def test_an_unreadable_fixed_file_is_not_judged(self) -> None:
        w = World(self, "app")
        w.change("app", {"feature.txt": "f\n"})
        entry = w.publish("app")
        write(w.capsule, "runtime-state/" + targetbranch.FILE, "{not json")
        publish, verified, out = inputs_on(w, entry, "main")
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        self.assertEqual(code, 2, err)
        self.assertIn("not judged", res["summary"], res["summary"])
        self.assertIn("not valid JSON", res["summary"], res["summary"])
        self.assertFalse(os.path.exists(landing))
