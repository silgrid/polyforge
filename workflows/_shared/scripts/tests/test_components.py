"""components: the epic-upgrade rule's conf keys (polyforge#89, AC1) —
the ``component=`` declaration and the ``epic_components`` /
``epic_units`` / ``epic_files`` thresholds parsed by pfstep.components
with the ticket's defaults (3 / 8 / 30), the diagnosed-and-ignored lines,
and the scope engine's acceptance of the keys: the engine reads none of
them (the components.py tool owns them — the not_runnable_rule
precedent), so it answers the same report for a conf that declares them
and one that does not, keeps the gate id unchanged (the keys are no gate
keys), and never diagnoses them as unknown.

The tool itself (AC2): workflows/_shared/scripts/components.py — one or
more clones, the origin/HEAD (else HEAD) commit tree read (never the
work tree), the generic division (declared ranges, nested conf roots,
manifest subdirectories — each nest outermost —, first-level
directories, the root files), the testdata/node_modules/vendor segments
excluded, the line report and the --json schema 1."""

import json
import os
import subprocess
import unittest
from typing import Any, Dict, Tuple

from pfstep import components, gateid

from .gitworld import GitCase, Repo, git

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")
TOOL = os.path.join(SCRIPTS, "components.py")
CONF = ".polyforge/verify-scope.conf"
DOCKERFILE = ".polyforge/Dockerfile.verify"

BASE_CONF = "stack=none\nfast_gate=true\n"
GOOD_COMPONENTS = "component=core|src,lib\ncomponent=web|web\n"
GOOD_THRESHOLDS = "epic_components=4\nepic_units=9\nepic_files=31\n"
NEW_KEYS = GOOD_COMPONENTS + GOOD_THRESHOLDS


class ConfKeysTest(unittest.TestCase):
    """pfstep.components.read_conf: the declarations, the defaults, and
    the diagnosed-and-ignored lines. The expected values come from the
    ticket: the thresholds 3 / 8 / 30, prefix-matched paths, the first
    declaration of a repeated one winning."""

    def test_valid_declarations_parse(self) -> None:
        r = components.read_conf(BASE_CONF + NEW_KEYS)
        self.assertEqual([c.name for c in r.components], ["core", "web"])
        self.assertEqual(r.components[0].paths, ["src", "lib"])
        self.assertEqual(r.components[1].paths, ["web"])
        self.assertEqual([c.source for c in r.components], ["declared", "declared"])
        self.assertEqual(r.diagnostics, [])
        t = r.thresholds
        self.assertEqual((t.epic_components, t.epic_units, t.epic_files), (4, 9, 31))
        self.assertEqual(t.source, {"epic_components": "declared",
                                    "epic_units": "declared",
                                    "epic_files": "declared"})

    def test_defaults_without_declarations(self) -> None:
        r = components.read_conf(BASE_CONF)
        self.assertEqual(r.components, [])
        t = r.thresholds
        self.assertEqual((t.epic_components, t.epic_units, t.epic_files), (3, 8, 30))
        self.assertEqual(t.source, {"epic_components": "default",
                                    "epic_units": "default",
                                    "epic_files": "default"})
        self.assertEqual(r.diagnostics, [])

    def test_bad_component_lines_diagnosed_and_ignored(self) -> None:
        for bad in (
            "component=no-pipe\n",
            "component=|src\n",
            "component=bad name|src\n",
            "component=core|\n",
            "component=core|src,,lib\n",
            "component=core|/absolute\n",
            "component=core|../escape\n",
        ):
            with self.subTest(bad):
                r = components.read_conf(BASE_CONF + bad)
                self.assertEqual(r.components, [], bad)
                self.assertTrue(any("component" in d for d in r.diagnostics), bad)

    def test_a_second_component_name_is_ignored(self) -> None:
        r = components.read_conf(GOOD_COMPONENTS + "component=core|other\n")
        self.assertEqual([c.name for c in r.components], ["core", "web"])
        self.assertEqual(r.components[0].paths, ["src", "lib"])
        self.assertTrue(any("core" in d for d in r.diagnostics))

    def test_bad_threshold_values_fall_back_to_the_default(self) -> None:
        for bad in ("epic_components=0\n", "epic_components=-1\n",
                    "epic_components=x\n", "epic_components=\n",
                    "epic_components=3.5\n"):
            with self.subTest(bad):
                r = components.read_conf(BASE_CONF + bad)
                self.assertEqual(r.thresholds.epic_components, 3, bad)
                self.assertTrue(any("epic_components" in d for d in r.diagnostics), bad)

    def test_a_trailing_slash_is_normalized_and_a_dot_range_is_rejected(self) -> None:
        """A declared path range is a prefix: a trailing slash is the
        same prefix (normalized, no diagnostic) and a bare dot names no
        path ever (diagnosed and ignored — review r1#5). The expected
        values come from the range's meaning, not the parser's output."""
        r = components.read_conf("component=core|src/,lib\n")
        self.assertEqual([c.paths for c in r.components], [["src", "lib"]])
        self.assertEqual(r.diagnostics, [])
        r = components.read_conf(BASE_CONF + "component=core|.\n")
        self.assertEqual(r.components, [])
        self.assertTrue(any("component" in d for d in r.diagnostics))

    def test_a_repeated_threshold_keeps_its_first_declaration(self) -> None:
        r = components.read_conf("epic_units=9\nepic_units=10\n")
        self.assertEqual(r.thresholds.epic_units, 9)
        self.assertTrue(any("epic_units" in d for d in r.diagnostics))


