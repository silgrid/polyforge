"""verify.py: a command's declared JUnit report (the conf's report=,
carried by the engine's --json as the command's ``report``) locates its
failure units — one failed testcase is one unit named
<classname>::<name> with the file the XML gives. A report that is missing,
stale, unparseable or names no failure changes nothing: the whole command
stays the unit and says why. The Go side's twin is
verifyrun/w371_junit_report_test.go (its Run-level tests)."""

import os
import shutil
import tempfile
import time
import unittest
from typing import Any, Callable, Dict, List, Optional, Tuple

from .fakebox import FakeRuntime
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, Run, cmd_of, fake_engine, report, run_verify, write_files

HAVE_NODE = shutil.which("node") is not None

JUNIT_FIXTURE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata", "junit-one-failure.xml")


def with_report(c: Dict[str, Any], path: str) -> Dict[str, Any]:
    """cmd_of declaring a junit report at path."""
    c["report"] = {"format": "junit", "path": path}
    return c


def command_of(r: Run, check: str) -> Dict[str, Any]:
    """result.json's command report of that check."""
    assert r.doc is not None
    for rp in r.doc["repos"] or []:
        for c in rp["commands"]:
            if c["check"] == check:
                return dict(c)
    raise AssertionError("no %s command in %r" % (check, r.doc["repos"]))


