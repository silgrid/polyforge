"""POSIX extended regular expressions with Go's ``regexp.CompilePOSIX``
semantics: leftmost-longest matching.

Python's ``re`` is a leftmost-first engine (the first alternative that
matches wins); POSIX, and Go's ``CompilePOSIX``, take the leftmost match
and, among those, the longest. No standard-library engine does that, so
this module is one, written after Go's own (regexp/syntax: parse,
simplify, compile; regexp: the backtracker), for the syntax Go reads under
the POSIX flags:

- literals, ``.`` (any character but a newline), bracket expressions with
  ranges and the POSIX classes (``[[:alpha:]]``: ASCII only, as Go's),
  ``^`` / ``$`` (line anchors: the start and end of a line), groups
  ``( )`` (capturing, numbered by their opening parenthesis), ``|``, and
  the repetitions ``*``, ``+``, ``?``, ``{n}``, ``{n,}`` and ``{n,m}`` (at
  most 1000; stacked repetitions are allowed, as POSIX reads them);
- an escaped punctuation character is itself, ``\\n`` and the other C
  escapes are their characters.

What Go refuses, this refuses with Go's message (``ReError``): a
repetition with nothing to repeat, an unbalanced parenthesis or bracket,
an invalid range or class name, a repeat count past 1000, a nesting deeper
than 1000, an expression too large.

The match is Go's exactly: the leftmost start, the longest end from it,
and among the paths reaching that end the one a backtracking search tries
first (Go's documented divergence from POSIX's submatch rule). The search
runs Go's bit-state backtracker — every (instruction, position) is visited
once — so it is linear in the text times the program, never exponential.
A text is a str of code points (Go reads runes of UTF-8 text; the caller
refuses text that is not UTF-8).
"""

import bisect
import re as _re
import sys
from typing import Dict, List, Optional, Set, Tuple

MAX_RUNE = 0x10FFFF

# Go's limits (regexp/syntax/parse.go).
MAX_HEIGHT = 1000
MAX_SIZE = (128 << 20) // 40
MAX_REPEAT = 1000

# Go's error codes (syntax.ErrorCode texts).
ERR_INVALID_CHAR_RANGE = "invalid character class range"
ERR_INVALID_ESCAPE = "invalid escape sequence"
ERR_INVALID_REPEAT_SIZE = "invalid repeat count"
ERR_MISSING_BRACKET = "missing closing ]"
ERR_MISSING_PAREN = "missing closing )"
ERR_MISSING_REPEAT_ARGUMENT = "missing argument to repetition operator"
ERR_TRAILING_BACKSLASH = "trailing backslash at end of expression"
ERR_UNEXPECTED_PAREN = "unexpected )"
ERR_NESTING_DEPTH = "expression nests too deeply"
ERR_LARGE = "expression too large"


class ReError(ValueError):
    """A regular expression Go's parser refuses: ``error parsing regexp:
    <code>: `<expr>```, Go's text."""

    def __init__(self, code: str, expr: str) -> None:
        super().__init__("error parsing regexp: %s: `%s`" % (code, expr))
        self.code = code
        self.expr = expr


# The parse tree's operators (Go's syntax.Op).
NO_MATCH, EMPTY, LITERAL, CLASS, ANY_NOT_NL, BEGIN_LINE, END_LINE = 1, 2, 3, 4, 5, 6, 7
CAPTURE, STAR, PLUS, QUEST, REPEAT, CONCAT, ALTERNATE = 8, 9, 10, 11, 12, 13, 14
# the parser's stack markers (Go's pseudo operators)
_LEFT_PAREN, _VBAR = 100, 101


class Node:
    """One parse tree node (Go's syntax.Regexp, reduced)."""

    __slots__ = ("op", "subs", "runes", "cap", "min", "max", "height")

    def __init__(self, op: int, subs: Optional[List["Node"]] = None, runes: Optional[List[int]] = None,
                 cap: int = 0, min: int = 0, max: int = 0) -> None:
        self.op = op
        self.subs: List[Node] = subs if subs is not None else []
        # LITERAL: its runes; CLASS: sorted, merged ranges lo, hi, lo, hi, ...
        self.runes: List[int] = runes if runes is not None else []
        self.cap = cap
        self.min = min
        self.max = max
        # the tree's height under this node (Go's calcHeight)
        h = 0
        for x in self.subs:
            xh: int = x.height
            if xh > h:
                h = xh
        self.height: int = 1 + h


