"""release.py — the release lane's green-batch resolver (polyforge#154).

The release step's one pre-written command: it finds the project's most
recent green integrate batch and decides what the lane releases — its
pinned commits, never a branch head. It reads the batches through the
step's hub (``pf --json step hub list``, held to the step's ``hub``
verbs), validates the batch's pins against the capsule's clones and the
owner pin's release tags, and answers one of:

- exit 0, verdict pass, decision ``fresh``: the owner pin carries no
  release tag; the step records the pre-release list and runs the
  procedure with the written pins file;
- exit 0, verdict pass, decision ``resume``: the owner pin carries
  exactly one release tag ``V`` — the same release resumed, whoever
  claimed it (a version is claimed once and never burned; never a second
  version of one commit);
- exit 1, verdict fail: a named refusal — ``PF-RELEASE-NO_GREEN_BATCH``,
  ``PF-RELEASE-ALREADY_RELEASED``, ``PF-RELEASE-PIN_OFF_DEFAULT``,
  ``PF-RELEASE-UNPINNED_OWNER``, ``PF-RELEASE-AMBIGUOUS_OWNER`` — the
  summary begins with the code;
- exit 2, not judged: an unreadable input (the hub, a batch record, a
  clone), never a refusal.

On a pass it writes the pins file (``--pins-out``), the procedure's whole
input: ``{"batch": "<batch id>", "repos": {"<repo>": "<sha>", ...}}``
plus ``"red_batches_after": [<batch id>, ...]`` when red batches closed
after the green one (the release notes name them, Q4 B). A repo of the
capsule the batch does not cover is not part of the release (named in the
details); the owner repo must be covered (Q5 B).

Usage (from the capsule root)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/release.py" --capsule <dir> \\
        [--batches <file>] [--pre-release <file>] [--pins-out <file>] \\
        [--deadline D] [--json] [--out <file>]

``--batches`` names a snapshot file (the hub list's answer: a JSON
object ``{"items": [...]}`` or a bare array) instead of the hub — the
tests' and a re-run's input. ``--pre-release`` defaults to
``<capsule>/artifacts/pre-release-tags.txt`` and is only read: a release
tag that file lacks, on a commit that is not the owner pin, is someone
else's new claim — the guard that refuses it lives here.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import datetime  # noqa: E402
import json  # noqa: E402
import re  # noqa: E402
from typing import Any, Callable, Dict, List, Mapping, Optional, Sequence, Tuple  # noqa: E402

from pfstep import atomic, hub, invoke  # noqa: E402
from pfstep.git import GitError, git as ggit, run as grun  # noqa: E402

COMMAND = "release.py"

# The one hub read: every wrapped batch of the integrate lane.
LIST_ARGS = ["list", "--status", "wrapped", "--bundle", "integrate", "--limit", "1000"]

# A pin is one full commit id: 40 lowercase hex digits.
SHA_RE = re.compile(r"^[0-9a-f]{40}$")

# A release tag: `v` then a digit (v1.2.3, v0.2.0-internal.4 alike).
RELEASE_TAG_RE = re.compile(r"^v[0-9]")

# The hub's runner: one `pf --json step hub <args>` call answered as stdout.
HubRunner = Callable[[List[str]], bytes]

GREEN, RED = "green", "red"


class Refusal(Exception):
    """A computed refusal: its message is the result's summary (exit 1)."""

    def __init__(self, message: str) -> None:
        super().__init__(message)
        self.message = message


class NotJudged(Exception):
    """An input that cannot be read: its message is the summary (exit 2)."""

    def __init__(self, message: str) -> None:
        super().__init__(message)
        self.message = message


def stamp_of(text: str) -> datetime.datetime:
    """A batch's closed_at as a comparable UTC time; NotJudged when not
    an RFC 3339 stamp (a record that cannot be ordered cannot pick the
    latest batch)."""
    try:
        t = datetime.datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        raise NotJudged("%s is not an RFC 3339 stamp" % text) from None
    if t.tzinfo is None:
        raise NotJudged("%s carries no zone" % text)
    return t.astimezone(datetime.timezone.utc)


def capsule_repos(root: str) -> List[str]:
    """The capsule's repos, one level under <root>/repos, name order."""
    base = os.path.join(root, "repos")
    if not os.path.isdir(base):
        return []
    return sorted(n for n in os.listdir(base) if os.path.isdir(os.path.join(base, n)))


