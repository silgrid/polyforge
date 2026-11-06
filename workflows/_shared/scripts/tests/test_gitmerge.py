"""pfstep.gitmerge: the merge mechanics sync.py, merge.py and revert.py run
— merging the default branch in place with the conflicts the conf makes
mechanical resolved, the holder rules hunk by hunk, the projected pin
computed in-process, reverts that keep the log, the list and their pins,
the lane's own identity, the merge attribute override and its restore, and
the landing rules. Every repository here is hermetic: no git configuration
and no identity anywhere (the release gate's container has none), so a
path relying on the host's identity fails here as it does there."""

import hashlib
import os
import shutil
import subprocess
import tempfile
import unittest
from typing import Callable, Dict, List, Optional

from pfstep.gitmerge import conf as gconf
from pfstep.gitmerge import landing, project, resolve
from pfstep.gitmerge import merge as gm
from pfstep.gitmerge import revert as gr
from pfstep.gitmerge.conf import CONF_REL, Conf, ListDecl, parse_conf
from pfstep.gitmerge.ctx import Ctx, GoError
from pfstep.gitmerge.git import IDENTITY, LANE_ATTRIBUTES, Git, PR

HAVE_GIT = shutil.which("git") is not None

_home = ""


def setUpModule() -> None:
    global _home
    _home = tempfile.mkdtemp(prefix="gitmerge-test-")
    with open(os.path.join(_home, ".gitconfig"), "w", encoding="utf-8") as f:
        f.write("[user]\n\tuseConfigOnly = true\n")


def tearDownModule() -> None:
    shutil.rmtree(_home, ignore_errors=True)


def env(**extra: str) -> Dict[str, str]:
    """The lane's environment: hermetic HOME, no system config, no identity."""
    e = {k: v for k, v in os.environ.items() if not k.startswith("GIT_") and k != "EMAIL"}
    e.update(HOME=_home, GIT_CONFIG_NOSYSTEM="1")
    e.update(extra)
    return e


def lane(**extra: str) -> Git:
    return Git(Ctx(), env(**extra))


def git(cwd: str, *args: str) -> str:
    """A test's own git: its own identity, hermetic otherwise."""
    e = env(GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@invalid", GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@invalid")
    out = subprocess.run(["git", "-c", "init.defaultBranch=main", *args], cwd=cwd or None, env=e,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if out.returncode != 0:
        raise AssertionError("git %s: %s" % (args, out.stdout.decode()))
    return out.stdout.decode().strip()


def write(d: str, rel: str, s: str) -> None:
    p = os.path.join(d, rel)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "w", encoding="utf-8") as f:
        f.write(s)


def read(d: str, rel: str) -> str:
    with open(os.path.join(d, rel), encoding="utf-8") as f:
        return f.read()


def sha(s: str) -> str:
    return hashlib.sha256(s.encode()).hexdigest()


def read_conf(d: str) -> Conf:
    with open(os.path.join(d, CONF_REL), "rb") as f:
        return parse_conf(f.read())


