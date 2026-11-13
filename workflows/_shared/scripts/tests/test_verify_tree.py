"""verify.py's view of a task repo's tree: an untouched repo runs no bar, a
touched one is verified at HEAD with nothing fetched or merged (the
merge-base its base), a merge in progress or an unresolvable default branch
is not judged, and the default branch falls back to main, else master —
ported from verifyrun/gitrepo_test.go."""

import json
import os
import subprocess
import tempfile
import unittest
from typing import Any, Dict

import verify

from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    engine_args,
    fake_engine,
    git_t,
    report,
    run_verify,
)


def trivial_plan(root: str, *repos: str) -> str:
    """An engine answering one passing full command per repo."""
    return fake_engine(root, {r: report("full", cmd_of("full-1", "full", "cat a.txt")) for r in repos})


def merge_in_progress(repo: str) -> bool:
    return subprocess.run(["git", "rev-parse", "-q", "--verify", "MERGE_HEAD"], cwd=repo,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False).returncode == 0


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class TreeTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def test_untouched_repo(self) -> None:
        """Port of TestUntouchedRepo (gitrepo_test.go): a repo with no commit
        beyond its default branch is verified as it stands (no fetch, no
        merge: outcome none), recorded untouched (skipped) and runs no bar —
        the engine is never asked."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        eng = trivial_plan(self.root, "app")
        r = run_verify(fx.capsule, eng)
        d = self.doc(r)
        m = d["merge_outcomes"].get("app")
        self.assertIsNotNone(m, d["merge_outcomes"])
        assert m is not None
        self.assertEqual((m["outcome"], m["touched"], m["default_branch"]), (verify.MERGE_NONE, False, "main"), m)
        c = r.check("app:untouched")
        self.assertEqual(c["status"], "skipped", c)
        self.assertEqual(engine_args(eng), [], "the engine must not be asked about an untouched repo")
        self.assertFalse(os.path.exists(os.path.join(os.path.dirname(eng), "args.log")))
        self.assertEqual(d["mechanical_verdict"], "pass")
        self.assertEqual(r.exit, 0, r.result.get("summary"))

    def test_touched_repo_verified_at_head(self) -> None:
        """Port of TestTouchedRepoVerifiedAtHead (gitrepo_test.go): the
        branch's own commit is verified at HEAD with nothing fetched or
        merged — a default branch that moved on origin is not taken in — and
        the engine is asked for the light plan with the merge-base as its
        base."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        base = fx.head("app")
        head = fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
        fx.advance_origin("app", {"b.txt": "upstream\n"}, "upstream")
        eng = trivial_plan(self.root, "app")
        r = run_verify(fx.capsule, eng)
        m = self.doc(r)["merge_outcomes"]["app"]
        self.assertEqual((m["outcome"], m["touched"], m["head"], m["pre_head"], m["base"]),
                         (verify.MERGE_NONE, True, head, head, base), m)
        self.assertEqual(fx.head("app"), head, "HEAD moved: the run merges nothing")
        args = "\n".join(engine_args(eng))
        self.assertIn("--json --light --base " + base, args)
        c = r.check("app:full-1")
        self.assertEqual(c["status"], "pass", c)

    def test_merge_in_progress_is_incomplete(self) -> None:
        """Port of TestMergeInProgressIsIncomplete (gitrepo_test.go): a merge
        left in progress (MERGE_HEAD) is no verified state — the result is
        incomplete (not judged), no bar runs, the merge is left as it is and
        result.json is still written."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"a.txt": "mine\n"}, "feature")
        fx.advance_origin("app", {"a.txt": "theirs\n"}, "upstream")
        git_t(fx.repo("app"), "fetch", "-q", "origin")
        subprocess.run(["git", "merge", "--no-edit", "origin/main"], cwd=fx.repo("app"),
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        self.assertTrue(merge_in_progress(fx.repo("app")), "setup: no merge in progress")
        r = run_verify(fx.capsule, trivial_plan(self.root, "app"))
        d = self.doc(r)
        self.assertIn("a merge is in progress (MERGE_HEAD)", d.get("incomplete", ""))
        self.assertEqual((d["checks"], d["mechanical_verdict"]), ([], ""), "an incomplete run reports no verdict")
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        self.assertTrue(merge_in_progress(fx.repo("app")), "the run must leave the sync's merge as it is")
        self.assertTrue(os.path.isfile(os.path.join(fx.capsule, d["out_dir"], "result.json")), "result.json must still be written")

    def test_unresolvable_default_is_incomplete(self) -> None:
        """Port of TestUnresolvableDefaultIsIncomplete (gitrepo_test.go): with
        no origin default branch in the clone there is nothing to measure
        the change against: the result is incomplete and no bar runs."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
        git_t(fx.repo("app"), "remote", "set-head", "origin", "--delete")
        git_t(fx.repo("app"), "update-ref", "-d", "refs/remotes/origin/main")
        r = run_verify(fx.capsule, trivial_plan(self.root, "app"))
        d = self.doc(r)
        self.assertIn("the origin default branch is unresolvable", d.get("incomplete", ""))
        self.assertEqual((d["checks"], d["mechanical_verdict"]), ([], ""), "an incomplete run reports no verdict")
        self.assertEqual(r.exit, 2, r.result.get("summary"))

    def test_default_branch_fallback(self) -> None:
        """Port of TestDefaultBranchFallback (gitrepo_test.go): without
        origin/HEAD the default branch is main, else master — always without
        the remote prefix."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        d = fx.repo("app")
        self.assertEqual(verify.resolve_default(d), "main", "origin/HEAD default")
        git_t(d, "remote", "set-head", "origin", "--delete")
        self.assertEqual(verify.resolve_default(d), "main", "fallback")
        git_t(d, "update-ref", "refs/remotes/origin/master", "refs/remotes/origin/main")
        git_t(d, "update-ref", "-d", "refs/remotes/origin/main")
        self.assertEqual(verify.resolve_default(d), "master", "fallback")


if __name__ == "__main__":
    unittest.main()
