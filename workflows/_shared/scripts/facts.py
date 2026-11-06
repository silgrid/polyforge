"""facts.py — the facts a review reads about a build's delivered tree,
written to a result file (schema pf-step-result/1, command ``facts.py``).

Usage (from the capsule root, or with --capsule; a default input is the
capsule's file, a given path is taken as given):

  facts.py plan-conformance [--capsule DIR] [--plan artifacts/plan.md]
      [--units artifacts/build/units.json] [--json] [--out PATH]
  facts.py build [--capsule DIR] [--units artifacts/build/units.json]
      [--replay [--isolation container|host]] [--unit-timeout D] [--deadline D]
      [--log-dir DIR] [--json] [--out PATH]
  facts.py carries [--capsule DIR] [--units artifacts/build/units.json] [--json] [--out PATH]
  facts.py micro-bounds [--capsule DIR] --plan artifacts/fix/evidence.json [--json] [--out PATH]

The task repos are capsule.json's (the claim's) present under repos/;
units.json is the build's `[{id, test_commit, impl_commit, run{repo, cwd,
argv}, marker}]` (absent for plan-conformance and carries = no unit;
required for build). D is a duration (10m, 90s, 1h30m).

plan-conformance — the plan's `## Units` declarations (`Touched files:`
writes and their `Coupled obligations:`) against the delivered diff (each
task repo's commits since its merge-base with the fetched origin default
branch), both ways; the units' test files count as declared. Details:
  {plan, units_file, unavailable, declared_writes: [{path, obligations:
   [{path, note}], none_reason}], undeclared, unchanged_obligations,
   unchanged_writes, released}
Exit 0: nothing changed undeclared and no declared coupled obligation left
unchanged (an unchanged plain write and a lapse are listed, not judged).
Exit 1: a difference to class. Exit 2: the plan is unreadable or the diff
could not be read (`unavailable` says why).

build — the units resolved against the task repos (the one repo holding
both commits; the test commit's files), each commit pair's test-first
shape (test_commit one commit of test paths alone, a proper ancestor of
impl_commit, which HEAD holds), the `test_weakened` hunks (a unit's test
files changed after its test commit) and, with --replay, each unit's run
replayed on the tree of its test commit — a detached worktree under the
clone's .git, removed afterwards — each bounded by --unit-timeout (10m)
under the whole deadline (--deadline, else the step's tool window less two
minutes). Where it replays: --isolation container (the repo's verification
container: its HEAD must carry .polyforge/Dockerfile.verify, else exit 2),
--isolation host (the host, under the step's PATH), or, without the flag,
the container when the repo's HEAD carries the Dockerfile, else the host.
Logs go to --log-dir, else logs/ beside --out, else the capsule's
runtime-state/facts/<n>. Details:
  {units_file, replay: {unit_timeout, deadline}, units: [{id, repo,
   test_commit, impl_commit, test_files, unresolved, commits, run: {repo,
   cwd, argv}, replay: {exit_code, red, not_runnable: {toolchain, rule,
   line}, setup: {because, first_log, commands: [{command, exit_code,
   timed_out, log}]}, timed_out, not_started, duration_ms, log, image,
   container, host, worktree, cwd, line, output_tail}, replay_error}],
   test_weakened: [{unit, repo, path, reason, hunk}]}
A replay is red only when its run failed on its assertions: a non-zero
exit whose output has a line of NOT_RUNNABLE_RULES (a Go package's
[setup failed] or [build failed], an embed pattern without files, a
compile error, a missing package, a Node module not found, a Python import
error) is `not_runnable` — the rule and the first such line — and carries
no red evidence. Such a run is prepared once and replayed again when the
test commit's .polyforge/verify-scope.conf declares `base_setup=` lines:
each runs at the worktree's root, in order, the way the run does (same
isolation, environment and time limit), the first run's logs kept under
first/, each line's under setup/<k>/; the run replayed again decides. A
line that fails makes the unit `not_runnable` by rule `base-setup`.
Exit 0: every unit resolves, its commits are test-first and, when
replayed, its run is red at the test commit. Exit 1: no unit is reported,
a unit does not resolve, its commits are not test-first, or its replay
exited 0 or was not runnable. Exit 2: units.json is unreadable, or a
replay could not run (a container not had, a worktree not added, the
deadline, an exit 126/127) — `replay_error` names it. A `test_weakened`
hunk moves no exit code.

carries — every e2e suite the build added or changed in a task repo
declares `carries:` in its opening comment block, and one it added or a
unit's run names matches a non-test path the build touched under the
suite's project root. Details:
  {units_file, repos: [{name, unit_repo, problems}], problems}
Exit 0: no problem. Exit 1: a suite without, or with a malformed or
non-matching, declaration — or a touched repo whose diff cannot be judged.

micro-bounds — every changed path (both sides of a rename) lies inside the
`micro_path=` lines of the repo's .polyforge/verify-scope.conf at the
change's base (`*` within one segment, `**` across `/`; AGENTS.md and
CLAUDE.md never), and the credential plan — the evidence lines of --plan
(a JSON array [{repo, line}]) for the repo, then each declared
`micro_check=` it omitted — is not empty. Details:
  {plan, in_bounds, reason, repos: [{name, base, changed_paths,
   declared_paths, commands: [{repo, command}]}]}
Exit 0: in bounds. Exit 1: out of bounds (`reason` lists every refusal).
Exit 2: the plan is unreadable.

Every kind's result carries `heads` (each task repo's HEAD) and, for build
--replay, the replays' logs. No kind writes a task repo; git runs hardened
(pfstep.git). Exit 2 also for an unusable invocation.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import re  # noqa: E402
import secrets  # noqa: E402
import shutil  # noqa: E402
import time  # noqa: E402
from typing import Any, Callable, Dict, List, Mapping, Optional, Sequence, Tuple  # noqa: E402

from pfstep import container, deadline, duration, git, goglob, gotext, runner, secretpat  # noqa: E402
from pfstep import godecode as gd  # noqa: E402
from pfstep.gotext import clip_list, clip_runes, clip_text, fields, quote, short_sha, trim_space  # noqa: E402
from pfstep.invoke import Invocation, Parser  # noqa: E402

COMMAND = "facts.py"

PLAN_CONFORMANCE = "plan-conformance"
BUILD = "build"
CARRIES = "carries"
MICRO_BOUNDS = "micro-bounds"
KINDS = (PLAN_CONFORMANCE, BUILD, CARRIES, MICRO_BOUNDS)

DEFAULT_PLAN = "artifacts/plan.md"
DEFAULT_UNITS = "artifacts/build/units.json"
DEFAULT_UNIT_TIMEOUT = 10 * duration.MINUTE

VERIFY_SCOPE_CONF = ".polyforge/verify-scope.conf"
CAPSULE_JSON = "capsule.json"

# GIT_READ_TIMEOUT bounds one git read; WORKTREE_TIMEOUT a worktree's add or
# remove (seconds).
GIT_READ_TIMEOUT = 10.0
WORKTREE_TIMEOUT = 120.0

OUTPUT_TAIL_LINES = 40

LOGS_GITIGNORE = "# pf step command logs are local, never capsule chain content\n*\n"

# Go RE2's \s (no vertical tab) and its POSIX [[:space:]] (with it).
_S = r"[\t\n\f\r ]"
_POSIX_SPACE = r"[\t\n\v\f\r ]"


def _ci(word: str) -> str:
    """A word matched as RE2's (?i) matches it: ASCII case, plus the two
    non-ASCII simple folds of ASCII letters (U+017F for s, U+212A for k)."""
    out = []
    for c in word:
        if c.isalpha():
            extra = {"s": "ſ", "k": "K"}.get(c.lower(), "")
            out.append("[" + c.upper() + c.lower() + extra + "]")
        else:
            out.append(re.escape(c))
    return "".join(out)


# ---------------------------------------------------------------------------
# git and file reads


def run_git(directory: str, *args: str, timeout: float = GIT_READ_TIMEOUT) -> Tuple[str, Optional[str]]:
    """One hardened git read: (stdout, None), or (stdout, why) when it failed."""
    c = git.run(directory, *args, timeout=timeout)
    out = c.stdout.decode("utf-8", "replace")
    if c.exit_code == 0:
        return out, None
    if c.timed_out:
        msg = "timed out after %gs" % timeout
    elif c.error:
        msg = c.error
    else:
        msg = gotext.trim_space(c.stderr.decode("utf-8", "replace")) or "exit status %d" % c.exit_code
    return out, "git %s: %s" % (" ".join(args), msg)


def read_text(path: str) -> str:
    with open(path, "rb") as f:
        return f.read().decode("utf-8", "replace")


def loads(text: str) -> Any:
    """JSON as Go reads it: no NaN or Infinity."""

    def refuse(name: str) -> Any:
        raise ValueError("invalid character in literal %s" % name)

    return json.loads(text, parse_constant=refuse)


def task_repo_names(repos_dir: str) -> List[str]:
    """The task repos under repos_dir (sorted): every directory not starting
    with a dot."""
    try:
        ents = os.listdir(repos_dir)
    except OSError:
        return []
    return sorted(e for e in ents if not e.startswith(".") and os.path.isdir(os.path.join(repos_dir, e)) and not os.path.islink(os.path.join(repos_dir, e)))


IDENTITY = gd.Struct(
    {"work_item_id": gd.STR, "capsule_id": gd.STR, "project": gd.STR, "bundle_name": gd.STR, "repos": gd.Map(gd.STR), "created_at": gd.STR}
)


def claim_repos(capsule_dir: str) -> List[str]:
    """capsule.json's repos (the claim's), sorted; [] when unreadable."""
    try:
        doc = loads(read_text(os.path.join(capsule_dir, CAPSULE_JSON)))
    except (OSError, ValueError):
        return []
    idn, err = gd.decode(doc, IDENTITY)
    if err is not None:
        return []
    return sorted((idn["repos"] or {}).keys())


def origin_default_ref(directory: str) -> str:
    """The fetched origin default branch (refs/remotes/origin/HEAD, else
    origin's main or master); "" when none is known."""
    out, err = run_git(directory, "symbolic-ref", "-q", "refs/remotes/origin/HEAD")
    if err is None:
        return trim_space(out)
    for b in ("main", "master"):
        _, err = run_git(directory, "rev-parse", "--verify", "-q", "refs/remotes/origin/" + b)
        if err is None:
            return "refs/remotes/origin/" + b
    return ""


def repo_head(directory: str) -> str:
    out, err = run_git(directory, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
    return "" if err is not None else trim_space(out)


def list_or_none(xs: Sequence[str]) -> str:
    return ", ".join(clip_list(xs, 8)) if xs else "none"


def dedupe(xs: Sequence[str]) -> List[str]:
    seen = set()
    out = []
    for x in xs:
        if x not in seen:
            seen.add(x)
            out.append(x)
    return out


def rel_to(root: str, p: str) -> str:
    """p relative to root (slash form) when it lies inside, else absolute."""
    a = os.path.abspath(p)
    rel = os.path.relpath(a, root)
    if rel == ".." or rel.startswith(".." + os.sep):
        return a
    return rel.replace(os.sep, "/")


def ensure_log_dir(d: str) -> None:
    """d, with the .gitignore that keeps it out of the capsule chain."""
    os.makedirs(d, exist_ok=True)
    gi = os.path.join(d, ".gitignore")
    try:
        with open(gi, encoding="utf-8") as f:
            if f.read() == LOGS_GITIGNORE:
                return
    except (OSError, ValueError):
        pass
    with open(gi, "w", encoding="utf-8") as f:
        f.write(LOGS_GITIGNORE)


_ATOI = re.compile(r"[+-]?[0-9]+", re.ASCII)


def next_run_dir(root: str) -> str:
    """root/<n>, n one past the highest existing run."""
    ensure_log_dir(root)
    n = 0
    for e in os.listdir(root):
        if _ATOI.fullmatch(e):
            n = max(n, int(e))
    while True:
        n += 1
        d = os.path.join(root, str(n))
        try:
            os.mkdir(d)
            return d
        except FileExistsError:
            continue


def tail_lines(path: str, n: int) -> str:
    try:
        text = read_text(path)
    except OSError:
        return ""
    lines = text.rstrip("\n").split("\n")
    return "\n".join(lines[-n:]) if len(lines) > n else "\n".join(lines)


# ---------------------------------------------------------------------------
# units


UNIT_RUN = gd.Struct({"repo": gd.STR, "cwd": gd.STR, "argv": gd.List(gd.STR)})
UNIT_DECL = gd.Struct({"id": gd.STR, "test_commit": gd.STR, "impl_commit": gd.STR, "run": gd.Ptr(UNIT_RUN), "marker": gd.STR})


class UnitsError(Exception):
    pass


def read_units(path: str) -> List[Dict[str, Any]]:
    """A units.json file (UnitsError names why it is unusable)."""
    try:
        text = read_text(path)
    except OSError as err:
        raise UnitsError(gotext.os_error("open", err)) from None
    try:
        doc = loads(text)
    except ValueError as err:
        raise UnitsError("%s is not a JSON array of {id, test_commit, impl_commit, run{repo, cwd, argv}}: %s" % (path, err)) from None
    units, derr = gd.decode(doc, gd.List(UNIT_DECL))
    if derr is not None:
        raise UnitsError("%s is not a JSON array of {id, test_commit, impl_commit, run{repo, cwd, argv}}: %s" % (path, derr))
    return list(units or [])


def run_json(run: Optional[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    """A unit's run as the details carry it (cwd omitted when empty)."""
    if run is None:
        return None
    out: Dict[str, Any] = {"repo": run["repo"]}
    if run["cwd"]:
        out["cwd"] = run["cwd"]
    out["argv"] = run["argv"]
    return out


_FULL_SHA = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?", re.ASCII)
_HEX_REV = re.compile(r"[0-9a-f]{7,64}", re.ASCII)


class Unit:
    """One reported unit resolved against the task repos."""

    def __init__(self, uid: str) -> None:
        self.id = uid
        self.repo = ""
        self.test_commit = ""
        self.impl_commit = ""
        self.test_files: List[str] = []
        self.why = ""

    def as_dict(self) -> Dict[str, Any]:
        out: Dict[str, Any] = {"id": self.id}
        for k, v in (("repo", self.repo), ("test_commit", self.test_commit), ("impl_commit", self.impl_commit)):
            if v:
                out[k] = v
        if self.test_files:
            out["test_files"] = list(self.test_files)
        if self.why:
            out["unresolved"] = self.why
        return out


def units_of(repos_dir: str, repos: Sequence[str], decls: Sequence[Dict[str, Any]]) -> List[Unit]:
    """Each declared unit resolved: the repo of the claim holding both
    commits and the files test_commit changed."""
    opened: Dict[str, Repo] = {}

    def repo_of(name: str) -> "Repo":
        if name not in opened:
            opened[name] = Repo(repos_dir, name)
        return opened[name]

    out = []
    for d in decls:
        u = Unit(d["id"])
        u.repo, u.test_commit, u.impl_commit, u.why = resolve_unit_commits(
            repos, trim_space(d["test_commit"]), trim_space(d["impl_commit"]), repo_of
        )
        if not u.why:
            files, err = run_git(os.path.join(repos_dir, u.repo), "diff-tree", "-r", "--no-commit-id", "--name-only", "--no-renames", u.test_commit)
            if err is not None:
                u.why = "its test commit's files are unreadable: " + err
            for p in files.split("\n"):
                p = trim_space(p)
                if p:
                    u.test_files.append(p)
        out.append(u)
    return out


def unit_test_files(units: Sequence[Unit]) -> List[str]:
    seen = set()
    out = []
    for u in units:
        for f in u.test_files:
            if f not in seen:
                seen.add(f)
                out.append(f)
    return out


def resolve_unit_commits(
    repos: Sequence[str], tc: str, ic: str, repo_of: Callable[[str], "Repo"]
) -> Tuple[str, str, str, str]:
    if not tc or not ic:
        return "", "", "", "test_commit and impl_commit are both required"
    hits = []
    repo = t_sha = i_sha = ""
    for name in repos:
        r = repo_of(name)
        t, i = r.commit(tc), r.commit(ic)
        if t and i:
            hits.append(name)
            repo, t_sha, i_sha = name, t, i
    if not hits:
        return "", "", "", "test_commit %s and impl_commit %s are not both commits of one task repo of the claim (%s)" % (
            clip_text(tc), clip_text(ic), ", ".join(repos))
    if len(hits) == 1:
        return repo, t_sha, i_sha, ""
    return "", "", "", "test_commit %s and impl_commit %s name commits in several task repos (%s) — report full shas" % (
        clip_text(tc), clip_text(ic), ", ".join(hits))


class TestPathRules:
    """Which repo-relative paths are test paths: the scope engine's test
    files (goglob.is_test_file), a testdata/, __tests__/ or e2e/ directory
    and the declared go_harness's directory, the conf read at the change's
    base (a change cannot widen its own test paths)."""

    TEST_DIRS = ("testdata", "__tests__", "e2e")

    def __init__(self, harness_dir: str = "", globs: Optional[List[str]] = None) -> None:
        self.harness_dir = harness_dir
        self.globs = list(globs or [])

    @classmethod
    def at(cls, directory: str) -> "TestPathRules":
        rules = cls()
        conf = ""
        ref = origin_default_ref(directory)
        if ref:
            base, err = run_git(directory, "merge-base", "HEAD", ref)
            if err is None:
                conf, _ = run_git(directory, "show", trim_space(base) + ":" + VERIFY_SCOPE_CONF)
        for line in conf.split("\n"):
            line = line.lstrip(" \t")
            if not line or line.startswith("#"):
                continue
            key, sep, val = line.partition("=")
            if not sep:
                continue
            val = trim_space(val)
            if key == "test_glob" and val:
                rules.globs.append(val)
            elif key == "go_harness" and val and not rules.harness_dir:
                d = gotext.path_dir(gotext.path_clean(val))
                if d not in (".", "/"):
                    rules.harness_dir = d
        return rules

    def is_test(self, p: str) -> bool:
        p = gotext.path_clean(p)
        if goglob.is_test_file(p, self.globs):
            return True
        if self.harness_dir and p.startswith(self.harness_dir + "/"):
            return True
        return any(d in self.TEST_DIRS for d in p.split("/")[:-1])

    def describe(self) -> str:
        parts = ["*_test.go, *.test.*, *.spec.*, test_*.py, *_test.py", "test/ tests/ testdata/ __tests__/ e2e/"]
        if self.harness_dir:
            parts.append(self.harness_dir + "/ (go_harness)")
        if self.globs:
            parts.append("test_glob " + " ".join(self.globs))
        return ", ".join(parts)


class Repo:
    """One task repo's lazily read facts."""

    def __init__(self, repos_dir: str, name: str) -> None:
        self.name = name
        self.dir = os.path.join(repos_dir, name)
        self._rules: Optional[TestPathRules] = None

    def commit(self, rev: str) -> str:
        """rev's full commit name ("" when it names none; a value that is
        not hex is never resolved, so no ref or option syntax reaches git)."""
        if not _HEX_REV.fullmatch(rev):
            return ""
        out, err = run_git(self.dir, "rev-parse", "--verify", "--quiet", rev + "^{commit}")
        if err is not None:
            return ""
        s = trim_space(out)
        return s if _FULL_SHA.fullmatch(s) else ""

    def ancestor(self, a: str, b: str) -> bool:
        return run_git(self.dir, "merge-base", "--is-ancestor", a, b)[1] is None

    def test_rules(self) -> TestPathRules:
        if self._rules is None:
            self._rules = TestPathRules.at(self.dir)
        return self._rules

    def check_unit_commits(self, t: str, i: str) -> str:
        """One unit's commit pair judged ("" = test-first)."""
        if t == i:
            return "test_commit and impl_commit are the same commit — commit the test files alone first"
        parents, err = run_git(self.dir, "rev-list", "--parents", "-n", "1", t)
        if err is not None:
            return "test_commit is unreadable: " + err
        n = len(fields(parents)) - 1
        if n != 1:
            return "test_commit %s has %d parents — it is one ordinary commit holding the unit's test files" % (short_sha(t), n)
        if not self.ancestor(t, i):
            return "test_commit %s is not an ancestor of impl_commit %s — the test is committed red before the implementation" % (
                short_sha(t), short_sha(i))
        if not self.ancestor(i, "HEAD"):
            return "impl_commit %s is not in HEAD's history" % short_sha(i)
        files, err = run_git(self.dir, "diff-tree", "-r", "--no-commit-id", "--name-only", "--no-renames", t)
        if err is not None:
            return "test_commit's diff is unreadable: " + err
        paths, bad = [], []
        rules = self.test_rules()
        for p in files.split("\n"):
            p = trim_space(p)
            if not p:
                continue
            paths.append(p)
            if not rules.is_test(p):
                bad.append(p)
        if not paths:
            return "test_commit %s changes no file" % short_sha(t)
        if bad:
            return "test_commit %s touches non-test paths %s (test paths here: %s) — the test commit holds the unit's test files alone" % (
                short_sha(t), ", ".join(clip_list(bad, 5)), rules.describe())
        return ""

    # carries -------------------------------------------------------------

    def check_e2e_carries(self, unit_repo: bool, bindings: Sequence["Binding"]) -> List[str]:
        """The e2e suites the build added or changed in this repo judged
        ([] = fine): each declares carries:, and one it added or a unit's
        recorded run (units.json) names covers the change."""
        ref = origin_default_ref(self.dir)
        if not ref:
            if not unit_repo and not self._local_commits():
                return []
            return ["repos/%s: the fetched origin default branch is unknown, so the e2e suites this build changed cannot be judged" % self.name]
        out, err = run_git(self.dir, "merge-base", "HEAD", ref)
        if err is not None:
            return ["repos/%s: the diff base is unresolvable, so the e2e suites this build changed cannot be judged: %s" % (self.name, err)]
        base = trim_space(out)
        status, err = run_git(self.dir, "diff", "--name-status", "-z", "-M", base + "..HEAD")
        if err is not None:
            return ["repos/%s: the build's diff is unreadable, so the e2e suites it changed cannot be judged: %s" % (self.name, err)]
        touched, present, added = diff_name_status(status)
        roots: Dict[str, CarriesRoot] = {}
        problems: List[str] = []
        for p in present:
            root = self._carries_root_of(base, gotext.path_dir(p), roots)
            rel, ok = root.rel(p)
            if not ok or not root.is_suite(rel):
                continue
            src, err = run_git(self.dir, "show", "HEAD:" + p)
            if err is not None:
                problems.append("e2e suite repos/%s/%s is unreadable at HEAD: %s" % (self.name, p, err))
                continue
            globs, malformed = carries_header(src)
            if malformed:
                problems.append("e2e suite repos/%s/%s has a malformed `carries:` declaration (an empty value, or a `carries` line without its colon)" % (self.name, p))
                continue
            if not globs:
                if rel in self._carries_graph_of(root):
                    continue  # a helper a suite imports
                problems.append(
                    "e2e suite repos/%s/%s declares no `carries:` in its opening comment block — open it with `// carries: <glob>, ...` (`# carries:` where comments start with `#`) naming the paths it covers; the non-test paths this build touched under %s: %s"
                    % (self.name, p, root.label(), list_or_none(self._covered_paths(root, touched)))
                )
                continue
            if p in added:
                which = "which this build added"
            elif suite_bound(bindings, self.name, p, rel):
                which = "which a unit's recorded run names"
            else:
                continue  # an edited suite owes only its declaration
            covered = self._covered_paths(root, touched)
            if not covered or matches_any(globs, covered):
                continue
            problems.append(
                "e2e suite repos/%s/%s, %s, declares `carries: %s`, which matches none of the non-test paths this build touched under %s: %s — its `carries:` names the paths the suite covers, by which verification selects it (docs/contributing.md, \"Carries\")"
                % (self.name, p, which, ", ".join(globs), root.label(), list_or_none(covered))
            )
        return problems

    def _local_commits(self) -> bool:
        refs, err = run_git(self.dir, "for-each-ref", "--count=1", "refs/remotes")
        if err is not None or not trim_space(refs):
            return False
        out, err = run_git(self.dir, "rev-list", "-n", "1", "HEAD", "--not", "--remotes")
        return err is None and trim_space(out) != ""

    def _covered_paths(self, root: "CarriesRoot", touched: Sequence[str]) -> List[str]:
        out = []
        for p in touched:
            rel, ok = root.rel(p)
            if not ok or self.test_rules().is_test(p) or carries_doc_path(rel) or root.excluded(rel) or root.forces_full(rel):
                continue
            out.append(rel)
        return out

    def _carries_root_of(self, base: str, d: str, cache: Dict[str, "CarriesRoot"]) -> "CarriesRoot":
        if d in (".", "/"):
            d = ""
        if d in cache:
            return cache[d]

        def read(rel: str) -> str:
            return run_git(self.dir, "show", base + ":" + gotext.path_clean(_join(d, rel)))[0]

        conf, err = run_git(self.dir, "show", base + ":" + gotext.path_clean(_join(d, VERIFY_SCOPE_CONF)))
        if err is None:
            root = read_carries_root(d, conf, read)
        elif d == "":
            root = read_carries_root(d, "", read)
        else:
            root = self._carries_root_of(base, gotext.path_dir(d), cache)
        if root.dir == d and root.analyzer == "?":
            root.analyzer = self._manifest_analyzer(base, d)
        cache[d] = root
        return root

    def _manifest_analyzer(self, base: str, d: str) -> str:
        tree = base + ":" + d if d else base
        out, err = run_git(self.dir, "ls-tree", "-z", "--name-only", tree)
        if err is not None:
            return ""
        has = set(out.split("\x00"))
        found = []
        for analyzer, a, b in (("go", "go.mod", ""), ("npm", "package.json", ""), ("python", "pyproject.toml", "requirements.txt"), ("rust", "Cargo.toml", "")):
            if a in has or (b and b in has):
                found.append(analyzer)
        return found[0] if len(found) == 1 else ""

    def _carries_graph_of(self, root: "CarriesRoot") -> Dict[str, bool]:
        """The root's import graph at HEAD as the engine reads it: every
        root-relative file an edge reachable from a suite candidate targets."""
        if root.graph is not None:
            return root.graph
        g: Dict[str, bool] = {}
        root.graph = g
        scope = ["ls-tree", "-r", "-z", "--name-only", "HEAD"]
        if root.dir:
            scope += ["--", root.dir]
        listed, err = run_git(self.dir, *scope)
        if err is not None:
            return g
        exists = set()
        frontier: List[str] = []
        for p in listed.split("\x00"):
            rel, ok = root.rel(p)
            if ok and p:
                exists.add(rel)
                if root.is_suite(rel):
                    frontier.append(rel)
        words = {"npm": ["from", "import", "require"], "python": ["import"], "rust": ["mod"]}.get(root.analyzer, [])
        args = ["grep", "-z", "-I", "-F"]
        for w in words:
            args += ["-e", w]
        args.append("HEAD")
        if root.dir:
            args += ["--", root.dir]
        out, _ = run_git(self.dir, *args)  # exit 1 = no line
        lines: Dict[str, List[str]] = {}
        for rec in out.split("\n"):
            name, sep, line = rec.partition("\x00")
            if not sep:
                continue
            if name.startswith("HEAD:"):
                name = name[len("HEAD:"):]
            rel, ok = root.rel(name)
            if ok:
                lines.setdefault(rel, []).append(line)

        def has(p: str) -> bool:
            return p in exists

        seen = set(frontier)
        while frontier:
            nxt: List[str] = []
            for f in frontier:
                targets: List[str] = []
                if root.analyzer == "npm" and js_module(f):
                    targets = js_imports(f, lines.get(f, []), has)
                elif root.analyzer == "python" and f.endswith(".py"):
                    targets = py_imports(f, lines.get(f, []), has)
                elif root.analyzer == "rust" and f.endswith(".rs"):
                    targets = rs_mods(f, lines.get(f, []), has)
                for t in targets:
                    g[t] = True
                    if t not in seen:
                        seen.add(t)
                        nxt.append(t)
            frontier = nxt
        return g


def _join(d: str, rel: str) -> str:
    return d + "/" + rel if d else rel


# ---------------------------------------------------------------------------
# test_weakened

_ASSERTION_LIKE = re.compile(
    "(?:" + _ci("assert") + "|" + _ci("expect") + "|" + _ci("require") + r")|Error|Fatal|\bt\.|===|!==", re.ASCII
)
_SKIP_ADDED = re.compile(
    r"\bt\.Skip|\.skip\(|\bxit\(|\bxdescribe\(|\btest\.skip|@pytest\.mark\.skip|@unittest\.skip|#\[ignore\]", re.ASCII
)
_CARRIES_LINE = re.compile(r"\bcarries:", re.ASCII)
_FINDING_CITE = re.compile(r"\bs[1-9][0-9]*#[1-9][0-9]*\b", re.ASCII)

HUNK_LINES_MAX = 16
HUNK_LINE_RUNES = 160


def weakened_facts(repos_dir: str, units: Sequence[Unit], summary: str) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]]]:
    """The test_weakened facts of the units (and the ones summary cites)."""
    cite = ""
    m = _FINDING_CITE.search(summary)
    if m:
        cite = m.group(0)
    weakened: List[Dict[str, Any]] = []
    cited: List[Dict[str, Any]] = []
    for u in units:
        if u.why or not u.test_files:
            continue
        args = ["diff", "--no-color", "--no-ext-diff", "--no-renames", "-U0", u.test_commit, "HEAD", "--"] + u.test_files
        diff, err = run_git(os.path.join(repos_dir, u.repo), *args)
        if err is not None:
            weakened.append({"unit": u.id, "repo": u.repo, "path": ", ".join(u.test_files),
                             "reason": "the diff since the test commit is unreadable: " + err})
            continue
        for path, header, lines in diff_hunks(diff):
            reason = weakening_reason(lines)
            if not reason:
                continue
            f: Dict[str, Any] = {"unit": u.id, "repo": u.repo, "path": path, "reason": reason}
            hunk = clip_hunk(header, lines)
            if hunk:
                f["hunk"] = hunk
            if cite and (path in summary or gotext.path_base(path) in summary):
                f["cite"] = cite
                cited.append(f)
                continue
            weakened.append(f)
    return weakened, cited


def diff_hunks(diff: str) -> List[Tuple[str, str, List[str]]]:
    out: List[Tuple[str, str, List[str]]] = []
    path = ""
    cur: Optional[List[str]] = None
    for line in diff.split("\n"):
        if line.startswith("diff --git "):
            cur = None
            i = line.rfind(" b/")
            if i >= 0:
                path = line[i + 3:]
        elif line.startswith("--- ") or line.startswith("+++ "):
            pass
        elif line.startswith("@@"):
            cur = []
            out.append((path, line, cur))
        elif cur is not None and (line.startswith("+") or line.startswith("-")):
            cur.append(line)
    return out


def weakening_reason(lines: Sequence[str]) -> str:
    reasons: List[str] = []
    deleted = asserts = 0
    for line in lines:
        body = line[1:]
        if line[0] == "-":
            deleted += 1
            if _ASSERTION_LIKE.search(body):
                asserts += 1
        if line[0] == "+" and _SKIP_ADDED.search(body):
            reasons.append("adds a skip")
        if _CARRIES_LINE.search(body):
            reasons.append("changes the `carries:` header")
    if deleted > 0:
        r = "deletes or changes %d line(s)" % deleted
        if asserts > 0:
            r += ", %d of them assertion-looking" % asserts
        reasons.insert(0, r)
    return "; ".join(dedupe(reasons))


def clip_hunk(header: str, lines: Sequence[str]) -> str:
    out = [header]
    for i, line in enumerate(lines):
        if i == HUNK_LINES_MAX:
            out.append("\n… (%d more line(s))" % (len(lines) - HUNK_LINES_MAX))
            break
        out.append("\n" + clip_runes(line, HUNK_LINE_RUNES))
    return "".join(out)


# ---------------------------------------------------------------------------
# plan conformance

_UNITS_HEADING = re.compile("^##" + _S + "+" + _ci("units") + r"\b", re.ASCII)
_NONE_DECL = re.compile("^" + _ci("none") + r"\b", re.ASCII)


def changed_files(repos_dir: str) -> Tuple[Optional[Dict[str, List[str]]], str]:
    """Every task repo's committed diff against its origin default branch:
    repo -> changed repo-relative paths; a reason when it cannot be read."""
    names = task_repo_names(repos_dir) if repos_dir else []
    if not names:
        return None, "no task repo is materialized under repos/"
    out: Dict[str, List[str]] = {}
    for name in names:
        d = os.path.join(repos_dir, name)
        ref = origin_default_ref(d)
        if not ref:
            return None, "repo %s: the fetched origin default branch (origin/HEAD, origin/main, origin/master) is unknown, so the diff base cannot be resolved" % name
        base, err = run_git(d, "merge-base", "HEAD", ref)
        if err is not None:
            return None, "repo %s: git merge-base HEAD %s failed: %s" % (name, ref, err)
        diff, err = run_git(d, "diff", "--name-only", "--no-renames", trim_space(base) + "..HEAD")
        if err is not None:
            return None, "repo %s: git diff --name-only failed: %s" % (name, err)
        for p in diff.split("\n"):
            p = trim_space(p)
            if p:
                out.setdefault(name, []).append(p)
    return out, ""


def clean_repo_path(v: Any) -> str:
    s = trim_space(v) if isinstance(v, str) else ""
    if not s:
        return ""
    s = gotext.path_clean(s)
    if s.startswith("repos/"):
        s = s[len("repos/"):]
    return s


def fold_plan_conformance(touched_files: Sequence[Any], authored: Sequence[str],
                          changed: Optional[Dict[str, List[str]]], unavailable: str) -> Dict[str, Any]:
    """The pure fold: the declared set against the changed files. The
    answer's lists are None where the Go fold left them nil."""
    obligations_out: List[Dict[str, Any]] = []
    pc: Dict[str, Any] = {"unavailable": unavailable, "declared_writes": None, "undeclared": None,
                          "unchanged_obligations": None, "unchanged_writes": None, "released": None}
    declared: Dict[str, bool] = {}
    writes: List[str] = []
    obligations: List[str] = []
    parents: Dict[str, List[str]] = {}
    for it in touched_files:
        f = it if isinstance(it, dict) else {}
        if f.get("intent") != "write":
            continue
        p = clean_repo_path(f.get("path"))
        if not p:
            continue
        dw: Dict[str, Any] = {"path": p}
        obs: List[Dict[str, Any]] = []
        lst = f.get("coupled_obligations")
        for o in lst if isinstance(lst, list) else []:
            om = o if isinstance(o, dict) else {}
            if om.get("none") is True:
                r = om.get("reason")
                dw["none_reason"] = r if isinstance(r, str) else ""
                continue
            op = clean_repo_path(om.get("path"))
            if op:
                parents.setdefault(op, []).append(p)
                note = om.get("note")
                ob: Dict[str, Any] = {"path": op}
                if isinstance(note, str) and note:
                    ob["note"] = note
                obs.append(ob)
                if not declared.get(op):
                    obligations.append(op)
                declared[op] = True
        out_w: Dict[str, Any] = {"path": p}
        if obs:
            out_w["obligations"] = obs
        if dw.get("none_reason"):
            out_w["none_reason"] = dw["none_reason"]
        obligations_out.append(out_w)
        if not declared.get(p):
            writes.append(p)
        declared[p] = True
    if obligations_out:
        pc["declared_writes"] = obligations_out
    for a in authored:
        p = clean_repo_path(a)
        if p:
            declared[p] = True
    if unavailable:
        return pc
    changed = changed or {}
    repos = sorted(changed)
    touched = set()
    undeclared: List[str] = []
    for name in repos:
        for p in changed[name]:
            touched.add(p)
            touched.add(name + "/" + p)
            if declared.get(p) or declared.get(name + "/" + p):
                continue
            undeclared.append(name + "/" + p if len(repos) > 1 else p)
    released: List[str] = []
    unchanged_obligations: List[str] = []
    for p in obligations:
        if p in touched:
            continue
        if not any(w in touched for w in parents.get(p, [])):
            released.append("`%s` — lapsed: no write that declares it is in the diff" % p)
        else:
            unchanged_obligations.append(p)
    unchanged_writes = [p for p in writes if p not in touched]
    pc["undeclared"] = undeclared or None
    pc["unchanged_obligations"] = unchanged_obligations or None
    pc["unchanged_writes"] = unchanged_writes or None
    pc["released"] = released or None
    return pc


def conformant(pc: Dict[str, Any]) -> bool:
    return not pc["unavailable"] and not pc["undeclared"] and not pc["unchanged_obligations"]


def plan_touched_files(md: str) -> List[Dict[str, Any]]:
    """A develop plan's `## Units` declarations in the plan step's
    touched_files shape (the whole plan without a `## Units` heading)."""
    lines = md.split("\n")
    frm, to = 0, len(lines)
    for i, line in enumerate(lines):
        if _UNITS_HEADING.search(trim_space(line)):
            frm, to = i + 1, len(lines)
            for j in range(i + 1, len(lines)):
                s = trim_space(lines[j])
                if s.startswith("## ") or s.startswith("# "):
                    to = j
                    break
            break
    out: List[Dict[str, Any]] = []
    last: List[Dict[str, Any]] = []
    for line in lines[frm:to]:
        kv = plan_line(line)
        if kv is None:
            continue
        key, val = kv
        if key == "touched files":
            last = []
            for e in split_plan_entries(val):
                path, paren = plan_entry(e)
                if not path or gotext.equal_fold(e.strip("() "), "no file edits"):
                    continue
                intent = "read" if gotext.equal_fold(paren, "read") else "write"
                f: Dict[str, Any] = {"path": path, "intent": intent}
                out.append(f)
                if intent == "write":
                    last.append(f)
        elif key == "coupled obligations":
            obs: Optional[List[Dict[str, Any]]] = None
            v = trim_space(trim_space(val).strip("`"))
            if _NONE_DECL.search(v):
                obs = [{"none": True, "reason": trim_space(v[4:]).lstrip("—-–:, ")}]
            else:
                for e in split_plan_entries(val):
                    path, note = plan_entry(e)
                    if path:
                        obs = obs or []
                        obs.append({"path": path, "note": note})
            for f in last:
                f["coupled_obligations"] = obs
    return out


def plan_line(line: str) -> Optional[Tuple[str, str]]:
    t = trim_space(line).lstrip("-*+ ").lstrip("*_")
    k, sep, v = t.partition(":")
    if not sep:
        return None
    k = gotext.to_lower(trim_space(k).strip("*_"))
    if k not in ("touched files", "coupled obligations"):
        return None
    return k, trim_space(v).lstrip("*_ ")


def split_plan_entries(v: str) -> List[str]:
    out = []
    depth, tick, start = 0, False, 0
    for i, r in enumerate(v):
        if r == "`":
            tick = not tick
        elif r == "(" and not tick:
            depth += 1
        elif r == ")" and not tick and depth > 0:
            depth -= 1
        elif r == "," and not tick and depth == 0:
            out.append(trim_space(v[start:i]))
            start = i + 1
    out.append(trim_space(v[start:]))
    return out


def plan_entry(e: str) -> Tuple[str, str]:
    path = ""
    i = e.find("`")
    if i >= 0:
        j = e.find("`", i + 1)
        if j >= 0:
            path = e[i + 1:j]
    if not path:
        fs = fields(e)
        if fs and not fs[0].startswith("("):
            path = fs[0]
    paren = ""
    i = e.find("(")
    if i >= 0:
        j = e.rfind(")")
        if j > i:
            paren = trim_space(e[i + 1:j])
    return trim_space(path), paren


# ---------------------------------------------------------------------------
# carries


class Binding:
    """A unit's recorded run in a repo (units.json `run`, its argv joined);
    the words of its command may name a suite."""

    def __init__(self, repo: str, command: str) -> None:
        self.repo = repo
        self.command = command


class CarriesRoot:
    """One project root's conf facts, read at the diff base."""

    def __init__(self, d: str) -> None:
        self.dir = d
        # analyzer: npm, python, rust or go; "" none; "?" the manifests decide
        self.analyzer = "?"
        self.suite_glob: List[str] = []
        self.excludes: List[str] = []
        self.forced: List[str] = []
        self.runners: Dict[str, bool] = {}
        self.graph: Optional[Dict[str, bool]] = None

    def rel(self, p: str) -> Tuple[str, bool]:
        if not self.dir:
            return p, True
        if p.startswith(self.dir + "/"):
            return p[len(self.dir) + 1:], True
        return p, False

    def label(self) -> str:
        return self.dir + "/" if self.dir else "the repo root"

    def excluded(self, rel: str) -> bool:
        return globs_match_path_or_base(self.excludes, rel)

    def forces_full(self, rel: str) -> bool:
        if rel == VERIFY_SCOPE_CONF or globs_match_path_or_base(self.forced, rel):
            return True
        base = gotext.path_base(rel)
        a = self.analyzer
        if a == "go":
            return rel in ("go.mod", "go.sum")
        if a == "npm":
            if rel in ("package.json", "package-lock.json", "npm-shrinkwrap.json", "tsconfig.json"):
                return True
            return (goglob.case_match("tsconfig.*", base) or goglob.case_match("eslint.config.*", base)
                    or base == ".eslintrc" or goglob.case_match(".eslintrc.*", base))
        if a == "python":
            if rel in ("pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "tox.ini"):
                return True
            return goglob.case_match("requirements*.txt", rel) or base == "conftest.py"
        if a == "rust":
            if rel in ("Cargo.toml", "Cargo.lock", "build.rs", "rust-toolchain", "rust-toolchain.toml"):
                return True
            return rel.startswith(".cargo/")
        return False

    def has_carries_layer(self) -> bool:
        return self.analyzer in ("npm", "python", "rust")

    def is_suite(self, rel: str) -> bool:
        if not self.has_carries_layer():
            return False
        found = False
        if self.suite_glob:
            for g in self.suite_glob:
                found = found or goglob.case_match(g, rel)
        else:
            d, base = gotext.path_dir(rel), gotext.path_base(rel)
            if self.analyzer == "npm":
                found = d == "e2e" and base.endswith(".mjs")
            elif self.analyzer == "python":
                found = ((rel.startswith("e2e/") or rel.startswith("tests/e2e/")) and base.endswith(".py")
                         and (base.startswith("test_") or base.endswith("_test.py")))
            elif self.analyzer == "rust":
                found = d == "tests" and base.endswith(".rs")
        return found and not self.excluded(rel) and not self.runners.get(rel, False)


PACKAGE_JSON = gd.Struct({"scripts": gd.Map(gd.ANY)})


def read_carries_root(d: str, conf: str, read: Callable[[str], str]) -> CarriesRoot:
    """A root's carries keys from its conf text, as the engine reads them."""
    root = CarriesRoot(d)
    runner_line = ""
    for line in conf.split("\n"):
        line = line.lstrip(" \t\r\v\f")
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if not sep:
            continue
        v = val.rstrip(" \t\r\v\f")
        if key == "stack":
            if root.analyzer == "?":
                if v in ("go", "npm", "python", "rust"):
                    root.analyzer = v
                elif v == "none":
                    root.analyzer = ""
        elif key == "suite_glob":
            if v:
                root.suite_glob.append(v)
        elif key == "exclude":
            if val:
                root.excludes.append(val)
        elif key == "forced_full":
            if val:
                root.forced.append(val)
        elif key == "e2e_runner":
            if not runner_line:
                runner_line = v
    words = fields(runner_line)
    if len(words) >= 3 and words[0] == "npm" and words[1] == "run":
        try:
            pkg, err = gd.decode(loads(read("package.json")), PACKAGE_JSON)
        except ValueError:
            pkg, err = None, "not JSON"
        if err is None and pkg is not None:
            cmd = (pkg["scripts"] or {}).get(words[2])
            if isinstance(cmd, str):
                words += fields(cmd)
    for w in words:
        if w.startswith("./"):
            w = w[2:]
        if w:
            root.runners[w] = True
    return root


def globs_match_path_or_base(globs: Sequence[str], rel: str) -> bool:
    base = gotext.path_base(rel)
    return any(goglob.case_match(g, rel) or goglob.case_match(g, base) for g in globs)


def diff_name_status(out: str) -> Tuple[List[str], List[str], Dict[str, bool]]:
    """`git diff --name-status -z -M`: every touched path (a rename's both
    sides), the paths present at HEAD and those added."""
    added: Dict[str, bool] = {}
    touched: List[str] = []
    present: List[str] = []
    f = (out[:-1] if out.endswith("\x00") else out).split("\x00")
    i = 0
    while i < len(f):
        st = f[i]
        if not st:
            i += 1
            continue
        if st[0] in "RC":
            if i + 2 >= len(f):
                break
            touched += [f[i + 1], f[i + 2]]
            present.append(f[i + 2])
            i += 3
            continue
        if i + 1 >= len(f):
            break
        p = f[i + 1]
        touched.append(p)
        if st[0] != "D":
            present.append(p)
        if st[0] == "A":
            added[p] = True
        i += 2
    return touched, present, added


def _bound_word(c: str) -> bool:
    return ("a" <= c <= "z") or ("A" <= c <= "Z") or ("0" <= c <= "9") or c in "._/-"


def suite_bound(bindings: Sequence[Binding], repo: str, p: str, rel: str) -> bool:
    for b in bindings:
        if b.repo != repo:
            continue
        cur: List[str] = []
        words = []
        for c in b.command:
            if _bound_word(c):
                cur.append(c)
            elif cur:
                words.append("".join(cur))
                cur = []
        if cur:
            words.append("".join(cur))
        for w in words:
            if w.startswith("./"):
                w = w[2:]
            if w == p or w == rel:
                return True
    return False


_JS_IMPORT = re.compile(
    "(from|import|require)" + _POSIX_SPACE + r"*\(?" + _POSIX_SPACE + r"*['\"](\.\.?/[^'\"]+)['\"]", re.ASCII
)
_PY_IMPORT = re.compile(r"^[ \t]*import[ \t]+", re.ASCII)
_PY_FROM = re.compile(r"^[ \t]*from[ \t]+([.A-Za-z0-9_]+)[ \t]+import[ \t]+", re.ASCII)
_PY_MOD = re.compile(r"[A-Za-z_][A-Za-z0-9_.]*", re.ASCII)
_PY_NAME = re.compile(r"[A-Za-z_][A-Za-z0-9_]*", re.ASCII)
_RS_MOD = re.compile(r"^[ \t]*(pub(\([^)]*\))?[ \t]+)?mod[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]*;", re.ASCII)
_RS_TOP_TEST = re.compile(r"tests/[^/]*\.rs", re.ASCII)


def js_module(f: str) -> bool:
    return gotext.path_ext(f) in (".mjs", ".js", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".jsx")


def parent_dir(p: str) -> str:
    i = p.rfind("/")
    return p[:i] if i >= 0 else ""


def norm_rel(d: str, spec: str) -> str:
    segs: List[str] = []
    for s in (d + "/" + spec).split("/"):
        if s in ("", "."):
            continue
        if s == "..":
            if not segs:
                return ""
            segs.pop()
            continue
        segs.append(s)
    return "/".join(segs)


def js_imports(f: str, lines: Sequence[str], exists: Callable[[str], bool]) -> List[str]:
    out = []
    for line in lines:
        for m in _JS_IMPORT.finditer(line):
            t = norm_rel(parent_dir(f), m.group(2))
            if not t:
                continue
            for c in (t, t + ".mjs", t + ".js", t + ".cjs", t + ".ts", t + ".tsx", t + ".mts", t + ".jsx",
                      t + "/index.mjs", t + "/index.js", t + "/index.ts", t + "/index.tsx"):
                if exists(c):
                    out.append(c)
                    break
    return out


def _first_word(s: str) -> str:
    s = s.lstrip(" \t")
    for i, c in enumerate(s):
        if c in " \t":
            return s[:i]
    return s


def py_imports(f: str, lines: Sequence[str], exists: Callable[[str], bool]) -> List[str]:
    d = parent_dir(f)

    def res(p: str) -> str:
        if exists(p + ".py"):
            return p + ".py"
        if exists(p + "/__init__.py"):
            return p + "/__init__.py"
        return ""

    out: List[str] = []

    def emit(r: str) -> None:
        if r and r != f:
            out.append(r)

    def absimp(m: str) -> None:
        p = m.replace(".", "/")
        r = res(p)
        if not r:
            r = res("src/" + p)
        if not r and d:
            r = res(d + "/" + p)
        emit(r)

    for line in lines:
        i = line.find("#")
        if i >= 0:
            line = line[:i]
        m = _PY_IMPORT.search(line)
        if m:
            for part in line[m.end():].split(","):
                part = _first_word(part)
                if _PY_MOD.fullmatch(part):
                    absimp(part)
            continue
        mf = _PY_FROM.search(line)
        if mf is None:
            continue
        base = mf.group(1)
        names = []
        rest = line[len(mf.group(0)):].replace("(", " ").replace(")", " ").replace("\\", " ")
        for nm in rest.split(","):
            nm = _first_word(nm)
            if _PY_NAME.fullmatch(nm):
                names.append(nm)
        if not base.startswith("."):
            absimp(base)
            for nm in names:
                absimp(base + "." + nm)
            continue
        mod = base.lstrip(".")
        pdir, ok = d, True
        for _ in range(1, len(base) - len(mod)):
            if not pdir:
                ok = False
                break
            pdir = parent_dir(pdir)
        if not ok:
            continue  # climbs out of the root: unresolvable
        prefix = pdir + "/" if pdir else ""
        mod = mod.replace(".", "/")
        if mod:
            r = res(prefix + mod)
            if not r:
                continue
            emit(r)
            for nm in names:
                emit(res(prefix + mod + "/" + nm))
            continue
        for nm in names:
            emit(res(prefix + nm))
        emit(res(prefix + "__init__"))
    return out


def rs_mods(f: str, lines: Sequence[str], exists: Callable[[str], bool]) -> List[str]:
    cdir = f[:-3] if f.endswith(".rs") else f
    b = gotext.path_base(f)
    if b in ("lib.rs", "main.rs", "mod.rs") or _RS_TOP_TEST.fullmatch(f):
        cdir = parent_dir(f)
    prefix = cdir + "/" if cdir else ""
    out = []
    for line in lines:
        m = _RS_MOD.search(line)
        if m is None:
            continue
        name = m.group(3)
        if exists(prefix + name + ".rs"):
            out.append(prefix + name + ".rs")
        elif exists(prefix + name + "/mod.rs"):
            out.append(prefix + name + "/mod.rs")
    return out


def _is_word_start(s: str) -> bool:
    c = s[0]
    return c == "_" or "0" <= c <= "9" or "a" <= c <= "z" or "A" <= c <= "Z"


def carries_header(src: str) -> Tuple[List[str], bool]:
    """A suite's opening comment block parsed like the engine: every
    `carries:` line's globs; malformed for an empty value or a `carries`
    line without its colon."""
    globs: List[str] = []
    malformed = False
    for line in src.split("\n"):
        t = trim_space(line)
        if not t:
            continue
        if not t.startswith("//") and not t.startswith("#"):
            break
        c = t.lstrip("/#")
        if c.startswith("!"):
            c = c[1:]
        c = c.lstrip(" \t")
        if not c.startswith("carries"):
            continue
        rest = c[len("carries"):]
        r = rest.lstrip(" \t")
        if r.startswith(":"):
            v = r[1:]
            fs = [x for x in re.split(r"[, \t\r]", v) if x]
            if not fs:
                malformed = True
            globs += fs
            continue
        if not rest or (rest[0] != "-" and not _is_word_start(rest)):
            malformed = True
    return globs, malformed


def carries_doc_path(rel: str) -> bool:
    if gotext.path_ext(rel) in (".md", ".markdown", ".rst", ".txt"):
        return True
    return rel.startswith("docs/") or rel.startswith("doc/")


def matches_any(globs: Sequence[str], paths: Sequence[str]) -> bool:
    return any(goglob.case_match(g, p) for g in globs for p in paths)


# ---------------------------------------------------------------------------
# micro bounds

MICRO_HARD_EXCLUDED = ("AGENTS.md", "CLAUDE.md")
MICRO_REASON_HEAD = "out of bounds for this verification-less lane: "


def micro_decls(conf: str) -> Tuple[List[str], List[str]]:
    """A conf's micro_path= and micro_check= lines (the engine's rules)."""
    paths: List[str] = []
    checks: List[str] = []
    for line in conf.split("\n"):
        line = line.lstrip(" \t")
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if not sep:
            continue
        if key == "micro_path":
            val = val.rstrip(" \t\r")
            if not micro_path_valid(val) or val in paths:
                continue
            paths.append(val)
        elif key == "micro_check":
            val = val.rstrip("\r")
            if not trim_space(val) or val.startswith("$(") or val.startswith("`"):
                continue
            checks.append(val)
    return paths, checks


def micro_path_valid(p: str) -> bool:
    if not trim_space(p) or p.startswith("/") or p.startswith("./"):
        return False
    return ".." not in p.split("/")


def changed_paths_z(out: str) -> List[str]:
    f = (out[:-1] if out.endswith("\x00") else out).split("\x00")
    paths: List[str] = []

    def add(p: str) -> None:
        if p and p not in paths:
            paths.append(p)

    i = 0
    while i < len(f):
        status = f[i]
        if status:
            n = 2 if status[0] in "RC" else 1
            j = 0
            while j < n and i + 1 < len(f):
                i += 1
                add(f[i])
                j += 1
        i += 1
    return paths


def micro_bounds_of(repos_dir: str, evidence: Sequence[Dict[str, str]]) -> Dict[str, Any]:
    """Every task repo under repos_dir judged, and the credential plan."""
    repos_out: List[Dict[str, Any]] = []
    problems: List[str] = []

    def refuse(repo: str, why: str) -> None:
        problems.append("repos/" + repo + ": " + why)

    if not repos_dir:
        problems.append("the capsule's task repos are unknown")
    for name in task_repo_names(repos_dir):
        d = os.path.join(repos_dir, name)
        ref = origin_default_ref(d)
        if not ref:
            refuse(name, "the fetched origin default branch is unknown, so the diff cannot be read")
            continue
        base_out, err = run_git(d, "merge-base", "HEAD", ref)
        if err is not None:
            refuse(name, "the diff base (merge-base HEAD " + (ref[len("refs/remotes/"):] if ref.startswith("refs/remotes/") else ref) + ") is unreadable")
            continue
        base = trim_space(base_out)
        commits, err = run_git(d, "rev-list", base + "..HEAD")
        if err is not None:
            refuse(name, "the history beyond the diff base is unreadable")
            continue
        if not trim_space(commits):
            continue  # not touched by this change
        diff, err = run_git(d, "diff", "--name-status", "-z", "-M", base + "..HEAD")
        if err is not None:
            refuse(name, "the diff " + short_sha(base) + "..HEAD is unreadable")
            continue
        changed = changed_paths_z(diff)
        conf, err = run_git(d, "show", base + ":" + VERIFY_SCOPE_CONF)
        if err is not None:
            refuse(name, "no " + VERIFY_SCOPE_CONF + " is readable at the diff base " + short_sha(base)
                   + ", so the repository declares no micro_path (it is not opted in)")
            continue
        declared, checks = micro_decls(conf)
        if not declared:
            refuse(name, "its " + VERIFY_SCOPE_CONF + " at the diff base declares no micro_path (the repository is not opted in)")
            continue
        out = []
        for p in changed:
            if gotext.path_base(p) in MICRO_HARD_EXCLUDED:
                out.append(p + " (an agent-contract file, never in bounds)")
            elif not any(goglob.micro_glob_match(g, p) for g in declared):
                out.append(p)
        if out:
            refuse(name, "changed path(s) outside its declared micro_path " + gotext.quote_list(declared) + ": " + ", ".join(out))
            continue
        cmds = [{"repo": e["repo"], "command": e["command"]} for e in evidence if e["repo"] == name]
        for c in checks:
            cc = trim_space(c)
            if not any(e["command"] == cc for e in cmds):
                cmds.append({"repo": name, "command": cc})
        if not cmds:
            refuse(name, "no fast evidence command is reported or declared for it, so the delivery has nothing to re-run")
            continue
        repos_out.append({"name": name, "base": base, "changed_paths": changed or None, "declared_paths": declared, "commands": cmds})
    if not problems and not repos_out:
        problems.append("the change touches no task repository")
    if problems:
        return {"in_bounds": False, "reason": MICRO_REASON_HEAD + "; ".join(problems), "repos": []}
    return {"in_bounds": True, "repos": repos_out}


# ---------------------------------------------------------------------------
# replay


# NOT_RUN_EXITS: 126 (found, not executable) and 127 (not found) — the
# command itself never ran: not judged (exit 2).
NOT_RUN_EXITS = (126, 127)

# NOT_RUNNABLE_RULES: (toolchain, rule, one-line pattern) — a line of a
# replayed run's output by which the run is known not to have reached its
# tests' assertions (a package that did not build or set up, a compile or
# import error, a module or package not found). Such a run is not red: it
# carries no red evidence. A Go assertion's own line (`    x_test.go:12:`)
# is indented and has no column, so the compile-error rule leaves it alone.
# Each pattern is anchored at the start of the line the toolchain itself
# prints (a `go: ` or `<file>.go:L:C: ` report, a Node `Error …:` header),
# never matched anywhere in a line: an assertion message that quotes such
# a line stays red.
NOT_RUNNABLE_RULES: Tuple[Tuple[str, str, str], ...] = (
    ("go", "setup-failed", r"^FAIL\s+\S+ \[setup failed\]"),
    ("go", "build-failed", r"^FAIL\s+\S+ \[build failed\]"),
    ("go", "embed-pattern", r"^(\s+imports \S+: )?\S+\.go:\d+:\d+: pattern \S+: no matching files found$"),
    ("go", "compile-error", r"^(\./)?[^\s:]+\.go:\d+:\d+: "),
    ("go", "missing-package", r"^(go: |\S+\.go:\d+:\d+: |\s+imports \S+: )(.*: )?(cannot find package|no required module provides package)"),
    ("node", "module-not-found", r"^\s*Error \[ERR_MODULE_NOT_FOUND\]: "),
    ("node", "cannot-find", r"^\s*Error: Cannot find (module|package) '"),
    ("python", "import-error", r"^(ModuleNotFoundError|ImportError): "),
)


def not_run(exit_code: int) -> bool:
    """126 (found, not executable) or 127 (not found): it never ran."""
    return exit_code in NOT_RUN_EXITS


def replay_verdict(exit_code: int, timed_out: bool, output: str) -> Tuple[str, Optional[Dict[str, str]]]:
    """A replayed run judged: ("pass", None) for exit 0; ("not_judged",
    None) when its time limit killed it or the command never ran
    (NOT_RUN_EXITS); ("not_runnable", {toolchain, rule, line}) when a line
    of its output matches a NOT_RUNNABLE_RULES pattern (the first such
    line) — also when another package's assertions failed beside it, as
    nothing then shows the unit's own test ran; else ("red", None)."""
    if timed_out or not_run(exit_code):
        return "not_judged", None
    if exit_code == 0:
        return "pass", None
    compiled = [(t, r, re.compile(p)) for t, r, p in NOT_RUNNABLE_RULES]
    for line in output.split("\n"):
        line = line.rstrip("\r")
        for toolchain, rule, rx in compiled:
            if rx.search(line):
                return "not_runnable", {"toolchain": toolchain, "rule": rule, "line": line}
    return "red", None


# BASE_SETUP_RULE: the not-runnable rule of a replay whose base_setup= line
# failed (the tree could not be prepared, so the run proves nothing).
BASE_SETUP_RULE = "base-setup"


def base_setup_lines(tree: str) -> List[str]:
    """The base_setup= lines of tree's .polyforge/verify-scope.conf (the
    engine's rules: comments and empty values skipped, a value opening a
    command substitution refused); [] when there is none."""
    try:
        conf = read_text(os.path.join(tree, VERIFY_SCOPE_CONF))
    except OSError:
        return []
    out: List[str] = []
    for line in conf.split("\n"):
        line = line.lstrip(" \t")
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if not sep or key != "base_setup":
            continue
        val = val.rstrip("\r")
        if not trim_space(val) or val.startswith("$(") or val.startswith("`"):
            continue
        out.append(val)
    return out


def keep_first_run(log_dir: str, log: str) -> str:
    """The first run's logs moved from log_dir to log_dir/first (the run
    replayed again writes its own there); the moved command log's path, in
    log's form ("" when there was none)."""
    first = os.path.join(log_dir, "first")
    try:
        os.makedirs(first, exist_ok=True)
        for e in os.listdir(log_dir):
            src = os.path.join(log_dir, e)
            if os.path.isfile(src):
                os.replace(src, os.path.join(first, e))
    except OSError:
        return ""
    if not log:
        return ""
    return os.path.join(os.path.dirname(log), "first", os.path.basename(log))


def run_output(capsule_dir: str, log: str) -> str:
    """A command log's output: its text without the runner's `$ <argv>` and
    `# cwd:` header ("" when there is no log)."""
    if not log:
        return ""
    try:
        text = read_text(log if os.path.isabs(log) else os.path.join(capsule_dir, log))
    except OSError:
        return ""
    lines = text.split("\n")
    head = 0
    while head < min(2, len(lines)) and (lines[head].startswith("$ ") or lines[head].startswith("# cwd: ")):
        head += 1
    return "\n".join(lines[head:])


class ReplayError(Exception):
    """An infrastructure failure of one replay: never a verdict."""


_REPLAY_TOKEN = re.compile(r"[^A-Za-z0-9._-]+", re.ASCII)
REPLAY_DIR = "pf-replay"


def replay(capsule_dir: str, unit: Unit, run: Dict[str, Any], deadline_at: float, unit_timeout: float,
           log_dir: str, seq: int, env: Mapping[str, str], co: container.ContainerOptions,
           isolation: str) -> Dict[str, Any]:
    """The unit's run on the tree of its test commit; its ReplayResult as
    the details carry it. ReplayError for an infrastructure failure."""
    if unit.why:
        raise ReplayError("unit %s is unresolved: %s" % (clip_text(unit.id), unit.why))
    if not run["argv"]:
        raise ReplayError("unit %s reports no run (units.json run.argv is empty): nothing to replay" % clip_text(unit.id))
    if run["repo"] != unit.repo:
        raise ReplayError("unit %s: its run names repos/%s, its commits are in repos/%s" % (clip_text(unit.id), clip_text(run["repo"]), unit.repo))
    cwd = gotext.path_clean(trim_space(run["cwd"]))
    if cwd in ("", "."):
        cwd = "."
    elif cwd.startswith("/") or cwd == ".." or cwd.startswith("../"):
        raise ReplayError("unit %s: its run's cwd %s is not a directory inside the repo" % (clip_text(unit.id), quote(clip_text(run["cwd"]))))
    repo_dir = os.path.join(capsule_dir, "repos", unit.repo)
    if not os.path.isdir(os.path.join(repo_dir, ".git")):
        raise ReplayError("repos/%s is not a clone with its own .git directory, so no replay worktree can be added under it" % unit.repo)
    clone_isolation = isolation
    if not clone_isolation:
        clone_isolation = runner.ISOLATION_CONTAINER if container.has_dockerfile_at(repo_dir, "HEAD") else runner.ISOLATION_HOST
    run_git(repo_dir, "worktree", "prune")
    token = "%d-%s-%s" % (seq, _REPLAY_TOKEN.sub("-", unit.id).strip("-"), secrets.token_hex(4))
    wt_rel = ".git/" + REPLAY_DIR + "/" + token
    wt = os.path.join(repo_dir, wt_rel)
    try:
        os.makedirs(os.path.dirname(wt), exist_ok=True)
    except OSError as err:
        raise ReplayError("repos/%s: %s" % (unit.repo, err)) from None
    _, why = run_git(repo_dir, "worktree", "add", "--detach", wt, unit.test_commit, timeout=WORKTREE_TIMEOUT)
    if why is not None:
        raise ReplayError("repos/%s: the worktree of test_commit %s could not be added: %s" % (unit.repo, short_sha(unit.test_commit), why))
    try:
        workdir = wt
        if cwd != ".":
            workdir = os.path.join(wt, cwd)
            if not os.path.isdir(workdir):
                raise ReplayError("unit %s: its run's cwd %s is no directory of test_commit %s" % (clip_text(unit.id), cwd, short_sha(unit.test_commit)))
        line = gotext.shell_join(run["argv"])
        until = deadline_at
        if unit_timeout > 0:
            d = time.time() + unit_timeout
            if d < until:
                until = d
        timeout = unit_timeout if unit_timeout > 0 else until - time.time()

        def run_in(command: str, wd: str, logs: str, limit: float) -> runner.CommandResult:
            try:
                return runner.run_command(runner.CommandOptions(
                    capsule_dir=capsule_dir, repo=unit.repo, command=command, deadline=until, timeout=limit,
                    log_dir=logs, run=seq, workdir=wd, isolation=clone_isolation, env=env, container=co,
                ))
            except runner.ExecutorError as err:
                raise ReplayError(str(err)) from None

        res = run_in(line, workdir, log_dir, timeout)
        verdict, hit = replay_verdict(res.exit, res.timed_out, run_output(capsule_dir, res.log))
        setup: Optional[Dict[str, Any]] = None
        lines = base_setup_lines(wt) if verdict == "not_runnable" else []
        if hit is not None and lines:
            because = hit
            setup = {"because": because, "first_log": keep_first_run(log_dir, res.log), "commands": []}
            failed = ""
            for k, command in enumerate(lines, 1):
                sres = run_in(command, wt, os.path.join(log_dir, "setup", str(k)), until - time.time())
                cmd_out: Dict[str, Any] = {"command": command, "exit_code": sres.exit, "timed_out": sres.timed_out}
                if sres.log:
                    cmd_out["log"] = sres.log
                setup["commands"].append(cmd_out)
                if sres.not_started:
                    res, verdict, hit = sres, "not_judged", None
                    break
                if sres.timed_out:
                    failed = "`%s` was killed at the time limit" % command
                elif sres.exit != 0:
                    failed = "`%s` exited %d" % (command, sres.exit)
                if failed:
                    hit = {"toolchain": because["toolchain"], "rule": BASE_SETUP_RULE, "line": failed}
                    break
            else:
                res = run_in(line, workdir, log_dir, until - time.time())
                verdict, hit = replay_verdict(res.exit, res.timed_out, run_output(capsule_dir, res.log))
    finally:
        run_git(repo_dir, "worktree", "remove", "--force", wt, timeout=WORKTREE_TIMEOUT)
        shutil.rmtree(wt, ignore_errors=True)
        run_git(repo_dir, "worktree", "prune")
    out: Dict[str, Any] = {
        "exit_code": res.exit,
        "red": verdict == "red",
        "timed_out": res.timed_out,
    }
    if verdict == "not_runnable" and hit is not None:
        out["not_runnable"] = hit
    if setup is not None:
        out["setup"] = setup
    if res.not_started:
        out["not_started"] = True
    out["duration_ms"] = res.duration_ms
    for key, v in (("log", res.log), ("image", res.image), ("container", res.container)):
        if v:
            out[key] = v
    if res.host:
        out["host"] = True
    out["worktree"] = wt_rel
    out["cwd"] = cwd
    out["line"] = line
    return out


# ---------------------------------------------------------------------------
# the kinds


class Capsule:
    """The capsule a kind reads: its root, its repos/ and the claim's repos
    present there."""

    def __init__(self, capsule_dir: str) -> None:
        d = os.path.abspath(capsule_dir)
        if not os.path.exists(os.path.join(d, CAPSULE_JSON)):
            raise ValueError("%s is not a capsule root (%s: stat %s: no such file or directory)" % (d, CAPSULE_JSON, os.path.join(d, CAPSULE_JSON)))
        self.dir = d
        self.repos_dir = os.path.join(d, "repos")
        self.repos = [n for n in claim_repos(d) if os.path.isdir(os.path.join(self.repos_dir, n))]

    def path(self, given: str, default: str) -> str:
        return given if given else os.path.join(self.dir, default)

    def heads(self) -> Dict[str, str]:
        out = {}
        for name in self.repos:
            sha = repo_head(os.path.join(self.repos_dir, name))
            if sha:
                out[name] = sha
        return out

    def units(self, path: str, required: bool) -> Tuple[Optional[List[Dict[str, Any]]], str]:
        if not os.path.exists(path):
            if not required:
                return None, ""
            raise UnitsError("--units: stat %s: no such file or directory" % path)
        try:
            decls = read_units(path)
        except UnitsError as err:
            raise UnitsError("--units: %s" % err) from None
        return decls, rel_to(self.dir, path)


def _duration(s: str) -> int:
    try:
        return duration.parse(s)
    except ValueError as err:
        raise argparse.ArgumentTypeError(str(err)) from None


def _parser(kind: str) -> Parser:
    p = Parser(COMMAND + " " + kind)
    p.add_argument("--capsule", default=".", help="the capsule root (capsule.json, repos/, artifacts/)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    p.add_argument("rest", nargs=argparse.REMAINDER, help=argparse.SUPPRESS)
    return p


def _parsed(inv: Invocation, p: Parser, args: List[str]) -> Tuple[Optional[argparse.Namespace], int]:
    ns = p.parse_args(args)
    inv.out, inv.json = ns.out, ns.json
    if ns.rest:
        return None, inv.usage("unexpected arguments [%s]" % " ".join(ns.rest))
    return ns, 0


def plan_conformance(inv: Invocation, args: List[str]) -> int:
    p = _parser(PLAN_CONFORMANCE)
    p.add_argument("--plan", default="", help="build's plan (default: the capsule's " + DEFAULT_PLAN + ")")
    p.add_argument("--units", default="", help="build's units.json, whose test files count as declared (default: the capsule's " + DEFAULT_UNITS + "; absent = none)")
    ns, code = _parsed(inv, p, args)
    if ns is None:
        return code
    try:
        c = Capsule(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    inv.heads = c.heads()
    plan_path = c.path(ns.plan, DEFAULT_PLAN)
    try:
        decls, units_file = c.units(c.path(ns.units, DEFAULT_UNITS), False)
    except UnitsError as err:
        return inv.usage(str(err))
    d: Dict[str, Any] = {"plan": rel_to(c.dir, plan_path)}
    if units_file:
        d["units_file"] = units_file
    pc_empty = {"declared_writes": None, "undeclared": None, "unchanged_obligations": None, "unchanged_writes": None, "released": None}
    inv.details = d
    try:
        raw = read_text(plan_path)
    except OSError as err:
        d["unavailable"] = "build's plan is unreadable: %s" % gotext.os_error("open", err)
        d.update(pc_empty)
        inv.summary = "not judged: " + d["unavailable"]
        return 2
    touched = plan_touched_files(raw)
    authored = unit_test_files(units_of(c.repos_dir, c.repos, decls or []))
    changed, reason = changed_files(c.repos_dir)
    pc = fold_plan_conformance(touched, authored, changed, reason)
    if pc["unavailable"]:
        d["unavailable"] = pc["unavailable"]
    for k in ("declared_writes", "undeclared", "unchanged_obligations", "unchanged_writes", "released"):
        d[k] = pc[k] or []
    if pc["unavailable"]:
        inv.summary = "not judged: " + pc["unavailable"]
        return 2
    if not conformant(pc):
        inv.summary = (
            "the plan and the diff differ: %d path(s) changed but undeclared, %d declared coupled obligation(s) left unchanged (%d declared write(s) left unchanged, %d lapsed)"
            % (len(d["undeclared"]), len(d["unchanged_obligations"]), len(d["unchanged_writes"]), len(d["released"]))
        )
        return 1
    inv.summary = "the plan declares every changed path and every declared coupled obligation changed (%d declared write(s), %d left unchanged, %d lapsed)" % (
        len(d["declared_writes"]), len(d["unchanged_writes"]), len(d["released"]))
    return 0


def build(inv: Invocation, args: List[str], environ: Mapping[str, str], runtime: Optional[container.ContainerRuntime]) -> int:
    p = _parser(BUILD)
    p.add_argument("--units", default="", help="build's units.json (default: the capsule's " + DEFAULT_UNITS + ")")
    p.add_argument("--replay", action="store_true", help="replay each unit's run on the tree of its test commit")
    p.add_argument("--isolation", default="", choices=["container", "host"], help="where a replay runs (default: the container when the repo's HEAD carries .polyforge/Dockerfile.verify, else the host)")
    p.add_argument("--unit-timeout", type=_duration, default=DEFAULT_UNIT_TIMEOUT, help="one replay's time limit (under the whole deadline; default 10m)")
    p.add_argument("--deadline", type=_duration, default=0, help="the whole run's deadline (default: the step's tool window less two minutes)")
    p.add_argument("--log-dir", default="", help="where the replays' logs go (default: logs/ beside --out, else the capsule's runtime-state/facts/<n>)")
    ns, code = _parsed(inv, p, args)
    if ns is None:
        return code
    try:
        c = Capsule(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    inv.heads = c.heads()
    try:
        decls_opt, units_file = c.units(c.path(ns.units, DEFAULT_UNITS), True)
    except UnitsError as err:
        return inv.usage(str(err))
    decls = decls_opt or []
    units: List[Dict[str, Any]] = []
    d: Dict[str, Any] = {"units_file": units_file, "units": units, "test_weakened": []}
    inv.details = d
    resolved = units_of(c.repos_dir, c.repos, decls)
    repos: Dict[str, Repo] = {}
    problems: List[str] = []
    entries: List[Tuple[Unit, Dict[str, Any]]] = []
    for i, u in enumerate(resolved):
        bu = u.as_dict()
        commits = ""
        if u.why:
            problems.append("unit %s: %s" % (quote(clip_text(u.id)), u.why))
        else:
            if u.repo not in repos:
                repos[u.repo] = Repo(c.repos_dir, u.repo)
            commits = repos[u.repo].check_unit_commits(u.test_commit, u.impl_commit)
            if commits:
                problems.append("unit %s (repos/%s): %s" % (quote(clip_text(u.id)), u.repo, commits))
        if commits:
            bu["commits"] = commits
        run = decls[i]["run"]
        if run is not None:
            bu["run"] = run_json(run)
        units.append(bu)
        entries.append((u, bu))
    weakened, _ = weakened_facts(c.repos_dir, resolved, "")
    d["test_weakened"] = weakened
    if not decls:
        inv.summary = "no unit is reported in %s — a build without a red-then-green unit proves nothing" % units_file
        return 1
    if ns.replay:
        logs = ns.log_dir
        if not logs:
            if ns.out:
                logs = os.path.join(os.path.dirname(ns.out), "logs")
            else:
                try:
                    logs = next_run_dir(os.path.join(c.dir, "runtime-state", "facts"))
                except OSError as err:
                    return inv.usage("no log directory: %s" % gotext.os_error("mkdir", err))
        logs = os.path.abspath(logs)
        try:
            ensure_log_dir(logs)
        except OSError as err:
            return inv.usage("the log directory %s: %s" % (logs, gotext.os_error("mkdir", err)))
        window = ns.deadline if ns.deadline > 0 else deadline.default_deadline_for(environ)
        deadline_at = time.time() + window / duration.SECOND
        co = container.ContainerOptions(runtime=runtime, say=inv.say).with_defaults(environ)
        d["replay"] = {"unit_timeout": duration.format(ns.unit_timeout), "deadline": duration.format(window)}
        # the replay's place in the details: after units_file
        inv.details = d = {"units_file": d["units_file"], "replay": d["replay"], "units": units, "test_weakened": d["test_weakened"]}
        env = dict(environ)
        for i, (u, bu) in enumerate(entries):
            if u.why or bu.get("commits"):
                continue  # nothing sound to replay
            run = decls[i]["run"] or {"repo": "", "cwd": "", "argv": None}
            if not run["argv"]:
                problems.append("unit %s: reports no run (units.json run.argv is empty), so its red run cannot be replayed" % quote(clip_text(u.id)))
                continue
            if run["repo"] != u.repo:
                problems.append("unit %s: its run names repos/%s, its commits are in repos/%s" % (quote(clip_text(u.id)), clip_text(run["repo"]), u.repo))
                continue
            inv.say("unit %s: replaying `%s` at test commit %s (%d/%d)" % (u.id, gotext.shell_join(run["argv"]), short_sha(u.test_commit), i + 1, len(entries)))
            try:
                rr = replay(c.dir, u, run, deadline_at, ns.unit_timeout / duration.SECOND,
                            os.path.join(logs, "replay", str(i + 1)), i + 1, env, co, ns.isolation)
            except (KeyboardInterrupt, runner.Interrupted) as err:
                inv.summary = "interrupted while replaying unit %s (%s): nothing is proven" % (u.id, err or "interrupt")
                return 2
            except ReplayError as err:
                bu["replay_error"] = str(err)
                inv.summary = "unit %s could not be replayed (an infrastructure failure, never the unit's verdict): %s" % (u.id, err)
                return 2
            if rr.get("log"):
                log_abs = rr["log"] if os.path.isabs(rr["log"]) else os.path.join(c.dir, rr["log"])
                tail = secretpat.mask(tail_lines(log_abs, OUTPUT_TAIL_LINES))
                if tail:
                    rr["output_tail"] = tail
                inv.logs.append(rr["log"])
            setup = rr.get("setup")
            if setup is not None:
                inv.logs.extend(x for x in [setup["first_log"]] + [c.get("log", "") for c in setup["commands"]] if x)
            bu["replay"] = rr
            if rr.get("not_started"):
                inv.summary = "unit %s was not replayed: the deadline (%s) had passed" % (u.id, duration.format(window))
                return 2
            if rr["timed_out"]:
                inv.summary = "unit %s's replay was killed at its time limit (%s): not judged" % (u.id, duration.format(ns.unit_timeout))
                return 2
            if not_run(rr["exit_code"]):
                inv.summary = "unit %s's run `%s` could not run at its test commit (exit %d: the command is missing or not executable there): not judged" % (
                    u.id, rr["line"], rr["exit_code"])
                return 2
            hit = rr.get("not_runnable")
            if hit is not None:
                problems.append("unit %s: its run `%s` could not run at test_commit %s (not runnable — %s %s: `%s`): no red evidence"
                                % (quote(clip_text(u.id)), rr["line"], short_sha(u.test_commit), hit["toolchain"], hit["rule"], clip_text(hit["line"])))
                continue
            if not rr["red"]:
                problems.append("unit %s: its run `%s` exited 0 on the tree of test_commit %s — the test passes before its implementation (never red, or weakened since)"
                                % (quote(clip_text(u.id)), rr["line"], short_sha(u.test_commit)))
    if problems:
        inv.summary = "%d of %d unit(s) fail the test-first check: %s" % (len(problems), len(units), "; ".join(clip_list(problems, 3)))
        return 1
    how = "resolve with test-first commits"
    if ns.replay:
        how = "resolve with test-first commits and replay red at their test commits"
    inv.summary = "%d unit(s) %s (%d test_weakened hunk(s) to read)" % (len(units), how, len(d["test_weakened"]))
    return 0


def carries(inv: Invocation, args: List[str]) -> int:
    p = _parser(CARRIES)
    p.add_argument("--units", default="", help="build's units.json, whose runs are the bound commands (default: the capsule's " + DEFAULT_UNITS + "; absent = none)")
    ns, code = _parsed(inv, p, args)
    if ns is None:
        return code
    try:
        c = Capsule(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    inv.heads = c.heads()
    try:
        decls_opt, units_file = c.units(c.path(ns.units, DEFAULT_UNITS), False)
    except UnitsError as err:
        return inv.usage(str(err))
    decls = decls_opt or []
    d: Dict[str, Any] = {}
    if units_file:
        d["units_file"] = units_file
    repos_out: List[Dict[str, Any]] = []
    all_problems: List[str] = []
    d["repos"] = repos_out
    d["problems"] = all_problems
    inv.details = d
    unit_repos = {u.repo for u in units_of(c.repos_dir, c.repos, decls) if not u.why}
    bindings = [Binding(decl["run"]["repo"], gotext.shell_join(decl["run"]["argv"]))
                for decl in decls if decl["run"] is not None and decl["run"]["argv"]]
    for name in c.repos:
        problems = Repo(c.repos_dir, name).check_e2e_carries(name in unit_repos, bindings)
        repos_out.append({"name": name, "unit_repo": name in unit_repos, "problems": problems})
        all_problems += problems
    if all_problems:
        first = all_problems[0].split(" — ", 1)[0]
        inv.summary = "%d carries problem(s): %s" % (len(all_problems), first)
        return 1
    inv.summary = "every e2e suite this build added or changed declares carries: that covers it (%d task repo(s))" % len(c.repos)
    return 0


PLAN_LINE = gd.Struct({"repo": gd.STR, "line": gd.STR})


def micro_bounds(inv: Invocation, args: List[str]) -> int:
    p = _parser(MICRO_BOUNDS)
    p.add_argument("--plan", default="", help="the fix step's evidence plan, a JSON array [{repo, line}] (required)")
    ns, code = _parsed(inv, p, args)
    if ns is None:
        return code
    if not ns.plan:
        return inv.usage("--plan <file> is required: the evidence plan, a JSON array [{repo, line}]")
    try:
        c = Capsule(ns.capsule)
    except ValueError as err:
        return inv.usage(str(err))
    inv.heads = c.heads()
    try:
        raw = read_text(ns.plan)
    except OSError as err:
        return inv.usage("--plan: %s" % gotext.os_error("open", err))
    try:
        plan, derr = gd.decode(loads(raw), gd.List(PLAN_LINE))
    except ValueError as err:
        plan, derr = None, str(err)
    if derr is not None:
        return inv.usage("--plan %s is not a JSON array of {repo, line}: %s" % (ns.plan, derr))
    evidence = []
    for pl in plan or []:
        repo, line = trim_space(pl["repo"]), trim_space(pl["line"])
        if repo and line:
            evidence.append({"repo": repo, "command": line})
    mb = micro_bounds_of(c.repos_dir, evidence)
    d: Dict[str, Any] = {"plan": rel_to(c.dir, ns.plan)}
    d.update(mb)
    inv.details = d
    if not mb["in_bounds"]:
        inv.summary = mb["reason"]
        return 1
    inv.summary = "in bounds: " + ", ".join(
        "repos/%s (%d path(s), %d command(s))" % (r["name"], len(r["changed_paths"] or []), len(r["commands"])) for r in mb["repos"])
    return 0


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None,
         runtime: Optional[container.ContainerRuntime] = None) -> int:
    """The script: argv without the program name; environ and runtime are a
    test's (default: the process's, the docker client)."""
    args = list(sys.argv[1:] if argv is None else argv)
    env = os.environ if environ is None else environ
    if args and args[0] in ("-h", "--help"):
        print(__doc__)
        return 0
    inv = Invocation(COMMAND, args, env)
    if not args or args[0].startswith("-"):
        inv.usage("a kind is required as the first argument: " + " | ".join(KINDS))
        return 2
    kind, rest = args[0], args[1:]
    if kind not in KINDS:
        inv.usage("%s is no kind of fact (the kinds: %s)" % (quote(kind), " | ".join(KINDS)))
        return 2
    try:
        if kind == PLAN_CONFORMANCE:
            code = plan_conformance(inv, rest)
        elif kind == BUILD:
            code = build(inv, rest, env, runtime)
        elif kind == CARRIES:
            code = carries(inv, rest)
        else:
            code = micro_bounds(inv, rest)
    except SystemExit as e:
        # argparse: --help (0) or a usage error (2) — no result file
        return int(e.code or 0) if not isinstance(e.code, str) else 2
    except (KeyboardInterrupt, runner.Interrupted) as err:
        inv.summary = "interrupted (%s): nothing is proven" % (err or "interrupt")
        code = 2
    return inv.finish(code)


if __name__ == "__main__":
    runner.raise_on_termination()
    sys.exit(main())
