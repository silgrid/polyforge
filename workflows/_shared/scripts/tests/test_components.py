"""components: the epic-upgrade rule's conf keys (polyforge#89, AC1) —
the ``component=`` declaration and the ``epic_components`` /
``epic_units`` / ``epic_files`` thresholds parsed by pfstep.components
with the ticket's defaults (3 / 8 / 30), the diagnosed-and-ignored lines,
and the scope engine's acceptance of the keys: the engine reads none of
them (the components.py tool owns them — the not_runnable_rule
precedent), so it answers the same report for a conf that declares them
and one that does not, keeps the gate id unchanged (the keys are no gate
keys), and never diagnoses them as unknown."""

import json
import os
import subprocess
import unittest
from typing import Any, Dict, Tuple

from pfstep import components, gateid

from .gitworld import GitCase, Repo

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")
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


if __name__ == "__main__":
    unittest.main()