# POSIX classes (Go's posixGroup: ASCII only).
_POSIX: Dict[str, List[int]] = {
    "[:alnum:]": [0x30, 0x39, 0x41, 0x5A, 0x61, 0x7A],
    "[:alpha:]": [0x41, 0x5A, 0x61, 0x7A],
    "[:ascii:]": [0x00, 0x7F],
    "[:blank:]": [0x09, 0x09, 0x20, 0x20],
    "[:cntrl:]": [0x00, 0x1F, 0x7F, 0x7F],
    "[:digit:]": [0x30, 0x39],
    "[:graph:]": [0x21, 0x7E],
    "[:lower:]": [0x61, 0x7A],
    "[:print:]": [0x20, 0x7E],
    "[:punct:]": [0x21, 0x2F, 0x3A, 0x40, 0x5B, 0x60, 0x7B, 0x7E],
    "[:space:]": [0x09, 0x0D, 0x20, 0x20],
    "[:upper:]": [0x41, 0x5A],
    "[:word:]": [0x30, 0x39, 0x41, 0x5A, 0x5F, 0x5F, 0x61, 0x7A],
    "[:xdigit:]": [0x30, 0x39, 0x41, 0x46, 0x61, 0x66],
}
for _name, _cls in list(_POSIX.items()):
    _POSIX["[:^" + _name[2:]] = [-1] + _cls  # negated: marked, expanded on use


def _clean(ranges: List[int]) -> List[int]:
    """ranges (lo, hi pairs) sorted and merged (Go's cleanClass)."""
    pairs = sorted((ranges[i], ranges[i + 1]) for i in range(0, len(ranges), 2))
    out: List[int] = []
    for lo, hi in pairs:
        if out and lo <= out[-1] + 1:
            if hi > out[-1]:
                out[-1] = hi
            continue
        out += [lo, hi]
    return out


def _negate(ranges: List[int]) -> List[int]:
    """The complement of clean ranges within [0, MAX_RUNE]."""
    out: List[int] = []
    nxt = 0
    for i in range(0, len(ranges), 2):
        lo, hi = ranges[i], ranges[i + 1]
        if lo > nxt:
            out += [nxt, lo - 1]
        nxt = hi + 1
    if nxt <= MAX_RUNE:
        out += [nxt, MAX_RUNE]
    return out


def _is_alnum(c: str) -> bool:
    return ("0" <= c <= "9") or ("A" <= c <= "Z") or ("a" <= c <= "z")


