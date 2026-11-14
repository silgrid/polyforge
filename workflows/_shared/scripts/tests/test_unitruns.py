"""pfstep.unitruns: the build's recorded unit runs (units.json) — the one
declaration, the file's parsing and the marker rules verify.py judges a
green run's output by (and build checks on the spot through fast_checks.py).
polyforge#23's marker — anchored ``$`` against a line node --test suffixed
with its elapsed time — is the sample that must not match.
"""

import json
import os
import shutil
import tempfile
import unittest

from pfstep import unitruns

# polyforge#23: the marker build recorded (u1) and the line the run
# actually printed — node --test appends the elapsed time after the
# description, so the anchored marker matches no line.
MARKER_23 = "^✔ status and elapsed time are in a separate row below command and description$"
LINE_23 = "✔ status and elapsed time are in a separate row below command and description (6.212729ms)"


class MarkerProblemTest(unittest.TestCase):
    def test_ok_marker_names_no_problem(self) -> None:
        self.assertEqual(unitruns.marker_problem("^--- PASS: TestAnswer "), "")

    def test_blank_marker_proves_nothing(self) -> None:
        self.assertIn("names no marker", unitruns.marker_problem(""))
        self.assertIn("names no marker", unitruns.marker_problem("  "))

    def test_non_regexp_marker(self) -> None:
        self.assertIn("not a regexp", unitruns.marker_problem("^--- PASS: TestAnswer ("))

    def test_marker_matching_the_empty_string(self) -> None:
        for m in (".*", "^", "a*"):
            self.assertIn("matches the empty string", unitruns.marker_problem(m))


class MarkerMatchesTest(unittest.TestCase):
    def test_the_23_marker_matches_no_suffixed_line(self) -> None:
        self.assertFalse(unitruns.marker_matches(MARKER_23, [LINE_23]))
        self.assertFalse(unitruns.marker_matches(MARKER_23, ["some other line", LINE_23, ""]))

    def test_the_23_marker_matches_the_exact_line(self) -> None:
        self.assertTrue(unitruns.marker_matches(MARKER_23, [MARKER_23[1:-1]]))

    def test_a_searched_ascii_match(self) -> None:
        self.assertTrue(unitruns.marker_matches("^--- PASS: TestAnswer ", ["other", "--- PASS: TestAnswer (0.00s)"]))
        self.assertFalse(unitruns.marker_matches("^--- PASS: TestAnswer ", ["--- FAIL: TestAnswer (0.00s)"]))

    def test_a_broken_regexp_matches_nothing(self) -> None:
        self.assertFalse(unitruns.marker_matches("^--- PASS: TestAnswer (", ["--- PASS: TestAnswer x"]))


class CleanCwdTest(unittest.TestCase):
    def test_the_root_forms(self) -> None:
        self.assertEqual(unitruns.clean_cwd(""), (".", ""))
        self.assertEqual(unitruns.clean_cwd("."), (".", ""))
        self.assertEqual(unitruns.clean_cwd("  "), (".", ""))

    def test_a_directory_inside_the_repo(self) -> None:
        self.assertEqual(unitruns.clean_cwd(" sub "), ("sub", ""))
        self.assertEqual(unitruns.clean_cwd("a/b"), ("a/b", ""))

    def test_outside_is_a_problem(self) -> None:
        self.assertNotEqual(unitruns.clean_cwd("/abs")[1], "")
        self.assertNotEqual(unitruns.clean_cwd("..")[1], "")
        self.assertNotEqual(unitruns.clean_cwd("../x")[1], "")


class ReadUnitRunsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.mkdtemp(prefix="pfstep-ur-")
        self.addCleanup(lambda: shutil.rmtree(self.tmp, True))

    def path(self, name: str, doc: object) -> str:
        p = os.path.join(self.tmp, name)
        with open(p, "w", encoding="utf-8") as f:
            json.dump(doc, f)
        return p

    def test_one_recorded_unit_parses(self) -> None:
        p = self.path("ok.json", [{"id": "u1", "test_commit": "t", "impl_commit": "i",
                                   "run": {"repo": "demo", "cwd": "sub", "argv": ["go", "test", "./..."]},
                                   "marker": "^PASS "}])
        self.assertEqual(unitruns.read_unit_runs(p), [
            unitruns.UnitRunDecl(id="u1", repo="demo", cwd="sub", argv=["go", "test", "./..."],
                                 marker="^PASS ", test_commit="t"),
        ])

    def test_a_unit_without_a_run_keeps_an_empty_argv(self) -> None:
        p = self.path("norun.json", [{"id": "u2", "marker": "^PASS "}])
        self.assertEqual(unitruns.read_unit_runs(p), [unitruns.UnitRunDecl(id="u2", marker="^PASS ")])

    def test_the_check_name(self) -> None:
        self.assertEqual(unitruns.UnitRunDecl(id="u1", repo="demo").check(), "demo:unit-run:u1")

    def test_malformed_documents_are_value_errors(self) -> None:
        for name, doc in (
            ("notjson.json", "not json"),
            ("notarray.json", {"id": "u1"}),
            ("notobject.json", ["u1"]),
            ("badargv.json", [{"id": "u1", "run": {"argv": ["go", 1]}}]),
            ("badfield.json", [{"id": 1}]),
        ):
            p = os.path.join(self.tmp, name)
            with open(p, "w", encoding="utf-8") as f:
                f.write(doc if isinstance(doc, str) else json.dumps(doc))
            with self.assertRaises(ValueError, msg=name):
                unitruns.read_unit_runs(p)

    def test_a_missing_file_is_a_value_error(self) -> None:
        with self.assertRaises(ValueError):
            unitruns.read_unit_runs(os.path.join(self.tmp, "absent.json"))


if __name__ == "__main__":
    unittest.main()
