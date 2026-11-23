"""facts.py's computations on real git repositories — ported from the Go
facts package's own tests (facts/facts_test.go) and the driver's tests
that pinned it (planconformance, w374 D2 plan grammar, build settle and
test_weakened, fast_lane, w403 carries): the units file and the resolved
units, the test-first check, the test paths from the base conf, the
test_weakened hunks, plan conformance both ways and its grammar, the
ticket's decided rulings (## 已定) against the plan's mapping of them,
the micro bounds and the e2e carries."""

import json
import os
import re
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Optional, Tuple

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


PRE_EXISTING_TEST = '''package app

import "testing"

func TestAnswer(t *testing.T) {
	if Answer() != 42 {
		t.Fatal("Answer must be 42")
	}
}
'''


class PreExistingRedUnitsTest(GitCase):
    """A unit declared `pre_existing_red: true` (polyforge#59): its
    reproduction test already exists and is red before the change, so it
    reports no test_commit — the unit resolves on its implementation
    commit alone and no test-first shape is asked of it. Expected values
    come from the declaration's meaning (a shape whose red evidence the
    engine replays on the tree of the change's base), never from the
    implementation; the ordinary unit keeps both commits required, and a
    declaration that also reports a test_commit is refused — one shape a
    unit."""

    def world(self, base: Dict[str, str]) -> Repo:
        r = Repo(os.path.join(self.tmp, "repos", "app"), base)
        r.git("update-ref", "refs/remotes/origin/main", "HEAD")
        return r

    def decl(self, ic: str, **kw: object) -> Dict[str, object]:
        d: Dict[str, object] = {"id": "u1", "pre_existing_red": True, "test_commit": "",
                                "impl_commit": ic, "run": None, "marker": ""}
        d.update(kw)
        return d

    def test_the_declaration_resolves_without_a_test_commit(self) -> None:
        # the reproduction test exists at the base and is red; the change is the fix alone
        r = self.world({"app_test.go": PRE_EXISTING_TEST, "app.go": "package app\n"})
        ic = r.commit("the fix", {"app.go": "package app\n\nfunc Answer() int { return 42 }\n"})
        resolved = facts.units_of(os.path.join(self.tmp, "repos"), ["app"], [self.decl(ic)])
        u = resolved[0]
        self.assertEqual(u.why, "", u.why)
        self.assertEqual((u.pre_existing_red, u.repo, u.test_commit, u.impl_commit, u.test_files),
                         (True, "app", "", ic, []))
        self.assertEqual(u.as_dict()["pre_existing_red"], True)
        # the declaration is part of the shape the reader parses: read_units takes it
        p = os.path.join(self.tmp, "units.json")
        with open(p, "w") as f:
            f.write(json.dumps([{ "id": "u1", "pre_existing_red": True, "impl_commit": ic,
                                  "run": {"repo": "app", "cwd": ".", "argv": ["go", "test", "./..."]},
                                  "marker": "^ok"}]))
        units = facts.read_units(p)
        self.assertEqual((units[0]["pre_existing_red"], units[0]["test_commit"], units[0]["impl_commit"]),
                         (True, "", ic))

    def test_a_pre_existing_red_unit_reports_no_test_commit(self) -> None:
        r = self.world({"app.go": "package app\n"})
        tc = r.commit("a test commit", {"app_test.go": "package app\n"})
        ic = r.commit("the fix", {"app.go": "package app\n\nfunc Answer() int { return 42 }\n"})
        resolved = facts.units_of(os.path.join(self.tmp, "repos"), ["app"], [self.decl(ic, test_commit=tc)])
        self.assertIn("reports no test_commit", resolved[0].why)

    def test_the_implementation_commit_alone_locates_the_unit(self) -> None:
        r = self.world({"app.go": "package app\n"})
        ic = r.commit("the fix", {"app.go": "package app\n\nvar X = 1\n"})
        resolved = facts.units_of(os.path.join(self.tmp, "repos"), ["app"],
                                  [self.decl("0000000"), self.decl(ic, id="u2")])
        self.assertIn("no commit of one task repo", resolved[0].why)
        self.assertEqual(resolved[1].why, "", resolved[1].why)
        self.assertEqual(resolved[1].repo, "app")

    def test_the_ordinary_shape_still_requires_both_commits(self) -> None:
        r = self.world({"app.go": "package app\n"})
        ic = r.commit("a commit", {"app.go": "package app\n\nvar X = 1\n"})
        resolved = facts.units_of(os.path.join(self.tmp, "repos"), ["app"],
                                  [{"id": "u1", "pre_existing_red": False, "test_commit": "", "impl_commit": ic,
                                    "run": None, "marker": ""}])
        self.assertIn("test_commit and impl_commit are both required", resolved[0].why)

    def test_a_pre_existing_red_commit_must_be_in_head(self) -> None:
        r = self.world({"app.go": "package app\n"})
        ic = r.commit("the fix", {"app.go": "package app\n\nvar X = 1\n"})
        r.git("checkout", "-q", "-b", "side")
        side = r.commit("elsewhere", {"other.go": "package app\n"})
        r.git("checkout", "-q", "main")
        repo = facts.Repo(os.path.join(self.tmp, "repos"), "app")
        self.assertEqual(repo.check_pre_existing_commits(ic), "")
        self.assertIn("not in HEAD's history", repo.check_pre_existing_commits(side))


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


