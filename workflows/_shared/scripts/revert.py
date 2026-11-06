"""revert.py — the integration lane's reverts (the successor of the Go step
command pf-revert): per item to revert, one PR per repo reverting the
item's merges on the default branch together with its landed fix tickets',
opened and merged, and the batch's pins written out with the reverts
applied.

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/revert.py" --batch <batch.json> [--item <wi>]... \\
        [--ops-out PATH] [--capsule DIR] [--plan PATH] [--deadline D] [--json] [--out PATH]

``--batch`` is the collect step's result file (schema pf-step-result/1):
its ``heads`` are the batch's pins — repo → the default branch head the
gate judges — its ``details.items`` the batch's landed items, ``[{id, slug,
merge_commits: {"<repo>": ["<sha>", …]}, fix_tickets: []}]`` (merge_commits
oldest first; fix_tickets the ids or slugs of its fix tickets), and its
``details.holds`` the held items a red batch blamed, ``[{wi, name,
merge_commits, landed_fixes: [{ref, merge_commits}]}]``. The items to
revert arrive ONLY as ``--item`` flags: the revert skill unions collect's
``reverts`` with the round's rewind input (full-gate's ``reverts``) and
passes each as ``--item``. Each names an item (id or slug): a batch item
and every landed fix ticket of it in the batch form the group; a held item
and its landed fix tickets as the hold records them do. Every merge of the
group in each repo is reverted, newest first, on the revert branch
``integrate/revert-<name>`` by the DEFAULT BRANCH's integrate.conf rules
(an append-only log keeps its bytes, its pins follow), the PR opened and
merged with ``--match-head-commit``. An item neither in the batch nor held
is exit 2.

The plan record (``--plan``; default ``revert-plan.json`` beside --out,
else the capsule's artifacts/integrate/) makes it resumable and carries the
reverts of earlier rounds: an item is planned (its group and merges) before
its first PR, each repo's revert PR and merge are recorded as they land,
and a run — with or without ``--item`` — first finishes every planned
revert not yet done (an existing branch or PR is picked up, a merge already
reverted skipped), so a crash between a PR and its record repeats no
revert. Without ``--item`` and with nothing planned the script only copies
the pins. After a revert lands, the repo's clone is checked out at the
revert's merge commit.

The result's details::

    { "batch": "<file>", "plan": "<file>",
      "reverts": [{wi, name, group[], repos: [{name, merge_commits[], revert_pr, revert_merge}], done, why}],
      "heads": {"<repo>": "<sha>"}, "gated": ["<repo>", …] }

``heads`` (the result's and the details') are the pins after the reverts —
each reverted repo's pin is its newest revert merge — and ``gated`` the
repos the gate judges: the batch's ``details.gated`` and every repo a
revert landed in. Exit 0: every planned revert is done (or there was
nothing to revert). Exit 1: a revert did not land — the forge refused its
PR, or a merge conflicts in code — named under ``why``; the plan keeps it
for the next run. Exit 2: not judged — an unreadable batch or plan, an item
not in the batch, a task repo missing, git or gh unreachable, the deadline
— or the invocation unusable.

``--ops-out`` (the revert skill registers it with ``pf step hub batch``) is
written atomically on exit 0: a JSON array of hub writes, per done revert
that landed in a repo (in the details' order) the item's whole record as
the step's hub reads it now (``pf step hub get``), moved under the lane's
rules (pfstep.integration) — ``{"batch": <PF_WI_ID>, "revert_merges":
{<repo>: <revert_merge>}, "revert_prs": [<each repo's revert_pr>],
"state": "reverted"}`` set on it — as ``{"verb": "attrs", "target": <wi>,
"name": "integration", "value": <record>, "if": {"integration.state":
"landed"}}``; ``[]`` when none. An earlier round's revert, which the record
already carries, needs no write. A record the rules refuse to move, or
the hub unreadable, is exit 2 (the reverts stay landed; a run again
writes the file). It needs PF_WI_ID (the batch the writes name); on any
other exit a stale file is removed.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import traceback  # noqa: E402
from dataclasses import dataclass, field  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional, Tuple  # noqa: E402

from pfstep import atomic, jsonout, runner  # noqa: E402
from pfstep import integration as rules  # noqa: E402
from pfstep.hub import HubError  # noqa: E402
from pfstep.hub import Runner as HubRunner  # noqa: E402
from pfstep.hub import runner as hub_runner  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402
from pfstep.gitmerge import gostr  # noqa: E402
from pfstep.gitmerge import merge as gm  # noqa: E402
from pfstep.gitmerge import revert as gr  # noqa: E402
from pfstep.gitmerge.ctx import Ctx, GoError, background  # noqa: E402
from pfstep.gitmerge.git import Git  # noqa: E402

NAME = "revert.py"
PLAN_NAME = "revert-plan.json"


def append_new(lst: List[str], *xs: str) -> List[str]:
    for x in xs:
        if x not in lst:
            lst.append(x)
    return lst


@dataclass
class BatchItem:
    """One landed item of the batch."""

    id: str = ""
    slug: str = ""
    merge_commits: Dict[str, List[str]] = field(default_factory=dict)
    fix_tickets: List[str] = field(default_factory=list)

    def is_(self, ref: str) -> bool:
        return ref != "" and (self.id == ref or self.slug == ref)

    def name(self) -> str:
        return self.slug if self.slug != "" else self.id


@dataclass
class BatchHold:
    """One held item of the batch: its merges and its landed fix tickets'."""

    wi: str = ""
    name: str = ""
    merge_commits: Dict[str, List[str]] = field(default_factory=dict)
    landed_fixes: List[Tuple[str, Dict[str, List[str]]]] = field(default_factory=list)

    def is_(self, ref: str) -> bool:
        return ref != "" and (self.wi == ref or self.name == ref)

    def as_items(self) -> Tuple[BatchItem, List[BatchItem]]:
        """The hold and its landed fixes as batch items (the fixes registered
        on the hold)."""
        item = BatchItem(id=self.wi, slug=self.name, merge_commits=self.merge_commits)
        items = [BatchItem(id=self.wi, slug=self.name, merge_commits=self.merge_commits)]
        for ref, mc in self.landed_fixes:
            item.fix_tickets.append(ref)
            items.append(BatchItem(id=ref, slug=ref, merge_commits=mc))
        return item, items


