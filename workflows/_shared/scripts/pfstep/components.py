"""pfstep.components (polyforge#89): the epic-upgrade rule's
configuration and a repository's component division — the shared core of
the entry script ``workflows/_shared/scripts/components.py``.

The conf keys (parsed here, read by no other tool; the scope engine
accepts their lines without the unknown-key diagnostic — the
not_runnable_rule precedent — and the gate id hashes none of them):

``component=<name>|<path>[,<path>]``
    Repeatable: one named component of this repository, the paths its
    prefix range (a declared path and everything under it; overlapping
    declarations take the longest prefix). ``<name>`` has the ``check``
    name shape; a path is repository-relative, never absolute or
    ``..``-escaping. A malformed line, or a second line naming an
    already-declared component, is diagnosed and ignored.
``epic_components`` / ``epic_units`` / ``epic_files``
    The epic-upgrade thresholds: the minimum component count, the unit
    fallback and the single-repository file count. Without a declaration
    the defaults are 3 / 8 / 30; a value that is not a positive integer
    is diagnosed and ignored (that key keeps its default), and a second
    declaration of a key keeps the first.

The component division itself (the entry script's): a declared range
first, then a nested engine root (a subdirectory with its own
``.polyforge/verify-scope.conf`` — only the root conf's declarations are
read; a nested conf only marks its subdirectory), then a manifest
subdirectory (go.mod, package.json, pyproject.toml, requirements.txt,
Cargo.toml), each nest taken at its outermost, then the first-level
directory, the repository's root files forming the ``.`` component. A
path with a ``testdata``, ``node_modules`` or ``vendor`` segment is
excluded from the division entirely."""

import os
import re
from typing import Dict, List, NamedTuple, Optional, Tuple

from pfstep import git as gitlib

CONF = ".polyforge/verify-scope.conf"

# The ticket's defaults (polyforge#1 ruling 18, #89 AC1): the component
# count is "at least", the unit and file counts "more than".
DEFAULT_EPIC_COMPONENTS = 3
DEFAULT_EPIC_UNITS = 8
DEFAULT_EPIC_FILES = 30

# The ``check`` name shape (verify-scope's conf rule).
NAME_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*\Z", re.ASCII)

# The engine's manifest list, the one every conf reader agrees on.
MANIFESTS = ("go.mod", "package.json", "pyproject.toml", "requirements.txt", "Cargo.toml")

# The path segments a generic component never has.
EXCLUDED_SEGMENTS = ("testdata", "node_modules", "vendor")

THRESHOLD_KEYS = ("epic_components", "epic_units", "epic_files")

CONF_SUFFIX = "/" + CONF


class Component(NamedTuple):
    """One component of a repository: its name, the path prefixes that
    define it, and where the division found it."""

    name: str
    paths: List[str]
    source: str


class Thresholds(NamedTuple):
    """The effective epic-upgrade thresholds and, per key, their source
    (``default`` or ``declared``)."""

    epic_components: int
    epic_units: int
    epic_files: int
    source: Dict[str, str]


class ConfReading(NamedTuple):
    """What the root conf declares: the declared components, the
    effective thresholds, and the diagnostics of the lines ignored."""

    components: List[Component]
    thresholds: Thresholds
    diagnostics: List[str]


class Division(NamedTuple):
    """One component of a repository as the division found it: its name,
    the path range that defines it, its source, and whether every path
    in it is documentation (never counted by the epic rules)."""

    name: str
    paths: List[str]
    source: str
    documentation_only: bool


class RepoReport(NamedTuple):
    """One repository's answer: its name, the commit (and which revision
    it came from), the division, the effective thresholds, and — on a
    read failure — why nothing else is there."""

    name: str
    commit: str
    rev: str
    division: List[Division]
    thresholds: Thresholds
    error: str
    diagnostics: List[str]


def _threshold_defaults() -> Thresholds:
    return Thresholds(DEFAULT_EPIC_COMPONENTS, DEFAULT_EPIC_UNITS, DEFAULT_EPIC_FILES,
                      {k: "default" for k in THRESHOLD_KEYS})


