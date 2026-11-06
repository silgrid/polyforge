"""facts.py build --replay --isolation host on a minimal repository whose
test commit cannot run as checked out — a Go package embedding a build
output that is not committed, a Node test importing a package that is not
installed: the repository's `base_setup=` lines (.polyforge/verify-scope.conf
at the test commit) run in the replay worktree, and the run, replayed
again, is judged on its assertions (red). Without `base_setup=`, or when it
fails, the unit is not runnable: no red evidence, exit 1. The setup
commands here are offline stand-ins (they write the build output or the
installed package themselves)."""

import json
import os
import shutil
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
FACTS = os.path.join(os.path.dirname(HERE), "facts.py")

HAVE_GO = shutil.which("go") is not None
HAVE_NODE = shutil.which("node") is not None

GO_RULES = ("setup-failed", "build-failed", "embed-pattern", "compile-error", "missing-package")

GO_BASE = {
    "go.mod": "module example.com/app\n\ngo 1.21\n",
    ".gitignore": "web/dist/\n",
    "web/embed.go": 'package web\n\nimport "embed"\n\n//go:embed all:dist\nvar Dist embed.FS\n',
    "server/server.go": 'package server\n\nimport "example.com/app/web"\n\nfunc Value() int {\n\t_ = web.Dist\n\treturn 1\n}\n',
    "tools/setup.sh": "mkdir -p web/dist && echo '<html></html>' > web/dist/index.html\n",
}
GO_TEST = {
    "server/server_test.go": 'package server\n\nimport "testing"\n\nfunc TestValue(t *testing.T) {\n\tif got := Value(); got != 2 {\n\t\tt.Errorf("got %d, want 2", got)\n\t}\n}\n',
}
GO_IMPL = {
    "server/server.go": 'package server\n\nimport "example.com/app/web"\n\nfunc Value() int {\n\t_ = web.Dist\n\treturn 2\n}\n',
}

NODE_BASE = {
    ".gitignore": "node_modules/\n",
    "web2/lib.mjs": "export const double = (x) => x;\n",
    "web2/setup.sh": (
        "mkdir -p node_modules/dep\n"
        "printf '{\"name\": \"dep\", \"type\": \"module\", \"exports\": \"./index.js\"}\\n' > node_modules/dep/package.json\n"
        "echo 'export const v = 1;' > node_modules/dep/index.js\n"
    ),
}
NODE_TEST = {
    "web2/t.test.mjs": (
        "import { test } from 'node:test';\n"
        "import assert from 'node:assert';\n"
        "import { v } from 'dep';\n"
        "import { double } from './lib.mjs';\n\n"
        "test('double of v is 2', () => {\n  assert.strictEqual(double(v), 2);\n});\n"
    ),
}
NODE_IMPL = {"web2/lib.mjs": "export const double = (x) => x * 2;\n"}


