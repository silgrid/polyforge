# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/pfstep/analysis.py, workflows/_shared/scripts/pfstep/unitruns.py
"""facts.py analysis — the build close's own check of the change
analysis (polyforge#112, AC1 and AC2). Every fixture here is a
project-neutral minimal equivalent of a verification failure the ticket
was filed over, built from the ticket's record of it — none is a copy of
the original capsules' files:

- polyforge#79 a3: the composite name — two tests' names joined into one
  string, which appears nowhere as one string;
- polyforge#79 a12: the cross-file entry — its name lives in another
  file than the one it names;
- polyforge#79 a14: the double cd — the entry's cwd is already the repo,
  its argv cds into it again; statically clean, so only the close run
  (the existing execution and marker rules) catches it;
- polyforge#97: the descriptive name — a scenario's description, not a
  test's name.

Each bad fixture is judged red with the entry and the rule it violates
named; a compliant fixture passes; over the supplement cap the tail is
cut and named, never run; an unusable analysis file is not judged (exit
2, verification's own treatment of the same file)."""

import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from typing import Any, Dict, Tuple

import facts
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, step_env


def entry(**kw: Any) -> Dict[str, Any]:
    """One listed test with the defaults every entry carries."""
    e: Dict[str, Any] = {"id": "a1", "repo": "app", "file": "tests/test_a.py", "cwd": ".",
                         "argv": ["bash", "-c", "echo ANALYSIS-PASS"], "marker": "^ANALYSIS-PASS$",
                         "why": "pins the changed behaviour"}
    e.update(kw)
    return e


def world(root: str) -> Fixture:
    """The app repo (a change over its base) whose tests the fixtures name."""
    fx = Fixture(root, {"app": {"tests/test_a.py": "class BuildCase:\n    def test_ok(self):\n        pass\n",
                                "tests/test_b.py": "def test_other():\n    pass\n"}})
    fx.commit_in_repo("app", {"lib/x.py": "x = 2\n"}, "feature")
    return fx


def analysis_file(fx: Fixture, doc: Dict[str, Any]) -> str:
    path = os.path.join(fx.capsule, "artifacts", "build", "change-analysis.json")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
    return path


