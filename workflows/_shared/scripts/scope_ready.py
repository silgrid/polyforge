"""scope_ready.py — whether the verification-scope engine is here and each
named repository is scoped-ready: the check `pf doctor`'s former
`scoped-gates readiness` row made, now the workflow's own (the engine,
verify-scope, moved into the workflow beside this script:).

Usage:

  scope_ready.py [--repo DIR]... [--engine PATH] [--json] [--out PATH]

--repo names a repository's clone (repeatable); without one, the task
repos of the capsule at $PF_CAPSULE (its repos/*). --engine names the
engine; default: verify-scope beside this script ($PF_SHARED_DIR/scripts/
in a step, <data-dir>/release/current/workflows/_shared/scripts/ for the
installed release), its dependency-audit helper audit-deps beside it.

Reads only — the engine and the confs are never executed. Per repository
(the engine is conf-first: it reports a repo conf's commands for every
stack):

- a `stack=go|npm|python|rust` pin, or a single supported manifest
  (go.mod, package.json, pyproject.toml/requirements.txt, Cargo.toml): the
  analyzer is effective — ready, unless the carries layer would force
  every light verification to the full bar: an npm, python or rust repo
  with an undeclared suite (no `carries:` in its opening comment block), an
  `e2e_map=` naming no discovered suite, an empty or malformed carries key
  (`e2e_runner=`, `suite_glob=`, `e2e_map=`, `smoke_suite=`), or a
  `smoke_suite=` naming no discovered suite;
- no analyzer — `stack=none`, or no supported manifest — with a conf
  declaring at least one command (`fast_gate=`, `full_command=`,
  `check=`): the repo conf is effective, on the full tier only — ready;
- no analyzer and no conf command, a `stack=none` pin without a command,
  or two or more supported manifests without a pin: not ready;
- a conf that cannot be read (not a file, not UTF-8): not ready;
- any of these without the engine: not ready.

A repository that is not ready gets no narrowed selection: its light
verification runs the full bar (docs/contributing.md『Verification
tiers』). Exit 0: the engine is here and every repository is ready; 1: the
engine is missing or a repository is not ready (each named, with why);
2: no repository is named, or one named is not a git clone (never faked
green). The result (schema pf-step-result/1, command scope_ready.py) goes to
--out and, with --json, stdout; its details:
  {engine, engine_present, repos: [{name, dir, ready, analyzer, conf_only,
   suites, smoke, problems}]}
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import re  # noqa: E402
from typing import Any, Callable, Dict, List, Optional, Tuple  # noqa: E402

from pfstep import goglob, gotext, stepenv  # noqa: E402
from pfstep.invoke import Invocation, Parser  # noqa: E402

COMMAND = "scope_ready.py"
CONF = ".polyforge/verify-scope.conf"
ENGINE = "verify-scope"
AUDIT_HELPER = "audit-deps"
POINTER = "docs/contributing.md『Verification tiers』"
NO_ANALYZER = (
    "the verification step derives the bar ad hoc on every run, there is no scoped tier (a light verification falls"
    " back to full), and a future runner dispatch fails closed; declare the bar in " + CONF +
    " (fast_gate=, full_command= or check= lines; stack=none pins a conf-only repo)"
)

_S = r"[\t\n\f\r ]"  # Go RE2's \s
_CHECK_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*", re.ASCII)
_RESERVED_NAME = re.compile(r"(?:fast|full|scoped)-[0-9]", re.ASCII)
_JS_IMPORT = re.compile(r"(?:from|import|require)" + _S + r"*\(?" + _S + r"*['\"](\.\.?/[^'\"]+)['\"]", re.ASCII)
_RUST_MOD = re.compile(r"^" + _S + r"*(?:pub(?:\([^)]*\))?" + _S + r"+)?mod" + _S + r"+([A-Za-z_][A-Za-z0-9_]*)" + _S + r"*;", re.ASCII)
_PY_IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_.]*", re.ASCII)
_PY_FROM = re.compile(r"^" + _S + r"*from" + _S + r"+([.A-Za-z0-9_]+)" + _S + r"+import" + _S + r"+(.*)\Z", re.ASCII)
_PY_IMPORT = re.compile(r"^" + _S + r"*import" + _S + r"+(.*)\Z", re.ASCII)


class ConfFacts:
    """What a conf declares: the first valid stack pin, the commands, the
    carries layer's keys and the lines the engine rejects."""

    def __init__(self) -> None:
        self.pin = ""
        self.commands = 0
        self.runner = ""
        self.suite_glob: List[str] = []
        self.maps: List[str] = []
        self.excludes: List[str] = []
        self.smoke: List[str] = []
        self.rejected: List[str] = []


