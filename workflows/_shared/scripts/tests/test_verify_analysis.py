"""verify.py's change analysis (--analysis): the engine's plan runs whole
and the analysis's tests run beside it — the union is add-only — under the
analysis's supplement cap (the first entries in listed order, the rest cut
and named), and an entry whose file is not at HEAD or whose name is not in
the file fails its check naming it. A missing or malformed analysis file
is the invocation's (exit 2)."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    engine_args,
    fake_engine,
    report,
    run_verify,
)

# one supplement's run: red always exits 1, flaky exits 1 once until
# $FLAKY_STATE exists, anything else prints the marker and exits 0.
SUPPLEMENT_RUN = (
    "if [ \"$1\" = red ]; then exit 1; fi; "
    "if [ \"$1\" = flaky ] && [ ! -f \"$FLAKY_STATE\" ]; then : >\"$FLAKY_STATE\"; exit 1; fi; "
    "echo ANALYSIS-PASS"
)


def entry(
    repo: str,
    file: str,
    name: str = "",
    argv: Optional[List[str]] = None,
    mode: str = "pass",
    **extra: Any,
) -> Dict[str, Any]:
    e: Dict[str, Any] = {
        "id": "a1", "repo": repo, "file": file, "cwd": ".",
        "argv": argv or ["bash", "-c", SUPPLEMENT_RUN, "bash", mode],
        "marker": "^ANALYSIS-PASS$", "why": "pins the changed behaviour",
    }
    if name:
        e["name"] = name
    e.update(extra)
    return e


def analysis_file(fixture: Fixture, doc: Dict[str, Any]) -> str:
    path = os.path.join(fixture.capsule, "artifacts", "build", "change-analysis.json")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
    return path


def analysis_fixture(root: str) -> Tuple[Fixture, str]:
    """The app repo (a change over base) whose engine plans one fast
    command, and the fake engine answering it."""
    fx = Fixture(root, {"app": {"tests/test_a.py": "def test_ok():\n"}})
    fx.commit_in_repo("app", {"lib/x.py": "x = 2\n"}, "feature")
    eng = fake_engine(root, {"app": report("scoped", cmd_of("gate", "fast", "echo plan-ok"))})
    return fx, eng


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class AnalysisTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def run_analysis(self, fx: Fixture, eng: str, path: str) -> Run:
        try:
            return run_verify(
                fx.capsule, eng, "--analysis", path,
                env={"FLAKY_STATE": os.path.join(self.root, "flaky")},
            )
        except FileNotFoundError:
            self.fail("verify.py wrote no result file: the --analysis invocation was refused")
            raise AssertionError("unreachable")


    def test_supplement_runs_beside_the_plan_add_only(self) -> None:
        """The union is add-only: the engine is still asked --light, its
        planned command runs and passes, and each listed test runs beside
        it as its own check, marker judged."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {
            "changed": ["lib/x.py: x now doubles"],
            "uncovered": {"app": []},
            "tests": [entry("app", "tests/test_a.py", name="test_ok")],
        })
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(r.check("app:gate")["status"], "pass")
        a = r.check("app:analysis:a1")
        self.assertEqual(a["status"], "pass", a)
        self.assertIn("ANALYSIS-PASS", a.get("detail", ""), a)
        # the engine's selection was not narrowed: still the light report
        self.assertTrue(any(("--light" in ln) for ln in engine_args(eng)), engine_args(eng))
        self.assertEqual(self.doc(r)["mechanical_verdict"], "pass")

    def test_red_supplement_fails_its_check(self) -> None:
        """A supplement that stays red fails its check and the run (exit 1)
        — a listed test is never a pass on the plan's word alone."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {"tests": [entry("app", "tests/test_a.py", mode="red")]})
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 1, r.result.get("summary"))
        a = r.check("app:analysis:a1")
        self.assertEqual(a["status"], "fail", a)
        self.assertIn("exit 1", a.get("detail", ""), a)

    def test_flaky_supplement_passes_on_its_rerun(self) -> None:
        """A supplement failing once and passing on its re-run is a flaky
        pass, recorded as such (the same policy as a unit run)."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {"tests": [entry("app", "tests/test_a.py", mode="flaky")]})
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        a = r.check("app:analysis:a1")
        self.assertEqual(a["status"], "pass", a)
        self.assertIn("re-run", a.get("detail", ""), a)

    def test_cap_cuts_the_tail_in_listed_order(self) -> None:
        """Over the cap the tail entries are not run: each keeps its check,
        skipped with the cap named, and the report names the cut ids."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {
            "supplement_limit": 1,
            "tests": [
                entry("app", "tests/test_a.py", name="test_ok"),
                entry("app", "tests/test_a.py", name="test_ok", id="a2"),
            ],
        })
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(r.check("app:analysis:a1")["status"], "pass")
        cut = r.check("app:analysis:a2")
        self.assertEqual(cut["status"], "skipped", cut)
        self.assertIn("cap", cut.get("detail", "").lower(), cut)
        self.assertIn("a2", r.report)

    def test_default_cap_when_the_analysis_names_none(self) -> None:
        """Without supplement_limit the script's built-in default applies
        (10): a handful of entries all run."""
        fx, eng = analysis_fixture(self.root)
        tests = [entry("app", "tests/test_a.py", name="test_ok", id="a%d" % (i + 1)) for i in range(4)]
        path = analysis_file(fx, {"tests": tests})
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        for i in range(4):
            self.assertEqual(r.check("app:analysis:a%d" % (i + 1))["status"], "pass")

    def test_missing_test_fails_named(self) -> None:
        """An entry whose file is not at HEAD, or whose name never appears
        in the file, fails its check naming the file and the name."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {"tests": [
            entry("app", "tests/gone.py", id="a1"),
            entry("app", "tests/test_a.py", name="test_no_such_one", id="a2"),
        ]})
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 1, r.result.get("summary"))
        gone = r.check("app:analysis:a1")
        self.assertEqual(gone["status"], "fail", gone)
        self.assertIn("tests/gone.py", gone.get("detail", ""), gone)
        no_name = r.check("app:analysis:a2")
        self.assertEqual(no_name["status"], "fail", no_name)
        self.assertIn("test_no_such_one", no_name.get("detail", ""), no_name)
        self.assertIn("tests/test_a.py", no_name.get("detail", ""), no_name)

    def test_entry_of_an_unknown_repo_fails(self) -> None:
        """An entry naming a repo that is not a task repo of the capsule
        fails its check (never a skip)."""
        fx, eng = analysis_fixture(self.root)
        path = analysis_file(fx, {"tests": [entry("web", "tests/test_a.py", name="test_ok")]})
        r = self.run_analysis(fx, eng, path)
        self.assertEqual(r.exit, 1, r.result.get("summary"))
        a = r.check("web:analysis:a1")
        self.assertEqual(a["status"], "fail", a)
        self.assertIn("not a task repo", a.get("detail", ""), a)

    def test_unusable_analysis_files_are_not_judged(self) -> None:
        """A missing analysis file, a malformed one, an entry without its
        run, one without why, and a non-positive cap: each exit 2."""
        fx, eng = analysis_fixture(self.root)
        for name, path in [
            ("missing", os.path.join(fx.capsule, "artifacts", "build", "nope.json")),
            ("malformed", analysis_file(fx, {"tests": "nope"})),
            ("no run", analysis_file(fx, {"tests": [{"id": "a1", "repo": "app", "file": "tests/test_a.py"}]})),
            ("no why", analysis_file(fx, {"tests": [{"id": "a1", "repo": "app", "file": "tests/test_a.py",
                                                     "argv": ["true"], "marker": "^x$"}]})),
            ("bad cap", analysis_file(fx, {"supplement_limit": 0, "tests": []})),
        ]:
            with self.subTest(name):
                r = self.run_analysis(fx, eng, path)
                self.assertEqual(r.exit, 2, "%s: %s" % (name, r.result.get("summary")))


if __name__ == "__main__":
    unittest.main()
