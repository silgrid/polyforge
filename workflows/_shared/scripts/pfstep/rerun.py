"""A failing unit's test file and targeted re-run, derived from the planned
command that ran it.

A re-run is only evidence when it runs the unit the way the plan did, so
it keeps the command's environment assignments and every flag it carried
(a ``-race``, ``-tags``, ``-W error`` dropped would turn a real failure into
a "flaky" pass); a flag whose arity cannot be read makes the unit not
re-runnable instead (fail closed: it stays a failure). A command that
declares a re-run template (the conf's ``rerun=``, ``{unit}`` standing for
the unit) re-runs every located unit by it, except past the race guard: in
a run that reported a data race, a re-run without ``-race`` is none. A
nested engine root's command derives the re-run from its engine's own
command and runs it behind the same ``cd <dir> &&``.

``locate`` is the whole of it for one unit (``UnitState``) of one command
(``CommandSpec``). Ported from the Go step commands' rerun.go and bar.go's
locate/derive.
"""

import os
import re
from dataclasses import dataclass, field
from typing import FrozenSet, List, Tuple

from . import duration, git, gostr
from .shellwords import (
    command_word,
    has_flag,
    has_go_test,
    leading_assignments,
    shell_quote,
    shell_words,
    shell_words_raw,
    token_index,
)
from .units import (
    KIND_COMMAND,
    KIND_E2E_SUITE,
    KIND_GO,
    KIND_GO_PKG,
    KIND_LIBTEST,
    KIND_MJS,
    KIND_PYTEST,
    KIND_UNITTEST,
    Unit,
)

# Bounds one git read of a derivation (seconds).
GIT_TIMEOUT = 600.0

RACE_RERUN_NOTE = "not re-runnable: the run reported a data race and a re-run without -race cannot prove it flaky"

# The go test / go build flags that take a separate value word (-tags x);
# every other flag is read as boolean.
GO_VALUE_FLAGS = frozenset((
    "run", "skip", "count", "timeout", "tags", "p", "parallel", "cpu", "bench", "benchtime", "shuffle",
    "coverprofile", "covermode", "coverpkg", "o", "exec", "ldflags", "gcflags", "asmflags", "mod", "modfile",
    "C", "overlay", "pgo", "vet", "blockprofile", "cpuprofile", "memprofile", "mutexprofile", "trace",
    "outputdir", "fuzz", "fuzztime", "fuzzminimizetime", "list", "toolexec", "compiler", "installsuffix",
    "pkgdir", "buildmode", "blockprofilerate", "memprofilerate", "mutexprofilefraction",
))

# The flags the targeted re-run sets itself (a planned -timeout is kept: a
# re-run without it would pass a hang).
GO_DROP_FLAGS = frozenset(("run", "skip", "count", "v", "json"))

# The pytest / unittest options a re-run can carry: valueless switches and
# `--name=value` forms. Any other option may take a separate value whose
# word cannot be told from a path, so its command is not re-run.
PYTEST_SWITCHES = frozenset((
    "-q", "-qq", "-qqq", "-v", "-vv", "-vvv", "-x", "-s", "-l", "--strict-markers", "--strict-config",
    "--no-header", "--disable-warnings", "--showlocals", "--exitfirst", "--quiet", "--verbose",
))
UNITTEST_SWITCHES = frozenset((
    "-v", "-q", "-f", "-c", "-b", "--locals", "--verbose", "--quiet", "--failfast", "--catch", "--buffer",
))
_ATTACHED_OPT = re.compile(r"(--[A-Za-z][\w-]*=.*|-r\w+|-W[^\t\n\f\r ]+|-p[^\t\n\f\r ]+)\Z", re.ASCII)

# A command that first changes into a repository directory: `cd <dir> && …`.
_LEADING_CD = re.compile(gostr.SPACE + r"*cd" + gostr.SPACE + r"+([A-Za-z0-9._/-]+)" + gostr.SPACE + r"*&&", re.ASCII)


