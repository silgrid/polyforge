# carries: workflows/_shared/scripts/pfstep/analysis.py, workflows/_shared/scripts/pfstep/unitruns.py, workflows/_shared/scripts/verify.py
"""The change analysis's one shared validator (pfstep/analysis.py and
pfstep/unitruns.py — polyforge#112): the run's shape rules (argv, cwd,
marker), the file and name rules at HEAD (a bare name verbatim, a
`.`-, `::`- or `/`-joined name segment by segment), the unknown-repo
message and the supplement cap's taken/cut split. The exact messages are
the contract verification has printed since polyforge#52 and #65 — pinned
by tests/test_verify_analysis.py, which AC4 keeps passing unchanged — so
the expected strings here are copied from that existing contract, never
derived from the implementation under test. Both callers (verify.py's
--analysis check and facts.py's close check) call these functions; a
second copy of a rule may not appear."""

import unittest
from typing import Any, Callable, Dict, List, Optional, Tuple

from pfstep.analysis import (
    Analysis,
    AnalysisDecl,
    SelectedCommand,
    SelectedDecl,
    name_segments,
    read_analysis,
    selected_problem,
    static_problem,
)
from pfstep.unitruns import argv_line, run_shape_problem, unknown_repo_problem

# the file the fixture entries name, and its body at HEAD
A_FILE = "tests/test_a.py"
A_BODY = "class BuildCase:\n    def test_ok(self):\n        pass\n"

ARGV_EMPTY = "its run has no argv (units.json run.argv is empty): nothing to run as written"
CWD_OUTSIDE = 'its cwd "/abs" is not a directory inside the repo'
CWD_NO_DIR = "its cwd sub is no directory of repos/app at HEAD"
BAD_MARKER = 'its marker "^(" is not a regexp: '
EMPTY_MARKER = ('its marker "a*" matches the empty string: every output would show it,'
                " so it proves no pass")
FILE_NOT_TRACKED = ('its file "tests/gone.py" is not a tracked file of repos/app at HEAD:'
                    " the analysis lists a test that does not exist")
NAME_NOWHERE = ('its name "test_ok + test_other" appears nowhere in "tests/test_a.py" at HEAD:'
                " the analysis lists a test that does not exist")
SEGMENT_NOWHERE = ('its name segment "NoSuchCase" appears nowhere in "tests/test_a.py" at HEAD:'
                   " the analysis lists a test that does not exist")
UNKNOWN_REPO = ("repos/web is not a task repo of this capsule (capsule.json names it"
                " not, or it is not cloned under repos/)")


def decl(file: str = A_FILE, name: str = "", cwd: str = ".",
         argv: Optional[List[str]] = None, marker: str = "^ok$") -> AnalysisDecl:
    """One listed test with the field defaults every entry carries."""
    return AnalysisDecl(id="a1", repo="app", file=file, name=name, cwd=cwd,
                        argv=list(argv) if argv is not None else ["python3", "-m", "unittest", "-h"],
                        marker=marker, why="pins it")


def tracked_of(files: List[str]) -> Callable[[str], bool]:
    return lambda p: p in files


def body_of(bodies: Dict[str, str]) -> Callable[[str], str]:
    return lambda p: bodies.get(p, "")


class RunShapeTest(unittest.TestCase):
    """The shape rules every recorded run (a units.json run and an
    analysis entry alike) is judged by, with the cleaned cwd they leave."""

    def test_no_argv_names_the_rule(self) -> None:
        self.assertEqual(run_shape_problem([], ".", "^x$", "app", lambda c: True), (ARGV_EMPTY, ""))

    def test_a_blank_first_word_is_no_argv(self) -> None:
        self.assertEqual(run_shape_problem([" "], ".", "^x$", "app", lambda c: True), (ARGV_EMPTY, ""))

    def test_a_cwd_outside_the_repo(self) -> None:
        self.assertEqual(run_shape_problem(["x"], "/abs", "^x$", "app", lambda c: True), (CWD_OUTSIDE, ""))

    def test_a_cwd_that_is_no_directory_at_head(self) -> None:
        problem, cwd = run_shape_problem(["x"], "sub", "^x$", "app", lambda c: False)
        self.assertEqual(problem, CWD_NO_DIR)
        self.assertEqual(cwd, "sub", "the cleaned cwd stays for the record")

    def test_a_bad_marker(self) -> None:
        problem, _cwd = run_shape_problem(["x"], "sub", "^(", "app", lambda c: True)
        self.assertTrue(problem.startswith(BAD_MARKER), problem)

    def test_a_marker_matching_the_empty_string(self) -> None:
        problem, _cwd = run_shape_problem(["x"], "sub", "a*", "app", lambda c: True)
        self.assertEqual(problem, EMPTY_MARKER)

    def test_a_run_that_can_run(self) -> None:
        self.assertEqual(run_shape_problem(["x"], "  . ", "^x$", "app", lambda c: True), ("", "."))


