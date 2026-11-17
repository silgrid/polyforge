"""merge.py — the work item's published PRs merged into the default branch
at the heads the lane verified (the successor of the Go step command
pf-merge: the same ``gh pr merge --merge --match-head-commit`` and the same
landing rules, written to a result file; no update of the PR branch — a
default branch that moved since verification is the sync step's again).

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish <publish.json> --verified <result.json> \\
        [--landing-out PATH] [--capsule DIR] [--deadline D] [--json] [--out PATH]

The script runs in the human-merge mode when the work item's frozen
step modes (``<capsule>/runtime-state/step-modes.json``, the claim's fix
of ``steps.<project>.merge.by: human``; polyforge#36) name the step it
runs as (``PF_STEP``) ``human``: the project declared the merge done by
a person. In that mode it never asks the forge to merge anything and
never judges the default branch's freshness (the person's landing is the
integrate lane's full gate to judge): a MERGED PR is taken by the HUMAN
landing rules only — the landed commit set with each commit's merge
flag, by the patch-id fingerprint (a merge commit carrying the verified
head as its branch side, a rebase's replays, a squash's whole diff);
anything else — a PR closed without merging, merged with content that
does not match the verified change, or still open — is exit 3, the
verdict partial: the step asks the owner, never a FAIL back to sync (its
summary never carries a lane's uncounted-rewind substring). A capsule
without the mode (micro's claims, every pre-#36 work item) runs the
default mode of the paragraphs below.

The work item's fixed target branch
(``runtime-state/target-branch.json``, a sub-ticket's) is the default
branch's stand-in everywhere below — both modes: an open PR must target
it, the freshness check compares ``origin/<target>`` with the verified
base, the landing rules judge the merge on it, and the landing record's
state is ``landed_on_target`` (with ``target``), a value no
``integration.state=landed`` filter ever takes. The file is read
strictly: one that is there but cannot be read, or a target branch
missing on a repo's origin, is not judged (exit 2) naming why — never a
silent fall-back to the default branch.

``--publish`` is the publish step's artifact: ``{"repos": [{name, branch,
pr_url, head, skipped_reason}]}`` (a repo with a skipped_reason has nothing
to merge). ``--verified`` is the verifying gate's result file (schema
pf-step-result/1): its ``heads`` are the heads the merge matches, one per
published repo — a published repo it names no head for is exit 2. Every
repo is judged before anything merges, in name order: its PR read (``gh pr
view``); one MERGED already is taken as the landing by the landing rules
(its branch side is the verified head — ``how: merged`` — or, merged
outside the lane, its merge commit is on the default branch and contains
the verified head — ``how: merged_elsewhere``), refused otherwise; one OPEN
must target the default branch, head the published branch and stand, on
origin, at the verified head, and — when the verified result records the
default branch's base (``details.repos[].base``, develop's verification) —
origin's default branch must still be at that base: one that gained commits
after verification is refused (sync, verify and publish again); any other
state is refused. A refusal merges
nothing (exit 1). Then each open PR is merged with ``gh pr merge --merge
--match-head-commit <verified head>``; a refused merge is read back once —
merged after all (the landing rules again), its branch moved, or the
forge's own refusal (a conflict with the default branch: sync again) — and
stops the pass: the later PRs stay open (exit 1), the merged ones are
landed and a re-run takes them by the landing rules.

The result's details::

    { "publish": "<file>", "verified": "<file>",
      "repos": [{name, pr_url, branch, head, published, merge_commit, how, branch_side, by_this_run, why}],
      "merge_commits": {"<repo>": ["<sha>"]} }

``heads`` are each repo's HEAD in the capsule (unchanged by the merge).

``merge_commits`` — of the details and the landing record alike — names,
per repo, the commit set the change landed as, oldest first, each
``{"sha": ..., "merge": true|false}`` (the default mode's lane merges
and merges taken elsewhere are merge commits; a human rebase landing
replays plain commits and a squash lands one).

``--landing-out`` (the merge skill registers it with ``pf step hub
wrap-attrs integration``) is written atomically on exit 0 — the landing
record: ``{state: landed, repos: [{repo, pr, head, light_verified_head,
merge_commit, accepted}], merge_commits}`` — and, on an EPIC's capsule
(``runtime-state/children.json``, polyforge#117), a ``children`` key
carrying the claim's snapshot verbatim: the ONE record the integrate
pool takes, each child's landed_on_target evidence riding it with its
source — or — the work item carrying
a fixed target branch — ``{state: landed_on_target, target, …}``, per
landed repo its name, PR
and the forge's merge commit, the verified head as its light-verified
head, and as its head the verified head (``how: merged`` or, in the human
mode, ``merged_by_human``) or, for a merge made elsewhere
(``merged_elsewhere``), the merge's branch side (else the verified head)
with ``accepted: true``; ``merge_commits`` the landed commit set, each
entry with its merge flag. It carries no waiver. On any other exit a
stale landing file is removed.

Exit 0: every published PR is landed. Exit 1 (never in the human mode): a
PR moved (its head is not the verified one), is closed or retargeted, was
merged elsewhere off the verified head, or the forge refused the merge.
Exit 3 (the human mode only): a PR is not landed and the owner is asked —
closed without merging, merged with content that does not match the
verified change, or still open. Exit 2: not judged — an unreadable
publish or verified file, a missing verified head, a task repo missing,
gh or git unreachable, the deadline — or the invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import traceback  # noqa: E402
from dataclasses import dataclass, field  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional  # noqa: E402

from pfstep import atomic, children, jsonout, runner  # noqa: E402
from pfstep import targetbranch  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402
from pfstep.gitmerge import gostr  # noqa: E402
from pfstep.gitmerge.ctx import Ctx, GoError, background  # noqa: E402
from pfstep.gitmerge.git import Git  # noqa: E402
from pfstep.gitmerge.humanmerge import Landed, human_landing  # noqa: E402
from pfstep.gitmerge.landing import take_as_landing  # noqa: E402

NAME = "merge.py"

HOW_MERGED = "merged"
HOW_MERGED_ELSEWHERE = "merged_elsewhere"
HOW_MERGED_BY_HUMAN = "merged_by_human"


@dataclass
class MergeRepo:
    """One repo of the merge, in the result's details."""

    name: str
    pr_url: str
    branch: str
    # head: the verified head the merge matches; published: the head publish pushed
    head: str
    published: str
    dir: str
    merge_commit: str = ""
    # how: merged, merged_elsewhere or merged_by_human ("" = not landed)
    how: str = ""
    # branch_side: the merge commit's second parent ("" = none); by_this_run:
    # this run asked the forge to merge it
    branch_side: str = ""
    by_this_run: bool = False
    why: str = ""
    # landed: the commit set the change landed as, oldest first (each
    # with its merge flag); empty until it lands
    landed: List[Landed] = field(default_factory=list)
    # The default branch commit recorded by verification (before the branch
    # was published).  ``base`` below is the branch name learned from the
    # forge; keep the two facts separate.
    verified_base: str = ""
    base: str = ""
    tip: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"name": self.name, "pr_url": self.pr_url, "branch": self.branch, "head": self.head}
        for k in ("published", "merge_commit", "how", "branch_side"):
            if getattr(self, k):
                d[k] = getattr(self, k)
        if self.by_this_run:
            d["by_this_run"] = True
        if self.why:
            d["why"] = self.why
        return d