class World:
    """A bare origin whose main holds an append-only upgrade log, a pin
    holder carrying its sha256, and some code; a seed clone to make
    branches with."""

    def __init__(self, tc: unittest.TestCase) -> None:
        self.root = tempfile.mkdtemp()
        tc.addCleanup(shutil.rmtree, self.root, True)
        self.origin, self.seed = os.path.join(self.root, "o.git"), os.path.join(self.root, "seed")
        git(self.root, "init", "-q", "--bare", "-b", "main", self.origin)
        git(self.root, "clone", "-q", self.origin, self.seed)
        up = "# upgrade\n\n## v1\n"
        write(self.seed, "docs/upgrade.md", up)
        write(self.seed, "pin_test.go", 'package x\n\nconst pin = "' + sha(up) + '"\n')
        write(self.seed, "code.go", "package x\n\nfunc F() int { return 1 }\n")
        write(self.seed, CONF_REL, "append_only=docs/upgrade.md\npin=docs/upgrade.md pin_test.go\n")
        self.commit_push("base", "main")

    def commit_push(self, msg: str, branch: str) -> str:
        git(self.seed, "add", "-A")
        git(self.seed, "commit", "-q", "-m", msg)
        git(self.seed, "push", "-q", "origin", "HEAD:refs/heads/" + branch)
        return git(self.seed, "rev-parse", "HEAD")

    def branch(self, name: str, section: str, code: str = "") -> str:
        git(self.seed, "fetch", "-q", "origin")
        git(self.seed, "checkout", "-q", "-B", name, "origin/main")
        up = read(self.seed, "docs/upgrade.md") + "\n## " + section + "\n"
        write(self.seed, "docs/upgrade.md", up)
        write(self.seed, "pin_test.go", 'package x\n\nconst pin = "' + sha(up) + '"\n')
        if code:
            write(self.seed, "code.go", code)
        return self.commit_push(name, name)

    def branch_files(self, name: str, section: str, holder: Callable[[str], str], files: Optional[Dict[str, str]] = None) -> str:
        git(self.seed, "fetch", "-q", "origin")
        git(self.seed, "checkout", "-q", "-B", name, "origin/main")
        up = read(self.seed, "docs/upgrade.md") + "\n## " + section + "\n"
        write(self.seed, "docs/upgrade.md", up)
        write(self.seed, "h.go", holder(sha(up)))
        for rel, s in (files or {}).items():
            write(self.seed, rel, s)
        return self.commit_push(name, name)

    def land(self, head: str) -> str:
        git(self.seed, "fetch", "-q", "origin")
        git(self.seed, "checkout", "-q", "--detach", "origin/main")
        git(self.seed, "merge", "-q", "--no-ff", "-m", "merge", head)
        git(self.seed, "push", "-q", "origin", "HEAD:refs/heads/main")
        return git(self.seed, "rev-parse", "HEAD")

    def work(self) -> str:
        w = os.path.join(tempfile.mkdtemp(dir=self.root), "work")
        git("", "clone", "-q", self.origin, w)
        return w


def sync_branch(g: Git, work: str, branch: str, c: Optional[Conf]) -> gm.Synced:
    """origin's branch checked out in work, origin's main synced into it."""
    g.fetch(work, "main", branch)
    g.checkout(work, "refs/remotes/origin/" + branch)
    main = g.rev(work, "refs/remotes/origin/main")
    return gm.sync(g, work, "main", main, c, "Merge main into " + branch)