class EntryProblemTest(unittest.TestCase):
    """One analysis entry's static rules: shape first, then the file at
    HEAD, then the name inside it."""

    def setUp(self) -> None:
        self.is_dir: Callable[[str], bool] = lambda c: True
        self.tracked = tracked_of([A_FILE])
        self.body = body_of({A_FILE: A_BODY})

    def problem(self, d: AnalysisDecl) -> Tuple[str, str]:
        return static_problem(d, "app", self.is_dir, self.tracked, self.body)

    def test_a_shape_problem_wins_over_the_file(self) -> None:
        self.assertEqual(self.problem(decl(argv=[], file="nope")), (ARGV_EMPTY, ""))

    def test_a_file_that_is_no_repo_relative_path(self) -> None:
        for f in ("", "/etc/x", "../x"):
            with self.subTest(f):
                self.assertEqual(self.problem(decl(file=f))[0],
                                'its file "%s" is not a repo-relative path' % f)

    def test_a_file_that_is_not_tracked_at_head(self) -> None:
        problem, cwd = self.problem(decl(file="tests/gone.py"))
        self.assertEqual(problem, FILE_NOT_TRACKED)
        self.assertEqual(cwd, ".")

    def test_a_composite_bare_name_appears_nowhere(self) -> None:
        """polyforge#79's a3 class: two names joined into one string — a
        bare name (no separator), judged verbatim, so it does not exist."""
        self.assertEqual(self.problem(decl(name="test_ok + test_other")), (NAME_NOWHERE, "."))

    def test_a_descriptive_name_appears_nowhere(self) -> None:
        """polyforge#97's class: a scenario's description, not a test's
        name — bare (no separator), so judged verbatim."""
        self.assertEqual(self.problem(decl(name="app-suite (scenario, D0–D7)"))[0],
                         ('its name "app-suite (scenario, D0–D7)" appears nowhere in'
                          ' "tests/test_a.py" at HEAD: the analysis lists a test that'
                          " does not exist"))

    def test_a_cross_file_name_appears_nowhere_in_the_named_file(self) -> None:
        """polyforge#79's a12 class: the entry names one file, the test
        lives in another; the name is judged inside the named file."""
        self.assertEqual(self.problem(decl(name="test_other"))[0],
                         ('its name "test_other" appears nowhere in "tests/test_a.py" at'
                          " HEAD: the analysis lists a test that does not exist"))

    def test_a_bare_name_that_exists(self) -> None:
        self.assertEqual(self.problem(decl(name="test_ok")), ("", "."))

    def test_a_qualified_name_passes_when_every_segment_is_there(self) -> None:
        for name in ("BuildCase.test_ok", "BuildCase::test_ok", "BuildCase/test_ok"):
            with self.subTest(name):
                self.assertEqual(self.problem(decl(name=name)), ("", "."))

    def test_a_missing_segment_is_named(self) -> None:
        self.assertEqual(self.problem(decl(name="NoSuchCase.test_ok")), (SEGMENT_NOWHERE, "."))

    def test_no_name_skips_the_name_rule(self) -> None:
        self.assertEqual(self.problem(decl(name="")), ("", "."))