@dataclass
class CommandSpec:
    """What a unit's derivation reads of the command that ran it."""

    cmd: str
    # timeout: the command's timeout (nanoseconds): a derived go re-run
    # without a -timeout of its own carries it
    timeout: int = 0
    # rerun_tmpl: the declared re-run template (the conf's rerun=, behind
    # a nested root's cd; "" = none)
    rerun_tmpl: str = ""
    # dir, inner: a nested engine root's command — its root and the
    # command its engine reported, before `cd <dir> &&`
    dir: str = ""
    inner: str = ""


@dataclass
class UnitState:
    """One parsed unit of a command and its resolution."""

    unit: Unit
    # file: the repo-relative test file, when located
    file: str = ""
    # protected: it pins this change — never pre-existing
    protected: bool = False
    # rerun: the targeted re-run ("" = not re-runnable)
    rerun: str = ""
    # runs at HEAD, the original included
    runs: int = 1
    # flaky: "flaky n/m" (passes/runs) when a re-run passed
    flaky: str = ""
    # not_rerun: its first re-run never started (the budget spent or the
    # deadline passed)
    not_rerun: bool = False
    pass_logs: List[str] = field(default_factory=list)
    # note: why a failure stays a failure
    note: str = ""
    # head_sig: the failure signature of the unit's own targeted re-run at
    # HEAD — the same command form the base runs use ("" = none showed it)
    head_sig: str = ""
    # rerun_dur: the last targeted re-run's wall time (seconds)
    rerun_dur: float = 0.0
    pre_existing: bool = False
    pre_detail: str = ""
    base_sig: str = ""
    # carries: the globs the unit's file declares it covers,
    # repository-relative (a failing unit's; [] = none)
    carries: List[str] = field(default_factory=list)

    @property
    def kind(self) -> str:
        return self.unit.kind

    @property
    def name(self) -> str:
        return self.unit.name

    @property
    def failed(self) -> bool:
        return self.unit.failed

    def failing(self) -> bool:
        """A unit still failing after the flaky policy."""
        return self.unit.failed and self.flaky == ""


def template_rerun(tmpl: str, unit: str) -> str:
    """A declared template's re-run of one unit: every ``{unit}`` replaced by
    the unit's name, shell-quoted."""
    return tmpl.replace("{unit}", shell_quote(unit))


def go_test_flags(cmd: str) -> Tuple[List[str], bool, bool]:
    """The flags of a simple ``go test`` command the re-run must carry
    (package patterns and the re-run's own flags dropped), whether it is a
    go test at all, and ok: False when it cannot be carried (``-args``)."""
    words, raws, simple = shell_words_raw(cmd)
    if not simple or not has_go_test(words):
        return [], False, True
    _, i = command_word(words)
    flags: List[str] = []
    j = i + 2
    while j < len(words):
        w = words[j]
        if w in ("-args", "--args"):
            return [], True, False
        if not w.startswith("-"):
            j += 1
            continue  # a package pattern
        name, eq, _ = w.lstrip("-").partition("=")
        separate = eq == "" and name in GO_VALUE_FLAGS and j + 1 < len(words)
        if name in GO_DROP_FLAGS:
            if separate:
                j += 1
            j += 1
            continue
        flags.append(raws[j])
        if separate:
            j += 1
            flags.append(raws[j])
        j += 1
    return flags, True, True


def flag_names(flags: List[str]) -> List[str]:
    """The flag words of a raw flag list (values dropped)."""
    return [f for f in flags if f.startswith("-")]


def go_rerun(cmd: str, run: str, target: str, timeout: int) -> Tuple[str, str]:
    """The targeted re-run of a Go test (run = "^Name$") or package (run =
    "") at target, carrying the planned command's assignments and flags;
    ("", why) when it cannot be had."""
    flags, _, ok = go_test_flags(cmd)
    if not ok:
        return "", "not re-runnable: the planned `go test` passes arguments through -args"
    parts = leading_assignments(cmd)
    parts += ["go", "test", *flags, "-count=1", "-v"]
    if not has_flag(flag_names(flags), "-timeout"):
        parts += ["-timeout", duration.format(timeout)]
    if run != "":
        parts += ["-run", "'" + run + "'"]
    parts.append(target)
    return " ".join(parts), ""


