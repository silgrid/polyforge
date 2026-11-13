"""The ``carries:`` declaration of a failing unit's file, recorded on the
unit so a routing can tell a red in the change's area from an unrelated
one.

The syntax is the scope engine's ("Carries layer"; docs/contributing.md):
the file's opening block of blank and comment (``//``, ``#``) lines, each
``carries:`` line a comma- or blank-separated list of globs relative to the
file's engine root — the nearest directory above it with its own
``.polyforge/verify-scope.conf``, else the repository root. The globs are
answered repository-relative (the root's directory prefixed). A malformed
declaration (an empty ``carries:`` line, a ``carries`` word without its
colon) declares nothing. Ported from the Go step commands' carries.go.
"""

import os
import re
from typing import List

from . import gostr

_COMMENT_LEAD = re.compile(r"(//+|#+)!?[ \t]*", re.ASCII)
_CARRIES_KEY = re.compile(r"carries[ \t]*:", re.ASCII)
_CARRIES_BAD = re.compile(r"carries(?:[^A-Za-z0-9_-]|\Z)", re.ASCII)
_CARRIES_SPLIT = re.compile(r"[ \t,]+", re.ASCII)

# How much of a file the header read takes.
MAX_HEADER = 64 << 10


def parse_carries(src: str) -> List[str]:
    """The globs a file's opening comment block declares ([] = none, or a
    malformed declaration)."""
    globs: List[str] = []
    for line in src.split("\n"):
        t = gostr.trim_space(line)
        if t == "":
            continue
        if not t.startswith("//") and not t.startswith("#"):
            break
        c = _COMMENT_LEAD.sub("", t, count=1) if _COMMENT_LEAD.match(t) else t
        if _CARRIES_KEY.match(c):
            v = gostr.trim_space(_CARRIES_KEY.sub("", c, count=1))
            if v == "":
                return []
            globs.extend(g for g in _CARRIES_SPLIT.split(v) if g != "")
            continue
        if _CARRIES_BAD.match(c):
            return []
    return globs


def engine_root_of(repo_dir: str, rel: str) -> str:
    """The repository-relative engine root of a file: the nearest directory
    above it carrying ``.polyforge/verify-scope.conf`` ("" = the root)."""
    d = gostr.path_dir(rel)
    while d not in (".", "/", ""):
        p = os.path.join(repo_dir, d, ".polyforge", "verify-scope.conf")
        if os.path.exists(p) and not os.path.isdir(p):
            return d
        d = gostr.path_dir(d)
    return ""


def file_carries(repo_dir: str, rel: str) -> List[str]:
    """The repository-relative globs the repo file rel declares ([] = none)."""
    try:
        with open(os.path.join(repo_dir, rel), "rb") as f:
            head = f.read(MAX_HEADER)
    except OSError:
        return []
    globs = parse_carries(head.decode("utf-8", "replace"))
    if not globs:
        return []
    root = engine_root_of(repo_dir, rel)
    if root == "":
        return globs
    return [root + "/" + g for g in globs]


def glob_match(pattern: str, s: str) -> bool:
    """The scope engine's glob (a carries: line's, a conf test_glob) as
    shell ``case`` matches it: ``*`` any run (slashes included), ``?`` one
    character, ``[...]`` a class (``[!...]`` negated)."""
    b = ["^"]
    i = 0
    while i < len(pattern):
        c = pattern[i]
        if c == "*":
            b.append(".*")
        elif c == "?":
            b.append(".")
        elif c == "[":
            j = pattern.find("]", i + 1)
            if j < 0:
                b.append(r"\[")
                i += 1
                continue
            cls = pattern[i + 1:j]
            if cls.startswith("!"):
                cls = "^" + cls[1:]
            b.append("[" + cls + "]")
            i = j
        else:
            b.append(gostr.quote_meta(c))
        i += 1
    b.append(r"\Z")
    try:
        return re.match("".join(b), s, re.ASCII | re.DOTALL) is not None
    except re.error:
        return False
