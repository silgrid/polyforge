"""Reading failure units out of a command's output, with their failure
signatures — the eight output shapes a bar reads.

Only what can be parsed is parsed: Go (``--- FAIL: X``, ``FAIL\\t<pkg>``,
``ok\\t<pkg>``), the Go e2e harness's ``FAIL <Test>`` lines and its
trailer, pytest node ids, unittest's ``FAIL: test_x (mod.Class)``, Rust
libtest's ``test x ... FAILED``, the ``PASS: <title>`` / ``FAIL: <title>``
marker lines of a node ``.mjs`` suite, and an aggregate e2e runner's
per-suite blocks (``===== PASS|FAIL <suite> (<n>s) =====``), failure lines
(``FAIL e2e/<suite>: <scenario> — <message>``) and the ``re-run: <cmd>``
line it prints after them; the eighth, a declared JUnit report, is
``pfstep.junit``'s. A Go package whose binary did not finish normally (a
panic, a timeout, an os.Exit, -failfast) is a failing unit of its own
beside its failed tests: tests after the stop never ran. Every failed unit
carries its failure signature: every assertion line of the failure WITH
its body (a multi-line diff, a testify report), source positions
normalized, the repo root and temp dirs as tokens, message content kept.
A failed unit whose failure lines (an aggregate runner suite's: its block
too, its ``ABORT`` line included) show Chromium's renderer crash
(``Target crashed`` / ``Page crashed``) is marked crashed.

Ported from the Go step commands' units.go (and outputLines,
normalizeOutput and unitPassed beside it).
"""

import os
import re
from dataclasses import dataclass
from typing import Callable, Dict, List, Optional, Sequence, Set, Tuple

from . import gostr
from .shellwords import shell_words

# The unit kinds.
KIND_GO = "go"  # a top-level Go test (also from the harness's FAIL lines)
KIND_GO_PKG = "go-pkg"  # a Go package's own result
KIND_PYTEST = "pytest"
KIND_UNITTEST = "unittest"
KIND_LIBTEST = "libtest"
KIND_MJS = "mjs"
KIND_E2E_SUITE = "e2e-suite"  # one suite of an aggregate e2e runner
KIND_COMMAND = "command"  # an unlocatable failure: the whole command
KIND_JUNIT = "junit"  # a failed testcase of a declared JUnit report (pfstep.junit)

# A signature or a detail string is cut at this many bytes.
MAX_TEXT_BYTES = 4 << 10

# A log line is cut at this many bytes (the rest dropped, never a failure).
MAX_LINE_BYTES = 64 << 10

_A = re.ASCII
_S = gostr.SPACE  # RE2's \s
_NS = r"[^\t\n\f\r ]"  # RE2's \S


@dataclass
class Unit:
    """One parsed result."""

    kind: str
    name: str
    pkg: str = ""
    failed: bool = False
    # build_failed: a Go package that did not build (never flaky)
    build_failed: bool = False
    # marker: the output line that identified it
    marker: str = ""
    # signature: the failure's normalized assertion lines (failures)
    signature: str = ""
    # printed_rerun: an aggregate runner's own `re-run:` line for this
    # suite (KIND_E2E_SUITE only)
    printed_rerun: str = ""
    # crashed: its failure output shows a browser crash (BROWSER_CRASH)
    crashed: bool = False


_GO_FAIL = re.compile(r"--- FAIL: (" + _NS + r"+) \(", _A)
_GO_PASS = re.compile(r"--- PASS: (" + _NS + r"+) \(", _A)
_GO_PKG_FAIL = re.compile(r"FAIL\t(" + _NS + r"+)(?:\t| \[|\Z)", _A)
_GO_PKG_OK = re.compile(r"ok" + _S + r"+(" + _NS + r"+)(?:" + _S + r"|\Z)", _A)
_HARNESS_FAIL = re.compile(r"FAIL (Test" + _NS + r"*)\Z", _A)
_GO_RUN_HEAD = re.compile(r"=== (?:RUN|CONT|NAME|PAUSE)" + _S + r"+(" + _NS + r"+)", _A)
_GO_ASSERT = re.compile(_S + r"*(?:[\w./-]+\.go:\d+: |panic: |Error:|Error Trace:)", _A)
_GO_FRAME = re.compile(_S + r"+" + _NS + r"+\(.*\)\Z", _A)
_HEX_ARG = re.compile(r"0x[0-9a-f]+", _A)
# the e2e harness trailer: ran/listed, the E2E_SKIP exclusions and the
# failed tests it names
_HARNESS_SUMMARY = re.compile(
    r"FAIL\te2e\t(\d+)/(\d+) tests ran(?:, (\d+) excluded by E2E_SKIP)?, failed: ([^\t]*)", _A
)