class _Parser:
    """Go's parser under syntax.POSIX (flags 0), as a stack machine."""

    def __init__(self, s: str) -> None:
        self.whole = s
        self.stack: List[Node] = []
        self.num_cap = 0

    # -- the stack (Go's push / op / maybeConcat / concat / alternate) --

    def check(self, n: Node) -> Node:
        """n, or Go's refusal of a tree nested deeper than MAX_HEIGHT."""
        if n.height > MAX_HEIGHT:
            raise ReError(ERR_NESTING_DEPTH, self.whole)
        return n

    def push(self, n: Node) -> None:
        if n.op == CLASS and len(n.runes) == 2 and n.runes[0] == n.runes[1]:
            n = Node(LITERAL, runes=[n.runes[0]])
        self.stack.append(self.check(n))

    def literal(self, r: int) -> None:
        self.push(Node(LITERAL, runes=[r]))

    def _concat(self) -> None:
        i = len(self.stack)
        while i > 0 and self.stack[i - 1].op < _LEFT_PAREN:
            i -= 1
        subs = self.stack[i:]
        del self.stack[i:]
        if not subs:
            self.stack.append(Node(EMPTY))
            return
        self.stack.append(self.check(_collapse(subs, CONCAT)))

    def _alternate(self) -> None:
        i = len(self.stack)
        while i > 0 and self.stack[i - 1].op < _LEFT_PAREN:
            i -= 1
        subs = self.stack[i:]
        del self.stack[i:]
        if not subs:
            self.stack.append(Node(NO_MATCH))
            return
        self.stack.append(self.check(_collapse(subs, ALTERNATE)))

    def _swap_vertical_bar(self) -> bool:
        n = len(self.stack)
        if n >= 2 and self.stack[n - 2].op == _VBAR:
            self.stack[n - 2], self.stack[n - 1] = self.stack[n - 1], self.stack[n - 2]
            return True
        return False

    def vertical_bar(self) -> None:
        self._concat()
        # the alternatives so far sit below one bar: the new one joins them
        if not self._swap_vertical_bar():
            self.stack.append(Node(_VBAR))

    def right_paren(self) -> None:
        self._concat()
        if self._swap_vertical_bar():
            self.stack.pop()
        self._alternate()
        n = len(self.stack)
        if n < 2:
            raise ReError(ERR_UNEXPECTED_PAREN, self.whole)
        re1, re2 = self.stack[n - 1], self.stack[n - 2]
        del self.stack[n - 2:]
        if re2.op != _LEFT_PAREN:
            raise ReError(ERR_UNEXPECTED_PAREN, self.whole)
        self.push(Node(CAPTURE, subs=[re1], cap=re2.cap))

    def repeat(self, op: int, mn: int, mx: int, text: str) -> None:
        if not self.stack or self.stack[-1].op >= _LEFT_PAREN:
            raise ReError(ERR_MISSING_REPEAT_ARGUMENT, text)
        sub = self.stack[-1]
        n = self.check(Node(op, subs=[sub], min=mn, max=mx))
        self.stack[-1] = n
        if op == REPEAT and (mn >= 2 or mx >= 2) and not _repeat_is_valid(n, MAX_REPEAT):
            raise ReError(ERR_INVALID_REPEAT_SIZE, text)

    # -- the scan --

    def parse(self) -> Node:
        t = self.whole
        i = 0
        while i < len(t):
            c = t[i]
            if c == "(":
                self.num_cap += 1
                self.stack.append(Node(_LEFT_PAREN, cap=self.num_cap))
                i += 1
            elif c == "|":
                self.vertical_bar()
                i += 1
            elif c == ")":
                self.right_paren()
                i += 1
            elif c == "^":
                self.push(Node(BEGIN_LINE))
                i += 1
            elif c == "$":
                self.push(Node(END_LINE))
                i += 1
            elif c == ".":
                self.push(Node(ANY_NOT_NL))
                i += 1
            elif c == "[":
                i = self.parse_class(i)
            elif c in "*+?":
                self.repeat({"*": STAR, "+": PLUS, "?": QUEST}[c], 0, 0, c)
                i += 1
            elif c == "{":
                rep = _parse_repeat(t, i)
                if rep is None:
                    self.literal(ord("{"))
                    i += 1
                    continue
                mn, mx, after = rep
                text = t[i:after]
                if mn < 0 or mn > MAX_REPEAT or mx > MAX_REPEAT or (mx >= 0 and mn > mx):
                    raise ReError(ERR_INVALID_REPEAT_SIZE, text)
                self.repeat(REPEAT, mn, mx, text)
                i = after
            elif c == "\\":
                r, i = _parse_escape(t, i)
                self.literal(r)
            else:
                self.literal(ord(c))
                i += 1
        self._concat()
        if self._swap_vertical_bar():
            self.stack.pop()
        self._alternate()
        if len(self.stack) != 1:
            raise ReError(ERR_MISSING_PAREN, self.whole)
        return self.stack[0]

    def parse_class(self, start: int) -> int:
        """Go's parseClass at t[start] == '[': answers the index after it."""
        t = self.whole
        whole_end = len(t)
        i = start + 1
        ranges: List[int] = []
        negate = False
        if i < whole_end and t[i] == "^":
            negate = True
            i += 1
            ranges += [0x0A, 0x0A]  # a negated class never matches a newline
        first = True
        while i >= whole_end or t[i] != "]" or first:
            if i < whole_end and t[i] == "-" and not first and (i + 1 >= whole_end or t[i + 1] != "]"):
                raise ReError(ERR_INVALID_CHAR_RANGE, t[i:i + 2])
            first = False
            if i + 2 < whole_end + 1 and t.startswith("[:", i) and whole_end - i > 2:
                end = t.find(":]", i + 2)
                if end >= 0:
                    name = t[i:end + 2]
                    g = _POSIX.get(name)
                    if g is None:
                        raise ReError(ERR_INVALID_CHAR_RANGE, name)
                    if g and g[0] == -1:
                        ranges += _negate(_clean(g[1:]))
                    else:
                        ranges += g
                    i = end + 2
                    continue
            rng_start = i
            lo, i = self.class_char(i, start)
            hi = lo
            if i + 1 < whole_end and t[i] == "-" and t[i + 1] != "]":
                hi, i = self.class_char(i + 1, start)
                if hi < lo:
                    raise ReError(ERR_INVALID_CHAR_RANGE, t[rng_start:i])
            ranges += [lo, hi]
        cleaned = _clean(ranges)
        if negate:
            cleaned = _negate(cleaned)
        self.push(Node(CLASS, runes=cleaned))
        return i + 1

    def class_char(self, i: int, class_start: int) -> Tuple[int, int]:
        t = self.whole
        if i >= len(t):
            raise ReError(ERR_MISSING_BRACKET, t[class_start:])
        if t[i] == "\\":
            return _parse_escape(t, i)
        return ord(t[i]), i + 1


