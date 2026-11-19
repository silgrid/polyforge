"""fast_checks.py — build's written fast/scoped check: the scope engine's
light reports for one task repo, their fast- and scoped-phase commands run
in order, each in its report's root, the full phase never run, the result
file carrying every command's exit code, log and skip reason (polyforge#26;
the engine is the real one, on minimal git fixtures with an origin).

polyforge#24's failure — npm ci run at the repository root because nothing
but the model's guess placed a command in its report's root — is the
scenario the nested-root tests (added with the nested planning) pin.
"""

import json
import os
import shutil
import subprocess
import sys
import typing
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .gitworld import GitCase, Repo, git, write
from .schemacheck import errors as schema_errors

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
ENGINE = os.path.join(SCRIPTS, "verify-scope")
FAST_CHECKS = os.path.join(SCRIPTS, "fast_checks.py")
SCHEMA = json.load(open(os.path.join(SCRIPTS, "schema", "pf-step-result-1.json"), encoding="utf-8"))

GO_MOD = "module demo\n\ngo 1.21\n"
APP = "package app\n\n// Answer is the answer.\nfunc Answer() int {\n\treturn 42\n}\n"
APP_TEST = "package app\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"not 42\")\n\t}\n}\n"

# A conf whose fast gates replace the analyzer's defaults, a full tail that
# would leave a file behind when run, and no forced-full path: the report
# stays scoped for a change under app/.
CONF = "stack=go\nfast_gate=pwd > root.txt\nfast_gate=echo fast-two\nfull_command=echo full-ran > full.txt\n"
APP_CHANGE = {"app/app.go": APP.replace("// Answer is the answer.\n", "// Answer is the answer. (changed)\n")}


def env_without(name: str) -> Dict[str, str]:
    env = dict(os.environ)
    env.pop(name, None)
    return env


class FastChecksCase(GitCase):
    """A capsule with one task repo: a clone whose origin carries its
    base, so the engine's default base (merge-base HEAD origin/<default>)
    is the base commit."""

    def setUp(self) -> None:
        super().setUp()
        if not (shutil.which("python3") and os.path.exists(ENGINE)):
            self.skipTest("python3 or the engine missing")
        self.capsule = self.new_dir("capsule")
        self.out = os.path.join(self.capsule, "fast-checks.json")

    def task_repo(self, name: str, files: Dict[str, str], change: Dict[str, str]) -> str:
        """A clone at <capsule>/repos/<name> one commit ahead of its
        origin, the change confined to the files the diff names."""
        bare = os.path.join(self.tmp, name + ".git")
        git(self.tmp, "init", "-q", "--bare", "-b", "main", name + ".git")
        clone = Repo(os.path.join(self.capsule, "repos", name), files)
        git(clone.dir, "remote", "add", "origin", bare)
        git(clone.dir, "push", "-q", "origin", "main")
        git(clone.dir, "remote", "set-head", "origin", "-a")
        clone.commit("the change", change)
        return clone.dir

    def run_checks(self, repo: str, env: Optional[Dict[str, str]] = None, extra: Tuple[str, ...] = ()) -> Tuple[int, Dict[str, Any]]:
        """fast_checks.py on the capsule's repo, as a skill runs it. The
        fixture is not a step context: the engine would read an inherited
        PF_CAPSULE (this suite's own run inside a step) and redirect the
        fixture's diff base to that step's fixed target branch — a branch
        the fixture's origin does not carry, so the report falls back to
        the full tier and the scoped phase never runs. The default child
        environment drops it; a caller's env is taken as given."""
        argv = [sys.executable, FAST_CHECKS, "--capsule", self.capsule,
                "--engine", ENGINE, "--deadline", "10m", "--out", self.out]
        if repo:
            argv += ["--repo", repo]
        argv += list(extra)
        c = subprocess.run(argv, capture_output=True, text=True, env=env if env is not None else env_without("PF_CAPSULE"))
        if not os.path.exists(self.out):
            raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (c.returncode, c.stderr[-500:]))
        with open(self.out, encoding="utf-8") as f:
            return c.returncode, json.load(f)

    def go_repo(self, conf: str = CONF, change: Optional[Dict[str, str]] = None) -> str:
        return self.task_repo("app", {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": conf,
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
        }, change if change is not None else APP_CHANGE)

    def commands_of(self, res: Dict[str, Any]) -> List[Dict[str, Any]]:
        return typing.cast(List[Dict[str, Any]], res["details"]["commands"])


