"""The integration lane's record rules (workflow contract 1).

A work item the develop lane merged into the default branch wraps with
its hand-off record in attrs ``integration`` (``state: landed``, ``repos``,
``merge_commits``, ``waived``). aihub keeps no rule about it: it writes a
top-level attrs key whole, fenced by the caller's ``if``. The integrate
lane's scripts build every write of the record here, from the record as
the step's hub reads it now:

- a move goes only from ``landed`` — to ``landed`` again (a fix round, an
  attribution), ``merged`` (a green tag covers it) or ``reverted``; a
  record that already carries the move (a resumed step making it again)
  needs no write;
- every write replaces the whole record and is fenced on the whole record
  as it was read (compare and swap): a write built from a read that another
  write has changed since — another writer's, or a second write of the
  same round from the same read — is refused ``CORE-409``, never applied
  over it, so no write drops another's keys (aihub merges nothing);
- the develop lane's keys (``repos``, ``merge_commits``, ``waived``) are
  never rewritten, and a move never writes ``hold_cleared``;
- ``landed -> merged`` names the green tag it is on (``green_tag`` or
  ``green_tags``);
- only a wrapped work item carries a hand-off;
- a hold — a landed record a red batch blamed (``red_rounds > 0`` or a
  ``fix_tickets`` entry) — is cleared only on the owner's word, to
  ``merged`` with its ``hold_cleared`` record ``{reason, by, at,
  answered_by, answered_at, cleared_in}``, and never while another batch
  of the lane is unfinished (it may be acting on the hold).

A refusal is ``RuleError``, its ``reason`` the stable name of the rule.
"""

import copy
from typing import Any, Dict, List, Mapping, Optional, Sequence

KEY = "integration"

STATUS_WRAPPED = "wrapped"
LANDED = "landed"
MERGED = "merged"
REVERTED = "reverted"
# PUBLISHED is the record of a develop work item whose lane does not merge
# its PRs (the merge step skipped; published.py): never a move of this
# lane, and never in its pool (it takes landed records only).
PUBLISHED = "published"

# MOVES is the transition table: the state a record moves from, to the
# states it may move to.
MOVES: Dict[str, Sequence[str]] = {LANDED: (LANDED, MERGED, REVERTED)}

# HANDOFF_KEYS are the develop lane's keys of the record.
HANDOFF_KEYS = ("repos", "merge_commits", "waived")

# The batches of the lane that are finished.
FINISHED = ("wrapped", "failed", "canceled")

# The reasons of a refusal.
NO_STATE = "NO_STATE"
NOT_A_MOVE = "INTEGRATION_MOVE"
LANE_KEY = "INTEGRATION_KEY"
FENCED = "INTEGRATION_FENCE"
NOT_WRAPPED = "NOT_WRAPPED"
NO_HANDOFF = "NO_HANDOFF"
NOT_HELD = "NOT_HELD"
NO_REASON = "NO_REASON"
BATCH_RUNNING = "BATCH_RUNNING"
BATCH_NOT_RUNNING = "BATCH_NOT_RUNNING"


class RuleError(ValueError):
    """A write the lane's rules refuse; reason names the rule."""

    def __init__(self, reason: str, message: str) -> None:
        super().__init__(message)
        self.reason = reason


def record_of(item: Mapping[str, Any]) -> Optional[Dict[str, Any]]:
    """A work item's integration record (None when it carries none)."""
    attrs = item.get("attrs")
    rec = attrs.get(KEY) if isinstance(attrs, dict) else None
    return dict(rec) if isinstance(rec, dict) else None


def _number(v: Any) -> float:
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        return 0.0
    return float(v)


def strings_of(v: Any) -> List[str]:
    """v as a list of non-empty strings."""
    if not isinstance(v, list):
        return []
    return [x for x in v if isinstance(x, str) and x]


def held(record: Optional[Mapping[str, Any]]) -> bool:
    """A landed record holds the green tag: a red batch blamed it."""
    if not record or record.get("state") != LANDED:
        return False
    return _number(record.get("red_rounds")) > 0 or len(strings_of(record.get("fix_tickets"))) > 0


def green_tagged(changes: Mapping[str, Any]) -> bool:
    """Whether a move names the green tag it is on."""
    t = changes.get("green_tag")
    if isinstance(t, str) and t.strip():
        return True
    tags = changes.get("green_tags")
    return isinstance(tags, dict) and len(tags) > 0


def _carries(record: Mapping[str, Any], changes: Mapping[str, Any]) -> bool:
    for k, v in changes.items():
        if v is None:
            if k in record:
                return False
        elif record.get(k) != v:
            return False
    return True


def _handoff(status: str, record: Optional[Mapping[str, Any]], name: str) -> Dict[str, Any]:
    if status != STATUS_WRAPPED:
        raise RuleError(NOT_WRAPPED, "%s is %s: only a wrapped work item carries an integration hand-off" % (name, status or "of no status"))
    if record is None:
        raise RuleError(NO_HANDOFF, "%s carries no integration hand-off" % name)
    return copy.deepcopy(dict(record))