def _reportable(cmd: str) -> bool:
    return cmd != "" and not cmd.startswith("$(") and not cmd.startswith("`")


def smoke_path(v: str) -> bool:
    """A smoke_suite= value the engine accepts: one repository-relative path."""
    return (v != "" and not any(c in v for c in " \t\r\n\v\f") and not v.startswith("/")
            and not v.startswith("./") and not v.startswith("../") and "/../" not in v)


def read_conf(text: str) -> ConfFacts:
    """A conf's facts, read as the engine reads it."""
    c = ConfFacts()
    lines = text.split("\n")
    if lines and lines[-1] == "":
        lines.pop()
    for raw in lines:
        if raw.endswith("\r"):
            raw = raw[:-1]
        line = raw.lstrip(" \t\r\v\f")
        if not line or line.startswith("#"):
            continue
        key, sep, val = line.partition("=")
        if not sep:
            continue
        if key == "stack":
            v = gotext.trim_space(val)
            if not c.pin and v in ("go", "npm", "python", "rust", "none"):
                c.pin = v
        elif key in ("fast_gate", "full_command"):
            if _reportable(val):
                c.commands += 1
        elif key == "check":
            parts = val.split("|", 2)
            if (len(parts) == 3 and _CHECK_NAME.fullmatch(parts[0]) and not _RESERVED_NAME.match(parts[0])
                    and parts[1] in ("fast", "full") and _reportable(parts[2])):
                c.commands += 1
        elif key == "exclude":
            if val:
                c.excludes.append(val)
        elif key == "e2e_runner":
            v = val.rstrip(" \t\r\v\f")
            if not _reportable(v) or c.runner:
                c.rejected.append(line)
                continue
            c.runner = v
        elif key == "suite_glob":
            v = val.rstrip(" \t\r\v\f")
            if not v:
                c.rejected.append(line)
                continue
            c.suite_glob.append(v)
        elif key == "e2e_map":
            v = val.rstrip(" \t\r\v\f")
            g, sep2, suite = v.partition("=")
            if not sep2 or not g or not suite or "=" in suite:
                c.rejected.append(line)
                continue
            c.maps.append(v)
        elif key == "smoke_suite":
            v = val.rstrip(" \t\r\v\f")
            if not smoke_path(v):
                c.rejected.append(line)
                continue
            if v not in c.smoke:
                c.smoke.append(v)
    return c


def manifests(clone: str) -> Tuple[List[str], List[str]]:
    """The supported manifests a clone holds and the analyzers they select."""
    names: List[str] = []
    stacks: List[str] = []
    for files, label, stack in ((("go.mod",), "go.mod", "go"), (("package.json",), "package.json", "npm"),
                                (("pyproject.toml", "requirements.txt"), "pyproject.toml/requirements.txt", "python"),
                                (("Cargo.toml",), "Cargo.toml", "rust")):
        if any(os.path.exists(os.path.join(clone, f)) for f in files):
            names.append(label)
            stacks.append(stack)
    return names, stacks


def _walk_files(root: str) -> List[str]:
    out: List[str] = []
    for d, dirs, files in os.walk(root):
        dirs[:] = sorted(x for x in dirs if x not in (".git", "node_modules"))
        for f in sorted(files):
            out.append(os.path.relpath(os.path.join(d, f), root).replace(os.sep, "/"))
    return out


def header_status(path: str) -> str:
    """A suite's opening comment block: declared, malformed or undeclared."""
    try:
        with open(path, "rb") as f:
            text = f.read().decode("utf-8", "replace")
    except OSError:
        return "undeclared"
    globs, bad = 0, False
    for line in text.split("\n"):
        t = gotext.trim_space(line)
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
            if not gotext.trim_space(v):
                bad = True
            globs += len([x for x in re.split(r"[, \t\r]", v) if x])
            continue
        if not rest or not (rest[0] in "_-" or "0" <= rest[0] <= "9" or "A" <= rest[0] <= "Z" or "a" <= rest[0] <= "z"):
            bad = True
    if bad:
        return "malformed"
    return "declared" if globs > 0 else "undeclared"


