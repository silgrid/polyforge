"""How a failed command's own output proves it ran to completion — the
one implementation verify.py's confirming re-run (the bar) and
fast_checks.py's (the build's fast check) share: the declared Go harness's
trailer, or an aggregate e2e runner's closing summary. A completion proof
stands in for the whole-command re-run, exactly as a confirm's exit 0
would (polyforge#157: the fast check re-uses verify's whole-command
confirmation, so its pieces live here, not copied)."""

import os
import re
from typing import Sequence

from . import gostr
from .rerun import UnitState
from .shellwords import command_word, shell_words
from .units import KIND_E2E_SUITE, output_lines

# The Go harness's closing trailer (its FAIL line carries the tally), and
# the aggregate e2e runner's summary shape: the header, the announced-suite
# line, one line per settled suite and the closing tally.
_HARNESS_RAN = re.compile(r"FAIL\te2e\t(\d+)/(\d+) tests ran(?:, (\d+) excluded by E2E_SKIP)?,", re.ASCII)
_SUMMARY_LINE = re.compile(r"(PASS|FAIL) ([^\t\n\f\r ]+) [^\t\n\f\r ]+s\Z", re.ASCII)
_SUMMARY_TALLY = re.compile(r"(\d+)/(\d+) passed\b", re.ASCII)
_SUMMARY_HEADER = re.compile(r"===== summary =====\Z", re.ASCII)
_SUITES_HEADER = re.compile(r"e2e/all: (\d+) suite\(s\)", re.ASCII)

# A note line the runner's own machinery prints (skipped when the run's
# own output is read back).
_OWN_NOTE = "# pf verify-run:"


def last_line(lines: Sequence[str]) -> str:
    """The last non-empty output line (the run's own `# pf verify-run:`
    notes skipped)."""
    for ln in reversed(lines):
        if gostr.trim_space(ln) != "" and not ln.startswith(_OWN_NOTE):
            return ln
    return ""


def read_go_harness(repo_dir: str) -> str:
    """The repo conf's ``go_harness=`` declaration (the last one wins)."""
    try:
        with open(os.path.join(repo_dir, ".polyforge", "verify-scope.conf"), encoding="utf-8", errors="replace") as f:
            raw = f.read()
    except OSError:
        return ""
    h = ""
    for line in raw.split("\n"):
        t = gostr.trim_space(line)
        if t.startswith("go_harness="):
            h = gostr.clean_rel(gostr.trim_space(t[len("go_harness="):]))
    return h


def harness_command(go_harness: str, cmd: str) -> bool:
    """Whether cmd runs the declared Go harness directly: assignments,
    then sh or bash, then exactly the harness path."""
    if go_harness == "":
        return False
    words, simple = shell_words(cmd)
    cw, ci = command_word(words)
    return simple and cw in ("sh", "bash") and len(words) == ci + 2 and gostr.clean_rel(words[ci + 1]) == go_harness


def completion_proof(harness: bool, timed_out: bool, log_path: str, units: Sequence[UnitState]) -> str:
    """How a failed command's first-run output proves its suite ran to
    completion ("" = it does not): the declared Go harness's trailer, or
    an aggregate e2e runner's closing summary."""
    if timed_out or log_path == "":
        return ""
    lines = output_lines(log_path)
    if not harness:
        return aggregate_completion(units, lines)
    m = _HARNESS_RAN.match(last_line(lines))
    if m is None:
        return ""
    ran, listed = int(m.group(1)), int(m.group(2))
    excluded = int(m.group(3)) if m.group(3) else 0
    if ran + excluded != listed:
        return ""
    return "the harness trailer shows all %d listed tests ran or were excluded" % listed


def aggregate_completion(units: Sequence[UnitState], lines: Sequence[str]) -> str:
    """How an aggregate e2e runner's summary proves every suite it announced
    settled, each failed one a located failing unit of the command ("" = no
    proof)."""
    at, announced = -1, -1
    for i, ln in enumerate(lines):
        if _SUMMARY_HEADER.match(ln):
            at = i
    if at < 0:
        return ""
    for ln in lines[:at]:
        m = _SUITES_HEADER.match(ln)
        if m:
            announced = int(m.group(1))
    if announced < 0:
        return ""
    failed = {u.name for u in units if u.kind == KIND_E2E_SUITE and u.failed}
    suites = passed = 0
    for ln in lines[at + 1:]:
        m = _SUMMARY_LINE.match(ln)
        if m:
            suites += 1
            if m.group(1) == "PASS":
                passed += 1
            else:
                s = m.group(2)
                if s.startswith("e2e/"):
                    s = s[len("e2e/"):]
                if "e2e/" + s not in failed:
                    return ""  # a failed suite the output did not locate
            continue
        m = _SUMMARY_TALLY.match(ln)
        if m is None:
            continue
        p, n = int(m.group(1)), int(m.group(2))
        if n == 0 or n != suites or n != announced or p != passed:
            return ""
        return "the runner's summary shows all %d suites it announced settled (%d passed)" % (n, p)
    return ""