_PYTEST_SUMMARY = re.compile(r"(FAILED|ERROR) (" + _NS + r"+::" + _NS + r"+?)(?: - (.*))?\Z", _A)
_PYTEST_VERBOSE = re.compile(r"(" + _NS + r"+::" + _NS + r"+) (PASSED|FAILED|ERROR)\b", _A)
PYTEST_SECTION = re.compile(r"_{3,} (.+?) _{3,}\Z", _A)

_UNITTEST_FAIL = re.compile(r"(FAIL|ERROR): (\w+) \(([\w.]+)\)\Z", _A)
_UNITTEST_PASS = re.compile(r"(\w+) \(([\w.]+)\)(?: " + _NS + r".*)? \.\.\. ok\Z", _A)
_UNITTEST_ERR = re.compile(r"\w*(?:Error|Exception|Failure)\b", _A)

_LIBTEST = re.compile(r"test (" + _NS + r"+) \.\.\. (ok|FAILED)\Z", _A)
_LIBTEST_STDOUT = re.compile(r"---- (" + _NS + r"+) stdout ----\Z", _A)

_MJS = re.compile(r"(PASS|FAIL): (.+)\Z", _A)

# the aggregate e2e runner: a suite's output block header, its failure
# line in the failures section, the suite's re-run after them, and the
# section headers that end a suite block
_SUITE_BLOCK = re.compile(r"===== (PASS|FAIL) (" + _NS + r"+) \(" + _NS + r"+\) =====\Z", _A)
_SUITE_FAIL = re.compile(r"FAIL (e2e/[^\t\n\f\r :]+): (.+)\Z", _A)
_SUITE_RERUN = re.compile(r"re-run: (.+)\Z", _A)
_SUITE_LOAD = re.compile(r"host loadavg ", _A)
_SUITE_SECTION = re.compile(r"===== (?:failures|summary) =====\Z", _A)

# Chromium's renderer crash as Playwright reports it
BROWSER_CRASH = re.compile(r"\b(?:Target|Page) crashed\b", _A)

# a real source position: a file with a known source extension
_SRC_POS = re.compile(r"\b([\w./-]+\.(?:go|py|rs|js|mjs|ts)):\d+(?::\d+)?", _A)
_PY_LINE = re.compile(r"\bline \d+\b", _A)
# a Go t.TempDir() under a run token: `<tmp>/<TestName><random>/<NNN>`
_TEMP_DIR = re.compile(r"(<tmp>|<root>)/([A-Za-z_]\w*?)\d+/(\d+)\b", _A)

_PYTEST_PASSED = re.compile(r"\b[1-9][0-9]* passed\b", _A)
_UNITTEST_RAN = re.compile(r"Ran [1-9][0-9]* tests?\b", _A)


def cap_text(s: str) -> str:
    """s cut to MAX_TEXT_BYTES on a rune boundary, the cut marked."""
    return gostr.cap_text(s, MAX_TEXT_BYTES)


def norm_sig(s: str) -> str:
    """One failure line normalized for the signature comparison: white
    space collapsed, real source positions (``x_test.go:12``, a traceback's
    ``line 7``) read as N, a Go t.TempDir() path's random suffix dropped —
    nothing else: a message's own content stays."""
    s = " ".join(gostr.fields(s))
    s = _TEMP_DIR.sub(r"\g<1>/\g<2>/\g<3>", s)
    s = _SRC_POS.sub(r"\g<1>:N", s)
    return cap_text(_PY_LINE.sub("line N", s))