def _component_paths_ok(name: str, val: str, line: str, diag: List[str]) -> Optional[List[str]]:
    """The declared paths of one component= line, or None (diagnosed)."""
    paths = [p.strip() for p in val.split(",")]
    for p in paths:
        if p == "" or p.startswith("/") or p == ".." or p.startswith("../") or "/../" in p:
            diag.append("component '%s': rejected path '%s' (expected one or more"
                        " repository-relative paths, never absolute or ..-escaping): %s"
                        % (name, p, line))
            return None
    return paths


def _read_component(val: str, line: str, out: List[Component], seen: List[str],
                    diag: List[str]) -> None:
    """One component= line: accepted, or diagnosed and ignored."""
    if "|" not in val:
        diag.append("component without the <name>|<path>[,<path>…] shape: %s" % line)
        return
    name, rest = val.split("|", 1)
    name = name.strip()
    if not NAME_RE.match(name):
        diag.append("component name '%s' rejected (letters, digits, '.', '_' and '-',"
                    " starting with a letter or digit): %s" % (name, line))
        return
    if name in seen:
        diag.append("a second component named '%s' is ignored, the first one wins: %s"
                    % (name, line))
        return
    paths = _component_paths_ok(name, rest, line, diag)
    if paths is None:
        return
    seen.append(name)
    out.append(Component(name, paths, "declared"))


def read_conf(text: str) -> ConfReading:
    """The root conf's component= and epic_* keys (polyforge#89, AC1):
    the declared components, the effective thresholds (the defaults
    3 / 8 / 30 without a declaration) and the diagnostics of every
    line ignored. Other keys are none of this reader's business: it
    reads past them silently, exactly as every other conf reader reads
    past these."""
    components: List[Component] = []
    seen: List[str] = []
    defaults = _threshold_defaults()
    values: Dict[str, int] = {"epic_components": defaults.epic_components,
                             "epic_units": defaults.epic_units,
                             "epic_files": defaults.epic_files}
    source: Dict[str, str] = dict(defaults.source)
    diag: List[str] = []
    for raw in text.split("\n"):
        line = raw.strip()
        if line == "" or line.startswith("#") or "=" not in line:
            continue
        key, val = line.split("=", 1)
        key = key.strip()
        if key == "component":
            _read_component(val.strip(), line, components, seen, diag)
        elif key in THRESHOLD_KEYS:
            value = val.strip()
            if not re.fullmatch(r"[1-9][0-9]*", value, re.ASCII):
                diag.append("%s '%s' rejected (expected a positive integer): %s"
                            % (key, value, line))
            elif source[key] == "declared":
                diag.append("a second %s is ignored, the first one wins: %s" % (key, line))
            else:
                values[key] = int(value)
                source[key] = "declared"
    return ConfReading(components, Thresholds(values["epic_components"], values["epic_units"],
                                              values["epic_files"], source), diag)


# ---------------------------------------------------------------------------
# The component division (polyforge#89, AC2): a repository's components
# from its commit tree — a declared range first, then a nested engine
# root, then a manifest subdirectory (each nest outermost), then the
# first-level directory, the root files forming the "." component.
# ---------------------------------------------------------------------------

def is_doc_path(p: str) -> bool:
    """verify-scope's documentation rule, mirrored: the paths the
    spanning rule never counts."""
    if p.endswith((".md", ".markdown", ".rst", ".txt")):
        return True
    return p.startswith("docs/") or p.startswith("doc/")


def _excluded(p: str) -> bool:
    """Whether the generic division skips the path entirely."""
    return any(seg in EXCLUDED_SEGMENTS for seg in p.split("/"))


def _under(p: str, root: str) -> bool:
    """Whether path p sits in root's prefix range (the module_root
    rule's match)."""
    return p == root or p.startswith(root + "/")


def _outermost(roots: List[str]) -> List[str]:
    """The roots no other root contains (a nest answers at its
    outermost directory), in stable order."""
    unique = sorted(set(roots))
    return [r for r in unique if not any(r != o and _under(r, o) for o in unique)]


def _assign(p: str, declared: List[Component], nested: List[str],
            manifest: List[str]) -> Tuple[str, str]:
    """One path's component: (source, name). A declared range wins
    (the longest prefix of overlapping declarations), then a nested
    conf root, then a manifest subdirectory, then the first-level
    directory, the root files answering as \".\""""
    best: Optional[Tuple[str, int, str]] = None
    for comp in declared:
        for r in comp.paths:
            if _under(p, r) and (best is None or len(r) > best[1]):
                best = ("declared", len(r), comp.name)
    if best is not None:
        return ("declared", best[2])
    for n in nested:
        if _under(p, n):
            return ("nested conf", n)
    for m in manifest:
        if _under(p, m):
            return ("manifest", m)
    if "/" not in p:
        return ("root files", ".")
    return ("top-level", p.split("/", 1)[0])