@unittest.skipUnless(HAVE_GIT, "git not installed")
class SyncTest(unittest.TestCase):
    def test_append_only_and_pins_resolve_and_code_conflicts(self) -> None:
        w = World(self)
        a = w.branch("a", "a-section", "package x\n\nfunc F() int { return 2 }\n")
        b = w.branch("b", "b-section")
        w.branch("c", "c-section", "package x\n\nfunc F() int { return 3 }\n")
        work, g, c = w.work(), lane(), read_conf(w.seed)
        self.assertEqual(len(c.pins), 1)
        w.land(a)
        pb = sync_branch(g, work, "b", c)
        self.assertEqual((pb.conflicts, pb.resolved), ([], ["docs/upgrade.md", "pin_test.go"]))
        up = read(work, "docs/upgrade.md")
        self.assertEqual(up, "# upgrade\n\n## v1\n\n## a-section\n\n## b-section\n")
        self.assertIn(sha(up), read(work, "pin_test.go"))
        self.assertTrue(g.is_ancestor(work, pb.main, pb.updated) and g.is_ancestor(work, b, pb.updated))
        pc = sync_branch(g, work, "c", c)
        self.assertEqual(pc.conflicts, ["code.go"])
        gm.abort(g, work)

    def test_sync_merges_main_in_place(self) -> None:
        w = World(self)
        main0 = git(w.seed, "rev-parse", "origin/main")
        a = w.branch("a", "a-section", "package x\n\nfunc F() int { return 2 }\n")
        b = w.branch("b", "b-section", "package x\n\nfunc F() int { return 3 }\n")
        c = w.branch("c", "c-section")
        work, g, cf = w.work(), lane(), read_conf(w.seed)
        git(work, "checkout", "-q", "--detach", a)
        s = gm.sync(g, work, "main", main0, cf, "sync a")
        self.assertEqual((s.updated, s.frm, s.resolved, s.conflicts), ("", a, [], []))
        main_a = w.land(a)
        git(work, "fetch", "-q", "origin")
        git(work, "checkout", "-q", "--detach", c)
        s = gm.sync(g, work, "main", main_a, cf, "sync c")
        self.assertEqual((s.conflicts, s.resolved), ([], ["docs/upgrade.md", "pin_test.go"]))
        self.assertEqual(git(work, "rev-parse", "HEAD"), s.updated)
        self.assertEqual(git(work, "log", "-1", "--format=%s"), "sync c")
        self.assertEqual(git(work, "log", "-1", "--format=%P"), c + " " + main_a)
        git(work, "checkout", "-q", "--detach", b)
        s = gm.sync(g, work, "main", main_a, cf, "sync b")
        self.assertEqual((s.conflicts, s.updated), (["code.go"], ""))
        self.assertEqual(git(work, "rev-parse", "MERGE_HEAD"), main_a)
        gm.abort(g, work)

    def test_holder_hunks_are_line_based(self) -> None:
        def holder(lst: str, code: str) -> Callable[[str], str]:
            return lambda s: ("package x\n\n// the sections, one line each:\n// sections:begin\n// - v1\n" + lst
                              + '// sections:end\n\nconst pin = "' + s + '"\n\n' + code)

        for name, la, ca, lb, cb, conflict, want in [
            ("list lines", "// - a\n", "const n = 1\n", "// - b\n", "const n = 1\n", False,
             "// sections:begin\n// - v1\n// - a\n// - b\n// sections:end\n"),
            ("same code line", "", "const n = 12\n", "", "const n = 10\n", True, ""),
            ("code inserted", "", "const n = 1\nconst a = 1\n", "", "const n = 1\nconst b = 2\n", True, ""),
            ("comments outside the region", "", "const n = 1\n// a\n", "", "const n = 1\n// b\n", True, ""),
        ]:
            with self.subTest(name):
                w = World(self)
                write(w.seed, CONF_REL, "append_only=docs/upgrade.md\npin=docs/upgrade.md h.go\nlist=h.go // sections:begin sections:end\n")
                write(w.seed, "h.go", holder("", "const n = 1\n")(sha(read(w.seed, "docs/upgrade.md"))))
                w.commit_push("holder", "main")
                a = w.branch_files("a", "a", holder(la, ca))
                w.branch_files("b", "b", holder(lb, cb))
                w.land(a)
                work = w.work()
                p = sync_branch(lane(), work, "b", read_conf(w.seed))
                if conflict:
                    self.assertEqual(p.conflicts, ["h.go"])
                    continue
                self.assertEqual(p.conflicts, [])
                got = read(work, "h.go")
                self.assertIn(want, got)
                self.assertIn(sha(read(work, "docs/upgrade.md")), got)


def proj_list(up: str, extra: str = "") -> str:
    """list.md pinning up (extra rows first in the section)."""
    rows = "s/internal/public/g\ns/keep1/keep1/g\ns/keep2/keep2/g\ns/keep3/keep3/g\n"
    psha = project.projected_sha(Ctx(), ("## REWRITE\n" + rows).encode(), up.encode())
    return "# list\n\n## REWRITE\n\n" + extra + rows + 's/"' + sha(up) + '"/"' + psha + '"/g\n\n## RENAME\n'


