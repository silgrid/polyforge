"""pfstep.rerun: a failing unit's targeted re-run keeps what the planned
command carried (assignments, flags, options) and refuses what it cannot
carry — the Go tables of verifyrun/rerun_test.go and
w403_light_tier_test.go, asserting the Go behavior."""

import unittest

from pfstep import duration, rerun


class GoRerunTest(unittest.TestCase):
    def test_go_rerun_carries_the_planned_flags(self) -> None:
        """Port of TestGoRerunCarriesThePlannedFlags (rerun_test.go)."""
        for cmd, run, target, want, want_why in (
            ("GOWORK=off go test -race -tags 'a b' -count=3 -v -run Foo -json ./...", "^TestX$", "./a/",
             "GOWORK=off go test -race -tags 'a b' -count=1 -v -timeout 20m0s -run '^TestX$' ./a/", ""),
            # the plan's timeout stays: a hang must not pass
            ("go test -shuffle on -timeout=5m ./pkg/...", "", "example.com/m/pkg",
             "go test -shuffle on -timeout=5m -count=1 -v example.com/m/pkg", ""),
            ("GOWORK=off make test", "^TestY$", "./test/",
             "GOWORK=off go test -count=1 -v -timeout 20m0s -run '^TestY$' ./test/", ""),
            ("go test ./... -args -flag", "^TestZ$", ".", "", "-args"),
        ):
            with self.subTest(cmd):
                got, why = rerun.go_rerun(cmd, run, target, 20 * duration.MINUTE)
                self.assertEqual(got, want, why)
                if want_why:
                    self.assertIn(want_why, why)


class PyRerunTest(unittest.TestCase):
    def test_py_rerun_carries_options_and_quotes_assignments(self) -> None:
        """Port of TestPyRerunCarriesOptionsAndQuotesAssignments (rerun_test.go)."""
        got, why = rerun.py_rerun('PYTEST_ADDOPTS="-p no:cacheprovider" python3 -m pytest -q --strict-markers tests', "pytest",
                                  "tests/a.py::test_x", rerun.PYTEST_SWITCHES)
        self.assertEqual(
            (got, why),
            ('PYTEST_ADDOPTS="-p no:cacheprovider" python3 -m pytest -q --strict-markers tests/a.py::test_x', ""))
        got, why = rerun.py_rerun("pytest -W error tests", "pytest", "tests/a.py::test_x", rerun.PYTEST_SWITCHES)
        self.assertEqual(got, "", "an option with a separate value must refuse the re-run")
        self.assertIn("-W", why)
        got, _ = rerun.py_rerun("python3 -m unittest -v pkg.test_m", "unittest", "pkg.test_m.T.test_x", rerun.UNITTEST_SWITCHES)
        self.assertEqual(got, "python3 -m unittest -v pkg.test_m.T.test_x")
        got, why = rerun.py_rerun("python3 -m unittest discover -s tests", "unittest", "a.T.test_x", rerun.UNITTEST_SWITCHES)
        self.assertEqual(got, "", "discover must refuse")
        self.assertIn("discover", why)


class SuiteRerunTest(unittest.TestCase):
    def test_suite_rerun_from_the_aggregate_runner(self) -> None:
        """Port of TestSuiteRerunFromTheAggregateRunner (rerun_test.go)."""
        for cmd, printed, want, want_why in (
            ("npm run test:e2e:all", "npm run test:e2e:all -- b.mjs", "npm run test:e2e:all -- b.mjs", ""),
            ("E2E_JOBS=2 npm run test:e2e:all", "npm run test:e2e:all -- b.mjs", "E2E_JOBS=2 npm run test:e2e:all -- b.mjs", ""),
            ("node e2e/all.mjs", "npm run test:e2e:all -- b.mjs", "npm run test:e2e:all -- b.mjs", ""),
            ("npm run test:e2e:all", "", "npm run test:e2e:all -- e2e/b.mjs", ""),
            ("E2E_JOBS=2 npm run test:e2e:all --", "", "E2E_JOBS=2 npm run test:e2e:all -- e2e/b.mjs", ""),
            ("npm run test:e2e:all -- a.mjs b.mjs", "", "", "no `re-run:` line"),
            ("make e2e", "", "", "no `re-run:` line"),
            ("npm run --silent test:e2e:all", "npm run test:e2e:all -- b.mjs", "", "--silent"),
            ("cd web && npm run test:e2e:all", "npm run test:e2e:all -- b.mjs", "", "not one simple command"),
        ):
            with self.subTest(cmd=cmd, printed=printed):
                got, why = rerun.suite_rerun(cmd, printed, "e2e/b.mjs")
                self.assertEqual(got, want, why)
                if want_why:
                    self.assertIn(want_why, why)
                else:
                    self.assertEqual(why, "")


class TemplateRerunTest(unittest.TestCase):
    def test_template_substitutes_the_quoted_unit(self) -> None:
        """Port of TestW403TemplateSubstitutesTheQuotedUnit (w403_light_tier_test.go)."""
        self.assertEqual(rerun.template_rerun("x -- {unit} && echo {unit}", "e2e/a b.mjs"),
                         "x -- 'e2e/a b.mjs' && echo 'e2e/a b.mjs'")


if __name__ == "__main__":
    unittest.main()