def _imports(clone: str, analyzer: str, rel: str, exists: Callable[[str], bool]) -> List[str]:
    try:
        with open(os.path.join(clone, rel), "rb") as f:
            raw = f.read().decode("utf-8", "replace")
    except OSError:
        return []
    d = gotext.path_dir(rel)
    out: List[str] = []
    ext = gotext.path_ext(rel)
    if analyzer == "npm":
        if ext not in (".mjs", ".js", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".jsx"):
            return []
        for m in _JS_IMPORT.finditer(raw):
            t = gotext.path_clean(d + "/" + m.group(1))
            if t.startswith("../") or t == "..":
                continue
            for c in (t, t + ".mjs", t + ".js", t + ".cjs", t + ".ts", t + ".tsx", t + ".mts", t + ".jsx",
                      t + "/index.mjs", t + "/index.js", t + "/index.ts", t + "/index.tsx"):
                if exists(c):
                    out.append(c)
                    break
    elif analyzer == "rust":
        if ext != ".rs":
            return []
        child = d
        b = gotext.path_base(rel)
        if b not in ("lib.rs", "main.rs", "mod.rs") and not (d == "tests" and b.endswith(".rs")):
            child = rel[:-3]
        for line in raw.split("\n"):
            mr = _RUST_MOD.search(line)
            if mr:
                for c in (gotext.path_clean(child + "/" + mr.group(1) + ".rs"), gotext.path_clean(child + "/" + mr.group(1) + "/mod.rs")):
                    if exists(c):
                        out.append(c)
                        break
    elif analyzer == "python":
        if ext != ".py":
            return []

        def res(p: str) -> str:
            if exists(p + ".py"):
                return p + ".py"
            if exists(p + "/__init__.py"):
                return p + "/__init__.py"
            return ""

        def absimp(mod: str) -> None:
            p = mod.replace(".", "/")
            for c in (p, "src/" + p, gotext.path_clean(d + "/" + p)):
                r = res(c)
                if r:
                    out.append(r)
                    return

        for line in raw.split("\n"):
            i = line.find("#")
            if i >= 0:
                line = line[:i]
            mi = _PY_IMPORT.search(line)
            if mi:
                for part in mi.group(1).split(","):
                    fs = gotext.fields(part)
                    if fs and _PY_IDENT.fullmatch(fs[0]):
                        absimp(fs[0])
                continue
            mf = _PY_FROM.search(line)
            if mf is None:
                continue
            base = mf.group(1)
            names = []
            for part in mf.group(2).replace("(", " ").replace(")", " ").replace("\\", " ").split(","):
                fs = gotext.fields(part)
                if fs:
                    names.append(fs[0])
            if not base.startswith("."):
                absimp(base)
                for n in names:
                    absimp(base + "." + n)
                continue
            dots = len(base) - len(base.lstrip("."))
            pdir = d
            for _ in range(1, dots):
                pdir = gotext.path_dir(pdir)
            mod = base[dots:].replace(".", "/")
            if mod:
                r = res(gotext.path_clean(pdir + "/" + mod))
                if r:
                    out.append(r)
                for n in names:
                    r = res(gotext.path_clean(pdir + "/" + mod + "/" + n))
                    if r:
                        out.append(r)
                continue
            for n in names:
                r = res(gotext.path_clean(pdir + "/" + n))
                if r:
                    out.append(r)
            r = res(gotext.path_clean(pdir + "/__init__"))
            if r:
                out.append(r)
    return out


