"""facts.py's computations on real git repositories — ported from the Go
facts package's own tests (facts/facts_test.go) and the driver's tests
that pinned it (planconformance, w374 D2 plan grammar, build settle and
test_weakened, fast_lane, w403 carries): the units file and the resolved
units, the test-first check, the test paths from the base conf, the
test_weakened hunks, plan conformance both ways and its grammar, the
micro bounds and the e2e carries."""

import json
import os
import re
import unittest
from typing import Dict, List, Optional

import facts
from pfstep import gotext

from .gitworld import GitCase, Repo, git, write


class UnitsTest(GitCase):
    def world(self, base: Dict[str, str]) -> Repo:
        r = Repo(os.path.join(self.tmp, "repos", "app"), base)
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        return r

    def test_read_units_shape(self) -> None:
        p = os.path.join(self.tmp, "units.json")
        with open(p, "w") as f:
            f.write('[{"id":"u1","test_commit":"abc","impl_commit":"def","run":{"repo":"app","cwd":".","argv":["go","test","./..."]},"marker":"^ok"}]')
        units = facts.read_units(p)
        self.assertEqual(len(units), 1)
        u = units[0]
        self.assertEqual((u["id"], u["run"]["repo"], u["run"]["argv"], u["marker"]), ("u1", "app", ["go", "test", "./..."], "^ok"))
        with open(p, "w") as f:
            f.write('{"not":"a list"}')
        with self.assertRaisesRegex(facts.UnitsError, "JSON array"):
            facts.read_units(p)

    def test_units_resolve_and_the_test_first_check(self) -> None:
        r = self.world({"app.go": "package app\n"})
        tc = r.commit("u1: test", {"app_test.go": "package app\n"})
        ic = r.commit("u1: impl", {"app.go": "package app\n\nfunc A() {}\n"})
        mixed = r.commit("u2: test and impl", {"b_test.go": "package app\n", "b.go": "package app\n"})
        repos = os.path.join(self.tmp, "repos")

        def decl(i: str, t: str, m: str) -> Dict[str, object]:
            return {"id": i, "test_commit": t, "impl_commit": m, "run": None, "marker": ""}

        units = facts.units_of(repos, ["app"], [decl("u1", tc[:10], ic), decl("u2", mixed, mixed), decl("u3", "0000000", ic), decl("u4", tc, "HEAD")])
        self.assertEqual(len(units), 4)
        u = units[0]
        self.assertEqual((u.why, u.repo, u.test_commit, u.impl_commit, u.test_files), ("", "app", tc, ic, ["app_test.go"]))
        self.assertIn("not both commits of one task repo", units[2].why)
        self.assertTrue(units[3].why, "HEAD is no commit name a unit may report")
        repo = facts.Repo(repos, "app")
        self.assertEqual(repo.check_unit_commits(tc, ic), "")
        self.assertIn("the same commit", repo.check_unit_commits(mixed, mixed))
        self.assertIn("not an ancestor", repo.check_unit_commits(ic, tc))
        self.assertEqual(facts.unit_test_files(units), ["app_test.go", "b.go", "b_test.go"])

    def test_test_paths_from_the_base_conf(self) -> None:
        rules = facts.TestPathRules()
        for p, want in {"a/b_test.go": True, "pkg/testdata/x.json": True, "src/x.test.ts": True, "web/e2e/run.mjs": True,
                        "tests/test_x.py": True, "src/__tests__/a.js": True, "a/b.go": False, "src/x.ts": False, "README.md": False}.items():
            self.assertEqual(rules.is_test(p), want, p)
        r = self.world({"app.go": "package app\n", ".polyforge/verify-scope.conf": "stack=npm\ntest_glob=spec/**\ngo_harness=harness/run.sh\n"})
        got = facts.TestPathRules.at(r.dir)
        for p, want in {"spec/a.js": True, "harness/x_test.go": True, "harness/helper.go": True, "src/x.test.ts": True, "lib/a.js": False}.items():
            self.assertEqual(got.is_test(p), want, (p, got.describe()))
        # the change's own conf never widens its test paths
        r.commit("widen", {".polyforge/verify-scope.conf": "test_glob=lib/**\n"})
        self.assertFalse(facts.TestPathRules.at(r.dir).is_test("lib/a.js"), "SYMPTOM: a test_glob the change itself added counted")

    def test_shell_join(self) -> None:
        self.assertEqual(gotext.shell_join(["go", "test", "./...", "-run", "^Test(A|B)$", "it's"]), "go test ./... -run '^Test(A|B)$' 'it'\\''s'")