def default_branch_update_reason(verified_base: str, current_base: str, base_branch: str = "") -> str:
    """Return the merge failure reason when the verified base moved. The
    base is the default branch, or — base_branch naming a sub-ticket's
    fixed target branch — that branch; either way the reason carries the
    stable substring a lane's uncounted rewind class matches
    (``branch moved since verification``)."""
    if verified_base == current_base:
        return ""
    if base_branch:
        return ("the target branch moved since verification (%s: verified %s, now %s); "
                "sync, verify and publish again" % (base_branch, gostr.short(verified_base), gostr.short(current_base)))
    return ("the default branch moved since verification (verified %s, now %s); "
            "sync, verify and publish again" % (gostr.short(verified_base), gostr.short(current_base)))


class Details:
    def __init__(self, publish: str, verified: str) -> None:
        self.publish = publish
        self.verified = verified
        self.repos: List[MergeRepo] = []
        self.merge_commits: Dict[str, List[Landed]] = {}
        # the work item's fixed target branch ("" = none): the base the
        # merge lands on, and the landing record's state names it
        self.target = ""
        # the epic's children snapshot (None = the capsule carries none:
        # not an epic's): the children the landing record attaches
        # verbatim (polyforge#117)
        self.children: Optional[Dict[str, Any]] = None

    def to_dict(self) -> Dict[str, Any]:
        return {"publish": self.publish, "verified": self.verified, "repos": [r.to_dict() for r in self.repos],
                "merge_commits": {k: [landed_of(c) for c in self.merge_commits[k]] for k in sorted(self.merge_commits)}}


