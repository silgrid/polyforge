"""integrate.py — the integrate lane's collect and close, computed from the
project's integration pool and the batch capsule's clones, and the record
writes a step registers, written to a result file (schema
pf-step-result/1, command ``integrate.py``).

Usage (from the capsule root, or with --capsule):

  integrate.py collect [--pool FILE] [--capsule DIR] [--json] [--out PATH]
  integrate.py close --batch FILE [--pool FILE] [--gate FILE] [--triage FILE]
      [--revert FILE] [--rulings FILE] [--decision FILE] [--calls FILE]
      [--ops-out FILE] [--record-out FILE] [--capsule DIR] [--json] [--out PATH]
  integrate.py records --in FILE --ops-out FILE [--pins FILE] [--capsule DIR]
      [--json] [--out PATH]

The pool is read in the script through the step's own hub reads — `pf
--json step hub list` / `get`, which reach aihub only through the daemon,
scoped to the step's project and held to the step's hub whitelist (the
step's `hub:` must name get and list); the project and the batches' lane
are the batch capsule's own (capsule.json: project, bundle_name) — or from
--pool <file> (a pool snapshot: a test's, an operator's hand run). Nothing
here writes to aihub: the writes it leads to are files the skill registers
(`pf step hub batch`, `pf step hub wrap-attrs integration_batch`).

collect pins each task repo's default branch (fetched; the newest green tag
merged into it named) and takes the batch over the pins: a repo is gated
when its head is new — neither its newest green tag nor the head the last
red batch's gate failed on (the pool's red_heads) — and carries a landed
merge no green tag covers; the batch is the landed items each of whose
merges, every one, is in its repo's pinned head, one in a gated repo. Every
other landed item is named with why it is not in the batch, and each held
item's hold is judged (stuck: none of its fix tickets can release it any
more). Another batch of the project not finished (this one, PF_WI_ID,
excepted) refuses the batch: nothing is pinned. Exit 0: the batch is taken
(refused and empty are facts of the details); 2: the pool is unreadable, a
fetch failed, the capsule has no repos/. The result's `heads` are the pins.
Details (batch.json):
  {batch, heads: {repo: sha}, repos: {repo: {branch, head, green, skip}},
   gated: [repo], items: [{id, slug, repos, merge_commits, waived,
   red_rounds, fix_tickets}], skipped: [{wi, name, why}], holds: [{wi, name,
   stuck, reason, red_rounds, fix_tickets, prs, merge_commits, landed_fixes:
   [{ref, merge_commits}]}], red_heads: {repo: sha}, refused, empty, taken_at}

close reads the batch (collect's result), the gate (gate.py's result;
absent = no gate ran), the triage (gate.py --triage's result: its regate is
the gate's result only when it is this gate run's — it names the run, the
gate result file's sha256 `gate_run`, and carries its round and heads) and
the pool again, and decides the batch's result — green: every judged repo
passed and carries its tag; red: a judged repo failed (its heads are the
record's red heads); empty: refused, nothing gated or nothing judged — then
fetches the green tags and names every landed item a green tag covers
(every merge of it, in every repo, under a green tag: this batch's or an
earlier one's; none when refused), and assembles the batch's record. Its
other facts: --revert (revert.py's result: the reverts that landed),
--rulings (collect's rulings.json: hold_rulings), --calls (the step call
ledger runtime-state/step-calls.jsonl: the work items the batch's executed
`file` writes filed — fix_tickets) and --decision (full-gate's
gate-decision.json: the gate step's result; close refuses a batch whose
files give another). A named gate, triage, revert or call-ledger file that
does not exist is absent; a named rulings or decision file that does not
exist is an error. Exit 0: with --ops-out the moves are written there as
hub writes — per moved item its whole record, `set` applied to the record
as the pool read it, under the lane's rules (pfstep.integration):
[{"verb": "attrs", "target": <wi>, "name": "integration", "value":
<record>, "if": {"integration.state": "landed"}}] ([] when none) — with
--record-out the record alone; 2: an input is
unreadable, the decision disagrees with the files (or names no result
green, red or empty), every judged repo passed but one carries no green tag
(a red batch tags nothing: a passed repo beside a red one is no refusal), a
fetch failed — and a stale --ops-out or --record-out file is removed.
Details:
  {moves: [{wi, name, set}], integration_batch: {result, main, tags, items,
   waived, green_items, fix_tickets, hold_rulings, reverts, refused, empty,
   closed_at}}

records turns a step's decisions on the integration records into the hub
writes it registers, each built under the lane's rules
(pfstep.integration) from the record the step's hub reads now (`pf step
hub get`). --in is a JSON array, in order, of `file` hub writes (passed on
as written, for a `key:<K>` to name) and record entries:
  {"item": <wi>, "fix": [<ref>, ...]}    a hold's new fix tickets (collect's
                                         fix ruling): added to its
                                         fix_tickets;
  {"item": <wi>, "clear": <at>}          a hold cleared on the owner message
                                         stamped at (runtime-state/asks.jsonl,
                                         to this step): merged, hold_cleared
                                         {reason: its words, by, at,
                                         answered_by, answered_at, cleared_in:
                                         PF_WI_ID}, the write citing it
                                         (per_owner); refused while another
                                         batch of the lane is unfinished (pf
                                         step hub list --bundle <lane>);
  {"item": <wi>, "blame": [<ref>, ...]}  full-gate's attribution: landed,
                                         batch PF_WI_ID, red_rounds + 1,
                                         red_heads the --pins file's heads,
                                         fix_tickets the refs.
Each item at most once. Exit 0: --ops-out gets the writes (a record that
already carries its entry needs none); 1: a rule refuses an entry (each
named in the summary; nothing is written); 2: an input or the hub is
unreadable. Details: {writes: [{item, kind, state}], unchanged: [item],
refused: [{item, kind, reason, why}]}.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import datetime  # noqa: E402
import hashlib  # noqa: E402
import json  # noqa: E402
import re  # noqa: E402
from typing import Any, Callable, Dict, List, Mapping, Optional, Sequence, Tuple  # noqa: E402

from pfstep import atomic, git, gotext, jsonout, result, stepenv  # noqa: E402
from pfstep import integration as rules  # noqa: E402
from pfstep import godecode as gd  # noqa: E402
from pfstep.hub import HubError  # noqa: E402
from pfstep.hub import Runner as HubRunner  # noqa: E402
from pfstep.hub import runner as hub_runner  # noqa: E402
from pfstep.gotext import quote, short_sha, trim_space  # noqa: E402
from pfstep.invoke import Invocation, Parser  # noqa: E402

COMMAND = "integrate.py"
GATE = "gate.py"
REVERT = "revert.py"
CAPSULE_JSON = "capsule.json"

RESULT_GREEN = "green"
RESULT_RED = "red"
RESULT_EMPTY = "empty"

STATUS_WRAPPED = "wrapped"
STATUS_FAILED = "failed"
STATUS_CANCELED = "canceled"
INTEGRATION_LANDED = rules.LANDED
INTEGRATION_MERGED = rules.MERGED

# LANDED_FILTER selects the landed pool: the hub list's attrs filter.
LANDED_FILTER = rules.KEY + ".state=" + rules.LANDED

# GIT_TIMEOUT bounds one git command of the lane (a fetch included; seconds).
GIT_TIMEOUT = 600.0

# The pool's list bounds: the landed items of a project, and its batches on
# the lane (newest first).
ITEMS_LIMIT = 1000
BATCHES_LIMIT = 200

ERR_CORE_NOT_FOUND = "CORE-404"
ERR_DAEMON_NOT_FOUND = "DAEMON-404"


def now_stamp() -> str:
    return result.stamp(datetime.datetime.now(datetime.timezone.utc))


def or_text(s: str, alt: str) -> str:
    return alt if not trim_space(s) else s


def or_id(slug: str, wid: str) -> str:
    return slug if slug else wid


def sorted_dict(m: Mapping[str, Any]) -> Dict[str, Any]:
    """A map in the key order Go's encoder writes it."""
    return {k: m[k] for k in sorted(m)}


def loads(text: str) -> Any:
    def refuse(name: str) -> Any:
        raise ValueError("invalid character in literal %s" % name)

    return json.loads(text, parse_constant=refuse)


def read_text(path: str) -> str:
    with open(path, "rb") as f:
        return f.read().decode("utf-8", "replace")


def _number(v: Any) -> Optional[float]:
    """A JSON number as Go's map[string]any holds it (float64), else None."""
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        return None
    return float(v)


# ---------------------------------------------------------------------------
# git (hardened, pfstep.git)


class GitError(Exception):
    pass