def _collapse(subs: List[Node], op: int) -> Node:
    if len(subs) == 1:
        return subs[0]
    flat: List[Node] = []
    for s in subs:
        if s.op == op:
            flat += s.subs
        else:
            flat.append(s)
    if op == CONCAT:
        # adjacent literals are one literal string (Go's maybeConcat)
        merged: List[Node] = []
        for s in flat:
            if s.op == LITERAL and merged and merged[-1].op == LITERAL:
                merged[-1] = Node(LITERAL, runes=merged[-1].runes + s.runes)
            else:
                merged.append(s)
        if len(merged) == 1:
            return merged[0]
        flat = merged
    return Node(op, subs=flat)


def _parse_repeat(t: str, i: int) -> Optional[Tuple[int, int, int]]:
    """Go's parseRepeat at t[i] == '{': (min, max, index after) or None
    when it is not one (the brace is then a literal); max -1 = no upper
    bound, min -1 = a number too large."""

    def parse_int(j: int) -> Optional[Tuple[int, int]]:
        if j >= len(t) or not ("0" <= t[j] <= "9"):
            return None
        if j + 1 < len(t) and t[j] == "0" and "0" <= t[j + 1] <= "9":
            return None  # a leading zero
        k = j
        while k < len(t) and "0" <= t[k] <= "9":
            k += 1
        n = 0
        for d in t[j:k]:
            if n >= 100000000:
                n = -1
                break
            n = n * 10 + ord(d) - 48
        return n, k

    p = parse_int(i + 1)
    if p is None:
        return None
    mn, j = p
    if j >= len(t):
        return None
    if t[j] != ",":
        mx = mn
    else:
        j += 1
        if j >= len(t):
            return None
        if t[j] == "}":
            mx = -1
        else:
            q = parse_int(j)
            if q is None:
                return None
            mx, j = q
            if mx < 0:
                mn = -1
    if j >= len(t) or t[j] != "}":
        return None
    return mn, mx, j + 1