def owner_among(root: str, repos: Sequence[str]) -> List[str]:
    """The repos carrying .polyforge/release.sh, name order."""
    return [r for r in repos if os.path.isfile(os.path.join(root, "repos", r, ".polyforge", "release.sh"))]


def read_batches(reader: HubRunner) -> List[Mapping[str, Any]]:
    """The wrapped integrate batches through the step's hub."""
    raw = reader(list(LIST_ARGS))
    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise NotJudged("the hub list's answer is not JSON: %s" % err) from None
    if isinstance(doc, dict):
        doc = doc.get("items")
    if not isinstance(doc, list):
        raise NotJudged("the hub list's answer is no work item list")
    return [it for it in doc if isinstance(it, dict)]


def read_batches_file(path: str) -> List[Mapping[str, Any]]:
    """The batches from a snapshot file (--batches): the hub list's answer
    as a JSON object ``{"items": [...]}`` or a bare array."""
    try:
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
    except (OSError, ValueError) as err:
        raise NotJudged("the batches file %s: %s" % (path, err)) from None
    if isinstance(doc, dict):
        doc = doc.get("items")
    if not isinstance(doc, list):
        raise NotJudged("the batches file %s is no work item list" % path)
    return [it for it in doc if isinstance(it, dict)]


def batch_of(item: Mapping[str, Any]) -> Tuple[str, str, Dict[str, str], str, int]:
    """One wrapped item's batch record: (id, result, main, closed_at, seq);
    NotJudged when the record is unreadable — a shape the resolver cannot
    read never silently selects or skips a batch."""
    wid = item.get("id")
    if not isinstance(wid, str) or not wid:
        raise NotJudged("a wrapped integrate item carries no id")
    attrs = item.get("attrs")
    attrs = attrs if isinstance(attrs, dict) else {}
    rec = attrs.get("integration_batch")
    if not isinstance(rec, dict):
        raise NotJudged("wrapped integrate item %s carries no integration_batch record" % wid)
    result = rec.get("result")
    # empty is a closed batch that gated nothing: it selects nothing; a
    # result this resolver does not know refuses rather than guesses
    if not isinstance(result, str) or result not in (GREEN, RED, "empty"):
        raise NotJudged("batch %s's record: result %r is none of green, red, empty" % (wid, result))
    main: Dict[str, str] = {}
    raw_main = rec.get("main")
    if result == "empty":
        # an empty batch gated nothing: its main is empty and pins nothing
        raw_main = raw_main if isinstance(raw_main, dict) else {}
    elif not isinstance(raw_main, dict) or not raw_main:
        raise NotJudged("batch %s's record: main is empty or no map" % wid)
    for repo, sha in raw_main.items():
        if not isinstance(sha, str) or not SHA_RE.match(sha):
            raise NotJudged("batch %s's record: %s is not a full commit id (40 lowercase hex): %r" % (wid, repo, sha))
        main[str(repo)] = sha
    closed_at = rec.get("closed_at")
    if not isinstance(closed_at, str) or not closed_at:
        raise NotJudged("batch %s's record: closed_at is missing" % wid)
    stamp_of(closed_at)
    seq = item.get("seq", 0)
    seq = seq if isinstance(seq, int) else 0
    return wid, result, main, closed_at, seq


def classify(items: Sequence[Mapping[str, Any]]) -> Tuple[List[Tuple[datetime.datetime, int, str, Dict[str, str], str]], List[Tuple[datetime.datetime, int, str]]]:
    """The wrapped batches as (greens, reds): a green is (closed_at, seq,
    id, main, closed_at text) orderable latest-first, a red (closed_at,
    seq, id). Results that are neither green nor red select nothing."""
    greens: List[Tuple[datetime.datetime, int, str, Dict[str, str], str]] = []
    reds: List[Tuple[datetime.datetime, int, str]] = []
    for it in items:
        wid, result, main, closed_at, seq = batch_of(it)
        when = stamp_of(closed_at)
        if result == GREEN:
            greens.append((when, seq, wid, main, closed_at))
        else:
            reds.append((when, seq, wid))
    greens.sort(key=lambda g: (g[0], g[1]), reverse=True)
    reds.sort(key=lambda r: (r[0], r[1]), reverse=True)
    return greens, reds


