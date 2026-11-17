# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/pfstep/analysis.py, workflows/_shared/scripts/pfstep/unitruns.py
"""AC3's parity (polyforge#112): the same analysis fixtures judged by both
callers of the one shared validator — facts.py analysis (the build
close's check) and verify.py --analysis (the machine gate) — reach the
same conclusions, entry by entry: the same exit, the same verdict per
id, and — the static rules being one implementation — the close check's
problem text verbatim inside the gate's check detail. The
through-examples (a legal qualified name, a legal shell cd, an omitted
name) pass on both sides, so this is not two engines agreeing to refuse
alike. The bad fixtures are the ticket's own record restated as
project-neutral minimal equivalents (polyforge#79's composite name,
cross-file entry and double cd; polyforge#97's descriptive name), never
copies of those capsules' files."""

import os
import tempfile
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .test_analysis_preflight import analysis_file, entry, run_facts
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, cmd_of, fake_engine, report, run_verify

# one supplement's run: a name that exists passes, the double cd fails
DOUBLE_CD = ["bash", "-c", "cd app && echo ANALYSIS-PASS"]
LEGAL_CD = ["bash", "-c", "cd tests && echo ANALYSIS-PASS"]


def world(root: str) -> Tuple[Fixture, str]:
    """The app repo (a change over base) the engine plans one fast command
    for, and the fake engine answering it."""
    fx = Fixture(root, {"app": {"tests/test_a.py": "class BuildCase:\n    def test_ok(self):\n        pass\n",
                                "tests/test_b.py": "def test_other():\n    pass\n",
                                "web/x.txt": "x\n"}})
    fx.commit_in_repo("app", {"lib/x.py": "x = 2\n"}, "feature")
    eng = fake_engine(root, {"app": report("scoped", cmd_of("gate", "fast", "echo plan-ok"))})
    return fx, eng


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class AnalysisParityTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)
        self.fx, self.eng = world(self.root)
        self.out = os.path.join(self.fx.capsule, "artifacts", "facts", "analysis.json")

    def both(self, doc: Dict[str, Any]) -> Tuple[int, Dict[str, Any], int, Optional[Any]]:
        """The same analysis through both callers: (facts exit, facts
        result, verify exit, verify result doc)."""
        path = analysis_file(self.fx, doc)
        fcode, fres = run_facts(self.fx.capsule, path, self.out)
        r = run_verify(self.fx.capsule, self.eng, "--analysis", path)
        return fcode, fres, r.exit, r.doc

    def facts_entries(self, res: Dict[str, Any]) -> Dict[str, Dict[str, Any]]:
        d = res.get("details") or {}
        assert isinstance(d, dict)
        return {e["id"]: dict(e) for e in d.get("entries") or []}

    def verify_checks(self, doc: Optional[Any]) -> Dict[str, Dict[str, Any]]:
        assert doc is not None
        return {c["name"]: dict(c) for c in doc["checks"]}

    def assert_same_conclusion(self, doc: Dict[str, Any], want: str,
                               static: Optional[List[str]] = None,
                               run_failed: bool = False) -> None:
        """want: the exit both sides reach ("red" or "green"); static: the
        entry ids whose problem is a static rule's (byte-compared against
        the gate's detail); run_failed: the entry's red is its run's."""
        fcode, fres, vcode, vdoc = self.both(doc)
        if want == "red":
            self.assertEqual(fcode, 1, fres.get("summary"))
            self.assertEqual(vcode, 1, "verification must reach the same red")
        else:
            self.assertEqual(fcode, 0, fres.get("summary"))
            self.assertEqual(vcode, 0, "verification must reach the same green")
        fentries = self.facts_entries(fres)
        vchecks = self.verify_checks(vdoc)
        for e in doc["tests"]:
            eid = e["id"]
            f, v = fentries[eid], vchecks["%s:analysis:%s" % (e["repo"], eid)]
            self.assertEqual(f["status"] == "fail", v["status"] == "fail",
                             "%s: the two callers disagree: %r vs %r" % (eid, f, v))
            if static and eid in static:
                # one implementation: the close check's problem text sits
                # verbatim inside the gate's check detail
                self.assertIn(f["problem"], v.get("detail", ""), (eid, f, v))
            if run_failed:
                self.assertIn("exited %d" % f["exit_code"], f["problem"])
                self.assertIn("exit %d" % f["exit_code"], v.get("detail", ""), (eid, v))
                self.assertNotEqual(f["exit_code"], 0)

    def test_the_composite_name_is_red_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="test_ok + test_other")]}, "red", static=["a1"])

    def test_the_cross_file_entry_is_red_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="test_other")]}, "red", static=["a1"])

    def test_the_descriptive_name_is_red_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="app-suite (scenario, D0–D7)")]}, "red", static=["a1"])

    def test_the_double_cd_is_red_on_both_sides(self) -> None:
        """Statically clean, so both callers' runs fail it at run time —
        the close check names the entry, the gate fails its check."""
        self.assert_same_conclusion({"tests": [entry(argv=DOUBLE_CD)]}, "red", run_failed=True)

    def test_the_compliant_entry_is_green_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="test_ok")]}, "green")

    def test_a_legal_qualified_name_is_green_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="BuildCase.test_ok")]}, "green")

    def test_a_legal_shell_cd_is_green_on_both_sides(self) -> None:
        """A cd that works is no rule's problem: neither caller refuses a
        legal command."""
        self.assert_same_conclusion({"tests": [entry(cwd=".", argv=LEGAL_CD)]}, "green")

    def test_an_omitted_name_is_green_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(name="")]}, "green")

    def test_the_cut_tail_is_named_on_both_sides(self) -> None:
        """Over the cap the tail runs nowhere: the gate skips its check
        naming the cap, the close check cuts and names it."""
        doc = {"supplement_limit": 1, "tests": [entry(name="test_ok"), entry(name="test_ok", id="a2")]}
        fcode, fres, vcode, vdoc = self.both(doc)
        self.assertEqual((fcode, vcode), (0, 0))
        cut = (fres.get("details") or {}).get("cut") or []
        self.assertEqual([c["id"] for c in cut], ["a2"])
        vchecks = self.verify_checks(vdoc)
        self.assertEqual(vchecks["app:analysis:a2"]["status"], "skipped")
        self.assertIn("cap", vchecks["app:analysis:a2"].get("detail", "").lower())

    def test_an_unknown_repo_is_red_on_both_sides(self) -> None:
        self.assert_same_conclusion({"tests": [entry(repo="web", file="tests/test_a.py")]}, "red", static=["a1"])

    def test_an_unusable_file_is_not_judged_on_both_sides(self) -> None:
        """A malformed analysis file is not judged on both sides — and the
        close check's own summary names the file unusable, which an unknown
        kind never prints ("no kind of fact" writes no result file at all)."""
        path = analysis_file(self.fx, {"tests": "nope"})
        fcode, fres = run_facts(self.fx.capsule, path, self.out)
        r = run_verify(self.fx.capsule, self.eng, "--analysis", path)
        self.assertEqual(fcode, 2)
        self.assertIn("is unusable", fres.get("summary", ""), fres)
        self.assertEqual(r.exit, 2, "verification's own treatment of the same file")


if __name__ == "__main__":
    unittest.main()