def parse_units(lines: Sequence[str], failfast: bool = False) -> List[Unit]:
    """Every unit one output carries. A Go test belongs to the package
    whose summary line (``ok``/``FAIL\\t<pkg>``) follows it: Go tests are
    collected per package block and keyed by package and name, so a test
    name two packages share stays two units; inside one block a test named
    twice is one unit. failfast: the go test run stops at its first
    failure, so a failing package's later tests never ran."""
    out: List[Unit] = []
    index: Dict[str, Unit] = {}

    def merge(prev: Unit, u: Unit) -> Unit:
        if u.failed and not prev.failed:
            prev.failed, prev.marker = True, u.marker
        if prev.signature == "":
            prev.signature = u.signature
        prev.crashed = prev.crashed or u.crashed
        return prev

    def add(u: Unit) -> Unit:
        u.crashed = u.crashed or (u.failed and browser_crashed([u.marker, u.signature]))
        k = u.kind + "\x00" + u.pkg + "\x00" + u.name
        prev = index.get(k)
        if prev is not None:
            return merge(prev, u)
        index[k] = u
        out.append(u)
        return u

    # the current package block's Go tests, by name
    pending: List[Unit] = []
    pending_idx: Dict[str, Unit] = {}

    def add_pending(u: Unit) -> None:
        prev = pending_idx.get(u.name)
        if prev is not None:
            merge(prev, u)
            return
        pending_idx[u.name] = u
        pending.append(u)

    def flush(pkg: str) -> bool:
        any_failed = False
        for u in pending:
            u.pkg = pkg
            any_failed = any_failed or u.failed
            add(u)
        pending.clear()
        pending_idx.clear()
        return any_failed

    # the current package block's completion signals: the testing
    # framework's bare `FAIL` line closes a binary that finished normally;
    # a top-level panic (a crash, a timeout) never does
    bare_fail, panic_line = False, ""

    def settle(pkg: str, failed: bool, build_failed: bool, marker: str) -> None:
        nonlocal bare_fail, panic_line
        any_failed = flush(pkg)
        stopped = failed and (not bare_fail or panic_line != "" or failfast)
        if failed and (not any_failed or stopped):
            why = marker
            if any_failed:
                why = "the package's test binary did not finish normally (" + marker + ")"
            add(Unit(kind=KIND_GO_PKG, name=pkg, failed=True, build_failed=build_failed, marker=why,
                     signature=norm_sig(panic_line)))
        elif not failed:
            add(Unit(kind=KIND_GO_PKG, name=pkg, marker=marker))
        bare_fail, panic_line = False, ""

    # the e2e harness trailer: every test it names failing is a failing
    # unit, and a run that lost tests (ran + excluded < listed) fails on its own
    def settle_harness(m: "re.Match[str]", marker: str) -> None:
        nonlocal bare_fail, panic_line
        flush("e2e")
        for name in gostr.fields(m.group(4)):
            u = index.get(KIND_GO + "\x00e2e\x00" + name)
            if u is None or not u.failed:
                add(Unit(kind=KIND_GO, name=name, pkg="e2e", failed=True, marker=marker))
        ran = int(m.group(1))
        listed = int(m.group(2))
        excluded = int(m.group(3)) if m.group(3) else 0
        if ran + excluded != listed:
            add(Unit(kind=KIND_GO_PKG, name="e2e", failed=True, marker=marker))
        bare_fail, panic_line = False, ""

    # the aggregate e2e runner: in_suite_block = inside one suite's echoed
    # output, whose own markers the suite unit stands for; fail_suite = the
    # suite whose failure lines a following `re-run:` line belongs to;
    # crashed_blocks = the suites whose FAIL block shows a browser crash
    in_suite_block, fail_suite = False, ""
    block_suite = ""
    crashed_blocks: Set[str] = set()
    for i, line in enumerate(lines):
        m = _SUITE_BLOCK.match(line)
        if m:
            in_suite_block, fail_suite = True, ""
            suite = m.group(2)
            if suite.startswith("e2e/"):
                suite = suite[len("e2e/"):]
            block_suite = "e2e/" + suite if m.group(1) == "FAIL" else ""
            if m.group(1) == "PASS":
                add(Unit(kind=KIND_E2E_SUITE, name="e2e/" + suite, marker=line))
            continue
        if _SUITE_SECTION.match(line):
            in_suite_block, fail_suite = False, ""
            continue
        if in_suite_block:
            if block_suite != "" and browser_crashed([line]):
                crashed_blocks.add(block_suite)
            continue
        m = _SUITE_FAIL.match(line)
        if m:
            u = add(Unit(kind=KIND_E2E_SUITE, name=m.group(1), failed=True, marker=line,
                         crashed=m.group(1) in crashed_blocks))
            sig = norm_sig(m.group(2))
            if u.signature != "":
                sig = u.signature + " | " + sig
            u.signature, fail_suite = cap_text(sig), m.group(1)
            continue
        if fail_suite != "":
            m = _SUITE_RERUN.match(line)
            if m:
                su = index.get(KIND_E2E_SUITE + "\x00\x00" + fail_suite)
                if su is not None and names_suite(m.group(1), fail_suite):
                    su.printed_rerun = m.group(1)
                fail_suite = ""
                continue
            if _SUITE_LOAD.match(line):
                # the runner's host-load line sits between a suite's failure
                # lines and its re-run: it keeps the suite
                continue
        fail_suite = ""
        if line == "FAIL":
            bare_fail = True
        if line.startswith("panic: ") and panic_line == "":
            panic_line = line
        m = _HARNESS_SUMMARY.match(line)
        if m:
            settle_harness(m, line)
            continue
        m = _GO_FAIL.match(line)
        if m:
            name = m.group(1)
            add_pending(Unit(kind=KIND_GO, name=name, failed=True, marker=line, signature=go_signature(lines, i, name)))
            continue
        m = _GO_PASS.match(line)
        if m:
            add_pending(Unit(kind=KIND_GO, name=m.group(1), marker=line))
            continue
        m = _HARNESS_FAIL.match(line)
        if m:
            add_pending(Unit(kind=KIND_GO, name=m.group(1), failed=True, marker=line))
            continue
        m = _GO_PKG_FAIL.match(line)
        if m:
            settle(m.group(1), True, "[build failed]" in line or "[setup failed]" in line, line)
            continue
        m = _GO_PKG_OK.match(line)
        if m:
            settle(m.group(1), False, False, line)
            continue
        m = _PYTEST_SUMMARY.match(line)
        if m:
            sig = m.group(3) or ""
            if sig == "":
                sig = pytest_section(lines, m.group(2))
            add(Unit(kind=KIND_PYTEST, name=m.group(2), failed=True, marker=line, signature=norm_sig(sig)))
            continue
        m = _PYTEST_VERBOSE.match(line)
        if m:
            if m.group(2) == "PASSED":
                add(Unit(kind=KIND_PYTEST, name=m.group(1), marker=line))
            else:
                add(Unit(kind=KIND_PYTEST, name=m.group(1), failed=True, marker=line,
                         signature=norm_sig(pytest_section(lines, m.group(1)))))
            continue
        m = _UNITTEST_FAIL.match(line)
        if m:
            add(Unit(kind=KIND_UNITTEST, name=unittest_id(m.group(2), m.group(3)), failed=True, marker=line,
                     signature=unittest_signature(lines, i)))
            continue
        m = _UNITTEST_PASS.match(line)
        if m:
            add(Unit(kind=KIND_UNITTEST, name=unittest_id(m.group(1), m.group(2)), marker=line))
            continue
        m = _LIBTEST.match(line)
        if m:
            if m.group(2) == "ok":
                add(Unit(kind=KIND_LIBTEST, name=m.group(1), marker=line))
            else:
                add(Unit(kind=KIND_LIBTEST, name=m.group(1), failed=True, marker=line,
                         signature=libtest_signature(lines, m.group(1))))
            continue
        m = _MJS.match(line)
        if m:
            if m.group(1) == "PASS":
                add(Unit(kind=KIND_MJS, name=m.group(2), marker=line))
            else:
                add(Unit(kind=KIND_MJS, name=m.group(2), failed=True, marker=line, signature=mjs_signature(lines, i)))
    flush("")  # tests no summary line closed (a cut-off output)
    return out