@dataclass
class Batch:
    heads: Dict[str, str] = field(default_factory=dict)
    items: List[BatchItem] = field(default_factory=list)
    holds: List[BatchHold] = field(default_factory=list)
    gated: List[str] = field(default_factory=list)


@dataclass
class RevertRepo:
    name: str
    merge_commits: Optional[List[str]] = None
    revert_pr: str = ""
    revert_merge: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"name": self.name, "merge_commits": self.merge_commits}
        if self.revert_pr:
            d["revert_pr"] = self.revert_pr
        if self.revert_merge:
            d["revert_merge"] = self.revert_merge
        return d


@dataclass
class RevertEntry:
    """One item's revert: its group, per repo the merges and the revert PR
    and merge once landed."""

    wi: str
    name: str
    group: Optional[List[str]] = None
    repos: Optional[List[RevertRepo]] = None
    done: bool = False
    # why the last run did not land it
    why: str = ""

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"wi": self.wi, "name": self.name, "group": self.group,
                             "repos": None if self.repos is None else [r.to_dict() for r in self.repos]}
        if self.done:
            d["done"] = True
        if self.why:
            d["why"] = self.why
        return d


class Plan:
    """The plan record: the reverts by source item id."""

    def __init__(self, path: str) -> None:
        self.path = path
        self.reverts: Dict[str, RevertEntry] = {}

    def save(self) -> None:
        doc = {"reverts": {k: self.reverts[k].to_dict() for k in sorted(self.reverts)}}
        atomic.write(self.path, jsonout.dumps(doc).encode("utf-8"), 0o644)

    def sorted(self) -> List[RevertEntry]:
        return [self.reverts[k] for k in sorted(self.reverts)]