# --- the ticket's decided rulings (## 已定) against the plan ----------

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)

# The ticket's verbatim content, as the daemon materializes it into
# runtime-state/work-item.json: one real `## 已定` section (three items)
# beside the shapes that must NOT open one — prose mentioning the word,
# a mid-line `## 已定`, a `### 已定` heading with its own list.
DECIDED_CONTENT = (
    "## 背景\n\n"
    "正文里出现「已定」两个字，行中间的 ## 已定 文本，都不算章节。\n\n"
    "### 已定\n\n"
    "- 三级标题不是已定章节，这条不作数。\n\n"
    "## 已定\n\n"
    "- 甲：回答常数由配置给出。\n"
    "- 乙：错误码不变。\n"
    "- 丙：文档同步交给 review。\n"
)

# Two sections opening `## 已定`: the numbering runs across them.
DECIDED_TWO_SECTIONS = (
    "## 已定\n\n- 甲：回答常数由配置给出。\n\n"
    "## 已定（补充）\n\n- 乙：错误码不变。\n- 丙：文档同步交给 review。\n"
)

# No `## 已定` section at all: the prose and the mid-line mention and
# the `### 已定` heading are not one.
DECIDED_NO_SECTION = (
    "## 背景\n\n"
    "这个票的正文提到已定二字，但只是叙述：裁定要写进专门的已定章节才作数。\n"
    "行中间的 ## 已定 不算章节开头。\n\n"
    "### 已定\n\n- 三级标题不算。\n"
)

DECIDED_PLAN = (
    "## Units\n\n"
    "**u1** — the answer.\n"
    "Touched files: `app.go` (write)\n"
    "Coupled obligations: none — a private constant no document lists\n"
)


def decided_map(*lines: str) -> str:
    """The plan above with a `## 已定对照` section of the given lines."""
    return DECIDED_PLAN + "\n## 已定对照\n\n" + "\n".join(lines) + "\n"


def decided_capsule(tmp: str, name: str, content: Optional[str], plan: str,
                    units: Optional[List[Dict[str, Any]]] = None,
                    raw_work_item: Optional[str] = None) -> str:
    """A capsule whose repos/app changed exactly the one file the plan
    declares (so the diff side of plan-conformance is conformant and
    only the decided mapping drives the exit), with the work item the
    daemon materializes: content None = the file absent, raw_work_item
    given = the file's own bytes (a corrupt-file case)."""
    cap = os.path.join(tmp, name)
    repo = Repo(os.path.join(cap, "repos", "app"), {"app.go": "package app\n"})
    repo.git("update-ref", "refs/remotes/origin/main", repo.head())
    repo.commit("the change", {"app.go": "package app\n\nvar Answer = 42\n"})
    files: Dict[str, str] = {
        "capsule.json": json.dumps({"work_item_id": "wi_221", "bundle_name": "develop", "repos": {"app": "app"}}),
        "artifacts/plan.md": plan,
    }
    if raw_work_item is not None:
        files["runtime-state/work-item.json"] = raw_work_item
    elif content is not None:
        files["runtime-state/work-item.json"] = json.dumps({"content": content})
    if units is not None:
        files["artifacts/build/units.json"] = json.dumps(units)
    write(cap, files)
    return cap


