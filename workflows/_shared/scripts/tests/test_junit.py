"""pfstep.junit: a declared JUnit report's failed testcases (the Go table
of verifyrun/w371_junit_report_test.go, asserting the Go behavior; the
Run-level tests of that file live with the verify.py run tests)."""

import os
import tempfile
import unittest

from pfstep import engine, junit


class ParseJUnitTest(unittest.TestCase):
    def test_parse_junit_shapes(self) -> None:
        """Port of TestW371ParseJUnitShapes (w371_junit_report_test.go)."""
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "r.xml")
            body = """<testsuite name="root" file="pkg/a_test.py">
  <testsuite name="nested">
    <testcase name="test_err" classname="pkg.a"><error message="boom">Traceback</error></testcase>
    <testcase name="test_fail"><failure>first line
second line</failure></testcase>
    <testcase name="test_ok" classname="pkg.a"/>
  </testsuite>
</testsuite>"""
            with open(p, "w", encoding="utf-8") as f:
                f.write(body)
            cases = junit.parse_junit_file(p)
            self.assertEqual(len(cases), 2, cases)
            self.assertEqual((cases[0].name, cases[0].file, cases[0].signature), ("pkg.a::test_err", "pkg/a_test.py", "boom | Traceback"))
            self.assertEqual((cases[1].name, cases[1].signature), ("test_fail", "first line"))
            with open(p, "w", encoding="utf-8") as f:
                f.write('<report><testcase name="x"><failure/></testcase></report>')
            with self.assertRaises(ValueError) as cm:
                junit.parse_junit_file(p)
            self.assertIn("root element", str(cm.exception), "a non-JUnit root")


class RealReportTest(unittest.TestCase):
    def test_a_real_pytest_junit_report(self) -> None:
        """A real report (pytest --junitxml, testdata/outputs/pytest-junit.xml):
        its four failed testcases, as Go's parseJUnitFile read them while it
        existed (the differential test, verifyrun/w476_python_units_diff_test.go
        at its first commit)."""
        path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata", "outputs", "pytest-junit.xml")
        got = [(c.name, c.file, c.signature) for c in junit.parse_junit_file(path)]
        self.assertEqual(got, [
            ("tests.test_calc::test_add", "", "assert 4 == 3 + where 4 = add(1, 2) | def test_add():"),
            ("tests.test_calc::test_param[1-1-2]", "", "assert 3 == 2 + where 3 = add(1, 1) | a = 1, b = 1, want = 2"),
            ("tests.test_calc::test_error", "", "RuntimeError: boom | def test_error():"),
            ("tests.test_calc.TestGroup::test_method", "",
             "assert [1, 2] == [1, 3] At index 1 diff: 2 != 3 Use -v to get more diff"
             " | self = <test_calc.TestGroup object at 0x7f1a2d882840>"),
        ])


class DecodeReportTest(unittest.TestCase):
    def test_decode_report_carries_the_declaration(self) -> None:
        """Port of TestW371DecodeReportCarriesTheDeclaration (w371_junit_report_test.go)."""
        raw = ('{"schema":1,"mode":"full","commands":['
               '{"name":"e2e","phase":"full","cmd":"npm run e2e","timeout":null,"report":{"format":"junit","path":"out/*.xml"},'
               '"source":"conf"},'
               '{"name":"unit","phase":"full","cmd":"go test ./...","timeout":null,"report":null,"source":"conf"}]}')
        rep = engine.decode_report(raw.encode())
        d = rep.commands[0].report
        self.assertIsNotNone(d)
        assert d is not None
        self.assertEqual((d.format, d.path), (junit.FORMAT_JUNIT, "out/*.xml"))
        self.assertIsNone(rep.commands[1].report, "an undeclared report decoded")


if __name__ == "__main__":
    unittest.main()