def plan_revert(item: BatchItem, items: List[BatchItem]) -> RevertEntry:
    """The revert of item: the item and its landed fix tickets (by id or
    slug) as the group, and per repo every merge of the group, in order of
    the group then the record."""
    e = RevertEntry(wi=item.id, name=item.name(), group=[item.id], repos=[])
    group = [item]
    for it in items:
        if it.id == item.id:
            continue
        for f in item.fix_tickets:
            if it.is_(f):
                group.append(it)
                assert e.group is not None
                e.group.append(it.id)
                break
    merges: Dict[str, List[str]] = {}
    for it in group:
        for repo, cs in it.merge_commits.items():
            for c in cs:
                if repo != "" and c != "":
                    merges[repo] = append_new(merges.get(repo, []), c)
    assert e.repos is not None
    for repo in sorted(merges):
        e.repos.append(RevertRepo(name=repo, merge_commits=merges[repo]))
    return e


class Details:
    def __init__(self, batch: str, plan: str, reverts: List[RevertEntry]) -> None:
        self.batch = batch
        self.plan = plan
        self.reverts = reverts
        self.heads: Dict[str, str] = {}
        self.gated: List[str] = []

    def to_dict(self) -> Dict[str, Any]:
        return {"batch": self.batch, "plan": self.plan, "reverts": [e.to_dict() for e in self.reverts],
                "heads": {k: self.heads[k] for k in sorted(self.heads)}, "gated": list(self.gated)}

    def pins(self, g: Git, capsule: str, batch: Dict[str, str], gated: List[str]) -> Dict[str, str]:
        """The details' heads — the batch's pins, each reverted repo's
        replaced by its newest revert merge — and gated (the batch's gated
        repos and every repo a revert landed in); the heads."""
        heads = dict(batch)
        self.gated = list(gated)
        for e in self.reverts:
            for r in e.repos or []:
                if r.revert_merge == "":
                    continue
                cur = heads.get(r.name, "")
                # the newest revert merge is the one the others are ancestors of
                if cur == "" or cur == r.revert_merge or not g.is_ancestor(os.path.join(capsule, "repos", r.name), r.revert_merge, cur):
                    heads[r.name] = r.revert_merge
                append_new(self.gated, r.name)
        self.heads = heads
        self.gated.sort()
        return heads


def revert_writes(d: Details, batch: str, hub: HubRunner) -> List[Dict[str, Any]]:
    """The record writes of the done reverts that landed, each naming
    batch: the item's whole record as the step's hub reads it now (`pf step
    hub get`), moved to reverted under the lane's rules
    (pfstep.integration) — none for a record an earlier round already
    moved. A move the rules refuse is rules.RuleError; an unreadable record
    HubError, RuntimeError or ValueError."""
    out: List[Dict[str, Any]] = []
    for e in d.reverts:
        if not e.done:
            continue
        prs: List[str] = []
        merges: Dict[str, str] = {}
        for r in e.repos or []:
            if r.revert_pr:
                prs.append(r.revert_pr)
            if r.revert_merge:
                merges[r.name] = r.revert_merge
        if not merges:
            continue  # nothing of it was on any default branch: no move
        ref = e.wi or e.name
        wi = json.loads(hub(["get", ref]).decode("utf-8", "replace"))
        if not isinstance(wi, dict):
            raise ValueError("pf step hub get %s: the answer is not a work item" % ref)
        status = wi.get("status")
        read = rules.record_of(wi)
        rec = rules.move(status if isinstance(status, str) else "", read,
                         {"batch": batch, "revert_merges": {k: merges[k] for k in sorted(merges)}, "revert_prs": prs, "state": rules.REVERTED},
                         e.name or ref)
        if rec is None:
            continue  # an earlier round's revert: the record carries it
        target = wi.get("id")
        assert read is not None  # a move follows a hand-off the rules read
        out.append(rules.write(target if isinstance(target, str) and target else ref, rec, read))
    return out


# -- reading the batch and the plan --


class _Bad(ValueError):
    pass


def _get(v: Any, key: str, want: type) -> Any:
    x = v.get(key) if isinstance(v, dict) else None
    if x is not None and not isinstance(x, want):
        raise _Bad("json: cannot unmarshal %s into the field %s" % (type(x).__name__, key))
    return x


