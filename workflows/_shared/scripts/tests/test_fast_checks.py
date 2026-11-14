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
import unittest

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


def env_without(name: str) -> dict:
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

    def task_repo(self, name: str, files: dict, change: dict) -> str:
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

    def run_checks(self, repo: str, env=None, extra=()) -> tuple:
        """fast_checks.py on the capsule's repo, as a skill runs it."""
        argv = [sys.executable, FAST_CHECKS, "--repo", repo, "--capsule", self.capsule,
                "--engine", ENGINE, "--deadline", "10m", "--out", self.out, *extra]
        c = subprocess.run(argv, capture_output=True, text=True, env=env if env is not None else os.environ)
        if not os.path.exists(self.out):
            return c.returncode, None
        with open(self.out, encoding="utf-8") as f:
            return c.returncode, json.load(f)

    def go_repo(self, conf: str = CONF, change: dict = None) -> str:
        return self.task_repo("app", {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": conf,
            "app/app.go": APP,
            "app/app_test.go": APP_TEST,
        }, change if change is not None else APP_CHANGE)

    def commands_of(self, res: dict) -> list:
        return res["details"]["commands"]


class RootReportTest(FastChecksCase):
    """AC2/AC4/AC5: a change at the repository's own root — the report's
    root is the clone, the fast gates and the scoped selection run there in
    order, the full tail never runs, and the result file records it all."""

    def test_green(self) -> None:
        clone = self.go_repo()
        code, res = self.run_checks("app")
        self.assertEqual(code, 0, res)
        self.assertEqual(schema_errors(res, SCHEMA), [])
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
