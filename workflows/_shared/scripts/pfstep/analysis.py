"""The build's change analysis (``artifacts/build/change-analysis.json``):
the tests verification supplements the engine's selection with, their one
declaration and the file's parsing. The analysis's prose sections —
``changed`` (what the change altered) and ``uncovered`` (the engine report's
changed paths no ``carries:`` declaration covers) — are the review's to
judge; the machine's contract is ``supplement_limit`` and ``tests``. A
supplement entry names an existing test of a task repo and the run that
shows it passing, under the same marker rules as a units.json run
(pfstep/unitruns: the one implementation, never a second copy)."""

import json
from dataclasses import dataclass, field
from typing import Any, List

from . import gostr
from .unitruns import marker_problem

# The supplement cap when the analysis names none (the lane's build step
# writes its own value into the file; this is the floor every lane gets).
DEFAULT_SUPPLEMENT_LIMIT = 10


@dataclass
class AnalysisDecl:
    """One listed test: an existing test of a task repo and the run that
    shows it passing."""

    id: str
    repo: str = ""
    file: str = ""
    name: str = ""  # optional: the test's name inside file
    cwd: str = ""
    argv: List[str] = field(default_factory=list)
    marker: str = ""
    why: str = ""

    def check(self) -> str:
        return self.repo + ":analysis:" + self.id


@dataclass
class Analysis:
    """The analysis as the machine consumes it: the cap and the listed
    tests in their order (the order is the cut's)."""

    limit: int = DEFAULT_SUPPLEMENT_LIMIT
    decls: List[AnalysisDecl] = field(default_factory=list)


def _text(v: Any, what: str) -> str:
    if v is None:
        return ""
    if not isinstance(v, str):
        raise ValueError("%s is not a string" % what)
    return v


def read_analysis(path: str) -> Analysis:
    """The change analysis as one value: its supplement cap (the default
    when it names none) and its listed tests in order. ValueError when the
    file is unusable, naming what is wrong."""
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as err:
        raise ValueError(str(err)) from None
    shape = "a JSON object {changed[], uncovered{repo: [path]}, supplement_limit, tests[]}"
    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise ValueError("%s is not %s: %s" % (path, shape, err)) from None
    if not isinstance(doc, dict):
        raise ValueError("%s is not %s: it is not an object" % (path, shape))

    def strings(v: Any, what: str) -> List[str]:
        if not isinstance(v, list) or not all(isinstance(x, str) for x in v):
            raise ValueError("%s is not %s: %s is not an array of strings" % (path, shape, what))
        return list(v)

    # the prose sections are shape-checked only (their content is the
    # review's): changed lists what the change altered, uncovered the
    # engine report's uncovered paths per repo.
    strings(doc.get("changed", []), "changed")
    uncovered = doc.get("uncovered", {})
    if not isinstance(uncovered, dict) or not all(
        isinstance(v, list) and all(isinstance(x, str) for x in v) for v in uncovered.values()
    ):
        raise ValueError("%s is not %s: uncovered is not an object of repo → array of strings" % (path, shape))

    limit = DEFAULT_SUPPLEMENT_LIMIT
    if "supplement_limit" in doc:
        v = doc["supplement_limit"]
        if isinstance(v, bool) or not isinstance(v, int) or v < 1:
            raise ValueError("%s: supplement_limit is a positive whole number of test runs, not %s"
                             % (path, gostr.quote(json.dumps(v))))
        limit = v

    tests = doc.get("tests", [])
    if not isinstance(tests, list):
        raise ValueError("%s is not %s: tests is not an array" % (path, shape))
    decls: List[AnalysisDecl] = []
    ids: List[str] = []
    for i, t in enumerate(tests):
        at = "tests[%d]" % i
        if not isinstance(t, dict):
            raise ValueError("%s: %s is not an object" % (path, at))
        d = AnalysisDecl(
            id=gostr.trim_space(_text(t.get("id", ""), at + ".id")) or ("a%d" % (i + 1)),
            repo=gostr.trim_space(_text(t.get("repo"), at + ".repo")),
            file=gostr.trim_space(_text(t.get("file"), at + ".file")),
            name=_text(t.get("name", ""), at + ".name"),
            cwd=_text(t.get("cwd", ""), at + ".cwd"),
            marker=_text(t.get("marker"), at + ".marker"),
            why=gostr.trim_space(_text(t.get("why"), at + ".why")),
        )
        argv = t.get("argv")
        if isinstance(argv, list) and all(isinstance(a, str) for a in argv):
            d.argv = list(argv)
        if d.repo == "" or d.file == "":
            raise ValueError("%s: %s names no repo or no file" % (path, at))
        if not d.argv or gostr.trim_space(d.argv[0]) == "":
            raise ValueError("%s: %s names no run (argv is empty): nothing to run as written" % (path, at))
        if d.why == "":
            raise ValueError("%s: %s names no why (what the test pins of the change)" % (path, at))
        if d.id in ids:
            raise ValueError("%s: %s repeats the id %s" % (path, at, d.id))
        ids.append(d.id)
        decls.append(d)
    return Analysis(limit=limit, decls=decls)