def carries(clone: str, analyzer: str, conf: ConfFacts) -> Tuple[List[str], List[str]]:
    """The clone's suites as the engine discovers them, and those whose
    opening block holds no valid carries: (or an e2e_map= naming none)."""

    def exists(rel: str) -> bool:
        return os.path.isfile(os.path.join(clone, rel))

    found: List[str] = []
    if conf.suite_glob:
        for rel in _walk_files(clone):
            if any(goglob.case_match(g, rel) for g in conf.suite_glob):
                found.append(rel)
    else:
        def top(d: str, suffix: str) -> None:
            try:
                ents = sorted(os.listdir(os.path.join(clone, d)))
            except OSError:
                return
            for e in ents:
                if e.endswith(suffix) and not os.path.isdir(os.path.join(clone, d, e)):
                    found.append(d + "/" + e)

        if analyzer == "npm":
            top("e2e", ".mjs")
        elif analyzer == "rust":
            top("tests", ".rs")
        elif analyzer == "python":
            for d in ("e2e", "tests/e2e"):
                if os.path.isdir(os.path.join(clone, d)):
                    for rel in _walk_files(os.path.join(clone, d)):
                        n = gotext.path_base(rel)
                        if n.endswith(".py") and (n.startswith("test_") or n.endswith("_test.py")):
                            found.append(d + "/" + rel)
    runners: Dict[str, bool] = {}
    if conf.runner:
        words = gotext.fields(conf.runner)
        if len(words) >= 3 and words[0] == "npm" and words[1] == "run":
            try:
                with open(os.path.join(clone, "package.json"), "rb") as fh:
                    pkg = json.loads(fh.read().decode("utf-8", "replace"))
                scripts = pkg.get("scripts") if isinstance(pkg, dict) else None
                cmd = scripts.get(words[2]) if isinstance(scripts, dict) else None
                if isinstance(cmd, str):
                    words += gotext.fields(cmd)
            except (OSError, ValueError):
                pass
        for w in words:
            if w.startswith("./"):
                w = w[2:]
            if w and exists(w):
                runners[w] = True

    def excluded(rel: str) -> bool:
        return any(goglob.case_match(g, rel) or goglob.case_match(g, gotext.path_base(rel)) for g in conf.excludes)

    cands: List[str] = []
    is_cand = set()
    for f in found:
        if excluded(f) or f in runners or f in is_cand:
            continue
        is_cand.add(f)
        cands.append(f)
    cands.sort()
    imported = set()
    seen = set(cands)
    queue = list(cands)
    while queue:
        f = queue.pop(0)
        for t in _imports(clone, analyzer, f, exists):
            imported.add(t)
            if t not in seen:
                seen.add(t)
                queue.append(t)
    mapped = set()
    suites: List[str] = []
    undeclared: List[str] = []
    for m in conf.maps:
        suite = m.partition("=")[2]
        mapped.add(suite)
        if suite not in is_cand:
            undeclared.append(suite + " (e2e_map names no discovered suite)")
    for f in cands:
        status = "declared" if f in mapped else header_status(os.path.join(clone, f))
        if status == "undeclared" and f in imported:
            continue  # a helper, not a suite
        suites.append(f)
        if status == "undeclared":
            undeclared.append(f)
        elif status == "malformed":
            undeclared.append(f + " (malformed carries:)")
    return suites, undeclared


