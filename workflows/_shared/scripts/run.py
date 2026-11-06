"""run.py — command lines run in a task repo, on the host or in the repo's
verification container, the step judged by their exit codes (the successor
of the Go step command pf-run, which ran in the container only).

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/run.py" --isolation container|host --repo <name> \\
        [--capsule DIR] [--deadline D] [--log-dir DIR] [--json] [--out PATH] -- '<line>'
    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/run.py" --isolation container|host --plan-from <file> \\
        [--capsule DIR] [--deadline D] [--log-dir DIR] [--json] [--out PATH]

``--isolation`` is required: where the lines run is the step's to say,
never the script's guess. ``container``: each line runs in its repo's
verification container (the image of its .polyforge/Dockerfile.verify at
HEAD, the clone mounted at its own path, the named cache volume, the
allowlisted environment, no credential), one container per line, removed
when the line ends; a repo without the Dockerfile is not judged (exit 2),
never run on the host instead. ``host``: each line runs on the host under
the step's command environment. Either way a line runs as ``bash -c`` from
the clone's root, in its own process group with a private TMPDIR, killed
whole at the deadline (pfstep.runner).

One line (``--repo R -- '<line>'``: exactly one argument, the line) or a
plan (``--plan-from <file>``: a JSON array ``[{"repo": "<name>", "line":
"<line>"}]``, every entry run in its repo, in the file's order — the fix
step's evidence plan).

Its logs: <log-dir>/<repo>/<seq>/command.log (and, in a container,
image-build.log), the log dir being --log-dir, else logs/ beside --out,
else the capsule's next runtime-state/run/<n>; a .gitignore keeps it out of
the capsule chain. The result's details::

    { "plan": "<file>", "deadline": "<D>",
      "runs": [{repo, line, exit_code, duration_ms, timed_out, not_started, log, output_tail, image, container}],
      "not_run": [{repo, line}] }

Exit 0: every line ran and exited 0. Exit 1: a line exited non-zero, was
killed at the deadline or never started because the deadline had passed
(the later lines do not run and are listed under not_run), or the plan
names no line — a plan that ran nothing passes nothing. Exit 2: a line
could not be run as asked (no container engine, an image or container not
had, no Dockerfile in a container run, no such repo — the skill retries
after a pause), the plan is unreadable, the invocation is unusable, or the
run was interrupted.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional  # noqa: E402

from pfstep import duration, runner, secretpat  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402
from pfstep.container import ContainerOptions  # noqa: E402
from pfstep.runner import CommandOptions  # noqa: E402

NAME = "run.py"

# How much of a line's log the result carries.
OUTPUT_TAIL_LINES = 40

# A log directory's .gitignore: logs are local, never capsule chain content.
LOGS_GITIGNORE = "# pf step command logs are local, never capsule chain content\n*\n"

ISOLATIONS = (runner.ISOLATION_CONTAINER, runner.ISOLATION_HOST)


def ensure_log_dir(d: str) -> None:
    """d created with the .gitignore that keeps it out of the capsule chain."""
    os.makedirs(d, exist_ok=True)
    gi = os.path.join(d, ".gitignore")
    try:
        with open(gi, encoding="utf-8") as f:
            if f.read() == LOGS_GITIGNORE:
                return
    except OSError:
        pass
    with open(gi, "w", encoding="utf-8") as f:
        f.write(LOGS_GITIGNORE)


def next_run_dir(root: str) -> str:
    """<root>/<n> allocated (n = one past the highest existing run)."""
    ensure_log_dir(root)
    n = 0
    for name in os.listdir(root):
        if name.isdigit() and name.isascii() and int(name) > n:
            n = int(name)
    while True:
        n += 1
        d = os.path.join(root, str(n))
        try:
            os.mkdir(d)
            return d
        except FileExistsError:
            continue


def tail_lines(path: str, n: int) -> str:
    """The last n lines of the file at path ("" when it cannot be read)."""
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError:
        return ""
    lines = raw.decode("utf-8", "replace").rstrip("\n").split("\n")
    return "\n".join(lines[-n:])


def plan_line(repo: str, line: str) -> Dict[str, str]:
    return {"repo": repo, "line": line}


def run_entry(repo: str, line: str, res: runner.CommandResult) -> Dict[str, Any]:
    e: Dict[str, Any] = {"repo": repo, "line": line, "exit_code": res.exit, "duration_ms": res.duration_ms,
                         "timed_out": res.timed_out}
    if res.not_started:
        e["not_started"] = True
    return e


def repo_list(plan: List[Dict[str, str]]) -> str:
    """The plan's repos, each once, in plan order."""
    names: List[str] = []
    for p in plan:
        if "repos/" + p["repo"] not in names:
            names.append("repos/" + p["repo"])
    return ", ".join(names)


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Run command lines in task repos, on the host or in their verification containers.")
    ap.add_argument("--isolation", default="", help="where the lines run: container (the repo's verification container) or host (required)")
    ap.add_argument("--repo", default="", help="the task repo (repos/<name>) the one line runs in")
    ap.add_argument("--plan-from", default="", help="a JSON array [{repo, line}] of lines to run, in order")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--log-dir", default="", help="where the logs go (default: logs/ beside --out, else the capsule's next runtime-state/run/<n>)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    ap.add_argument("line", nargs="*", help="the one line, after --")
    return ap


def _go_plus_v(p: Any) -> str:
    """Go's %+v of a plan entry ({Repo:… Line:…})."""
    repo = p.get("repo", "") if isinstance(p, dict) else ""
    line = p.get("line", "") if isinstance(p, dict) else ""
    return "{Repo:%s Line:%s}" % (repo if isinstance(repo, str) else "", line if isinstance(line, str) else "")


def run(inv: Invocation) -> int:
    a = parser().parse_args(inv.argv)
    inv.out, inv.json = a.out, a.json
    line = ""
    if (a.plan_from == "") == (a.repo == ""):
        return inv.usage("exactly one of --plan-from <file> and --repo <name> -- '<line>' is given")
    if a.plan_from != "" and a.line:
        return inv.usage("--plan-from takes no line argument (got %s)" % go_list(a.line))
    if a.repo != "" and len(a.line) != 1:
        return inv.usage("--repo %s takes exactly one argument after --, the line (quote it as one word; got %d: %s)" % (
            a.repo, len(a.line), go_list(a.line)))
    if a.repo != "":
        line = a.line[0]
        if line.strip() == "":
            return inv.usage("the line is empty")
    if a.isolation not in ISOLATIONS:
        return inv.usage("--isolation %s: want container or host — where the lines run is the step's to say" % (
            a.isolation or "(none)"))
    capsule = os.path.abspath(a.capsule)
    plan: List[Dict[str, str]]
    if a.plan_from != "":
        try:
            with open(a.plan_from, "rb") as f:
                raw = f.read()
        except OSError as err:
            why = (err.strerror or str(err))
            return inv.usage("--plan-from: open %s: %s" % (a.plan_from, why[:1].lower() + why[1:]))
        try:
            doc = json.loads(raw.decode("utf-8", "replace"))
            if not isinstance(doc, list) or not all(isinstance(p, dict) for p in doc):
                raise ValueError("not an array of objects")
            for p in doc:
                for k in ("repo", "line"):
                    if p.get(k) is not None and not isinstance(p.get(k), str):
                        raise ValueError("%s is not a string" % k)
        except ValueError as err:
            return inv.usage("--plan-from %s is not a JSON array of {repo, line}: %s" % (a.plan_from, err))
        plan = []
        for i, p in enumerate(doc):
            repo, ln = p.get("repo") or "", p.get("line") or ""
            if repo.strip() == "" or ln.strip() == "":
                return inv.usage("--plan-from %s: entry %d needs a repo and a line (got %s)" % (a.plan_from, i + 1, _go_plus_v(p)))
            plan.append(plan_line(repo, ln))
    else:
        plan = [plan_line(a.repo, line)]
    logs = a.log_dir
    try:
        if logs == "":
            logs = os.path.join(os.path.dirname(a.out), "logs") if a.out else next_run_dir(os.path.join(capsule, "runtime-state", "run"))
        logs = os.path.abspath(logs)
        ensure_log_dir(logs)
    except OSError as err:
        return inv.usage("the log directory %s: %s" % (logs, err))
    window = inv.deadline(a.deadline)
    deadline = time.time() + duration.seconds(window)
    # one set of container options for the whole run: one boot nonce, the
    # owner's sweep and the left-behind messages said on stderr
    co = ContainerOptions(say=inv.say).with_defaults(inv.env)
    details: Dict[str, Any] = {}
    if a.plan_from:
        details["plan"] = a.plan_from
    details["deadline"] = duration.format(window)
    runs: List[Dict[str, Any]] = []
    details["runs"] = runs
    inv.details = details
    env = dict(inv.env)
    if not plan:
        inv.summary = "the plan ran nothing: %s names no line — a plan that ran nothing passes nothing" % a.plan_from
        return 1
    where = "the verification container of repos/%s" if a.isolation == runner.ISOLATION_CONTAINER else "repos/%s on the host"
    for i, p in enumerate(plan):
        seq = i + 1
        inv.say("repos/%s: `%s` (%d/%d)" % (p["repo"], p["line"], seq, len(plan)))
        opts = CommandOptions(capsule_dir=capsule, repo=p["repo"], command=p["line"], deadline=deadline,
                              timeout=duration.seconds(window), log_dir=os.path.join(logs, p["repo"], str(seq)),
                              run=seq, isolation=a.isolation, env=env, container=co)
        try:
            res = runner.run_command(opts)
        except (KeyboardInterrupt, runner.Interrupted):
            details["not_run"] = plan[i:]
            inv.summary = "interrupted before repos/%s: `%s` ended (context canceled): nothing is proven" % (p["repo"], p["line"])
            return 2
        except runner.ExecutorError as err:
            details["not_run"] = plan[i:]
            inv.summary = "%s could not run `%s` (an infrastructure failure, never the line's verdict): %s" % (
                where % p["repo"], p["line"], err)
            return 2
        e = run_entry(p["repo"], p["line"], res)
        if res.log:
            # the log is capsule-relative inside the capsule: the result's convention
            e["log"] = res.log
            path = res.log if os.path.isabs(res.log) else os.path.join(capsule, res.log)
            tail = secretpat.mask(tail_lines(path, OUTPUT_TAIL_LINES))
            if tail:
                e["output_tail"] = tail
            inv.logs.append(res.log)
        if res.image:
            e["image"] = res.image
        if res.container:
            e["container"] = res.container
        if res.head:
            inv.heads[p["repo"]] = res.head
        runs.append(e)
        if res.exit != 0 or res.timed_out:
            what = "exited %d" % res.exit
            if res.not_started:
                what = "never started: the deadline (%s) had passed" % duration.format(window)
            elif res.timed_out:
                what = "was killed at the deadline (%s)" % duration.format(window)
            inv.summary = "repos/%s: `%s` %s" % (p["repo"], p["line"], what)
            if e.get("log"):
                inv.summary += " (log " + e["log"] + ")"
            left = plan[i + 1:]
            if left:
                details["not_run"] = left
                inv.summary += "; the %d later line(s) did not run" % len(left)
            return 1
    inv.summary = "%d line(s) ran and exited 0 in %s" % (len(plan), repo_list(plan))
    return 0


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None) -> int:
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    runner.raise_on_termination()
    try:
        code = run(inv)
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "interrupted (context canceled): nothing is proven"
        code = 2
    except Exception as err:  # a bug, never a verdict: said, and not judged
        traceback.print_exc()
        inv.summary = "the lines could not be run (an infrastructure failure, never the line's verdict): %s" % err
        code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
