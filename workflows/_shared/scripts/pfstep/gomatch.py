"""Go's path.Match and filepath.Glob, written out (fnmatch and glob differ:
``*`` there takes a leading dot or crosses no separator differently, ``[!``
negates, and a malformed pattern is no error).

- ``match(pattern, name)``: ``*`` any run of non-``/`` characters, ``?``
  one non-``/`` character, ``[...]`` / ``[^...]`` a class with ranges,
  ``\\`` an escape; a malformed pattern raises BadPattern (Go's
  ErrBadPattern), also when the match fails before reaching the fault;
- ``glob(pattern)``: the existing paths that match, each directory's names
  sorted, a pattern without a meta character answered by its own
  existence.
"""

import os
from typing import List, Optional, Tuple

from . import gostr


class BadPattern(ValueError):
    """syntax error in pattern (Go's path.ErrBadPattern)."""

    def __init__(self) -> None:
        super().__init__("syntax error in pattern")


def _scan_chunk(pattern: str) -> Tuple[bool, str, str]:
    star = False
    while pattern and pattern[0] == "*":
        pattern = pattern[1:]
        star = True
    inrange = False
    i = 0
    while i < len(pattern):
        c = pattern[i]
        if c == "\\":
            if i + 1 < len(pattern):
                i += 1
        elif c == "[":
            inrange = True
        elif c == "]":
            inrange = False
        elif c == "*" and not inrange:
            break
        i += 1
    return star, pattern[:i], pattern[i:]


def _get_esc(chunk: str) -> Tuple[str, str]:
    if not chunk or chunk[0] in "-]":
        raise BadPattern()
    if chunk[0] == "\\":
        chunk = chunk[1:]
        if not chunk:
            raise BadPattern()
    r, rest = chunk[0], chunk[1:]
    if not rest:
        raise BadPattern()
    return r, rest


def _match_chunk(chunk: str, s: str) -> Tuple[str, bool]:
    """(rest, ok); raises BadPattern on a malformed chunk."""
    failed = False
    while chunk:
        if not failed and not s:
            failed = True
        c = chunk[0]
        if c == "[":
            r: Optional[str] = None
            if not failed:
                r, s = s[0], s[1:]
            chunk = chunk[1:]
            negated = False
            if chunk and chunk[0] == "^":
                negated = True
                chunk = chunk[1:]
            matched = False
            nrange = 0
            while True:
                if chunk and chunk[0] == "]" and nrange > 0:
                    chunk = chunk[1:]
                    break
                lo, chunk = _get_esc(chunk)
                hi = lo
                if chunk[0] == "-":
                    hi, chunk = _get_esc(chunk[1:])
                if r is not None and lo <= r <= hi:
                    matched = True
                nrange += 1
            if matched == negated:
                failed = True
        elif c == "?":
            if not failed:
                if s[0] == "/":
                    failed = True
                s = s[1:]
            chunk = chunk[1:]
        else:
            if c == "\\":
                chunk = chunk[1:]
                if not chunk:
                    raise BadPattern()
            if not failed:
                if chunk[0] != s[0]:
                    failed = True
                s = s[1:]
            chunk = chunk[1:]
    if failed:
        return "", False
    return s, True


def match(pattern: str, name: str) -> bool:
    """path.Match(pattern, name); raises BadPattern."""
    while pattern:
        star, chunk, pattern = _scan_chunk(pattern)
        if star and chunk == "":
            # a trailing * matches the rest unless it holds a /
            return "/" not in name
        t, ok = _match_chunk(chunk, name)
        if ok and (t == "" or pattern):
            name = t
            continue
        found = False
        if star:
            i = 0
            while i < len(name) and name[i] != "/":
                t, ok = _match_chunk(chunk, name[i + 1:])
                if ok:
                    if not pattern and t:
                        i += 1
                        continue
                    name = t
                    found = True
                    break
                i += 1
        if found:
            continue
        # the rest of the pattern must still be well formed
        while pattern:
            _, chunk, pattern = _scan_chunk(pattern)
            _match_chunk(chunk, "")
        return False
    return name == ""


def has_meta(path: str) -> bool:
    return any(c in path for c in "*?[\\")


def _glob_in(directory: str, pattern: str, matches: List[str]) -> None:
    try:
        if not os.path.isdir(directory):
            return
        names = sorted(os.listdir(directory))
    except OSError:
        return
    for n in names:
        if match(pattern, n):
            matches.append(gostr.path_clean(directory + "/" + n))


def glob(pattern: str) -> List[str]:
    """filepath.Glob(pattern); raises BadPattern."""
    match(pattern, "")
    if not has_meta(pattern):
        try:
            os.lstat(pattern)
        except OSError:
            return []
        return [pattern]
    cut = pattern.rfind("/") + 1
    directory, file = pattern[:cut], pattern[cut:]
    if directory == "":
        directory = "."
    elif directory != "/":
        directory = directory[:-1]
    if not has_meta(directory):
        out: List[str] = []
        _glob_in(directory, file, out)
        return out
    if directory == pattern:
        raise BadPattern()
    out = []
    for d in glob(directory):
        _glob_in(d, file, out)
    return out