WEAK_TEST = '''// carries: app.go
package app

import "testing"

func TestAnswer(t *testing.T) {
\tif Answer() != 42 {
\t\tt.Fatal("Answer must be 42")
\t}
}
'''


class WeakenedTest(GitCase):
    def world(self) -> "tuple[Repo, List[facts.Unit]]":
        base = os.path.join(self.tmp, "w%d" % len(os.listdir(self.tmp)))
        r = Repo(os.path.join(base, "repos", "app"), {"app.go": "package app\n"})
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        tc = r.commit("u1: test", {"app_test.go": WEAK_TEST})
        ic = r.commit("u1: impl", {"app.go": "package app\n\nfunc Answer() int { return 42 }\n"})
        units = facts.units_of(os.path.dirname(r.dir), ["app"], [{"id": "u1", "test_commit": tc, "impl_commit": ic, "run": None, "marker": ""}])
        self.assertEqual((units[0].why, units[0].test_files), ("", ["app_test.go"]))
        return r, units

    def test_table(self) -> None:
        cases = {
            "untouched test": (WEAK_TEST, "", "", False),
            "a pure addition": (WEAK_TEST + "\nfunc TestMore(t *testing.T) {}\n", "", "", False),
            "a deleted assertion": (WEAK_TEST.replace('\t\tt.Fatal("Answer must be 42")\n', "", 1), "1 of them assertion-looking", "", False),
            "a relaxed assertion": (WEAK_TEST.replace("!= 42", "< 0", 1), "deletes or changes 1 line(s)", "", False),
            "an added skip": (WEAK_TEST.replace("func TestAnswer(t *testing.T) {\n", "func TestAnswer(t *testing.T) {\n\tt.Skip(\"later\")\n", 1), "adds a skip", "", False),
            "a changed carries header": (WEAK_TEST.replace("// carries: app.go", "// carries: nothing.go", 1), "changes the `carries:` header", "", False),
            "a change the summary cites": (WEAK_TEST.replace("!= 42", "!= 43", 1), "deletes or changes", "app_test.go fixed per review s4#2 (the expected value was wrong)", True),
            "a citation naming no file": (WEAK_TEST.replace("!= 42", "!= 43", 1), "deletes or changes", "fixed per review s4#2", False),
            "a file named without a ref": (WEAK_TEST.replace("!= 42", "!= 43", 1), "deletes or changes", "changed app_test.go", False),
        }
        for name, (after, reason, summary, cited) in cases.items():
            with self.subTest(name):
                r, units = self.world()
                if after != WEAK_TEST:
                    r.commit("later", {"app_test.go": after})
                weakened, cites = facts.weakened_facts(os.path.dirname(r.dir), units, summary)
                if not reason:
                    self.assertEqual(weakened + cites, [])
                elif cited:
                    self.assertEqual(weakened, [])
                    self.assertEqual(len(cites), 1)
                    self.assertEqual(cites[0]["cite"], "s4#2")
                    self.assertIn(reason, cites[0]["reason"])
                else:
                    self.assertEqual(len(weakened), 1, (weakened, cites))
                    f = weakened[0]
                    self.assertIn(reason, f["reason"])
                    self.assertEqual((f["path"], f["unit"]), ("app_test.go", "u1"))
                    self.assertIn("@@", f["hunk"])


CONFORMANCE_PLAN = json.loads('''{"touched_files":[
 {"path":"a.go","intent":"write","coupled_obligations":[{"path":"docs/a.md","note":"the a row"}]},
 {"path":"b.go","intent":"write","coupled_obligations":[{"none":true,"reason":"nothing lists b"}]},
 {"path":"c.go","intent":"write","coupled_obligations":[{"none":true,"reason":"fixture"}]},
 {"path":"r.go","intent":"read"}]}''')["touched_files"]

DEVELOP_PLAN = (
    "# plan\n\n## Acceptance\n\n- AC1 x\n\n## Units\n\n"
    "1. **u1** — the answer\n"
    "   - Touched files: `app.go` (write), `lib/util.go` (read)\n"
    "   - Coupled obligations: `docs/app.md` (the table gains the row), `README.md`\n"
    "2. **u2** — check only\n"
    "   - Touched files: (no file edits)\n"
    "3. **u3** — the flag\n"
    "   - Touched files: cfg.go (write)\n"
    "   - Coupled obligations: none — a private constant no document lists\n\n"
    "## Coverage\n\n- Touched files: `outside.go` (write)\n"
)


