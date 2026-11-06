"""A command's declared failure report: a JUnit XML file.

A repository whose test runner's output ``pfstep.units`` cannot read
declares, in its scope conf, ``report=<check>|junit|<path or glob>`` (the
engine's ``--json`` carries it as the command's ``report``): the command
writes a JUnit XML report there. After a failing run of the command it is
read and every failed testcase (a ``<failure>`` or ``<error>`` child)
becomes one located unit — named ``<classname>::<name>`` (just ``<name>``
without a classname), its file the testcase's ``file`` attribute (else its
suite's) when present. Such a unit has no derived re-run, so it is never
flaky and never pre-existing (fail closed).

The report is read only when it is this command's: the path's files are
stamped (mtime, size, inode) before the command starts
(``snapshot_report``), and only a file it created or changed counts. The
testcases are merged with the units the output located
(``merge_reported``). A report that is missing, unchanged, unparseable or
names no failed testcase changes nothing, and the reason is recorded
(``ReportRead.note``). (The Go side's twin, verifyrun/junitreport.go,
left with the step command pf-verify.)
"""

import os
import xml.etree.ElementTree as ET
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Set, Tuple

from . import gomatch, gostr
from .engine import ReportDecl
from .rerun import UnitState
from .units import KIND_JUNIT, Unit, norm_sig

FORMAT_JUNIT = "junit"

# One report file is read up to this size (a larger one is unparseable).
MAX_BYTES = 64 << 20

# A file's state without reading it: (mtime ns, size, inode).
Stamp = Tuple[int, int, int]


@dataclass
class ReportRead:
    """How a command's declared report was used."""

    format: str
    path: str
    # files: the report files read (repository-relative)
    files: List[str] = field(default_factory=list)
    # units: how many failed testcases the report located
    units: int = 0
    # note: why the report located nothing, or why it was not read
    note: str = ""

    def as_dict(self) -> Dict[str, Any]:
        out: Dict[str, Any] = {"format": self.format, "path": self.path}
        if self.files:
            out["files"] = list(self.files)
        out["units"] = self.units
        if self.note:
            out["note"] = self.note
        return out


def _rel(repo_dir: str, p: str) -> str:
    return os.path.relpath(p, repo_dir).replace(os.sep, "/")


def snapshot_report(repo_dir: str, d: ReportDecl) -> Dict[str, Stamp]:
    """Every regular file the declared report path matches inside the
    repository, stamped ({} for an unreadable declaration)."""
    snap: Dict[str, Stamp] = {}
    if d.format != FORMAT_JUNIT or gostr.clean_rel(d.path) == "":
        return snap
    try:
        matches = gomatch.glob(os.path.join(repo_dir, d.path))
    except gomatch.BadPattern:
        return snap
    for m in matches:
        rel = _rel(repo_dir, m)
        if gostr.clean_rel(rel) == "":
            continue
        try:
            st = os.stat(m)
        except OSError:
            continue
        if not os.path.isfile(m):
            continue
        snap[rel] = (st.st_mtime_ns, st.st_size, st.st_ino)
    return snap


@dataclass
class _Case:
    name: str
    file: str
    signature: str


