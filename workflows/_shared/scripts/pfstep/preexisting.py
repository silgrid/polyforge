"""Proving a failure pre-existing: the judgment over the facts, and the
existence proof a base tree gives.

A unit still failing at HEAD after the flaky policy is pre-existing only
when ALL of these hold, fail closed on anything else: it exists at the
base (its file there declares it — a test the base does not have is this
change's failure); its isolated targeted re-run at HEAD shows its failure
with an assertion line, the same signature it failed with in the bar (an
order-dependent failure proves nothing); and every base run of that same
targeted command reports it failing with that same signature (a base run
that did not get as far as the unit's own failure, or shows it without an
assertion line, is environmental, never proof). Ported from the Go step
commands' preexisting.go (classifyBase, unitPresence).
"""

from dataclasses import dataclass
from typing import List, Sequence, Tuple

from . import git, gostr
from .units import KIND_E2E_SUITE, KIND_GO, KIND_MJS, KIND_PYTEST, KIND_UNITTEST

# Bounds one git read (seconds).
GIT_TIMEOUT = 600.0

# A unit's presence in a tree.
PRESENT = "present"  # its file declares it
ABSENT = "absent"  # its file is missing, or does not declare it
UNKNOWN = "unknown"  # its kind has no existence proof


@dataclass
class BaseObservation:
    """One base run of a unit."""

    exit: int = 0
    # found: the unit is reported in the run's output; failed: failing
    found: bool = False
    failed: bool = False
    signature: str = ""


def classify_base(
    bar_sig: str, head_sig: str, exists: bool, exists_why: str, runs: Sequence[BaseObservation], want: int
) -> Tuple[bool, str]:
    """The pre-existing judgment: (pre-existing, why)."""
    q = gostr.quote
    if not exists:
        return False, "not pre-existing: the base does not have this test (" + exists_why + ")"
    if head_sig == "":
        return False, "not pre-existing: no targeted re-run at HEAD shows its failure with an assertion line to compare"
    if bar_sig != head_sig:
        # an order-dependent failure: in the suite it fails one way, alone another
        return False, "not pre-existing: its failure in the bar (%s) differs from its isolated re-run at HEAD (%s)" % (q(bar_sig), q(head_sig))
    if len(runs) < want:
        return False, "not pre-existing: only %d of %d base runs ran (budget or deadline)" % (len(runs), want)
    for i, b in enumerate(runs, 1):
        if b.failed and b.signature == "":
            return False, "not pre-existing: base run %d shows the failure without an assertion line — no proof (an environmental failure)" % i
        if b.failed and b.signature != head_sig:
            return False, "not pre-existing: base run %d fails differently (%s, HEAD %s)" % (i, q(b.signature), q(head_sig))
        if b.failed:
            continue
        if b.found and b.exit == 0:
            return False, "not pre-existing: it passes on the base (run %d)" % i
        return False, "not pre-existing: base run %d did not reach the test's own failure (exit %d, an environmental failure)" % (i, b.exit)
    return True, "fails the same way on all %d base runs" % want


def short(sha: str) -> str:
    """A sha abbreviated for messages ("(none)" for none)."""
    if len(sha) > 12:
        return sha[:12]
    if sha == "":
        return "(none)"
    return sha


def unit_presence(repo_dir: str, rev: str, kind: str, name: str, file: str) -> Tuple[str, str]:
    """Whether rev's tree has the unit: (PRESENT | ABSENT | UNKNOWN, why it is
    not present)."""
    if file == "":
        return UNKNOWN, "no test file locates it"
    if git.run(repo_dir, "cat-file", "-e", rev + ":" + file, timeout=GIT_TIMEOUT).exit_code != 0:
        return ABSENT, file + " does not exist at " + short(rev)
    args: List[str]
    if kind == KIND_GO:
        args = ["grep", "-q", "-E", "^func " + gostr.quote_meta(name) + r"\(", rev, "--", file]
    elif kind in (KIND_PYTEST, KIND_UNITTEST):
        fn = name
        i = fn.find("[")
        if i >= 0:
            fn = fn[:i]  # a parametrized id's parameters may carry dots
        i = max(fn.rfind(":"), fn.rfind("."))
        if i >= 0:
            fn = fn[i + 1:]
        # git grep -E is the platform's POSIX ERE: \s is a GNU extension
        # macOS's regex library does not know, so the class is spelled out
        args = ["grep", "-q", "-E", "def[[:space:]]+" + gostr.quote_meta(fn) + r"\(", rev, "--", file]
    elif kind == KIND_MJS:
        args = ["grep", "-q", "-F", "-e", name, rev, "--", file]
    elif kind == KIND_E2E_SUITE:
        return PRESENT, ""  # the unit is the suite file itself
    else:
        return UNKNOWN, "its kind has no existence proof"
    if git.run(repo_dir, *args, timeout=GIT_TIMEOUT).exit_code != 0:
        return ABSENT, file + " at " + short(rev) + " does not declare " + name
    return PRESENT, ""