class EngineAcceptsTest(GitCase):
    """The engine owns none of the new keys: a conf that declares them
    answers the same report as one that does not (the report, the other
    keys and the diagnostics unchanged), and no unknown-key diagnostic
    names them — the not_runnable_rule precedent, no conf reader
    diagnosing a key another tool owns."""

    def run_engine(self, d: str) -> Tuple[Dict[str, Any], str]:
        c = subprocess.run(["python3", ENGINE, "--json", "--base", "HEAD"], cwd=d,
                           capture_output=True, text=True, stdin=subprocess.DEVNULL)
        self.assertEqual(c.returncode, 0, c.stderr)
        return json.loads(c.stdout), c.stderr

    def test_the_engine_accepts_the_component_keys(self) -> None:
        files = {CONF: BASE_CONF + NEW_KEYS, DOCKERFILE: "FROM demo\n",
                 "src/a.go": "package a\n", "web/b.txt": "b\n", "lib/c.md": "c\n"}
        d_with = os.path.join(self.tmp, "with")
        Repo(d_with, files)
        with_doc, with_err = self.run_engine(d_with)
        d_without = os.path.join(self.tmp, "without")
        Repo(d_without, dict(files, **{CONF: BASE_CONF}))
        without_doc, without_err = self.run_engine(d_without)
        # no diagnostic names the new keys, on stderr or in the report
        for text in (with_err, "\n".join(with_doc["diagnostics"])):
            for line in text.splitlines():
                self.assertNotRegex(line, "component|epic_")
        self.assertEqual(with_err, without_err)
        # the report is unchanged: the same commands, mode and key set
        for doc in (with_doc, without_doc):
            for k in ("root", "base", "head"):
                doc.pop(k, None)
        self.assertEqual(with_doc, without_doc)
        self.assertEqual([c["cmd"] for c in with_doc["commands"]], ["true"])


class GateIdUnchangedTest(GitCase):
    """The new keys are no gate keys: the id of a commit whose conf
    declares them equals the id of the same conf without them, and a
    moved gate key still changes it."""

    def test_the_new_keys_change_no_gate_id(self) -> None:
        d = os.path.join(self.tmp, "r")
        repo = Repo(d, {CONF: BASE_CONF + NEW_KEYS, DOCKERFILE: "FROM demo\n"})
        with_id = gateid.gate_id(d, "HEAD")
        repo.commit("the conf without the new keys",
                    {CONF: BASE_CONF, DOCKERFILE: "FROM demo\n"})
        self.assertEqual(gateid.gate_id(d, "HEAD"), with_id)
        repo.commit("a gate key moves",
                    {CONF: BASE_CONF + "full_command=echo full\n", DOCKERFILE: "FROM demo\n"})
        self.assertNotEqual(gateid.gate_id(d, "HEAD"), with_id)


