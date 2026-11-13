"""verify-scope's host_requires= declarations: a repository's host
prerequisites (name, probe command, install hint) echoed in --json as
`host_requires` in file order, only when one is accepted; a malformed line
or a second line for one name is diagnosed and contributes nothing."""

import json
import os
import subprocess
from typing import Any, Dict, List

from .gitworld import GitCase, Repo

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")
CONF = ".polyforge/verify-scope.conf"

GOOD = "host_requires=demo-tool|demo-tool --version|pip3 install --user \"demo-tool>=1.2,<2\" | tee log"
GOOD_ENTRY = {"name": "demo-tool", "probe": "demo-tool --version",
              "hint": "pip3 install --user \"demo-tool>=1.2,<2\" | tee log"}
SECOND = "host_requires=lint.x_2|test -x ./bin/lint|make lint-tools"
SECOND_ENTRY = {"name": "lint.x_2", "probe": "test -x ./bin/lint", "hint": "make lint-tools"}


class HostRequiresTest(GitCase):
    def report(self, body: str) -> Dict[str, Any]:
        d = os.path.join(self.tmp, "r%d" % len(os.listdir(self.tmp)))
        Repo(d, {CONF: "stack=none\nfast_gate=true\n" + body, "a.txt": "a\n"})
        c = subprocess.run(["sh", ENGINE, "--json", "--base", "HEAD"], cwd=d,
                           capture_output=True, text=True, stdin=subprocess.DEVNULL)
        self.assertEqual(c.returncode, 0, c.stderr)
        doc = json.loads(c.stdout)
        self.assertIsInstance(doc, dict)
        return dict(doc)

    def test_declarations_parsed_and_malformed_rejected(self) -> None:
        with self.subTest("accepted lines are listed verbatim in file order"):
            doc = self.report(GOOD + "\n" + SECOND + "\n")
            self.assertEqual(doc.get("host_requires"), [GOOD_ENTRY, SECOND_ENTRY])
            hits: List[str] = [d for d in doc["diagnostics"] if "host_requires" in d]
            self.assertEqual(hits, [])
            self.assertEqual([c["cmd"] for c in doc["commands"]], ["true"])

        with self.subTest("no declaration, no key"):
            doc = self.report("")
            self.assertNotIn("host_requires", doc)

        rejected = {
            "two fields": "host_requires=demo-tool|demo-tool --version",
            "one field": "host_requires=demo-tool",
            "empty name": "host_requires=|demo-tool --version|get it",
            "name with a blank": "host_requires=demo tool|demo-tool --version|get it",
            "name starting with a dot": "host_requires=.demo|demo-tool --version|get it",
            "name with a slash": "host_requires=demo/tool|demo-tool --version|get it",
            "blank probe": "host_requires=demo-x|   |get it",
            "empty probe": "host_requires=demo-x||get it",
            "blank hint": "host_requires=demo-x|demo-x --version|  ",
            "empty hint": "host_requires=demo-x|demo-x --version|",
            "command-substitution probe": "host_requires=demo-x|$(which demo-x) --version|get it",
            "backtick probe": "host_requires=demo-x|`which demo-x` --version|get it",
        }
        for label, bad in rejected.items():
            with self.subTest("rejected: " + label):
                doc = self.report(GOOD + "\n" + bad + "\n")
                self.assertEqual(doc.get("host_requires"), [GOOD_ENTRY])
                hits = [d for d in doc["diagnostics"] if "host_requires" in d and bad in d]
                self.assertEqual(len(hits), 1, doc["diagnostics"])

        with self.subTest("only rejected lines: no key"):
            doc = self.report(rejected["two fields"] + "\n")
            self.assertNotIn("host_requires", doc)
            hits = [d for d in doc["diagnostics"] if rejected["two fields"] in d]
            self.assertEqual(len(hits), 1, doc["diagnostics"])

        with self.subTest("a second line for one name is ignored"):
            dup = "host_requires=demo-tool|other --version|other hint"
            doc = self.report(GOOD + "\n" + dup + "\n" + SECOND + "\n")
            self.assertEqual(doc.get("host_requires"), [GOOD_ENTRY, SECOND_ENTRY])
            hits = [d for d in doc["diagnostics"] if "second host_requires for 'demo-tool'" in d and dup in d]
            self.assertEqual(len(hits), 1, doc["diagnostics"])