def read_json(path: str) -> Any:
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as err:
        raise GoError(gostr.os_error("open", path, err)) from None
    try:
        return json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise GoError("%s is not the JSON expected: %s" % (path, err)) from None


def _field(v: Any, key: str, want: type, path: str) -> Any:
    x = v.get(key) if isinstance(v, dict) else None
    if x is not None and not isinstance(x, want):
        raise GoError("%s is not the JSON expected: %s is not a %s" % (path, key, want.__name__))
    return x


def landing_of(d: Details) -> Dict[str, Any]:
    """The landing record of a merge that landed every PR."""
    repos = []
    for r in d.repos:
        e: Dict[str, Any] = {"repo": r.name, "pr": r.pr_url, "head": r.head}
        if r.how == HOW_MERGED_ELSEWHERE:
            e["head"] = gostr.or_text(r.branch_side, r.head)
        if r.head:
            e["light_verified_head"] = r.head
        if r.merge_commit:
            e["merge_commit"] = r.merge_commit
        if r.how == HOW_MERGED_ELSEWHERE:
            e["accepted"] = True
        repos.append(e)
    out: Dict[str, Any] = {"state": "landed_on_target" if d.target else "landed", "repos": repos}
    if d.target:
        out["target"] = d.target
    if d.children is not None:
        # the epic's one record carries the children the claim snapshotted
        # verbatim (each child's landed_on_target evidence with its
        # source); the state stays landed, the pool's own
        out["children"] = d.children["children"]
    if d.merge_commits:
        out["merge_commits"] = {k: [landed_of(c) for c in d.merge_commits[k]] for k in sorted(d.merge_commits)}
    return out


def landed_of(c: Landed) -> Dict[str, Any]:
    """One landed commit as the record's entry: its sha and whether it is
    a merge commit."""
    return {"sha": c.sha, "merge": c.merge}


def remove_stale(*paths: str) -> None:
    for p in paths:
        if p:
            try:
                os.remove(p)
            except OSError:
                pass