class RootReportTest(FastChecksCase):
    """AC2/AC4/AC5: a change at the repository's own root — the report's
    root is the clone, the fast gates and the scoped selection run there in
    order, the full tail never runs, and the result file records it all."""

    def test_green(self) -> None:
        clone = self.go_repo()
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        self.assertEqual(schema_errors(SCHEMA, res), [])
        self.assertEqual(res["heads"], {"app": git(clone, "rev-parse", "HEAD")})
        cmds = self.commands_of(res)
        self.assertEqual([c["name"] for c in cmds], ["fast-1", "fast-2", "scoped-1"])
        self.assertEqual([c["phase"] for c in cmds], ["fast", "fast", "scoped"])
        self.assertEqual([c["root"] for c in cmds], ["repos/app"] * 3)
        self.assertIn("go test", cmds[2]["cmd"])
        for c in cmds:
            self.assertNotIn("skipped", c)
            self.assertEqual(c["exit_code"], 0)
            self.assertTrue(c["log"])
            path = c["log"] if os.path.isabs(c["log"]) else os.path.join(self.capsule, c["log"])
            self.assertTrue(os.path.exists(path), c["log"])
        # the command ran in the report's root: the repository's own root
        with open(os.path.join(clone, "root.txt"), encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), clone)
        # the full phase never ran
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")))

    def test_the_plan_is_recorded(self) -> None:
        self.go_repo()
        code, res = self.run_checks("app")
        self.assertEqual(code, 0)
        plans = res["details"]["reports"]
        self.assertEqual(len(plans), 1)
        self.assertEqual(plans[0]["root"], "repos/app")
        self.assertEqual(plans[0]["mode"], "scoped")


class RedCheckTest(FastChecksCase):
    """AC4: any non-zero exit is red, and every command still runs."""

    def test_a_failing_command_is_red_and_recorded(self) -> None:
        self.go_repo(conf="stack=go\nfast_gate=exit 3\nfast_gate=echo after\n"
                          "full_command=echo full-ran > full.txt\n")
        code, res = self.run_checks("app")
        self.assertEqual(code, 1)
        self.assertEqual(res["verdict"], "fail")
        cmds = self.commands_of(res)
        self.assertEqual([c["exit_code"] for c in cmds[:2]], [3, 0])
        self.assertIn("exit 3", res["summary"])

    def test_a_declared_timeout_kills_the_command(self) -> None:
        self.go_repo(conf="stack=go\ncheck=slow|fast|sleep 5\ncheck_timeout=slow|2s\n")
        code, res = self.run_checks("app")
        self.assertEqual(code, 1)
        slow = [c for c in self.commands_of(res) if c["name"] == "slow"][0]
        self.assertTrue(slow["timed_out"])
        self.assertNotEqual(slow["exit_code"], 0)


class SkipTest(FastChecksCase):
    """AC2: a command missing its declared prerequisite is skipped with
    the reason recorded, and a skip is not a red check."""

    def test_missing_prerequisites_are_skipped_with_reasons(self) -> None:
        self.go_repo(conf="stack=go\n"
                          "check=absentcmd|fast|echo never\ncheck_requires_cmd=absentcmd|no-such-bin-xyz\n"
                          "check=absentvar|fast|echo never\ncheck_requires_env=absentvar|FAST_CHECKS_TEST_VAR\n")
        code, res = self.run_checks("app", env=env_without("FAST_CHECKS_TEST_VAR"))
        self.assertEqual(code, 0, res)
        by_name = {c["name"]: c for c in self.commands_of(res)}
        for name in ("absentcmd", "absentvar"):
            self.assertTrue(by_name[name]["skipped"], name)
            self.assertIn("no-such-bin-xyz" if name == "absentcmd" else "FAST_CHECKS_TEST_VAR",
                          by_name[name]["skip_reason"], name)