class PlanConformanceTest(GitCase):
    def test_both_directions(self) -> None:
        pc = facts.fold_plan_conformance(CONFORMANCE_PLAN, [], {"app": ["a.go", "b.go", "r.go", "x.go"]}, "")
        self.assertEqual(pc["undeclared"], ["r.go", "x.go"], "a read declaration does not declare a write")
        self.assertEqual(pc["unchanged_obligations"], ["docs/a.md"])
        self.assertEqual(pc["unchanged_writes"], ["c.go"])
        self.assertFalse(facts.conformant(pc))

    def test_unavailable_base_says_so(self) -> None:
        pc = facts.fold_plan_conformance(CONFORMANCE_PLAN, [], None, "repo app: no base")
        self.assertIsNone(pc["undeclared"])
        self.assertIsNone(pc["unchanged_obligations"])
        self.assertIsNone(pc["unchanged_writes"])
        self.assertIn("repo app: no base", pc["unavailable"])

    def test_conformant(self) -> None:
        base = {"unavailable": "", "undeclared": None, "unchanged_obligations": None}
        self.assertTrue(facts.conformant(dict(base, unchanged_writes=["x"], released=["y"])))
        for bad in ({"undeclared": ["z"]}, {"unchanged_obligations": ["d.md"]}, {"unavailable": "no base"}):
            self.assertFalse(facts.conformant(dict(base, **bad)), bad)

    def test_authored_test_files_count_as_declared(self) -> None:
        changed = {"app": ["a.go", "test/w78_acceptance_test.go", "x.go"]}
        pc = facts.fold_plan_conformance(CONFORMANCE_PLAN, ["test/w78_acceptance_test.go"], changed, "")
        self.assertEqual(pc["undeclared"], ["x.go"])
        for fs in (pc["unchanged_writes"] or [], pc["unchanged_obligations"] or []):
            self.assertNotIn("test/w78_acceptance_test.go", fs)
        pc = facts.fold_plan_conformance(CONFORMANCE_PLAN, [], changed, "")
        self.assertEqual(pc["undeclared"], ["test/w78_acceptance_test.go", "x.go"])

    def test_changed_files_against_origin_default(self) -> None:
        r = Repo(os.path.join(self.tmp, "caps", "repos", "app"), {"a.go": "a", "old.go": "o"})
        repos = os.path.join(self.tmp, "caps", "repos")
        _, reason = facts.changed_files(repos)
        self.assertIn("origin default branch", reason)
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        write(r.dir, {"a.go": "a2", "new.go": "n"})
        r.git("rm", "-q", "old.go")
        r.git("add", "-A")
        r.git("commit", "-q", "-m", "work")
        write(r.dir, {"uncommitted.go": "u"})
        got, reason = facts.changed_files(repos)
        self.assertEqual(reason, "")
        self.assertEqual(got, {"app": ["a.go", "new.go", "old.go"]})

    def test_plan_touched_files_grammar(self) -> None:
        got = facts.plan_touched_files(DEVELOP_PLAN)
        want = json.loads(
            '[{"coupled_obligations":[{"note":"the table gains the row","path":"docs/app.md"},{"note":"","path":"README.md"}],"intent":"write","path":"app.go"},'
            '{"intent":"read","path":"lib/util.go"},'
            '{"coupled_obligations":[{"none":true,"reason":"a private constant no document lists"}],"intent":"write","path":"cfg.go"}]')
        self.assertEqual(got, want)


FL_CONF = "micro_path=docs/**\nmicro_path=*.md\nfast_gate=sh fast.sh\nmicro_check=sh keys.sh\n"


def fl_base() -> Dict[str, str]:
    return {".polyforge/verify-scope.conf": FL_CONF, "docs/a.md": "a\n", "src/x.go": "package x\n", "AGENTS.md": "rules\n"}