def _parse_escape(t: str, i: int) -> Tuple[int, int]:
    """Go's parseEscape at t[i] == '\\\\': the rune and the index after."""
    if i + 1 >= len(t):
        raise ReError(ERR_TRAILING_BACKSLASH, "")
    c = t[i + 1]
    j = i + 2
    if ord(c) < 0x80 and not _is_alnum(c):
        return ord(c), j
    if c in "1234567" and (j >= len(t) or not ("0" <= t[j] <= "7")):
        raise ReError(ERR_INVALID_ESCAPE, t[i:j])
    if c in "01234567":
        r = ord(c) - 48
        for _ in range(2):
            if j < len(t) and "0" <= t[j] <= "7":
                r = r * 8 + ord(t[j]) - 48
                j += 1
        return r, j
    if c == "x":
        if j >= len(t):
            raise ReError(ERR_INVALID_ESCAPE, t[i:j])
        if t[j] == "{":
            k = t.find("}", j)
            digits = t[j + 1:k] if k >= 0 else ""
            if k < 0 or not digits or any(d not in "0123456789abcdefABCDEF" for d in digits) or int(digits, 16) > MAX_RUNE:
                raise ReError(ERR_INVALID_ESCAPE, t[i:k + 1 if k >= 0 else len(t)])
            return int(digits, 16), k + 1
        two = t[j:j + 2]
        if len(two) < 2 or any(d not in "0123456789abcdefABCDEF" for d in two):
            raise ReError(ERR_INVALID_ESCAPE, t[i:j + len(two)])
        return int(two, 16), j + 2
    simple = {"a": 7, "f": 12, "n": 10, "r": 13, "t": 9, "v": 11}
    if c in simple:
        return simple[c], j
    raise ReError(ERR_INVALID_ESCAPE, t[i:j])


def _repeat_is_valid(n: Node, limit: int) -> bool:
    """Go's repeatIsValid: the nested repetitions of n make at most limit
    copies of the innermost thing."""
    stack = [(n, limit)]
    while stack:
        x, lim = stack.pop()
        if x.op == REPEAT:
            m = x.max
            if m == 0:
                continue
            if m < 0:
                m = x.min
            if m > lim:
                return False
            if m > 0:
                lim //= m
        stack.extend((sub, lim) for sub in x.subs)
    return True


def _size(n: Node) -> int:
    """Go's calcSize: the compiled program's size, estimated."""
    if n.op == LITERAL:
        size = len(n.runes)
    elif n.op in (CAPTURE, STAR):
        size = 2 + _size(n.subs[0])
    elif n.op in (PLUS, QUEST):
        size = 1 + _size(n.subs[0])
    elif n.op == CONCAT:
        size = sum(_size(s) for s in n.subs)
    elif n.op == ALTERNATE:
        size = sum(_size(s) for s in n.subs) + max(0, len(n.subs) - 1)
    elif n.op == REPEAT:
        sub = _size(n.subs[0])
        if n.max == -1:
            size = 2 + sub if n.min == 0 else 1 + n.min * sub
        else:
            size = n.max * sub + (n.max - n.min)
    else:
        size = 0
    return max(1, size)


# -- simplify (Go's Simplify) --


def _simplify1(op: int, sub: Node, orig: Optional[Node]) -> Node:
    if sub.op == EMPTY:
        return sub
    if op == sub.op:
        return sub
    if orig is not None and orig.op == op and orig.subs[0] is sub:
        return orig
    return Node(op, subs=[sub])


def _simplify(n: Node) -> Node:
    if n.op in (CAPTURE, CONCAT, ALTERNATE):
        subs = [_simplify(s) for s in n.subs]
        if all(a is b for a, b in zip(subs, n.subs)):
            return n
        return Node(n.op, subs=subs, runes=n.runes, cap=n.cap, min=n.min, max=n.max)
    if n.op in (STAR, PLUS, QUEST):
        return _simplify1(n.op, _simplify(n.subs[0]), n)
    if n.op == REPEAT:
        if n.min == 0 and n.max == 0:
            return Node(EMPTY)
        sub = _simplify(n.subs[0])
        if n.max == -1:
            if n.min == 0:
                return _simplify1(STAR, sub, None)
            if n.min == 1:
                return _simplify1(PLUS, sub, None)
            return Node(CONCAT, subs=[sub] * (n.min - 1) + [_simplify1(PLUS, sub, None)])
        if n.min == 1 and n.max == 1:
            return sub
        prefix: Optional[Node] = None
        if n.min > 0:
            prefix = Node(CONCAT, subs=[sub] * n.min)
        if n.max > n.min:
            suffix = _simplify1(QUEST, sub, None)
            for _ in range(n.min + 1, n.max):
                suffix = _simplify1(QUEST, Node(CONCAT, subs=[sub, suffix]), None)
            if prefix is None:
                return suffix
            prefix.subs.append(suffix)
        if prefix is not None:
            return prefix
        return Node(NO_MATCH)
    return n


