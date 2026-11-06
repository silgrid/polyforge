"""The result file every step script writes: schema pf-step-result/1.

One JSON object, written atomically to the script's ``--out`` path (the
same document on stdout with ``--json``), its keys in the order below.
The verdict is the exit code's and nothing else (``verdict_of``). Its JSON
Schema is ``schema/pf-step-result-1.json`` beside this package (the shape
the Go step commands wrote).
A later script takes the values it needs (a head, a version) from the
result file, never from a hand-off's prose: ``read_result``.
"""

import datetime
import json
import os
from dataclasses import dataclass, field
from typing import Any, Dict, List, Mapping, Optional

from . import atomic, jsonout

SCHEMA = "pf-step-result/1"

VERDICT_PASS = "pass"  # 0: the script judged the tree and it passed
VERDICT_FAIL = "fail"  # 1: the script judged the tree and it failed
VERDICT_ERROR = "error"  # 2, or any other code: not judged
VERDICT_PARTIAL = "partial"  # 3: judged and passed, its side effect did not land

# FIELDS is the document's key order (the Go struct's field order).
FIELDS = (
    "schema",
    "command",
    "version",
    "argv",
    "round",
    "started_at",
    "ended_at",
    "exit_code",
    "verdict",
    "heads",
    "summary",
    "details",
    "logs",
)

# The schema file, beside the package.
SCHEMA_PATH = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "schema", "pf-step-result-1.json"
)


def verdict_of(exit_code: int) -> str:
    """The verdict an exit code decides: 0 pass, 1 fail, 3 partial, else error."""
    if exit_code == 0:
        return VERDICT_PASS
    if exit_code == 1:
        return VERDICT_FAIL
    if exit_code == 3:
        return VERDICT_PARTIAL
    return VERDICT_ERROR


def stamp(when: Optional[datetime.datetime] = None) -> str:
    """A timestamp as the result file carries it: RFC 3339, UTC, whole seconds."""
    t = when if when is not None else datetime.datetime.now(datetime.timezone.utc)
    if t.tzinfo is None:
        raise ValueError("stamp: a naive datetime has no zone")
    return t.astimezone(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


@dataclass
class Result:
    """One result file's content; ``to_dict`` gives it in FIELDS order."""

    command: str
    version: str
    argv: List[str] = field(default_factory=list)
    round: str = ""
    started_at: str = ""
    ended_at: str = ""
    exit_code: int = 2
    heads: Dict[str, str] = field(default_factory=dict)
    summary: str = ""
    details: Dict[str, Any] = field(default_factory=dict)
    logs: List[str] = field(default_factory=list)

    @property
    def verdict(self) -> str:
        return verdict_of(self.exit_code)

    def to_dict(self) -> Dict[str, Any]:
        return {
            "schema": SCHEMA,
            "command": self.command,
            "version": self.version,
            "argv": list(self.argv),
            "round": self.round,
            "started_at": self.started_at,
            "ended_at": self.ended_at,
            "exit_code": self.exit_code,
            "verdict": self.verdict,
            "heads": dict(self.heads),
            "summary": self.summary,
            "details": self.details,
            "logs": list(self.logs),
        }


def begin(
    command: str, argv: List[str], version: str, environ: Optional[Mapping[str, str]] = None
) -> Result:
    """A result opened now: the round is PF_ROUND ("" outside a step)."""
    env = os.environ if environ is None else environ
    return Result(
        command=command,
        version=version,
        argv=list(argv),
        round=env.get("PF_ROUND", ""),
        started_at=stamp(),
    )


def finish(res: Result, exit_code: int, out: Optional[str] = None) -> Result:
    """Close res with exit_code now and, when out is given, write it there."""
    res.exit_code = exit_code
    res.ended_at = stamp()
    if out:
        write_result(out, res)
    return res


def write_result(path: str, res: Result) -> None:
    """Land res at path atomically (mode 0644, the parent created)."""
    atomic.write(path, jsonout.dumps(res.to_dict()).encode("utf-8"), 0o644)


class ResultError(ValueError):
    """A file that is not a usable pf-step-result/1 document."""


def read_result(path: str) -> Dict[str, Any]:
    """Read a result file another script wrote, checked for its shape.

    The document must carry the schema tag, every key of FIELDS, and the
    verdict its exit code decides; anything else is a ResultError naming
    the file.
    """
    try:
        with open(path, "rb") as f:
            doc = json.loads(f.read().decode("utf-8"))
    except (OSError, ValueError) as err:
        raise ResultError("%s: not a readable JSON result file: %s" % (path, err)) from err
    if not isinstance(doc, dict):
        raise ResultError("%s: a result file is one JSON object" % path)
    if doc.get("schema") != SCHEMA:
        raise ResultError("%s: schema %r, want %r" % (path, doc.get("schema"), SCHEMA))
    missing = [k for k in FIELDS if k not in doc]
    if missing:
        raise ResultError("%s: missing %s" % (path, ", ".join(missing)))
    code = doc["exit_code"]
    if not isinstance(code, int) or isinstance(code, bool):
        raise ResultError("%s: exit_code %r is not an integer" % (path, code))
    if doc["verdict"] != verdict_of(code):
        raise ResultError(
            "%s: verdict %r, but exit code %d decides %r" % (path, doc["verdict"], code, verdict_of(code))
        )
    return doc