def divide(paths: List[str], reading: ConfReading) -> List[Division]:
    """The division of a repository's tracked paths: the declared
    components in conf order (a declaration answers even where no path
    falls in it — the report shows it), the generic components only
    where paths fell in them, the root files last."""
    kept = [p for p in paths if not _excluded(p)]
    nested = _outermost([f[:-len(CONF_SUFFIX)] for f in kept
                        if f.endswith(CONF_SUFFIX) and len(f) > len(CONF)])
    manifest = [m for m in _outermost([os.path.dirname(f) for f in kept
                                       if os.path.basename(f) in MANIFESTS
                                       and os.path.dirname(f) != ""])
                if not any(_under(m, n) for n in nested)]
    groups: Dict[Tuple[str, str], List[str]] = {}
    for p in kept:
        groups.setdefault(_assign(p, reading.components, nested, manifest), []).append(p)

    out: List[Division] = []
    for comp in reading.components:
        assigned = groups.get(("declared", comp.name), [])
        out.append(Division(comp.name, list(comp.paths), "declared",
                            bool(assigned) and all(is_doc_path(p) for p in assigned)))
    for source, roots in (("nested conf", nested), ("manifest", manifest)):
        for r in roots:
            assigned = groups.get((source, r), [])
            if assigned:
                out.append(Division(r, [r], source, all(is_doc_path(p) for p in assigned)))
    for t in sorted(name for src, name in groups if src == "top-level"):
        assigned = groups[("top-level", t)]
        out.append(Division(t, [t], "top-level", all(is_doc_path(p) for p in assigned)))
    root_files = groups.get(("root files", "."), [])
    if root_files:
        out.append(Division(".", sorted(root_files), "root files",
                            all(is_doc_path(p) for p in root_files)))
    return out


# ---------------------------------------------------------------------------
# Reading one clone (polyforge#89, AC2): the origin/HEAD (else HEAD)
# commit tree — never the work tree, never a fetch.
# ---------------------------------------------------------------------------

def resolve_rev(repo_dir: str, rev: str) -> Tuple[str, str]:
    """The commit the tool reads and which revision named it: --rev's,
    else origin/HEAD's, else HEAD's."""
    if rev != "":
        return gitlib.git(repo_dir, "rev-parse", "--verify", rev + "^{commit}").strip(), rev
    try:
        return (gitlib.git(repo_dir, "rev-parse", "--verify",
                           "origin/HEAD^{commit}").strip(), "origin/HEAD")
    except gitlib.GitError:
        return gitlib.git(repo_dir, "rev-parse", "--verify",
                          "HEAD^{commit}").strip(), "HEAD"


def tree_paths(repo_dir: str, commit: str) -> List[str]:
    """The tracked paths of commit's tree, in git's order."""
    return [p for p in gitlib.git(repo_dir, "ls-tree", "-r", "--name-only",
                                  "-z", commit).split("\x00") if p != ""]


def tree_file(repo_dir: str, commit: str, path: str) -> str:
    """One file of commit's tree, "" when the tree holds no such path."""
    try:
        return gitlib.git(repo_dir, "show", commit + ":" + path)
    except gitlib.GitError:
        return ""


def read_repo(repo_dir: str, rev: str = "") -> RepoReport:
    """One clone's whole answer. A repository that cannot be read is an
    error entry, never a wrong division: no components, the default
    thresholds, and the reason."""
    name = os.path.basename(os.path.abspath(repo_dir))
    try:
        commit, rev_name = resolve_rev(repo_dir, rev)
        reading = read_conf(tree_file(repo_dir, commit, CONF))
        return RepoReport(name, commit, rev_name, divide(tree_paths(repo_dir, commit), reading),
                          reading.thresholds, "", reading.diagnostics)
    except gitlib.GitError as err:
        return RepoReport(name, "", rev if rev != "" else "origin/HEAD", [],
                         _threshold_defaults(), str(err), [])
