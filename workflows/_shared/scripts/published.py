"""published.py — the published record of a work item whose lane does not
merge its PRs (a project that skips the develop lane's merge step: the PRs
await a human review). The publish step, then the lane's last step, runs it
and registers the record with ``pf step hub wrap-attrs integration``.

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/published.py" --publish <publish.json> --verified <result.json> \\
        [--record-out PATH] [--capsule DIR] [--deadline D] [--json] [--out PATH]

``--publish`` and ``--verified`` are read as merge.py reads them (its
parsing, reused): the publish step's artifact ``{"repos": [{name, branch,
pr_url, head, skipped_reason}]}`` (a repo with a skipped_reason published
nothing) and the verifying gate's result file (schema pf-step-result/1),
whose ``heads`` name one verified head per published repo — a published
repo it names no head for is exit 2. Each published repo's head (the head
publish pushed) must be its verified head; the forge is never asked
anything and nothing is merged.

The result's details::

    { "publish": "<file>", "verified": "<file>",
      "repos": [{name, pr_url, branch, head, published, why}] }

``heads`` are each repo's HEAD in the capsule.

``--record-out`` is written atomically on exit 0 — the published record,
the landing record's shape (merge.py) without a merge: ``{state:
published, repos: [{repo, pr, head, light_verified_head}]}``, per published
repo (name order) its name, PR and the verified head as both its head and
its light-verified head. On any other exit a stale record is removed. The
integrate lane's collect takes landed records only: a published work item
never enters its pool.

Exit 0: every published head is the verified one. Exit 1: a published head
is not the verified one (the PR moved after the verification). Exit 2: not
judged — an unreadable publish or verified file, a missing verified head, a
task repo missing — or the invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional  # noqa: E402

from merge import MergeRepo, _field, read_json, remove_stale  # noqa: E402
from pfstep import atomic, jsonout, runner  # noqa: E402
from pfstep.gitmerge import gostr  # noqa: E402
from pfstep.gitmerge.ctx import Ctx, GoError  # noqa: E402
from pfstep.gitmerge.git import Git  # noqa: E402
from pfstep.integration import PUBLISHED  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402

NAME = "published.py"


class Details:
    def __init__(self, publish: str, verified: str) -> None:
        self.publish = publish
        self.verified = verified
        self.repos: List[MergeRepo] = []

    def to_dict(self) -> Dict[str, Any]:
        return {"publish": self.publish, "verified": self.verified, "repos": [r.to_dict() for r in self.repos]}


def record_of(d: Details) -> Dict[str, Any]:
    """The published record of a run whose published heads are all the
    verified ones."""
    repos = [{"repo": r.name, "pr": r.pr_url, "head": r.head, "light_verified_head": r.head} for r in d.repos]
    return {"state": PUBLISHED, "repos": repos}


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Record the work item's published PRs at the verified heads (no merge).")
    ap.add_argument("--publish", default="", help="the publish step's artifact: {repos: [{name, branch, pr_url, head, skipped_reason}]}")
    ap.add_argument("--verified", default="", help="the verifying gate's result file (pf-step-result/1): its heads are the heads published must be")
    ap.add_argument("--record-out", default="", help="on exit 0 write the published record there (pf step hub wrap-attrs integration)")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    ap.add_argument("rest", nargs="*", help=argparse.SUPPRESS)
    return ap


def run(inv: Invocation, a: argparse.Namespace, held: List[Details]) -> int:
    """The check; held receives its details once the invocation is usable."""
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
            return inv.usage("--verified %s names no head for the published repo %s: there is no verified head to record (the"
                             " verification must have run over it)" % (a.verified, name))
        if not os.path.isdir(r.dir):
            return inv.usage("repos/%s is not a task repo of the capsule %s" % (name, capsule))
        details.repos.append(r)
    if not details.repos:
        return inv.usage("--publish %s names no published repo (every entry is skipped, or none is listed)" % a.publish)
    details.repos.sort(key=lambda r: r.name)
    inv.details = details.to_dict
    held.append(details)
    g = Git(Ctx(inv.deadline(a.deadline) / 1e9), inv.env)
    for r in details.repos:
        try:
            inv.heads[r.name] = g.rev(r.dir, "HEAD")
        except GoError:
            pass
    moved = []
    for r in details.repos:
        if r.published != r.head:
            r.why = ("publish pushed %s, not the verified head %s — the PR moved since verification (sync, verify and"
                     " publish again)" % (gostr.short(r.published), gostr.short(r.head)))
            moved.append("repos/%s: %s" % (r.name, r.why))
    if moved:
        inv.summary = "nothing recorded — %d PR(s) not at the verified head: %s" % (len(moved), "; ".join(moved))
        return 1
    inv.summary = "%d PR(s) published at the verified heads, awaiting a human review: %s" % (
        len(details.repos), ", ".join("repos/%s %s" % (r.name, gostr.short(r.head)) for r in details.repos))
    return 0


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None) -> int:
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    runner.raise_on_termination()
    a = parser().parse_args(inv.argv)
    record_out = a.record_out
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
        remove_stale(record_out)
    elif record_out and held:
        try:
            atomic.write(record_out, jsonout.dumps(record_of(held[0])).encode("utf-8"), 0o644)
        except OSError as err:
            inv.summary = "every PR is at the verified head, but the published record %s could not be written: %s" % (record_out, err)
            remove_stale(record_out)
            code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