def _indent_of(line: str) -> int:
    return len(line) - len(gostr.trim_left_space(line))


def go_signature(lines: Sequence[str], at: int, name: str) -> str:
    """Go test name's failure signature: every assertion-shaped line
    (``file.go:N: …``, ``panic: …``, testify's ``Error:``) the test and all
    its subtests printed, each WITH its body (the deeper indented lines
    under it), and a data race's report head and first frame — from the -v
    blocks of the test or a subtest and from the indented block under its
    ``--- FAIL:`` header — each line normalized, in order."""
    sig: List[str] = []

    def take(j: int, end: int) -> int:
        sig.append(norm_sig(lines[j]))
        ind = _indent_of(lines[j])
        k = j + 1
        while k < end:
            ln = lines[k]
            if gostr.trim_space(ln) == "" or _indent_of(ln) <= ind:
                break
            sig.append(norm_sig(ln))
            k += 1
        return k - 1

    cur = ""
    j = 0
    while j < at:
        ln = lines[j]
        m = _GO_RUN_HEAD.match(ln)
        if m:
            cur = m.group(1)
            j += 1
            continue
        if ln.startswith("--- ") or ln.startswith("ok ") or ln.startswith("FAIL"):
            cur = ""  # a top-level verdict or summary closes the block
            j += 1
            continue
        if cur != name and not cur.startswith(name + "/"):
            j += 1
            continue
        if ln == "WARNING: DATA RACE":
            sig.append(ln)
            k = j + 1
            while k < at and k < j + 6:
                if _GO_FRAME.match(lines[k]):
                    sig.append(norm_sig(_HEX_ARG.sub("0x?", lines[k])))
                    break
                k += 1
        elif _GO_ASSERT.match(ln):
            j = take(j, at)
        j += 1
    j = at + 1
    while j < len(lines):
        ln = lines[j]
        if gostr.trim_space(ln) == "":
            j += 1
            continue
        if ln[0] != " " and ln[0] != "\t" and not ln.startswith("panic: "):
            break
        if _GO_ASSERT.match(ln):
            j = take(j, len(lines))
        j += 1
    return cap_text(" | ".join(sig))