def units_of(r: Run, check: str) -> List[Dict[str, Any]]:
    """The units of one check."""
    assert r.doc is not None
    return [u for u in r.doc["units"] if u["check"] == check]


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are needed")
class JUnitReportTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def touched(self, sub: str = "fx") -> Fixture:
        """A fixture whose one repo carries a commit of its own."""
        root = os.path.join(self.root, sub)
        os.makedirs(root)
        f = Fixture(root, {"app": {"a.txt": "base\n"}})
        f.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
        return f

    def run_fx(self, f: Fixture, engine: str) -> Run:
        # a fake container engine: no repo here carries the Dockerfile, and
        # the run never reaches this machine's docker
        return run_verify(f.capsule, engine, runtime=FakeRuntime(os.path.join(f.root, "box-tmp")))

    def junit_run(self, cmd: str, prep: Optional[Callable[[str], None]] = None, sub: str = "fx") -> Tuple[Run, Dict[str, Any]]:
        """One failing e2e command declaring out/*.xml as its report."""
        f = self.touched(sub)
        if prep is not None:
            prep(f.repo("app"))
        eng = fake_engine(f.root, {"app": report("full", with_report(cmd_of("e2e", "full", cmd), "out/*.xml"))})
        r = self.run_fx(f, eng)
        self.assertIsNotNone(r.doc, r.stderr)
        return r, command_of(r, "app:e2e")

    def test_locates_the_failed_testcase(self) -> None:
        """TestW371JUnitReportLocatesTheFailedTestcase."""
        r, cmd = self.junit_run("mkdir -p out && cp " + JUNIT_FIXTURE + " out/e2e.xml && echo 1 of 3 failed; exit 1")
        us = units_of(r, "app:e2e")
        self.assertEqual(len(us), 1, "want exactly the one failed testcase: %r" % us)
        u = us[0]
        self.assertEqual(
            (u["kind"], u["name"], u.get("file"), u["status"]),
            ("junit", "e2e/panel.spec.ts::panel saves a draft", "e2e/panel.spec.ts", "fail"),
        )
        self.assertIn("expected 200, got 500", u.get("signature", ""))
        rep = cmd.get("report")
        self.assertIsNotNone(rep, cmd)
        assert rep is not None
        self.assertEqual((rep["units"], rep.get("files"), rep.get("note", "")), (1, ["out/e2e.xml"], ""))
        c = r.check("app:e2e")
        self.assertEqual(c["status"], "fail")
        self.assertIn("panel saves a draft", c.get("detail", ""))

    def test_falls_back_to_the_whole_command(self) -> None:
        """TestW371JUnitReportFallsBackToTheWholeCommand."""

        def stale(repo: str) -> None:
            # a previous run's report: older than this command's start
            p = os.path.join(repo, "out", "e2e.xml")
            os.makedirs(os.path.dirname(p), exist_ok=True)
            shutil.copyfile(JUNIT_FIXTURE, p)
            old = time.time() - 3600
            os.utime(p, (old, old))

        cases: List[Tuple[str, str, Optional[Callable[[str], None]], str]] = [
            ("missing", "echo crashed before writing; exit 1", None, "does not exist after the command"),
            ("unparseable", "mkdir -p out && echo 'not xml <' > out/e2e.xml; exit 1", None, "is unparseable"),
            ("no failure", "mkdir -p out && printf '<testsuite name=\"s\"><testcase name=\"ok\"/></testsuite>' > out/e2e.xml; exit 1",
             None, "names no failed testcase"),
            ("stale", "exit 1", stale, "was not written by this command"),
        ]
        for i, (name, cmd, prep, note) in enumerate(cases):
            with self.subTest(name):
                r, c = self.junit_run(cmd, prep, sub="fx%d" % i)
                us = units_of(r, "app:e2e")
                self.assertEqual(len(us), 1, us)
                u = us[0]
                self.assertEqual((u["kind"], u["name"], u["status"]), ("command", "app:e2e", "fail"), "want today's whole-command unit")
                self.assertIn("the whole command is the unit", u.get("detail", ""))
                self.assertIn(note, u.get("detail", ""))
                rep = c.get("report")
                self.assertIsNotNone(rep, c)
                assert rep is not None
                self.assertEqual(rep["units"], 0)
                self.assertIn(note, rep.get("note", ""))

    def test_not_read_on_a_pass(self) -> None:
        """TestW371JUnitReportNotReadOnAPass."""
        r, cmd = self.junit_run("mkdir -p out && cp " + JUNIT_FIXTURE + " out/e2e.xml")
        self.assertEqual(r.check("app:e2e")["status"], "pass")
        rep = cmd.get("report")
        self.assertIsNotNone(rep, cmd)
        assert rep is not None
        self.assertEqual(rep["units"], 0)
        self.assertIn("the command passed", rep.get("note", ""))

    def test_shared_glob_is_per_command(self) -> None:
        """TestW371JUnitReportSharedGlobIsPerCommand: two commands share one
        report glob; the first writes its report and fails, the second
        crashes before writing any. The first's file — written moments
        earlier, within the same second — is not the second's report: the
        second keeps the whole-command unit and says why."""
        f = self.touched()
        eng = fake_engine(f.root, {"app": report(
            "full",
            with_report(cmd_of("e2e-a", "full", "mkdir -p out && cp " + JUNIT_FIXTURE + " out/a.xml; exit 1"), "out/*.xml"),
            with_report(cmd_of("e2e-b", "full", "echo crashed; exit 1"), "out/*.xml"),
        )})
        r = self.run_fx(f, eng)
        a, b = units_of(r, "app:e2e-a"), units_of(r, "app:e2e-b")
        self.assertEqual([u["kind"] for u in a], ["junit"], "e2e-a: want its one junit testcase: %r" % a)
        self.assertEqual([u["kind"] for u in b], ["command"], "e2e-b: want the whole-command unit: %r" % b)
        self.assertIn("was not written by this command", b[0].get("detail", ""))
        rep = command_of(r, "app:e2e-b").get("report")
        self.assertIsNotNone(rep)
        assert rep is not None
        self.assertEqual((rep.get("files", []), rep["units"]), ([], 0), "e2e-b: want no file read")

    @unittest.skipUnless(HAVE_NODE, "node is not installed")
    def test_merges_with_output_units(self) -> None:
        """TestW371JUnitReportMergesWithOutputUnits: the report adds what the
        output does not locate and never replaces a re-runnable output
        unit: a node suite's own FAIL line (re-runnable as the suite) stands
        for the testcase in its file, and a testcase in another file is
        added."""
        script = """import { mkdirSync, writeFileSync } from "node:fs";
mkdirSync("out", { recursive: true });
writeFileSync("out/r.xml", '<testsuite name="s">' +
  '<testcase name="saves" classname="e2e/x.mjs" file="e2e/x.mjs"><failure message="boom"/></testcase>' +
  '<testcase name="renders" classname="e2e/y.spec.ts" file="e2e/y.spec.ts"><failure message="blank"/></testcase>' +
  '</testsuite>');
console.log("FAIL: saves");
process.exit(1);
"""
        r, cmd = self.junit_run("node e2e/x.mjs", lambda repo: write_files(repo, {"e2e/x.mjs": script}))
        us = units_of(r, "app:e2e")
        kinds = {u["kind"] + " " + u["name"]: u for u in us}
        self.assertEqual(len(us), 2, "want the suite's own unit plus the one testcase it does not cover: %r" % us)
        suite = kinds.get("mjs saves")
        self.assertIsNotNone(suite, "the re-runnable suite unit is missing: %r" % us)
        assert suite is not None
        self.assertEqual(suite.get("file"), "e2e/x.mjs")
        self.assertNotIn("not re-runnable", suite.get("detail", ""), "want the suite unit kept and re-run")
        case = kinds.get("junit e2e/y.spec.ts::renders")
        self.assertIsNotNone(case, "the uncovered testcase was not added: %r" % us)
        assert case is not None
        self.assertEqual(case.get("file"), "e2e/y.spec.ts")
        rep = cmd.get("report")
        self.assertIsNotNone(rep, cmd)
        assert rep is not None
        self.assertEqual(rep["units"], 2, "want both testcases read")


if __name__ == "__main__":
    unittest.main()