def py_rerun(cmd: str, runner: str, unit_id: str, switches: FrozenSet[str]) -> Tuple[str, str]:
    """The targeted re-run of a pytest node or a unittest case: the command
    through its runner word, the options it can carry, then the unit's id
    (positional paths dropped)."""
    words, raws, simple = shell_words_raw(cmd)
    i = token_index(words, runner)
    bad = "not re-runnable: the planned command is not a simple " + runner + " invocation"
    if not simple or i < 0:
        return "", bad
    _, ci = command_word(words)
    if ci < 0 or ci > i:
        return "", bad
    parts = leading_assignments(cmd)
    parts += raws[ci:i + 1]
    for j, w in enumerate(words[i + 1:]):
        if w == "discover":
            return "", "not re-runnable: `unittest discover` arguments cannot be carried to one case"
        if not w.startswith("-"):
            continue  # a path or an id: the re-run names its own
        if w in switches or _ATTACHED_OPT.match(w):
            parts.append(raws[i + 1 + j])
            continue
        return "", "not re-runnable: the planned command's option %s may take a value the re-run cannot tell from a path" % w
    parts.append(shell_quote(unit_id))
    return " ".join(parts), ""


def suite_rerun(cmd: str, printed: str, suite: str) -> Tuple[str, str]:
    """The targeted re-run of one suite of an aggregate e2e runner: its
    printed ``re-run:`` command verbatim behind the planned command's
    leading assignments, when the planned command is one simple command
    carrying no flag the printed one could drop; without a printed line,
    only ``<planned npm run <script> [--]> -- <suite>``. Anything else is
    not re-runnable."""
    words, simple = shell_words(cmd)
    w, i = command_word(words)
    if not simple or i < 0:
        return "", "not re-runnable: the planned aggregate e2e command is not one simple command"
    for x in words[i + 1:]:
        if x.startswith("-") and x != "--":
            return "", "not re-runnable: the planned aggregate e2e command carries %s, which a single-suite re-run could drop" % x
    if printed != "":
        return " ".join(leading_assignments(cmd) + [printed]), ""
    rest = words[i + 1:]
    if w != "npm" or len(rest) < 2 or rest[0] != "run" or len(rest) > 3 or (len(rest) == 3 and rest[2] != "--"):
        return "", "not re-runnable: the runner printed no `re-run:` line and the planned command is not a simple `npm run <script>`"
    sep = " " if len(rest) == 3 else " -- "
    return cmd.rstrip(" \t") + sep + shell_quote(suite), ""


def file_exists(repo_dir: str, rel: str) -> bool:
    """Whether the repository-relative file exists (not a directory)."""
    p = os.path.join(repo_dir, rel)
    return os.path.exists(p) and not os.path.isdir(p)


def command_dir(cs: CommandSpec) -> str:
    """The directory a command runs its tests from, when not the repository
    root: a nested root's, or the one a leading ``cd <dir> &&`` changes
    into ("" = the root)."""
    if cs.dir != "":
        return cs.dir
    m = _LEADING_CD.match(cs.cmd)
    if m:
        d = gostr.clean_rel(m.group(1))
        if d not in ("", "."):
            return d
    return ""


def go_test_file(repo_dir: str, name: str, pkg: str) -> str:
    """The _test.go file declaring a top-level test, disambiguated by its
    package when several do ("" = not located)."""
    c = git.run(repo_dir, "grep", "-l", "-E", "^func " + gostr.quote_meta(name) + r"\(", "--", "*_test.go",
                timeout=GIT_TIMEOUT, literal_pathspecs=False)
    if c.exit_code != 0:
        return ""
    files = gostr.fields(c.stdout.decode("utf-8", "replace"))
    if len(files) == 1:
        return files[0]
    found = [f for f in files if pkg != "" and (pkg.endswith("/" + gostr.path_dir(f)) or pkg == gostr.path_dir(f))]
    return found[0] if len(found) == 1 else ""