def pytest_section(lines: Sequence[str], node_id: str) -> str:
    """The first ``E `` line of a failing node's section."""
    name = node_id
    i = node_id.rfind("::")
    if i >= 0:
        name = node_id[i + 2:]
    for i, ln in enumerate(lines):
        m = PYTEST_SECTION.match(ln)
        if m is None or (m.group(1) != name and not m.group(1).endswith("." + name)):
            continue
        for x in lines[i + 1:]:
            if PYTEST_SECTION.match(x):
                break
            if x.startswith("E "):
                return gostr.trim_space(x[1:])
    return ""


def unittest_id(method: str, owner: str) -> str:
    """The dotted id of a unittest case: Python >= 3.11 prints the method
    inside the parentheses already."""
    if owner.endswith("." + method):
        return owner
    return owner + "." + method


def unittest_signature(lines: Sequence[str], at: int) -> str:
    """The exception line of the traceback block under a ``FAIL:`` /
    ``ERROR:`` header."""
    for ln in lines[at + 1:]:
        if ln.startswith("====="):
            break
        if _UNITTEST_ERR.match(ln):
            return norm_sig(ln)
    return ""


def libtest_signature(lines: Sequence[str], name: str) -> str:
    """The panic message of a failing Rust test."""
    for i, ln in enumerate(lines):
        m = _LIBTEST_STDOUT.match(ln)
        if m is None or m.group(1) != name:
            continue
        for j in range(i + 1, len(lines)):
            if _LIBTEST_STDOUT.match(lines[j]):
                break
            if "panicked at" in lines[j]:
                for k in range(j + 1, len(lines)):
                    t = gostr.trim_space(lines[k])
                    if t != "" and not _LIBTEST_STDOUT.match(lines[k]):
                        return norm_sig(t)
                return norm_sig(lines[j])
    return ""


def mjs_signature(lines: Sequence[str], at: int) -> str:
    """The first line after a suite's ``FAIL: <title>``."""
    for ln in lines[at + 1:]:
        t = gostr.trim_space(ln)
        if t == "":
            continue
        if _MJS.match(ln):
            return ""
        return norm_sig(t)
    return ""


def names_suite(cmd: str, suite: str) -> bool:
    """Whether a printed re-run command is one simple command whose last
    word is the suite (``x.mjs`` or ``e2e/x.mjs``)."""
    words, simple = shell_words(cmd)
    if not simple or not words:
        return False
    last = words[-1]
    return last == suite or "e2e/" + last == suite


