"""A script's reads of the hub: ``pf --json step hub <args>``.

A step reaches aihub only through the daemon — ``pf step hub``, scoped to
the step's project and held to the step's hub whitelist — so a script holds
no credential: it runs the pf on its environment's PATH (the step's own
pf, which reaches the daemon) under that environment, bounded by
``TIMEOUT``. A refusal the daemon typed (its last stderr line a JSON
object with a ``code``) is ``HubError``; any other failure is
``RuntimeError`` naming the call.
"""

import json
import shutil
from typing import Callable, List, Mapping

from . import proc

# TIMEOUT bounds one call (seconds).
TIMEOUT = 600.0

# Runner runs one call (its arguments after ``step hub``) and answers stdout.
Runner = Callable[[List[str]], bytes]


class HubError(Exception):
    """A refusal the daemon answered (aihub's own passes through it): its
    typed code and detail."""

    def __init__(self, code: str, detail: str) -> None:
        super().__init__("%s: %s" % (code, detail) if code else detail)
        self.code = code
        self.detail = detail


def run(args: List[str], environ: Mapping[str, str]) -> bytes:
    """``pf --json step hub <args>`` under environ; its stdout."""
    env = dict(environ)
    pf = shutil.which("pf", path=env.get("PATH", ""))
    if pf is None:
        raise RuntimeError("the pf binary is not found on PATH (pf step hub reaches the hub through it)")
    c = proc.run([pf, "--json", "step", "hub"] + args, env=env, timeout=TIMEOUT)
    if c.exit_code != 0 or c.error:
        stderr = c.stderr.decode("utf-8", "replace").strip()
        try:
            doc = json.loads(stderr.split("\n")[-1])
        except ValueError:
            doc = None
        if isinstance(doc, dict) and isinstance(doc.get("code"), str) and doc["code"]:
            detail = doc.get("detail")
            raise HubError(doc["code"], detail if isinstance(detail, str) else "")
        why = c.error or ("exit status %d" % c.exit_code)
        raise RuntimeError("pf step hub %s: %s: %s" % (" ".join(args), why, stderr))
    return c.stdout


def runner(environ: Mapping[str, str]) -> Runner:
    """``run`` bound to environ."""
    env = dict(environ)
    return lambda args: run(args, env)
