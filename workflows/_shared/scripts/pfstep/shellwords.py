"""The little shell reading a bar needs to derive a unit's re-run command
and a command's skip form from a planned command line.

Word splitting with quotes, and whether the command is one simple command
(no pipe, list, redirection, subshell or comment at the top level) — the
only shape a flag may be appended to. An unquoted expansion (``$NAME``,
``${...}``, ``$(...)``, a backtick substitution) stays inside its word.
Every word also keeps its raw text exactly as written: a derived command
re-emits the planned command's own words raw (so quoting and expansions
mean what they meant), and quotes only the new words it adds
(``shell_quote``). Ported from the Go step commands' shellwords.go.
"""

import re
from typing import List, Tuple

_ASSIGNMENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=", re.ASCII)
_BARE_WORD = re.compile(r"[A-Za-z0-9_@%+=:,./-]+", re.ASCII)
_NAME_CHAR = re.compile(r"[_a-zA-Z0-9]", re.ASCII)


def shell_words(cmd: str) -> Tuple[List[str], bool]:
    """cmd's words (quotes removed) and whether it is one simple command."""
    words, _, simple = shell_words_raw(cmd)
    return words, simple


def shell_words_raw(cmd: str) -> Tuple[List[str], List[str], bool]:
    """cmd's words, their raw text, and simple: False when an unquoted
    operator (``| & ; < > ( )``), a comment, an expansion inside double
    quotes or an unterminated quote or expansion occurs — the words then
    cover only the text before it."""
    words: List[str] = []
    raws: List[str] = []
    cur: List[str] = []
    in_word = False
    start = 0

    def begin(i: int) -> None:
        nonlocal in_word, start
        if not in_word:
            start, in_word = i, True

    def flush(end: int) -> None:
        nonlocal in_word
        if in_word:
            words.append("".join(cur))
            raws.append(cmd[start:end])
            cur.clear()
            in_word = False

    n = len(cmd)
    i = 0
    while i < n:
        c = cmd[i]
        if c == "'":
            j = cmd.find("'", i + 1)
            if j < 0:
                return words, raws, False
            begin(i)
            cur.append(cmd[i + 1:j])
            i = j
        elif c == '"':
            begin(i)
            j = i + 1
            while j < n and cmd[j] != '"':
                if cmd[j] in "$`":
                    return words, raws, False
                if cmd[j] == "\\" and j + 1 < n:
                    j += 1
                cur.append(cmd[j])
                j += 1
            if j >= n:
                return words, raws, False
            i = j
        elif c == "\\" and i + 1 < n:
            begin(i)
            cur.append(cmd[i + 1])
            i += 1
        elif c in " \t\n":
            flush(i)
        elif c == "#" and not in_word:
            # a comment: whatever follows is no part of the command, and
            # nothing may be appended after it
            return words, raws, False
        elif c in "$`":
            end = expansion_end(cmd, i)
            if end < 0:
                return words, raws, False
            begin(i)
            cur.append(cmd[i:end])
            i = end - 1
        elif c in "|&;<>()":
            flush(i)
            return words, raws, False
        else:
            begin(i)
            cur.append(c)
        i += 1
    flush(n)
    return words, raws, True


def expansion_end(cmd: str, i: int) -> int:
    """The index just past the unquoted expansion that starts at cmd[i]
    (``$NAME``, ``${...}``, ``$(...)`` with its nesting and quotes, a
    backtick substitution), or -1 when it does not close."""
    n = len(cmd)
    if cmd[i] == "`":
        j = i + 1
        while j < n:
            if cmd[j] == "\\":
                j += 1
            elif cmd[j] == "`":
                return j + 1
            j += 1
        return -1
    if i + 1 >= n:
        return i + 1
    if cmd[i + 1] == "{":
        j = cmd.find("}", i + 2)
        return j + 1 if j >= 0 else -1
    if cmd[i + 1] == "(":
        depth = 0
        j = i + 1
        while j < n:
            c = cmd[j]
            if c == "\\":
                j += 1
            elif c == "'":
                k = cmd.find("'", j + 1)
                if k < 0:
                    return -1
                j = k
            elif c == '"':
                k = j + 1
                while k < n and cmd[k] != '"':
                    if cmd[k] == "\\":
                        k += 1
                    k += 1
                if k >= n:
                    return -1
                j = k
            elif c == "(":
                depth += 1
            elif c == ")":
                depth -= 1
                if depth == 0:
                    return j + 1
            j += 1
        return -1
    j = i + 1
    while j < n and _NAME_CHAR.match(cmd[j]):
        j += 1
    if j == i + 1 and j < n and cmd[j] in "?#@*!$-":
        j += 1  # a special parameter
    return j


def is_assignment(word: str) -> bool:
    """Whether word is a NAME=value assignment."""
    return _ASSIGNMENT.match(word) is not None


def leading_assignments(cmd: str) -> List[str]:
    """The NAME=value words a command starts with (after an optional
    ``env``), raw as written — the environment a derived re-run must keep
    (e.g. ``GOWORK=off``)."""
    words, raws, _ = shell_words_raw(cmd)
    i = 0
    if i < len(words) and words[i] == "env":
        i += 1
    out: List[str] = []
    while i < len(words) and is_assignment(words[i]):
        out.append(raws[i])
        i += 1
    return out


def shell_quote(w: str) -> str:
    """One NEW word (a test name, a node id) quoted for bash."""
    if _BARE_WORD.fullmatch(w):
        return w
    return "'" + w.replace("'", "'\\''") + "'"


def command_word(words: List[str]) -> Tuple[str, int]:
    """The command word of a simple command (assignments and an ``env``
    prefix skipped) and its index (("", -1) = none)."""
    i = 0
    if i < len(words) and words[i] == "env":
        i += 1
    while i < len(words):
        if not is_assignment(words[i]):
            return words[i], i
        i += 1
    return "", -1


def has_go_test(words: List[str]) -> bool:
    """Whether a simple command is a ``go test`` invocation."""
    w, i = command_word(words)
    return (w == "go" or w.endswith("/go")) and i + 1 < len(words) and words[i + 1] == "test"


def has_flag(words: List[str], flag: str) -> bool:
    """Whether a Go flag is already given (-f, --f, -f=v)."""
    bare = flag.lstrip("-")
    for w in words:
        t = w.lstrip("-")
        if w.startswith("-") and (t == bare or t.startswith(bare + "=")):
            return True
    return False


def has_assignment(cmd: str, name: str) -> bool:
    """Whether a command's leading assignments set name."""
    return any(a.startswith(name + "=") for a in leading_assignments(cmd))


def token_index(words: List[str], tok: str) -> int:
    """The index of the word that is tok or ends in /tok (-1 = none)."""
    for i, w in enumerate(words):
        if w == tok or w.endswith("/" + tok):
            return i
    return -1