class Merge:
    def __init__(self, inv: Invocation, g: Git, ctx: Ctx, human: bool, target: str = "") -> None:
        self.inv = inv
        self.g = g
        self.ctx = ctx
        # the human-merge mode: the claim's frozen step modes name this
        # step human (step_mode below)
        self.human = human
        # the work item's fixed target branch ("" = none): the base of the
        # merge in the default branch's place
        self.target = target

    def not_judged(self, why: str) -> int:
        """The run ends as not judged (exit 2), naming an interruption when
        the context ended."""
        e = self.ctx.err()
        if e is not None:
            why = "interrupted (%s): %s" % (e, why)
        self.inv.summary = "not judged: " + why
        return 2

    def take_landing(self, r: MergeRepo, merge: str, by_this_run: bool) -> None:
        """A PR found MERGED (merge its merge commit) recorded as the landing
        when the landing rules take it; else r.why."""
        g = self.g
        try:
            outside, refused = take_as_landing(g, r.dir, r.base, merge, [r.head, r.published], r.head)
        except GoError as err:
            raise GoError("repos/%s: reading the merged PR %s: %s" % (r.name, r.pr_url, err)) from None
        if refused:
            r.why = "its PR %s is merged outside this step, and %s" % (r.pr_url, refused)
            return
        r.merge_commit, r.by_this_run, r.how = merge, by_this_run, HOW_MERGED
        if outside:
            r.how = HOW_MERGED_ELSEWHERE
        try:
            r.branch_side = g.rev(r.dir, merge + "^2")
        except GoError:
            pass
        r.landed = [Landed(merge, True)]
        self.inv.say("repos/%s: landed as %s (%s)" % (r.name, gostr.short(merge), r.how))

    def judge(self, details: Details) -> int:
        if self.human:
            return self.judge_human(details)
        g, inv = self.g, self.inv
        # 1. every PR judged, nothing merged
        opened: List[MergeRepo] = []
        refused: List[str] = []
        for r in details.repos:
            r.base = self.target or g.default_branch(r.dir)
            if self.target:
                # a base that cannot be fetched is not judged, never a
                # judgment over a stand-in default branch (a PR against
                # main would be refused as though the lane asked for it)
                try:
                    g.fetch(r.dir, self.target)
                except GoError as err:
                    return self.not_judged("repos/%s: fetching the target branch %s: %s" % (r.name, self.target, err))
            try:
                pr = g.view_pr(r.dir, r.pr_url)
            except GoError as err:
                return self.not_judged("repos/%s: reading the PR %s: %s" % (r.name, r.pr_url, err))
            if pr.state == "MERGED":
                try:
                    self.take_landing(r, pr.merge_commit, False)
                except GoError as err:
                    return self.not_judged(str(err))
            elif pr.state == "OPEN":
                why = pr.mismatch(r.base, r.branch, self.target)
                if why:
                    r.why = "its PR " + why
                else:
                    try:
                        g.fetch(r.dir, r.base, r.branch)
                    except GoError as err:
                        return self.not_judged("repos/%s: fetching %s and %s: %s" % (r.name, r.base, r.branch, err))
                    try:
                        r.tip = g.rev(r.dir, "refs/remotes/origin/" + r.branch)
                    except GoError:
                        r.why = "origin has no branch %s (deleted outside the lane?)" % r.branch
                    else:
                        if r.tip != r.head:
                            r.why = ("origin's %s is at %s, not the verified head %s — the PR moved since verification (publish"
                                     " pushed %s)" % (r.branch, gostr.short(r.tip), gostr.short(r.head), gostr.short(r.published)))
                        else:
                            if r.verified_base:
                                current_base = g.rev(r.dir, "refs/remotes/origin/" + r.base)
                                r.why = default_branch_update_reason(r.verified_base, current_base, self.target)
                            if not r.why:
                                opened.append(r)
            else:
                r.why = "its PR %s is %s" % (r.pr_url, pr.state.lower())
            if r.why:
                refused.append("repos/" + r.name + ": " + r.why)
        if refused:
            inv.summary = "nothing merged — %d PR(s) cannot be taken: %s" % (len(refused), "; ".join(refused))
            return 1
        # 2. each open PR merged at the verified head
        for i, r in enumerate(opened):
            inv.say("repos/%s: gh pr merge %s --merge --match-head-commit %s" % (r.name, r.pr_url, gostr.short(r.head)))
            merr: Optional[GoError] = None
            try:
                g.merge_pr(r.dir, r.pr_url, r.head)
            except GoError as err:
                merr = err
            try:
                pr = g.view_pr(r.dir, r.pr_url)
            except GoError as err:
                return self.not_judged("repos/%s: reading the PR %s after the merge (%s): %s" % (r.name, r.pr_url, _v(merr), err))
            if pr.state == "MERGED":
                try:
                    self.take_landing(r, pr.merge_commit, merr is None)
                except GoError as err:
                    return self.not_judged(str(err))
            elif pr.state != "OPEN":
                r.why = "the merge was refused (%s) and the PR is %s" % (_v(merr), pr.state.lower())
            else:
                try:
                    g.fetch(r.dir, r.base, r.branch)
                except GoError as err:
                    return self.not_judged("repos/%s: fetching %s and %s after the refused merge (%s): %s" % (
                        r.name, r.base, r.branch, _v(merr), err))
                try:
                    tip = g.rev(r.dir, "refs/remotes/origin/" + r.branch)
                except GoError as err:
                    tip = _out(err)
                    moved = True
                else:
                    moved = tip != r.head
                if moved:
                    r.why = "the merge was refused (%s): origin's %s is at %s, not the verified head %s — the PR moved" % (
                        _v(merr), r.branch, gostr.or_text(gostr.short(tip), "none"), gostr.short(r.head))
                else:
                    r.why = ("the forge refused the merge at the verified head %s: %s (a default branch that moved and"
                             " conflicts is the sync step's: sync, verify and publish again)" % (gostr.short(r.head), _v(merr)))
            if r.why:
                later = len(opened) - i - 1
                inv.summary = "repos/%s: %s" % (r.name, r.why)
                if later > 0:
                    inv.summary += "; the %d later PR(s) stay open" % later
                return 1
        landed = ["repos/%s %s (%s)" % (r.name, gostr.short(r.merge_commit), r.how) for r in details.repos]
        inv.summary = "%d PR(s) landed on the default branch: %s" % (len(details.repos), ", ".join(landed))
        return 0

    def judge_human(self, details: Details) -> int:
        """The human-merge mode's judgment (mode file + PF_STEP, run()):
        every PR read, nothing merged by the run, the freshness never
        judged — each MERGED one taken by the human landing rules, every
        other state or a mismatch an exit-3 ask of the owner."""
        g, inv = self.g, self.inv
        ask: List[str] = []
        for r in details.repos:
            r.base = self.target or g.default_branch(r.dir)
            if self.target:
                # the same not-judged as the default mode's: a base that
                # cannot be fetched is never asked over
                try:
                    g.fetch(r.dir, self.target)
                except GoError as err:
                    return self.not_judged("repos/%s: fetching the target branch %s: %s" % (r.name, self.target, err))
            try:
                pr = g.view_pr(r.dir, r.pr_url)
            except GoError as err:
                return self.not_judged("repos/%s: reading the PR %s: %s" % (r.name, r.pr_url, err))
            if pr.state == "MERGED":
                try:
                    landed, refused = human_landing(g, r.dir, r.base, r.head, pr.merge_commit)
                except GoError as err:
                    return self.not_judged("repos/%s: reading the merged PR %s: %s" % (r.name, r.pr_url, err))
                if refused:
                    r.why = "its PR %s is merged, and %s" % (r.pr_url, refused)
                else:
                    r.merge_commit, r.how, r.landed = pr.merge_commit, HOW_MERGED_BY_HUMAN, landed
                    inv.say("repos/%s: the person landed it as %d commit(s) (%s)"
                            % (r.name, len(landed), gostr.short(pr.merge_commit)))
            elif pr.state == "OPEN":
                r.why = "its PR %s is still open — the person has not merged it yet" % r.pr_url
            else:
                r.why = "its PR %s is %s without merging" % (r.pr_url, pr.state.lower())
            if r.why:
                ask.append("repos/" + r.name + ": " + r.why)
        if ask:
            inv.summary = "%d PR(s) not landed — %s (ask the owner)" % (len(ask), "; ".join(ask))
            return 3
        took = ["repos/%s %s (%d commit(s))" % (r.name, gostr.short(r.merge_commit), len(r.landed)) for r in details.repos]
        inv.summary = "%d PR(s) landed by a person on the default branch: %s" % (len(details.repos), ", ".join(took))
        return 0


