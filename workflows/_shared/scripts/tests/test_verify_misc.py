"""verify.py, the rest of verifyrun's Run tests: a data race is never flaky,
a command's process group dies with it, a light request with nothing
touched is no light run (rerun_test.go); the caller's deadline is the run's
(w371_deadline_bound_test.go); a failed go list names why the light run is
full (w386_tier_reason_test.go); the dependency audit is never skipped nor
pre-existing (audit_pin_test.go); the engine's e2e selection is recorded and
reported (selection_report_test.go)."""

import json
import os
import shutil
import signal
import tempfile
import time
import unittest
from typing import Any, Dict

from pfstep.engine import ReportError, decode_report
from pfstep.units import KIND_COMMAND

from .procutil import alive
from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    executable,
    fake_engine,
    report,
    run_verify,
    write_files,
)

# the workflow's scope engine, workflows/_shared/scripts/verify-scope
SCOPE_ENGINE = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "verify-scope"))


def touched(root: str) -> Fixture:
    """A fixture whose one repo carries a commit of its own."""
    fx = Fixture(root, {"app": {"a.txt": "base\n"}})
    fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
    return fx


def audit_cmd(cmd: str) -> Dict[str, Any]:
    """The engine's audit command shape: name audit, phase full, source
    default, no prerequisites, null timeout."""
    return cmd_of("audit", "full", cmd, source="default", timeout=None)