# -- compile (Go's syntax.Compile) --

I_FAIL, I_ALT, I_CAP, I_EMPTY, I_MATCH, I_NOP, I_RUNE = 0, 1, 2, 3, 4, 5, 6
EMPTY_BEGIN_LINE, EMPTY_END_LINE = 1, 2
_ANY_NOT_NL = [0, 0x09, 0x0B, MAX_RUNE]

_Frag = Tuple[int, List[Tuple[int, int]], bool]  # first instruction, patch list, nullable


class Prog:
    """A compiled program (Go's syntax.Prog)."""

    def __init__(self) -> None:
        self.op: List[int] = [I_FAIL]
        self.out: List[int] = [0]
        self.arg: List[int] = [0]
        self.ranges: List[List[int]] = [[]]
        self.start = 0

    def inst(self, op: int) -> int:
        self.op.append(op)
        self.out.append(0)
        self.arg.append(0)
        self.ranges.append([])
        return len(self.op) - 1

    def patch(self, pl: List[Tuple[int, int]], target: int) -> None:
        for i, which in pl:
            if which == 0:
                self.out[i] = target
            else:
                self.arg[i] = target


def _compile(root: Node) -> Prog:
    p = Prog()

    def nop() -> _Frag:
        i = p.inst(I_NOP)
        return i, [(i, 0)], True

    def cap(arg: int) -> _Frag:
        i = p.inst(I_CAP)
        p.arg[i] = arg
        return i, [(i, 0)], True

    def cat(f1: _Frag, f2: _Frag) -> _Frag:
        if f1[0] == 0 or f2[0] == 0:
            return 0, [], False
        p.patch(f1[1], f2[0])
        return f1[0], f2[1], f1[2] and f2[2]

    def alt(f1: _Frag, f2: _Frag) -> _Frag:
        if f1[0] == 0:
            return f2
        if f2[0] == 0:
            return f1
        i = p.inst(I_ALT)
        p.out[i] = f1[0]
        p.arg[i] = f2[0]
        return i, f1[1] + f2[1], f1[2] or f2[2]

    def quest(f1: _Frag) -> _Frag:
        i = p.inst(I_ALT)
        p.out[i] = f1[0]
        return i, [(i, 1)] + f1[1], True

    def loop(f1: _Frag) -> _Frag:
        i = p.inst(I_ALT)
        p.out[i] = f1[0]
        p.patch(f1[1], i)
        return i, [(i, 1)], True

    def plus(f1: _Frag) -> _Frag:
        return f1[0], loop(f1)[1], f1[2]

    def star(f1: _Frag) -> _Frag:
        if f1[2]:
            # (f1+)? keeps the priority order right (golang.org/issue/46123)
            return quest(plus(f1))
        return loop(f1)

    def rune(ranges: List[int]) -> _Frag:
        i = p.inst(I_RUNE)
        p.ranges[i] = ranges
        return i, [(i, 0)], False

    def empty(flag: int) -> _Frag:
        i = p.inst(I_EMPTY)
        p.arg[i] = flag
        return i, [(i, 0)], True

    def c(n: Node) -> _Frag:
        op = n.op
        if op == NO_MATCH:
            return 0, [], False
        if op == EMPTY:
            return nop()
        if op == LITERAL:
            f: _Frag = (0, [], False)
            for j, r in enumerate(n.runes):
                f1 = rune([r, r])
                f = f1 if j == 0 else cat(f, f1)
            return f
        if op == CLASS:
            return rune(n.runes)
        if op == ANY_NOT_NL:
            return rune(_ANY_NOT_NL)
        if op == BEGIN_LINE:
            return empty(EMPTY_BEGIN_LINE)
        if op == END_LINE:
            return empty(EMPTY_END_LINE)
        if op == CAPTURE:
            bra = cap(n.cap << 1)
            sub = c(n.subs[0])
            ket = cap(n.cap << 1 | 1)
            return cat(cat(bra, sub), ket)
        if op == STAR:
            return star(c(n.subs[0]))
        if op == PLUS:
            return plus(c(n.subs[0]))
        if op == QUEST:
            return quest(c(n.subs[0]))
        if op == CONCAT:
            if not n.subs:
                return nop()
            f = c(n.subs[0])
            for s in n.subs[1:]:
                f = cat(f, c(s))
            return f
        if op == ALTERNATE:
            f = (0, [], False)
            for s in n.subs:
                f = alt(f, c(s))
            return f
        raise ValueError("posixre: unhandled operator %d" % op)

    f = c(root)
    m = p.inst(I_MATCH)
    p.patch(f[1], m)
    p.start = f[0]
    return p