def run_facts(capsule: str, path: str, out: str) -> Tuple[int, Dict[str, Any]]:
    """facts.py analysis in-process; (exit, the result file's JSON, or {}
    when the invocation wrote none)."""
    so, se = io.StringIO(), io.StringIO()
    with redirect_stdout(so), redirect_stderr(se):
        code = facts.main(["analysis", "--capsule", capsule, "--analysis", path,
                           "--unit-timeout", "1m", "--json", "--out", out], step_env())
    res: Dict[str, Any] = {}
    if os.path.exists(out):
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
    return code, res


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class AnalysisCloseCheckTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)
        self.fx = world(self.root)
        self.out = os.path.join(self.fx.capsule, "artifacts", "facts", "analysis.json")

    def check(self, doc: Dict[str, Any]) -> Tuple[int, Dict[str, Any]]:
        path = analysis_file(self.fx, doc)
        return run_facts(self.fx.capsule, path, self.out)

    def details(self, res: Dict[str, Any]) -> Dict[str, Any]:
        d = res.get("details") or {}
        assert isinstance(d, dict)
        return d

    def one(self, res: Dict[str, Any]) -> Dict[str, Any]:
        entries = self.details(res)["entries"]
        self.assertEqual(len(entries), 1, entries)
        return dict(entries[0])

    def test_composite_name_red_naming_the_entry_and_the_rule(self) -> None:
        """#79's a3 class: the close check judges it red and names the entry
        (its id) and the violated rule (the name exists nowhere in the
        file it names)."""
        code, res = self.check({"tests": [entry(name="test_ok + test_other")]})
        self.assertEqual(code, 1, res.get("summary"))
        self.assertIn("a1", res.get("summary", ""), res)
        self.assertIn("appears nowhere", res.get("summary", ""), res)
        rec = self.one(res)
        self.assertEqual(rec["status"], "fail")
        self.assertIn('its name "test_ok + test_other" appears nowhere in "tests/test_a.py"', rec["problem"])

    def test_cross_file_name_red_in_the_named_file(self) -> None:
        """#79's a12 class: the test lives in another file than the one the
        entry names, so its name appears nowhere in the named file."""
        code, res = self.check({"tests": [entry(name="test_other")]})
        self.assertEqual(code, 1, res.get("summary"))
        rec = self.one(res)
        self.assertEqual(rec["status"], "fail")
        self.assertIn('its name "test_other" appears nowhere in "tests/test_a.py"', rec["problem"])

    def test_descriptive_name_red(self) -> None:
        """#97's class: a scenario's description is not a test's name."""
        code, res = self.check({"tests": [entry(name="app-suite (scenario, D0–D7)")]})
        self.assertEqual(code, 1, res.get("summary"))
        rec = self.one(res)
        self.assertEqual(rec["status"], "fail")
        self.assertIn('its name "app-suite (scenario, D0–D7)" appears nowhere', rec["problem"])

    def test_double_cd_red_at_the_close_run(self) -> None:
        """#79's a14 class (AC2): the entry is statically clean — its file,
        name and cwd all exist — and its run fails at the close, the
        existing execution and marker rules naming it; it is a run
        failure, never a name failure."""
        code, res = self.check({"tests": [entry(argv=["bash", "-c", "cd app && echo ANALYSIS-PASS"])]})
        self.assertEqual(code, 1, res.get("summary"))
        self.assertIn("a1", res.get("summary", ""), res)
        rec = self.one(res)
        self.assertEqual(rec["status"], "fail")
        self.assertEqual(rec["exit_code"], 1, rec)
        self.assertTrue(rec.get("log"), "the close check ran the entry's run as written")
        self.assertIn("exited 1", rec["problem"])
        self.assertNotIn("appears nowhere", rec["problem"], "statically clean: the red is the run's")

    def test_the_compliant_fixture_passes(self) -> None:
        code, res = self.check({"tests": [entry(name="test_ok")]})
        self.assertEqual(code, 0, res.get("summary"))
        rec = self.one(res)
        self.assertEqual(rec["status"], "pass")

    def test_the_cap_cuts_the_tail_and_names_it(self) -> None:
        code, res = self.check({"supplement_limit": 1,
                                "tests": [entry(name="test_ok"), entry(name="test_ok", id="a2")]})
        self.assertEqual(code, 0, res.get("summary"))
        entries = self.details(res)["entries"]
        self.assertEqual([e["id"] for e in entries], ["a1"])
        cut = self.details(res)["cut"]
        self.assertEqual([c["id"] for c in cut], ["a2"])
        self.assertIn("supplement cap", cut[0]["note"])

    def test_an_unknown_repo_is_red(self) -> None:
        code, res = self.check({"tests": [entry(repo="web")]})
        self.assertEqual(code, 1, res.get("summary"))
        rec = self.one(res)
        self.assertEqual(rec["status"], "fail")
        self.assertIn("is not a task repo of this capsule", rec["problem"])

    def test_the_same_run_executes_once(self) -> None:
        """Two entries naming the same run (the same tree, the same
        command) execute once: the second is judged on the first's output
        and says which entry it shared the run with."""
        code, res = self.check({"tests": [entry(name="test_ok"), entry(name="test_ok", id="a2")]})
        self.assertEqual(code, 0, res.get("summary"))
        entries = self.details(res)["entries"]
        self.assertEqual([e["status"] for e in entries], ["pass", "pass"])
        self.assertEqual(entries[1].get("shared_run_with"), "a1")
        self.assertEqual(entries[1].get("log"), entries[0].get("log"))

    def test_an_unusable_analysis_file_is_not_judged(self) -> None:
        for name, doc in (("malformed", {"tests": "nope"}),
                          ("no run", {"tests": [{"id": "a1", "repo": "app", "file": "tests/test_a.py"}]})):
            with self.subTest(name):
                code, _res = self.check(doc)
                self.assertEqual(code, 2)
        code, _res = run_facts(self.fx.capsule, os.path.join(self.fx.capsule, "artifacts", "build", "nope.json"),
                               self.out)
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
