"""One step script's invocation: what it answers and how it ends.

A step script parses its flags (``Parser``: argparse, ``--flag`` forms
only), does its work and answers an exit code, filling the invocation's
heads, summary, details and logs as it goes; ``finish`` turns them into the
script's result file (``result``): written atomically to ``--out`` when the
script set it, printed as one compact JSON line with ``--json``, else its
summary echoed as ``<script>: <summary> (<verdict>)`` (not when ``quiet``:
the run wrote its own stdout). A result that cannot be written turns the
exit code to 2, said on stderr. A usage error is one stderr line and exit
2 — with the result written when ``--out`` was already parsed. Progress
lines go to stderr as ``<script>: <text>`` (stdout is the result's).

The result's ``command`` is the script's name (``sync.py``, ``facts.py``,
``integrate.py``…): the files' readers — the next step's skill, the other
scripts — check it. Its ``version`` is the library's (``pfstep.LIBRARY``):
the scripts ship with the workflow, not with a pf build.

``deadline`` is the run's bound: the ``--deadline`` flag's when given, else
what the step environment allows (``pfstep.deadline``).
"""

import argparse
import os
import sys
from typing import IO, Any, Callable, Dict, List, Mapping, Optional, Union

from . import LIBRARY, deadline, duration, jsonout, result

# The version a script's result file names: the shared library's.
VERSION = LIBRARY


def duration_flag(s: str) -> int:
    """argparse type of a Go duration flag (``--deadline 5m``): nanoseconds."""
    try:
        return duration.parse(s)
    except ValueError as err:
        raise argparse.ArgumentTypeError(str(err)) from None


def go_list(xs: List[str]) -> str:
    """A string list as Go's %v prints it: ``[a b]``."""
    return "[" + " ".join(xs) + "]"


# Details are a dict, or a function answering one (read when the run ends).
Details = Union[Dict[str, Any], Callable[[], Dict[str, Any]]]


class Invocation:
    """One run of the script name with argv under environ (default the
    process's environment)."""

    def __init__(self, name: str, argv: List[str], environ: Optional[Mapping[str, str]] = None,
                 stdout: Optional[IO[str]] = None, stderr: Optional[IO[str]] = None) -> None:
        self.name = name
        self.argv = list(argv)
        self.env: Dict[str, str] = dict(os.environ if environ is None else environ)
        self.stdout = stdout if stdout is not None else sys.stdout
        self.stderr = stderr if stderr is not None else sys.stderr
        self.res = result.begin(name, self.argv, VERSION, self.env)
        # set by the script once its flags are parsed
        self.out = ""
        self.json = False
        # quiet: the run wrote its own stdout; the summary line is not echoed
        self.quiet = False
        # filled by the script
        self.heads: Dict[str, str] = {}
        self.summary = ""
        self.details: Details = {}
        self.logs: List[str] = []

    def getenv(self, name: str) -> str:
        return self.env.get(name, "")

    def deadline(self, flag_ns: int) -> int:
        """The run's deadline duration (nanoseconds)."""
        if flag_ns > 0:
            return flag_ns
        return deadline.default_deadline_for(self.env)

    def usage(self, msg: str) -> int:
        """A usage error: one stderr line, exit 2."""
        self.say(msg)
        return 2

    def say(self, msg: str) -> None:
        """One progress line on stderr."""
        print("%s: %s" % (self.name, msg), file=self.stderr, flush=True)

    def finish(self, exit_code: int) -> int:
        """The result for exit_code written and printed; the exit code (2 when
        the result could not be written)."""
        res = self.res
        res.heads = {k: self.heads[k] for k in sorted(self.heads)}
        res.summary = self.summary
        d = self.details
        res.details = d() if callable(d) else d
        res.logs = list(self.logs)
        result.finish(res, exit_code)
        if self.out:
            try:
                result.write_result(self.out, res)
            except OSError as err:
                self.say("the result could not be written to %s: %s" % (self.out, err))
                res.exit_code = 2
        if self.json:
            print(jsonout.line(res.to_dict()), file=self.stdout, flush=True)
        elif res.summary and not self.quiet:
            print("%s: %s (%s)" % (self.name, res.summary, res.verdict), file=self.stdout, flush=True)
        return res.exit_code


class Parser(argparse.ArgumentParser):
    """argparse for a step script: no abbreviated flags, a usage error is
    exit 2 (argparse's own), the reference text kept as written."""

    def __init__(self, prog: str, description: str = "", **kw: Any) -> None:
        super().__init__(
            prog=prog,
            description=description,
            formatter_class=argparse.RawDescriptionHelpFormatter,
            allow_abbrev=False,
            **kw,
        )