# -- the search helpers: where a match may start, what it must contain --


def _first(n: Node) -> Tuple[List[int], bool]:
    """The ranges of runes a match of n can start with, and whether n can
    match without consuming one (an anchor counts as that)."""
    op = n.op
    if op == LITERAL:
        return [n.runes[0], n.runes[0]], False
    if op == CLASS:
        return list(n.runes), False
    if op == ANY_NOT_NL:
        return list(_ANY_NOT_NL), False
    if op in (EMPTY, BEGIN_LINE, END_LINE):
        return [], True
    if op == NO_MATCH:
        return [], False
    if op in (CAPTURE, PLUS):
        return _first(n.subs[0])
    if op in (STAR, QUEST):
        return _first(n.subs[0])[0], True
    if op == CONCAT:
        out: List[int] = []
        for s in n.subs:
            r, nullable = _first(s)
            out += r
            if not nullable:
                return _clean(out), False
        return _clean(out), True
    if op == ALTERNATE:
        out = []
        any_null = False
        for s in n.subs:
            r, nullable = _first(s)
            out += r
            any_null = any_null or nullable
        return _clean(out), any_null
    return [0, MAX_RUNE], True


def _required(n: Node) -> str:
    """A literal every match of n contains ("" = none known)."""
    while n.op == CAPTURE:
        n = n.subs[0]
    if n.op == LITERAL:
        return "".join(chr(r) for r in n.runes)
    best = ""
    if n.op == CONCAT:
        for s in n.subs:
            while s.op == CAPTURE:
                s = s.subs[0]
            if s.op == LITERAL and len(s.runes) > len(best):
                best = "".join(chr(r) for r in s.runes)
    return best


def _class_pattern(ranges: List[int]) -> str:
    parts = []
    for i in range(0, len(ranges), 2):
        lo, hi = ranges[i], ranges[i + 1]
        parts.append("\\U%08x" % lo if lo == hi else "\\U%08x-\\U%08x" % (lo, hi))
    return "[" + "".join(parts) + "]"


