"""verify.py's plan and how it runs: the whole plan past the first failure,
unmet preconditions skipped, the light plan asked for (and the full fallback
named), the light tier alone, an engine failure unplanned, the conf change
and the engine's diagnostics surfaced, the logs out of artifacts/ and the
capsule chain, a command's timeout and the whole run's deadline, a private
TMPDIR per command, untracked test files, and a required command resolved in
the repo — ported from verifyrun/plan_test.go. (Its TestDefaultDeadlineFor
is tests/test_deadline.py's DeadlineTest.test_the_go_cases.)"""

import io
import json
import os
import tempfile
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from typing import Any, Dict, Tuple

import verify
from pfstep.runner import TIMEOUT_EXIT

from .fakebox import FakeRuntime
from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    engine_args,
    executable,
    fake_engine,
    git_t,
    report,
    run_verify,
    step_env,
    write_files,
)


def touched(root: str) -> Fixture:
    """A fixture whose one repo carries a commit of its own."""
    fx = Fixture(root, {"app": {"a.txt": "base\n"}})
    fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
    return fx


def run_in_capsule(capsule: str, engine: str) -> Tuple[int, Dict[str, Any], Dict[str, Any]]:
    """verify.py with neither --out nor --log-dir (the logs go to the
    capsule's next runtime-state/verify/<n>): its exit, the result it
    printed (--json) and its result.json."""
    so, se = io.StringIO(), io.StringIO()
    with redirect_stdout(so), redirect_stderr(se):
        code = verify.main(["--tier", "light", "--capsule", capsule, "--engine", engine, "--json"], step_env(),
                           runtime=FakeRuntime(os.path.join(os.path.dirname(capsule), "box-tmp")))
    lines = [ln for ln in so.getvalue().split("\n") if ln.strip()]
    assert lines, "no result printed; stderr:\n" + se.getvalue()
    res = json.loads(lines[-1])
    with open(os.path.join(capsule, res["details"]["result"]), encoding="utf-8") as f:
        doc = json.load(f)
    return code, res, doc


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class PlanTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def test_whole_plan_runs_past_first_failure(self) -> None:
        """Port of TestWholePlanRunsPastFirstFailure (plan_test.go): the plan
        never stops at the first failure — every command runs and is
        recorded, in order."""
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("lint", "fast", "echo lint broke; exit 3"),
                                                    cmd_of("unit", "full", "echo all good"))})
        r = run_verify(fx.capsule, eng)
        c = r.check("app:lint")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("exit 3", c.get("detail", ""), c)
        c = r.check("app:unit")
        self.assertEqual(c["status"], "pass", "the second command must still run: %s" % c)
        d = self.doc(r)
        self.assertEqual(d["mechanical_verdict"], "fail")
        self.assertIn("app:lint", d.get("fail_reason", ""))
        self.assertEqual(r.exit, 1, r.result.get("summary"))
        cs = d["repos"][0]["commands"]
        self.assertEqual([c["command"] for c in cs], ["echo lint broke; exit 3", "echo all good"], cs)

    def test_unmet_precondition_skipped(self) -> None:
        """Port of TestUnmetPreconditionSkipped (plan_test.go): a declared
        requires_env / requires_cmd that does not hold skips the command
        with the missing names; it never runs."""
        fx = touched(self.root)
        c = cmd_of("browser", "full", "touch ran.txt", requires_env=["VR_NO_SUCH_VAR"], requires_cmd=["vr-no-such-binary"])
        ck = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", c)})).check("app:browser")
        self.assertEqual(ck["status"], "skipped", ck)
        self.assertIn("env VR_NO_SUCH_VAR", ck.get("detail", ""), ck)
        self.assertIn("command vr-no-such-binary", ck.get("detail", ""), ck)
        self.assertFalse(os.path.exists(os.path.join(fx.repo("app"), "ran.txt")), "a skipped command must not run")

    def test_run_asks_for_the_light_plan(self) -> None:
        """Port of TestRunAsksForTheLightPlan (plan_test.go): the engine is
        asked with --light, never --full: a scoped report runs the fast gates
        plus the scoped selection and proves the light tier; a full report
        (the engine's own fallback) runs the fast gates plus the full tail
        and proves only the full tier, naming why."""
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("fast-1", "fast", "true"), cmd_of("scoped-1", "scoped", "echo s"))})
        d = self.doc(run_verify(fx.capsule, eng))
        cs = d["repos"][0]["commands"]
        self.assertEqual([c["name"] for c in cs], ["fast-1", "scoped-1"], cs)
        self.assertEqual((d["tier"]["requested"], d["tier"]["effective"]), ("light", "light"), d["tier"])
        ref = d["scope"]["fast_gates_ref"]
        self.assertIn(eng + " --json", ref)
        self.assertIn("conf present", ref)
        args = "\n".join(engine_args(eng))
        self.assertIn("app --json --light --base ", args)
        self.assertNotIn("--full", args, "want --json --light --base and never --full")
        full = fake_engine(self.root, {"app": report("full", cmd_of("fast-1", "fast", "true"), cmd_of("full-1", "full", "echo full"))})
        d = self.doc(run_verify(fx.capsule, full, out=os.path.join(fx.capsule, "artifacts", "verify-2", "result.json")))
        self.assertEqual([c["name"] for c in d["repos"][0]["commands"]], ["fast-1", "full-1"], "want the full plan")
        self.assertEqual(d["tier"]["effective"], "full", d["tier"])
        reasons = d["tier"].get("reasons") or [""]
        self.assertTrue(reasons[0].startswith("repos/app: "), "want why the run is full: %s" % d["tier"])

    def test_run_refuses_any_tier_but_light(self) -> None:
        """Port of TestRunRefusesAnyTierButLight (plan_test.go): the full tier
        is the gate's; the light tier alone runs and any other is refused
        before anything runs (exit 2, the reason on stderr)."""
        fx = touched(self.root)
        for tier in ("", "full", "scoped"):
            with self.subTest(tier=tier):
                r = run_verify(fx.capsule, "/x", "--tier", tier)
                self.assertEqual(r.exit, 2, r.stderr)
                # Go: "the full tier is pf-gate's"; verify.py names the gate step "the gate"
                self.assertIn("the full tier is the gate's", r.stderr)
                self.assertIn("the full tier is the gate's", r.result.get("summary", ""), r.result)
                self.assertFalse(os.path.exists(os.path.join(fx.capsule, "runtime-state")), "nothing runs")
                self.assertIsNone(r.doc)

    def test_engine_failure_is_unplanned_and_full(self) -> None:
        """Port of TestEngineFailureIsUnplannedAndFull (plan_test.go): a
        failing engine leaves the repo unplanned, and the light request can
        no longer be an effective light run."""
        fx = touched(self.root)
        d = self.doc(run_verify(fx.capsule, fake_engine(self.root, {})))  # no report: exits 3
        self.assertEqual(d["repos"][0].get("plan"), verify.PLAN_ENGINE_FAILED, d["repos"])
        self.assertEqual(d["tier"]["effective"], "full", d["tier"])
        self.assertEqual(len(d["unplanned_repos"]), 1, d["unplanned_repos"])

    def test_conf_changed_is_visible(self) -> None:
        """Port of TestConfChangedIsVisible (plan_test.go): a conf changed in
        the diff is a visible skipped hint check and a result fact."""
        fx = touched(self.root)
        rep = report("full", cmd_of("full-1", "full", "true"), conf_changed_in_diff=True)
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": rep}))
        c = r.check("app:conf-changed")
        self.assertEqual(c["status"], "skipped", c)
        self.assertIs(self.doc(r)["conf_changed_in_diff"].get("app"), True, self.doc(r)["conf_changed_in_diff"])

    def test_logs_stay_out_of_artifacts_and_the_chain(self) -> None:
        """Port of TestLogsStayOutOfArtifactsAndTheChain (plan_test.go): with
        no --log-dir and no --out, logs land under
        runtime-state/verify/<n>/<repo>/<k>.log behind a .gitignore that
        keeps the tree out of the capsule chain; nothing lands under
        artifacts/."""
        fx = touched(self.root)
        _, _, d = run_in_capsule(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("full-1", "full", "echo hi"))}))
        with open(os.path.join(fx.capsule, "runtime-state", "verify", ".gitignore"), encoding="utf-8") as f:
            self.assertIn("\n*\n", f.read())
        self.assertEqual((d["run"], d["out_dir"]), (1, "runtime-state/verify/1"))
        self.assertFalse(os.path.exists(os.path.join(fx.capsule, "artifacts")), "verify-run must write nothing under artifacts/")
        cs = d["repos"][0]["commands"]
        self.assertEqual(len(cs), 1, cs)
        self.assertTrue(cs[0].get("log_path", "").startswith("runtime-state/verify/1/app/"), cs)
        self.assertTrue(cs[0].get("log_path", "").endswith(".log"), cs)
        # a second run is run 2, and git sees nothing to add
        _, _, d2 = run_in_capsule(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("full-1", "full", "echo hi"))}))
        self.assertEqual(d2["run"], 2)
        git_t(fx.capsule, "init", "-q")
        st = git_t(fx.capsule, "status", "--porcelain", "--untracked-files=all", "--", "runtime-state")
        self.assertEqual(st, "", "runtime-state/verify is visible to the capsule chain")

    def test_timeout_kills_the_command(self) -> None:
        """Port of TestTimeoutKillsTheCommand (plan_test.go): a command over
        its declared timeout is killed (its process group with it) and
        fails."""
        fx = touched(self.root)
        c = cmd_of("slow", "full", "(while :; do :; done) & wait", timeout="300ms")
        start = time.monotonic()
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", c)}))
        took = time.monotonic() - start
        self.assertLessEqual(took, 20.0, "took %.1fs" % took)
        ck = r.check("app:slow")
        self.assertEqual(ck["status"], "fail", ck)
        self.assertIn("timeout", ck.get("detail", ""), ck)
        cmd = self.doc(r)["repos"][0]["commands"][0]
        self.assertEqual(cmd["exit_code"], TIMEOUT_EXIT, cmd)

    def test_private_tmpdir(self) -> None:
        """Port of TestPrivateTmpdir (plan_test.go): every command gets its own
        TMPDIR (removed afterwards)."""
        fx = touched(self.root)
        out = os.path.join(self.root, "env.txt")
        cmd = 'echo "$TMPDIR" >> ' + out
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("a", "full", cmd), cmd_of("b", "full", cmd))}))
        d = self.doc(r)
        self.assertEqual(d["mechanical_verdict"], "pass", d.get("fail_reason"))
        with open(out, encoding="utf-8") as f:
            raw = f.read()
        ls = raw.strip().split("\n")
        self.assertEqual(len(ls), 2, raw)
        a, b = ls[0].split(), ls[1].split()
        self.assertEqual((len(a), len(b)), (1, 1), raw)
        self.assertNotEqual(a[0], b[0], raw)
        self.assertNotEqual(a[0], step_env().get("TMPDIR", ""), raw)
        self.assertFalse(os.path.exists(a[0]), "private TMPDIR %s survived" % a[0])

    def test_each_command_record_names_its_private_tmpdir(self) -> None:
        """each command record in result.json carries its
        private TMPDIR's real path (tmp_dir), so its length can be checked;
        the report's failure output keeps <tmp>."""
        fx = touched(self.root)
        out = os.path.join(self.root, "env.txt")
        cmd = 'echo "$TMPDIR" >> ' + out
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("a", "full", cmd), cmd_of("b", "full", cmd))}))
        d = self.doc(r)
        self.assertEqual(d["mechanical_verdict"], "pass", d.get("fail_reason"))
        with open(out, encoding="utf-8") as f:
            raw = f.read()
        seen = raw.strip().split("\n")
        self.assertEqual(len(seen), 2, raw)
        self.assertEqual([c.get("tmp_dir") for c in d["repos"][0]["commands"]], seen, raw)

    def test_the_report_and_the_result_file_name_each_private_tmpdir(self) -> None:
        """ (r1#1): the report (report-mechanical.md) and the
        result file's details.checks[] — what a reader of the capsule sees —
        carry each command's private TMPDIR and its length in bytes."""
        fx = touched(self.root)
        out = os.path.join(self.root, "env.txt")
        cmd = 'echo "$TMPDIR" >> ' + out
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("a", "full", cmd), cmd_of("b", "full", cmd))}))
        d = self.doc(r)
        self.assertEqual(d["mechanical_verdict"], "pass", d.get("fail_reason"))
        with open(out, encoding="utf-8") as f:
            raw = f.read()
        seen = raw.strip().split("\n")
        self.assertEqual(len(seen), 2, raw)
        checks = [c["check"] for c in d["repos"][0]["commands"]]
        for ck, p in zip(checks, seen):
            self.assertIn("`%s`: private TMPDIR `%s` (%d bytes)" % (ck, p, len(p.encode("utf-8"))), r.report)
        by_id = {c["id"]: c for c in r.details["checks"]}
        self.assertEqual([by_id[ck].get("tmp_dir") for ck in checks], seen, json.dumps(r.details["checks"], indent=1))

    def test_untracked_test_files(self) -> None:
        """Port of TestUntrackedTestFiles (plan_test.go): an untracked test
        file by the generic patterns or the conf's test_glob fails the check;
        a tracked one does not."""
        fx = touched(self.root)
        fx.commit_in_repo("app", {"test/accept_test.go": "package test\n"}, "authored")
        write_files(fx.repo("app"), {"pkg/stray_test.go": "package pkg\n", "checks/x.check": "x\n", "notes.txt": "not a test\n"})
        rep = report("full", cmd_of("full-1", "full", "true"), test_glob=["*.check"])
        c = run_verify(fx.capsule, fake_engine(self.root, {"app": rep})).check("app:no-untracked-test-files")
        self.assertEqual(c["status"], "fail", c)
        for want in ("pkg/stray_test.go", "checks/x.check"):
            self.assertIn(want, c.get("detail", ""), c)
        self.assertNotIn("notes.txt", c.get("detail", ""), c)
        for p in ("pkg/stray_test.go", "checks/x.check"):
            os.remove(os.path.join(fx.repo("app"), p))
        c = run_verify(fx.capsule, fake_engine(self.root, {"app": rep}),
                       out=os.path.join(fx.capsule, "artifacts", "verify-2", "result.json")).check("app:no-untracked-test-files")
        self.assertEqual(c["status"], "pass", c)

    def test_requires_cmd_path_resolves_in_the_repo(self) -> None:
        """Port of TestRequiresCmdPathResolvesInTheRepo (plan_test.go): a
        required command naming a path resolves against the repo the
        command runs in, a bare name on PATH."""
        fx = touched(self.root)
        executable(os.path.join(fx.repo("app"), "bin", "tool"), "#!/bin/sh\necho tool ran\n")
        git_t(fx.repo("app"), "add", "-A")
        git_t(fx.repo("app"), "commit", "-q", "-m", "tool")
        ok = cmd_of("tool", "full", "./bin/tool", requires_cmd=["./bin/tool", "sh"])
        missing = cmd_of("other", "full", "./bin/none", requires_cmd=["./bin/none"])
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", ok, missing)}))
        c = r.check("app:tool")
        self.assertEqual(c["status"], "pass", "a repo-relative required command present in the repo must run: %s" % c)
        c = r.check("app:other")
        self.assertEqual(c["status"], "skipped", c)
        self.assertIn("command ./bin/none", c.get("detail", ""), "a missing repo-relative command is skipped naming it")

    def test_deadline_bounds_the_whole_run(self) -> None:
        """Port of TestDeadlineBoundsTheWholeRun (plan_test.go): no command
        runs past the run's deadline — a running one is killed at it, a
        later one never starts — both fail, and the result is still written
        in time."""
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("slow", "full", "sleep 60"), cmd_of("later", "full", "true"))})
        bound = verify.WRITE_MARGIN + 3.0
        start = time.monotonic()
        r = run_verify(fx.capsule, eng, "--deadline", "%gs" % bound)
        took = time.monotonic() - start
        self.assertLessEqual(took, bound, "the run took %.1fs, past its deadline" % took)
        c = r.check("app:slow")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("deadline", c.get("detail", ""), c)
        c = r.check("app:later")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("not started", c.get("detail", ""), c)
        self.assertTrue(os.path.isfile(os.path.join(fx.capsule, self.doc(r)["out_dir"], "result.json")))

    def test_engine_diagnostics_surface(self) -> None:
        """Port of TestEngineDiagnosticsSurface (plan_test.go): the engine's
        diagnostics reach the result per repo and as a visible skipped
        check; the conf-changed hint names who sees it; the report carries
        the diagnostics."""
        fx = touched(self.root)
        rep = report("full", cmd_of("full-1", "full", "true"), diagnostics=["verify-scope: unknown key foo= ignored"],
                     conf_changed_in_diff=True)
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": rep}))
        c = r.check("app:engine-diagnostics")
        self.assertEqual(c["status"], "skipped", c)
        self.assertIn("unknown key foo=", c.get("detail", ""), c)
        self.assertEqual(len(self.doc(r)["repos"][0].get("diagnostics", [])), 1, self.doc(r)["repos"])
        self.assertIn("deliver and the owner see it", r.check("app:conf-changed").get("detail", ""))
        self.assertIn("unknown key foo=", r.report, "the report must carry the engine diagnostics")


if __name__ == "__main__":
    unittest.main()
