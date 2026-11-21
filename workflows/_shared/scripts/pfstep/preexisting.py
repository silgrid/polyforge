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

The one entry both callers judge by (polyforge#212, the owner's plan
review): ``judge_on_base`` — the base worktree's whole lifecycle, the
base setup, the presence proof, the base runs and the classification —
lives here with the candidate filter, the protected-path matching and
the skip form, so verification (``verify.py``) and the build checks
(``fast_checks.py``, its ``--repo`` run and its close) cannot drift; the
executors are the callers' own, injected (the git that adds the worktree
on the host, the shell that runs the base setup and the re-runs —
verify's boxed runs, the fast checks' host runs), and the timeout arrives
in seconds. The result lands on each candidate ``UnitState``: the class
(``base_class``), the observation summary (``base_detail``), and the
pre-existing record verify has always written (``pre_existing`` /
``pre_detail`` / ``base_sig``), its wording unchanged.
"""

import os
import shutil
import tempfile
from dataclasses import dataclass
from typing import Callable, Iterable, List, Optional, Protocol, Sequence, Set, Tuple

from . import git, gostr
from .rerun import UnitState
from .shellwords import has_assignment, has_flag, has_go_test, shell_quote, shell_words, token_index
from .unitruns import UnitRunDecl
from .units import (
    KIND_COMMAND,
    KIND_E2E_SUITE,
    KIND_GO,
    KIND_GO_PKG,
    KIND_LIBTEST,
    KIND_MJS,
    KIND_PYTEST,
    KIND_UNITTEST,
    cap_text,
    find_unit,
    normalize_output,
    output_lines,
    parse_units,
)

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

# The base runs a candidate gets — verification's own count, one entry
# for every caller (verify imported its own copy before polyforge#212).
BASE_RUNS = 1

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


def join_note(a: str, b: str) -> str:
    """Two notes as one ("; " between), the second alone when the first is
    empty — verify's own wording, moved here for both callers."""
    return b if a == "" else a + "; " + b


def path_is_protected(p: str, paths: Sequence[str]) -> bool:
    """Whether p is or lives under one of the protected paths (verify's own
    prefix matching, moved here for both callers)."""
    c = gostr.clean_rel(p)
    return c != "" and any(c == pr or c.startswith(pr + "/") for pr in paths)


def protected_paths(capsule: str, decls: Sequence[UnitRunDecl], known: Sequence[str] = ()) -> List[str]:
    """The paths every recorded unit run's test commit touched — a test
    this change authored is protected, never pre-existing (verify's own
    fold, moved here for both callers; the known paths keep their place
    ahead of the new ones)."""
    seen: Set[str] = set(known)
    out: List[str] = [p for p in known]
    for d in decls:
        sha = gostr.trim_space(d.test_commit)
        if sha == "" or sha.startswith("-") or d.repo == "":
            continue
        repo = os.path.join(capsule, "repos", d.repo)
        if not os.path.isdir(repo):
            continue
        try:
            changed = git.git(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", sha,
                              timeout=GIT_TIMEOUT)
        except git.GitError:
            continue
        for p in gostr.fields(changed):
            c = gostr.clean_rel(p)
            if c and c not in seen:
                seen.add(c)
                out.append(c)
    return out


def candidates(units: Iterable[UnitState]) -> List[UnitState]:
    """The still-failing units a base run could judge, each already carrying
    its class (verify's own filter, its wording unchanged): a protected test
    is this change's by definition, a unit no test file and name locate is
    never provable, and one without a targeted re-run keeps the note the
    flaky policy wrote — only the rest reach the base."""
    cands: List[UnitState] = []
    for u in units:
        if not u.failing():
            continue
        if u.protected:
            u.note = join_note(u.note, "never pre-existing: it is a protected test that pins this change")
            u.base_class = CLASS_THIS_CHANGE
        elif u.kind in (KIND_COMMAND, KIND_GO_PKG, KIND_LIBTEST) or u.file == "":
            u.note = join_note(u.note, "never pre-existing: no test file and test name locate it")
            u.base_class = CLASS_UNDETERMINED
        elif u.rerun == "":
            u.base_class = CLASS_UNDETERMINED
        else:
            cands.append(u)
    return cands


class BashResult(Protocol):
    """What one base-run execution answers: the exit, the log it wrote, its
    private TMPDIR and whether it never started — the caller's executor
    answers its own shape (verify's ExecResult, the fast checks' RerunRun)."""

    exit: int
    log_path: str
    tmp_dir: str
    not_started: bool


# (cmd, directory, timeout seconds) -> one execution's result: the base
# setup and every re-run, run by the caller's own executor.
RunBash = Callable[[str, str, float], BashResult]
# (tmp, base) -> the exit of `git worktree add --detach tmp base`: the
# caller's own git (verify's logged exec, the fast checks' host git).
WorktreeAdd = Callable[[str, str], int]


