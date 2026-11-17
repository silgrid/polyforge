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

import re
from typing import Dict, List, NamedTuple, Optional

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