@unittest.skipUnless(HAVE_GIT, "git not installed")
class ProjectedPinTest(unittest.TestCase):
    def world(self) -> World:
        w = World(self)
        write(w.seed, CONF_REL, "append_only=docs/upgrade.md\npin=docs/upgrade.md pin_test.go\npin=docs/upgrade.md list.md\n"
                                "pin=docs/upgrade.md list.md projected list.md\n")
        write(w.seed, "list.md", proj_list(read(w.seed, "docs/upgrade.md")))
        w.commit_push("a projected pin", "main")
        return w

    def branch(self, w: World, name: str, section: str, extra: str = "") -> str:
        git(w.seed, "fetch", "-q", "origin")
        git(w.seed, "checkout", "-q", "-B", name, "origin/main")
        up = read(w.seed, "docs/upgrade.md") + "\n## " + section + " (internal)\n"
        write(w.seed, "docs/upgrade.md", up)
        write(w.seed, "pin_test.go", 'package x\n\nconst pin = "' + sha(up) + '"\n')
        write(w.seed, "list.md", proj_list(up, extra))
        return w.commit_push(name, name)

    def test_recomputed_in_process(self) -> None:
        w = self.world()
        a = self.branch(w, "a", "a")
        self.branch(w, "b", "b")
        w.land(a)
        work, c = w.work(), read_conf(w.seed)
        self.assertEqual(c.pins[2].list, "list.md")
        p = sync_branch(lane(), work, "b", c)
        self.assertEqual(p.conflicts, [])
        self.assertEqual(read(work, "list.md"), proj_list(read(work, "docs/upgrade.md")))

    def test_a_branch_s_rewrite_row_never_runs(self) -> None:
        w = self.world()
        marker = os.path.join(w.root, "pwned")
        a = self.branch(w, "a", "a")
        self.branch(w, "b", "b", "s|.*|touch " + marker + "|e\n")
        w.land(a)
        p = sync_branch(lane(), w.work(), "b", read_conf(w.seed))
        self.assertFalse(os.path.exists(marker), "the branch's row ran on the host")
        self.assertEqual(p.conflicts, ["list.md"])
        self.assertIn("outside the grammar", ";".join(p.notes))

    def test_a_pin_with_a_command_is_refused(self) -> None:
        for line in ["pin=docs/upgrade.md h.go sh tools/h.sh", "pin=docs/upgrade.md h.go bash tools/x/projsha.sh list.md",
                     "pin=docs/upgrade.md h.go projected"]:
            with self.subTest(line), self.assertRaises(GoError) as cm:
                parse_conf((line + "\n").encode())
            self.assertIn("no pin runs a command", str(cm.exception))


@unittest.skipUnless(HAVE_GIT, "git not installed")
class RevertTest(unittest.TestCase):
    def test_revert_keeps_the_log_and_its_pin(self) -> None:
        w = World(self)
        ma = w.land(w.branch("a", "a-section", "package x\n\nfunc F() int { return 2 }\n"))
        w.land(w.branch("b", "b-section"))
        work, g, c = w.work(), lane(), read_conf(w.seed)
        main = git(work, "rev-parse", "origin/main")
        log_before = git(work, "show", "origin/main:docs/upgrade.md")
        g.checkout(work, main)
        gr.revert_merge(g, work, c, main, ma)
        log = read(work, "docs/upgrade.md")
        self.assertIn("return 1", read(work, "code.go"))
        self.assertEqual(log.strip(), log_before)
        self.assertIn(sha(log), read(work, "pin_test.go"))
        self.assertTrue(gr.reverted(g, work, ma, "HEAD"))
        self.assertFalse(gr.reverted(g, work, ma, main))

    def test_revert_keeps_the_list(self) -> None:
        w = World(self)
        write(w.seed, CONF_REL, "append_only=docs/upgrade.md\npin=docs/upgrade.md h.go\nlist=h.go // sections:begin sections:end\n")

        def holder(lst: str) -> Callable[[str], str]:
            return lambda s: "package x\n\n// sections:begin\n// - v1\n" + lst + '// sections:end\n\nconst pin = "' + s + '"\n'

        write(w.seed, "h.go", holder("")(sha(read(w.seed, "docs/upgrade.md"))))
        w.commit_push("a list", "main")
        ma = w.land(w.branch_files("a", "a", holder("// - a\n"), {"code.go": "package x\n\nfunc F() int { return 2 }\n"}))
        w.land(w.branch_files("b", "b", holder("// - a\n// - b\n")))
        work, g, c = w.work(), lane(), read_conf(w.seed)
        main = git(work, "rev-parse", "origin/main")
        g.checkout(work, main)
        gr.revert_merge(g, work, c, main, ma)
        self.assertEqual(read(work, "h.go"), holder("// - a\n// - b\n")(sha(read(work, "docs/upgrade.md"))))
        self.assertIn("return 1", read(work, "code.go"))

    def test_newest_first_and_the_revert_branch(self) -> None:
        w = World(self)
        m1 = w.land(w.branch("a", "a-section"))
        m2 = w.land(w.branch("b", "b-section"))
        m3 = w.land(w.branch("c", "c-section"))
        work, g = w.work(), lane()
        g.fetch(work, "main")
        self.assertEqual(gr.newest_first(g, work, "main", [m1, m3, m2]), [m3, m2, m1])
        self.assertEqual(gr.newest_first(g, work, "main", []), [])
        self.assertEqual(gr.revert_branch("p#12"), "integrate/revert-p-12")
        self.assertEqual(gr.revert_branch("wi_x"), "integrate/revert-wi_x")