def _local(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def _attr(e: ET.Element, name: str) -> str:
    for k, v in e.attrib.items():
        if _local(k) == name:
            return v
    return ""


def _chardata(e: ET.Element) -> str:
    return (e.text or "") + "".join(c.tail or "" for c in e)


def junit_signature(message: str, text: str) -> str:
    """A failure's message and the first line of its body, normalized."""
    parts: List[str] = []
    m = gostr.trim_space(message)
    if m != "":
        parts.append(m)
    for line in text.split("\n"):
        line = gostr.trim_space(line)
        if line != "":
            if not parts or line != parts[0]:
                parts.append(line)
            break
    return norm_sig(" | ".join(parts))


def parse_junit_file(path: str) -> List[_Case]:
    """Every failed testcase of one JUnit XML file (ValueError naming why
    it cannot be read)."""
    try:
        with open(path, "rb") as f:
            raw = f.read(MAX_BYTES + 1)
    except OSError as err:
        raise ValueError(str(err)) from None
    if len(raw) > MAX_BYTES:
        raise ValueError("larger than %d MiB" % (MAX_BYTES >> 20))
    if raw.strip() == b"":
        raise ValueError("empty")
    try:
        root = ET.fromstring(raw)
    except ET.ParseError as err:
        raise ValueError(str(err)) from None
    n = _local(root.tag)
    if n not in ("testsuites", "testsuite"):
        raise ValueError("root element <%s>, want <testsuites> or <testsuite>" % n)
    out: List[_Case] = []

    def walk(s: ET.Element, file: str) -> None:
        sf = _attr(s, "file")
        if sf != "":
            file = sf
        for c in s:
            if _local(c.tag) != "testcase":
                continue
            fails = [x for x in c if _local(x.tag) == "failure"] + [x for x in c if _local(x.tag) == "error"]
            name, classname = _attr(c, "name"), _attr(c, "classname")
            if not fails or (name == "" and classname == ""):
                continue
            full = classname + "::" + name if classname != "" else name
            cf = _attr(c, "file") or file
            out.append(_Case(full, cf, junit_signature(_attr(fails[0], "message"), _chardata(fails[0]))))
        for sub in s:
            if _local(sub.tag) == "testsuite":
                walk(sub, file)

    walk(root, "")
    return out


def junit_file(repo_dir: str, report_dir: str, file: str) -> str:
    """A testcase's file repository-relative: an absolute path inside the
    repo, a relative one as written from the repo root or else from the
    report's directory when it exists there, else as written ("" when it
    leaves the repository)."""
    file = gostr.trim_space(file)
    if file == "":
        return ""
    if os.path.isabs(file):
        return gostr.clean_rel(_rel(repo_dir, file))
    for base in (repo_dir, report_dir):
        p = os.path.join(base, file)
        if not os.path.exists(p):
            continue
        c = gostr.clean_rel(_rel(repo_dir, os.path.normpath(p)))
        if c != "":
            return c
    return gostr.clean_rel(gostr.path_clean(file))


def read_declared_report(repo_dir: str, d: ReportDecl, before: Dict[str, Stamp]) -> Tuple[ReportRead, List[UnitState]]:
    """A command's declared report after its failing run: the failed
    testcases of every file the command wrote or rewrote as units, or none
    with the reason on the answer."""
    rd = ReportRead(format=d.format, path=d.path)
    if d.format != FORMAT_JUNIT:
        rd.note = "the declared report format %s is not one verify-run reads (junit)" % gostr.quote(d.format)
        return rd, []
    if gostr.clean_rel(d.path) == "":
        rd.note = "the declared report path %s is not repository-relative" % gostr.quote(d.path)
        return rd, []
    try:
        gomatch.glob(os.path.join(repo_dir, d.path))
    except gomatch.BadPattern as err:
        rd.note = "the declared report path %s is not a valid glob: %s" % (gostr.quote(d.path), err)
        return rd, []
    fresh: List[str] = []
    unchanged = 0
    for rel, st in snapshot_report(repo_dir, d).items():
        if before.get(rel) == st:
            unchanged += 1
            continue
        fresh.append(rel)
    fresh.sort()
    if not fresh:
        if unchanged > 0:
            rd.note = "the declared junit report %s was not written by this command (%d file(s) unchanged since before it ran)" % (d.path, unchanged)
        else:
            rd.note = "the declared junit report %s does not exist after the command" % d.path
        return rd, []
    units: List[UnitState] = []
    seen: Set[str] = set()
    for rel in fresh:
        p = os.path.join(repo_dir, rel)
        try:
            cases = parse_junit_file(p)
        except ValueError as err:
            # fail closed: a partial read locates nothing
            rd.note = "the declared junit report %s is unparseable: %s" % (rel, err)
            return rd, []
        report_dir = os.path.dirname(p)
        for c in cases:
            if c.name in seen:
                continue
            seen.add(c.name)
            units.append(UnitState(
                unit=Unit(kind=KIND_JUNIT, name=c.name, failed=True, marker="junit " + rel + ": " + c.name, signature=c.signature),
                file=junit_file(repo_dir, report_dir, c.file),
            ))
    rd.files, rd.units = fresh, len(units)
    if not units:
        rd.note = "the declared junit report %s names no failed testcase" % d.path
    return rd, units


def merge_reported(out: List[UnitState], reported: List[UnitState]) -> List[UnitState]:
    """The report's failed testcases folded into the units the output
    located: a failing output unit with a targeted re-run is kept and stands
    for every testcase it covers; one without a re-run that a testcase
    covers more precisely gives way to it; every testcase no kept unit
    covers is added. A failure the report does not mention is never dropped."""
    if not reported:
        return out

    def covers(o: UnitState, j: UnitState) -> bool:
        return ((o.file != "" and o.file == j.file) or j.name == o.name
                or j.name.startswith(o.name + "::") or j.name.endswith("::" + o.name))

    merged: List[UnitState] = []
    for o in out:
        if o.failed and o.rerun == "" and any(covers(o, j) for j in reported):
            continue
        merged.append(o)
    for j in reported:
        if not any(o.failed and o.kind != KIND_JUNIT and covers(o, j) for o in merged):
            merged.append(j)
    return merged


def note_of(rd: Optional[ReportRead]) -> str:
    """A report read's note ("" for none)."""
    return rd.note if rd is not None else ""
