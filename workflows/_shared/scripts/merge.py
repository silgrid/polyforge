"""merge.py — the work item's published PRs merged into the default branch
at the heads the lane verified (the successor of the Go step command
pf-merge: the same ``gh pr merge --merge --match-head-commit`` and the same
landing rules, written to a result file; no update of the PR branch — a
default branch that moved and conflicts is the sync step's again).

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/merge.py" --publish <publish.json> --verified <result.json> \\
        [--landing-out PATH] [--capsule DIR] [--deadline D] [--json] [--out PATH]

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
origin, at the verified head; any other state is refused. A refusal merges
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

``--landing-out`` (the merge skill registers it with ``pf step hub
wrap-attrs integration``) is written atomically on exit 0 — the landing
record: ``{state: landed, repos: [{repo, pr, head, light_verified_head,
merge_commit, accepted}], merge_commits}``, per landed repo its name, PR and
merge commit, the verified head as its light-verified head, and as its head
the verified head (``how: merged``) or, for a merge made elsewhere
(``merged_elsewhere``), the merge's branch side (else the verified head)
with ``accepted: true``. It carries no waiver. On any other exit a stale
landing file is removed.

Exit 0: every published PR is landed. Exit 1: a PR moved (its head is not
the verified one), is closed or retargeted, was merged elsewhere off the
verified head, or the forge refused the merge. Exit 2: not judged — an
unreadable publish or verified file, a missing verified head, a task repo
missing, gh or git unreachable, the deadline — or the invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import traceback  # noqa: E402
from dataclasses import dataclass  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional  # noqa: E402

from pfstep import atomic, jsonout, runner  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402
from pfstep.gitmerge import gostr  # noqa: E402
from pfstep.gitmerge.ctx import Ctx, GoError, background  # noqa: E402
from pfstep.gitmerge.git import Git  # noqa: E402
from pfstep.gitmerge.landing import take_as_landing  # noqa: E402

NAME = "merge.py"

HOW_MERGED = "merged"
HOW_MERGED_ELSEWHERE = "merged_elsewhere"


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
    # how: merged or merged_elsewhere ("" = not landed)
    how: str = ""
    # branch_side: the merge commit's second parent ("" = none); by_this_run:
    # this run asked the forge to merge it
    branch_side: str = ""
    by_this_run: bool = False
    why: str = ""
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


class Details:
    def __init__(self, publish: str, verified: str) -> None:
        self.publish = publish
        self.verified = verified
        self.repos: List[MergeRepo] = []
        self.merge_commits: Dict[str, List[str]] = {}

    def to_dict(self) -> Dict[str, Any]:
        return {"publish": self.publish, "verified": self.verified, "repos": [r.to_dict() for r in self.repos],
                "merge_commits": {k: self.merge_commits[k] for k in sorted(self.merge_commits)}}


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
    out: Dict[str, Any] = {"state": "landed", "repos": repos}
    if d.merge_commits:
        out["merge_commits"] = {k: d.merge_commits[k] for k in sorted(d.merge_commits)}
    return out


def remove_stale(*paths: str) -> None:
    for p in paths:
        if p:
            try:
                os.remove(p)
            except OSError:
                pass


class Merge:
    def __init__(self, inv: Invocation, g: Git, ctx: Ctx) -> None:
        self.inv = inv
        self.g = g
        self.ctx = ctx

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
        self.inv.say("repos/%s: landed as %s (%s)" % (r.name, gostr.short(merge), r.how))

    def judge(self, details: Details) -> int:
        g, inv = self.g, self.inv
        # 1. every PR judged, nothing merged
        opened: List[MergeRepo] = []
        refused: List[str] = []
        for r in details.repos:
            r.base = g.default_branch(r.dir)
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
                why = pr.mismatch(r.base, r.branch)
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
        for v in vheads.values():
            if not isinstance(v, str):
                raise GoError("%s is not the JSON expected: a head is not a string" % a.verified)
    except GoError as err:
        return inv.usage("--verified: %s" % err)
    details = Details(a.publish, a.verified)
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
    m = Merge(inv, Git(ctx, inv.env), ctx)
    try:
        return m.judge(details)
    finally:
        bg = m.g.with_ctx(background())
        for r in details.repos:
            try:
                inv.heads[r.name] = bg.rev(r.dir, "HEAD")
            except GoError:
                pass
            if r.merge_commit:
                details.merge_commits[r.name] = [r.merge_commit]


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