def default_branch(clone: str, repo: str, env: Mapping[str, str]) -> str:
    """The clone's origin default branch (origin/HEAD), `main`-shaped;
    NotJudged when it cannot be resolved — a branch is never guessed."""
    try:
        ref = ggit(clone, "symbolic-ref", "--short", "refs/remotes/origin/HEAD", environ=env).strip()
    except GitError as err:
        raise NotJudged("%s: the default branch cannot be resolved from refs/remotes/origin/HEAD (%s) — refusing to guess one" % (repo, err)) from None
    if not ref.startswith("origin/") or len(ref) <= len("origin/"):
        raise NotJudged("%s: refs/remotes/origin/HEAD names %r, not a branch of origin" % (repo, ref or "(nothing)"))
    return ref[len("origin/"):]


def pin_is_on_default(clone: str, repo: str, sha: str, env: Mapping[str, str]) -> str:
    """The default branch the pin sits on (fetched first); Refusal when the
    pin is no longer on the origin default branch's history — a commit the
    clone cannot resolve after its fetch is as off-branch as a side
    commit: the batch's pin is gone either way."""
    try:
        ggit(clone, "fetch", "origin", environ=env)
    except GitError as err:
        raise NotJudged("%s: fetching origin: %s" % (repo, err)) from None
    branch = default_branch(clone, repo, env)
    c = grun(clone, "merge-base", "--is-ancestor", sha, "refs/remotes/origin/" + branch, environ=env)
    if c.exit_code != 0:
        raise Refusal("PF-RELEASE-PIN_OFF_DEFAULT: batch pins %s at %s, which is not on the history of origin/%s (branch %s) — the green batch's commit is gone from the default branch; a new green batch is needed" % (repo, short(sha), branch, branch))
    return branch


def release_tags(clone: str, env: Mapping[str, str]) -> Tuple[Dict[str, str], List[str]]:
    """The owner repo's remote release tags: (name -> commit, peeled first)
    and the raw ls-remote lines (the pre-release list's comparison)."""
    try:
        raw = ggit(clone, "ls-remote", "--tags", "origin", environ=env)
    except GitError as err:
        raise NotJudged("reading the owner repo's remote tags (git ls-remote --tags origin): %s" % err) from None
    at: Dict[str, str] = {}
    lines: List[str] = []
    for line in raw.splitlines():
        fields = line.split()
        if len(fields) != 2:
            continue
        sha, ref = fields
        lines.append(line)
        name = ref[len("refs/tags/"):] if ref.startswith("refs/tags/") else ""
        if not RELEASE_TAG_RE.match(name):
            continue
        plain = name
        peeled = name.endswith("^{}")
        if peeled:
            plain = name[:-3]
        # a peeled line is the commit; a direct line of a lightweight tag is
        if peeled or plain not in at:
            at[plain] = sha
    return at, lines


def short(text: str) -> str:
    """A commit id as a summary names it: its first 8 digits."""
    return text[:8]


def _parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog=COMMAND, add_help=False)
    p.add_argument("--capsule", default="")
    p.add_argument("--batches", default="")
    p.add_argument("--pre-release", default="")
    p.add_argument("--pins-out", default="")
    p.add_argument("--deadline", type=invoke.duration_flag, default=0)
    p.add_argument("--json", action="store_true")
    p.add_argument("--out", default="")
    return p