class ReplaySetupTest(GitCase):
    def capsule(self, conf: str, base: Dict[str, str], test: Dict[str, str], impl: Dict[str, str],
                cwd: str, argv: List[str]) -> str:
        """A capsule whose repos/app holds base (with conf as its
        .polyforge/verify-scope.conf, origin/main there), then one unit:
        its test commit and its implementation."""
        cap = self.new_dir("capsule")
        files = dict(base)
        if conf:
            files[".polyforge/verify-scope.conf"] = conf
        repo = Repo(os.path.join(cap, "repos", "app"), files)
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        tc = repo.commit("test", test)
        ic = repo.commit("impl", impl)
        units = [{"id": "u1", "test_commit": tc, "impl_commit": ic,
                  "run": {"repo": "app", "cwd": cwd, "argv": argv}, "marker": ""}]
        write(cap, {
            "capsule.json": json.dumps({"work_item_id": "wi_x", "bundle_name": "develop", "repos": {"app": "app"}}),
            "artifacts/build/units.json": json.dumps(units),
        })
        return cap

    def replay(self, cap: str) -> Tuple[int, Dict[str, Any], str]:
        out = os.path.join(cap, "artifacts", "facts", "build.json")
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        env["GOWORK"] = "off"
        env["GOTOOLCHAIN"] = "local"
        env.pop("GOFLAGS", None)
        p = subprocess.run([sys.executable, "-B", FACTS, "build", "--capsule", cap, "--replay", "--isolation", "host",
                            "--unit-timeout", "3m", "--deadline", "5m", "--json", "--out", out],
                           env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
        return p.returncode, res, p.stderr.decode("utf-8", "replace")

    def unit_replay(self, res: Dict[str, Any]) -> Dict[str, Any]:
        r = res["details"]["units"][0].get("replay")
        self.assertIsNotNone(r, res)
        assert r is not None
        return r  # type: ignore[no-any-return]

    def log_text(self, cap: str, r: Dict[str, Any]) -> str:
        p = r["log"] if os.path.isabs(r["log"]) else os.path.join(cap, r["log"])
        with open(p, encoding="utf-8", errors="replace") as f:
            return f.read()

    @unittest.skipUnless(HAVE_GO, "go not installed")
    def test_go_embed_unit_runs_after_setup_and_is_red(self) -> None:
        cap = self.capsule("stack=go\nbase_setup=sh tools/setup.sh\n", GO_BASE, GO_TEST, GO_IMPL, ".", ["go", "test", "./server/"])
        code, res, err = self.replay(cap)
        r = self.unit_replay(res)
        self.assertEqual(code, 0, (res["summary"], err))
        self.assertIs(r["red"], True, r)
        self.assertNotIn("not_runnable", r)
        setup: Optional[Dict[str, Any]] = r.get("setup")
        assert setup is not None, r
        self.assertEqual(setup["because"]["toolchain"], "go")
        self.assertIn(setup["because"]["rule"], GO_RULES)
        self.assertEqual(setup["commands"][0]["command"], "sh tools/setup.sh")
        self.assertEqual(setup["commands"][0]["exit_code"], 0)
        self.assertIn("--- FAIL: TestValue", self.log_text(cap, r))

    @unittest.skipUnless(HAVE_NODE, "node not installed")
    def test_node_unit_runs_after_setup_and_is_red(self) -> None:
        cap = self.capsule("stack=npm\nbase_setup=cd web2 && sh setup.sh\n", NODE_BASE, NODE_TEST, NODE_IMPL,
                           "web2", ["node", "--test", "t.test.mjs"])
        code, res, err = self.replay(cap)
        r = self.unit_replay(res)
        self.assertEqual(code, 0, (res["summary"], err))
        self.assertIs(r["red"], True, r)
        self.assertNotIn("not_runnable", r)
        self.assertEqual(r["setup"]["because"]["toolchain"], "node")
        self.assertIn("AssertionError", self.log_text(cap, r))

    @unittest.skipUnless(HAVE_GO, "go not installed")
    def test_without_setup_it_is_not_runnable(self) -> None:
        cap = self.capsule("stack=go\n", GO_BASE, GO_TEST, GO_IMPL, ".", ["go", "test", "./server/"])
        code, res, err = self.replay(cap)
        r = self.unit_replay(res)
        self.assertEqual((code, res["verdict"]), (1, "fail"), (res["summary"], err))
        self.assertIs(r["red"], False, r)
        self.assertEqual(r["not_runnable"]["toolchain"], "go")
        self.assertIn(r["not_runnable"]["rule"], GO_RULES)
        self.assertNotIn("setup", r)
        self.assertIn("not runnable", res["summary"])

    @unittest.skipUnless(HAVE_GO, "go not installed")
    def test_failed_setup_is_not_runnable(self) -> None:
        cap = self.capsule("stack=go\nbase_setup=false\n", GO_BASE, GO_TEST, GO_IMPL, ".", ["go", "test", "./server/"])
        code, res, err = self.replay(cap)
        r = self.unit_replay(res)
        self.assertEqual((code, res["verdict"]), (1, "fail"), (res["summary"], err))
        self.assertIs(r["red"], False, r)
        self.assertEqual(r["not_runnable"]["rule"], "base-setup")
        self.assertEqual(r["setup"]["commands"][0]["exit_code"], 1)
        self.assertIn("not runnable", res["summary"])


if __name__ == "__main__":
    unittest.main()