@unittest.skipUnless(HAVE_GIT, "git not installed")
class IdentityAndAttributesTest(unittest.TestCase):
    def test_the_lane_commits_as_its_own_identity(self) -> None:
        self.assertEqual(IDENTITY, "polyforge daemon <daemon@polyforge.invalid>")
        w = World(self)
        a = w.branch("a", "a-section", "package x\n\nfunc F() int { return 2 }\n")
        w.branch("b", "b-section")
        ma = w.land(a)
        work, c = w.work(), read_conf(w.seed)
        for host in (False, True):
            extra = {}
            if host:
                extra = {"GIT_AUTHOR_NAME": "someone", "GIT_AUTHOR_EMAIL": "someone@host.invalid",
                         "GIT_COMMITTER_NAME": "someone", "GIT_COMMITTER_EMAIL": "someone@host.invalid"}
            g = lane(**extra)
            p = sync_branch(g, work, "b", c)
            self.assertNotEqual(p.updated, "")
            who = "%an <%ae>|%cn <%ce>"
            self.assertEqual(git(work, "log", "-1", "--format=" + who, p.updated), IDENTITY + "|" + IDENTITY)
            main = git(work, "rev-parse", "origin/main")
            g.checkout(work, main)
            gr.revert_merge(g, work, c, main, ma)
            self.assertEqual(git(work, "log", "-1", "--format=" + who, "HEAD"), IDENTITY + "|" + IDENTITY)

    def test_branch_attributes_never_resolve_a_conflict(self) -> None:
        for attrs in ["*.go merge=union\n", "* merge=union\n", "*.go merge=union\n* -merge\n*.go merge=union\n"]:
            with self.subTest(attrs):
                w = World(self)
                a = w.branch("a", "a-section", "package x\n\nfunc F() int { return 2 }\n")
                git(w.seed, "checkout", "-q", "-B", "u", "origin/main")
                write(w.seed, ".gitattributes", attrs)
                write(w.seed, "code.go", "package x\n\nfunc F() int { return 3 }\n")
                w.commit_push("u", "u")
                ma = w.land(a)
                work, g, c = w.work(), lane(), read_conf(w.seed)
                p = sync_branch(g, work, "u", c)
                self.assertEqual(p.conflicts, ["code.go"])
                gm.abort(g, work)
                git(w.seed, "fetch", "-q", "origin")
                git(w.seed, "checkout", "-q", "-B", "m", "origin/main")
                write(w.seed, ".gitattributes", attrs)
                write(w.seed, "code.go", "package x\n\nfunc F() int { return 4 }\n")
                w.commit_push("attributes and a later change", "main")
                git(work, "fetch", "-q", "origin")
                main = git(work, "rev-parse", "origin/main")
                g.checkout(work, main)
                with self.assertRaises(GoError) as cm:
                    gr.revert_merge(g, work, c, main, ma)
                self.assertIn("conflicts in code: code.go", str(cm.exception))

    def test_keep_attributes_leaves_them_as_found(self) -> None:
        w = World(self)
        work, g = w.work(), lane()
        p = os.path.join(work, ".git", "info", "attributes")

        class Boom(Exception):
            pass

        for name, before, after in [("absent", None, None), ("custom", "*.bin binary\n", "*.bin binary\n"),
                                    ("custom, error", "*.png -diff\n", "*.png -diff\n"),
                                    ("a crash's left override", LANE_ATTRIBUTES.decode(), None)]:
            with self.subTest(name):
                if os.path.exists(p):
                    os.remove(p)
                if before is not None:
                    os.makedirs(os.path.dirname(p), exist_ok=True)
                    with open(p, "w", encoding="utf-8") as f:
                        f.write(before)

                def fn() -> None:
                    g.pin_attributes(work)
                    with open(p, "rb") as f:
                        self.assertEqual(f.read(), LANE_ATTRIBUTES)
                    if name.endswith("error"):
                        raise Boom()

                if name.endswith("error"):
                    with self.assertRaises(Boom):
                        g.keep_attributes(work, fn)
                else:
                    g.keep_attributes(work, fn)
                if after is None:
                    self.assertFalse(os.path.exists(p))
                else:
                    self.assertEqual(read(work, ".git/info/attributes"), after)

    def test_conf_at_reads_the_revision(self) -> None:
        w = World(self)
        work, g = w.work(), lane()
        write(work, CONF_REL, "append_only=CHANGELOG.md\n")
        self.assertEqual(gm.conf_at(g, work, "refs/remotes/origin/main").append_only, ["docs/upgrade.md"])
        git(work, "rm", "-q", "--cached", CONF_REL)
        git(work, "commit", "-q", "-m", "drop conf")
        c = gm.conf_at(g, work, "HEAD")
        self.assertEqual((c.append_only, c.pins, c.lists), ([], [], []))
        with self.assertRaises(GoError):
            gm.conf_at(g, work, "refs/remotes/origin/nope")