def _run(inv: invoke.Invocation, ns: argparse.Namespace, batches: Sequence[Mapping[str, Any]],
         env: Mapping[str, str]) -> int:
    if not ns.capsule:
        return inv.usage("--capsule <dir> is required")
    root = ns.capsule
    repos = capsule_repos(root)
    owners = owner_among(root, repos)
    if not owners:
        inv.summary = "no repo in the capsule carries .polyforge/release.sh — a release work item without a procedure is an error"
        return 1
    if len(owners) > 1:
        named = ", ".join("repos/%s" % o for o in owners)
        inv.summary = "PF-RELEASE-AMBIGUOUS_OWNER: %s all carry .polyforge/release.sh — the release owner is not unique, a human decides" % named
        return 1
    owner = owners[0]

    greens, reds = classify(batches)
    if not greens:
        inv.summary = "PF-RELEASE-NO_GREEN_BATCH: the project has no wrapped integrate batch judged green — there is nothing to release (a release object is never invented; run an integrate batch to a green close first)"
        return 1
    green_when, green_seq, wid, main, closed_at = greens[0]
    red_after = sorted(rid for (when, seq, rid) in reds if (when, seq) > (green_when, green_seq))

    for repo in main:
        if repo not in repos:
            inv.summary = "batch %s's record: main names repo '%s', no clone of it is in the capsule — the batch and the capsule do not agree" % (wid, repo)
            return 2
    if owner not in main:
        inv.summary = "PF-RELEASE-UNPINNED_OWNER: green batch %s's main names no commit for the owner repo %s — the batch did not judge the repo whose procedure releases" % (wid, owner)
        return 1
    excluded = [r for r in repos if r not in main]

    branches: Dict[str, str] = {}
    for repo in sorted(main):
        branches[repo] = pin_is_on_default(os.path.join(root, "repos", repo), repo, main[repo], env)

    owner_pin = main[owner]
    clone = os.path.join(root, "repos", owner)
    tags, lines = release_tags(clone, env)
    on_pin = sorted(t for t, sha in tags.items() if sha == owner_pin)
    pre_path = ns.pre_release or os.path.join(root, "artifacts", "pre-release-tags.txt")
    if os.path.isfile(pre_path):
        try:
            with open(pre_path, encoding="utf-8") as f:
                kept = f.read().splitlines()
        except OSError as err:
            raise NotJudged("the pre-release list %s: %s" % (pre_path, err)) from None
        kept_set = set(kept)
        for name, sha in sorted(tags.items()):
            if sha == owner_pin:
                continue
            for line in lines:
                fields = line.split()
                if len(fields) == 2 and fields[0] == sha and ("refs/tags/" + name) in fields[1]:
                    if line not in kept_set:
                        raise Refusal("PF-RELEASE-ALREADY_RELEASED: release tag %s (at %s) is not on the owner pin and was not in the recorded pre-release list — a release was claimed elsewhere since this work item recorded its list; a human decides" % (name, short(sha)))
    if len(on_pin) > 1:
        inv.summary = "PF-RELEASE-ALREADY_RELEASED: the owner pin %s carries more than one release tag: %s — no single version to resume, a human decides" % (short(owner_pin), ", ".join(on_pin))
        return 1
    decision, version = ("fresh", "")
    if on_pin:
        decision, version = "resume", on_pin[0]

    if ns.pins_out:
        doc: Dict[str, Any] = {"batch": wid, "repos": {r: main[r] for r in sorted(main)}}
        if red_after:
            doc["red_batches_after"] = red_after
        try:
            atomic.write(ns.pins_out, (json.dumps(doc, indent=2, sort_keys=True) + "\n").encode("utf-8"), 0o644)
        except OSError as err:
            inv.summary = "the pins file %s: %s" % (ns.pins_out, err)
            return 2

    inv.heads = {r: main[r] for r in sorted(main)}
    n = len(main)
    if decision == "resume":
        inv.summary = "green batch %s (closed %s): owner %s at %s, %d repo%s pinned; the owner pin carries %s — resume %s, the same release (never a second version of one commit)" % (
            wid, closed_at, owner, short(owner_pin), n, "" if n == 1 else "s", version, version)
    else:
        inv.summary = "green batch %s (closed %s): owner %s at %s, %d repo%s pinned; the owner pin carries no release tag — a fresh release" % (
            wid, closed_at, owner, short(owner_pin), n, "" if n == 1 else "s")
    details: Dict[str, Any] = {"owner": owner, "owner_pin": owner_pin, "batch": wid, "batch_closed_at": closed_at,
                               "pins": dict(inv.heads), "decision": decision,
                               "red_batches_after": red_after, "default_branches": branches}
    if excluded:
        details["excluded_repos"] = excluded
    if version:
        details["version"] = version
    inv.details = details
    return 0


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None,
         batches_read: Optional[HubRunner] = None) -> int:
    """The script: argv without the program name; environ and batches_read
    are a test's (default: the process's, `pf --json step hub list`)."""
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] in ("-h", "--help", "help"):
        print(__doc__)
        return 0
    env = dict(os.environ if environ is None else environ)
    inv = invoke.Invocation(COMMAND, args, env)
    ns = _parser().parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    if not ns.capsule:
        return inv.usage("--capsule <dir> is required")
    try:
        try:
            if ns.batches:
                batches = read_batches_file(ns.batches)
            elif batches_read is not None:
                batches = read_batches(batches_read)
            else:
                batches = read_batches(hub.runner(env))
        except (RuntimeError, hub.HubError) as err:
            raise NotJudged("the batches could not be read through the step's hub: %s" % err) from None
        code = _run(inv, ns, batches, env)
    except Refusal as r:
        inv.summary = r.message
        code = 1
    except NotJudged as nj:
        inv.summary = nj.message
        code = 2
    return inv.finish(code)


if __name__ == "__main__":
    raise SystemExit(main())