class MicroBoundsTest(GitCase):
    def fl_repo(self, repos: str, name: str, base: Dict[str, str]) -> Repo:
        bare = os.path.join(self.tmp, "origins", os.path.basename(repos) + "-" + name + ".git")
        os.makedirs(os.path.dirname(bare), exist_ok=True)
        git(os.path.dirname(bare), "init", "-q", "--bare", "-b", "main", bare)
        r = Repo(os.path.join(repos, name), base)
        r.git("remote", "add", "origin", bare)
        r.git("push", "-q", "origin", "HEAD:refs/heads/main")
        r.git("fetch", "-q", "origin")
        r.git("remote", "set-head", "origin", "main")
        return r

    def test_micro_decls(self) -> None:
        conf = ("# c\n  micro_path=docs/**\nmicro_path=/abs\nmicro_path=./x\nmicro_path=a/../b\nmicro_path=\nmicro_path=docs/**\n"
                "micro_check=sh check.sh\nmicro_check=$(evil)\nmicro_check=\nfast_gate=echo\n")
        self.assertEqual(facts.micro_decls(conf), (["docs/**"], ["sh check.sh"]))

    def test_in_bounds_and_the_plan(self) -> None:
        repos = self.new_dir("repos")
        r = self.fl_repo(repos, "demo", fl_base())
        r.commit("change", {"docs/a.md": "b\n", "docs/new/n.md": "n\n", "README.md": "r\n"})
        self.fl_repo(repos, "other", {"x": "x\n"})  # untouched: skipped, opted in or not
        v = facts.micro_bounds_of(repos, [{"repo": "demo", "command": "sh fast.sh"}, {"repo": "nope", "command": "x"}])
        self.assertTrue(v["in_bounds"], v)
        self.assertNotIn("reason", v)
        self.assertEqual(len(v["repos"]), 1)
        rr = v["repos"][0]
        self.assertEqual((rr["name"], rr["changed_paths"]), ("demo", ["README.md", "docs/a.md", "docs/new/n.md"]))
        self.assertEqual(rr["commands"], [{"repo": "demo", "command": "sh fast.sh"}, {"repo": "demo", "command": "sh keys.sh"}])

    def test_refusals(self) -> None:
        fix = [{"repo": "demo", "command": "sh fast.sh"}]

        def renamed(r: Repo) -> None:
            r.git("mv", "src/x.go", "docs/x.go")
            r.git("commit", "-q", "-m", "rename")

        def deleted(r: Repo) -> None:
            r.git("rm", "-q", "src/x.go")
            r.git("commit", "-q", "-m", "delete")

        def unreadable(r: Repo) -> None:
            r.git("remote", "set-head", "origin", "-d")
            r.git("update-ref", "-d", "refs/remotes/origin/main")

        cases = [
            ("code path", fl_base(), {"docs/a.md": "b\n", "src/x.go": "package x\n// y\n"}, None, "src/x.go"),
            ("hard-excluded despite *.md", fl_base(), {"AGENTS.md": "new rules\n"}, None, "AGENTS.md (an agent-contract file"),
            ("nested CLAUDE.md", fl_base(), {"docs/CLAUDE.md": "x\n"}, None, "docs/CLAUDE.md (an agent-contract file"),
            ("undeclared repo", {".polyforge/verify-scope.conf": "fast_gate=true\n", "docs/a.md": "a\n"}, {"docs/a.md": "b\n"}, None, "declares no micro_path"),
            ("no conf at base", {"docs/a.md": "a\n"}, {"docs/a.md": "b\n", ".polyforge/verify-scope.conf": FL_CONF}, None,
             "no .polyforge/verify-scope.conf is readable at the diff base"),
            ("base conf wins over a widened one", fl_base(),
             {".polyforge/verify-scope.conf": FL_CONF + "micro_path=src/**\nmicro_path=.polyforge/**\n", "src/x.go": "package x\n// y\n"}, None, "src/x.go"),
            ("rename judges the old side", fl_base(), None, renamed, "src/x.go"),
            ("deletion", fl_base(), None, deleted, "src/x.go"),
            ("unreadable diff base", fl_base(), {"docs/a.md": "b\n"}, unreadable, "origin default branch is unknown"),
        ]
        for name, base, change, setup, want in cases:
            with self.subTest(name):
                repos = self.new_dir("repos-" + re.sub(r"[^a-z]+", "-", name, flags=re.ASCII))
                r = self.fl_repo(repos, "demo", base)
                if change is not None:
                    r.commit("change", change)
                if setup is not None:
                    setup(r)
                v = facts.micro_bounds_of(repos, fix)
                self.assertFalse(v["in_bounds"])
                self.assertEqual(v["repos"], [])
                self.assertIn(want, v["reason"])
                self.assertTrue(v["reason"].startswith(facts.MICRO_REASON_HEAD))
                self.assertNotIn("re-file", v["reason"])

    def test_each_repo_on_its_own_declaration(self) -> None:
        repos = self.new_dir("repos")
        a = self.fl_repo(repos, "a", {".polyforge/verify-scope.conf": "micro_path=workflows/**\nfast_gate=true\n", "workflows/w.md": "w\n"})
        b = self.fl_repo(repos, "b", {".polyforge/verify-scope.conf": "micro_path=docs/**\nfast_gate=true\n", "workflows/w.md": "w\n"})
        a.commit("c", {"workflows/w.md": "w2\n"})
        b.commit("c", {"workflows/w.md": "w2\n"})
        v = facts.micro_bounds_of(repos, [{"repo": "a", "command": "true"}, {"repo": "b", "command": "true"}])
        self.assertFalse(v["in_bounds"])
        self.assertIn("repos/b:", v["reason"])
        self.assertNotIn("repos/a:", v["reason"])
        self.assertIn('micro_path ["docs/**"]', v["reason"])

    def test_empty_plan_and_no_change(self) -> None:
        repos = self.new_dir("repos")
        r = self.fl_repo(repos, "demo", {".polyforge/verify-scope.conf": "micro_path=docs/**\n", "docs/a.md": "a\n"})
        r.commit("c", {"docs/a.md": "b\n"})
        v = facts.micro_bounds_of(repos, [])
        self.assertIn("no fast evidence command", v["reason"])
        repos2 = self.new_dir("repos2")
        self.fl_repo(repos2, "demo", fl_base())
        self.assertIn("touches no task repository", facts.micro_bounds_of(repos2, [])["reason"])


