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

# The three classes a still-red unit's base judgment answers (the class
# beside the why, polyforge#212): a failure the base carries the same way
# is pre-existing (not this change's to fix), one the base proves fresh
# (it lacks the test, or the test passes there) is this change's, and
# anything the base runs could not settle is undetermined — never
# released, the existing red rules hold.
CLASS_PRE_EXISTING = "pre_existing"
CLASS_THIS_CHANGE = "this_change"
CLASS_UNDETERMINED = "undetermined"

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


def classify_base_class(
    bar_sig: str, head_sig: str, presence: str, presence_why: str,
    runs: Sequence[BaseObservation], want: int,
) -> Tuple[str, str]:
    """The pre-existing judgment with its class: (class, why) — the same
    conditions, the same order and the same why wording as classify_base,
    the class beside them. A base that lacks the test, or where the test
    passes, proves the failure this change's (CLASS_THIS_CHANGE); anything
    the base runs could not settle is CLASS_UNDETERMINED, never released."""
    q = gostr.quote
    if presence != PRESENT:
        cls = CLASS_THIS_CHANGE if presence == ABSENT else CLASS_UNDETERMINED
        return cls, "not pre-existing: the base does not have this test (" + presence_why + ")"
    if head_sig == "":
        return CLASS_UNDETERMINED, "not pre-existing: no targeted re-run at HEAD shows its failure with an assertion line to compare"
    if bar_sig != head_sig:
        # an order-dependent failure: in the suite it fails one way, alone another
        return CLASS_UNDETERMINED, "not pre-existing: its failure in the bar (%s) differs from its isolated re-run at HEAD (%s)" % (q(bar_sig), q(head_sig))
    if len(runs) < want:
        return CLASS_UNDETERMINED, "not pre-existing: only %d of %d base runs ran (budget or deadline)" % (len(runs), want)
    for i, b in enumerate(runs, 1):
        if b.failed and b.signature == "":
            return CLASS_UNDETERMINED, "not pre-existing: base run %d shows the failure without an assertion line — no proof (an environmental failure)" % i
        if b.failed and b.signature != head_sig:
            return CLASS_UNDETERMINED, "not pre-existing: base run %d fails differently (%s, HEAD %s)" % (i, q(b.signature), q(head_sig))
        if b.failed:
            continue
        if b.found and b.exit == 0:
            return CLASS_THIS_CHANGE, "not pre-existing: it passes on the base (run %d)" % i
        return CLASS_UNDETERMINED, "not pre-existing: base run %d did not reach the test's own failure (exit %d, an environmental failure)" % (i, b.exit)
    return CLASS_PRE_EXISTING, "fails the same way on all %d base runs" % want


def classify_base(
    bar_sig: str, head_sig: str, exists: bool, exists_why: str, runs: Sequence[BaseObservation], want: int
) -> Tuple[bool, str]:
    """The pre-existing judgment: (pre-existing, why) — classify_base_class
    over the boolean presence its callers have always passed (a test whose
    kind has no existence proof never reaches it: the candidates filter
    drops those first)."""
    cls, why = classify_base_class(bar_sig, head_sig, PRESENT if exists else ABSENT, exists_why, runs, want)
    return cls == CLASS_PRE_EXISTING, why


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