def sel_report(sel: Dict[str, Any], *cmds: Dict[str, Any]) -> Dict[str, Any]:
    """A full report carrying an e2e selection."""
    return report("full", *cmds, smoke_suite=["e2e/smoke.mjs"], e2e_selection=sel)


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class MiscTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    # -- rerun_test.go

    def test_race_failure_is_never_flaky(self) -> None:
        """Port of TestRaceFailureIsNeverFlaky (rerun_test.go): a run that
        reported a data race is never proven flaky by a re-run without
        -race."""
        fx = touched(self.root)
        fx.commit_in_repo("app", {"r_test.go": "package app\n\nfunc TestRacy(t *testing.T) {}\n"}, "racy")
        out = os.path.join(self.root, "out.txt")
        write_files(self.root, {"out.txt": "==================\nWARNING: DATA RACE\n--- FAIL: TestRacy (0.00s)\n"
                                           "    testing.go:1: race detected during execution of test\nFAIL\n"
                                           "FAIL\texample.com/app\t0.1s\n"})
        eng = fake_engine(self.root, {"app": report("full", cmd_of("unit", "full", "cat " + out + "; exit 1"))})
        u = run_verify(fx.capsule, eng).unit("TestRacy")
        self.assertEqual(u["status"], "fail", u)
        self.assertIn("data race", u.get("detail", ""), u)

    def test_command_group_dies_with_the_command(self) -> None:
        """Port of TestCommandGroupDiesWithTheCommand (rerun_test.go): a
        background job a command leaves behind is killed when the command
        ends."""
        fx = touched(self.root)
        pid_file = os.path.join(self.root, "pid")
        cmd = "(while :; do :; done) & echo $! > " + pid_file + "; echo started"
        c = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", cmd_of("bg", "full", cmd))})).check("app:bg")
        self.assertEqual(c["status"], "pass", c)
        with open(pid_file, encoding="utf-8") as f:
            pid = int(f.read().strip())
        deadline = time.monotonic() + 5
        while alive(pid):
            if time.monotonic() > deadline:
                try:
                    os.kill(pid, signal.SIGKILL)
                except OSError:
                    pass
                self.fail("the background job %d outlived its command" % pid)
            time.sleep(0.02)

    def test_light_with_nothing_touched_is_not_light(self) -> None:
        """Port of TestLightWithNothingTouchedIsNotLight (rerun_test.go): a
        light request with no touched repo proves no light run."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        d = self.doc(run_verify(fx.capsule, fake_engine(self.root, {})))
        self.assertEqual(d["tier"]["effective"], "full", d["tier"])

    # -- w371_deadline_bound_test.go

    def test_w371_run_uses_the_callers_deadline(self) -> None:
        """Port of TestW371RunUsesTheCallersDeadline
        (w371_deadline_bound_test.go): a 78m deadline stays 78m although the
        environment carries a 30m tool window, in the run and in
        result.json."""
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("unit", "full", "true"))})
        r = run_verify(fx.capsule, eng, "--deadline", "78m",
                       env={"BASH_MAX_TIMEOUT_MS": "1740000", "PF_TOOL_WINDOW_MS": "1800000"})
        d = self.doc(r)
        self.assertEqual(d["deadline"], "1h18m0s", "want the caller's 1h18m0s")
        with open(os.path.join(fx.capsule, d["out_dir"], "result.json"), encoding="utf-8") as f:
            raw = f.read()
        self.assertIn('"deadline": "1h18m0s"', raw)
        self.assertNotIn("deadline_bound", raw)

    def test_w371_deadline_required(self) -> None:
        """Port of TestW371DeadlineRequired (w371_deadline_bound_test.go): a
        run is never without a deadline. Go's Options.Validate refusal of a
        zero deadline has no command-line twin — pf-verify (and verify.py)
        always states one: --deadline when it is positive, else the one the
        step environment derives (25m0s outside a step) — so that half is
        asserted as the derivation; a stated deadline is taken."""
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("unit", "full", "true"))})
        for i, (flag, want) in enumerate(((None, "25m0s"), ("0", "25m0s"), ("1h", "1h0m0s"))):
            with self.subTest(deadline=flag):
                extra = [] if flag is None else ["--deadline", flag]
                r = run_verify(fx.capsule, eng, *extra, out=os.path.join(fx.capsule, "artifacts", "v%d" % i, "result.json"))
                self.assertEqual(r.exit, 0, r.result.get("summary"))
                self.assertEqual(self.doc(r)["deadline"], want)

    # -- w386_tier_reason_test.go

    @unittest.skipUnless(os.path.isfile(SCOPE_ENGINE), "scripts/verify-scope is not in this tree")
    def test_w386_tier_reason_names_go_list(self) -> None:
        """Port of TestW386TierReasonNamesGoList (w386_tier_reason_test.go):
        when the scope engine's `go list` fails and the requested light tier
        falls back to full, result.json's tier carries the reason — the
        failed command and its first output line — and report-mechanical.md
        states it."""
        fx = Fixture(self.root, {"app": {
            "go.mod": "module fixture.test/app\n\ngo 1.21\n",
            "a/a.go": "package a\n\nfunc A() int { return 1 }\n",
            "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
            "b/b.go": "package b\n\nfunc B() int { return 2 }\n",
        }})
        fx.commit_in_repo("app", {"a/a.go": "package a\n\nfunc A() int { return 3 }\n"}, "change a")
        # a go that fails every call: first on PATH, so the engine's go list
        # (and every bar command) meets it
        stub = os.path.join(self.root, "stub")
        executable(os.path.join(stub, "go"), "#!/bin/sh\necho 'stub-go: no toolchain behind this go' >&2\n"
                                             "echo 'stub-go: second line' >&2\nexit 2\n")
        r = run_verify(fx.capsule, SCOPE_ENGINE, env={"PATH": stub + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin")})
        d = self.doc(r)
        self.assertEqual(d.get("incomplete", ""), "")
        tier = d["tier"]
        self.assertEqual((tier["requested"], tier["effective"]), ("light", "full"), "the failed go list makes the light run full")
        reasons = [x for x in tier.get("reasons", []) if "go list" in x and "stub-go: no toolchain behind this go" in x]
        self.assertTrue(reasons, "a tier reason names the failed go list and its first output line: %r" % tier)
        with open(os.path.join(fx.capsule, d["out_dir"], "result.json"), encoding="utf-8") as f:
            self.assertIn('"reasons"', f.read())
        self.assertIn(reasons[0], r.report, "report-mechanical.md states the tier reason")

    @unittest.skipUnless(os.path.isfile(SCOPE_ENGINE), "scripts/verify-scope is not in this tree")
    def test_w465_tier_reasons_one_line_each(self) -> None:
        """Port of TestW465TierReasonsOneLineEach (w386_tier_reason_test.go):
        every reason a light request runs full is its own
        tier.reasons entry, prefixed with its repo, and its own `- ` line of
        report-mechanical.md — a changed stack config file and a failed go
        list here — while the engine's informational --light diagnostics (a
        diff spanning module_root= roots) are not reasons."""
        if shutil.which("go") is None:
            self.skipTest("go not installed")
        fx = Fixture(self.root, {
            "app": {
                "go.mod": "module fixture.test/app\n\ngo 1.21\n",
                "a/a.go": "package a\n\nfunc A() int { return 1 }\n",
                "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
            },
            "lib": {
                "go.mod": "module fixture.test/lib\n\ngo 1.21\n",
                ".polyforge/verify-scope.conf": "module_root=x\nmodule_root=y\n",
                "x/x.go": "package x\n\nfunc X() int { return 1 }\n",
                "y/y.go": "package y\n\nfunc Y() int { return 1 }\n",
            },
        })
        fx.commit_in_repo("app", {"go.mod": "module fixture.test/app\n\ngo 1.22\n"}, "bump go")
        fx.commit_in_repo("lib", {
            "x/x.go": "package x\n\nfunc X() int { return 2 }\n",
            "y/y.go": "package y\n\nimport \"fixture.test/lib/missing\"\n\nfunc Y() int { return missing.M() }\n",
        }, "change x and y")
        r = run_verify(fx.capsule, SCOPE_ENGINE)
        d = self.doc(r)
        self.assertEqual(d.get("incomplete", ""), "")
        tier = d["tier"]
        self.assertEqual((tier["requested"], tier["effective"]), ("light", "full"), "both repos make the light run full")
        reasons = tier.get("reasons", [])
        self.assertEqual(len(reasons), 2, "one reason per repo and fallback: %r" % reasons)
        for x in reasons:
            self.assertNotIn("does not force the full tier", x, "an informational --light diagnostic is not a reason")
        app = [x for x in reasons if x.startswith("repos/app: ")]
        lib = [x for x in reasons if x.startswith("repos/lib: ")]
        self.assertTrue(app and "go.mod" in app[0] and "falling back to the full tier" in app[0], reasons)
        self.assertTrue(lib and "go list failed" in lib[0], reasons)
        for x in reasons:
            self.assertIn("\n- " + x + "\n", r.report, "report-mechanical.md states each reason on its own line")

    # -- audit_pin_test.go

    def test_w320_audit_never_skipped(self) -> None:
        """Port of TestW320AuditNeverSkipped (audit_pin_test.go): a missing
        audit tool is a failing run, never a skip — while a sibling command
        with an unmet declared prerequisite is."""
        fx = touched(self.root)
        gated = cmd_of("gated", "full", "true", requires_cmd=["w320-no-such-tool"])
        audit = audit_cmd("echo 'tool-missing: npm is not on PATH; surface npm:. was not audited' >&2; exit 1")
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", gated, audit)}))
        c = r.check("app:gated")
        self.assertEqual(c["status"], "skipped", "control: a command with an unmet declared prerequisite is skipped: %s" % c)
        c = r.check("app:audit")
        self.assertEqual(c["status"], "fail", "a missing audit tool must fail the audit check, never skip it: %s" % c)
        self.assertEqual(self.doc(r)["mechanical_verdict"], "fail")

    def test_w320_audit_never_pre_existing(self) -> None:
        """Port of TestW320AuditNeverPreExisting (audit_pin_test.go): an
        unexempted finding the base carries identically still fails the bar:
        the audit is one command unit, never pre-existing."""
        fx = touched(self.root)
        audit = audit_cmd("echo 'finding: surface npm:.: GHSA-xxxx-xxxx-xxxx in left-pad (high)' >&2; exit 1")
        r = run_verify(fx.capsule, fake_engine(self.root, {"app": report("full", audit)}))
        u = r.unit("app:audit")
        self.assertEqual((u["kind"], u["status"]), (KIND_COMMAND, "fail"), u)
        self.assertIn("never pre-existing", u.get("detail", ""), u)
        c = r.check("app:audit")
        self.assertEqual(c["status"], "fail", c)
        d = self.doc(r)
        self.assertEqual((d["pre_existing_failures"], d["mechanical_verdict"]), ([], "fail"))

    # -- selection_report_test.go

    def test_selection_is_recorded_and_reported(self) -> None:
        """Port of TestSelectionIsRecordedAndReported
        (selection_report_test.go): an opted-in repo's e2e selection (carries
        or smoke) is run as planned — never the full tail — recorded on the
        repo report and rendered in report-mechanical.md."""
        for mode in ("carries", "smoke"):
            with self.subTest(mode=mode):
                root = os.path.join(self.root, mode)
                os.makedirs(root)
                fx = touched(root)
                eng = fake_engine(root, {"app": sel_report(
                    {"mode": mode, "suites": ["e2e/panel.mjs", "e2e/smoke.mjs"], "uncovered_paths": ["scripts/ops.sh"],
                     "full_commands": ["echo FULL-E2E"]},
                    cmd_of("fast-1", "fast", "echo FAST"), cmd_of("full-1", "full", "echo SUBSET e2e/panel.mjs e2e/smoke.mjs"))})
                r = run_verify(fx.capsule, eng)
                rp = self.doc(r)["repos"][0]
                self.assertEqual([c["command"] for c in rp["commands"]],
                                 ["echo FAST", "echo SUBSET e2e/panel.mjs e2e/smoke.mjs"])
                sel = rp.get("e2e_selection")
                self.assertIsNotNone(sel, rp)
                assert sel is not None
                self.assertEqual((sel["mode"], sel["uncovered_paths"]), (mode, ["scripts/ops.sh"]), sel)
                for s in ("e2e selection (smoke_suite opt-in): mode **" + mode + "**", "`e2e/panel.mjs`, `e2e/smoke.mjs`",
                          "uncovered paths `scripts/ops.sh`", "carried full command(s) `echo FULL-E2E`"):
                    self.assertIn(s, r.report)

    def test_selection_fallback_and_non_opted(self) -> None:
        """Port of TestSelectionFallbackAndNonOpted
        (selection_report_test.go): a selection that fell back to the full
        tail is still recorded; a non-opted repo records none."""
        fx = touched(self.root)
        full = sel_report({"mode": "full", "suites": [], "uncovered_paths": [], "full_commands": ["echo FULL-E2E"]},
                          cmd_of("fast-1", "fast", "echo FAST"), cmd_of("full-1", "full", "echo FULL-E2E"))
        rp = self.doc(run_verify(fx.capsule, fake_engine(self.root, {"app": full})))["repos"][0]
        self.assertEqual((rp.get("e2e_selection") or {}).get("mode"), "full", "the fallback is still recorded: %s" % rp)
        plain = report("full", cmd_of("fast-1", "fast", "echo FAST"), cmd_of("full-1", "full", "echo FULL-E2E"))
        rp = self.doc(run_verify(fx.capsule, fake_engine(self.root, {"app": plain}),
                                 out=os.path.join(fx.capsule, "artifacts", "verify-2", "result.json")))["repos"][0]
        self.assertIsNone(rp.get("e2e_selection"), "non-opted selection")

    def test_selection_mode_validated(self) -> None:
        """Port of TestSelectionModeValidated (selection_report_test.go): an
        unknown selection mode is an unreadable report."""
        raw = b'{"schema":1,"mode":"full","commands":[],"e2e_selection":{"mode":"bogus"}}'
        with self.assertRaises(ReportError) as cm:
            decode_report(raw)
        self.assertIn("e2e_selection mode", str(cm.exception))


if __name__ == "__main__":
    unittest.main()
