"""pfstep.gateid — the lane's own gate id.

The identity of a repository's test gate at a commit — the sha256 over
the ``polyforge-gate-id 1`` header, the sha256 of
``.polyforge/Dockerfile.verify`` and the conf's gate lines, both files
read from that commit's tree — as a library, for a repository whose tree
carries no ``tools/gate-id``: the gate falls back to this one
(gate.py, polyforge#29). ``tools/gate-id`` (awk over ``git show``) stays
the release's; the two must stay byte-identical, and the consistency
test (``tests/test_gateid.py``) runs both over the same commits to pin
it. The algorithm is the documented one (``tools/gate-id``'s header,
``docs/reference/release.md`` "The gate id"): a gate line is a conf line
with its leading whitespace removed that is neither blank nor a ``#``
comment and whose key — the text before its first ``=`` — is one of
``stack``, ``fast_gate``, ``full_command``, ``check``,
``check_requires_env``, ``check_requires_cmd`` or ``check_timeout``,
kept byte for byte in file order, each ending in a newline (a last line
without a trailing newline still hashes with one, as awk prints it).

A gate that runs in a prepared image (the step's ``image`` parameter,
polyforge#64) hashes one line more — ``image <the image's local ID>``,
after the dockerfile one — and a tree without a Dockerfile is then
allowed: its line is left out. Without an image the bytes are exactly
what they always were.
"""

import hashlib
from typing import List, Mapping, Optional

from . import git

CONF = ".polyforge/verify-scope.conf"
DOCKERFILE = ".polyforge/Dockerfile.verify"
HEADER = b"polyforge-gate-id 1"
# The conf keys whose lines decide a --full plan on an empty diff: the
# same set as tools/gate-id's (the id is the gate's identity, not the
# engine's).
GATE_KEYS = (
    b"stack",
    b"fast_gate",
    b"full_command",
    b"check",
    b"check_requires_env",
    b"check_requires_cmd",
    b"check_timeout",
)
# awk's [[:space:]] in the C locale, minus the \n that never occurs in a
# \n-split line: the leading bytes the line rule strips.
_LEADING_SPACE = b" \t\x0b\x0c\r"


class GateIdError(Exception):
    """Why the gate id of a commit could not be computed."""


def _commit(directory: str, rev: str, environ: Optional[Mapping[str, str]]) -> str:
    """rev as a full commit id of the clone at directory (GateIdError
    naming why it is not one, as tools/gate-id refuses it)."""
    try:
        out = git.git(directory, "rev-parse", "--verify", rev + "^{commit}", environ=environ)
    except git.GitError as err:
        raise GateIdError("%s is not a commit of %s: %s" % (rev, directory, err)) from None
    return out.strip()


def _blob(directory: str, commit: str, path: str, environ: Optional[Mapping[str, str]]) -> bytes:
    """path's bytes as committed at commit (never the working tree)."""
    c = git.run(directory, "show", commit + ":" + path, environ=environ)
    if c.exit_code != 0 or c.error:
        why = c.error or c.stderr.decode("utf-8", "replace").strip() or "exit %d" % c.exit_code
        raise GateIdError("%s is not in the tree of %s (%s): %s" % (path, commit, directory, why))
    return c.stdout


def _gate_lines(conf: bytes) -> List[bytes]:
    """conf's gate lines, byte for byte (the line rule in the module's
    doc), each ending in a newline, in file order."""
    out: List[bytes] = []
    for line in conf.split(b"\n"):
        i = 0
        while i < len(line) and line[i] in _LEADING_SPACE:
            i += 1
        s = line[i:]
        if s == b"" or s[0:1] == b"#":
            continue
        eq = s.find(b"=")
        if eq < 0:
            continue
        if s[:eq] in GATE_KEYS:
            out.append(s + b"\n")
    return out


def hashed_text(conf: bytes, dockerfile: Optional[bytes], image: str = "") -> bytes:
    """The exact bytes the id hashes (tools/gate-id --show's output).
    dockerfile None is a tree without one, allowed only with a prepared
    image: its line is left out and the ``image <local ID>`` line stands
    in for the image's content half. Without an image the bytes are
    exactly what they always were."""
    out = [HEADER + b"\n"]
    if dockerfile is not None:
        out.append(b"dockerfile " + hashlib.sha256(dockerfile).hexdigest().encode("ascii") + b"\n")
    if image:
        out.append(b"image " + image.encode("ascii") + b"\n")
    return b"".join(out + _gate_lines(conf))


def show(directory: str, commit: str, environ: Optional[Mapping[str, str]] = None, image: str = "") -> bytes:
    """The exact bytes the gate id of commit (a rev, resolved in the clone
    at directory) hashes, both files read from that commit's tree; raises
    GateIdError naming why it could not. With a prepared image a tree
    without a Dockerfile is allowed: its line is left out."""
    sha = _commit(directory, commit, environ)
    conf = _blob(directory, sha, CONF, environ)
    dockerfile: Optional[bytes]
    if image:
        try:
            dockerfile = _blob(directory, sha, DOCKERFILE, environ)
        except GateIdError:
            dockerfile = None  # the prepared image replaces the built one
    else:
        dockerfile = _blob(directory, sha, DOCKERFILE, environ)
    return hashed_text(conf, dockerfile, image)


def gate_id(directory: str, commit: str, environ: Optional[Mapping[str, str]] = None, image: str = "") -> str:
    """The gate id of commit: the sha256 of show's bytes (64 hex)."""
    return hashlib.sha256(show(directory, commit, environ, image)).hexdigest()
