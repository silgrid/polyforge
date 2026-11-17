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
import subprocess
import sys
import tempfile
import unittest
from typing import Any, Dict, Optional, Tuple

import facts
import verify
from pfstep import targetbranch

from .gitworld import GitCase, Repo
from .test_scripts import HAVE_GIT, SCRIPTS, World, read, repo_of, write
from .test_verify_tree import trivial_plan
from .verifyfix import HAVE_BASH, Fixture, Run, engine_args, git_t, run_verify, write_files


def fix_target(w: World, target: str = "epic/1") -> None:
    """The capsule's fixed target branch (the daemon's own file shape)."""
    write(w.capsule, "runtime-state/" + targetbranch.FILE,
          json.dumps({"target": target, "parent": "epic#1"}))


def fix_target_dir(capsule_dir: str, target: str = "epic/1") -> None:
    """The capsule root's fixed target branch file (tests' direct helper)."""
    write(os.path.join(capsule_dir, "runtime-state"), targetbranch.FILE,
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
        write(os.path.join(prs, str(n)), k, v + "\n")
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
        self.assertEqual(code, 0, err)
        self.assertEqual((r["how"], r["by_this_run"], r["branch_side"]), ("merged", True, head), r)
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
        self.assertEqual(code, 0, err)
        self.assertEqual(r["how"], "merged_by_human", r)
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


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class VerifyTargetTest(unittest.TestCase):
    """verify.py on a capsule whose target branch is fixed: the untouched
    judgment and the diff base (and the engine's --base, the baseline
    with it) take merge-base HEAD origin/<target>, and a target that
    cannot be named is not judged — never a fall-back to the default
    branch. A sibling's commit already on the target branch is not this
    change's."""

    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def fix_target(self, fx: Fixture, target: str = "epic/1") -> None:
        write_files(fx.capsule, {"runtime-state/target-branch.json": json.dumps({"target": target, "parent": "epic#1"})})

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def test_the_target_branch_decides_untouched_and_the_diff_base(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        self.fix_target(fx)
        base = fx.head("app")
        head = fx.commit_in_repo("app", {"c.txt": "mine\n"}, "the sub-ticket's own commit")
        # the sibling landed that very commit on the target branch: with
        # the target fixed the repo carries nothing beyond origin/epic/1
        # (against the default branch it would be a touched repo)
        git_t(fx.repo("app"), "push", "-q", "origin", "HEAD:refs/heads/epic/1")
        git_t(fx.repo("app"), "fetch", "-q", "origin")
        eng = trivial_plan(self.root, "app")
        r = run_verify(fx.capsule, eng)
        m = self.doc(r)["merge_outcomes"]["app"]
        self.assertEqual((m["outcome"], m["touched"], m["default_branch"], m["base"]),
                         (verify.MERGE_NONE, False, "epic/1", head), m)
        self.assertEqual(engine_args(eng), [], "the engine must not be asked about an untouched repo")
        self.assertEqual(r.check("app:untouched")["status"], "skipped")
        # a commit of the item's own beyond the target branch: measured
        # from merge-base HEAD origin/epic/1, never origin/main
        beyond = fx.commit_in_repo("app", {"d.txt": "beyond\n"}, "beyond the target")
        eng2 = trivial_plan(self.root, "app")
        r = run_verify(fx.capsule, eng2)
        m = self.doc(r)["merge_outcomes"]["app"]
        self.assertEqual((m["outcome"], m["touched"], m["head"], m["base"], m["default_branch"]),
                         (verify.MERGE_NONE, True, beyond, head, "epic/1"), m)
        self.assertIn("--json --light --base " + head, "\n".join(engine_args(eng2)))
        self.assertEqual(r.check("app:full-1")["status"], "pass")

    def test_an_unreadable_fixed_file_is_incomplete(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        write_files(fx.capsule, {"runtime-state/target-branch.json": "{not json"})
        fx.commit_in_repo("app", {"c.txt": "mine\n"}, "change")
        r = run_verify(fx.capsule, trivial_plan(self.root, "app"))
        d = self.doc(r)
        self.assertIn(targetbranch.FILE, d.get("incomplete", ""))
        self.assertIn("not valid JSON", d.get("incomplete", ""))
        self.assertEqual(r.exit, 2, r.result.get("summary"))

    def test_a_target_missing_on_origin_is_incomplete(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        self.fix_target(fx, "epic/9")  # origin carries no epic/9
        fx.commit_in_repo("app", {"c.txt": "mine\n"}, "change")
        r = run_verify(fx.capsule, trivial_plan(self.root, "app"))
        d = self.doc(r)
        self.assertIn("origin has no branch epic/9", d.get("incomplete", ""))
        self.assertEqual(r.exit, 2, r.result.get("summary"))


@unittest.skipUnless(HAVE_GIT, "git not installed")
class FactsTargetTest(GitCase):
    """facts.py on a capsule whose target branch is fixed: every change
    base (changed_files, change_base, the test paths' conf, the micro
    bounds) is merge-base HEAD origin/<target>, so a sibling's commit
    already on the target branch is not this change's; a target that
    cannot be named is an error naming why, never the default branch."""

    def world(self) -> Tuple[str, Repo]:
        """A capsule-shaped tmp (the capsule root is it, its repos/ holds
        app) with the repo's base on origin/main."""
        r = Repo(os.path.join(self.tmp, "repos", "app"), {"base.txt": "base\n"})
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        return os.path.join(self.tmp, "repos"), r

    def test_changed_files_and_change_base_take_the_target(self) -> None:
        repos_dir, r = self.world()
        # the sibling's commit is the target branch's tip on origin: the
        # change is measured from it, not from origin/main
        sibling = r.commit("the sibling lands on the target", {"docs/sibling.txt": "s\n"})
        r.git("update-ref", "refs/remotes/origin/epic/1", sibling)
        r.commit("mine", {"docs/mine.txt": "m\n"})
        fix_target_dir(self.tmp)
        changed, why = facts.changed_files(repos_dir)
        self.assertEqual((why, changed), ("", {"app": ["docs/mine.txt"]}), why)
        base, why = facts.change_base(os.path.join(repos_dir, "app"))
        self.assertEqual((why, base), ("", sibling), why)

    def test_the_test_paths_come_from_the_conf_at_the_target_base(self) -> None:
        repos_dir, r = self.world()
        sibling = r.commit("the sibling lands on the target",
                           {".polyforge/verify-scope.conf": "test_glob=*_test.py\n", "docs/sibling.txt": "s\n"})
        r.git("update-ref", "refs/remotes/origin/epic/1", sibling)
        r.commit("mine", {"docs/mine.txt": "m\n"})
        fix_target_dir(self.tmp)
        rules = facts.TestPathRules.at(os.path.join(repos_dir, "app"))
        self.assertIn("*_test.py", rules.globs, rules.globs)

    def test_micro_bounds_judge_from_the_target_base(self) -> None:
        repos_dir, r = self.world()
        sibling = r.commit("the sibling lands on the target",
                           {".polyforge/verify-scope.conf": "micro_path=docs/**\nmicro_check=echo micro-ok\n",
                            "docs/sibling.txt": "s\n"})
        r.git("update-ref", "refs/remotes/origin/epic/1", sibling)
        r.commit("mine", {"docs/mine.txt": "m\n"})
        fix_target_dir(self.tmp)
        v = facts.micro_bounds_of(repos_dir, [{"repo": "app", "command": "echo fast-ok"}])
        self.assertTrue(v["in_bounds"], v)
        self.assertEqual(v["repos"][0]["base"], sibling, v["repos"][0])

    def test_a_target_that_cannot_be_named_is_an_error_naming_why(self) -> None:
        repos_dir, r = self.world()
        r.commit("mine", {"docs/mine.txt": "m\n"})
        for body, want in [("{not json", "not valid JSON"),
                           (json.dumps({"target": "epic/9", "parent": "epic#1"}), "origin has no branch epic/9")]:
            with self.subTest(want):
                write(os.path.join(self.tmp, "runtime-state"), targetbranch.FILE, body)
                changed, why = facts.changed_files(repos_dir)
                self.assertIsNone(changed, why)
                self.assertIn(want, why, why)
                base, why = facts.change_base(os.path.join(repos_dir, "app"))
                self.assertEqual(base, "", why)
                self.assertIn(want, why, why)


@unittest.skipUnless(HAVE_GIT, "git not installed")
class EngineTargetTest(GitCase):
    """verify-scope inside a step (PF_CAPSULE names the capsule): the
    default diff base — and --micro's, whose governing conf is read at it
    — is merge-base HEAD origin/<target>, so a sibling's commit already
    on the target branch is not this change's; outside a step nothing
    changes, and a fixed target that cannot be read is diagnosed and
    answers the full tier — never a fall-back to the default branch."""

    ENGINE = os.path.join(SCRIPTS, "verify-scope")

    def repo_with(self) -> Tuple[str, Repo]:
        r = Repo(os.path.join(self.tmp, "repos", "app"), {"base.txt": "base\n"})
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        return r.head(), r

    def run_engine(self, cwd: str, *flags: str, capsule: str = "") -> Dict[str, Any]:
        """The repo's own engine as a subprocess, in a step's environment
        (PF_CAPSULE names the capsule) or outside one (the variable
        stripped, a hand run's)."""
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_"))}
        if capsule:
            env["PF_CAPSULE"] = capsule
        p = subprocess.run([sys.executable, self.ENGINE, "--json", *flags], cwd=cwd, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(p.returncode, 0, p.stderr.decode())
        return json.loads(p.stdout.decode("utf-8", "replace"))

    def test_the_step_capsules_target_is_the_default_base(self) -> None:
        b0, r = self.repo_with()
        sibling = r.commit("the sibling lands on the target",
                           {".polyforge/verify-scope.conf": "fast_gate=echo epic-ok\n", "docs/s.txt": "s\n"})
        r.git("update-ref", "refs/remotes/origin/epic/1", sibling)
        r.commit("mine", {"docs/mine.txt": "m\n"})
        fix_target_dir(self.tmp)
        rep = self.run_engine(r.dir, capsule=self.tmp)
        self.assertEqual((rep["default_branch"], rep["base"]), ("epic/1", sibling), rep)
        # outside a step (no PF_CAPSULE): the default branch again
        rep = self.run_engine(r.dir)
        self.assertEqual((rep["default_branch"], rep["base"]), ("main", b0), rep)

    def test_micro_reads_the_conf_at_the_targets_base(self) -> None:
        b0, r = self.repo_with()
        sibling = r.commit("the sibling lands on the target",
                           {".polyforge/verify-scope.conf": "micro_path=docs/**\nmicro_check=echo epic-ok\n",
                            "docs/s.txt": "s\n"})
        r.git("update-ref", "refs/remotes/origin/epic/1", sibling)
        r.commit("mine", {"docs/mine.txt": "m\n"})
        fix_target_dir(self.tmp)
        rep = self.run_engine(r.dir, "--micro", capsule=self.tmp)
        self.assertEqual((rep["default_branch"], rep["base"]), ("epic/1", sibling), rep)
        self.assertIn("echo epic-ok", [c["cmd"] for c in rep["commands"]], rep["commands"])
        # outside a step the governing conf is the default branch's base's
        rep = self.run_engine(r.dir, "--micro")
        self.assertEqual((rep["default_branch"], rep["base"]), ("main", b0), rep)
        self.assertNotIn("echo epic-ok", [c["cmd"] for c in rep["commands"]], rep["commands"])

    def test_an_unreadable_fixed_target_is_diagnosed_never_fallen_back(self) -> None:
        _, r = self.repo_with()
        r.commit("mine", {"docs/mine.txt": "m\n"})
        write(os.path.join(self.tmp, "runtime-state"), targetbranch.FILE, "{not json")
        rep = self.run_engine(r.dir, capsule=self.tmp)
        self.assertTrue(any(targetbranch.FILE in d and "not valid JSON" in d for d in rep["diagnostics"]),
                        rep["diagnostics"])
        # the base falls back to nothing — the default branch is never the answer
        self.assertEqual(rep["default_branch"], "", rep)
        rep = self.run_engine(r.dir)  # outside a step the file is not read at all
        self.assertEqual(rep["default_branch"], "main", rep)
