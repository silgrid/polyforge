"""The step scripts sync.py, merge.py, revert.py and run.py end to end, as a
skill runs them — ``"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<x>.py" …`` — on
fixture repositories, with the fake gh (tests/fakegh.sh: PRs as files, a
merge a real --no-ff merge into the bare origin) and the fake docker
(tests/fakedocker.sh) first on the PATH: the exit codes, the result
file (its JSON Schema), the files beside it (merge's landing record,
revert's hub writes) and what the repositories end up holding."""

import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .schemacheck import errors as schema_errors

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
HAVE_GIT = shutil.which("git") is not None and shutil.which("bash") is not None

with open(os.path.join(SCRIPTS, "schema", "pf-step-result-1.json"), encoding="utf-8") as _f:
    SCHEMA = json.load(_f)


def pin_of(s: str) -> str:
    return hashlib.sha256(s.encode()).hexdigest()


def write(d: str, rel: str, s: str) -> None:
    p = os.path.join(d, rel)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "w", encoding="utf-8") as f:
        f.write(s)


def read(d: str, rel: str) -> str:
    with open(os.path.join(d, rel), encoding="utf-8") as f:
        return f.read()


def install(src: str, dst: str) -> None:
    shutil.copyfile(src, dst)
    os.chmod(dst, os.stat(dst).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


class World:
    """Per repo a bare origin whose main holds an append-only upgrade log and
    its pin (main's integrate.conf declares both) and some code; a capsule
    with each repo cloned on the work item's branch; the fakes on PATH."""

    BRANCH = "polyforge/wi"

    def __init__(self, tc: unittest.TestCase, *repos: str) -> None:
        self.root = tempfile.mkdtemp()
        tc.addCleanup(shutil.rmtree, self.root, True)
        self.home = os.path.join(self.root, "home")
        os.makedirs(self.home)
        self.origins, self.gh, self.bin = (os.path.join(self.root, x) for x in ("origins", "gh", "bin"))
        self.capsule = os.path.join(self.root, "capsule")
        for d in (self.origins, self.gh, self.bin):
            os.makedirs(d)
        install(os.path.join(HERE, "fakegh.sh"), os.path.join(self.bin, "gh"))
        install(os.path.join(HERE, "fakedocker.sh"), os.path.join(self.bin, "docker"))
        self.seeds: Dict[str, str] = {}
        for r in repos:
            origin = os.path.join(self.origins, r + ".git")
            self.git(self.root, "init", "-q", "--bare", "-b", "main", origin)
            seed = os.path.join(self.root, "seed-" + r)
            self.git(self.root, "clone", "-q", origin, seed)
            self.git(seed, "checkout", "-q", "-B", "main")
            up = "# upgrade\n"
            for rel, body in {"README": r + "\n", "docs/upgrade.md": up, "pin.txt": pin_of(up) + "\n",
                              ".polyforge/integrate.conf": "append_only=docs/upgrade.md\npin=docs/upgrade.md pin.txt\n",
                              "pkg/code.go": "package pkg\n\nfunc F() int { return 1 }\n"}.items():
                write(seed, rel, body)
            self.commit(seed, "base")
            self.git(seed, "push", "-q", "origin", "HEAD:refs/heads/main")
            self.seeds[r] = seed
            self.git(self.root, "clone", "-q", origin, self.dir(r))
            self.git(self.dir(r), "checkout", "-q", "-b", self.BRANCH)

    def env(self, **extra: str) -> Dict[str, str]:
        e = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_"))}
        e.update(HOME=self.home, GIT_CONFIG_NOSYSTEM="1", PATH=self.bin + os.pathsep + os.environ.get("PATH", ""),
                 FAKE_GH_STATE=self.gh, FAKE_GH_ORIGINS=self.origins, FAKE_DOCKER_STATE=os.path.join(self.root, "docker"),
                 FAKE_DOCKER_PATH=os.environ.get("PATH", "/usr/bin:/bin"))
        e.update(extra)
        return e

    def git(self, cwd: str, *args: str) -> str:
        e = self.env(GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@invalid", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@invalid")
        out = subprocess.run(["git", *args], cwd=cwd, env=e, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if out.returncode != 0:
            raise AssertionError("git %s: %s" % (args, out.stdout.decode()))
        return out.stdout.decode().strip()

    def commit(self, d: str, msg: str) -> str:
        self.git(d, "add", "-A")
        self.git(d, "commit", "-q", "-m", msg)
        return self.git(d, "rev-parse", "HEAD")

    def dir(self, repo: str) -> str:
        return os.path.join(self.capsule, "repos", repo)

    def head(self, repo: str) -> str:
        return self.git(self.dir(repo), "rev-parse", "HEAD")

    def upgrade(self, d: str, section: str) -> Dict[str, str]:
        up = read(d, "docs/upgrade.md") + "\n## " + section + "\n"
        return {"docs/upgrade.md": up, "pin.txt": pin_of(up) + "\n"}

    def change(self, repo: str, files: Dict[str, str], section: str = "") -> str:
        d = self.dir(repo)
        files = dict(files, **(self.upgrade(d, section) if section else {}))
        for rel, body in files.items():
            write(d, rel, body)
        return self.commit(d, "change")

    def main_moves(self, repo: str, files: Dict[str, str], section: str = "") -> str:
        seed = self.seeds[repo]
        self.git(seed, "fetch", "-q", "origin")
        self.git(seed, "checkout", "-q", "-B", "main", "origin/main")
        files = dict(files, **(self.upgrade(seed, section) if section else {}))
        for rel, body in files.items():
            write(seed, rel, body)
        sha = self.commit(seed, "main moves")
        self.git(seed, "push", "-q", "origin", "HEAD:refs/heads/main")
        return sha

    def publish(self, repo: str) -> Dict[str, str]:
        """Push the branch, open its PR at the fake gh: the publish entry."""
        self.git(self.dir(repo), "push", "-q", "origin", "HEAD:refs/heads/" + self.BRANCH)
        prs = os.path.join(self.gh, repo, "prs")
        n = len(os.listdir(prs)) + 1 if os.path.isdir(prs) else 1
        url = "https://github.com/fake/%s/pull/%d" % (repo, n)
        for k, v in {"head": self.BRANCH, "base": "main", "state": "OPEN", "url": url}.items():
            write(os.path.join(prs, str(n)), k, v + "\n")
        return {"name": repo, "branch": self.BRANCH, "pr_url": url, "head": self.head(repo)}

    def land(self, repo: str, branch: str, files: Dict[str, str]) -> str:
        """A work item landed on origin's main through the fake gh: its merge."""
        seed = self.seeds[repo]
        self.git(seed, "fetch", "-q", "origin")
        self.git(seed, "checkout", "-q", "-B", branch, "origin/main")
        for rel, body in dict(files, **self.upgrade(seed, branch)).items():
            write(seed, rel, body)
        head = self.commit(seed, branch)
        self.git(seed, "push", "-q", "origin", "HEAD:refs/heads/" + branch)
        prs = os.path.join(self.gh, repo, "prs")
        n = len(os.listdir(prs)) + 1 if os.path.isdir(prs) else 1
        url = "https://github.com/fake/%s/pull/%d" % (repo, n)
        for k, v in {"head": branch, "base": "main", "state": "OPEN", "url": url}.items():
            write(os.path.join(prs, str(n)), k, v + "\n")
        out = subprocess.run([os.path.join(self.bin, "gh"), "pr", "merge", url, "--merge", "--match-head-commit", head],
                             cwd=seed, env=self.env(), stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if out.returncode != 0:
            raise AssertionError(out.stdout.decode())
        return read(os.path.join(prs, str(n)), "merge").strip()

    def run(self, script: str, *args: str, **extra: str) -> Tuple[int, Optional[Dict[str, Any]], str]:
        """The script under the world's environment: its exit, the result it
        wrote at --out (checked against the schema), its stderr."""
        p = subprocess.run([sys.executable, "-B", os.path.join(SCRIPTS, script), *args], env=self.env(**extra),
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        res = None
        if "--out" in args:
            out = args[args.index("--out") + 1]
            if os.path.exists(out):
                with open(out, encoding="utf-8") as f:
                    res = json.load(f)
                errs = schema_errors(SCHEMA, res)
                if errs:
                    raise AssertionError("%s: %s" % (out, errs))
        return p.returncode, res, p.stderr.decode()


def repo_of(res: Dict[str, Any], name: str) -> Dict[str, Any]:
    for r in res["details"]["repos"]:
        if r["name"] == name:
            return r  # type: ignore[no-any-return]
    raise AssertionError("no repos entry %s: %s" % (name, res["details"]))


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class SyncScriptTest(unittest.TestCase):
    def test_merge_conflict_continue_and_abort(self) -> None:
        w = World(self, "app", "lib")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        w.change("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "branch-section")
        main = w.main_moves("app", {"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "main-section")
        lib_main = w.main_moves("lib", {"lib.txt": "l\n"})
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        self.assertEqual((code, res["verdict"], res["command"], res["round"]), (1, "fail", "sync.py", "r1"), err)
        app = repo_of(res, "app")
        self.assertEqual((app["conflicts"], app["mechanical"]), (["pkg/code.go"], ["docs/upgrade.md", "pin.txt"]))
        self.assertIn("sync.py --continue", res["summary"])
        self.assertEqual(w.git(w.dir("app"), "rev-parse", "MERGE_HEAD"), main)
        lib = repo_of(res, "lib")
        self.assertEqual((lib["after"], lib["fast_forwarded"]), (lib_main, True))
        self.assertTrue(os.path.exists(os.path.join(w.capsule, "artifacts", "sync", "syncing.json")))
        # resolved in place, continued: the same result file rewritten
        write(w.dir("app"), "pkg/code.go", "package pkg\n\nfunc F() int { return 5 }\n")
        w.git(w.dir("app"), "add", "pkg/code.go")
        code, res, err = w.run("sync.py", "--continue", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual((code, app["conflicts"], app["resolved"]), (0, [], ["pkg/code.go"]), err)
        self.assertEqual(w.git(w.dir("app"), "log", "-1", "--format=%P"), app["before"] + " " + main)
        self.assertEqual(w.git(w.dir("app"), "log", "-1", "--format=%an <%ae>"), "polyforge daemon <daemon@polyforge.invalid>")
        self.assertFalse(os.path.exists(os.path.join(w.capsule, "artifacts", "sync", "syncing.json")))
        self.assertFalse(os.path.exists(os.path.join(w.dir("app"), ".git", "info", "attributes")))
        # nothing in progress: --continue refuses and leaves the result as it is
        before = read(w.capsule, "artifacts/sync/result.json")
        code, _, err = w.run("sync.py", "--continue", "--capsule", w.capsule, "--json", "--out", out)
        self.assertEqual(code, 2)
        self.assertIn("no sync in progress", err)
        self.assertEqual(read(w.capsule, "artifacts/sync/result.json"), before)
        # --abort with nothing in progress: exit 0, marked aborted
        code, res, err = w.run("sync.py", "--abort", "--capsule", w.capsule, "--out", out)
        assert res is not None
        self.assertEqual((code, res["details"]["aborted"], res["summary"]), (0, True, "aborted: no sync in progress, nothing to abort"))

    def test_continue_refuses_a_resolution_that_drops_lines_both_parents_keep(self) -> None:
        w = World(self, "app")
        d = w.dir("app")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        shared = ["shared line %03d" % i for i in range(1, 301)]
        before = w.change("app", {"notes.txt": "\n".join(shared + ["branch tail"]) + "\n"})
        main = w.main_moves("app", {"notes.txt": "\n".join(shared + ["main tail"]) + "\n"})
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        self.assertEqual((code, repo_of(res, "app")["conflicts"]), (1, ["notes.txt"]), err)
        # 200 of the 300 lines both parents carry dropped: refused, still pending
        write(d, "notes.txt", "\n".join(shared[:100] + ["branch tail", "main tail"]) + "\n")
        w.git(d, "add", "notes.txt")
        code, res, err = w.run("sync.py", "--continue", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual((code, app["conflicts"], app["resolved"]), (1, ["notes.txt"], []), err)
        self.assertEqual(w.git(d, "rev-parse", "MERGE_HEAD"), main)
        self.assertTrue([n for n in app["notes"] if "notes.txt" in n and "200" in n and "shared line 101" in n], app["notes"])
        # every shared line kept, in another order: accepted
        write(d, "notes.txt", "\n".join(shared[::-1] + ["branch tail", "main tail"]) + "\n")
        w.git(d, "add", "notes.txt")
        code, res, err = w.run("sync.py", "--continue", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual((code, app["conflicts"], app["resolved"]), (0, [], ["notes.txt"]), err)
        self.assertEqual(w.git(d, "log", "-1", "--format=%P"), before + " " + main)

    def test_continue_skips_binary_and_one_sided_files(self) -> None:
        w = World(self, "app")
        d = w.dir("app")
        out = os.path.join(w.capsule, "artifacts", "sync", "result.json")
        shared = "".join("shared %d\n" % k for k in range(1, 6))
        os.remove(os.path.join(d, "pkg", "code.go"))
        w.change("app", {"blob.bin": "\x00" + shared + "branch\n"})
        w.main_moves("app", {"blob.bin": "\x00" + shared + "main\n", "pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"})
        code, res, err = w.run("sync.py", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        self.assertEqual((code, sorted(repo_of(res, "app")["conflicts"])), (1, ["blob.bin", "pkg/code.go"]), err)
        # the binary file drops the lines both parents carry; the one-sided file is removed
        write(d, "blob.bin", "\x00resolved\n")
        w.git(d, "add", "blob.bin")
        w.git(d, "rm", "-q", "pkg/code.go")
        code, res, err = w.run("sync.py", "--continue", "--capsule", w.capsule, "--json", "--out", out, PF_ROUND="r1")
        assert res is not None
        app = repo_of(res, "app")
        self.assertEqual((code, app["conflicts"], sorted(app["resolved"])), (0, [], ["blob.bin", "pkg/code.go"]), err)
        self.assertFalse([n for n in app.get("notes") or [] if "drops" in n], app.get("notes"))

    def test_refusals(self) -> None:
        w = World(self, "app")
        for args, want in [(["--capsule", w.capsule, "--continue", "--abort"], "exclude each other"),
                           (["--capsule", w.capsule, "extra"], "unexpected arguments [extra]"),
                           (["--capsule", w.root], "the task repos")]:
            code, _, err = w.run("sync.py", *args)
            self.assertEqual(code, 2, args)
            self.assertIn(want, err)


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class MergeScriptTest(unittest.TestCase):
    def inputs(self, w: World, *entries: Dict[str, str], heads: Optional[Dict[str, str]] = None) -> Tuple[str, str, str]:
        publish = os.path.join(w.capsule, "artifacts", "publish.json")
        write(w.capsule, "artifacts/publish.json", json.dumps({"repos": list(entries)}))
        verified = os.path.join(w.capsule, "artifacts", "verify", "result.json")
        write(w.capsule, "artifacts/verify/result.json", json.dumps({"schema": "pf-step-result/1", "heads": heads or {e["name"]: e["head"] for e in entries}}))
        return publish, verified, os.path.join(w.capsule, "artifacts", "merge", "result.json")

    def test_lands_at_the_verified_head_and_writes_the_landing(self) -> None:
        w = World(self, "app")
        head = w.change("app", {"feature.txt": "f\n"}, "app-section")
        publish, verified, out = self.inputs(w, w.publish("app"))
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--json", "--out", out)
        assert res is not None
        r = repo_of(res, "app")
        self.assertEqual((code, r["how"], r["by_this_run"], r["branch_side"]), (0, "merged", True, head), err)
        with open(landing, encoding="utf-8") as f:
            self.assertEqual(json.load(f), {"state": "landed", "repos": [
                {"repo": "app", "pr": r["pr_url"], "head": head, "light_verified_head": head, "merge_commit": r["merge_commit"]}],
                "merge_commits": {"app": [r["merge_commit"]]}})

    def test_a_moved_head_merges_nothing_and_leaves_no_landing(self) -> None:
        w = World(self, "app")
        w.change("app", {"feature.txt": "f\n"})
        entry = w.publish("app")
        publish, verified, out = self.inputs(w, entry, heads={"app": w.git(w.dir("app"), "rev-parse", "HEAD~1")})
        landing = os.path.join(w.capsule, "artifacts", "merge", "landing.json")
        write(w.capsule, "artifacts/merge/landing.json", "{}")  # a stale one
        code, res, err = w.run("merge.py", "--publish", publish, "--verified", verified, "--landing-out", landing,
                               "--capsule", w.capsule, "--out", out)
        assert res is not None
        self.assertEqual(code, 1, err)
        self.assertIn("nothing merged", res["summary"])
        self.assertFalse(os.path.exists(landing))
        code, _, err = w.run("merge.py", "--publish", publish, "--capsule", w.capsule)
        self.assertEqual(code, 2)
        self.assertIn("both required", err)


# FAKE_PF answers `pf --json step hub get <ref>` from FAKE_PF_ITEMS (a
# JSON list of work items), as the step's hub would.
FAKE_PF = """#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
with open(os.environ["FAKE_PF_ITEMS"]) as f:
    items = json.load(f)
if a[:4] == ["--json", "step", "hub", "get"]:
    for it in items:
        if a[4] in (it.get("id"), it.get("slug")):
            print(json.dumps(it))
            sys.exit(0)
sys.stderr.write(json.dumps({"code": "DAEMON-404", "detail": "no %r" % a}) + "\\n")
sys.exit(1)
"""


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class RevertScriptTest(unittest.TestCase):
    def test_reverts_an_item_and_writes_its_move(self) -> None:
        w = World(self, "app")
        with open(os.path.join(w.bin, "pf"), "w", encoding="utf-8") as f:
            f.write(FAKE_PF)
        os.chmod(os.path.join(w.bin, "pf"), 0o755)
        m = w.land("app", "p1", {"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"})
        items = os.path.join(w.root, "items.json")
        with open(items, "w", encoding="utf-8") as f:
            json.dump([{"id": "wi_1", "slug": "p#1", "status": "wrapped", "attrs": {"integration": {"state": "landed", "merge_commits": {"app": [m]}}}}], f)
        w.land("app", "p2", {"other.txt": "o\n"})
        pins = {"app": w.git(w.seeds["app"], "rev-parse", "HEAD")}
        batch = os.path.join(w.capsule, "artifacts", "integrate", "batch.json")
        write(w.capsule, "artifacts/integrate/batch.json", json.dumps({
            "schema": "pf-step-result/1", "heads": pins,
            "details": {"items": [{"id": "wi_1", "slug": "p#1", "merge_commits": {"app": [m]}, "fix_tickets": []}], "gated": ["app"]}}))
        out = os.path.join(w.capsule, "artifacts", "integrate", "revert.json")
        ops = os.path.join(w.capsule, "artifacts", "integrate", "revert-ops.json")
        code, res, err = w.run("revert.py", "--batch", batch, "--item", "wi_1", "--ops-out", ops, "--capsule", w.capsule,
                               "--json", "--out", out, PF_WI_ID="wi_batch", FAKE_PF_ITEMS=items)
        assert res is not None
        self.assertEqual(code, 0, err)
        e = res["details"]["reverts"][0]
        rr = e["repos"][0]
        self.assertEqual((e["wi"], e["name"], e["done"], rr["merge_commits"]), ("wi_1", "p#1", True, [m]))
        self.assertEqual(res["heads"], {"app": rr["revert_merge"]})
        self.assertIn("return 1", w.git(w.dir("app"), "show", rr["revert_merge"] + ":pkg/code.go"))
        with open(ops, encoding="utf-8") as f:
            self.assertEqual(json.load(f), [{"verb": "attrs", "target": "wi_1", "name": "integration", "if": {"integration": {"state": "landed", "merge_commits": {"app": [m]}}}, "value": {
                "batch": "wi_batch", "merge_commits": {"app": [m]}, "revert_merges": {"app": rr["revert_merge"]}, "revert_prs": [rr["revert_pr"]],
                "state": "reverted"}}])
        # resumable: a second run finds the revert done, writes the record again
        code, res, err = w.run("revert.py", "--batch", batch, "--ops-out", ops, "--capsule", w.capsule, "--out", out, PF_WI_ID="wi_batch",
                               FAKE_PF_ITEMS=items)
        self.assertEqual(code, 0, err)
        # the record moved meanwhile (merged by another batch): refused, no file
        with open(items, "w", encoding="utf-8") as f:
            json.dump([{"id": "wi_1", "status": "wrapped", "attrs": {"integration": {"state": "merged", "green_tag": "green/20261101-1"}}}], f)
        code, res, err = w.run("revert.py", "--batch", batch, "--ops-out", ops, "--capsule", w.capsule, "--out", out, PF_WI_ID="wi_batch",
                               FAKE_PF_ITEMS=items)
        assert res is not None
        self.assertEqual(code, 2, err)
        self.assertIn("another move landed first", res["summary"])
        self.assertFalse(os.path.exists(ops))
        # an item neither in the batch nor held, and --ops-out outside a batch
        code, _, err = w.run("revert.py", "--batch", batch, "--item", "nope", "--capsule", w.capsule, "--out", out)
        self.assertEqual(code, 2)
        self.assertIn("--item nope is neither", err)
        code, _, err = w.run("revert.py", "--batch", batch, "--ops-out", ops, "--capsule", w.capsule)
        self.assertEqual(code, 2)
        self.assertIn("--ops-out needs PF_WI_ID", err)


@unittest.skipUnless(HAVE_GIT, "git or bash not installed")
class RunScriptTest(unittest.TestCase):
    def world(self, dockerfile: bool) -> World:
        w = World(self, "app")
        files = {"a.txt": "a\n"}
        if dockerfile:
            files[".polyforge/Dockerfile.verify"] = "FROM scratch-test-image\nRUN true\n"
        w.change("app", files)
        return w

    def test_a_plan_on_the_host_stops_at_the_first_failure(self) -> None:
        w = self.world(dockerfile=False)
        plan = os.path.join(w.root, "evidence.json")
        write(w.root, "evidence.json", json.dumps([{"repo": "app", "line": "echo one"}, {"repo": "app", "line": "echo two >&2; exit 3"},
                                                   {"repo": "app", "line": "echo three"}]))
        out = os.path.join(w.capsule, "artifacts", "run", "result.json")
        code, res, err = w.run("run.py", "--isolation", "host", "--plan-from", plan, "--capsule", w.capsule, "--out", out)
        assert res is not None
        d = res["details"]
        self.assertEqual((code, len(d["runs"]), d["runs"][1]["exit_code"], d["not_run"]), (1, 2, 3, [{"repo": "app", "line": "echo three"}]), err)
        self.assertNotIn("image", d["runs"][0])
        self.assertIn("two", d["runs"][1]["output_tail"])
        self.assertEqual(d["runs"][0]["log"], "artifacts/run/logs/app/1/command.log")
        self.assertEqual(res["heads"], {"app": w.head("app")})
        self.assertEqual(read(w.capsule, "artifacts/run/logs/.gitignore"), "# pf step command logs are local, never capsule chain content\n*\n")

    def test_a_line_in_the_container_and_the_refusals(self) -> None:
        w = self.world(dockerfile=True)
        out = os.path.join(w.capsule, "artifacts", "run", "result.json")
        code, res, err = w.run("run.py", "--isolation", "container", "--repo", "app", "--capsule", w.capsule, "--json", "--out", out,
                               "--", "echo hi && test -f .polyforge/Dockerfile.verify", PF_WI_ID="wi_r",
                               PF_TOOL_WINDOW_MS="1800000")
        assert res is not None
        run = res["details"]["runs"][0]
        self.assertEqual((code, run["exit_code"], res["details"]["deadline"]), (0, 0, "28m0s"), err)
        self.assertTrue(run["image"].startswith("pf-verify-app:"))
        with open(os.path.join(w.root, "docker", "runs.log"), encoding="utf-8") as f:
            started = f.read()
        for label in ("pf.verify.daemon=pf-step", "pf.verify.wi=wi_r", "pf.verify.repo=app", "pf.verify.expires="):
            self.assertIn(label, started)
        # no Dockerfile in a container run: not judged, never run on the host
        w2 = self.world(dockerfile=False)
        out2 = os.path.join(w2.capsule, "result.json")
        code, res, err = w2.run("run.py", "--isolation", "container", "--repo", "app", "--capsule", w2.capsule, "--out", out2, "--", "true")
        assert res is not None
        self.assertEqual(code, 2)
        self.assertIn("Dockerfile.verify", res["summary"])
        for args, want in [(["--repo", "app", "--capsule", w.capsule, "--", "true"], "--isolation (none)"),
                           (["--isolation", "vm", "--repo", "app", "--capsule", w.capsule, "--", "true"], "--isolation vm"),
                           (["--isolation", "host", "--capsule", w.capsule], "exactly one of"),
                           (["--isolation", "host", "--repo", "app", "--capsule", w.capsule, "--", "make", "smoke"], "exactly one argument")]:
            code, _, err = w.run("run.py", *args)
            self.assertEqual(code, 2, args)
            self.assertIn(want, err)


if __name__ == "__main__":
    unittest.main()