def judge_on_base(
    repo_dir: str,
    base: str,
    base_name: str,
    cands: Sequence[UnitState],
    setup: Sequence[str],
    worktree_add: WorktreeAdd,
    run_bash: RunBash,
    budget_left: Callable[[], bool],
    fits: Callable[[float], bool],
    budget_note: str,
    timeout: float,
    tmp_parent: Optional[str] = None,
    batch_note: Optional[Callable[[str], None]] = None,
) -> None:
    """Judge the candidates on a throwaway worktree of the base commit —
    the one entry every caller triages through (verify's own pre-existing
    phase, its wording and order byte-identical): the worktree's lifecycle,
    the base setup, each candidate's existence proof and its base runs,
    then classify_base_class. The result lands on each UnitState — the
    pre-existing record (pre_existing / pre_detail / base_sig) verify has
    always written, plus the class and the observation summary — and a
    candidate the base could not settle keeps its note, fail closed.
    batch_note, when given, receives every whole-batch failure's why (no
    budget, no worktree, a failed base setup): the caller's own record of
    a baseline that never ran; verification passes none, its record is
    the units' notes alone.
    """

    def mark_all(why: str) -> None:
        for u in cands:
            u.note = join_note(u.note, why)
            u.base_class = CLASS_UNDETERMINED
        if batch_note is not None:
            batch_note(why)

    if not budget_left():
        mark_all("not pre-existing: no base run started, %s is spent" % budget_note)
        return
    parent = tmp_parent
    try:
        if parent:
            os.makedirs(parent, exist_ok=True)
        tmp = tempfile.mkdtemp(prefix="pf-verify-base-", dir=parent)
    except OSError as err:
        mark_all("not pre-existing: no throwaway worktree: %s" % err)
        return
    try:
        add = worktree_add(tmp, base)
        if add != 0:
            mark_all("not pre-existing: the base worktree could not be checked out (exit %d)" % add)
            return
        try:
            for s in setup:
                er = run_bash(s, tmp, timeout)
                if er.exit != 0:
                    mark_all("not pre-existing: the base_setup command %s failed on the base (exit %d) — an"
                             " environmental failure" % (gostr.quote(s), er.exit))
                    return
            for u in cands:
                presence, why = unit_presence(repo_dir, base, u.kind, u.name, u.file)
                runs: List[BaseObservation] = []
                if presence == PRESENT:
                    i = 0
                    while i < BASE_RUNS and budget_left() and fits(u.rerun_dur):
                        er = run_bash(u.rerun, tmp, timeout)
                        us = parse_units(normalize_output(output_lines(er.log_path), tmp, er.tmp_dir))
                        obs = BaseObservation(exit=er.exit)
                        got = find_unit(us, u.kind, u.name)
                        if got is not None:
                            obs.found, obs.failed, obs.signature = True, got.failed, got.signature
                        runs.append(obs)
                        i += 1
                cls, detail = classify_base_class(u.unit.signature, u.head_sig, presence, why, runs, BASE_RUNS)
                u.base_class = cls
                u.base_detail = detail
                if cls == CLASS_PRE_EXISTING:
                    u.pre_existing = True
                    u.pre_detail = cap_text("fails on base %s too: %s, signature %s" % (base_name, detail, gostr.quote(u.head_sig)))
                    u.base_sig = u.head_sig
                else:
                    u.note = join_note(u.note, detail)
        finally:
            git.run(repo_dir, "worktree", "remove", "--force", tmp, timeout=GIT_TIMEOUT)
            git.run(repo_dir, "worktree", "prune", timeout=GIT_TIMEOUT)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def skip_form(cmd: str, repo: str, units: Sequence[UnitState], harness: bool, applier: str) -> Tuple[str, str]:
    """The command's bar form with its pre-existing units excluded, or the
    run form plus a note when it cannot exclude them (verify's own skip
    form, its wording unchanged, the applier named — verify-run or the
    fast checks)."""
    pre = [u for u in units if u.pre_existing]
    if not pre:
        return cmd, ""
    words, simple = shell_words(cmd)
    kind = pre[0].kind
    if any(u.kind != kind for u in pre):
        kind = ""
    names = [u.name for u in pre]
    if kind == KIND_GO and simple and has_go_test(words) and not has_flag(words, "-skip"):
        return cmd + " -skip '^(" + "|".join(names) + ")$'", ""
    if kind == KIND_GO and simple and harness and not has_assignment(cmd, "E2E_SKIP"):
        # the e2e harness protocol: E2E_SKIP excludes top-level tests
        return "E2E_SKIP='^(" + "|".join(names) + ")$' " + cmd, ""
    if kind == KIND_PYTEST and simple and token_index(words, "pytest") >= 0:
        return cmd + "".join(" --deselect " + shell_quote(n) for n in names), ""
    return cmd, ("repos/%s `%s` carries the pre-existing failure(s) %s but has no skip switch %s can apply: it"
                 " is listed as run, its failure(s) named here" % (repo, cmd, ", ".join(names), applier))


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
