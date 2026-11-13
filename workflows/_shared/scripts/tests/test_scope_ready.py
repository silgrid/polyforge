"""scope_ready.py: the scoped-gates readiness check that replaced `pf
doctor`'s row — its exit codes (an engine missing or a conf the engine
cannot read is not ready, exit 1; no clone, exit 2), and its reading of the
carries layer against the engine's own on the same git fixture (the
doctor row's parity test,, moved with it: one fact in two
places, so drift reddens the suite)."""

import contextlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

import scope_ready

from .gitworld import GitCase, Repo, write

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")

NPM_DECLARED = "// carries: src/*\nconsole.log(1)\n"
PY_DECLARED = "# carries: src/*\ndef test_a():\n    pass\n"
RS_DECLARED = "// carries: src/*\n#[test]\nfn a() {}\n"
PKG = '{"name":"w248","scripts":{"test:e2e:all":"node e2e/all.mjs"}}\n'
CONF = ".polyforge/verify-scope.conf"

_SUITE_DIAG = re.compile(
    r"^(?:verify-scope: )?carries: (?:suite (\S+) (declares no carries: header|has a malformed carries: declaration)"
    r"|e2e_map names (\S+), which is not a discovered suite)", re.ASCII)


def run_main(args: List[str]) -> Tuple[int, Dict[str, Any]]:
    out = io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        code = scope_ready.main(args + ["--json"])
    text = out.getvalue().strip()
    return code, (json.loads(text) if text else {})


class ExitCodesTest(GitCase):
    def repo(self, files: Dict[str, str]) -> str:
        return Repo(os.path.join(self.tmp, "r%d" % len(os.listdir(self.tmp))), files).dir

    def engine_copy(self, with_helper: bool = True) -> str:
        d = self.new_dir("engine%d" % len(os.listdir(self.tmp)))
        shutil.copy(ENGINE, os.path.join(d, "verify-scope"))
        if with_helper:
            shutil.copy(os.path.join(SCRIPTS, "audit-deps"), os.path.join(d, "audit-deps"))
        return os.path.join(d, "verify-scope")

    def test_ready(self) -> None:
        code, res = run_main(["--repo", self.repo({"go.mod": "module m\n", "a.go": "package a\n"})])
        self.assertEqual(code, 0, res)
        conf_only = self.repo({CONF: "stack=none\nfull_command=make test\n"})
        code, res = run_main(["--repo", conf_only])
        self.assertEqual(code, 0, res)
        self.assertTrue(res["details"]["repos"][0]["conf_only"])

    def test_the_engine_missing_is_not_ready(self) -> None:
        r = self.repo({"go.mod": "module m\n"})
        code, res = run_main(["--repo", r, "--engine", os.path.join(self.tmp, "none", "verify-scope")])
        self.assertEqual(code, 1)
        self.assertIn("lacks the engine", res["summary"])
        # the engine without its audit helper beside it is no engine either
        code, res = run_main(["--repo", r, "--engine", self.engine_copy(with_helper=False)])
        self.assertEqual(code, 1, res)
        code, _ = run_main(["--repo", r, "--engine", self.engine_copy()])
        self.assertEqual(code, 0)

    def test_a_conf_the_engine_cannot_read_is_not_ready(self) -> None:
        for files, why in (
            ({"package.json": PKG, CONF: "suite_glob=\n"}, "empty or malformed carries key"),
            ({"package.json": PKG, CONF: "e2e_map=src/*\n"}, "empty or malformed carries key"),
            ({"go.mod": "module m\n", CONF: "stack=go\n\xff\n"}, "cannot be read"),
            ({CONF: "stack=none\n"}, "pins stack=none but declares no command"),
            ({"package.json": "{}\n", "go.mod": "module m\n"}, "unpinned multi-manifest"),
            ({"README": "x\n"}, "no supported stack manifest"),
        ):
            with self.subTest(why):
                r = Repo(os.path.join(self.tmp, "c%d" % len(os.listdir(self.tmp))), {"README.md": "r\n"})
                p = os.path.join(r.dir, CONF)
                for rel, body in files.items():
                    path = os.path.join(r.dir, rel)
                    os.makedirs(os.path.dirname(path), exist_ok=True)
                    with open(path, "wb") as f:
                        f.write(body.encode("latin-1"))
                del p
                code, res = run_main(["--repo", r.dir])
                self.assertEqual(code, 1, res)
                self.assertIn(why, res["summary"])

    def test_unusable_invocations(self) -> None:
        env = dict(os.environ)
        env.pop("PF_CAPSULE", None)
        c = subprocess.run(["python3", os.path.join(SCRIPTS, "scope_ready.py")], env=env, capture_output=True, text=True)
        self.assertEqual(c.returncode, 2, c.stderr)
        self.assertIn("--repo", c.stderr)
        not_a_clone = self.new_dir("plain")
        code, _ = run_main(["--repo", not_a_clone])
        self.assertEqual(code, 2)
        c = subprocess.run(["python3", os.path.join(SCRIPTS, "scope_ready.py"), "--help"], capture_output=True, text=True)
        self.assertEqual(c.returncode, 0)
        self.assertIn("scoped-ready", c.stdout)