class NestedRootTest(FastChecksCase):
    """AC3 (polyforge#24's scenario): a change confined to a subdirectory
    with its own verify-scope conf — the nested root — runs that root's
    gates in the subdirectory; the same gate run at the repository root,
    the pre-ticket approach, fails in the same scenario."""

    NESTED_FILES = {
        "webapp/.polyforge/verify-scope.conf":
            "stack=npm\nfast_gate=test -f package-lock.json && pwd > nested-root.txt\n",
        "webapp/package.json": '{"name": "webapp", "version": "1.0.0"}\n',
        "webapp/package-lock.json": '{"name": "webapp", "lockfileVersion": 3}\n',
        "webapp/src/x.js": "1\n",
    }

    def nested_repo(self) -> str:
        files = {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": CONF,
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
        }
        files.update(self.NESTED_FILES)
        return self.task_repo("app", files, {"webapp/src/x.js": "2\n"})

    def test_a_change_confined_to_the_nested_root_runs_there(self) -> None:
        clone = self.nested_repo()
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        where = {(c["name"], c["root"]) for c in self.commands_of(res)}
        self.assertIn(("fast-1", "repos/app"), where)
        self.assertIn(("webapp/fast-1", "repos/app/webapp"), where)
        # the nested gate ran in the nested root, and proved it
        with open(os.path.join(clone, "webapp", "nested-root.txt"), encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), os.path.join(clone, "webapp"))
        # the repository root's own gate still ran at the root
        with open(os.path.join(clone, "root.txt"), encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), clone)
        # the full phase never ran
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")))
        self.assertEqual([p["root"] for p in res["details"]["reports"]],
                         ["repos/app", "repos/app/webapp"])

    def test_the_pre_ticket_approach_fails_in_the_same_scenario(self) -> None:
        """The same gate, run at the repository root as the model did in
        #24, exits non-zero there: the root has no package-lock.json."""
        clone = self.nested_repo()
        c = subprocess.run(["bash", "-c", "test -f package-lock.json"], cwd=clone, capture_output=True)
        self.assertNotEqual(c.returncode, 0)


MARKER_23 = "^✔ status and elapsed time are in a separate row below command and description$"
LINE_23 = "✔ status and elapsed time are in a separate row below command and description (6.212729ms)"