CARRIES_SEED = {
    "app.go": "package app\n",
    "web/.polyforge/verify-scope.conf": "stack=npm\nforced_full=next.config.ts\nexclude=themes.mjs\ne2e_runner=npm run test:e2e:all --\n",
    "web/package.json": '{"scripts":{"test:e2e:all":"node e2e/all.mjs"}}\n',
    "web/next.config.ts": "export default {};\n",
    "web/lib/panel.ts": "export const panel = 1;\n",
    "web/lib/other.ts": "export const other = 1;\n",
    "web/e2e/run.mjs": '// carries: lib/run*\n\nimport { settle } from "./timeouts.mjs";\n',
    "web/e2e/timeouts.mjs": "export const settle = 1;\n",
    "web/e2e/legacy.mjs": 'import "node:assert";\n',
    "web/e2e/all.mjs": 'import "./run.mjs";\n',
    "web/e2e/themes.mjs": "export const themes = [];\n",
}


def unit_run(command: str) -> List[facts.Binding]:
    return [facts.Binding("app", command)]


class LaneTextTest(unittest.TestCase):
    def test_no_plan_bindings_section(self) -> None:
        """No workflow text or script reads or asks for the plan's retired
        Bindings section: the carries check takes its runs from units.json."""
        needle = "## " + "Bindings"
        root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
        self.assertEqual(os.path.basename(root), "workflows")
        hits = []
        for d, dirs, files in os.walk(root):
            dirs[:] = [x for x in dirs if x != "__pycache__"]
            for name in files:
                path = os.path.join(d, name)
                with open(path, encoding="utf-8", errors="replace") as f:
                    for n, line in enumerate(f, 1):
                        if needle in line:
                            hits.append("%s:%d" % (os.path.relpath(path, root), n))
        self.assertEqual(hits, [], "SYMPTOM: workflow text still names the plan's %s section" % needle)