class HelperTest(unittest.TestCase):
    def test_name_segments(self) -> None:
        table: List[Tuple[str, Optional[List[str]]]] = [
            ("test_ok", None),
            ("test_ok + test_other", None),          # #79's composite: bare
            ("app-suite (scenario, D0–D7)", None),   # #97's descriptive: bare
            ("BuildCase.", None),                    # one non-empty segment: verbatim
            ("BuildCase.test_ok", ["BuildCase", "test_ok"]),
            ("BuildCase::test_ok", ["BuildCase", "test_ok"]),
            ("BuildCase/test_ok", ["BuildCase", "test_ok"]),
            ("a..b", ["a", "b"]),                    # empty parts drop, the parts remain
        ]
        for name, want in table:
            with self.subTest(name):
                self.assertEqual(name_segments(name), want)

    def test_unknown_repo_problem(self) -> None:
        self.assertEqual(unknown_repo_problem("web"), UNKNOWN_REPO)

    def test_argv_line(self) -> None:
        self.assertEqual(argv_line("", ["go", "test", "./..."]), "go test ./...")
        self.assertEqual(argv_line(".", ["go", "test"]), "go test")
        self.assertEqual(argv_line("sub", ["go", "test"]), "(cd sub) go test")

    # -- the machine selection records (polyforge#164): the one shared
    # validator's own rules, and the file's parsing of the section

    def sel(self, **kw: Any) -> SelectedDecl:
        """A green selection record with the field defaults."""
        base = SelectedDecl(head="b" * 40, engine="the engine --json --light", mode="light", status="green")
        for k, v in kw.items():
            setattr(base, k, v)
        return base

    def test_a_green_record_with_evidence_passes(self) -> None:
        d = self.sel(commands=[SelectedCommand(name="gate", cmd="echo ok", phase="scoped", exit_code=0),
                               SelectedCommand(name="fast", cmd="echo f", phase="fast", reused="fast.json#fast")])
        self.assertEqual(selected_problem(d, "b" * 40), "")

    def test_a_head_of_another_tree_is_stale(self) -> None:
        self.assertIn("is not the tree it is judged at", selected_problem(self.sel(), "c" * 40))
        self.assertIn("stale", selected_problem(self.sel(), "c" * 40))
        self.assertEqual(selected_problem(self.sel(), ""), "", "a caller that cannot read a HEAD reports it itself")

    def test_a_selection_that_is_not_green_cannot_stand(self) -> None:
        self.assertIn("its selection is incomplete: a lockfile changed",
                      selected_problem(self.sel(status="incomplete", reason="a lockfile changed"), "b" * 40))
        self.assertIn("its selection records no status",
                      selected_problem(self.sel(status=""), "b" * 40))

    def test_a_pre_existing_record_stands_like_a_green_one(self) -> None:
        """polyforge#212: the close's pre_existing selection record stands
        like a green one — the same evidence rules, a skip-form confirmation
        among them; a red or an empty status still cannot."""
        head = "b" * 40
        d = self.sel(status="pre_existing",
                     commands=[SelectedCommand(name="gate", cmd="echo ok", phase="scoped", exit_code=0),
                               SelectedCommand(name="tests", cmd="pytest", phase="scoped", exit_code=1,
                                               confirmed="pytest --deselect tests/test_a.py::test_old")])
        self.assertEqual(selected_problem(d, head), "", "a pre_existing record is green-equivalent evidence")
        self.assertIn("its selection is red", selected_problem(self.sel(status="red", reason="nothing ran"), head))

    def test_the_file_parses_a_pre_existing_record_and_refuses_unknown_statuses(self) -> None:
        """polyforge#212: read_analysis takes the close's new status and
        names all four it accepts when a record carries another."""
        import json as _json
        import tempfile
        import os
        good = {"head": "b" * 40, "engine": "the engine --json --light", "mode": "light", "status": "pre_existing",
                "commands": [{"name": "tests", "cmd": "pytest", "phase": "scoped", "exit_code": 1,
                              "confirmed": "pytest --deselect tests/test_a.py::test_old"}]}
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "change-analysis.json")
            with open(path, "w", encoding="utf-8") as f:
                _json.dump({"tests": [], "selected": {"app": good}}, f)
            try:
                an = read_analysis(path)
            except ValueError as err:
                self.fail("a pre_existing record must parse: %s" % err)
            self.assertEqual(an.selected["app"].status, "pre_existing")
            self.assertEqual(selected_problem(an.selected["app"], "b" * 40), "")
            with open(path, "w", encoding="utf-8") as f:
                _json.dump({"tests": [], "selected": {"app": dict(good, status="purple")}}, f)
            with self.assertRaises(ValueError) as cm:
                read_analysis(path)
            self.assertIn("status is not green, pre_existing, red or incomplete", str(cm.exception))

    def test_a_command_green_through_its_flaky_rerun_is_evidence(self) -> None:
        """The per-unit bar's own green: a red first run whose failing units
        re-ran flaky and whose whole command was then confirmed — one whole
        re-run exiting 0 (`confirmed`), or its completion proof
        (`complete`, the entry keeping the first run's red exit beside it)
        — is green evidence, the record stating it with either."""
        head = "b" * 40
        confirmed = SelectedCommand(name="pin", cmd="go test ./...", phase="scoped", exit_code=1,
                                    confirmed="go test ./...")
        complete = SelectedCommand(name="pin", cmd="sh e2e.sh", phase="scoped", exit_code=1,
                                   complete="PASS S1; PASS S2; 2 suites")
        d = self.sel(commands=[confirmed, complete])
        self.assertEqual(selected_problem(d, head), "")
        # still red without either proof
        c = SelectedCommand(name="pin", cmd="go test ./...", phase="scoped", exit_code=1)
        self.assertIn("exited 1", selected_problem(self.sel(commands=[c]), head))

    def test_a_command_without_green_evidence_cannot_stand(self) -> None:
        head = "b" * 40
        cases = (
            (SelectedCommand(name="pin", cmd="pin", phase="scoped", exit_code=3), "exited 3"),
            (SelectedCommand(name="pin", cmd="pin", phase="scoped", skipped=True, skip_reason="env NO"), "cannot run"),
            (SelectedCommand(name="pin", cmd="pin", phase="scoped", timed_out=True), "killed at its timeout"),
            (SelectedCommand(name="pin", cmd="pin", phase="scoped"), "carries no run"),
        )
        for c, want in cases:
            with self.subTest(want):
                self.assertIn(want, selected_problem(self.sel(commands=[c]), head))

    def test_the_file_parses_the_selected_records(self) -> None:
        import json as _json
        import tempfile
        import os
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "change-analysis.json")
            doc = {"tests": [], "selected": {"app": {
                "head": "b" * 40, "tree": "t" * 40, "base": "a" * 40, "default_branch": "main",
                "engine": "the engine --json --light", "conf": ".polyforge/verify-scope.conf",
                "conf_changed_in_diff": True, "mode": "light", "paths": ["lib/x.py"], "suites": ["e2e/s1.mjs"],
                "test_glob": ["*_test.go"], "status": "green",
                "commands": [{"name": "gate", "root": "repos/app", "cmd": "echo ok", "phase": "scoped",
                              "source": "conf", "exit_code": 0},
                             {"name": "fast", "root": "repos/app", "cmd": "echo f", "phase": "fast",
                              "reused": "fast-checks.json#fast"},
                             {"name": "flaky", "root": "repos/app", "cmd": "go test ./...", "phase": "scoped",
                              "exit_code": 1, "confirmed": "go test ./..."},
                             {"name": "suites", "root": "repos/app", "cmd": "sh e2e.sh", "phase": "scoped",
                              "exit_code": 1, "complete": "PASS S1; PASS S2; 2 suites"}]}}}
            with open(path, "w", encoding="utf-8") as f:
                _json.dump(doc, f)
            an = read_analysis(path)
            self.assertEqual(list(an.selected), ["app"])
            d = an.selected["app"]
            self.assertEqual((d.head, d.engine, d.mode, d.status), ("b" * 40, "the engine --json --light", "light", "green"))
            self.assertEqual((d.conf, d.conf_changed_in_diff, d.paths, d.suites, d.test_glob),
                             (".polyforge/verify-scope.conf", True, ["lib/x.py"], ["e2e/s1.mjs"], ["*_test.go"]))
            self.assertEqual([(c.name, c.cmd, c.phase, c.exit_code, c.reused, c.confirmed, c.complete) for c in d.commands],
                             [("gate", "echo ok", "scoped", 0, "", "", ""),
                              ("fast", "echo f", "fast", None, "fast-checks.json#fast", "", ""),
                              ("flaky", "go test ./...", "scoped", 1, "", "go test ./...", ""),
                              ("suites", "sh e2e.sh", "scoped", 1, "", "", "PASS S1; PASS S2; 2 suites")])

    def test_the_file_rejects_a_bad_selected_shape(self) -> None:
        import json as _json
        import tempfile
        import os
        good = {"head": "b" * 40, "engine": "the engine", "mode": "light", "status": "green", "commands": []}
        bad = (
            ("no head", {k: v for k, v in good.items() if k != "head"}),
            ("no engine", {k: v for k, v in good.items() if k != "engine"}),
            ("status", dict(good, status="done")),
            ("commands", dict(good, commands="nope")),
            ("a command without a cmd", dict(good, commands=[{"name": "x"}])),
            ("head not a string", dict(good, head=42)),
        )
        with tempfile.TemporaryDirectory() as td:
            path = os.path.join(td, "change-analysis.json")
            for name, sel in bad:
                with self.subTest(name):
                    with open(path, "w", encoding="utf-8") as f:
                        _json.dump({"tests": [], "selected": {"app": sel}}, f)
                    with self.assertRaises(ValueError) as cm:
                        read_analysis(path)
                    self.assertIn("selected", str(cm.exception))

    def test_the_cap_takes_the_head_and_names_the_cut_tail(self) -> None:
        decls = [AnalysisDecl(id="a%d" % (i + 1)) for i in range(12)]
        taken, cut = Analysis(decls=decls).taken()
        self.assertEqual([d.id for d in taken], ["a%d" % (i + 1) for i in range(10)])
        self.assertEqual([d.id for d in cut], ["a11", "a12"])
        taken, cut = Analysis(limit=1, decls=decls).taken()
        self.assertEqual([d.id for d in taken], ["a1"])
        self.assertEqual([d.id for d in cut], ["a%d" % (i + 1) for i in range(1, 12)])


if __name__ == "__main__":
    unittest.main()