class CheckUnitsTest(FastChecksCase):
    """AC6 (polyforge#23's lesson): recording a unit checks its marker
    against its run's output on the spot, with verification's own rules —
    the #23 marker (anchored $ against node --test's elapsed-time suffix)
    is judged red and the artifact names the closest output line."""

    def units_file(self, units: List[Dict[str, Any]]) -> str:
        p = os.path.join(self.capsule, "artifacts", "build", "units.json")
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            json.dump(units, f)
        return p

    def unit(self, id: str, marker: str, argv: List[str], cwd: str = ".") -> Dict[str, Any]:
        return {"id": id, "test_commit": "", "impl_commit": "",
                "run": {"repo": "app", "cwd": cwd, "argv": argv}, "marker": marker}

    def run_units(self, units: List[Dict[str, Any]], extra: Tuple[str, ...] = ()) -> Tuple[int, Dict[str, Any]]:
        if not os.path.isdir(os.path.join(self.capsule, "repos", "app")):
            self.go_repo()
        return self.run_checks("", extra=("--check-units", self.units_file(units), *extra))

    def test_the_23_marker_is_red_with_the_closest_line(self) -> None:
        code, res = self.run_units([
            self.unit("u1", MARKER_23, ["sh", "-c", "echo 'a plain unrelated output line'; echo '%s'" % LINE_23]),
        ])
        self.assertEqual(code, 1, res)
        self.assertEqual(res["verdict"], "fail")
        u = res["details"]["units"][0]
        self.assertEqual(u["id"], "u1")
        self.assertFalse(u["passed"])
        self.assertEqual(u["exit_code"], 0)
        self.assertEqual(u["closest_line"], LINE_23)
        self.assertIn("no output line matches", u["why"])
        self.assertIn(MARKER_23, u["why"])
        self.assertIn("u1", res["summary"])

    def test_a_matching_marker_passes(self) -> None:
        code, res = self.run_units([
            self.unit("u1", "^✔ status and elapsed time are in a separate row below command and description \\(",
                      ["sh", "-c", "echo '%s'" % LINE_23]),
        ])
        self.assertEqual(code, 0, res)
        u = res["details"]["units"][0]
        self.assertTrue(u["passed"])
        self.assertEqual(u["why"], "")
        self.assertNotIn("closest_line", u)

    def test_broken_markers_are_red_without_running(self) -> None:
        for marker, why in (("", "names no marker"), (".*", "matches the empty string"),
                           ("^--- PASS: TestAnswer (", "is not a regexp")):
            with self.subTest(marker=marker or "(empty)"):
                code, res = self.run_units([self.unit("u1", marker, ["sh", "-c", "echo should-not-run"])])
                self.assertEqual(code, 1)
                u = res["details"]["units"][0]
                self.assertFalse(u["passed"])
                self.assertIn(why, u["why"])
                self.assertNotIn("exit_code", u, "a broken marker never runs")

    def test_a_run_that_cannot_run_as_written_is_red(self) -> None:
        for cwd, argv, why in (("no/such/dir", ["true"], "no directory"),
                               (".", [], "no argv")):
            with self.subTest(why=why):
                code, res = self.run_units([self.unit("u1", "^x", argv, cwd=cwd)])
                self.assertEqual(code, 1)
                u = res["details"]["units"][0]
                self.assertFalse(u["passed"])
                self.assertIn(why, u["why"])

    def test_a_unit_naming_a_repo_the_capsule_does_not_carry_is_red(self) -> None:
        unit = {"id": "u1", "test_commit": "", "impl_commit": "",
                "run": {"repo": "nonesuch", "cwd": ".", "argv": ["true"]}, "marker": "^x"}
        code, res = self.run_units([unit])
        self.assertEqual(code, 1, res)
        u = res["details"]["units"][0]
        self.assertFalse(u["passed"])
        self.assertIn("nonesuch", u["why"])

    def test_the_unit_filter_checks_one_unit(self) -> None:
        code, res = self.run_units([
            self.unit("u1", "^PASS23 ", ["sh", "-c", "echo 'PASS23 one'"]),
            self.unit("u2", "^PASS23 ", ["sh", "-c", "echo 'PASS23 two'"]),
        ], extra=("--unit", "u2"))
        self.assertEqual(code, 0, res)
        self.assertEqual([u["id"] for u in res["details"]["units"]], ["u2"])

    def test_an_unusable_units_file_is_not_judged(self) -> None:
        self.go_repo()
        p = os.path.join(self.capsule, "units.json")
        with open(p, "w", encoding="utf-8") as f:
            f.write("not json")
        code, res = self.run_checks("", extra=("--check-units", p))
        self.assertEqual(code, 2)