def _strs(v: Any, key: str) -> List[str]:
    xs = _get(v, key, list) or []
    for x in xs:
        if not isinstance(x, str):
            raise _Bad("json: cannot unmarshal %s into the field %s" % (type(x).__name__, key))
    return list(xs)


def _commits(v: Any, key: str) -> Dict[str, List[str]]:
    m = _get(v, key, dict) or {}
    out: Dict[str, List[str]] = {}
    for repo, cs in m.items():
        if cs is not None and (not isinstance(cs, list) or any(not isinstance(c, str) for c in cs)):
            raise _Bad("json: cannot unmarshal %s into the field %s" % (type(cs).__name__, key))
        out[repo] = list(cs or [])
    return out


def _obj(v: Any) -> Any:
    if not isinstance(v, dict):
        raise _Bad("json: cannot unmarshal %s into an object" % type(v).__name__)
    return v


def parse_batch(doc: Any) -> Batch:
    if not isinstance(doc, dict):
        raise _Bad("json: cannot unmarshal %s into a result" % type(doc).__name__)
    b = Batch()
    heads = _get(doc, "heads", dict) or {}
    for k, v in heads.items():
        if not isinstance(v, str):
            raise _Bad("json: cannot unmarshal %s into the field heads" % type(v).__name__)
        b.heads[k] = v
    det = _get(doc, "details", dict) or {}
    for it in _get(det, "items", list) or []:
        _obj(it)
        b.items.append(BatchItem(id=_get(it, "id", str) or "", slug=_get(it, "slug", str) or "",
                                 merge_commits=_commits(it, "merge_commits"), fix_tickets=_strs(it, "fix_tickets")))
    for h in _get(det, "holds", list) or []:
        _obj(h)
        fixes = []
        for f in _get(h, "landed_fixes", list) or []:
            _obj(f)
            fixes.append((_get(f, "ref", str) or "", _commits(f, "merge_commits")))
        b.holds.append(BatchHold(wi=_get(h, "wi", str) or "", name=_get(h, "name", str) or "",
                                 merge_commits=_commits(h, "merge_commits"), landed_fixes=fixes))
    b.gated = _strs(det, "gated")
    return b