@unittest.skipUnless(HAVE_GIT, "git not installed")
class LandingTest(unittest.TestCase):
    def test_merged_from_checks_the_branch_side(self) -> None:
        w = World(self)
        b = w.branch("b", "b-section")
        mb = w.land(b)
        c = w.branch("c", "c-section")
        git(w.seed, "checkout", "-q", "-B", "side", "origin/main")
        git(w.seed, "merge", "-q", "--no-ff", "-m", "off main", c)
        git(w.seed, "push", "-q", "origin", "HEAD:refs/heads/side")
        off = git(w.seed, "rev-parse", "HEAD")
        git(w.seed, "checkout", "-q", "-B", "sq", "origin/main")
        write(w.seed, "sq.txt", "squashed\n")
        sq = w.commit_push("a squash merge", "main")
        work, g = w.work(), lane()
        for merge, heads, want in [(mb, [b], ""), (mb, ["x", b], ""), (mb, [c], "merged " + b), (sq, [b], "no branch side"),
                                   (off, [c], "not on main"), ("", [b], "no merge commit")]:
            with self.subTest(merge=merge, heads=heads):
                got = gm.merged_from(g, work, "main", merge, heads)
                self.assertEqual(want == "", got == "")
                self.assertIn(want, got)

    def test_the_landing_rule_and_take_as_landing(self) -> None:
        w = World(self)
        a = w.branch("a", "a-section")
        ma = w.land(a)
        stray = w.branch("stray", "stray-section")
        work, g = w.work(), lane()
        g.fetch(work, "main", "a", "stray")
        for merge, head, want in [("", a, "names no merge commit"), (stray, a, "is not on main"),
                                  (ma, "", "recorded no light-verified head"),
                                  (ma, stray, "does not contain the light-verified head"), (ma, a, "")]:
            got = landing.landing_rule(g, work, "main", merge, head)
            self.assertIn(want, got)
            self.assertEqual(want == "", got == "")
        self.assertEqual(landing.take_as_landing(g, work, "main", ma, [a], a), (False, ""))
        self.assertEqual(landing.take_as_landing(g, work, "main", ma, [stray], a), (True, ""))
        outside, refused = landing.take_as_landing(g, work, "main", ma, [stray], stray)
        self.assertTrue(outside)
        self.assertIn("does not contain the light-verified head", refused)
        pr = PR(state="OPEN", head_ref_name="a", base_ref_name="main")
        self.assertEqual(pr.mismatch("main", "a"), "")
        self.assertIn("targets main, not the default branch release", pr.mismatch("release", "a"))
        self.assertIn("heads the branch a, not the published b", pr.mismatch("main", "b"))

    def test_conflicted_paths_are_read_verbatim(self) -> None:
        w = World(self)
        odd = ["docs/my file.md", "docs/文件.md"]
        for p in odd:
            write(w.seed, p, "base\n")
        w.commit_push("odd names", "main")
        git(w.seed, "checkout", "-q", "-B", "b", "origin/main")
        for p in odd:
            write(w.seed, p, "branch\n")
        w.commit_push("branch side", "b")
        git(w.seed, "checkout", "-q", "-B", "main", "origin/main")
        for p in odd:
            write(w.seed, p, "main\n")
        w.commit_push("main side", "main")
        work, g = w.work(), lane()
        s = sync_branch(g, work, "b", None)
        self.assertEqual(s.conflicts, odd)
        self.assertEqual(gm.conflicted(g, work), odd)
        for p in odd:
            write(work, p, "resolved\n")
            git(work, "add", "--", p)
        gm.commit(g, work)
        from pfstep.gitmerge.git import paths
        self.assertEqual(paths("a b\x00\x00c\nd\x00a b\x00"), ["a b", "c\nd"])