def judge(clone: str, engine_present: bool, engine: str) -> Dict[str, Any]:
    """One repository's readiness."""
    name = os.path.basename(os.path.abspath(clone))
    out: Dict[str, Any] = {"name": name, "dir": os.path.abspath(clone), "ready": False, "analyzer": "", "conf_only": False,
                           "suites": [], "smoke": [], "problems": []}
    problems: List[str] = out["problems"]
    conf_path = os.path.join(clone, CONF)
    text = ""
    if os.path.lexists(conf_path):
        try:
            with open(conf_path, "rb") as f:
                text = f.read().decode("utf-8")
        except (OSError, UnicodeDecodeError) as err:
            problems.append("%s cannot be read (%s): the engine's reading of it is unknown, so the verification falls back to full (%s)" % (CONF, err, POINTER))
            return out
    conf = read_conf(text)
    names, stacks = manifests(clone)
    analyzer = conf.pin
    no_analyzer = False
    if conf.pin == "none" and conf.commands == 0:
        problems.append("%s pins stack=none but declares no command — %s (%s)" % (CONF, NO_ANALYZER, POINTER))
        return out
    if conf.pin == "none":
        no_analyzer, analyzer = True, ""
    elif conf.pin:
        pass
    elif len(stacks) > 1:
        problems.append("%s are present without a stack= pin — an unpinned multi-manifest repo; a light verification falls back to full (%s)" % (", ".join(names), POINTER))
        return out
    elif not stacks and conf.commands == 0:
        problems.append("no supported stack manifest (go.mod, package.json, pyproject.toml/requirements.txt or Cargo.toml) and no repo conf command — %s (%s)" % (NO_ANALYZER, POINTER))
        return out
    elif not stacks:
        no_analyzer = True
    else:
        analyzer = stacks[0]
    out["analyzer"] = analyzer
    if not engine_present:
        problems.append("the workflow lacks the engine %s (or its %s beside it) — a light verification falls back to full (%s)" % (engine, AUDIT_HELPER, POINTER))
        return out
    if no_analyzer:
        out["conf_only"] = True
        out["ready"] = True
        return out
    if analyzer in ("npm", "python", "rust"):
        if conf.rejected:
            problems.append("%s carries an empty or malformed carries key (%s) — every report falls back to full (%s)" % (CONF, "; ".join(gotext.clip_list(conf.rejected, 8)), POINTER))
            return out
        suites, undeclared = carries(clone, analyzer, conf)
        out["suites"] = suites
        if undeclared:
            problems.append("%d undeclared suite(s) without a carries: header in the opening comment block — a light verification falls back to full: %s (%s)" % (
                len(undeclared), ", ".join(gotext.clip_list(undeclared, 8)), POINTER))
            return out
        missing = [s for s in conf.smoke if s not in suites]
        if missing:
            problems.append("%s declares smoke_suite= naming no discovered suite (%s) — the full-tier e2e selection falls back to the full e2e command (%s)" % (
                CONF, ", ".join(gotext.clip_list(missing, 8)), POINTER))
            return out
        out["smoke"] = list(conf.smoke)
    out["ready"] = True
    return out


def main(argv: Optional[List[str]] = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    inv = Invocation(COMMAND, args)
    p = Parser(COMMAND, __doc__ or "")
    p.add_argument("--repo", action="append", default=[], help="a repository's clone (repeatable; default: $PF_CAPSULE's repos/*)")
    p.add_argument("--engine", default="", help="the engine (default: verify-scope beside this script)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    try:
        ns = p.parse_args(args)
    except SystemExit as e:
        return int(e.code or 0) if not isinstance(e.code, str) else 2
    inv.out, inv.json = ns.out, ns.json
    engine = os.path.abspath(ns.engine or os.path.join(os.path.dirname(os.path.abspath(__file__)), ENGINE))
    repos = list(ns.repo)
    if not repos:
        capsule = stepenv.read().capsule
        if not capsule:
            return inv.finish(inv.usage("name a repository's clone with --repo (outside a step, PF_CAPSULE names no capsule)"))
        rd = os.path.join(capsule, "repos")
        try:
            repos = [os.path.join(rd, n) for n in sorted(os.listdir(rd)) if not n.startswith(".") and os.path.isdir(os.path.join(rd, n))]
        except OSError as err:
            return inv.finish(inv.usage("the capsule's task repos: %s" % gotext.os_error("open", err)))
        if not repos:
            return inv.finish(inv.usage("the capsule %s has no task repo under repos/" % capsule))
    for r in repos:
        if not os.path.exists(os.path.join(r, ".git")):
            return inv.finish(inv.usage("%s is not a git clone (never faked green: name the repository's clone)" % r))
    present = os.path.isfile(engine) and os.path.isfile(os.path.join(os.path.dirname(engine), AUDIT_HELPER))
    verdicts = [judge(r, present, engine) for r in repos]
    inv.details = {"engine": engine, "engine_present": present, "repos": verdicts}
    not_ready = [v for v in verdicts if not v["ready"]]
    if not_ready:
        inv.summary = "%d of %d repo(s) not scoped-ready: %s" % (
            len(not_ready), len(verdicts), "; ".join("%s: %s" % (v["name"], v["problems"][0]) for v in not_ready))
        return inv.finish(1)
    inv.summary = "%d repo(s) scoped-ready (%s)" % (len(verdicts), ", ".join(
        "%s: %s" % (v["name"], "repo conf, full tier only" if v["conf_only"] else v["analyzer"]) for v in verdicts))
    return inv.finish(0)


if __name__ == "__main__":
    sys.exit(main())