class Regexp:
    """A compiled POSIX regexp (``compile_posix``)."""

    def __init__(self, expr: str, prog: Prog, num_cap: int, first: List[int], nullable: bool, required: str) -> None:
        self.expr = expr
        self.prog = prog
        self.num_cap = num_cap
        self._required = required
        # where a match may start: a compiled class of its first runes
        # (None = anywhere)
        self._starts: Optional["_re.Pattern[str]"] = None
        if not nullable:
            self._starts = _re.compile(_class_pattern(first) if first else "(?!)", _re.ASCII)

    def num_subexp(self) -> int:
        """The number of parenthesized groups (Go's NumSubexp)."""
        return self.num_cap

    def match_string(self, s: str) -> bool:
        """Whether s holds a match (Go's MatchString)."""
        return self._search(s, 0) is not None

    def find_all_submatch_index(self, s: str, n: int) -> List[List[int]]:
        """Go's FindAllStringSubmatchIndex(s, n) in code point offsets: up to
        n matches (n < 0: all), each [start, end, group starts and ends …],
        -1 for a group that did not take part. An empty match right after a
        match is not one, as Go's."""
        out: List[List[int]] = []
        if n < 0:
            n = len(s) + 1
        pos, prev_end = 0, -1
        end = len(s)
        while len(out) < n and pos <= end:
            m = self._search(s, pos)
            if m is None:
                break
            accept = True
            if m[1] == pos:
                if m[0] == prev_end:
                    accept = False
                pos = pos + 1 if pos < end else end + 1
            else:
                pos = m[1]
            prev_end = m[1]
            if accept:
                out.append(m)
        return out

    def _search(self, s: str, pos: int) -> Optional[List[int]]:
        """The leftmost-longest match starting at pos or later."""
        if self.prog.start == 0:
            return None
        if self._required and s.find(self._required, pos) < 0:
            return None
        visited: Set[int] = set()
        width = len(s) + 1
        start = pos
        while start <= len(s):
            if self._starts is not None:
                m = self._starts.search(s, start)
                if m is None:
                    return None
                start = m.start()
            caps = self._backtrack(s, start, visited, width)
            if caps is not None:
                return caps
            start += 1
        return None

    def _backtrack(self, s: str, start: int, visited: Set[int], width: int) -> Optional[List[int]]:
        """Go's tryBacktrack in longest mode from start: the longest match's
        captures (the first path in priority order among the longest)."""
        prog = self.prog
        ops, outs, args, rngs = prog.op, prog.out, prog.arg, prog.ranges
        ncap = 2 * (self.num_cap + 1)
        cap = [-1] * ncap
        cap[0] = start
        best: Optional[List[int]] = None
        best_end = -1
        end = len(s)
        # jobs: (pc, pos, arg) — arg 0: a fresh step; an alternative's arg 1:
        # its second branch; a capture's arg: the slot's old position + 2,
        # restored on the way back (Go's arg=true jobs)
        jobs: List[Tuple[int, int, int]] = []
        key = prog.start * width + start
        if key in visited:
            return None
        visited.add(key)
        jobs.append((prog.start, start, 0))
        while jobs:
            pc, p, arg = jobs.pop()
            first_step = True
            while True:
                if not first_step:
                    key = pc * width + p
                    if key in visited:
                        break
                    visited.add(key)
                first_step = False
                op = ops[pc]
                if op == I_ALT:
                    if arg:
                        arg = 0
                        pc = args[pc]
                        continue
                    jobs.append((pc, p, 1))
                    pc = outs[pc]
                    continue
                if op == I_RUNE:
                    if p >= end:
                        break
                    r = ord(s[p])
                    rr = rngs[pc]
                    k = bisect.bisect_right(rr, r)
                    if not (k & 1) and not (k > 0 and rr[k - 1] == r):
                        break
                    p += 1
                    pc = outs[pc]
                    continue
                if op == I_CAP:
                    slot = args[pc]
                    if arg:
                        cap[slot] = arg - 2
                        break
                    if slot < ncap:
                        jobs.append((pc, p, cap[slot] + 2))
                        cap[slot] = p
                    pc = outs[pc]
                    continue
                if op == I_EMPTY:
                    flag = args[pc]
                    if flag == EMPTY_BEGIN_LINE:
                        if not (p == 0 or s[p - 1] == "\n"):
                            break
                    elif flag == EMPTY_END_LINE:
                        if not (p == end or s[p] == "\n"):
                            break
                    pc = outs[pc]
                    continue
                if op == I_NOP:
                    pc = outs[pc]
                    continue
                if op == I_MATCH:
                    cap[1] = p
                    if best is None or (p > 0 and p > best_end):
                        best = list(cap)
                        best_end = p
                    if p == end:
                        return best
                    break
                break  # I_FAIL
            # continue with the next job
        return best


def compile_posix(expr: str) -> Regexp:
    """Go's regexp.CompilePOSIX(expr), or ReError with Go's message."""
    root = _Parser(expr).parse()
    num_cap = _count_caps(root)
    # the tree is at most MAX_HEIGHT deep, its simplified form at most
    # about twice that: the walks below recurse that deep
    limit = sys.getrecursionlimit()
    try:
        sys.setrecursionlimit(max(limit, 8 * MAX_HEIGHT))
        if _size(root) > MAX_SIZE:
            raise ReError(ERR_LARGE, expr)
        simple = _simplify(root)
        prog = _compile(simple)
        first, nullable = _first(simple)
    finally:
        sys.setrecursionlimit(limit)
    return Regexp(expr, prog, num_cap, first, nullable, _required(simple))


def _count_caps(n: Node) -> int:
    best = 0
    stack = [n]
    while stack:
        x = stack.pop()
        if x.op == CAPTURE and x.cap > best:
            best = x.cap
        stack.extend(x.subs)
    return best
