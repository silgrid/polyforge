"""The build's change analysis (``artifacts/build/change-analysis.json``):
the tests verification supplements the engine's selection with, their one
declaration and the file's parsing. The analysis's prose sections —
``changed`` (what the change altered) and ``uncovered`` (the engine report's
changed paths no ``carries:`` declaration covers) — are the review's to
judge; the machine's contract is ``supplement_limit`` and ``tests``. A
supplement entry names an existing test of a task repo and the run that
shows it passing, under the same marker rules as a units.json run
(pfstep/unitruns: the one implementation, never a second copy).

The entry rules themselves — ``name_segments``, ``static_problem`` and the
cap's ``taken`` split — are the one shared validator every caller judges an
entry by (verify.py's ``--analysis`` check and facts.py's close check,
polyforge#112): no second copy of a rule may appear. The git reads they
need (a file tracked at HEAD, its body) stay the caller's plumbing,
injected as callables; the rules, their order and their messages live here,
once."""

import json
import re
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Tuple

from . import gostr
from .unitruns import run_shape_problem

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


# The separators a qualified test name joins its parts by.
NAME_SEGMENTS = re.compile(r"::|[./]")


def name_segments(name: str) -> Optional[List[str]]:
    """The name's segments when it is qualified — two or more non-empty
    parts joined by ``.`` (a class and its method), ``::`` or ``/`` (a
    nested name): the parts sit on separate lines of the source, so the
    joined name is judged part by part. None when the name carries no
    separator (or no non-empty part besides one): the verbatim rule."""
    parts = [p for p in NAME_SEGMENTS.split(name) if p != ""]
    return parts if len(parts) > 1 else None


def static_problem(d: AnalysisDecl, repo_name: str, is_dir: Callable[[str], bool],
                   tracked: Callable[[str], bool], body_of: Callable[[str], str]) -> Tuple[str, str]:
    """Why the listed test cannot run or does not exist at HEAD, and its
    run's cleaned cwd ("" = it can): the run's shape rules first, then the
    test itself — its ``file`` a repo-relative path tracked at HEAD, its
    ``name`` (when given) inside it, a bare name verbatim and a `.`-, ``::``
    or ``/``-joined one segment by segment. The one shared validator
    (verify.py's ``--analysis`` check and facts.py's close check, the
    messages the existing tests pin); the git reads arrive as callables
    (``tracked(p)`` whether p is a tracked file at HEAD, ``body_of(p)`` its
    body, "" when it cannot be read)."""
    problem, cwd = run_shape_problem(d.argv, d.cwd, d.marker, repo_name, is_dir)
    if problem:
        return problem, cwd
    p = gostr.clean_rel(d.file)
    if p == "" or p.startswith("/") or p == ".." or p.startswith("../"):
        return "its file %s is not a repo-relative path" % gostr.quote(d.file), cwd
    if not tracked(p):
        return ("its file %s is not a tracked file of repos/%s at HEAD: the analysis lists a"
                " test that does not exist" % (gostr.quote(p), repo_name)), cwd
    if d.name != "":
        body = body_of(p)
        segs = name_segments(d.name)
        if segs is None:
            if d.name not in body:
                return ("its name %s appears nowhere in %s at HEAD: the analysis lists a test"
                        " that does not exist" % (gostr.quote(d.name), gostr.quote(p))), cwd
        else:
            for seg in segs:
                if seg not in body:
                    return ("its name segment %s appears nowhere in %s at HEAD: the analysis"
                            " lists a test that does not exist" % (gostr.quote(seg), gostr.quote(p))), cwd
    return "", cwd


@dataclass
class SelectedCommand:
    """One command of a repo's machine selection record (skeleton)."""

    name: str = ""
    cmd: str = ""
    phase: str = ""
    reused: str = ""
    exit_code: Optional[int] = None
    timed_out: bool = False
    skipped: bool = False
    skip_reason: str = ""


@dataclass
class SelectedDecl:
    """One task repo's machine selection record (skeleton)."""

    head: str = ""
    engine: str = ""
    mode: str = ""
    status: str = ""
    reason: str = ""
    commands: List[SelectedCommand] = field(default_factory=list)


def selected_problem(d: SelectedDecl, head: str) -> str:
    """Why a repo's machine selection record cannot stand (skeleton)."""
    return ""


def missing_selected_problem(repo: str) -> str:
    """Why a touched repo's analysis carries no machine selection record (skeleton)."""
    return ""


@dataclass
class Analysis:
    """The analysis as the machine consumes it: the cap and the listed
    tests in their order (the order is the cut's)."""

    limit: int = DEFAULT_SUPPLEMENT_LIMIT
    decls: List[AnalysisDecl] = field(default_factory=list)
    selected: Dict[str, SelectedDecl] = field(default_factory=dict)

    def taken(self) -> Tuple[List[AnalysisDecl], List[AnalysisDecl]]:
        """The entries the supplement cap takes (the first ones in listed
        order — the order is the cut's), and the cut tail every caller
        names explicitly: the one selection verification's ``--analysis``
        and the build close's check share."""
        return self.decls[: self.limit], self.decls[self.limit:]


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