class SettleHunkTest(unittest.TestCase):
    def test_settle_hunk_rules(self) -> None:
        h1, h2, h3 = "a" * 64, "b" * 64, "c" * 64
        known = {h1, h2}
        lst = ListDecl(holder="h.go", prefix=b"//", begin=b"sections:begin", end=b"sections:end")
        L = resolve.lines

        def region(br: str, base: str, mn: str) -> List[resolve.Seg]:
            return [resolve.Seg(lines=L(b"package x\n\n// sections:begin\n// - v1\n")),
                    resolve.Seg(h=resolve.Hunk(br=L(br.encode()), base=L(base.encode()), mn=L(mn.encode()))),
                    resolve.Seg(lines=L(b"// sections:end\n\nconst n = 1\n"))]

        def outside(br: str, base: str, mn: str) -> List[resolve.Seg]:
            return [resolve.Seg(lines=L(b"package x\n\n// sections:begin\n// - v1\n// sections:end\n")),
                    resolve.Seg(h=resolve.Hunk(br=L(br.encode()), base=L(base.encode()), mn=L(mn.encode()))),
                    resolve.Seg(lines=L(b"\nconst n = 1\n"))]

        for name, segs, want in [
            ("120", outside("const n = 10\n", "const n = 1\n", "const n = 12\n"), None),
            ("pin line", outside('pin = "' + h1 + '"\n', 'pin = "' + h3 + '"\n', 'pin = "' + h2 + '"\n'), 'pin = "' + h2 + '"\n'),
            ("undeclared hash", outside('x = "' + h3 + '"\n', 'x = ""\n', 'x = "' + h1 + '"\n'), None),
            ("list lines", region("// - b\n", "", "// - a\n"), "// - a\n// - b\n"),
            ("list lines after a kept one", region("// - v2\n// - b\n", "// - v2\n", "// - v2\n// - a\n"), "// - v2\n// - a\n// - b\n"),
            ("list lines outside the region", outside("// - b\n", "", "// - a\n"), None),
            ("another comment prefix", region("# - b\n", "", "# - a\n"), None),
            ("text spliced into a line", region("// x), and the b section, and\n", "// x), and\n", "// x), and the a section, and\n"), None),
            ("a marker inserted", region("// - b\n// sections:end\n", "", "// - a\n"), None),
            ("pointer store", region("*p = 10\n", "*p = 1\n", "*p = 12\n"), None),
            ("define", region("#define N 10\n", "#define N 1\n", "#define N 12\n"), None),
            ("markdown bullet", region(" * timeout: 10s\n", " * timeout: 1s\n", " * timeout: 12s\n"), None),
            ("build constraint", region("//go:build linux\n", "", "//go:build darwin\n"), None),
            ("code lines", region("const b = 2\n", "", "const a = 1\n"), None),
        ]:
            with self.subTest(name):
                h = segs[1].h
                assert h is not None
                got = resolve.settle_hunk(h, known, resolve.regions_of([lst], segs, 1))
                if want is None:
                    self.assertIsNone(got)
                else:
                    self.assertEqual(b"".join(got or []).decode(), want)

    def test_list_grammar(self) -> None:
        c = parse_conf(b"list=test/a_test.go // upgrade-sections:begin upgrade-sections:end\n")
        self.assertEqual(c.lists, [ListDecl(holder="test/a_test.go", prefix=b"//", begin=b"upgrade-sections:begin", end=b"upgrade-sections:end")])
        self.assertTrue(c.holder("test/a_test.go"))
        for bad in ["list=a.go // begin", "list=a.go // m m", "list=../a.go // b e", "list=a.go // b e extra"]:
            with self.subTest(bad), self.assertRaises(GoError):
                parse_conf((bad + "\n").encode())

    def test_go_clean(self) -> None:
        for p, want in [("", "."), ("a/./b/../c", "a/c"), ("../a/..", ".."), ("a/../..", ".."), ("//a", "/a"), ("/..", "/")]:
            self.assertEqual(gconf.go_clean(p), want, p)


if __name__ == "__main__":
    unittest.main()
