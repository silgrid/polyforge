"""pfstep: the shared library of a workflow's step scripts.

A lane's scripts (``workflows/_shared/scripts/<name>.py`` and
``workflows/<lane>/scripts/<name>.py``) are run by a step's skill as
``"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" ...`` (the workflow
contract's "Scripts" section, docs/reference/workflow-contract.md). They
import this package by path, never by installation:

    import os, sys
    sys.dont_write_bytecode = True
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    from pfstep import result

The package is the standard library only (Python >= 3.9) and holds what
every script needs the same way:

- ``result``: the result file (schema pf-step-result/1), written atomically;
- ``atomic``: temp file + fsync + rename + directory fsync;
- ``jsonout``: JSON in the order the value was built, never sorted;
- ``stepenv``: the step environment the daemon starts every step with;
- ``deadline``: a script's deadline from the step's tool window;
- ``duration``: Go duration strings, parsed and formatted as Go does;
- ``proc``: a command in its own process group, killed whole;
- ``git``: one hardened git call;
- ``tmpdir``: a private TMPDIR and the sweep of a killed run's leftovers;
- ``secretpat``: the secret shapes a text is scanned for and masked by;
- ``runner``: one command in a task repo — on the host or in the repo's
  verification container — with its log, deadline and record;
- ``container``: the verification container — its image, labels, limits,
  bounded keep-alive and the sweep of expired ones (the docker CLI);
- ``command``: one script invocation — its usage errors, its result file
  written to ``--out`` and printed with ``--json``;
- ``gitmerge``: the merge mechanics of sync.py, merge.py and revert.py —
  git and gh as the lane's own identity, a repository's integrate.conf,
  merging the default branch in place with its mechanical conflicts
  resolved, the landing rules, reverts that keep the log and its pins, and
  the projection a projected pin is computed under (with ``posixre``, POSIX
  leftmost-longest regexes).
- ``invoke``: one run of a script that writes a result file — its parse,
  the result written and printed;
- ``gotext``, ``goglob``, ``godecode``: what a script that took over a Go
  step command keeps of Go's behaviour — whitespace, ``%q``, clipping and
  ``path``; ``path.Match`` and the scope engine's shell-case glob (never
  ``fnmatch``); typed JSON reads as ``encoding/json`` reads them.
"""

LIBRARY = "pfstep/1"