class FullPhaseNeverRunsTest(FastChecksCase):
    """AC2 (review r1 #1): a report that falls back to the full tier — a
    stack config file changed — never has its full-phase commands run:
    only the fast gates execute, and the run stays green."""

    def test_a_root_report_falling_back_to_full_runs_fast_only(self) -> None:
        clone = self.task_repo("app", {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf":
                "stack=go\nfast_gate=echo fast-one\nfull_command=echo full-ran > full.txt\n",
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
        }, {"go.mod": GO_MOD + "\n// touched in the change\n"})
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        self.assertEqual(res["details"]["reports"][0]["mode"], "full")
        cmds = self.commands_of(res)
        self.assertTrue(cmds)
        for c in cmds:
            self.assertEqual(c["phase"], "fast", c)
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")), "the full phase ran")

    def test_a_nested_report_falling_back_to_full_runs_fast_only(self) -> None:
        files = {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": CONF,
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
            "webapp/.polyforge/verify-scope.conf":
                "stack=npm\nfast_gate=test -f package-lock.json && pwd > nested-root.txt\n"
                "full_command=echo nested-full > nested-full.txt\n",
            "webapp/package.json": '{"name": "webapp", "version": "1.0.0"}\n',
            "webapp/package-lock.json": '{"name": "webapp", "lockfileVersion": 3}\n',
        }
        clone = self.task_repo("app", files, {"webapp/package.json": '{"name": "webapp", "version": "1.0.1"}\n'})
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        nested = [c for c in self.commands_of(res) if c["root"] == "repos/app/webapp"]
        self.assertTrue(nested, "the nested root is planned")
        for c in nested:
            self.assertEqual(c["phase"], "fast", c)
        with open(os.path.join(clone, "webapp", "nested-root.txt"), encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), os.path.join(clone, "webapp"))
        self.assertFalse(os.path.exists(os.path.join(clone, "webapp", "nested-full.txt")), "the nested full phase ran")
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")), "the root full phase ran")


class DefaultTierTest(FastChecksCase):
    """AC5 (review r1 #2): the engine's default report — the one the model
    ran by hand before, no --light — so a root-level change on a
    forced-full path runs its fast gates and nothing more, exactly as
    before; and the #24 shape still works: a change confined to a nested
    root runs that root's gates in the subdirectory even though the root
    report answered full."""

    def test_a_forced_full_root_change_runs_its_fast_gates_and_nothing_more(self) -> None:
        clone = self.task_repo("app", {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf":
                "stack=go\nforced_full=app\nforced_full=app/*\nfast_gate=pwd > root.txt\n"
                "full_command=echo full-ran > full.txt\n",
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
        }, APP_CHANGE)
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        self.assertEqual(res["details"]["reports"][0]["mode"], "full")
        cmds = self.commands_of(res)
        self.assertTrue(cmds)
        for c in cmds:
            self.assertEqual(c["phase"], "fast", c)
        self.assertTrue(os.path.exists(os.path.join(clone, "root.txt")))
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")), "the full phase ran")

    def test_a_nested_root_change_still_runs_there_when_the_root_report_is_full(self) -> None:
        files = {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf":
                "stack=go\nforced_full=webapp\nforced_full=webapp/*\nfast_gate=pwd > root.txt\n"
                "full_command=echo full-ran > full.txt\n",
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
            "webapp/.polyforge/verify-scope.conf":
                "stack=npm\nfast_gate=test -f package-lock.json && pwd > nested-root.txt\n",
            "webapp/package.json": '{"name": "webapp", "version": "1.0.0"}\n',
            "webapp/package-lock.json": '{"name": "webapp", "lockfileVersion": 3}\n',
            "webapp/src/x.js": "1\n",
        }
        clone = self.task_repo("app", files, {"webapp/src/x.js": "2\n"})
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        self.assertEqual(res["details"]["reports"][0]["mode"], "full",
                         "the root conf forces the full tier for every webapp/ path")
        where = {(c["name"], c["root"]) for c in self.commands_of(res)}
        self.assertIn(("webapp/fast-1", "repos/app/webapp"), where)
        with open(os.path.join(clone, "webapp", "nested-root.txt"), encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), os.path.join(clone, "webapp"))
        self.assertFalse(os.path.exists(os.path.join(clone, "full.txt")), "the full phase ran")


class UsageTest(FastChecksCase):
    def test_no_repo_is_a_usage_error(self) -> None:
        argv = [sys.executable, FAST_CHECKS, "--capsule", self.capsule, "--engine", ENGINE, "--out", self.out]
        c = subprocess.run(argv, capture_output=True, text=True)
        self.assertEqual(c.returncode, 2)

    def test_an_unknown_repo_is_not_judged(self) -> None:
        self.go_repo()
        code, res = self.run_checks("nonesuch")
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