def git_run(directory: str, *args: str) -> str:
    try:
        return git.git(directory, *args, timeout=GIT_TIMEOUT)
    except git.GitError as err:
        raise GitError(str(err)) from None


def git_ok(directory: str, *args: str) -> bool:
    return git.run(directory, *args, timeout=GIT_TIMEOUT).exit_code == 0


def default_branch(directory: str) -> str:
    """origin's default branch (origin/HEAD, else main)."""
    c = git.run(directory, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD", timeout=GIT_TIMEOUT)
    if c.exit_code == 0:
        b = trim_space(c.stdout.decode("utf-8", "replace"))
        if b.startswith("origin/"):
            b = b[len("origin/"):]
        if b:
            return b
    return "main"


def fetch(directory: str, *branches: str) -> None:
    """origin's named branches into refs/remotes/origin/*, and the green tags."""
    args = ["fetch", "-q", "origin", "+refs/tags/green/*:refs/tags/green/*"]
    for b in branches:
        args.append("+refs/heads/" + b + ":refs/remotes/origin/" + b)
    git_run(directory, *args)


def rev(directory: str, r: str) -> str:
    """The commit r names; GitError when none."""
    return trim_space(git_run(directory, "rev-parse", "--verify", "--quiet", r + "^{commit}"))


def rev_or_empty(directory: str, r: str) -> str:
    try:
        return rev(directory, r)
    except GitError:
        return ""


def is_ancestor(directory: str, a: str, b: str) -> bool:
    return git_ok(directory, "merge-base", "--is-ancestor", a, b)


_GREEN_NAME = re.compile(r"green/([0-9]{8})-([0-9]+)", re.ASCII)


def newest_green_of(names: Sequence[str]) -> str:
    """The newest well-formed green tag of names: the date, then the number."""
    best, bd, bn = "", "", -1
    for t in names:
        m = _GREEN_NAME.fullmatch(t)
        if m is None:
            continue
        k = int(m.group(2))
        if m.group(1) > bd or (m.group(1) == bd and k > bn):
            best, bd, bn = t, m.group(1), k
    return best


def newest_green(directory: str, ref: str) -> str:
    """The newest green tag merged into ref ("" = none)."""
    try:
        out = git_run(directory, "tag", "--list", "green/*", "--merged", ref)
    except GitError:
        return ""
    return newest_green_of(gotext.fields(out))


def green_containing(directory: str, c: str) -> str:
    """The newest green tag (as fetched) whose commit contains c ("" = none)."""
    try:
        out = git_run(directory, "tag", "--list", "green/*", "--contains", c)
    except GitError:
        return ""
    return newest_green_of(gotext.fields(out))


# ---------------------------------------------------------------------------
# records


INTEGRATION_REPO = gd.Struct({
    "repo": gd.STR, "pr": gd.STR, "head": gd.STR, "light_verified_head": gd.STR, "merge_commit": gd.STR, "accepted": gd.BOOL,
})
INTEGRATION = gd.Struct({
    "state": gd.STR, "repos": gd.List(INTEGRATION_REPO), "merge_commits": gd.Map(gd.List(gd.STR)),
    "waived": gd.List(gd.Struct({"gate": gd.STR, "ruling": gd.STR})),
})


def integration_repo_json(r: Dict[str, Any]) -> Dict[str, Any]:
    out: Dict[str, Any] = {"repo": r["repo"], "pr": r["pr"], "head": r["head"]}
    for k in ("light_verified_head", "merge_commit"):
        if r[k]:
            out[k] = r[k]
    if r["accepted"]:
        out["accepted"] = True
    return out


def merge_commits_of(integration: Optional[Mapping[str, Any]]) -> Dict[str, List[str]]:
    """A hand-off's merge_commits (repo -> its merge commits, oldest first);
    a record of any other shape names none."""
    v = (integration or {}).get("merge_commits")
    if v is None:
        return {}
    mc, err = gd.decode(v, gd.Map(gd.List(gd.STR)))
    if err is not None or mc is None:
        return {}
    out: Dict[str, List[str]] = {}
    for repo in sorted(mc):
        for c in mc[repo] or []:
            if repo and c:
                out.setdefault(repo, []).append(c)
    return out


def strings_of(v: Any) -> List[str]:
    """v as a list of non-empty strings."""
    if not isinstance(v, list):
        return []
    return [x for x in v if isinstance(x, str) and x]


def held(item: Mapping[str, Any]) -> bool:
    """A landed item holds the green tag: a red batch blamed it (the
    lane's hold, pfstep.integration.held)."""
    return rules.held(item["integration"])


def batch_item_of(p: Mapping[str, Any]) -> Dict[str, Any]:
    """A landed pool item read into the batch's item."""
    integ = p["integration"] or {}
    dec, _ = gd.decode(integ, INTEGRATION)
    bi: Dict[str, Any] = {"id": p["id"]}
    if p["slug"]:
        bi["slug"] = p["slug"]
    repos = dec["repos"]
    bi["repos"] = None if repos is None else [integration_repo_json(r) for r in repos]
    bi["merge_commits"] = merge_commits_of(integ)
    if p["waivers"]:
        bi["waived"] = True
    rr = _number(integ.get("red_rounds"))
    if rr is not None and int(rr):
        bi["red_rounds"] = int(rr)
    fixes = strings_of(integ.get("fix_tickets"))
    if fixes:
        bi["fix_tickets"] = fixes
    return bi


# ---------------------------------------------------------------------------
# the pool

TICKET = gd.Struct({"status": gd.STR, "integration": gd.STR, "merge_commits": gd.Map(gd.List(gd.STR))})
POOL_ITEM = gd.Struct({
    "id": gd.STR, "slug": gd.STR, "priority": gd.STR, "landed": gd.STR, "integration": gd.Map(gd.ANY), "waivers": gd.List(gd.ANY),
})
SNAPSHOT = gd.Struct({
    "items": gd.List(POOL_ITEM), "open_batches": gd.List(gd.STR), "red_heads": gd.Map(gd.STR), "tickets": gd.Map(TICKET),
})
WORK_ITEM = gd.Struct({
    "id": gd.STR, "slug": gd.STR, "seq": gd.INT, "project_id": gd.STR, "project": gd.STR, "goal": gd.STR,
    "content": gd.Ptr(gd.STR), "parent_id": gd.Ptr(gd.STR), "status": gd.STR, "priority": gd.STR,
    "milestone": gd.Ptr(gd.STR), "labels": gd.List(gd.STR), "attrs": gd.Map(gd.ANY), "reporter": gd.STR,
    "assignee": gd.Ptr(gd.STR), "waiting_on": gd.Ptr(gd.STR), "bundle_name": gd.Ptr(gd.STR),
    "created_at": gd.STR, "updated_at": gd.STR, "closed_at": gd.Ptr(gd.STR),
})
IDENTITY = gd.Struct({
    "work_item_id": gd.STR, "capsule_id": gd.STR, "project": gd.STR, "bundle_name": gd.STR, "repos": gd.Map(gd.STR), "created_at": gd.STR,
})


class HubLister:
    """The pool reader's two reads over the step's hub."""

    def __init__(self, run: HubRunner) -> None:
        self.run = run

    def list(self, status: str = "", bundle: str = "", attrs: Sequence[str] = (), limit: int = 0) -> List[Dict[str, Any]]:
        """The work items the filters select; attrs are `<path>=<value>`
        filters (the attr at the dot path is that string)."""
        args = ["list"]
        for flag, v in (("--status", status), ("--bundle", bundle)):
            if v:
                args += [flag, v]
        for a in attrs:
            args += ["--attr", a]
        if limit > 0:
            args += ["--limit", str(limit)]
        raw = self.run(args)
        try:
            page, err = gd.decode(loads(raw.decode("utf-8", "replace")), gd.Struct({"items": gd.List(WORK_ITEM)}))
        except ValueError as e:
            page, err = None, str(e)
        if err is not None or page is None:
            raise RuntimeError("pf step hub list: the answer is not a work item list: %s" % err)
        return list(page["items"] or [])

    def get(self, ref: str) -> Optional[Dict[str, Any]]:
        """One work item; None when the hub does not find it."""
        try:
            raw = self.run(["get", ref])
        except HubError as e:
            if e.code in (ERR_CORE_NOT_FOUND, ERR_DAEMON_NOT_FOUND):
                return None
            raise
        try:
            wi, err = gd.decode(loads(raw.decode("utf-8", "replace")), WORK_ITEM)
        except ValueError as e:
            wi, err = None, str(e)
        if err is not None or wi is None:
            raise RuntimeError("pf step hub get %s: the answer is not a work item: %s" % (ref, err))
        return dict(wi)


def waivers_of(attrs: Mapping[str, Any], integration: Mapping[str, Any]) -> Optional[List[Any]]:
    """An item's owner waivers: its attrs.waivers when the attrs carry the
    key (a contract 4 wrap writes it), else — an item that landed before
    contract 4 — its integration record's `waived` (owner ruling R13).
    Transition code for the upgrade across contract 4:
    delete the fallback (and the integration record's `waived` reading) once
    no project's pool holds an item that landed before contract 4 — every
    such item merged or reverted by an integration batch."""
    if "waivers" in attrs:
        w = attrs["waivers"]
        return w if isinstance(w, list) else None
    w = integration.get("waived")
    return w if isinstance(w, list) else None


def red_heads_of(heads: Dict[str, str], attrs: Mapping[str, Any]) -> Dict[str, str]:
    """A finished batch's red heads (its record's main, when the result is
    red), for every repo a newer red batch did not name."""
    ib = attrs.get("integration_batch")
    ib = ib if isinstance(ib, dict) else {}
    if ib.get("result") != "red":
        return heads
    main = ib.get("main")
    for repo, v in (main if isinstance(main, dict) else {}).items():
        if isinstance(v, str) and v and not heads.get(repo):
            heads[repo] = v
    return heads


def read_pool(lister: HubLister, lane: str) -> Dict[str, Any]:
    """The project's integration pool, the batches being lane's."""
    if not lane:
        raise RuntimeError("no integration lane named: the batches cannot be listed")
    items = lister.list(status=STATUS_WRAPPED, attrs=[LANDED_FILTER], limit=ITEMS_LIMIT)
    snap: Dict[str, Any] = {"items": None, "open_batches": None, "red_heads": {}, "tickets": None}
    refs: List[str] = []
    pool_items: List[Dict[str, Any]] = []
    for it in items:
        attrs = it["attrs"] or {}
        integ = attrs.get("integration")
        integ = integ if isinstance(integ, dict) else None
        pi = {"id": it["id"], "slug": it["slug"], "priority": it["priority"],
              "landed": it["closed_at"] if it["closed_at"] is not None else it["updated_at"],
              "integration": integ, "waivers": waivers_of(attrs, integ or {})}
        pool_items.append(pi)
        refs += strings_of((integ or {}).get("fix_tickets"))
    if pool_items:
        snap["items"] = pool_items
    for ref in refs:
        if snap["tickets"] is not None and ref in snap["tickets"]:
            continue
        wi = lister.get(ref)
        t: Dict[str, Any] = {"status": "", "integration": "", "merge_commits": None}
        if wi is not None:
            t["status"] = wi["status"]
            attrs = wi["attrs"] or {}
            integ = attrs.get("integration")
            integ = integ if isinstance(integ, dict) else {}
            st = integ.get("state")
            t["integration"] = st if isinstance(st, str) else ""
            mc = integ.get("merge_commits")
            if isinstance(mc, dict):
                for repo, v in mc.items():
                    for x in v if isinstance(v, list) else []:
                        if isinstance(x, str) and repo and x:
                            t["merge_commits"] = t["merge_commits"] or {}
                            t["merge_commits"].setdefault(repo, []).append(x)
        tickets: Dict[str, Any] = snap["tickets"] if snap["tickets"] is not None else {}
        tickets[ref] = t
        snap["tickets"] = tickets
    batches = lister.list(bundle=lane, limit=BATCHES_LIMIT)
    batches.sort(key=lambda b: b["seq"], reverse=True)  # newest first
    for b in batches:
        if b["status"] == STATUS_WRAPPED:
            snap["red_heads"] = red_heads_of(snap["red_heads"], b["attrs"] or {})
        elif b["status"] in (STATUS_FAILED, STATUS_CANCELED):
            pass
        else:
            snap["open_batches"] = (snap["open_batches"] or []) + [b["id"]]
    return snap


def snapshot_json(snap: Mapping[str, Any]) -> Dict[str, Any]:
    """The snapshot as a pool file carries it."""
    out: Dict[str, Any] = {"items": None if snap["items"] is None else [_item_json(i) for i in snap["items"]]}
    if snap["open_batches"]:
        out["open_batches"] = list(snap["open_batches"])
    out["red_heads"] = sorted_dict(snap["red_heads"] or {})
    if snap["tickets"]:
        tickets = {}
        for ref in sorted(snap["tickets"]):
            t = snap["tickets"][ref]
            tj: Dict[str, Any] = {"status": t["status"]}
            if t["integration"]:
                tj["integration"] = t["integration"]
            if t["merge_commits"]:
                tj["merge_commits"] = sorted_dict(t["merge_commits"])
            tickets[ref] = tj
        out["tickets"] = tickets
    return out


def _item_json(i: Mapping[str, Any]) -> Dict[str, Any]:
    out: Dict[str, Any] = {"id": i["id"]}
    if i["slug"]:
        out["slug"] = i["slug"]
    if i["priority"]:
        out["priority"] = i["priority"]
    out["landed"] = i["landed"]
    out["integration"] = i["integration"]
    if i["waivers"]:
        out["waivers"] = i["waivers"]
    return out


def read_pool_file(path: str) -> Dict[str, Any]:
    """A pool snapshot file; ValueError/OSError naming why it is unusable."""
    text = read_text(path)
    try:
        doc = loads(text)
    except ValueError as err:
        raise ValueError("not a pool snapshot: %s" % err) from None
    snap, derr = gd.decode(doc, SNAPSHOT)
    if derr is not None:
        raise ValueError("not a pool snapshot: %s" % derr)
    snap = dict(snap)
    if snap["red_heads"] is None:
        snap["red_heads"] = {}
    return snap


def pool_of(path: str, capsule_root: str, hub: HubRunner) -> Dict[str, Any]:
    """The pool snapshot a subcommand reads: the file at path when named,
    else the pool read now through the step's hub (the capsule's project
    and lane). RuntimeError names why it cannot be read."""
    if path:
        try:
            return read_pool_file(path)
        except OSError as err:
            raise RuntimeError("the pool snapshot %s: %s" % (path, gotext.os_error("open", err))) from None
        except ValueError as err:
            raise RuntimeError("the pool snapshot %s: %s" % (path, err)) from None
    try:
        text = read_text(os.path.join(capsule_root, CAPSULE_JSON))
    except OSError as err:
        raise RuntimeError("the pool's project and lane (the capsule's %s): %s" % (CAPSULE_JSON, gotext.os_error("open", err))) from None
    try:
        idn, derr = gd.decode(loads(text), IDENTITY)
    except ValueError as e:
        idn, derr = None, str(e)
    if derr is not None or idn is None or not idn["project"] or not idn["bundle_name"]:
        raise RuntimeError("the capsule's %s names no project and lane (bundle_name): the pool cannot be read" % CAPSULE_JSON)
    try:
        snap = read_pool(HubLister(hub), idn["bundle_name"])
    except (HubError, RuntimeError, OSError) as err:
        raise RuntimeError("reading the integration pool through the step's hub: %s" % err) from None
    if snap["red_heads"] is None:
        snap["red_heads"] = {}
    return snap


# ---------------------------------------------------------------------------
# collect


def pin_heads(repo_dir: Callable[[str], str], repos: Sequence[str]) -> Dict[str, Dict[str, str]]:
    """Each task repo's default branch fetched and its head pinned (the
    newest green tag merged into it named)."""
    out: Dict[str, Dict[str, str]] = {}
    for repo in repos:
        d = repo_dir(repo)
        base = default_branch(d)
        try:
            fetch(d, base)
        except GitError as err:
            raise RuntimeError("%s: fetching %s: %s" % (repo, base, err)) from None
        try:
            head = rev(d, "refs/remotes/origin/" + base)
        except GitError as err:
            raise RuntimeError("%s: origin has no %s: %s" % (repo, base, err)) from None
        out[repo] = {"branch": base, "head": head, "green": newest_green(d, head), "skip": ""}
    return out


def pinned_json(ph: Mapping[str, str]) -> Dict[str, str]:
    out = {"branch": ph["branch"], "head": ph["head"]}
    if ph["green"]:
        out["green"] = ph["green"]
    if ph["skip"]:
        out["skip"] = ph["skip"]
    return out


def take_batch(repo_dir: Callable[[str], str], heads: Dict[str, Dict[str, str]], snap: Mapping[str, Any],
               revert_heads: Optional[Mapping[str, str]] = None) -> Tuple[List[Dict[str, Any]], List[Dict[str, str]], List[str]]:
    """Which repos the gate judges (skip set on the others) and which landed
    items the batch carries; every other landed item with why, and the
    gated-out repos' reasons."""
    revert_heads = revert_heads or {}
    landed_in: List[Tuple[Dict[str, Any], Dict[str, bool]]] = []
    fresh_all: Dict[str, bool] = {}
    skipped: List[Dict[str, str]] = []
    for it in snap["items"] or []:
        mc = merge_commits_of(it["integration"])
        name = or_id(it["slug"], it["id"])
        if not mc:
            skipped.append({"wi": it["id"], "name": name, "why": "its record names no merge commit"})
            continue
        fresh: Dict[str, bool] = {}
        outside = ""
        for repo in sorted(mc):
            ph = heads.get(repo)
            d = repo_dir(repo)
            for c in mc[repo]:
                if ph is None:
                    if not outside:
                        outside = "its merge %s is in %s, which is no task repo of the batch" % (short_sha(c), repo)
                elif not is_ancestor(d, c, ph["head"]):
                    if not outside:
                        outside = "its merge %s in %s is not in the pinned head %s (merged after the pin)" % (short_sha(c), repo, short_sha(ph["head"]))
                elif not ph["green"] or not is_ancestor(d, c, "refs/tags/" + ph["green"]):
                    fresh[repo] = True
        if outside:
            skipped.append({"wi": it["id"], "name": name, "why": outside})
            continue
        landed_in.append((it, fresh))
        for repo in fresh:
            fresh_all[repo] = True
    repo_skips: List[str] = []
    for repo in sorted(heads):
        ph = heads[repo]
        green_at = rev_or_empty(repo_dir(repo), "refs/tags/" + ph["green"]) if ph["green"] else ""
        if revert_heads.get(repo):
            pass  # a revert of this batch's: the gate runs over it
        elif green_at and green_at == ph["head"]:
            ph["skip"] = "at its newest green tag " + ph["green"]
        elif (snap["red_heads"] or {}).get(repo, "") == ph["head"]:
            ph["skip"] = "at the head the last red gate judged (" + short_sha(ph["head"]) + ")"
        elif not fresh_all.get(repo):
            ph["skip"] = "no landed merge in it awaits a green tag"
        if ph["skip"]:
            repo_skips.append(repo + ": " + ph["skip"])
    items: List[Dict[str, Any]] = []
    for it, fresh in landed_in:
        if any(heads[repo]["skip"] == "" for repo in fresh):
            items.append(batch_item_of(it))
            continue
        name = or_id(it["slug"], it["id"])
        if not fresh:
            skipped.append({"wi": it["id"], "name": name, "why": "every merge of it is under a green tag already (close moves it to merged)"})
            continue
        rs = [repo + ": " + heads[repo]["skip"] for repo in sorted(fresh)]
        skipped.append({"wi": it["id"], "name": name, "why": "no repo its merges await a green tag in is gated (" + "; ".join(rs) + ")"})
    items.sort(key=lambda i: i["id"])
    skipped.sort(key=lambda s: s["wi"])
    return items, skipped, repo_skips


def gated(heads: Mapping[str, Mapping[str, str]]) -> List[str]:
    return sorted(repo for repo, h in heads.items() if h["skip"] == "")


def empty_reason(repo_skips: Sequence[str]) -> str:
    return "nothing new to gate: " + or_text("; ".join(repo_skips), "the project has no task repo")


def merged_after(repo_dir: Callable[[str], str], fix_merges: Mapping[str, Sequence[str]], src: Mapping[str, Any]) -> bool:
    """A landed fix ticket's merges newer than the gate that last blamed src
    (src's record red_heads): some merge not in that gate's head of its
    repo — a repo that gate did not judge, or no task repo, counts newer."""
    rh = (src["integration"] or {}).get("red_heads")
    heads = rh if isinstance(rh, dict) else {}
    for repo, cs in fix_merges.items():
        head = heads.get(repo)
        head = head if isinstance(head, str) else ""
        d = repo_dir(repo)
        for c in cs:
            if not head or not os.path.isdir(d) or not is_ancestor(d, c, head):
                return True
    return False


def _ticket_open(t: Mapping[str, Any]) -> bool:
    st = t["status"]
    if st in ("", STATUS_FAILED, STATUS_CANCELED):
        return False
    if st == STATUS_WRAPPED:
        return bool(t["integration"] == INTEGRATION_LANDED and t["merged_after"])
    return True


def hold_stuck(fixes: Sequence[Mapping[str, Any]]) -> Tuple[bool, str]:
    """Whether a held source's fix tickets can no longer release it (none
    open; no ticket at all is stuck too), each ticket's state named."""
    if not fixes:
        return True, "no fix ticket is registered"
    parts = []
    stuck = True
    for f in fixes:
        if _ticket_open(f):
            stuck = False
        st = f["status"] or "not found"
        if f["integration"]:
            st += ", " + f["integration"]
        if f["status"] == STATUS_WRAPPED and f["integration"] == INTEGRATION_LANDED:
            st += " after the blaming gate" if f["merged_after"] else " before the blaming gate"
        parts.append("%s %s" % (f["ref"], st))
    return stuck, "; ".join(parts)


def holds_of(repo_dir: Callable[[str], str], snap: Mapping[str, Any]) -> List[Dict[str, Any]]:
    """Every held item's hold judged over the snapshot's fix-ticket states
    and the clones."""
    out: List[Dict[str, Any]] = []
    tickets = snap["tickets"] or {}
    empty_ticket = {"status": "", "integration": "", "merge_commits": None}
    for src in snap["items"] or []:
        if not held(src):
            continue
        integ = src["integration"] or {}
        fixes = strings_of(integ.get("fix_tickets"))
        states = []
        for f in fixes:
            t = tickets.get(f, empty_ticket)
            st = {"ref": f, "status": t["status"], "integration": t["integration"], "merged_after": False}
            if st["integration"] == INTEGRATION_LANDED:
                st["merged_after"] = merged_after(repo_dir, t["merge_commits"] or {}, src)
            states.append(st)
        stuck, why = hold_stuck(states)
        h: Dict[str, Any] = {"wi": src["id"]}
        if src["slug"]:
            h["name"] = src["slug"]
        h["stuck"] = stuck
        h["reason"] = why
        rr = _number(integ.get("red_rounds"))
        h["red_rounds"] = int(rr) if rr is not None else 0
        h["fix_tickets"] = fixes
        dec, derr = gd.decode(integ, INTEGRATION)
        if derr is None:
            prs = [r["pr"] for r in dec["repos"] or []]
            if prs:
                h["prs"] = prs
        mc = merge_commits_of(integ)
        if mc:
            h["merge_commits"] = mc
        landed_fixes = []
        for f in fixes:
            t = tickets.get(f, empty_ticket)
            if t["status"] == STATUS_WRAPPED and t["integration"] == INTEGRATION_LANDED and t["merge_commits"]:
                landed_fixes.append({"ref": f, "merge_commits": sorted_dict(t["merge_commits"])})
        if landed_fixes:
            h["landed_fixes"] = landed_fixes
        out.append(h)
    out.sort(key=lambda h: h["wi"])
    return out


def capsule_repos(capsule_dir: str) -> Tuple[str, List[str]]:
    """The capsule root and its task repos (the clones under repos/,
    sorted); ValueError naming why they are unreadable."""
    root = os.path.abspath(capsule_dir)
    rd = os.path.join(root, "repos")
    try:
        entries = list(os.scandir(rd))
    except OSError as err:
        raise ValueError("--capsule %s: the task repos: %s" % (root, gotext.os_error("open", err))) from None
    out = [e.name for e in entries if e.is_dir(follow_symlinks=False) and os.path.exists(os.path.join(rd, e.name, ".git"))]
    return root, sorted(out)


def _parser(sub: str) -> Parser:
    p = Parser(COMMAND + " " + sub)
    p.add_argument("--pool", default="", help="a pool snapshot file (default: the pool read now through the step's hub, pf step hub list/get)")
    p.add_argument("--capsule", default=".", help="the batch capsule root (repos/<name> clones)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    p.add_argument("rest", nargs=argparse.REMAINDER, help=argparse.SUPPRESS)
    return p


def collect(inv: Invocation, args: List[str], environ: Mapping[str, str], hub: HubRunner) -> int:
    ns = _parser("collect").parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    if ns.rest:
        return inv.usage("unexpected arguments [%s]" % " ".join(ns.rest))
    try:
        root, repos = capsule_repos(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    try:
        snap = pool_of(ns.pool, root, hub)
    except RuntimeError as err:
        inv.summary = str(err)
        return 2
    batch = environ.get(stepenv.ENV_WI_ID, "")

    def repo_dir(repo: str) -> str:
        return os.path.join(root, "repos", repo)

    heads_out: Dict[str, str] = {}
    d: Dict[str, Any] = {"batch": batch, "heads": heads_out, "repos": {}, "gated": [], "items": [], "skipped": [], "holds": [],
                         "red_heads": sorted_dict(snap["red_heads"])}
    taken_at = now_stamp()
    inv.details = d
    inv.heads = heads_out
    refused = ""
    for b in snap["open_batches"] or []:
        if b != batch:
            refused = "another integration batch (%s) is not finished: one batch at a time" % b
    if refused:
        d["refused"] = refused
        d["taken_at"] = taken_at
        inv.summary = "refused: " + refused
        return 0
    try:
        heads = pin_heads(repo_dir, repos)
    except RuntimeError as err:
        d["taken_at"] = taken_at
        inv.summary = str(err)
        return 2
    items, skipped, repo_skips = take_batch(repo_dir, heads, snap)
    d["repos"] = {repo: pinned_json(heads[repo]) for repo in sorted(heads)}
    gated_repos = gated(heads)
    d["gated"] = gated_repos or None  # Go's nil list: null
    d["items"] = items
    d["skipped"] = skipped
    for repo in sorted(heads):
        heads_out[repo] = heads[repo]["head"]
    empty = ""
    if not gated_repos:
        empty = empty_reason(repo_skips)
    d["holds"] = holds_of(repo_dir, snap)
    if empty:
        d["empty"] = empty
    d["taken_at"] = taken_at
    stuck = sum(1 for h in d["holds"] if h["stuck"])
    if empty:
        inv.summary = "%s; %d stuck hold(s)" % (empty, stuck)
    else:
        inv.summary = "%d item(s) in the batch, %d repo(s) to gate (%s); %d stuck hold(s)" % (
            len(items), len(gated_repos), ", ".join(gated_repos), stuck)
    return 0


# ---------------------------------------------------------------------------
# close

BATCH_ITEM = gd.Struct({
    "id": gd.STR, "slug": gd.STR, "repos": gd.List(INTEGRATION_REPO), "merge_commits": gd.Map(gd.List(gd.STR)),
    "waived": gd.BOOL, "red_rounds": gd.INT, "fix_tickets": gd.List(gd.STR),
})
COLLECT_DETAILS = gd.Struct({
    "batch": gd.STR, "heads": gd.Map(gd.STR),
    "repos": gd.Map(gd.Ptr(gd.Struct({"branch": gd.STR, "head": gd.STR, "green": gd.STR, "skip": gd.STR}))),
    "gated": gd.List(gd.STR), "items": gd.List(BATCH_ITEM),
    "skipped": gd.List(gd.Struct({"wi": gd.STR, "name": gd.STR, "why": gd.STR})),
    "holds": gd.List(gd.Struct({
        "wi": gd.STR, "name": gd.STR, "stuck": gd.BOOL, "reason": gd.STR, "red_rounds": gd.INT, "fix_tickets": gd.List(gd.STR),
        "prs": gd.List(gd.STR), "merge_commits": gd.Map(gd.List(gd.STR)),
        "landed_fixes": gd.List(gd.Struct({"ref": gd.STR, "merge_commits": gd.Map(gd.List(gd.STR))})),
    })),
    "red_heads": gd.Map(gd.STR), "refused": gd.STR, "empty": gd.STR, "taken_at": gd.STR,
})
# The gate's result, as gate.py writes it.
GATE_RESULT = gd.Struct({
    "commit": gd.STR, "clean": gd.BOOL, "runner": gd.STR, "runner_version": gd.STR, "plan": gd.ANY, "plan_digest": gd.STR,
    "image": gd.STR, "built": gd.BOOL, "container": gd.STR, "engine": gd.STR, "log_dir": gd.STR, "commands": gd.ANY,
    "incomplete": gd.STR, "passed": gd.BOOL, "duration_ns": gd.INT,
})
GATE_REPO = gd.Struct({"head": gd.STR, "branch": gd.STR, "result": gd.Ptr(GATE_RESULT), "tag": gd.STR, "tag_error": gd.STR})
GATE_DETAILS = gd.Struct({
    "mode": gd.STR, "pins": gd.STR, "gated": gd.List(gd.STR), "repos": gd.Map(gd.Ptr(GATE_REPO)), "tags": gd.Map(gd.STR),
    "failed": gd.List(gd.STR), "incomplete": gd.Map(gd.STR), "log_dir": gd.STR,
})
TRIAGE_UNIT = gd.Struct({k: gd.STR for k in (
    "repo", "command", "unit", "kind", "file", "class", "item", "root", "why", "base", "outside", "rerun", "log", "signature", "excerpt")})
TRIAGE_DETAILS = gd.Struct({
    "pins": gd.STR, "gate": gd.STR, "repos": gd.List(gd.STR), "baselines": gd.Map(gd.STR), "units": gd.List(TRIAGE_UNIT),
    "classes": gd.Map(gd.INT), "all_flaky": gd.BOOL, "regate": gd.Ptr(GATE_DETAILS), "log_dir": gd.STR, "gate_run": gd.STR,
})
REVERT_DETAILS = gd.Struct({
    "batch": gd.STR, "plan": gd.STR,
    "reverts": gd.List(gd.Ptr(gd.Struct({
        "wi": gd.STR, "name": gd.STR, "group": gd.List(gd.STR),
        "repos": gd.List(gd.Ptr(gd.Struct({"name": gd.STR, "merge_commits": gd.List(gd.STR), "revert_pr": gd.STR, "revert_merge": gd.STR}))),
        "done": gd.BOOL, "why": gd.STR,
    }))),
    "heads": gd.Map(gd.STR), "gated": gd.List(gd.STR),
})
RESULT_FILE = gd.Struct({
    "schema": gd.STR, "command": gd.STR, "version": gd.STR, "argv": gd.List(gd.STR), "round": gd.STR, "started_at": gd.STR,
    "ended_at": gd.STR, "exit_code": gd.INT, "verdict": gd.STR, "heads": gd.Map(gd.STR), "summary": gd.STR, "details": gd.ANY,
    "logs": gd.List(gd.STR),
})
STEP_OP = gd.Struct({k: gd.STR for k in (
    "id", "verb", "key", "project", "lane", "goal", "content", "priority", "target", "from", "reason", "batch",
    "per_owner", "answered_by", "answered_at", "name")})
STEP_OP.fields.update({"labels": gd.List(gd.STR), "set": gd.Map(gd.ANY), "value": gd.ANY})
STEP_OP.fold = {k.lower(): k for k in STEP_OP.fields}
LEDGER_LINE = gd.Struct({
    "seq": gd.INT, "at": gd.STR, "kind": gd.STR, "step": gd.STR, "round": gd.INT, "spawn": gd.INT, "verb": gd.STR, "args": gd.ANY,
    "call": gd.INT, "outcome": gd.STR, "code": gd.STR, "error": gd.STR,
    "handoff": gd.Ptr(gd.Struct({
        "status": gd.STR, "to": gd.STR, "per_owner": gd.STR, "reason": gd.STR, "evidence": gd.List(gd.STR),
        "heads": gd.Map(gd.STR), "candidate": gd.STR, "started_at": gd.STR,
    })),
    "pending": gd.List(STEP_OP), "op": gd.STR, "ref": gd.STR,
})


class InputError(Exception):
    pass


def _fold_get(doc: Mapping[str, Any], name: str) -> Tuple[bool, Any]:
    if name in doc:
        return True, doc[name]
    for k, v in doc.items():
        if k.lower() == name:
            return True, v
    return False, None


def read_step_result(path: str, name: str, spec: Any) -> Any:
    """A step result file of command name, its details read as spec."""
    try:
        text = read_text(path)
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    try:
        doc = loads(text)
    except ValueError as err:
        raise InputError("not JSON: %s" % err) from None
    env, derr = gd.decode(doc, gd.Struct({"schema": gd.STR, "command": gd.STR, "details": gd.ANY}))
    if derr is not None:
        raise InputError("not JSON: %s" % derr)
    if env["schema"] != result.SCHEMA or env["command"] != name:
        raise InputError("not %s %s result file (schema %s, command %s)" % ("an" if name[:1] in "aeiou" else "a", name, quote(env["schema"]),
                                                                          quote(env["command"])))
    has, details = _fold_get(doc, "details") if isinstance(doc, dict) else (False, None)
    if not has:
        raise InputError("its details do not decode: unexpected end of JSON input")
    out, derr = gd.decode(details, spec)
    if derr is not None:
        raise InputError("its details do not decode: %s" % derr)
    return out


def read_result_file(path: str) -> Dict[str, Any]:
    """A step result file's envelope (its round and heads)."""
    try:
        text = read_text(path)
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    try:
        doc = loads(text)
    except ValueError as err:
        raise InputError("not JSON: %s" % err) from None
    res, derr = gd.decode(doc, RESULT_FILE)
    if derr is not None:
        raise InputError("not JSON: %s" % derr)
    return dict(res)


def read_json_as(path: str, spec: Any) -> Any:
    """merge's readJSON: the file decoded as spec."""
    try:
        text = read_text(path)
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    try:
        doc = loads(text)
    except ValueError as err:
        raise InputError("%s is not the JSON expected: %s" % (path, err)) from None
    out, derr = gd.decode(doc, spec)
    if derr is not None:
        raise InputError("%s is not the JSON expected: %s" % (path, derr))
    return out


def filed_tickets(path: str) -> List[str]:
    """The references the step call ledger at path records filed: every
    executed `file` write (an exec line of verb hub.file, done, with its
    reference), in ledger order, each once. A missing ledger filed none; a
    torn line is skipped."""
    try:
        with open(path, "rb") as f:
            raw = f.read().decode("utf-8", "replace")
    except FileNotFoundError:
        return []
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    out: List[str] = []
    for text in raw.split("\n"):
        if not trim_space(text):
            continue
        try:
            doc = loads(text)
        except ValueError:
            continue
        line, derr = gd.decode(doc, LEDGER_LINE)
        if derr is not None or line["seq"] == 0:
            continue
        if line["kind"] == "exec" and line["verb"] == "hub.file" and line["outcome"] == "done" and line["ref"]:
            if line["ref"] not in out:
                out.append(line["ref"])
    return out


RULINGS = gd.Struct({"hold_rulings": gd.List(gd.Struct({"item": gd.STR, "ruling": gd.STR, "words": gd.STR}))})


def hold_rulings(path: str) -> Optional[Dict[str, Any]]:
    """collect's hold rulings as the record carries them: item -> {ruling,
    words}; None when none."""
    f = read_json_as(path, RULINGS)
    out: Dict[str, Any] = {}
    for r in f["hold_rulings"] or []:
        if r["item"]:
            out[r["item"]] = {"ruling": r["ruling"], "words": r["words"]}
    return sorted_dict(out) if out else None


def decided_result(path: str) -> str:
    """The gate step's decided result (gate-decision.json's result)."""
    d = read_json_as(path, gd.Struct({"result": gd.STR}))
    if d["result"] in (RESULT_GREEN, RESULT_RED, RESULT_EMPTY):
        return str(d["result"])
    raise InputError("its result is %s, none of green, red or empty: the gate step has not decided" % quote(d["result"]))


def same_heads(a: Optional[Mapping[str, str]], b: Optional[Mapping[str, str]]) -> bool:
    a, b = a or {}, b or {}
    if len(a) != len(b):
        return False
    return all(b.get(k, "") == v for k, v in a.items())


def green_cover(repo_dir: Callable[[str], str], mc: Mapping[str, Sequence[str]], greens: Mapping[str, str],
                tags: Mapping[str, str]) -> Optional[Dict[str, str]]:
    """The green tag covering each repo's merges of a landed item (per repo
    the tag over its newest merge: this batch's tagged commit, else an
    earlier green tag containing it) — None when a merge is under no green
    tag, lies in no task repo, or the item records none."""
    if not mc:
        return None
    out: Dict[str, str] = {}
    for repo in sorted(mc):
        d = repo_dir(repo)
        if not os.path.isdir(d):
            return None
        for c in mc[repo]:
            gc = greens.get(repo)
            if gc is not None and is_ancestor(d, c, gc):
                out[repo] = tags.get(repo, "")
                continue
            t = green_containing(d, c)
            if not t:
                return None
            out[repo] = t
    return out


def green_items(repo_dir: Callable[[str], str], snap: Mapping[str, Any], batch: str, greens: Mapping[str, str],
                tags: Mapping[str, str]) -> List[Dict[str, Any]]:
    """The landed items each of whose repos' merges a green tag covers, in
    pool order, each with the record it moves to."""
    out: List[Dict[str, Any]] = []
    for it in snap["items"] or []:
        covered = green_cover(repo_dir, merge_commits_of(it["integration"]), greens, tags)
        if covered is None:
            continue
        s: Dict[str, Any] = {"state": INTEGRATION_MERGED, "batch": batch, "green_tags": sorted_dict(covered)}
        for repo in sorted(covered):
            if repo in greens:
                s["green_tag"], s["green_commit"] = covered[repo], greens[repo]
                break
        m: Dict[str, Any] = {"wi": it["id"]}
        name = or_id(it["slug"], it["id"])
        if name:
            m["name"] = name
        m["set"] = sorted_dict(s)
        out.append(m)
    return out


def batch_record(f: Mapping[str, Any]) -> Dict[str, Any]:
    """The integration_batch hand-off: the lists and maps present only when
    non-empty, main always, tags on a green batch."""
    b: Dict[str, Any] = {"result": f["result"]}
    if f["refused"]:
        b["refused"] = f["refused"]
    if f["empty"]:
        b["empty"] = f["empty"]
    if f["hold_rulings"]:
        b["hold_rulings"] = f["hold_rulings"]
    b["main"] = sorted_dict(f["main"])
    if f["result"] == RESULT_GREEN:
        b["tags"] = sorted_dict(f["tags"])
    items = [it["id"] for it in f["items"]]
    waived = [it["id"] for it in f["items"] if it["waived"]]
    b["items"] = items
    if waived:
        b["waived"] = waived
    if f["green_items"]:
        b["green_items"] = sorted(f["green_items"])
    if f["fix_tickets"]:
        b["fix_tickets"] = sorted(f["fix_tickets"])
    b["closed_at"] = f["closed_at"]
    return b


def _exists(path: str) -> bool:
    return os.path.exists(path)


def close(inv: Invocation, args: List[str], environ: Mapping[str, str], hub: HubRunner) -> int:
    p = _parser("close")
    p.add_argument("--batch", default="", help="the batch (collect's result file)")
    p.add_argument("--gate", default="", help="the gate (gate.py's result file; absent or missing = no gate ran)")
    p.add_argument("--triage", default="", help="the triage (gate.py --triage's result file; missing = none)")
    p.add_argument("--revert", default="", help="the reverts (revert.py's result file; missing = none)")
    p.add_argument("--rulings", default="", help="collect's rulings file (rulings.json): its hold_rulings go on the record")
    p.add_argument("--decision", default="", help="full-gate's decision file (gate-decision.json): its result must agree with the gate files")
    p.add_argument("--calls", default="", help="the step call ledger (runtime-state/step-calls.jsonl; missing = none)")
    p.add_argument("--ops-out", default="", help="on exit 0 write the moves there as a hub write array (pf step hub batch)")
    p.add_argument("--record-out", default="", help="on exit 0 write the batch's record there (pf step hub wrap-attrs integration_batch)")
    ns = p.parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    ops: List[Dict[str, Any]] = []
    code = _close_run(inv, ns, environ, hub, ops)
    if code == 0:
        details = inv.details
        assert isinstance(details, dict)
        for path, value in ((ns.ops_out, ops), (ns.record_out, details["integration_batch"])):
            if not path:
                continue
            try:
                atomic.write(path, jsonout.dumps(value).encode("utf-8"), 0o644)
            except OSError as err:
                inv.summary = "%s could not be written: %s" % (path, err)
                code = 2
    if code != 0:
        for path in (ns.ops_out, ns.record_out):
            if path:
                try:
                    os.remove(path)
                except OSError:
                    pass
    return code


def _close_run(inv: Invocation, ns: argparse.Namespace, environ: Mapping[str, str], hub: HubRunner, ops: List[Dict[str, Any]]) -> int:
    if ns.rest:
        return inv.usage("unexpected arguments [%s]" % " ".join(ns.rest))
    if not ns.batch:
        return inv.usage("--batch <file> is required")
    try:
        root, repos = capsule_repos(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    try:
        snap = pool_of(ns.pool, root, hub)
    except RuntimeError as err:
        inv.summary = str(err)
        return 2
    try:
        bd = read_step_result(ns.batch, COMMAND, COLLECT_DETAILS)
    except InputError as err:
        inv.summary = "the batch %s: %s" % (ns.batch, err)
        return 2
    gd_: Optional[Dict[str, Any]] = None
    gate_res: Optional[Dict[str, Any]] = None
    gate_run = ""
    if ns.gate and _exists(ns.gate):
        try:
            gd_ = dict(read_step_result(ns.gate, GATE, GATE_DETAILS))
            if gd_["repos"] is None:
                gd_["repos"] = {}
            gate_res = read_result_file(ns.gate)
            with open(ns.gate, "rb") as fh:
                gate_run = hashlib.sha256(fh.read()).hexdigest()
        except (InputError, OSError) as err:
            inv.summary = "the gate %s: %s" % (ns.gate, err)
            return 2
    if ns.triage and _exists(ns.triage) and gate_res is not None:
        try:
            td = read_step_result(ns.triage, GATE, TRIAGE_DETAILS)
            tri_res = read_result_file(ns.triage)
        except InputError as err:
            inv.summary = "the triage %s: %s" % (ns.triage, err)
            return 2
        # a regate is the gate's result only when the triage is this gate
        # run's: it names the run and carries its round and heads — a stale
        # triage (an earlier round's, other pins, or a run an owner's rerun
        # replaced) is ignored, so its failed regate never turns a later
        # green gate red
        regate = td["regate"]
        if (regate is not None and regate["repos"] and td["gate_run"] and td["gate_run"] == gate_run
                and tri_res["round"] == gate_res["round"] and same_heads(tri_res["heads"], gate_res["heads"])):
            gd_ = dict(regate)  # every failure was flaky: the gate ran once more
    reverts: List[Dict[str, Any]] = []
    if ns.revert and _exists(ns.revert):
        try:
            rd = read_step_result(ns.revert, REVERT, REVERT_DETAILS)
        except InputError as err:
            inv.summary = "the reverts %s: %s" % (ns.revert, err)
            return 2
        for e in rd["reverts"] or []:
            if e is None or not e["done"] or not e["repos"]:
                continue
            prs = [r["revert_pr"] for r in e["repos"] if r is not None and r["revert_pr"]]
            reverts.append({"name": e["name"], "prs": prs, "wi": e["wi"]})
    fix_tickets: List[str] = []
    if ns.calls:
        try:
            fix_tickets = filed_tickets(ns.calls)
        except InputError as err:
            inv.summary = "the step call ledger %s: %s" % (ns.calls, err)
            return 2
    rulings: Optional[Dict[str, Any]] = None
    if ns.rulings:
        try:
            rulings = hold_rulings(ns.rulings)
        except InputError as err:
            inv.summary = "the rulings %s: %s" % (ns.rulings, err)
            return 2
    recorded = ""
    if ns.decision:
        try:
            recorded = decided_result(ns.decision)
        except InputError as err:
            inv.summary = "the gate decision %s: %s" % (ns.decision, err)
            return 2
    inv.heads = dict(sorted_dict(bd["heads"] or {}))
    name = or_text(bd["batch"], environ.get(stepenv.ENV_WI_ID, ""))
    f: Dict[str, Any] = {"items": bd["items"] or [], "refused": bd["refused"], "empty": bd["empty"], "main": {}, "tags": {},
                         "fix_tickets": fix_tickets, "hold_rulings": rulings, "closed_at": now_stamp(), "green_items": [],
                         "result": ""}
    greens: Dict[str, str] = {}
    if bd["refused"]:
        f["result"] = RESULT_EMPTY
    elif gd_ is None or not gd_["repos"]:
        f["result"] = RESULT_EMPTY
        if not f["empty"]:
            f["empty"] = "no gate ran"
    else:
        judged = passed = 0
        not_judged: List[str] = []
        untagged = ""
        for repo in sorted(gd_["repos"]):
            rd_ = gd_["repos"][repo]
            res = rd_["result"] if rd_ is not None else None
            if res is None or res["incomplete"]:
                not_judged.append(repo)
                continue
            judged += 1
            if not res["passed"]:
                # the heads a red gate failed on: a repo whose gate passed, or
                # could not be judged, is no red head
                f["main"][repo] = res["commit"]
                continue
            passed += 1
            if not rd_["tag"]:
                # the gate tags only when every gated repo passed: an untagged
                # pass beside a red repo is the red batch's, never a refusal
                if not untagged:
                    untagged = "%s passed the gate at %s but carries no green tag (gate.py --mark-green did not write it): the batch cannot close green" % (
                        repo, short_sha(res["commit"]))
                continue
            greens[repo] = res["commit"]
            f["tags"][repo] = rd_["tag"]
        if judged == 0:
            f["result"] = RESULT_EMPTY
            if not f["empty"]:
                f["empty"] = "the gate judged no repo (" + ", ".join(not_judged) + ")"
        elif passed == judged and untagged:
            inv.summary = untagged
            return 2
        elif passed == judged:
            f["result"] = RESULT_GREEN
            f["main"] = dict(greens)
        else:
            # a red batch tags nothing: nothing of this batch covers an item
            f["result"] = RESULT_RED
            greens, f["tags"] = {}, {}

    def repo_dir(repo: str) -> str:
        return os.path.join(root, "repos", repo)

    moves: List[Dict[str, Any]] = []
    if not bd["refused"]:
        for repo in repos:
            try:
                fetch(repo_dir(repo))  # the green tags
            except GitError as err:
                inv.summary = "%s: fetching the green tags: %s" % (repo, err)
                return 2
        moves = green_items(repo_dir, snap, name, greens, f["tags"])
        f["green_items"] = [m.get("name", "") for m in moves]
        records = {it["id"]: it["integration"] for it in snap["items"] or []}
        for m in moves:
            # the pool is the landed wrapped items: each record as read now
            try:
                rec = rules.move(STATUS_WRAPPED, records.get(m["wi"]), m["set"], m.get("name", m["wi"]))
            except rules.RuleError as err:
                inv.summary = "the move of %s to merged: %s" % (m.get("name", m["wi"]), err)
                return 2
            if rec is not None:
                ops.append(rules.write(m["wi"], rec, records[m["wi"]]))
    if recorded and recorded != f["result"]:
        # the gate step judged the batch one way and the files say another
        inv.summary = "the gate step's result is %s, but the gate files give %s (gate %s; the decision %s): close the batch on the gate's own result file — run the gate step again" % (
            recorded, f["result"], or_text(ns.gate, "not named"), ns.decision)
        return 2
    record = batch_record(f)
    if reverts:
        record["reverts"] = reverts
    inv.details = {"moves": moves, "integration_batch": sorted_dict(record)}
    summary = "the batch is %s: %d landed item(s) move to merged" % (f["result"], len(moves))
    if f["result"] == RESULT_RED:
        summary += "; red heads " + ", ".join(sorted(f["main"]))
    elif f["result"] == RESULT_EMPTY:
        summary += " (" + or_text(f["refused"], f["empty"]) + ")"
    inv.summary = summary
    return 0


# ---------------------------------------------------------------------------
# records

ASKS_JSONL = os.path.join("runtime-state", "asks.jsonl")
ASK_LINE = gd.Struct({"at": gd.STR, "step": gd.STR, "kind": gd.STR, "text": gd.STR, "by": gd.STR})
RECORD_KINDS = ("fix", "clear", "blame")


def capsule_identity(capsule_root: str) -> Dict[str, Any]:
    """The capsule's identity (capsule.json: project, bundle_name);
    RuntimeError naming why it is unreadable."""
    try:
        text = read_text(os.path.join(capsule_root, CAPSULE_JSON))
    except OSError as err:
        raise RuntimeError("the capsule's %s: %s" % (CAPSULE_JSON, gotext.os_error("open", err))) from None
    try:
        idn, derr = gd.decode(loads(text), IDENTITY)
    except ValueError as e:
        idn, derr = None, str(e)
    if derr is not None or idn is None or not idn["project"] or not idn["bundle_name"]:
        raise RuntimeError("the capsule's %s names no project and lane (bundle_name)" % CAPSULE_JSON)
    return dict(idn)


def owner_message(capsule_root: str, at: str, step: str) -> Optional[Dict[str, str]]:
    """The owner message stamped at (a reply or message line of
    runtime-state/asks.jsonl, to step when named): its words and writer;
    None when there is none. A torn line is skipped."""
    try:
        raw = read_text(os.path.join(capsule_root, ASKS_JSONL))
    except FileNotFoundError:
        return None
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    found: Optional[Dict[str, str]] = None
    for text in raw.split("\n"):
        if not trim_space(text):
            continue
        try:
            line, derr = gd.decode(loads(text), ASK_LINE)
        except ValueError:
            continue
        if derr is not None or line["kind"] not in ("reply", "message") or line["at"] != at:
            continue
        if step and line["step"] != step:
            continue
        found = {"words": line["text"], "by": line["by"] or "owner"}
    return found


def read_records_in(path: str) -> List[Dict[str, Any]]:
    """The records file: a JSON array of `file` hub writes (passed on as
    written) and record entries {item, fix|clear|blame}; InputError naming
    a malformed entry."""
    try:
        doc = loads(read_text(path))
    except OSError as err:
        raise InputError(gotext.os_error("open", err)) from None
    except ValueError as err:
        raise InputError("not JSON: %s" % err) from None
    if not isinstance(doc, list):
        raise InputError("not a JSON array of entries")
    seen: Dict[str, int] = {}
    for i, e in enumerate(doc):
        at = "entry %d" % (i + 1)
        if not isinstance(e, dict):
            raise InputError("%s is no JSON object" % at)
        if e.get("verb") == "file":
            continue
        item = e.get("item")
        kinds = [k for k in RECORD_KINDS if k in e]
        if not isinstance(item, str) or not item or len(kinds) != 1 or set(e) != {"item", kinds[0]}:
            raise InputError("%s is neither a file write nor {item, fix|clear|blame}" % at)
        v = e[kinds[0]]
        if kinds[0] == "clear" and (not isinstance(v, str) or not v):
            raise InputError("%s: clear names the stamp (at) of the owner message ruling the hold cleared" % at)
        if kinds[0] in ("fix", "blame") and (not isinstance(v, list) or not strings_of(v) or len(strings_of(v)) != len(v)):
            raise InputError("%s: %s names the fix tickets, a non-empty list of references (an id, a slug or key:<K>)" % (at, kinds[0]))
        if item in seen:
            raise InputError("%s: %s already has a record entry (entry %d)" % (at, item, seen[item]))
        seen[item] = i + 1
    return [dict(e) for e in doc]


def _records_parser() -> Parser:
    p = Parser(COMMAND + " records")
    p.add_argument("--in", dest="in_", default="", help="the records file: file writes and {item, fix|clear|blame} entries")
    p.add_argument("--ops-out", default="", help="on exit 0 write the hub writes there (pf step hub batch)")
    p.add_argument("--pins", default="", help="the gate's pins (revert.py's result file): a blame's red_heads")
    p.add_argument("--capsule", default=".", help="the batch capsule root (capsule.json, runtime-state/asks.jsonl)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    p.add_argument("rest", nargs=argparse.REMAINDER, help=argparse.SUPPRESS)
    return p


def records(inv: Invocation, args: List[str], environ: Mapping[str, str], hub: HubRunner) -> int:
    ns = _records_parser().parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    ops: List[Dict[str, Any]] = []
    code = _records_run(inv, ns, environ, hub, ops)
    if code == 0:
        try:
            atomic.write(ns.ops_out, jsonout.dumps(ops).encode("utf-8"), 0o644)
        except OSError as err:
            inv.summary = "%s could not be written: %s" % (ns.ops_out, err)
            code = 2
    if code != 0 and ns.ops_out:
        try:
            os.remove(ns.ops_out)
        except OSError:
            pass
    return code


def _records_run(inv: Invocation, ns: argparse.Namespace, environ: Mapping[str, str], hub: HubRunner, ops: List[Dict[str, Any]]) -> int:
    if ns.rest:
        return inv.usage("unexpected arguments [%s]" % " ".join(ns.rest))
    if not ns.in_ or not ns.ops_out:
        return inv.usage("--in <file> and --ops-out <file> are required")
    batch = environ.get(stepenv.ENV_WI_ID, "")
    if not batch:
        return inv.usage("records needs PF_WI_ID (the batch the records name): it runs inside the batch's step")
    root = os.path.abspath(ns.capsule)
    try:
        entries = read_records_in(ns.in_)
    except InputError as err:
        inv.summary = "the records %s: %s" % (ns.in_, err)
        return 2
    heads: Dict[str, str] = {}
    if any("blame" in e for e in entries):
        if not ns.pins:
            return inv.usage("a blame entry needs --pins (the gate's pins: its red_heads)")
        try:
            heads = dict(read_result_file(ns.pins)["heads"] or {})
        except InputError as err:
            inv.summary = "the pins %s: %s" % (ns.pins, err)
            return 2
    lister = HubLister(hub)
    batches: Optional[List[Dict[str, Any]]] = None
    written: List[Dict[str, Any]] = []
    unchanged: List[str] = []
    refused: List[Dict[str, str]] = []
    now = now_stamp()
    for e in entries:
        if e.get("verb") == "file":
            ops.append(e)
            continue
        ref = e["item"]
        kind = next(k for k in RECORD_KINDS if k in e)
        try:
            wi = lister.get(ref)
            if wi is None:
                raise rules.RuleError(rules.NO_HANDOFF, "%s is no work item of the project" % ref)
            name = or_id(wi["slug"], wi["id"])
            rec = rules.record_of(wi)
            per_owner = ""
            if kind == "fix":
                if not rules.held(rec) or wi["status"] != STATUS_WRAPPED:
                    raise rules.RuleError(rules.NOT_HELD, "%s holds no default branch: a fix ticket is registered only on a hold" % name)
                assert rec is not None
                fixes = strings_of(rec.get("fix_tickets"))
                fixes += [r for r in e["fix"] if r not in fixes]
                new = rules.move(wi["status"], rec, {"state": rules.LANDED, "fix_tickets": fixes}, name)
            elif kind == "clear":
                per_owner = e["clear"]
                msg = owner_message(root, per_owner, environ.get(stepenv.ENV_STEP, ""))
                if msg is None:
                    raise rules.RuleError(rules.NO_REASON, "%s: no owner message stamped %s (runtime-state/asks.jsonl) rules the hold cleared" % (name, per_owner))
                if batches is None:
                    lane = capsule_identity(root)["bundle_name"]
                    batches = lister.list(bundle=lane, limit=BATCHES_LIMIT)
                rules.refuse_while_batches_run(batches, batch)
                new = rules.clear_hold(wi["status"], rec, msg["words"], msg["by"], per_owner, batch, now, name)
            else:
                rr = _number((rec or {}).get("red_rounds")) or 0.0
                new = rules.move(wi["status"], rec, {"state": rules.LANDED, "batch": batch, "red_rounds": int(rr) + 1,
                                                     "red_heads": sorted_dict(heads), "fix_tickets": list(e["blame"])}, name)
        except rules.RuleError as err:
            refused.append({"item": ref, "kind": kind, "reason": err.reason, "why": str(err)})
            continue
        except (HubError, RuntimeError, InputError) as err:
            inv.details = {"writes": written, "unchanged": unchanged, "refused": refused}
            inv.summary = "%s %s: not judged: %s" % (kind, ref, err)
            return 2
        if new is None:
            unchanged.append(wi["id"])
            continue
        assert rec is not None  # a write follows a hand-off the rules read
        ops.append(rules.write(wi["id"], new, rec, per_owner))
        written.append({"item": wi["id"], "kind": kind, "state": new["state"]})
    inv.details = {"writes": written, "unchanged": unchanged, "refused": refused}
    if refused:
        del ops[:]
        inv.summary = "refused, nothing written: " + "; ".join("%s %s: %s" % (r["kind"], r["item"], r["why"]) for r in refused)
        return 1
    inv.summary = "%d record write(s) and %d file write(s)%s" % (
        len(written), sum(1 for o in ops if o.get("verb") == "file"),
        ("; already carried: " + ", ".join(unchanged)) if unchanged else "")
    return 0


USAGE = (
    "usage: integrate.py collect [--pool FILE] [--capsule DIR] [--json] [--out PATH]\n"
    "       integrate.py close --batch FILE [--pool FILE] [--gate FILE] [--triage FILE] [--revert FILE]"
    " [--rulings FILE] [--decision FILE] [--calls FILE] [--ops-out FILE] [--record-out FILE] [--capsule DIR] [--json] [--out PATH]\n"
    "       integrate.py records --in FILE --ops-out FILE [--pins FILE] [--capsule DIR] [--json] [--out PATH]"
)


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None,
         hub: Optional[HubRunner] = None) -> int:
    """The script: argv without the program name; environ and hub are a
    test's (default: the process's, `pf --json step hub`)."""
    args = list(sys.argv[1:] if argv is None else argv)
    env = os.environ if environ is None else environ
    if args and args[0] in ("-h", "--help", "help"):
        print(__doc__)
        return 0
    inv = Invocation(COMMAND, args, env)
    if not args:
        return inv.usage("a subcommand is required: collect | close | records")
    if args[0] not in ("collect", "close", "records"):
        return inv.usage("%s is no subcommand: collect | close | records" % quote(args[0]))
    runner_hub = hub if hub is not None else hub_runner(env)
    try:
        if args[0] == "collect":
            code = collect(inv, args[1:], env, runner_hub)
        elif args[0] == "records":
            code = records(inv, args[1:], env, runner_hub)
        else:
            code = close(inv, args[1:], env, runner_hub)
    except SystemExit as e:
        return int(e.code or 0) if not isinstance(e.code, str) else 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