def step_mode(capsule: str, step: str) -> bool:
    """Whether the work item's claim-frozen step modes (the capsule's
    runtime-state/step-modes.json) name step `human` — the project
    declared the step's work done by a person. A missing file, an
    unreadable one or no name for the step is the default mode (micro's
    claims, every pre-#36 capsule)."""
    if step == "":
        return False
    try:
        with open(os.path.join(capsule, "runtime-state", "step-modes.json"), "rb") as f:
            raw = f.read()
    except OSError:
        return False
    try:
        modes = json.loads(raw.decode("utf-8", "replace"))
    except ValueError:
        return False
    return isinstance(modes, dict) and modes.get(step) == "human"


def _v(err: Optional[GoError]) -> str:
    """Go's %v of an error (<nil> for none)."""
    return "<nil>" if err is None else str(err)


def _out(err: GoError) -> str:
    out = getattr(err, "stdout", b"")
    return out.decode("utf-8", "surrogateescape").strip() if isinstance(out, bytes) else ""


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Merge the work item's published PRs at the verified heads.")
    ap.add_argument("--publish", default="", help="the publish step's artifact: {repos: [{name, branch, pr_url, head, skipped_reason}]}")
    ap.add_argument("--verified", default="", help="the verifying gate's result file (pf-step-result/1): its heads are the heads the merge matches")
    ap.add_argument("--landing-out", default="", help="on exit 0 write the landing record there (pf step hub wrap-attrs integration)")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    ap.add_argument("rest", nargs="*", help=argparse.SUPPRESS)
    return ap