def derive(repo_dir: str, cs: CommandSpec, u: UnitState, race: bool) -> None:
    """The unit's file and re-run derived from the command its runner was
    given (a nested root's own command, not the cd list)."""
    cmd = cs.inner if cs.dir != "" else cs.cmd
    words, simple = shell_words(cmd)
    why = ""
    k = u.unit
    if k.kind in (KIND_GO, KIND_GO_PKG):
        target, run = "", ""
        if k.kind == KIND_GO:
            u.file = go_test_file(repo_dir, k.name, k.pkg)
            run = "^" + gostr.quote_meta(k.name) + "$"
            if u.file != "" and gostr.path_dir(u.file) == ".":
                target = "."
            elif u.file != "":
                target = "./" + gostr.path_dir(u.file) + "/"
            elif "/" in k.pkg or "." in k.pkg:
                target = k.pkg
        elif not k.build_failed and ("/" in k.name or "." in k.name):
            target = k.name
        if target != "":
            u.rerun, why = go_rerun(cmd, run, target, cs.timeout)
        if u.rerun != "" and race and " -race " not in " " + u.rerun + " ":
            u.rerun, why = "", RACE_RERUN_NOTE
    elif k.kind == KIND_PYTEST:
        u.file = k.name.partition("::")[0]
        u.rerun, why = py_rerun(cmd, "pytest", k.name, PYTEST_SWITCHES)
    elif k.kind == KIND_UNITTEST:
        parts = k.name.split(".")
        if len(parts) >= 3:
            u.file = "/".join(parts[:-2]) + ".py"
        u.rerun, why = py_rerun(cmd, "unittest", k.name, UNITTEST_SWITCHES)
    elif k.kind == KIND_LIBTEST:
        w, i = command_word(words)
        if simple and w == "cargo" and i + 1 < len(words) and words[i + 1] == "test":
            sep = " " if token_index(words, "--") >= 0 else " -- "
            u.rerun = cmd + sep + "--exact " + shell_quote(k.name)
    elif k.kind == KIND_E2E_SUITE:
        u.file = k.name
        u.rerun, why = suite_rerun(cmd, k.printed_rerun, k.name)
    elif k.kind == KIND_MJS:
        w, _ = command_word(words)
        if simple and w == "node":
            files = [x for x in words if x.endswith(".mjs")]
            if len(files) == 1:
                u.file = gostr.clean_rel(files[0])
                u.rerun = cmd
    u.file = gostr.clean_rel(u.file)
    if u.rerun == "" and why != "":
        u.note = why


def locate(repo_dir: str, cs: CommandSpec, u: UnitState, race: bool) -> None:
    """The unit's test file and targeted re-run (a re-run that would not run
    the unit the way the plan did is none: the unit's note says why). race:
    the command's output reported a data race. A file the command names
    relative to the directory it changes into is made repository-relative
    when it exists there."""
    derive(repo_dir, cs, u, race)
    d = command_dir(cs)
    if d != "" and u.file != "" and not file_exists(repo_dir, u.file) and file_exists(repo_dir, gostr.path_join(d, u.file)):
        u.file = gostr.path_join(d, u.file)
    if cs.rerun_tmpl != "" and u.kind != KIND_COMMAND and not u.unit.build_failed:
        # the race guard holds over the template too
        tr = template_rerun(cs.rerun_tmpl, u.name)
        if not race or " -race " in " " + tr + " ":
            u.rerun, u.note = tr, ""
        else:
            u.rerun, u.note = "", RACE_RERUN_NOTE
    elif cs.dir != "" and u.rerun != "":
        u.rerun = "cd " + shell_quote(cs.dir) + " && " + u.rerun


def locate_unit(repo_dir: str, cs: CommandSpec, unit: Unit, race: bool) -> UnitState:
    """A parsed unit located (``locate``) as a fresh UnitState."""
    u = UnitState(unit=unit)
    locate(repo_dir, cs, u, race)
    return u


def whole_command(check: str, exit_code: int, rerun: str = "") -> UnitState:
    """The one unit of a failed command whose output locates none: the
    whole command."""
    return UnitState(unit=Unit(kind=KIND_COMMAND, name=check, failed=True, marker="exit %d" % exit_code), rerun=rerun)
