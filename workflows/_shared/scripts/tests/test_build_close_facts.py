# carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/_shared/skills/review.md
"""The build close's own facts wiring: the command lines the develop
step's 3c and the shared build skill state — facts.py build with the
replay (review's own line, --isolation host included), facts.py
plan-conformance and facts.py carries — run exactly as written (the
$PF_PYTHON and $PF_SHARED_DIR placeholders resolved to this tree)
against real git fixture capsules. The three commit fixtures (an
unresolvable id, a merge-commit test_commit, a test_commit mixing
implementation files) and the two carries fixtures (a suite with no or
a malformed `carries:` declaration, one matching nothing it touched)
are judged red, naming the unit, or the repo, suite and reason; the
compliant fixture passes; an unusable units file is exit 2, and the
wiring states the close's own exit-2 rules — the two review lines
(build with the replay, plan-conformance) retried after one minute at
most twice and, still 2, disclosed in the hand-off, never judged
blocked, while carries keeps exit 2 no pass. Review's own pre-written
lines, extracted from the review skill the same way, agree with the
close: the build line is review's own, argv for argv, and the static
commit facts and carries conclusions agree on the same fixtures (one
facts.py implementation; only each side's replay record is never part
of the comparison)."""

import json
import os
import re
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Optional, Tuple

from pfstep.shellwords import shell_words

from .gitworld import GitCase, Repo, write
from .test_facts import CARRIES_SEED

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
SHARED = os.path.dirname(SCRIPTS)
WORKFLOWS = os.path.dirname(SHARED)
STEP_TEXT = os.path.join(WORKFLOWS, "develop", "steps", "build.md")
SKILL_TEXT = os.path.join(WORKFLOWS, "_shared", "skills", "build.md")
REVIEW_TEXT = os.path.join(WORKFLOWS, "_shared", "skills", "review.md")

FACTS_LINE = re.compile(
    r'`"\$PF_PYTHON" "\$PF_SHARED_DIR/scripts/facts\.py" (build|carries|plan-conformance|analysis)([^`]*)`',
    re.ASCII,
)


