# carries: workflows/_shared/scripts/pfstep/preexisting.py, workflows/_shared/scripts/pfstep/rerun.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/fast_checks.py, workflows/_shared/scripts/pfstep/analysis.py, workflows/_shared/scripts/tests/test_preexisting.py, workflows/_shared/scripts/tests/test_close_preexisting.py, workflows/_shared/scripts/tests/test_build_scoped_close.py, workflows/_shared/scripts/tests/test_analysis_validator.py
"""The baseline triage of a still-red check (polyforge#212): build's close
(``fast_checks.py --close-scoped`` and the per-unit ``--repo`` run) and
verification judge a red that survived its flaky re-run against the change's
baseline with the SAME code — the judgment, its classes, the candidate
filter, the protected paths and the skip form all live in
``pfstep/preexisting.py`` and both callers import the one entry
(``judge_on_base``); the result records each red's class, signature and the
baseline commit it was judged on, and only a red every baseline run carries
the same way is pre-existing — this change's reds and anything the base
could not settle keep the existing red rules. The fixtures are the shared
ones: verifyfix's capsule, the fake engines, and test_verify_flows's
FAKE_PYTEST / FAKE_NPM scripts whose failure lines a parser locates."""

import ast
import os
import unittest
from typing import List

import verify
from pfstep import preexisting
from pfstep.rerun import UnitState
from pfstep.units import KIND_GO, Unit

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VERIFY_PY = os.path.join(SCRIPTS, "verify.py")
FAST_CHECKS_PY = os.path.join(SCRIPTS, "fast_checks.py")

# the judgment's own names: the calls stay inside pfstep/preexisting.py, so
# no caller ever judges a red by a second copy of the rule
JUDGMENT_CALLS = ("classify_base", "classify_base_class", "unit_presence")


def direct_judgment_calls(path: str) -> List[str]:
    """The judgment functions path's source calls by name (a Call on a bare
    Name or an Attribute whose attr is one of them)."""
    with open(path, encoding="utf-8") as f:
        tree = ast.parse(f.read())
    out: List[str] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        name = node.func.id if isinstance(node.func, ast.Name) else (
            node.func.attr if isinstance(node.func, ast.Attribute) else "")
        if name in JUDGMENT_CALLS:
            out.append("%s:%d" % (os.path.basename(path), node.lineno))
    return out


class SharedEntryTest(unittest.TestCase):
    def test_verify_wires_the_shared_judgment(self) -> None:
        # the shared entry exists, and verify references the very same object
        # (getattr'd into assertions: the first red is the missing wiring
        # itself, never an AttributeError)
        self.assertIsNotNone(getattr(preexisting, "judge_on_base", None),
                             "judge_on_base must live in pfstep.preexisting: the one entry both callers judge by")
        self.assertIs(getattr(verify, "judge_on_base", None), getattr(preexisting, "judge_on_base", None),
                      "verify imports the shared entry, never a copy of it")
        for name in ("candidates", "join_note", "skip_form", "path_is_protected", "protected_paths", "BASE_RUNS"):
            self.assertTrue(hasattr(preexisting, name), "%s lives in pfstep.preexisting" % name)
        # the judgment itself runs once: neither caller calls the rule
        # functions directly (pfstep/preexisting.py is the only judge)
        self.assertEqual(direct_judgment_calls(VERIFY_PY), [],
                         "verify.py must judge through pfstep.preexisting, not call the rules itself")
        # verify's skip_form stays its tested thin wrapper over the shared
        # core, its wording byte-identical (test_verify_rules and
        # test_shellwords pin the forms; here only the delegation)
        pre = UnitState(unit=Unit(kind=KIND_GO, name="TestOld", failed=True), pre_existing=True)
        cr = verify.CmdRun(repo="app", check="app:x", name="x", phase="full", cmd="GOWORK=off go test -count=1 ./...",
                           origin=verify.ORIGIN_PLAN, timeout=0, units=[pre])
        self.assertEqual(verify.skip_form(cr),
                         preexisting.skip_form(cr.cmd, cr.repo, cr.units, cr.harness, "verify-run"))


if __name__ == "__main__":
    unittest.main()
