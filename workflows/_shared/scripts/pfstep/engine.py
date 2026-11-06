"""The verification-scope engine's ``--json`` report (schema 1): decoded
and checked against its contract, and the commands one report contributes
to a tier (``choose``). Ported from the Go step commands' engine.go
(EngineReport, decodeReport, choose).
"""

import json
import os
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple

from . import gostr

TIER_FULL = "full"
TIER_SCOPED = "scoped"
TIER_LIGHT = "light"

# The e2e selection modes.
SELECTION_MODES = ("carries", "smoke", "full")


class ReportError(ValueError):
    """An engine report that breaks the contract."""


@dataclass
class ReportDecl:
    """A command's declared failure report: its format and repo-relative
    path or glob (the conf's report=)."""

    format: str
    path: str


@dataclass
class EngineCommand:
    """One planned command."""

    name: str
    phase: str
    cmd: str
    requires_env: List[str] = field(default_factory=list)
    requires_cmd: List[str] = field(default_factory=list)
    timeout: str = ""
    report: Optional[ReportDecl] = None
    # rerun: the declared unit re-run template ({unit}; "" = none)
    rerun: str = ""
    source: str = ""
    # dir, inner: a nested root's command (the light tier): its root
    # directory and the command its engine reported, before `cd <dir> &&`
    dir: str = ""
    inner: str = ""


@dataclass
class EngineReport:
    """The engine's report; ``raw`` is the decoded document."""

    raw: Dict[str, Any]
    schema: int
    stack: str
    conf: str
    conf_changed_in_diff: bool
    mode: str
    paths: Optional[List[str]]
    commands: List[EngineCommand]
    test_glob: List[str]
    base_setup: List[str]
    diagnostics: Optional[List[str]]
    # e2e_selection: {mode, suites, uncovered_paths, full_commands} (None = none)
    e2e_selection: Optional[Dict[str, Any]]


def _typed(obj: Dict[str, Any], key: str, typ: type, what: str) -> Any:
    v = obj.get(key)
    if v is None:
        return None
    if typ is int:
        if isinstance(v, bool) or not isinstance(v, (int, float)) or (isinstance(v, float) and not v.is_integer()):
            raise ReportError("json: cannot unmarshal %s into %s of type int" % (json.dumps(v), what))
        return int(v)
    if not isinstance(v, typ) or (typ is not bool and isinstance(v, bool)):
        raise ReportError("json: cannot unmarshal %s into %s" % (type(v).__name__, what))
    return v


def _strings(obj: Dict[str, Any], key: str, what: str) -> Optional[List[str]]:
    v = _typed(obj, key, list, what)
    if v is None:
        return None
    for x in v:
        if not isinstance(x, str):
            raise ReportError("json: cannot unmarshal %s into %s of type string" % (type(x).__name__, what))
    return list(v)


def _str(obj: Dict[str, Any], key: str, what: str) -> str:
    v = _typed(obj, key, str, what)
    return "" if v is None else str(v)


def decode_report(raw: bytes) -> EngineReport:
    """One engine report parsed and checked against the contract (the
    first JSON value of raw; what follows it is not read)."""
    text = raw.decode("utf-8", "replace").lstrip(" \t\r\n")
    try:
        doc, _ = json.JSONDecoder().raw_decode(text)
    except ValueError as err:
        if text == "":
            raise ReportError("EOF") from None
        raise ReportError(str(err)) from None
    if not isinstance(doc, dict):
        raise ReportError("json: cannot unmarshal %s into a report object" % type(doc).__name__)
    schema = _typed(doc, "schema", int, "report.schema") or 0
    if schema != 1:
        raise ReportError("schema %d, want 1" % schema)
    mode = _str(doc, "mode", "report.mode")
    if mode not in (TIER_FULL, TIER_SCOPED):
        raise ReportError("mode %s, want full or scoped" % gostr.quote(mode))
    sel = _typed(doc, "e2e_selection", dict, "report.e2e_selection")
    if sel is not None:
        smode = _str(sel, "mode", "report.e2e_selection.mode")
        if smode not in SELECTION_MODES:
            raise ReportError("e2e_selection mode %s, want carries, smoke or full" % gostr.quote(smode))
        sel = {
            "mode": smode,
            "suites": _strings(sel, "suites", "report.e2e_selection.suites"),
            "uncovered_paths": _strings(sel, "uncovered_paths", "report.e2e_selection.uncovered_paths"),
            "full_commands": _strings(sel, "full_commands", "report.e2e_selection.full_commands"),
        }
    cmds: List[EngineCommand] = []
    for i, c in enumerate(_typed(doc, "commands", list, "report.commands") or []):
        if not isinstance(c, dict):
            raise ReportError("json: cannot unmarshal %s into report.commands of type object" % type(c).__name__)
        rep = _typed(c, "report", dict, "report.commands.report")
        ec = EngineCommand(
            name=_str(c, "name", "report.commands.name"),
            phase=_str(c, "phase", "report.commands.phase"),
            cmd=_str(c, "cmd", "report.commands.cmd"),
            requires_env=_strings(c, "requires_env", "report.commands.requires_env") or [],
            requires_cmd=_strings(c, "requires_cmd", "report.commands.requires_cmd") or [],
            timeout=_str(c, "timeout", "report.commands.timeout"),
            report=None if rep is None else ReportDecl(_str(rep, "format", "report.format"), _str(rep, "path", "report.path")),
            rerun=_str(c, "rerun", "report.commands.rerun"),
            source=_str(c, "source", "report.commands.source"),
        )
        if gostr.trim_space(ec.name) == "" or gostr.trim_space(ec.cmd) == "":
            raise ReportError("commands[%d] lacks a name or a cmd" % i)
        if ec.phase not in ("fast", "full", "scoped"):
            raise ReportError("commands[%d] (%s) has phase %s" % (i, ec.name, gostr.quote(ec.phase)))
        cmds.append(ec)
    return EngineReport(
        raw=doc,
        schema=schema,
        stack=_str(doc, "stack", "report.stack"),
        conf=_str(doc, "conf", "report.conf"),
        conf_changed_in_diff=bool(_typed(doc, "conf_changed_in_diff", bool, "report.conf_changed_in_diff")),
        mode=mode,
        paths=_strings(doc, "paths", "report.paths"),
        commands=cmds,
        test_glob=_strings(doc, "test_glob", "report.test_glob") or [],
        base_setup=_strings(doc, "base_setup", "report.base_setup") or [],
        diagnostics=_strings(doc, "diagnostics", "report.diagnostics"),
        e2e_selection=sel,
    )


def choose(rep: EngineReport, tier: str) -> Tuple[List[EngineCommand], str]:
    """The commands one report contributes for the tier and the mode they
    belong to: the fast gates plus the full tail on a full report, the fast
    gates plus the scoped selection on a scoped report under the light tier
    (the mode is then light)."""
    mode, phase = TIER_FULL, TIER_FULL
    if rep.mode == TIER_SCOPED and tier == TIER_LIGHT:
        mode, phase = TIER_LIGHT, TIER_SCOPED
    return [c for c in rep.commands if c.phase in ("fast", phase)], mode


def argv_of(engine: str) -> List[str]:
    """How the engine at path engine runs: itself when it is executable,
    else through sh (a lane snapshot's files are mode 0444)."""
    return [engine] if os.access(engine, os.X_OK) else ["sh", engine]