@unittest.skipUnless(sys.platform.startswith("linux") and shutil.which("python3") and shutil.which("sh"),
                     "the engine parity runs where the Go parity test ran: Linux, with sh and python3")
class EngineParityTest(GitCase):
    """For every carries-layer shape, the engine's mode and the suites it
    names undeclared or malformed equal scope_ready's reading."""

    CASES = [
        ("npm declared", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", "e2e/a.mjs": NPM_DECLARED}),
        ("npm undeclared", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", "e2e/a.mjs": NPM_DECLARED, "e2e/b.mjs": "console.log(2)\n"}),
        ("npm malformed carries", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", "e2e/a.mjs": NPM_DECLARED, "e2e/m.mjs": "// carries:\nconsole.log(3)\n"}),
        ("npm imported helper", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n",
                                                   "e2e/a.mjs": '// carries: src/*\nimport { h } from "./helper.mjs";\nh();\n',
                                                   "e2e/helper.mjs": "export const h = () => 1;\n"}),
        ("suite_glob discovery", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "suite_glob=spec/*.spec.mjs\n",
                                                    "spec/a.spec.mjs": NPM_DECLARED, "spec/b.spec.mjs": "console.log(2)\n", "e2e/ignored.mjs": "console.log(9)\n"}),
        ("exclude", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "exclude=e2e/skip-*.mjs\n",
                                       "e2e/a.mjs": NPM_DECLARED, "e2e/skip-me.mjs": "console.log(2)\n"}),
        ("e2e_runner target", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "e2e_runner=npm run test:e2e:all\n",
                                                 "e2e/a.mjs": NPM_DECLARED, "e2e/all.mjs": "console.log('runner')\n"}),
        ("e2e_map present suite", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "e2e_map=src/*=e2e/b.mjs\n",
                                                     "e2e/a.mjs": NPM_DECLARED, "e2e/b.mjs": "console.log(2)\n"}),
        ("e2e_map absent suite", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "e2e_map=src/*=e2e/gone.mjs\n", "e2e/a.mjs": NPM_DECLARED}),
        ("python declared", "python", "e2e/test_a.py", {"pyproject.toml": '[project]\nname = "w248"\n', "src/a.py": "A = 1\n", "e2e/test_a.py": PY_DECLARED}),
        ("python undeclared", "python", "e2e/test_a.py", {"pyproject.toml": '[project]\nname = "w248"\n', "src/a.py": "A = 1\n", "e2e/test_a.py": PY_DECLARED,
                                                          "tests/e2e/test_b.py": "def test_b():\n    pass\n"}),
        ("rust declared", "rust", "tests/a.rs", {"Cargo.toml": '[package]\nname = "w248"\nversion = "0.1.0"\n', "src/lib.rs": "pub fn a() {}\n", "tests/a.rs": RS_DECLARED}),
        ("npm empty smoke line", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "smoke_suite=\n", "e2e/a.mjs": NPM_DECLARED}),
        ("npm malformed smoke line", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "smoke_suite=e2e/a.mjs e2e/b.mjs\n", "e2e/a.mjs": NPM_DECLARED}),
        ("npm valid smoke line", "npm", "src/a.js", {"package.json": PKG, "src/a.js": "1\n", CONF: "smoke_suite=e2e/a.mjs\n", "e2e/a.mjs": NPM_DECLARED}),
        ("rust undeclared", "rust", "tests/a.rs", {"Cargo.toml": '[package]\nname = "w248"\nversion = "0.1.0"\n', "src/lib.rs": "pub fn a() {}\n",
                                                   "tests/a.rs": RS_DECLARED, "tests/b.rs": "#[test]\nfn b() {}\n"}),
    ]

    def engine(self, d: str, *args: str) -> Dict[str, Any]:
        c = subprocess.run(["sh", ENGINE, *args, "--json"], cwd=d, capture_output=True, text=True)
        self.assertEqual(c.returncode, 0, c.stderr)
        doc = json.loads(c.stdout)
        self.assertIsInstance(doc, dict)
        return dict(doc)

    def fixture(self, files: Dict[str, str], change: str) -> Tuple[str, str]:
        r = Repo(os.path.join(self.tmp, "p%d" % len(os.listdir(self.tmp))), files)
        base = r.head()
        write(r.dir, {change: files[change] + "\n"})
        r.git("commit", "-q", "-am", "change " + change)
        return r.dir, base

    def test_carries_parity(self) -> None:
        for name, analyzer, change, files in self.CASES:
            with self.subTest(name):
                if analyzer == "npm" and shutil.which("node") is None:
                    self.skipTest("the engine's npm analyzer reads package.json with node, which is not installed")
                d, base = self.fixture(files, change)
                rep = self.engine(d, "--base", base)
                self.assertEqual(rep.get("analyzer"), analyzer)
                engine_set = []
                for diag in rep.get("diagnostics") or []:
                    m = _SUITE_DIAG.search(str(diag))
                    if m:
                        s = m.group(1)
                        if m.group(3):
                            s = m.group(3) + " (e2e_map names no discovered suite)"
                        elif (m.group(2) or "").startswith("has a malformed"):
                            s += " (malformed carries:)"
                        engine_set.append(s)
                conf_path = os.path.join(d, CONF)
                text = ""
                if os.path.exists(conf_path):
                    with open(conf_path, encoding="utf-8") as fh:
                        text = fh.read()
                facts = scope_ready.read_conf(text)
                row_set: List[str] = []
                row_full = bool(facts.rejected)
                if not row_full:
                    _, undeclared = scope_ready.carries(d, analyzer, facts)
                    row_set = list(undeclared)
                    row_full = bool(undeclared)
                self.assertEqual(rep.get("mode") == "full", row_full, (rep.get("diagnostics"), row_set))
                self.assertEqual(sorted(engine_set), sorted(row_set), rep.get("diagnostics"))

    def test_smoke_parity(self) -> None:
        if shutil.which("node") is None:
            self.skipTest("the engine's npm analyzer reads package.json with node, which is not installed")
        head = "full_command=npm run test:e2e:all\n"
        missing_diag = re.compile(r"smoke_suite (\S+) is not a discovered suite", re.ASCII)
        for name, conf in (("valid list", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=e2e/b.mjs\nsmoke_suite=e2e/a.mjs\n"),
                           ("empty line", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=\n"),
                           ("not a discovered suite", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=e2e/gone.mjs\n")):
            with self.subTest(name):
                files = {"package.json": PKG, "src/a.js": "1\n", CONF: conf,
                         "e2e/a.mjs": "// carries: src/*\nconsole.log(1)\n", "e2e/b.mjs": "// carries: lib/*\nconsole.log(2)\n"}
                d, base = self.fixture(files, "src/a.js")
                rep = self.engine(d, "--base", base, "--full")
                rejected = any("conf rejects smoke_suite value" in str(x) for x in rep.get("diagnostics") or [])
                missing = [m.group(1) for x in rep.get("diagnostics") or [] for m in [missing_diag.search(str(x))] if m]
                facts = scope_ready.read_conf(conf)
                self.assertEqual(facts.smoke, rep.get("smoke_suite") or [])
                self.assertEqual(bool(facts.rejected), rejected, rep.get("diagnostics"))
                if not rejected:
                    suites, _ = scope_ready.carries(d, "npm", facts)
                    self.assertEqual([s for s in facts.smoke if s not in suites], missing)


if __name__ == "__main__":
    unittest.main()