def move(status: str, record: Optional[Mapping[str, Any]], changes: Mapping[str, Any], name: str = "the work item") -> Optional[Dict[str, Any]]:
    """The record after a move of the lane (changes' keys set, a None
    value deleting its key) — None when the record already carries it. A
    move the rules refuse is RuleError."""
    to = changes.get("state")
    if not isinstance(to, str) or not to:
        raise RuleError(NO_STATE, "the move of %s names no state" % name)
    if to not in MOVES[LANDED]:
        raise RuleError(NOT_A_MOVE, "integration %s -> %s is not a move of the integration lane" % (LANDED, to))
    for k in sorted(changes):
        if k in HANDOFF_KEYS:
            raise RuleError(LANE_KEY, "integration.%s is the develop lane's hand-off; the integration lane never rewrites it" % k)
        if k == "hold_cleared":
            raise RuleError(LANE_KEY, "integration.hold_cleared is written only by a hold's clear, on the owner's word")
    if to == MERGED and not green_tagged(changes):
        raise RuleError(NOT_A_MOVE, "landed -> merged needs the green tag the move is on (green_tag or green_tags); a hold is cleared only on the owner's word")
    rec = _handoff(status, record, name)
    cur = rec.get("state")
    if cur != LANDED:
        if _carries(rec, changes):
            return None
        raise RuleError(FENCED, "the integration record of %s is %s, not %s: another move landed first" % (name, cur, LANDED))
    for k, v in changes.items():
        if v is None:
            rec.pop(k, None)
        else:
            rec[k] = copy.deepcopy(v)
    return rec


def clear_hold(status: str, record: Optional[Mapping[str, Any]], words: str, answered_by: str, answered_at: str, batch: str, at: str,
               name: str = "the work item") -> Optional[Dict[str, Any]]:
    """The record after its hold is cleared on the owner's word — merged,
    with hold_cleared {reason: the words, by: the owner who ruled, at, the
    message's writer and stamp as answered_by and answered_at, cleared_in:
    the batch} — None when this batch cleared it already. A record that
    holds nothing is RuleError."""
    reason = words.strip()
    if not reason:
        raise RuleError(NO_REASON, "a hold is cleared with a reason: the owner's words")
    rec = _handoff(status, record, name)
    hc = rec.get("hold_cleared")
    if rec.get("state") == MERGED and isinstance(hc, dict) and batch and hc.get("cleared_in") == batch:
        return None
    if not held(rec):
        raise RuleError(NOT_HELD, "%s holds no default branch: only a landed item a red batch blamed can have its hold cleared" % name)
    by = answered_by.strip() or "owner"
    note: Dict[str, Any] = {"reason": reason, "by": by, "at": at}
    for k, v in (("answered_by", by), ("answered_at", answered_at.strip()), ("cleared_in", batch.strip())):
        if v:
            note[k] = v
    rec["state"], rec["hold_cleared"] = MERGED, note
    return rec


def refuse_while_batches_run(batches: Sequence[Mapping[str, Any]], batch: str) -> None:
    """Refuse a hold's clear while a batch of the lane other than batch is
    unfinished (it may be acting on the hold); batch (an id or a slug) must
    be one of the unfinished ones itself. batches are the lane's work
    items ({id, slug, status})."""
    running: List[str] = []
    self_running = False
    for b in batches:
        if b.get("status") in FINISHED:
            continue
        if batch and batch in (b.get("id"), b.get("slug")):
            self_running = True
            continue
        running.append("%s (%s)" % (b.get("slug") or b.get("id"), b.get("status")))
    if not self_running:
        raise RuleError(BATCH_NOT_RUNNING, "%s is no running batch of the lane: only a running batch clears a hold in its own name" % (batch or "no batch"))
    if running:
        raise RuleError(BATCH_RUNNING, "the batch %s of the lane is running and may be acting on this hold: clear it once that batch has ended" % ", ".join(sorted(running)))


def fence(read: Mapping[str, Any]) -> Dict[str, Any]:
    """A write's condition: attrs integration is still exactly read, the
    record the write was built from."""
    return {KEY: copy.deepcopy(dict(read))}


def write(target: str, value: Mapping[str, Any], read: Mapping[str, Any], per_owner: str = "") -> Dict[str, Any]:
    """The hub write of a record (``pf step hub batch``): attrs integration
    replaced whole by value, fenced on the whole record as read (fence);
    per_owner the stamp of the owner message it carries out."""
    op: Dict[str, Any] = {"verb": "attrs", "target": target, "name": KEY, "value": dict(value), "if": fence(read)}
    if per_owner:
        op["per_owner"] = per_owner
    return op
