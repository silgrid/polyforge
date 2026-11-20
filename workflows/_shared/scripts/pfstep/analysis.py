"""The build's change analysis (``artifacts/build/change-analysis.json``):
the tests verification supplements the engine's selection with, their one
declaration and the file's parsing. The analysis's prose sections —
``changed`` (what the change altered) and ``uncovered`` (the engine report's
changed paths no ``carries:`` declaration covers) — are the review's to
judge; the machine's contract is ``supplement_limit``, ``tests`` and
``selected`` — the machine selection record the build close's scoped run
writes (``fast_checks.py --close-scoped``, polyforge#164: the selection
verification plans, its identity and its execution evidence, one entry
per task repo). A supplement entry names an existing test of a task repo
and the run that shows it passing, under the same marker rules as a
units.json run (pfstep/unitruns: the one implementation, never a second
copy).

The entry rules themselves — ``name_segments``, ``static_problem``, the
cap's ``taken`` split and the machine selection's ``selected_problem`` —
are the one shared validator every caller judges an entry by (verify.py's
``--analysis`` check and facts.py's close check, polyforge#112 and #164):
no second copy of a rule may appear. The git reads they need (a file
tracked at HEAD, its body, a repo's HEAD) stay the caller's plumbing,
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


@dataclass
class SelectedCommand:
    """One command of a repo's machine selection record: what ran (or was
    reused, or could not run) and its evidence."""

    name: str = ""
    root: str = ""
    cmd: str = ""
    phase: str = ""
    source: str = ""
    reused: str = ""  # the result file and entry a green same-tree run came from
    exit_code: Optional[int] = None
    timed_out: bool = False
    skipped: bool = False
    skip_reason: str = ""
    # the per-unit bar's own green through a flaky re-run: the whole command
    # re-run that exited 0, or its completion proof — evidence beside a red
    # first run
    confirmed: str = ""
    complete: str = ""


@dataclass
class SelectedDecl:
    """One task repo's machine selection record: the identity the selection
    was computed at (head, tree, base, default branch, engine invocations,
    the conf's own identity), the selection itself (mode, affected paths,
    suites, test globs, commands) and its outcome (status, reason)."""

    head: str = ""
    tree: str = ""
    base: str = ""
    default_branch: str = ""
    engine: str = ""
    conf: str = ""
    conf_changed_in_diff: bool = False
    mode: str = ""
    paths: List[str] = field(default_factory=list)
    suites: List[str] = field(default_factory=list)
    test_glob: List[str] = field(default_factory=list)
    commands: List[SelectedCommand] = field(default_factory=list)
    status: str = ""
    reason: str = ""


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


def selected_problem(d: SelectedDecl, head: str) -> str:
    """Why a repo's machine selection record cannot stand ("" = it can):
    the one shared validator (verify.py's ``--analysis`` check and
    facts.py's close check, the messages the existing tests pin) — the
    record's head must be the repo's HEAD it is judged at (a record of
    another tree is stale: recompute the selection at the final tree),
    its status must be green (a red or incomplete selection claimed
    nothing and cannot stand as evidence), and every selected command
    must carry its evidence: a green run (exit 0) or a named reuse
    source — a command that could not run, was killed at its timeout or
    ran red is a selection the close never completed. ``head`` is the
    caller's read of the repo's HEAD ("" when it cannot read one: the
    identity check is then the caller's to report)."""
    if head and d.head != head:
        return ("its head %s is not the tree it is judged at (%s): a selection record of another"
                " tree is stale — recompute it at the final tree" % (gostr.quote(d.head), head))
    if d.status != "green":
        return ("its selection is %s: %s" % (d.status, d.reason or "nothing ran")) if d.status else \
            "its selection records no status: the close's scoped run never completed"
    for c in d.commands:
        if c.reused:
            continue
        if c.skipped:
            return "its selected command `%s` cannot run: %s — the close never ran it" % (c.cmd, c.skip_reason or "no reason recorded")
        if c.timed_out:
            return "its selected command `%s` was killed at its timeout — its run is no evidence" % c.cmd
        if c.exit_code is None:
            return "its selected command `%s` carries no run: no green run, no reuse source — the close never completed it" % c.cmd
        if c.exit_code != 0 and not (c.confirmed or c.complete):
            # the per-unit bar's own green through a flaky re-run: a red
            # first run whose failing units re-ran flaky and whose whole
            # command was then confirmed (one whole re-run exiting 0) or
            # proven complete (its completion proof, the entry keeping the
            # first run's red exit beside it) is green evidence, the
            # record stating it with `confirmed`/`complete`
            return "its selected command `%s` exited %d: a red selection is not evidence" % (c.cmd, c.exit_code)
    return ""


@dataclass
class Analysis:
    """The analysis as the machine consumes it: the cap, the listed
    tests in their order (the order is the cut's) and the machine
    selection records, one per task repo ({} when the file carries
    none)."""

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

    # the machine selection records: one per task repo, each the close's
    # scoped run wrote (a file without the section carries none — the
    # section's presence itself is the build step's own rule)
    selected_raw = doc.get("selected", {})
    if not isinstance(selected_raw, dict) or not all(isinstance(k, str) and k != "" for k in selected_raw):
        raise ValueError("%s is not %s: selected is not an object of repo → selection record" % (path, shape))
    selected: Dict[str, SelectedDecl] = {}
    for repo, ent in selected_raw.items():
        at = "selected[%s]" % gostr.quote(repo)
        if not isinstance(ent, dict):
            raise ValueError("%s: %s is not an object" % (path, at))
        for key in ("head", "engine", "mode", "status"):
            if not isinstance(ent.get(key, ""), str):
                raise ValueError("%s: %s.%s is not a string" % (path, at, key))
        if gostr.trim_space(ent.get("head", "")) == "":
            raise ValueError("%s: %s names no head: a selection record is bound to the tree it was computed at" % (path, at))
        if gostr.trim_space(ent.get("engine", "")) == "":
            raise ValueError("%s: %s names no engine invocation: a selection record states its own identity" % (path, at))
        cmds_raw = ent.get("commands", [])
        if not isinstance(cmds_raw, list) or not all(isinstance(c, dict) for c in cmds_raw):
            raise ValueError("%s: %s.commands is not an array of objects" % (path, at))
        cmds: List[SelectedCommand] = []
        for c in cmds_raw:
            if gostr.trim_space(_text(c.get("cmd", ""), at + ".cmd")) == "":
                raise ValueError("%s: %s.commands names a command with no cmd" % (path, at))
            exit_code = c.get("exit_code")
            if exit_code is not None and (isinstance(exit_code, bool) or not isinstance(exit_code, int)):
                raise ValueError("%s: %s.commands exit_code is not a whole number" % (path, at))
            cmds.append(SelectedCommand(
                name=_text(c.get("name", ""), at + ".name"), root=_text(c.get("root", ""), at + ".root"),
                cmd=_text(c.get("cmd", ""), at + ".cmd"), phase=_text(c.get("phase", ""), at + ".phase"),
                source=_text(c.get("source", ""), at + ".source"), reused=_text(c.get("reused", ""), at + ".reused"),
                exit_code=exit_code, timed_out=bool(c.get("timed_out", False)),
                skipped=bool(c.get("skipped", False)), skip_reason=_text(c.get("skip_reason", ""), at + ".skip_reason"),
                confirmed=_text(c.get("confirmed", ""), at + ".confirmed"),
                complete=_text(c.get("complete", ""), at + ".complete"),
            ))
        sel = SelectedDecl(
            head=gostr.trim_space(ent.get("head", "")),
            tree=_text(ent.get("tree", ""), at + ".tree"), base=_text(ent.get("base", ""), at + ".base"),
            default_branch=_text(ent.get("default_branch", ""), at + ".default_branch"),
            engine=gostr.trim_space(ent.get("engine", "")), conf=_text(ent.get("conf", ""), at + ".conf"),
            conf_changed_in_diff=bool(ent.get("conf_changed_in_diff", False)),
            mode=_text(ent.get("mode", ""), at + ".mode"),
            paths=list(strings(list(ent.get("paths", [])), at + ".paths")) if isinstance(ent.get("paths", []), list) else [],
            suites=list(strings(list(ent.get("suites", [])), at + ".suites")) if isinstance(ent.get("suites", []), list) else [],
            test_glob=list(strings(list(ent.get("test_glob", [])), at + ".test_glob")) if isinstance(ent.get("test_glob", []), list) else [],
            commands=cmds, status=_text(ent.get("status", ""), at + ".status"),
            reason=_text(ent.get("reason", ""), at + ".reason"),
        )
        if sel.status not in ("green", "red", "incomplete"):
            raise ValueError("%s: %s.status is not green, red or incomplete" % (path, at))
        selected[repo] = sel

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
    return Analysis(limit=limit, decls=decls, selected=selected)