class CarriesTest(GitCase):
    def world(self, seed: Dict[str, str]) -> Repo:
        r = Repo(os.path.join(self.tmp, "w%d" % len(os.listdir(self.tmp)), "repos", "app"), seed)
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        return r

    def problems(self, r: Repo, unit_repo: bool = False, bindings: Optional[List[facts.Binding]] = None) -> str:
        return "\n".join(facts.Repo(os.path.dirname(r.dir), "app").check_e2e_carries(unit_repo, bindings or []))

    def expect(self, got: str, *wants: str) -> None:
        if not wants:
            self.assertEqual(got, "", "SYMPTOM: want no problem")
            return
        for w in wants:
            self.assertIn(w, got)

    def test_develop_build_e2e_carries(self) -> None:
        panel = "web/lib/panel.ts"
        touch = {panel: "export const panel = 2;\n"}

        def with_(extra: Dict[str, str]) -> Dict[str, str]:
            m = dict(touch)
            m.update(extra)
            return m

        run_edited = '// carries: lib/run*\n\nimport { settle } from "./timeouts.mjs";\n// panel too\n'
        cases = {
            "a new suite whose carries cover a touched path": (with_({"web/e2e/panel.mjs": '// carries: lib/panel*\n\nimport "node:assert";\n'}), None, []),
            "a carries line among others in the opening block": (with_({"web/e2e/panel.mjs": '#!/usr/bin/env node\n// the panel scenario\n// carries: components/Panel*\n// carries: lib/pan*\nimport "node:assert";\n'}), None, []),
            "a new suite whose carries match no touched path": (with_({"web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'}), None,
                                                                ["e2e suite repos/app/web/e2e/panel.mjs, which this build added, declares `carries: lib/other*`, which matches none of the non-test paths this build touched under web/: lib/panel.ts"]),
            "a new suite without carries": (with_({"web/e2e/panel.mjs": 'import "node:assert";\n// carries: lib/panel* (after code: not a declaration)\n'}), None,
                                            ["e2e suite repos/app/web/e2e/panel.mjs declares no `carries:` in its opening comment block", "under web/: lib/panel.ts"]),
            "an empty carries value": (with_({"web/e2e/panel.mjs": '// carries:\nimport "node:assert";\n'}), None,
                                       ["e2e suite repos/app/web/e2e/panel.mjs has a malformed `carries:` declaration"]),
            "a carries line without its colon": (with_({"web/e2e/panel.mjs": '// carries lib/panel*\nimport "node:assert";\n'}), None, ["has a malformed `carries:` declaration"]),
            "an edited suite whose carries do not cover the change": (with_({"web/e2e/run.mjs": run_edited}), None, []),
            "an edited suite without carries": (with_({"web/e2e/legacy.mjs": 'import "node:assert";\n// fixed\n'}), None,
                                                ["e2e suite repos/app/web/e2e/legacy.mjs declares no `carries:` in its opening comment block"]),
            "an edited suite made malformed": (with_({"web/e2e/run.mjs": '// carries\nimport "node:assert";\n'}), None,
                                               ["e2e suite repos/app/web/e2e/run.mjs has a malformed `carries:` declaration"]),
            "an edited suite a unit's recorded run names, root-relative": (with_({"web/e2e/run.mjs": run_edited}), unit_run("cd web && node e2e/run.mjs"),
                                                                           ["e2e suite repos/app/web/e2e/run.mjs, which a unit's recorded run names, declares `carries: lib/run*`, which matches none of the non-test paths this build touched under web/: lib/panel.ts"]),
            "an edited suite a unit's recorded run names, repo-relative": (with_({"web/e2e/run.mjs": run_edited}), unit_run("npm --prefix web run test:e2e:all -- ./web/e2e/run.mjs"),
                                                                           ["which a unit's recorded run names"]),
            "a run in another repo binds nothing here": (with_({"web/e2e/run.mjs": run_edited}), [facts.Binding("other", "node e2e/run.mjs")], []),
            "a run naming another file binds nothing": (with_({"web/e2e/run.mjs": run_edited}), unit_run("node e2e/run.mjs.bak"), []),
            "a changed helper a suite imports": (with_({"web/e2e/timeouts.mjs": "export const settle = 2;\n"}), None, []),
            "a new helper a new suite imports without its extension": (with_({
                "web/e2e/panel.mjs": "// carries: lib/panel*\nimport { fx } from './fixture';\n",
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, []),
            "a new helper reached through a non-suite module": (with_({
                "web/e2e/panel.mjs": '// carries: lib/panel*\nconst s = require("../lib/support.mjs");\n',
                "web/lib/support.mjs": 'export { fx } from "../e2e/fixture.mjs";\n',
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, []),
            "a new helper loaded by a dynamic import": (with_({
                "web/e2e/panel.mjs": "// carries: lib/panel*\nconst { fx } = await import( './fixture.mjs' );\n",
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, []),
            "a new helper imported across a vertical tab ([[:space:]])": (with_({
                "web/e2e/panel.mjs": "// carries: lib/panel*\nimport\v'./fixture.mjs';\n",
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, []),
            "a no-break space is no space ([[:space:]] is ASCII)": (with_({
                "web/e2e/panel.mjs": "// carries: lib/panel*\nimport\xa0'./fixture.mjs';\n",
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, ["e2e suite repos/app/web/e2e/fixture.mjs declares no `carries:`"]),
            "a new undeclared suite a package.json script names": (with_({
                "web/package.json": '{"scripts":{"test:e2e:all":"node e2e/all.mjs","test:panel":"node e2e/panel.mjs"}}\n',
                "web/e2e/panel.mjs": 'import "node:assert";\n'}), None, ["e2e suite repos/app/web/e2e/panel.mjs declares no `carries:`"]),
            "a new undeclared suite whose name is a route": ({panel: 'export const panel = await fetch("/api/panel");\n', "web/e2e/panel.mjs": 'import "node:assert";\n'}, None,
                                                             ["e2e suite repos/app/web/e2e/panel.mjs declares no `carries:`"]),
            "a new undeclared suite imported only from a module no suite reaches": (with_({
                "web/lib/orphan.ts": 'import { fx } from "../e2e/fixture.mjs";\n',
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, ["e2e suite repos/app/web/e2e/fixture.mjs declares no `carries:`"]),
            "a new undeclared suite imported only in a comment's prose": (with_({
                "web/e2e/panel.mjs": '// carries: lib/panel*\n// see the fixture.mjs file\nimport "node:assert";\n',
                "web/e2e/fixture.mjs": "export const fx = 1;\n"}), None, ["e2e suite repos/app/web/e2e/fixture.mjs declares no `carries:`"]),
            "the changed e2e_runner target": (with_({"web/e2e/all.mjs": 'import "./run.mjs";\n// all\n'}), None, []),
            "a changed exclude= match": (with_({"web/e2e/themes.mjs": "export const themes = [1];\n"}), None, []),
            "a suite changed alone: nothing to cover": ({"web/e2e/run.mjs": run_edited}, None, []),
            "a new suite added alone still declares": ({"web/e2e/panel.mjs": 'import "node:assert";\n'}, None, ["declares no `carries:`", "under web/: none"]),
            "doc and test paths are not what a suite covers": ({"web/README.md": "x\n", "web/lib/panel.test.ts": "x\n", "web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'}, None, []),
            "a path outside the root is not the root's to cover": ({"app.go": "package app\n\nvar X = 1\n", "web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'}, None, []),
            "paths that force the full tier are not what a suite covers": ({
                "web/package.json": '{"scripts":{"test:e2e:all":"node e2e/all.mjs"},"x":1}\n', "web/package-lock.json": "{}\n",
                "web/tsconfig.json": "{}\n", "web/tsconfig.build.json": "{}\n", "web/eslint.config.mjs": "export default [];\n",
                "web/next.config.ts": "export default { x: 1 };\n", "web/e2e/panel.mjs": '// carries: lib/panel*\nimport "node:assert";\n'}, None, []),
            "a forced path beside a real one is not listed": (with_({
                "web/package.json": '{"scripts":{"test:e2e:all":"node e2e/all.mjs"},"x":1}\n', "web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'}), None,
                ["touched under web/: lib/panel.ts —"]),
            "an exclude= the change itself adds": (with_({"web/.polyforge/verify-scope.conf": "stack=npm\nexclude=themes.mjs\nexclude=panel.mjs\n", "web/e2e/panel.mjs": 'import "node:assert";\n'}), None,
                                                   ["e2e suite repos/app/web/e2e/panel.mjs declares no `carries:`"]),
        }
        for name, (commit, bindings, wants) in cases.items():
            with self.subTest(name):
                r = self.world(CARRIES_SEED)
                r.commit("build", commit)
                self.expect(self.problems(r, bindings=bindings), *wants)

        with self.subTest("a deleted suite"):
            r = self.world(CARRIES_SEED)
            r.git("rm", "-q", "web/e2e/run.mjs")
            r.commit("drop run", touch)
            self.expect(self.problems(r))
        with self.subTest("an unknown diff base in a repo a unit lives in"):
            r = self.world(CARRIES_SEED)
            r.git("update-ref", "-d", "refs/remotes/origin/main")
            self.expect(self.problems(r, unit_repo=True), "repos/app: the fetched origin default branch is unknown, so the e2e suites this build changed cannot be judged")
        with self.subTest("an unknown diff base under local commits"):
            r = self.world(CARRIES_SEED)
            r.git("update-ref", "refs/remotes/origin/feature", "HEAD")
            r.git("update-ref", "-d", "refs/remotes/origin/main")
            r.commit("build", touch)
            self.expect(self.problems(r), "repos/app: the fetched origin default branch is unknown")
        with self.subTest("an unknown diff base in a repo the build never touched"):
            r = self.world(CARRIES_SEED)
            r.git("update-ref", "refs/remotes/origin/feature", "HEAD")
            r.git("update-ref", "-d", "refs/remotes/origin/main")
            self.expect(self.problems(r))
            r = self.world(CARRIES_SEED)
            r.git("update-ref", "-d", "refs/remotes/origin/main")
            self.expect(self.problems(r))

    def test_suites_follow_the_engine_analyzer(self) -> None:
        def bad(p: str) -> Dict[str, str]:
            return {"src/x.ts": "export const x = 2;\n", p: '// carries: nothing/*\n\nimport "node:assert";\n'}

        cases = {
            "a Go root's suite_glob= marks nothing": ({".polyforge/verify-scope.conf": "stack=go\nsuite_glob=test/*_test.go\n", "go.mod": "module m\n", "src/x.ts": "x\n"}, bad("test/x_test.go"), ""),
            "stack=none discovers nothing": ({".polyforge/verify-scope.conf": "stack=none\n", "package.json": "{}\n", "src/x.ts": "x\n"}, bad("e2e/x.mjs"), ""),
            "no conf: the one manifest picks npm": ({"package.json": "{}\n", "src/x.ts": "x\n"}, bad("e2e/x.mjs"), "e2e suite repos/app/e2e/x.mjs, which this build added, declares `carries: nothing/*`"),
            "no conf, two manifests: ambiguous, no analyzer": ({"package.json": "{}\n", "go.mod": "module m\n", "src/x.ts": "x\n"}, bad("e2e/x.mjs"), ""),
            "no manifest at all": ({"src/x.ts": "x\n"}, bad("e2e/x.mjs"), ""),
            "suite_glob= replaces npm's discovery": ({".polyforge/verify-scope.conf": "stack=npm\nsuite_glob=tests/*.spec.mjs\n", "package.json": "{}\n", "src/x.ts": "x\n"},
                                                     {"src/x.ts": "x2\n", "e2e/x.mjs": 'import "node:assert";\n', "tests/a.spec.mjs": "// carries: nothing/*\nx\n"},
                                                     "e2e suite repos/app/tests/a.spec.mjs, which this build added"),
            "python's discovery and its imports": ({".polyforge/verify-scope.conf": "stack=python\n", "src/x.py": "x = 1\n"},
                                                   {"src/x.py": "x = 2\n", "e2e/test_main.py": "# carries: src/x*\nfrom test_util import helper\n", "e2e/test_util.py": "def helper(): pass\n", "e2e/test_lone.py": "def test_x(): pass\n"},
                                                   "e2e suite repos/app/e2e/test_lone.py declares no `carries:`"),
            "rust's discovery and its mod tree": ({".polyforge/verify-scope.conf": "stack=rust\n", "src/lib.rs": "pub fn x() {}\n"},
                                                  {"src/lib.rs": "pub fn x() { }\n", "tests/main.rs": "// carries: src/*\nmod common;\n", "tests/common.rs": "pub fn h() {}\n", "tests/lone.rs": "#[test] fn t() {}\n"},
                                                  "e2e suite repos/app/tests/lone.rs declares no `carries:`"),
        }
        for name, (seed, commit, want) in cases.items():
            with self.subTest(name):
                r = self.world(seed)
                r.commit("build", commit)
                got = self.problems(r)
                if not want:
                    self.expect(got)
                    continue
                self.expect(got, want)
                self.assertNotIn("test_util.py", got, "SYMPTOM: an imported helper is judged as a suite")
                self.assertNotIn("tests/common.rs", got, "SYMPTOM: an imported helper is judged as a suite")

    def test_carries_header(self) -> None:
        for src, want in {
            "// carries: a/*, b/c.ts\n// carries: d*\nimport x\n": (["a/*", "b/c.ts", "d*"], False),
            "#!/usr/bin/env python3\n# carries: src/x*\n\nimport x\n": (["src/x*"], False),
            "\n\n// carries:   \nx\n": ([], True),
            "// carries everything\nx\n": ([], True),
            "// carries_extra: no\nx\n": ([], False),
            "x\n// carries: late\n": ([], False),
        }.items():
            self.assertEqual(facts.carries_header(src), want, src)

    def test_js_imports_read_posix_space(self) -> None:
        # carries.go's [[:space:]]: \v counts, an Unicode space does not
        have = {"a/x.mjs"}.__contains__
        self.assertEqual(facts.js_imports("a/s.mjs", ["import\v'./x.mjs'"], have), ["a/x.mjs"])
        self.assertEqual(facts.js_imports("a/s.mjs", ["from \t\n\f\r\v('./x.mjs')"], have), ["a/x.mjs"])
        self.assertEqual(facts.js_imports("a/s.mjs", ["import\u2003'./x.mjs'"], have), [])


if __name__ == "__main__":
    unittest.main()