def read_plan(plan: Plan) -> None:
    """plan's record read in (none = nothing planned); GoError otherwise."""
    try:
        with open(plan.path, "rb") as f:
            raw = f.read()
    except FileNotFoundError:
        return
    except OSError as err:
        raise GoError("the plan record %s: %s" % (plan.path, gostr.os_error("open", plan.path, err))) from None
    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
        if not isinstance(doc, dict):
            raise _Bad("json: cannot unmarshal %s into a plan" % type(doc).__name__)
        for k, e in (_get(doc, "reverts", dict) or {}).items():
            _obj(e)
            repos = None
            if _get(e, "repos", list) is not None:
                repos = []
                for r in e["repos"]:
                    _obj(r)
                    mc = _get(r, "merge_commits", list)
                    repos.append(RevertRepo(name=_get(r, "name", str) or "", merge_commits=None if mc is None else _strs(r, "merge_commits"),
                                            revert_pr=_get(r, "revert_pr", str) or "", revert_merge=_get(r, "revert_merge", str) or ""))
            grp = _get(e, "group", list)
            plan.reverts[k] = RevertEntry(wi=_get(e, "wi", str) or "", name=_get(e, "name", str) or "",
                                          group=None if grp is None else _strs(e, "group"), repos=repos,
                                          done=bool(_get(e, "done", bool)), why=_get(e, "why", str) or "")
    except ValueError as err:
        raise GoError("the plan record %s is not JSON: %s" % (plan.path, err)) from None


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Carry out the integration batch's reverts and write the gate's pins.")
    ap.add_argument("--batch", default="", help="the collect step's result file (pf-step-result/1): heads = the pins, details.items = the landed items")
    ap.add_argument("--item", action="append", default=[],
                    help="an item (id or slug) to revert, with its landed fix tickets; repeatable — the only way items to revert"
                         " arrive: the skill passes collect's reverts and the round's rewind input (full-gate's reverts) each as --item")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--plan", default="", help="the plan record (default: revert-plan.json beside --out, else the capsule's artifacts/integrate/)")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    ap.add_argument("--ops-out", default="", help="on exit 0 write the record moves of the landed reverts there (a hub write array: pf step hub batch)")
    ap.add_argument("rest", nargs="*", help=argparse.SUPPRESS)
    return ap


class Revert:
    def __init__(self, inv: Invocation, g: Git, ctx: Ctx) -> None:
        self.inv = inv
        self.g = g
        self.ctx = ctx

    def not_judged(self, why: str) -> int:
        e = self.ctx.err()
        if e is not None:
            why = "interrupted (%s): %s" % (e, why)
        self.inv.summary = "not judged: " + why
        return 2

    def carry_out(self, capsule: str, plan: Plan, details: Details, batch: Batch) -> int:
        g, inv = self.g, self.inv
        batch_id = gostr.or_text(inv.getenv("PF_WI_ID"), NAME)
        for e in details.reverts:
            if e.done:
                continue
            for r in e.repos or []:
                if r.revert_merge != "":
                    continue
                d = os.path.join(capsule, "repos", r.name)
                if not os.path.isdir(d):
                    return self.not_judged("reverting %s: repos/%s is not a task repo of the capsule %s" % (e.name, r.name, capsule))
                base = g.default_branch(d)
                try:
                    g.fetch(d, base)
                except GoError as err:
                    return self.not_judged("reverting %s in repos/%s: fetching %s: %s" % (e.name, r.name, base, err))
                try:
                    merges = gr.newest_first(g, d, base, r.merge_commits or [])
                except GoError as err:
                    return self.not_judged("reverting %s in repos/%s: ordering its merges: %s" % (e.name, r.name, err))
                try:
                    conf = gm.conf_at(g, d, "refs/remotes/origin/" + base)
                except GoError as err:
                    return self.not_judged("reverting %s in repos/%s: %s" % (e.name, r.name, err))
                inv.say("reverting %s in repos/%s: %s" % (e.name, r.name, ", ".join(gostr.short(m) for m in merges)))
                url, merge, fail = gr.revert(
                    g, d, base, conf, merges, gr.revert_branch(e.name),
                    "Revert %s (integration batch %s)" % (e.name, batch_id),
                    "The integration batch %s reverts %s (%s) from %s, together with its landed fix tickets.\n" % (
                        batch_id, e.name, ", ".join(e.group or []), base))
                if url != "":
                    r.revert_pr = url
                if fail is not None:
                    e.why = "repos/%s: %s" % (r.name, fail)
                    try:
                        plan.save()
                    except OSError as serr:
                        return self.not_judged("the plan record %s could not be written: %s" % (plan.path, serr))
                    if self.ctx.err() is not None:
                        return self.not_judged("reverting %s: %s" % (e.name, fail))
                    inv.summary = ("the revert of %s did not land in repos/%s: %s (the plan keeps it: fix the cause, then run"
                                   " revert.py again)" % (e.name, r.name, fail))
                    return 1
                if merge == "":
                    # the default branch already reverts every merge: the gate
                    # runs on its head
                    try:
                        g.fetch(d, base)
                    except GoError as ferr:
                        return self.not_judged("reverting %s in repos/%s: fetching %s: %s" % (e.name, r.name, base, ferr))
                    try:
                        merge = g.rev(d, "refs/remotes/origin/" + base)
                    except GoError as rerr:
                        return self.not_judged("reverting %s in repos/%s: origin has no %s: %s" % (e.name, r.name, base, rerr))
                r.revert_merge, e.why = merge, ""
                try:
                    plan.save()
                except OSError as serr:
                    return self.not_judged("the plan record %s could not be written: %s" % (plan.path, serr))
                try:
                    g.fetch(d, base)
                    try:
                        g.checkout(d, merge)
                    except GoError:
                        pass
                except GoError:
                    pass
                inv.say("reverted %s in repos/%s: %s merged as %s" % (
                    e.name, r.name, gostr.or_text(url, "(already on the default branch)"), gostr.short(merge)))
            e.done = True
            try:
                plan.save()
            except OSError as serr:
                return self.not_judged("the plan record %s could not be written: %s" % (plan.path, serr))
        names = [e.name for e in details.reverts]
        if not names:
            inv.summary = "nothing to revert: the pins copied for %d repo(s)" % len(batch.heads)
        else:
            inv.summary = "%d revert(s) landed (%s); the pins written with them" % (len(names), ", ".join(names))
        return 0


def run(inv: Invocation, a: argparse.Namespace, held: List[Details]) -> int:
    inv.out, inv.json = a.out, a.json
    if a.rest:
        return inv.usage("unexpected arguments %s" % go_list(a.rest))
    if a.batch == "":
        return inv.usage("--batch <batch.json> is required")
    if a.ops_out and inv.getenv("PF_WI_ID") == "":
        return inv.usage("--ops-out needs PF_WI_ID (the batch the moves name): it runs inside the batch's step")
    capsule = os.path.abspath(a.capsule)
    try:
        with open(a.batch, "rb") as f:
            raw = f.read()
    except OSError as err:
        return inv.usage("--batch: %s" % gostr.os_error("open", a.batch, err))
    try:
        batch = parse_batch(json.loads(raw.decode("utf-8", "replace")))
    except ValueError as err:
        return inv.usage("--batch: %s is not the JSON expected: %s" % (a.batch, err))
    path = a.plan
    if path == "":
        path = os.path.join(os.path.dirname(a.out), PLAN_NAME) if a.out else os.path.join(capsule, "artifacts", "integrate", PLAN_NAME)
    plan = Plan(os.path.abspath(path))
    try:
        read_plan(plan)
    except GoError as err:
        return inv.usage(str(err))
    for ref in a.item:
        ref = gostr.trim_text(ref)
        item: Optional[BatchItem] = None
        group = batch.items
        for it in batch.items:
            if it.is_(ref):
                item = it
        if item is None:
            # a held item the owner ruled reverted: its merges and its landed
            # fix tickets' are the hold's record
            for h in batch.holds:
                if h.is_(ref):
                    item, group = h.as_items()
        if item is None:
            if any(e.wi == ref or e.name == ref for e in plan.reverts.values()):
                continue  # an earlier round planned it: finished below
            return inv.usage("--item %s is neither a landed item of the batch %s (details.items) nor a held one"
                             " (details.holds): nothing of it can be reverted" % (ref, a.batch))
        if item.id in plan.reverts:
            continue
        plan.reverts[item.id] = plan_revert(item, group)
    for e in plan.reverts.values():
        if not e.repos:
            e.done = True  # nothing of it is on any default branch
    try:
        plan.save()
    except OSError as err:
        return inv.usage("the plan record %s could not be written: %s" % (plan.path, err))
    details = Details(a.batch, plan.path, plan.sorted())
    inv.details = details.to_dict
    held.append(details)
    ctx = Ctx(inv.deadline(a.deadline) / 1e9)
    rv = Revert(inv, Git(ctx, inv.env), ctx)
    try:
        return rv.carry_out(capsule, plan, details, batch)
    finally:
        inv.heads = details.pins(rv.g.with_ctx(background()), capsule, batch.heads, batch.gated)


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None, hub: Optional[HubRunner] = None) -> int:
    """The script: argv without the program name; environ and hub are a
    test's (default: the process's, `pf --json step hub`)."""
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    runner.raise_on_termination()
    a = parser().parse_args(inv.argv)
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
        if a.ops_out:
            try:
                os.remove(a.ops_out)
            except OSError:
                pass
    elif a.ops_out and held:
        try:
            ops = revert_writes(held[0], inv.getenv("PF_WI_ID"), hub if hub is not None else hub_runner(inv.env))
            atomic.write(a.ops_out, jsonout.dumps(ops).encode("utf-8"), 0o644)
        except (OSError, HubError, RuntimeError, ValueError) as err:
            inv.summary = "the reverts landed, but their record writes %s could not be written: %s" % (a.ops_out, err)
            try:
                os.remove(a.ops_out)
            except OSError:
                pass
            code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