def run(inv: Invocation, a: argparse.Namespace, held: List[Details]) -> int:
    """The merge; held receives its details once the invocation is usable."""
    inv.out, inv.json = a.out, a.json
    if a.rest:
        return inv.usage("unexpected arguments %s" % go_list(a.rest))
    if a.publish == "" or a.verified == "":
        return inv.usage("--publish <publish.json> and --verified <result.json> are both required")
    capsule = os.path.abspath(a.capsule)
    try:
        pub = read_json(a.publish)
        entries = _field(pub, "repos", list, a.publish) or []
        for e in entries:
            if not isinstance(e, dict):
                raise GoError("%s is not the JSON expected: a repos entry is not an object" % a.publish)
            for k in ("name", "skipped_reason", "branch", "pr_url", "head"):
                _field(e, k, str, a.publish)
    except GoError as err:
        return inv.usage("--publish: %s" % err)
    try:
        ver = read_json(a.verified)
        vheads = _field(ver, "heads", dict, a.verified) or {}
        vdetails = _field(ver, "details", dict, a.verified) or {}
        vrepos = _field(vdetails, "repos", list, a.verified) or []
        vbases: Dict[str, str] = {}
        for vrepo in vrepos:
            if not isinstance(vrepo, dict):
                raise GoError("%s is not the JSON expected: a details.repos entry is not an object" % a.verified)
            name = vrepo.get("name")
            base = vrepo.get("base")
            if isinstance(name, str) and isinstance(base, str):
                vbases[name] = base
        for v in vheads.values():
            if not isinstance(v, str):
                raise GoError("%s is not the JSON expected: a head is not a string" % a.verified)
    except GoError as err:
        return inv.usage("--verified: %s" % err)
    details = Details(a.publish, a.verified)
    try:
        details.target = targetbranch.read(capsule)
        details.children = children.read(capsule)
    except targetbranch.TargetBranchError as err:
        inv.summary = "not judged: %s" % err
        return 2
    except children.ChildrenError as err:
        inv.summary = "not judged: %s" % err
        return 2
    for i, p in enumerate(entries):
        name = gostr.trim_text(p.get("name") or "")
        if name == "":
            return inv.usage("--publish %s: repos[%d] has no name" % (a.publish, i))
        if gostr.trim_text(p.get("skipped_reason") or "") != "":
            continue
        branch = gostr.trim_text(p.get("branch") or "")
        if branch.startswith("refs/heads/"):
            branch = branch[len("refs/heads/"):]
        r = MergeRepo(name=name, pr_url=gostr.trim_text(p.get("pr_url") or ""), branch=branch,
                      published=gostr.trim_text(p.get("head") or ""), head=vheads.get(name, ""),
                      verified_base=vbases.get(name, ""),
                      dir=os.path.join(capsule, "repos", name))

        if r.pr_url == "" or r.branch == "" or r.published == "":
            return inv.usage("--publish %s: repos[%d] (%s) needs pr_url, branch and head (or a skipped_reason)" % (a.publish, i, name))
        if r.head == "":
            return inv.usage("--verified %s names no head for the published repo %s: the merge has nothing to match (the"
                             " verification must have run over it)" % (a.verified, name))
        if not os.path.isdir(r.dir):
            return inv.usage("repos/%s is not a task repo of the capsule %s" % (name, capsule))
        details.repos.append(r)
    if not details.repos:
        return inv.usage("--publish %s names no repo to merge (every entry is skipped, or none is listed)" % a.publish)
    details.repos.sort(key=lambda r: r.name)
    inv.details = details.to_dict
    held.append(details)
    ctx = Ctx(inv.deadline(a.deadline) / 1e9)
    m = Merge(inv, Git(ctx, inv.env), ctx, human=step_mode(capsule, inv.getenv("PF_STEP")), target=details.target)
    try:
        return m.judge(details)
    finally:
        bg = m.g.with_ctx(background())
        for r in details.repos:
            try:
                inv.heads[r.name] = bg.rev(r.dir, "HEAD")
            except GoError:
                pass
            if r.landed:
                details.merge_commits[r.name] = list(r.landed)


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None) -> int:
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    runner.raise_on_termination()
    a = parser().parse_args(inv.argv)
    landing_out = a.landing_out
    held: List[Details] = []
    try:
        code = run(inv, a, held)
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "not judged: interrupted (context canceled)"
        code = 2
    except Exception as err:  # a bug, never a verdict: said, and not judged
        traceback.print_exc()
        inv.summary = "not judged: %s" % err
        code = 2
    if code != 0:
        remove_stale(landing_out)
    elif landing_out and held:
        try:
            atomic.write(landing_out, jsonout.dumps(landing_of(held[0])).encode("utf-8"), 0o644)
        except OSError as err:
            inv.summary = "every PR landed, but the landing record %s could not be written: %s" % (landing_out, err)
            remove_stale(landing_out)
            code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