def find_unit(us: Sequence[Unit], kind: str, name: str) -> Optional[Unit]:
    """The parsed unit of the given kind and name (None = absent)."""
    for u in us:
        if u.kind == kind and u.name == name:
            return u
    return None


def unit_passed(kind: str, name: str, exit_code: int, us: Sequence[Unit], lines: Sequence[str]) -> bool:
    """A targeted run judged as a pass of the unit: positive evidence of
    the unit passing is required (a run that matched nothing is no pass)."""
    got = find_unit(us, kind, name)
    if got is not None and got.failed:
        return False
    if kind in (KIND_GO, KIND_GO_PKG, KIND_LIBTEST):
        return exit_code == 0 and got is not None
    if kind == KIND_PYTEST:
        if exit_code != 0:
            return False
        if any(_PYTEST_PASSED.search(ln) for ln in lines):
            return True
        return got is not None
    if kind == KIND_UNITTEST:
        ran = any(_UNITTEST_RAN.match(ln) for ln in lines)
        ok = any(ln.startswith("OK") for ln in lines)
        return exit_code == 0 and ran and ok
    if kind in (KIND_MJS, KIND_E2E_SUITE):
        # the whole suite re-ran: it passes only as a whole
        return exit_code == 0 and got is not None
    return False


def browser_crashed(lines: Sequence[str]) -> bool:
    """Whether any of the lines shows a browser crash (BROWSER_CRASH)."""
    return any(BROWSER_CRASH.search(ln) for ln in lines)


def race_seen(lines: Sequence[str]) -> bool:
    """Whether an output reported a data race (only a re-run carrying
    -race can reproduce one)."""
    return any("WARNING: DATA RACE" in ln or "race detected during execution of test" in ln for ln in lines)


# ---- reading a command's log -------------------------------------------


def split_lines(raw: bytes) -> List[str]:
    """A log's lines as Go's scanLines reads them: split at \\n, trailing
    \\r\\n cut, each line capped at MAX_LINE_BYTES (the rest dropped)."""
    if not raw:
        return []
    parts = raw.split(b"\n")
    if raw.endswith(b"\n"):
        parts = parts[:-1]
    return [p.rstrip(b"\r\n")[:MAX_LINE_BYTES].decode("utf-8", "replace") for p in parts]


def read_lines(path: str) -> List[str]:
    """A log's lines ([] when it cannot be read)."""
    try:
        with open(path, "rb") as f:
            return split_lines(f.read())
    except OSError:
        return []


def log_tail(log_path: str) -> str:
    """The last few output lines of a command log (its two header lines
    skipped), joined for a one-line reason."""
    lines = read_lines(log_path)
    lines = lines[2:] if len(lines) > 2 else []
    keep = [gostr.trim_space(ln) for ln in lines if gostr.trim_space(ln) != ""]
    if len(keep) > 4:
        keep = keep[-4:]
    return " | ".join(keep) if keep else "(no output)"


def output_lines(path: str) -> List[str]:
    """A command log's output lines (its two header lines skipped)."""
    lines = read_lines(path)
    if len(lines) < 2:
        return []
    return lines[2:]


def temp_dir() -> str:
    """Go's os.TempDir(): $TMPDIR, else /tmp."""
    return os.environ.get("TMPDIR", "") or "/tmp"


def normalize_output(lines: Sequence[str], root: str, tmp: str) -> List[str]:
    """The lines with the paths that differ between two runs of the same
    test — the command's private TMPDIR, the tree it ran in (the task
    repo, or a base worktree) and the system temp dir — as tokens, so a
    failure signature compares the failure, not where it ran (Go's
    strings.Replacer: at each position the first pair in this order wins)."""
    pairs: List[Tuple[str, str]] = []
    for old, new in ((tmp, "<tmp>"), (root, "<root>"), (temp_dir(), "<tmp>")):
        if old != "" and old != "/":
            pairs.append((old, new))
    if not pairs:
        return list(lines)
    table: Dict[str, str] = {}
    for old, new in pairs:
        table.setdefault(old, new)
    pat = re.compile("|".join(re.escape(old) for old, _ in pairs))
    repl: Callable[["re.Match[str]"], str] = lambda m: table[m.group(0)]
    return [pat.sub(repl, ln) for ln in lines]