def flat(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return " ".join(f.read().split())


def facts_commands(path: str) -> Dict[str, List[List[str]]]:
    """The backticked facts.py command lines a workflow text carries,
    argv by argv ($PF_PYTHON this interpreter, $PF_SHARED_DIR this tree)."""
    out: Dict[str, List[List[str]]] = {"build": [], "carries": [], "plan-conformance": [], "analysis": []}
    with open(path, encoding="utf-8") as f:
        text = f.read()
    for m in FACTS_LINE.finditer(text):
        kind = m.group(1)
        line = '"%s" "%s" %s%s' % (sys.executable, os.path.join(SCRIPTS, "facts.py"), kind, m.group(2))
        words, simple = shell_words(line)
        if not simple:
            raise AssertionError("SYMPTOM: %s: a facts line is not one simple command: %s" % (path, m.group(0)))
        out[kind].append(words)
    return out


def one_line(path: str, kind: str, replayed: Optional[bool] = None) -> List[str]:
    """The one wiring line of `kind' a workflow text carries (a build
    line filtered by whether it replays); the close's own build wiring
    is review's line — the one with the replay."""
    got = [a for a in facts_commands(path)[kind]
           if replayed is None or (("--replay" in a) == replayed)]
    if len(got) != 1:
        raise AssertionError(
            "SYMPTOM: %s must state exactly one `facts.py %s` wiring line%s — the close runs the engine's own checks before review does"
            % (path, kind, "" if replayed is None else (" (no replay)" if not replayed else " (with --replay)")))
    return got[0]


def static_units(res: Dict[str, Any]) -> List[Dict[str, Any]]:
    """The static commit facts of a build result: each unit as both the
    close's own line and review's report it — the replay record and its
    errors are never part of the comparison. An unjudged result (exit 2)
    carries no units on either side — that emptiness is the agreement."""
    return [{k: v for k, v in u.items() if k not in ("replay", "replay_error")}
            for u in res["details"].get("units", [])]


class BuildCloseFactsTest(GitCase):
    maxDiff = None

    # --- the wiring ------------------------------------------------------

    def test_the_close_wiring_lives_in_the_step_and_the_skill(self) -> None:
        for name, path in (("the develop step's 3c", STEP_TEXT), ("the shared build skill", SKILL_TEXT)):
            with self.subTest(name):
                b = one_line(path, "build", replayed=True)
                c = one_line(path, "carries")
                p = one_line(path, "plan-conformance")
                self.assertIn("--replay", b, "the close's own build line carries the replay — review's line, run before review does")
                self.assertIn("--isolation", b, "the replay runs where review's does")
                self.assertIn("host", b, "the replay runs on the host, never in a container the close did not plan")
                self.assertIn("--json", b)
                self.assertIn("artifacts/facts/build.json", b)
                self.assertEqual(b, one_line(REVIEW_TEXT, "build", replayed=True),
                                 "the close's build line is review's own — argv for argv, the same engine and the same replay")
                self.assertIn("--json", c)
                self.assertIn("artifacts/facts/carries.json", c)
                self.assertIn("--json", p)
                self.assertIn("artifacts/facts/plan-conformance.json", p)
                self.assertIn("wait one minute and run that line again, at most twice", flat(path),
                              "SYMPTOM: the wiring must state the exit-2 retry the close makes before it discloses")
                self.assertIn("never `blocked` for it", flat(path),
                              "SYMPTOM: an exit 2 that stayed 2 is disclosed in the hand-off, never judged blocked")

    # --- AC1: the fixtures the close must judge --------------------------

    def commit_fixture_capsule(self) -> str:
        """A capsule whose repos/app carries three units: u1 reports a
        test_commit no task repo holds, u2's test_commit is a merge
        commit, u3's test_commit mixes an implementation file in."""
        cap = self.new_dir("capsule%d" % len(os.listdir(self.tmp)))
        repo = Repo(os.path.join(cap, "repos", "app"), {"app.go": "package app\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps({"work_item_id": "wi_131", "bundle_name": "develop", "repos": {"app": "app"}})})
        impl = repo.commit("the unit's implementation", {"notes-impl.txt": "x\n"})
        repo.git("checkout", "-q", "-b", "side")
        side = repo.commit("side work", {"side.txt": "s\n"})
        repo.git("checkout", "-q", "main")
        repo.git("merge", "-q", "--no-ff", "-m", "u2's test commit (a merge)", side)
        merged = repo.head()
        impl2 = repo.commit("u2's implementation", {"impl2.txt": "x\n"})
        mixed = repo.commit("u3's test commit (a test and an implementation file)",
                            {"test/mixed_test.sh": "test -f mixed.txt\n", "mixed-impl.txt": "x\n"})
        impl3 = repo.commit("u3's implementation", {"mixed-impl.txt": "y\n"})
        write(cap, {"artifacts/build/units.json": json.dumps([
            {"id": "u1", "test_commit": "0" * 40, "impl_commit": impl,
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^ok"},
            {"id": "u2", "test_commit": merged, "impl_commit": impl2,
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^ok"},
            {"id": "u3", "test_commit": mixed, "impl_commit": impl3,
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/mixed_test.sh"]}, "marker": "^ok"},
        ])})
        return cap

    def compliant_capsule(self) -> str:
        """A capsule whose one unit is sound and red at its test commit
        (so the replay both the close and review make of it passes)."""
        cap = self.new_dir("capsule%d" % len(os.listdir(self.tmp)))
        repo = Repo(os.path.join(cap, "repos", "app"), {"app.go": "package app\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps({"work_item_id": "wi_131", "bundle_name": "develop", "repos": {"app": "app"}})})
        test = repo.commit("the unit's test commit", {"test/notes_test.sh": "test -f notes-impl.txt\n"})
        impl = repo.commit("the unit's implementation", {"notes-impl.txt": "x\n"})
        write(cap, {"artifacts/build/units.json": json.dumps([
            {"id": "u1", "test_commit": test, "impl_commit": impl,
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^ok"},
        ])})
        return cap

    def carries_capsule(self, change: Dict[str, str]) -> str:
        cap = self.new_dir("capsule%d" % len(os.listdir(self.tmp)))
        repo = Repo(os.path.join(cap, "repos", "app"), CARRIES_SEED)
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps({"work_item_id": "wi_131", "bundle_name": "develop", "repos": {"app": "app"}})})
        repo.commit("the build's change", change)
        return cap

    def unusable_units_capsule(self) -> str:
        cap = self.new_dir("capsule%d" % len(os.listdir(self.tmp)))
        repo = Repo(os.path.join(cap, "repos", "app"), {"app.go": "package app\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps({"work_item_id": "wi_131", "bundle_name": "develop", "repos": {"app": "app"}}),
                    "artifacts/build/units.json": "{not a JSON array"})
        return cap

    def run_line(self, cap: str, argv: List[str]) -> Tuple[int, Dict[str, Any]]:
        """One wiring line run as the step runs it: the capsule root as
        cwd (the line's --out is relative to it), no PF_/GIT_ env of the
        host's own step reaching the child."""
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        p = subprocess.run(argv, cwd=cap, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        res_path = os.path.join(cap, argv[argv.index("--out") + 1])
        with open(res_path, encoding="utf-8") as f:
            res = json.load(f)
        self.assertEqual(p.returncode, res["exit_code"], "the result file must carry the run's own exit code")
        return p.returncode, res

    def test_close_wiring_judges_the_commit_fixtures(self) -> None:
        line = one_line(STEP_TEXT, "build", replayed=True)
        code, res = self.run_line(self.commit_fixture_capsule(), line)
        self.assertEqual(code, 1, res["summary"])
        got = " ".join(res["summary"].split())
        for want in ("u1", "not both commits of one task repo",
                     "u2", "has 2 parents",
                     "u3", "touches non-test paths"):
            self.assertIn(want, got, "the close must name the unit and its reason")
        by_id = {u["id"]: u for u in res["details"]["units"]}
        self.assertIn("unresolved", by_id["u1"], "the unresolvable unit is named with its reason")
        self.assertIn("commits", by_id["u2"], "the merge test_commit is named with its reason")
        self.assertIn("mixed-impl.txt", by_id["u3"]["commits"], "the mixed-in implementation file is named")

    def test_close_wiring_judges_the_carries_fixtures(self) -> None:
        line = one_line(STEP_TEXT, "carries")
        panel = "export const panel = 2;\n"
        cases = [
            ("a suite with no carries declaration",
             {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": 'import "node:assert";\n'},
             ["e2e suite repos/app/web/e2e/panel.mjs declares no `carries:` in its opening comment block"]),
            ("a suite with a malformed declaration",
             {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": '// carries:\nimport "node:assert";\n'},
             ["e2e suite repos/app/web/e2e/panel.mjs has a malformed `carries:` declaration"]),
            ("a suite whose carries match nothing it touched",
             {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'},
             ["e2e suite repos/app/web/e2e/panel.mjs",
              "matches none of the non-test paths this build touched under web/: lib/panel.ts"]),
            ("a compliant suite",
             {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": '// carries: lib/panel*\nimport "node:assert";\n'},
             []),
        ]
        for name, change, wants in cases:
            with self.subTest(name):
                code, res = self.run_line(self.carries_capsule(change), line)
                got = " ".join(" ".join(res["details"].get("problems") or []).split())
                if wants:
                    self.assertEqual(code, 1, res["summary"])
                    self.assertIn("repos/app", got, "the carries problem names the repo")
                    for w in wants:
                        self.assertIn(w, got, "the carries problem names the suite and its reason")
                else:
                    self.assertEqual(code, 0, res["summary"])
                    self.assertEqual(res["details"].get("problems"), [])

    def test_exit_two_is_not_a_pass(self) -> None:
        # the split the close now states: carries keeps exit 2 no pass;
        # the two review lines are retried, then disclosed, never blocked
        self.assertIn("exit 2 is no pass either (fix the input it names; a real machine blocker is `blocked`)",
                      flat(STEP_TEXT), "SYMPTOM: the carries line keeps the no-pass rule")
        self.assertIn("exit 2 on `carries` is no pass either", flat(SKILL_TEXT),
                      "SYMPTOM: the carries line keeps the no-pass rule")
        cap = self.unusable_units_capsule()
        for kind, replayed in (("build", True), ("carries", None)):
            with self.subTest(kind):
                line = one_line(STEP_TEXT, kind, replayed=replayed)
                code, res = self.run_line(cap, line)
                self.assertEqual(code, 2, "an unusable units file is exit 2 — never a pass the close may hand off on")

    # --- AC2: the close and review agree (one line, one engine) ----------

    def test_close_and_review_agree_on_the_static_facts(self) -> None:
        close_build = one_line(STEP_TEXT, "build", replayed=True)
        close_carries = one_line(STEP_TEXT, "carries")
        review_build = one_line(REVIEW_TEXT, "build", replayed=True)
        review_carries = one_line(REVIEW_TEXT, "carries")
        # one implementation, one line: the close's build wiring is
        # review's own argv for argv, and both sides' carries lines run
        # the same facts.py of this tree
        self.assertEqual(close_build, review_build,
                         "the close's build line is review's own — argv for argv, the same engine and the same replay")
        self.assertEqual(close_carries[1], review_carries[1], "both sides must run the same facts.py script")
        self.assertEqual(close_carries[2], review_carries[2], "both sides must run the same fact kind")
        for name, cap in (("the three commit fixtures", self.commit_fixture_capsule()),
                          ("the compliant fixture", self.compliant_capsule()),
                          ("the unusable units file", self.unusable_units_capsule())):
            with self.subTest(name + " — build"):
                c_code, c_res = self.run_line(cap, close_build)
                r_code, r_res = self.run_line(cap, review_build)
                # the exits agree too: the bad fixtures never reach the
                # replay's own verdicts (the structure refusal stops the run
                # first), and the unusable units file is exit 2 on both sides
                self.assertEqual(c_code, r_code)
                self.assertEqual(static_units(c_res), static_units(r_res),
                                 "the static commit facts must agree between the close and review")
            with self.subTest(name + " — carries"):
                c_code, c_res = self.run_line(cap, close_carries)
                r_code, r_res = self.run_line(cap, review_carries)
                self.assertEqual(c_code, r_code)
                self.assertEqual(c_res["details"].get("problems"), r_res["details"].get("problems"),
                                 "the carries conclusions must agree between the close and review")
        panel = "export const panel = 2;\n"
        for name, change in (
                ("a suite with no carries declaration", {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": 'import "node:assert";\n'}),
                ("a suite whose carries match nothing it touched", {"web/lib/panel.ts": panel, "web/e2e/panel.mjs": '// carries: lib/other*\nimport "node:assert";\n'})):
            with self.subTest(name + " — carries"):
                cap = self.carries_capsule(change)
                c_code, c_res = self.run_line(cap, close_carries)
                r_code, r_res = self.run_line(cap, review_carries)
                self.assertEqual(c_code, r_code)
                self.assertEqual(c_res["details"].get("problems"), r_res["details"].get("problems"))


if __name__ == "__main__":
    unittest.main()