class PlanDecidedTest(GitCase):
    maxDiff = None

    def plan_conformance(self, cap: str) -> Tuple[int, Dict[str, Any]]:
        """One facts.py plan-conformance run on the capsule; the result
        file must carry the run's own exit code."""
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        out = os.path.join(self.tmp, "decided-res.json")
        argv = [sys.executable, "-B", os.path.join(SCRIPTS, "facts.py"),
                "plan-conformance", "--capsule", cap, "--json", "--out", out]
        p = subprocess.run(argv, cwd=cap, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
        self.assertEqual(p.returncode, res["exit_code"], "the result file carries the run's own exit code")
        return p.returncode, res

    def test_decided_items_read_only_the_sections(self) -> None:
        items = facts.decided_items(DECIDED_CONTENT)
        self.assertEqual([i["n"] for i in items], [1, 2, 3],
                         "prose, a mid-line mention and a ### heading open no section")
        self.assertEqual(items[2], {"n": 3, "text": "丙：文档同步交给 review。"})
        self.assertEqual(facts.decided_items(DECIDED_NO_SECTION), [], "no section, no ruling")

    def test_paragraph_counts_as_one_item(self) -> None:
        items = facts.decided_items("## 已定\n\n甲：回答常数由配置给出。\n\n- 乙：错误码不变。\n")
        self.assertEqual([i["n"] for i in items], [1, 2], "a prose ruling is one item, so it cannot slip past")
        self.assertEqual(items[0]["text"], "甲：回答常数由配置给出。")
        # and it is judged like any other: mapped, it passes
        cap = decided_capsule(self.tmp, "decided-para", "## 已定\n\n甲：回答常数由配置给出。\n\n- 乙：错误码不变。\n",
                              decided_map("- 已定 1: u1", "- 已定 2: u1"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])

    def test_three_decided_one_unmapped_is_red_and_named(self) -> None:
        cap = decided_capsule(self.tmp, "decided-red", DECIDED_CONTENT,
                              decided_map("- 已定 1: u1", "- 已定 2: u1"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("已定 3", res["summary"], "the missing ruling is named by its number")
        self.assertIn("丙：文档同步交给 review。", res["summary"], "the missing ruling is named by its text")
        d = res["details"]
        self.assertEqual([i["n"] for i in d["decided"]], [1, 2, 3])
        unmapped = d["unmapped_decided"]
        self.assertEqual(len(unmapped), 1, res["summary"])
        self.assertEqual(unmapped[0]["n"], 3)
        self.assertEqual(unmapped[0]["text"], "丙：文档同步交给 review。")
        self.assertTrue(unmapped[0]["why"], "why the ruling is unmapped")

    def test_unknown_unit_id_is_red(self) -> None:
        cap = decided_capsule(self.tmp, "decided-unknown", DECIDED_CONTENT,
                              decided_map("- 已定 1: u1", "- 已定 2: u1", "- 已定 3: u9"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("u9", res["summary"], "the unknown id is named")
        unmapped = res["details"]["unmapped_decided"]
        self.assertEqual([u["n"] for u in unmapped], [3])

    def test_none_without_a_reason_is_red(self) -> None:
        cap = decided_capsule(self.tmp, "decided-none", DECIDED_CONTENT,
                              decided_map("- 已定 1: u1", "- 已定 2: u1", "- 已定 3: none"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        unmapped = res["details"]["unmapped_decided"]
        self.assertEqual([u["n"] for u in unmapped], [3])

    def test_two_decided_sections_number_across(self) -> None:
        cap = decided_capsule(self.tmp, "decided-two", DECIDED_TWO_SECTIONS,
                              decided_map("- 已定 1: u1", "- 已定 3: u1"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        d = res["details"]
        self.assertEqual([i["n"] for i in d["decided"]], [1, 2, 3], "the numbering runs across the sections")
        self.assertEqual([u["n"] for u in d["unmapped_decided"]], [2])
        self.assertIn("乙", res["summary"])

    def test_all_mapped_passes(self) -> None:
        cap = decided_capsule(self.tmp, "decided-all", DECIDED_CONTENT,
                              decided_map("- 已定 1: u1", "- 已定 2: u1", "- 已定 3: none — 只改文档，无单元"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"].get("unmapped_decided"), [])
        self.assertIn("3 decided ruling(s), all mapped", res["summary"])

    def test_no_decided_section_needs_no_mapping(self) -> None:
        cap = decided_capsule(self.tmp, "decided-no-section", DECIDED_NO_SECTION, DECIDED_PLAN)
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        d = res["details"]
        self.assertEqual(d.get("decided"), [])
        self.assertEqual(d.get("unmapped_decided"), [])

    def test_empty_section_and_absent_work_item(self) -> None:
        cap = decided_capsule(self.tmp, "decided-empty", "## 已定\n\n## 背景\n\n- 不在已定节的条目。\n", DECIDED_PLAN)
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"].get("decided"), [], "an empty section carries no ruling")
        cap = decided_capsule(self.tmp, "decided-absent", None, DECIDED_PLAN)
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"].get("work_item"), "absent", "a work item the daemon has not materialized reads as no decided ruling")
        self.assertEqual(res["details"].get("unmapped_decided"), [])

    def test_corrupt_work_item_is_exit_two(self) -> None:
        cap = decided_capsule(self.tmp, "decided-corrupt", "", DECIDED_PLAN, raw_work_item="{not json")
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 2, "a corrupt work item is not judged, never a silent pass")
        self.assertIn("not judged", res["summary"])

    def test_decided_targets_prefer_units_json(self) -> None:
        self.assertEqual(facts.decided_targets([{"id": "u1"}], DECIDED_PLAN), ["u1"],
                         "with units.json, its own ids are the targets")
        self.assertEqual(facts.decided_targets([], DECIDED_PLAN), ["u1"],
                         "without units.json, the plan's bold ## Units ids are")
        self.assertEqual(facts.decided_targets([], "# no units section\n"), [])
        # with units.json present, a bold id it does not carry is not a target
        decls = [{"id": "u2", "test_commit": "0" * 40, "impl_commit": "0" * 40,
                  "run": {"repo": "app", "cwd": ".", "argv": ["true"]}, "marker": "^x"}]
        cap = decided_capsule(self.tmp, "decided-targets", DECIDED_CONTENT,
                              decided_map("- 已定 1: u1", "- 已定 2: u1", "- 已定 3: u2"), units=decls)
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"] + " — u1 is bold in the plan but no id of units.json")
        self.assertEqual([u["n"] for u in res["details"]["unmapped_decided"]], [1, 2])


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


class NotRunnableRuleTest(unittest.TestCase):
    """The repository-declared not-runnable rules (polyforge#50 AC5): a
    replay's classification hears the `not_runnable_rule=` lines of the
    conf governing its cwd — a repository's own toolchain text is the
    repository's to declare, not the lane script's to hardcode. Expected
    values are derived from the declared pattern text itself and the Go
    table of the generic rules, independent of the implementation."""

    def test_declared_rule_classifies_not_runnable(self) -> None:
        rules = [("demo", "custom-boom", r"^BOOM ")]
        got = facts.replay_verdict(1, False, "noise\nBOOM from the build\n", rules)
        self.assertEqual(got, ("not_runnable", {"toolchain": "demo", "rule": "custom-boom", "line": "BOOM from the build"}))

    def test_undeclared_pattern_stays_red(self) -> None:
        rules = [("demo", "custom-boom", r"^BOOM ")]
        self.assertEqual(facts.replay_verdict(1, False, "unrelated failure\n"), ("red", None))
        # a line the declared pattern does not anchor stays an assertion red
        self.assertEqual(facts.replay_verdict(1, False, "later BOOM from nowhere\n", rules), ("red", None))

    def test_generic_rules_still_apply(self) -> None:
        # the generic builtin rules keep classifying with no declaration
        self.assertEqual(facts.replay_verdict(1, False, "FAIL\tgithub.com/x/y [build failed]\n"),
                         ("not_runnable", {"toolchain": "go", "rule": "build-failed", "line": "FAIL\tgithub.com/x/y [build failed]"}))
        # a pi-web-shaped output is red unless the repository declares it
        self.assertEqual(facts.replay_verdict(1, False, "node_modules/ is missing: run npm ci first\n"), ("red", None))

    def test_rules_of_the_nearest_conf(self) -> None:
        import tempfile
        import shutil
        tree = tempfile.mkdtemp()
        try:
            os.makedirs(os.path.join(tree, ".polyforge"))
            os.makedirs(os.path.join(tree, "pi-web", ".polyforge"))
            with open(os.path.join(tree, ".polyforge", "verify-scope.conf"), "w") as f:
                f.write("# comment\nnot_runnable_rule=root|R1|^ONE \nnot_runnable_rule=bad-line\n")
            with open(os.path.join(tree, "pi-web", ".polyforge", "verify-scope.conf"), "w") as f:
                f.write("not_runnable_rule=node|node-browser-preflight|^(?:node_modules/ is missing:|Playwright Chromium is missing: )\n")
            self.assertEqual(facts.not_runnable_rules_of(tree, "pi-web"),
                             [("node", "node-browser-preflight", r"^(?:node_modules/ is missing:|Playwright Chromium is missing: )")])
            # the root's cwd reads the root's conf; a malformed line is skipped
            self.assertEqual(facts.not_runnable_rules_of(tree, "."), [("root", "R1", "^ONE ")])
            # a cwd under the root reads the root's conf when no nearer one exists
            self.assertEqual(facts.not_runnable_rules_of(tree, "elsewhere"), [("root", "R1", "^ONE ")])
            # a tree with no conf anywhere declares nothing
            empty = tempfile.mkdtemp()
            try:
                self.assertEqual(facts.not_runnable_rules_of(empty, "."), [])
            finally:
                shutil.rmtree(empty)
        finally:
            shutil.rmtree(tree)

    def test_declared_rule_applies_after_base_setup_too(self) -> None:
        rules = [("demo", "custom-boom", r"^BOOM ")]
        self.assertEqual(facts.replay_verdict(1, False, "prefix\nBOOM after the setup\n", rules),
                         ("not_runnable", {"toolchain": "demo", "rule": "custom-boom", "line": "BOOM after the setup"}))


# --- polyforge#194: plan paths under a nested project root ---------------

def prefix_plan(touched: str, obligations: str = "none — fixture") -> str:
    """A develop plan with one unit declaring `touched` (its write paths)
    and `obligations`."""
    return ("# plan\n\n## Units\n\n1. **u1** — the page\n"
            "   - Touched files: %s\n"
            "   - Coupled obligations: %s\n" % (touched, obligations))


class PlanPrefixNormalizeTest(GitCase):
    """polyforge#194: a plan's subproject-relative path and the diff's
    repository-root path are one file; a relative path no single nested
    project root owns stays undeclared. Every case is a real git capsule
    run through the full `facts.py plan-conformance` entry, so the exit
    code and the result file are the observed behaviour, never a helper's
    return. Expected values are hand-derived from the ticket's AC1 and
    the repository's nested-project-root convention (a directory whose
    tracked `.polyforge/verify-scope.conf` gives it a root of its own),
    not from the implementation's output."""

    def capsule(self, name: str, base: Dict[str, str],
                change: Dict[str, Optional[str]], plan: str) -> str:
        """A capsule whose one commit changes `change` (None = delete)
        against `base`, with the plan at artifacts/plan.md."""
        cap = os.path.join(self.tmp, name)
        repo = Repo(os.path.join(cap, "repos", "polyforge"), base)
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        for p in change:
            if change[p] is None:
                repo.git("rm", "-q", p)
        write(repo.dir, {p: v for p, v in change.items() if v is not None})
        repo.git("add", "-A")
        repo.git("commit", "-q", "-m", "the change")
        write(cap, {"capsule.json": json.dumps({"work_item_id": "wi_194", "bundle_name": "develop",
                                                "repos": {"polyforge": "polyforge"}}),
                    "artifacts/plan.md": plan})
        return cap

    def plan_conformance(self, cap: str) -> Tuple[int, Dict[str, Any]]:
        """One facts.py plan-conformance run on the capsule; the result
        file must carry the run's own exit code."""
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        out = os.path.join(self.tmp, "prefix-result.json")
        argv = [sys.executable, "-B", os.path.join(SCRIPTS, "facts.py"),
                "plan-conformance", "--capsule", cap, "--json", "--out", out]
        p = subprocess.run(argv, cwd=cap, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        with open(out, encoding="utf-8") as f:
            res: Dict[str, Any] = json.load(f)
        self.assertEqual(p.returncode, res["exit_code"], "the result file carries the run's own exit code")
        return p.returncode, res

    def test_a_subproject_relative_declaration_declares_the_nested_file(self) -> None:
        cap = self.capsule(
            "prefix-ok",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n", "pi-web/app/page.tsx": "old\n"},
            {"pi-web/app/page.tsx": "new\n"},
            prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [])
        self.assertEqual(res["details"]["unchanged_writes"], [])

    def test_an_undeclared_nested_file_is_still_reported(self) -> None:
        cap = self.capsule(
            "prefix-negative",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n",
             "pi-web/app/page.tsx": "old\n", "pi-web/app/other.tsx": "old\n"},
            {"pi-web/app/page.tsx": "new\n", "pi-web/app/other.tsx": "new\n"},
            prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertEqual(res["details"]["undeclared"], ["pi-web/app/other.tsx"],
                         "only the truly undeclared file is named")

    def test_repository_root_and_repos_prefixed_paths_keep_their_meaning(self) -> None:
        cap = self.capsule(
            "prefix-explicit",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n", "pi-web/app/page.tsx": "old\n"},
            {"pi-web/app/page.tsx": "new\n"},
            prefix_plan("`pi-web/app/page.tsx` (write), `repos/polyforge/pi-web/app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [])
        self.assertEqual(res["details"]["unchanged_writes"], [])

    def test_a_similar_directory_is_no_nested_root(self) -> None:
        cap = self.capsule(
            "prefix-boundary",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n", "pi-webx/app/page.tsx": "old\n"},
            {"pi-webx/app/page.tsx": "new\n"},
            prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertEqual(res["details"]["undeclared"], ["pi-webx/app/page.tsx"],
                         "a directory sharing the root's prefix is no root")

    def test_a_repository_root_file_needs_no_root(self) -> None:
        cap = self.capsule("prefix-root-file", {"app/page.tsx": "old\n"},
                           {"app/page.tsx": "new\n"}, prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [])
        cap = self.capsule("prefix-no-root",
                           {"app/page.tsx": "old\n", "other/app/page.tsx": "old\n"},
                           {"other/app/page.tsx": "new\n"}, prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertEqual(res["details"]["undeclared"], ["other/app/page.tsx"],
                         "a directory without a conf is no root")

    def test_two_nested_roots_holding_the_same_path_keep_it_undeclared(self) -> None:
        base = {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n",
                "pi-web/app/page.tsx": "old\n",
                "admin/.polyforge/verify-scope.conf": "stack=npm\n",
                "admin/app/page.tsx": "old\n"}
        cap = self.capsule("prefix-ambiguous", base, {"pi-web/app/page.tsx": "new\n"},
                           prefix_plan("`app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertEqual(res["details"]["undeclared"], ["pi-web/app/page.tsx"],
                         "the unchanged sibling root makes the relative path ambiguous")
        cap = self.capsule("prefix-ambiguous-explicit", base, {"pi-web/app/page.tsx": "new\n"},
                           prefix_plan("`pi-web/app/page.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [])

    def test_a_deleted_nested_file_keeps_its_base_identity(self) -> None:
        cap = self.capsule(
            "prefix-deleted",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n", "pi-web/app/old.tsx": "old\n"},
            {"pi-web/app/old.tsx": None},
            prefix_plan("`app/old.tsx` (write)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [])

    def test_an_untracked_nested_root_is_no_root(self) -> None:
        cap = self.capsule(
            "prefix-untracked",
            {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n", "pi-web/app/page.tsx": "old\n"},
            {"pi-web/app/page.tsx": "new\n"},
            prefix_plan("`app/page.tsx` (write)"))
        write(os.path.join(cap, "repos", "polyforge"),
              {"admin/.polyforge/verify-scope.conf": "stack=npm\n", "admin/app/page.tsx": "old\n"})
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["undeclared"], [],
                         "only the committed trees name roots")

    def test_a_coupled_obligation_uses_the_same_identity(self) -> None:
        base = {"pi-web/.polyforge/verify-scope.conf": "stack=npm\n",
                "pi-web/app/page.tsx": "old\n", "pi-web/app/docs/page.md": "old\n"}
        cap = self.capsule("prefix-obligation", base,
                           {"pi-web/app/page.tsx": "new\n", "pi-web/app/docs/page.md": "new\n"},
                           prefix_plan("`app/page.tsx` (write)", "`app/docs/page.md` (the row)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["unchanged_obligations"], [])
        self.assertEqual(res["details"]["released"], [])
        cap = self.capsule("prefix-obligation-unchanged", base,
                           {"pi-web/app/page.tsx": "new\n"},
                           prefix_plan("`app/page.tsx` (write)", "`app/docs/page.md` (the row)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 1, res["summary"],
                         "an unchanged coupled obligation is judged red (conformant())")
        self.assertEqual(res["details"]["unchanged_obligations"], ["app/docs/page.md"])
        self.assertEqual(res["details"]["released"], [])
        cap = self.capsule("prefix-obligation-lapsed", base,
                           {"pi-web/app/docs/page.md": "new\n"},
                           prefix_plan("`app/page.tsx` (write)", "`app/docs/page.md` (the row)"))
        code, res = self.plan_conformance(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(len(res["details"]["released"]), 1)
        self.assertIn("app/docs/page.md", res["details"]["released"][0])


if __name__ == "__main__":
    unittest.main()
