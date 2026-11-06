"""Every entry script (a ``*.py`` directly in a lane's or the shared tree's
``scripts/``) opens with the prologue the workflow contract's "Scripts"
section prescribes before it imports pfstep: ``sys.dont_write_bytecode =
True`` (no ``__pycache__`` lands in a capsule's lane snapshot) and its
``sys.path.insert`` (the snapshot is imported by path, never installed)."""

import os
import re
import unittest
from typing import List

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORKFLOWS = os.path.dirname(os.path.dirname(SCRIPTS))

_IMPORT = re.compile(r"^\s*(?:import\s+pfstep\b|from\s+pfstep\b)", re.ASCII | re.MULTILINE)


def prologue_problems(src: str) -> List[str]:
    """What src lacks before its first pfstep import ([] = none)."""
    m = _IMPORT.search(src)
    if m is None:
        return []
    head = src[: m.start()]
    out = []
    if not re.search(r"^sys\.dont_write_bytecode\s*=\s*True\s*$", head, re.ASCII | re.MULTILINE):
        out.append("sys.dont_write_bytecode = True")
    if not re.search(r"^sys\.path\.insert\(0,", head, re.ASCII | re.MULTILINE):
        out.append("sys.path.insert(0, ...)")
    return out


def entry_scripts() -> List[str]:
    out = []
    for lane in sorted(os.listdir(WORKFLOWS)):
        d = os.path.join(WORKFLOWS, lane, "scripts")
        if not os.path.isdir(d):
            continue
        for name in sorted(os.listdir(d)):
            if name.endswith(".py") and os.path.isfile(os.path.join(d, name)):
                out.append(os.path.join(d, name))
    return out


class EntryScriptTest(unittest.TestCase):
    def test_every_entry_script_has_the_prologue(self) -> None:
        for path in entry_scripts():
            with open(path, encoding="utf-8") as f:
                self.assertEqual(prologue_problems(f.read()), [], path)

    def test_the_check_bites(self) -> None:
        good = (
            "import os\nimport sys\n\nsys.dont_write_bytecode = True\n"
            "sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))\n\nfrom pfstep import result\n"
        )
        self.assertEqual(prologue_problems(good), [])
        self.assertEqual(prologue_problems(good.replace("sys.dont_write_bytecode = True\n", "")),
                         ["sys.dont_write_bytecode = True"])
        late = "import sys\nfrom pfstep import result\nsys.dont_write_bytecode = True\nsys.path.insert(0, 'x')\n"
        self.assertEqual(len(prologue_problems(late)), 2, "a prologue after the import is too late")
        self.assertEqual(prologue_problems("print('no pfstep')\n"), [])


if __name__ == "__main__":
    unittest.main()
