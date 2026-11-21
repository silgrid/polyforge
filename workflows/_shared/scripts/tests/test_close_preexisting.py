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
import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

import verify
from pfstep import preexisting
from pfstep.rerun import UnitState
from pfstep.units import KIND_GO, Unit

from .test_verify_flows import FAKE_NPM, FAKE_PYTEST, fake_bin
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, cmd_of, fake_engine, report, run_verify

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VERIFY_PY = os.path.join(SCRIPTS, "verify.py")
FAST_CHECKS_PY = os.path.join(SCRIPTS, "fast_checks.py")
FAST_CHECKS = FAST_CHECKS_PY

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


def close_units(res: Dict[str, Any]) -> Dict[str, Dict[str, Any]]:
    """The close result's unit entries by name (every command's units[])."""
    out: Dict[str, Dict[str, Any]] = {}
    for e in (res.get("details") or {}).get("commands") or []:
        for u in e.get("units") or []:
            out[u["name"]] = u
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


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class OneEntryTest(unittest.TestCase):
    """AC3: build's checks and verification call the one judgment — the
    static wiring (both import judge_on_base; neither calls the rules)
    and the behavior (one fixture, three reds, both entries agree)."""

    def setUp(self) -> None:
        import tempfile
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def test_both_sides_call_the_one_judgment(self) -> None:
        import fast_checks
        self.assertIsNotNone(getattr(fast_checks, "judge_on_base", None),
                             "fast_checks imports the shared entry: the per-unit bar and the close triage through it")
        self.assertIs(getattr(fast_checks, "judge_on_base", None), preexisting.judge_on_base,
                      "fast_checks imports the shared entry, never a copy of it")
        self.assertEqual(direct_judgment_calls(FAST_CHECKS_PY), [],
                         "fast_checks.py must judge through pfstep.preexisting, not call the rules itself")
        self.assertEqual(direct_judgment_calls(VERIFY_PY), [])

    def test_build_and_verification_class_the_same_red_alike(self) -> None:
        # expected values derived from FAKE_PYTEST's behaviour, not from the
        # implementation: test_old fails the same way on the base (its file
        # and its `# FAIL` line are the base's), test_moved only fails at
        # HEAD (the base's copy passes), test_new's file is the feature's
        fx = Fixture(self.root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\ndef test_moved():\n"}})
        fx.commit_in_repo("app", {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"
                                                       "def test_moved(): # FAIL assert moved\n",
                                   "tests/test_b.py": "def test_new(): # FAIL assert new\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("tests", "scoped", "pytest"))})
        env = fake_bin(self.root, "pytest", FAKE_PYTEST)
        from .test_build_scoped_close import run_close
        r = run_verify(fx.capsule, eng, env=env)
        _code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"), env=env)
        units = close_units(res)
        for name in ("tests/test_a.py::test_old", "tests/test_a.py::test_moved", "tests/test_b.py::test_new"):
            with self.subTest(name):
                v = r.unit(name)
                self.assertIn(units.get(name, {}).get("class"), ("pre_existing", "this_change"),
                              "the close classifies every still-red unit: %s" % units.get(name))
                want = "pre_existing" if v["status"] == "pre_existing" else "this_change"
                self.assertEqual(units[name]["class"], want,
                                 "verify says %s, the close says %s" % (v["status"], units[name]["class"]))
                self.assertIn(units[name].get("detail", ""), v.get("detail", ""),
                              "the two entries carry the same wording")


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class CloseClassificationTest(unittest.TestCase):
    """The baseline phase of fast_checks' --repo run and its close: every
    still-red unit of a red command carries its class, its signatures and
    the base runs' observation summary beside its failure — the classes
    expected from FAKE_PYTEST's behaviour, never from the implementation
    (exit codes, reds[] and the selection's status are the next unit's)."""

    def setUp(self) -> None:
        import tempfile
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def env(self, name: str = "pytest", script: str = FAKE_PYTEST) -> Dict[str, str]:
        return fake_bin(self.root, name, script)

    def close_units_of(self, name: str, base: Dict[str, str], feature: Dict[str, str],
                       **report_extra: Any) -> Dict[str, Dict[str, Any]]:
        from .test_build_scoped_close import run_close
        self.root = self.sub(name)
        fx = Fixture(self.root, {"app": base})
        fx.commit_in_repo("app", feature, "feature")
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("tests", "scoped", "pytest"), **report_extra)})
        _code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"), env=self.env())
        return close_units(res)

    def sub(self, name: str) -> str:
        d = os.path.join(self.root, name)
        os.makedirs(d)
        return d

    def test_units_of_a_close_carry_the_base_classes(self) -> None:
        cases = (
            ("the same failure on the base",
             {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"}, {"c.txt": "mine\n"},
             "tests/test_a.py::test_old", "pre_existing",
             [("status", "pre_existing"), ("class", "pre_existing"), ("signature", "assert old breakage"),
              ("head_signature", "assert old breakage")],
             "fails the same way on all 1 base runs"),
            ("a test the base lacks",
             {"tests/test_a.py": "def test_ok():\n"}, {"tests/test_b.py": "def test_new(): # FAIL assert new breakage\n"},
             "tests/test_b.py::test_new", "this_change",
             [("class", "this_change"), ("signature", "assert new breakage")],
             "the base does not have this test"),
            ("a test that passes on the base",
             {"tests/test_a.py": "def test_old():\n"}, {"tests/test_a.py": "def test_old(): # FAIL assert broke it\n"},
             "tests/test_a.py::test_old", "this_change",
             [("class", "this_change")], "it passes on the base"),
            ("a base failure without an assertion line",
             {"tests/test_a.py": "def test_old(): # FAIL \n"},
             {"tests/test_a.py": "def test_old(): # FAIL assert x\n"},
             "tests/test_a.py::test_old", "undetermined",
             [("class", "undetermined")], "without an assertion line"),
        )
        for name, base, feature, unit, want_cls, fields, want_why in cases:
            with self.subTest(name):
                units = self.close_units_of(name.replace(" ", "-"), base, feature)
                u = units.get(unit)
                self.assertIsNotNone(u, "%s: the close records the unit (%s)" % (unit, sorted(units)))
                assert u is not None
                self.assertEqual(u.get("class"), want_cls, u)
                for key, want in fields:
                    self.assertEqual(u.get(key), want, "%s: %s" % (unit, key))
                self.assertIn(want_why, u.get("base_detail", ""), "%s: base_detail keeps the observation summary" % unit)
                self.assertIn(want_why, u.get("detail", ""), "%s: the detail keeps the judgment's why" % unit)

    def test_a_base_run_that_never_reaches_the_test_is_undetermined(self) -> None:
        from .test_build_scoped_close import run_close
        from .verifyfix import git_t
        fx = Fixture(self.root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old\n",
                                          "tests/ENV_BROKEN": "x\n"}})
        os.remove(os.path.join(fx.repo("app"), "tests", "ENV_BROKEN"))
        git_t(fx.repo("app"), "commit", "-q", "-am", "feature")
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("tests", "scoped", "pytest"))})
        _code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"), env=self.env())
        u = close_units(res).get("tests/test_a.py::test_old")
        self.assertIsNotNone(u, u)
        assert u is not None
        self.assertEqual(u.get("class"), "undetermined", u)
        self.assertIn("did not reach the test's own failure", u.get("base_detail", ""), u)
        self.assertIn("did not reach the test's own failure", u.get("detail", ""), u)

    def test_a_failing_base_setup_leaves_every_red_undetermined(self) -> None:
        units = self.close_units_of("setup-false", {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
                                    {"c.txt": "mine\n"}, base_setup=["false"])
        u = units.get("tests/test_a.py::test_old")
        self.assertIsNotNone(u, u)
        assert u is not None
        self.assertEqual(u.get("class"), "undetermined", u)
        self.assertIn('the base_setup command "false" failed on the base', u.get("detail", ""))

    def test_a_protected_test_is_this_change_without_a_base_run(self) -> None:
        from .test_build_scoped_close import run_close
        fx = Fixture(self.root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"}})
        fx.commit_in_repo("app", {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n# the unit's test commit\n"}, "feature")
        units_file = os.path.join(self.root, "units.json")
        with open(units_file, "w", encoding="utf-8") as f:
            json.dump([{"id": "u1", "test_commit": fx.head("app"), "impl_commit": "",
                        "run": {"repo": "app", "cwd": ".", "argv": ["sh", "-c", "echo UNIT-OK"]},
                        "marker": "UNIT-OK"}], f)
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("tests", "scoped", "pytest"))})
        _code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                               "--unit-runs", units_file, env=self.env())
        u = close_units(res).get("tests/test_a.py::test_old")
        self.assertIsNotNone(u, u)
        assert u is not None
        self.assertEqual(u.get("class"), "this_change", u)
        self.assertIn("protected", u.get("detail", ""), u)

    def test_an_aggregate_e2e_without_a_skip_switch_keeps_its_residual(self) -> None:
        from .test_build_scoped_close import run_close
        fx = Fixture(self.root, {"app": {"e2e/a.mjs": "// a\n", "e2e/c.mjs": "// FAIL expected 2 rows\n"}})
        fx.commit_in_repo("app", {"e2e/b.mjs": "// b\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("e2e", "scoped", "npm run test:e2e:all"))})
        _code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                               env=self.env("npm", FAKE_NPM))
        units = close_units(res)
        self.assertEqual(units["e2e/c.mjs"].get("class"), "pre_existing", units["e2e/c.mjs"])
        residual = [u for u in units.values() if u.get("class") == "undetermined"]
        self.assertEqual(len(residual), 1, units)
        self.assertIn("no skip switch", residual[0].get("detail", ""), residual[0])

    def test_unit_runs_is_the_close_modes_own_argument(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"))})
        out = os.path.join(fx.capsule, "close.json")
        # without --close-scoped the argument names nothing
        argv = [sys.executable, FAST_CHECKS, "--capsule", fx.capsule, "--repo", "app",
                "--engine", eng, "--deadline", "10m", "--out", out, "--unit-runs", os.path.join(self.root, "units.json")]
        c = subprocess.run(argv, capture_output=True, text=True)
        self.assertEqual(c.returncode, 2, c.stderr)
        self.assertIn("--unit-runs names the build's units.json of --close-scoped, which is missing", c.stderr)
        # an unusable units.json is not judged, the message naming the file
        bad = os.path.join(self.root, "units.json")
        with open(bad, "w", encoding="utf-8") as f:
            f.write("{not json")
        argv = [sys.executable, FAST_CHECKS, "--capsule", fx.capsule, "--repo", "app", "--close-scoped",
                "--engine", eng, "--deadline", "10m", "--out", out, "--unit-runs", bad]
        c = subprocess.run(argv, capture_output=True, text=True)
        self.assertEqual(c.returncode, 2, c.stderr)
        self.assertIn("--unit-runs %s is not a usable units.json" % bad, c.stderr)


if __name__ == "__main__":
    unittest.main()
