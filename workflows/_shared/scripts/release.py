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
after the green one (the release notes name them). A repo of the capsule
the batch does not cover is not part of the release (named in the
details); the owner repo must be covered.

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

import argparse
import os
import sys
from typing import Any, Callable, List, Mapping, Optional

import pfstep  # noqa: F401
from pfstep import invoke

COMMAND = "release.py"


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None,
         batches_read: Optional[Callable[[], List[Mapping[str, Any]]]] = None) -> int:
    """The script: argv without the program name; environ and batches_read
    are a test's (default: the process's, `pf --json step hub list`)."""
    args = list(sys.argv[1:] if argv is None else argv)
    env = dict(os.environ if environ is None else environ)
    inv = invoke.Invocation(COMMAND, args, env)
    p = argparse.ArgumentParser(prog=COMMAND, add_help=False)
    p.add_argument("--capsule", default="")
    p.add_argument("--batches", default="")
    p.add_argument("--pre-release", default="")
    p.add_argument("--pins-out", default="")
    p.add_argument("--deadline", type=invoke.duration_flag, default=0)
    p.add_argument("--json", action="store_true")
    p.add_argument("--out", default="")
    ns = p.parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    inv.summary = "not implemented yet"
    return inv.finish(2)


if __name__ == "__main__":
    raise SystemExit(main())