GENERIC_FILES = {
    "web/.polyforge/verify-scope.conf": "component=ignored|web/src\n",  # only marks the subdir
    "web/src/app.ts": "x\n",
    "tools/py/pyproject.toml": "[project]\n",
    "tools/py/mod.py": "x = 1\n",
    "tools/run.sh": "echo\n",
    "internal/x.go": "package x\n",
    "test/main.go": "package test\n",
    "test/testdata/verify-scope/go-fixture/go.mod": "module fixture\n",  # excluded: testdata
    "node_modules/pkg/package.json": "{}\n",  # excluded: node_modules
    "vendor/lib/a.c": "int a;\n",  # excluded: vendor
    "docs/a.md": "a\n",
    "docs/b.txt": "b\n",
    "go.mod": "module m\n",
    "README.md": "readme\n",
    ".polyforge/verify-scope.conf": "stack=none\nfast_gate=true\n",
}


class ComponentsToolTest(GitCase):
    """The entry script's behaviour on synthetic clones. The expected
    division is derived by hand from the fixture's layout (the ticket's
    generic definition), never from the implementation's output."""

    def run_tool(self, *args: str) -> "subprocess.CompletedProcess[str]":
        return subprocess.run(["python3", TOOL, *args], capture_output=True, text=True,
                              stdin=subprocess.DEVNULL)

    def repo_with(self, name: str, files: Dict[str, str]) -> str:
        d = os.path.join(self.tmp, name)
        Repo(d, files)
        return d

    def test_the_tool_divides_by_the_generic_definition(self) -> None:
        d = self.repo_with("generic", GENERIC_FILES)
        c = self.run_tool("--repo", d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertEqual(c.stderr, "")
        for want in (
            "  component web  [web]  nested conf",
            "  component tools/py  [tools/py]  manifest",
            "  component .polyforge  [.polyforge]  top-level",
            "  component docs  [docs]  top-level, documentation",
            "  component internal  [internal]  top-level",
            "  component test  [test]  top-level",
            "  component tools  [tools]  top-level",
            "  component .  [README.md, go.mod]  root files",
            "  thresholds epic_components 3 (default), epic_units 8 (default),"
            " epic_files 30 (default)",
        ):
            self.assertIn(want, c.stdout, want)
        # the excluded segments leave no component behind
        self.assertNotIn("testdata", c.stdout)
        self.assertNotIn("node_modules", c.stdout)
        self.assertNotIn("vendor", c.stdout)
        # the root report names the commit it read
        self.assertRegex(c.stdout.splitlines()[0], r"^repo generic [0-9a-f]{40} \(HEAD\)$")

    def test_nests_answer_at_their_outermost(self) -> None:
        """AC2's「嵌套时取最外层」: an independently-checkable
        subdirectory — a nested conf root or a manifest subdirectory —
        answers at its outermost directory whatever kind marks it (a
        directory both kinds mark answers as the conf's). The
        expectations are hand-derived from the definition (review
        r1#1/#2), never from the implementation's output."""
        # ① a manifest inside a manifest directory: only the outer one
        d = self.repo_with("mm", {"svc/go.mod": "module svc\n", "svc/y.js": "y\n",
                                  "svc/sub/package.json": "{}\n", "svc/sub/z.ts": "z\n"})
        c = self.run_tool("--repo", d, "--json")
        self.assertEqual(c.returncode, 0, c.stderr)
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual([(x["name"], x["source"]) for x in entry["components"]],
                         [("svc", "manifest")], c.stdout)
        # ② a manifest inside a nested conf root: one component, the conf's
        d = self.repo_with("cm", {"web/.polyforge/verify-scope.conf": "stack=none\n",
                                  "web/package.json": "{}\n", "web/src/a.ts": "a\n"})
        c = self.run_tool("--repo", d, "--json")
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual([(x["name"], x["source"]) for x in entry["components"]],
                         [("web", "nested conf")], c.stdout)
        # ③ a nested conf inside a manifest directory (r1#1's shape)
        d = self.repo_with("mc", {"svc/package.json": "{}\n", "svc/y.js": "y\n",
                                  "svc/api/.polyforge/verify-scope.conf": "stack=none\n",
                                  "svc/api/x.go": "package x\n"})
        c = self.run_tool("--repo", d, "--json")
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual([(x["name"], x["source"]) for x in entry["components"]],
                         [("svc", "manifest")], c.stdout)

    def test_the_tree_read_is_origin_head_not_the_worktree(self) -> None:
        origin = self.repo_with("origin", {"a.go": "a\n",
                                            CONF: "component=old|old\n"})
        d = os.path.join(self.tmp, "clone")
        git(self.tmp, "clone", "-q", origin, d)
        old_commit = git(d, "rev-parse", "origin/HEAD")
        # a newer commit on the clone's own branch, invisible to origin/HEAD
        with open(os.path.join(d, CONF), "w", encoding="utf-8") as f:
            f.write("component=new|new\n")
        git(d, "add", "-A")
        git(d, "commit", "-q", "-m", "new declaration")
        c = self.run_tool("--repo", d, "--json")
        self.assertEqual(c.returncode, 0, c.stderr)
        doc = json.loads(c.stdout)
        self.assertEqual(doc["schema"], 1)
        entry = doc["repos"][0]
        self.assertEqual(entry["commit"], old_commit)
        self.assertEqual(entry["rev"], "origin/HEAD")
        self.assertEqual([cmp["name"] for cmp in entry["components"]], ["old", ".polyforge", "."])
        # --rev overrides the revision read
        c = self.run_tool("--repo", d, "--rev", "HEAD", "--json")
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual(entry["rev"], "HEAD")
        self.assertNotEqual(entry["commit"], old_commit)
        self.assertEqual([cmp["name"] for cmp in entry["components"]], ["new", ".polyforge", "."])

    def test_the_json_schema_and_the_threshold_sources(self) -> None:
        d = self.repo_with("declared", {CONF: "component=core|src,lib\nepic_units=9\n",
                                        "src/a.go": "package a\n", "lib/b.go": "package b\n",
                                        "docs/x.md": "x\n"})
        c = self.run_tool("--repo", d, "--json")
        self.assertEqual(c.returncode, 0, c.stderr)
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual(entry["repo"], "declared")
        self.assertEqual(set(entry), {"repo", "commit", "rev", "components", "thresholds"})
        self.assertEqual(entry["components"], [
            {"name": "core", "paths": ["src", "lib"], "source": "declared",
             "documentation_only": False},
            {"name": ".polyforge", "paths": [".polyforge"], "source": "top-level",
             "documentation_only": False},
            {"name": "docs", "paths": ["docs"], "source": "top-level",
             "documentation_only": True},
        ])
        self.assertEqual(entry["thresholds"], {
            "epic_components": 3, "epic_units": 9, "epic_files": 30,
            "source": {"epic_components": "default", "epic_units": "declared",
                       "epic_files": "default"},
        })

    def test_a_declared_range_wins_over_the_generic_division(self) -> None:
        d = self.repo_with("win", {CONF: "component=ui|web\n",
                                   "web/.polyforge/verify-scope.conf": "stack=none\n",
                                   "web/src/app.ts": "x\n", "web/e2e/a.mjs": "e\n",
                                   "other/o.go": "package o\n"})
        c = self.run_tool("--repo", d, "--json")
        entry = json.loads(c.stdout)["repos"][0]
        self.assertEqual([cmp["name"] for cmp in entry["components"]], ["ui", ".polyforge", "other"])
        self.assertEqual(entry["components"][0]["paths"], ["web"])

    def test_several_repos_report_in_order_with_per_repo_errors(self) -> None:
        a = self.repo_with("a", {"x.go": "x\n"})
        b = self.repo_with("b", {CONF: "component=core|src\n", "src/y.go": "y\n"})
        c = self.run_tool("--repo", a, "--repo", b, "--repo", self.new_dir("plain"))
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertIn("repo a ", c.stdout)
        self.assertIn("component core  [src]  declared", c.stdout)
        self.assertIn("repo plain ", c.stdout)
        self.assertIn("cannot read", c.stdout)

    def test_bad_conf_lines_are_diagnosed_on_stderr(self) -> None:
        d = self.repo_with("bad", {CONF: "component=x|../escape\ncomponent=core|src\n",
                                   "src/a.go": "a\n"})
        c = self.run_tool("--repo", d)
        self.assertEqual(c.returncode, 0)
        self.assertEqual([cmp["name"] for cmp in json.loads(self.run_tool(
            "--repo", d, "--json").stdout)["repos"][0]["components"]], ["core", ".polyforge"])
        self.assertTrue(any("component" in line for line in c.stderr.splitlines()), c.stderr)

    def test_usage_errors(self) -> None:
        c = self.run_tool()
        self.assertEqual(c.returncode, 2)
        self.assertIn("--repo", c.stderr)


if __name__ == "__main__":
    unittest.main()
