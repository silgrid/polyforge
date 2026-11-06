"""verify.py's light tier: the engine is asked with --light in the repo and
in every nested engine root the diff reaches (its commands run behind
``cd <root> &&``); a failing unit is re-run once — a suite of an aggregate
e2e command through the conf's declared re-run template — and a unit still
red is run on the baseline: the default branch's commit the tested tree
carries (merge-base HEAD origin/<default>). The aggregate runner's summary
proves the command complete; each failing unit carries its suite's
``carries:`` globs, made repository-relative. A nested root whose own light
report falls back to the full tier makes the run full. The Go side's twins
are verifyrun/w403_light_tier_test.go, w403_light_replay_test.go and
w403_review_test.go (their Run-level tests)."""

import os
import shutil
import tempfile
import unittest
from typing import Any, Dict, List, Mapping, Optional, Tuple

from .fakebox import FakeRuntime
from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    engine_args,
    fake_engine,
    git_t,
    report,
    run_verify,
    write_files,
)

HAVE_NODE = shutil.which("node") is not None
HAVE_PYTHON3 = shutil.which("python3") is not None

TESTDATA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata")
# the repository's own scope engine (workflows/_shared/scripts/verify-scope)
REAL_ENGINE = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "verify-scope"))

# A fake aggregate e2e runner (the e2e/all.mjs output protocol, its
# `e2e/all: N suite(s)` announcement first): suites s1.mjs and s2.mjs;
# `<SUITE>_BROKEN` in the cwd fails a suite on every run, `<SUITE>_FLAKY`
# fails it until $FLAKY_STATE exists.
W403_RUNNER = r"""#!/bin/sh
[ "$1" = "--" ] && shift
suites="$*"
[ -n "$suites" ] || suites="s1.mjs s2.mjs"
echo "e2e/all: $(echo $suites | wc -w | tr -d ' ') suite(s), browser pool 2, serial lane 1"
failed=0; passed=0; total=0; fails=""; summary=""
for s in $suites; do
  s=${s#e2e/}
  total=$((total+1))
  up=$(echo "${s%.mjs}" | tr a-z A-Z)
  bad=0
  [ -f "${up}_BROKEN" ] && bad=1
  if [ -f "${up}_FLAKY" ] && [ ! -f "$FLAKY_STATE" ]; then : >"$FLAKY_STATE"; bad=1; fi
  if [ "$bad" = 1 ]; then
    echo "===== FAIL $s (1s) ====="; echo "FAIL: $up boom"; echo "  expected x"
    failed=$((failed+1)); fails="$fails $s"; summary="$summary
FAIL $s 1s"
  else
    echo "===== PASS $s (1s) ====="; echo "PASS: $up ok"
    passed=$((passed+1)); summary="$summary
PASS $s 1s"
  fi
done
if [ "$failed" -gt 0 ]; then
  echo; echo "===== failures ====="
  for s in $fails; do
    up=$(echo "${s%.mjs}" | tr a-z A-Z)
    echo "FAIL e2e/$s: $up boom — expected x"
    echo "host loadavg 1m/5m/15m: 1 / 1 / 1"
    echo "re-run: sh e2e/all.sh -- $s"
  done
fi
echo; echo "===== summary ====="
echo "$summary" | sed '/^$/d'
echo "$passed/$total passed in 2s"
[ "$failed" -eq 0 ]
"""

# The fake aggregate runner of the replay: the whole run announces the suite
# count first, as e2e/all.mjs does (the recorded files keep only the failures
# and summary sections; the count is their tally's total), then prints the
# recorded output ($REPLAY_LOG) and fails; a one-suite run (the template's
# re-run) passes or fails as $REPLAY_RERUN says at HEAD (HEAD_MARK present)
# and as $REPLAY_BASE says on the baseline, failing with the recorded
# failure lines of that suite.
W403_REPLAY_RUNNER = r"""#!/bin/sh
[ "$1" = "--" ] && shift
if [ $# -eq 0 ]; then
  echo "e2e/all: $(sed -n 's|^[0-9]*/\([0-9]*\) passed.*|\1|p' "$REPLAY_LOG") suite(s), browser pool 4, serial lane 1, next start (build once)"
  cat "$REPLAY_LOG"; exit 1
fi
s=${1#e2e/}
echo "e2e/all: 1 suite(s), browser pool 4, serial lane 1, next start (build once)"
if [ -f HEAD_MARK ]; then mode=$REPLAY_RERUN; else mode=$REPLAY_BASE; fi
if [ "$mode" = pass ]; then
  printf '===== PASS %s (1s) =====\nPASS: replayed\n\n===== summary =====\nPASS %s 1s\n1/1 passed in 1s\n' "$s" "$s"
  exit 0
fi
printf '===== FAIL %s (1s) =====\nFAIL: replayed\n\n===== failures =====\n' "$s"
grep "^FAIL e2e/$s: " "$REPLAY_LOG"
printf 'host loadavg 1m/5m/15m: 1 / 1 / 1\nre-run: npm run test:e2e:all -- %s\n\n===== summary =====\nFAIL %s 1s\n0/1 passed in 1s\n' "$s" "$s"
exit 1
"""

# The replayed suites' carries: headers on 2026-11-08.
W403_REPLAY_HEADERS = {
    "polyforge-messages.mjs": "// carries: components/Polyforge*, components/polyforge-*, hooks/usePolyforge*, lib/polyforge*,"
                              " app/api/polyforge/*\n",
    "run.mjs": "// carries: components/ChatWindow*, components/ChatInput*, components/MessageView*, hooks/useAgentSession*,"
               " lib/agent-*, lib/stream-client*, lib/session-*, app/api/agent/*, app/api/sessions/*\n"
               "// carries: app/page.tsx, app/layout.tsx, app/globals.css\n",
}


def w403_repo(extra: Optional[Mapping[str, str]] = None) -> Dict[str, str]:
    """A repo whose web/ subtree is a nested engine root with the fake
    runner and two suites declaring what they carry."""
    files = {
        ".polyforge/verify-scope.conf": "stack=go\n",
        "web/.polyforge/verify-scope.conf": "stack=npm\n",
        "web/e2e/all.sh": W403_RUNNER,
        "web/e2e/s1.mjs": "// carries: lib/a*, components/A*\nconsole.log('s1');\n",
        "web/e2e/s2.mjs": "// carries: lib/b*\nconsole.log('s2');\n",
        "web/lib/a.ts": "export const a = 1;\n",
        "web/lib/b.ts": "export const b = 1;\n",
    }
    files.update(extra or {})
    return files


def w403_report(mode: str, paths: List[str], *cmds: Dict[str, Any], **extra: Any) -> Dict[str, Any]:
    """An engine report with the given mode, paths and commands."""
    return report(mode, *cmds, paths=paths, **extra)


def w403_rerun(name: str, phase: str, cmd: str, tmpl: str) -> Dict[str, Any]:
    """cmd_of with a declared re-run template."""
    return cmd_of(name, phase, cmd, rerun=tmpl)


def doc_of(r: Run) -> Dict[str, Any]:
    if r.doc is None:
        raise AssertionError("no result.json (exit %d): %s %s" % (r.exit, r.result, r.stderr))
    return r.doc


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are needed")
class LightTierTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.n = 0

    def tearDown(self) -> None:
        self._td.cleanup()

    def fixture(self, repos: Mapping[str, Mapping[str, str]]) -> Fixture:
        self.n += 1
        root = os.path.join(self.root, "fx%d" % self.n)
        os.makedirs(root)
        return Fixture(root, repos)

    def run_light(self, f: Fixture, engine: str, env: Optional[Mapping[str, str]] = None) -> Run:
        """The light run over f (FLAKY_STATE in a fresh place); a fake
        container engine keeps the run away from this machine's docker (no
        repo here carries the Dockerfile)."""
        e = {"FLAKY_STATE": os.path.join(f.root, "flaky-state", "flaky")}
        os.makedirs(os.path.dirname(e["FLAKY_STATE"]), exist_ok=True)
        e.update(env or {})
        return run_verify(f.capsule, engine, env=e, runtime=FakeRuntime(os.path.join(f.root, "box-tmp")))

    def test_light_plans_the_repo_and_its_nested_root(self) -> None:
        """TestW403LightPlansTheRepoAndItsNestedRoot: the light tier asks the
        engine with --light in the repo and in the nested root the diff
        reaches, runs the nested commands behind `cd web &&` and proves the
        light tier."""
        f = self.fixture({"app": w403_repo()})
        f.commit_in_repo("app", {"web/lib/a.ts": "export const a = 2;\n"}, "feature")
        engine = fake_engine(f.root, {
            "app": w403_report("scoped", ["web/lib/a.ts"], cmd_of("fast-1", "fast", "echo ROOT-FAST")),
            "web": w403_report("scoped", ["lib/a.ts"], cmd_of("fast-1", "fast", "echo NESTED-FAST"),
                               cmd_of("scoped-1", "scoped", "sh e2e/all.sh e2e/s1.mjs")),
        })
        r = self.run_light(f, engine)
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "")
        args = "\n".join(engine_args(engine))
        for want in ("app --json --light --base", "web --json --light --base"):
            self.assertIn(want, args, "the engine must be asked %r" % want)
        self.assertEqual((doc["tier"]["requested"], doc["tier"]["effective"]), ("light", "light"), doc["tier"])
        c = r.check("app:web/scoped-1")
        self.assertEqual(c["status"], "pass", c)
        self.assertIn("`cd web && sh e2e/all.sh e2e/s1.mjs`", c.get("detail", ""), "the nested scoped command must run behind cd web")
        self.assertEqual(r.check("app:web/fast-1")["status"], "pass", "the nested fast gate must run")
        self.assertEqual(r.check("app:fast-1")["status"], "pass", "the repo's fast gate must run")
        self.assertEqual(doc["mechanical_verdict"], "pass", doc.get("fail_reason"))

    def test_light_nested_suite_reruns_once_and_passes_flaky(self) -> None:
        """TestW403LightNestedSuiteRerunsOnceAndPassesFlaky: a red suite of
        the nested runner command is re-run alone once (the runner's printed
        re-run behind cd web), passes, and the runner's summary proves the
        command complete — no whole re-run."""
        f = self.fixture({"app": w403_repo({"web/S1_FLAKY": "x\n"})})
        f.commit_in_repo("app", {"web/lib/a.ts": "export const a = 2;\n"}, "feature")
        engine = fake_engine(f.root, {
            "app": w403_report("scoped", ["web/lib/a.ts"]),
            "web": w403_report("scoped", ["lib/a.ts"], cmd_of("scoped-1", "scoped", "sh e2e/all.sh e2e/s1.mjs e2e/s2.mjs")),
        })
        r = self.run_light(f, engine)
        u = r.unit("e2e/s1.mjs")
        self.assertEqual((u["status"], u.get("file")), ("flaky", "web/e2e/s1.mjs"),
                         "the suite must pass flaky on its one re-run, located repo-relative: %r" % u)
        c = r.check("app:web/scoped-1")
        self.assertEqual(c["status"], "pass", c)
        self.assertIn("complete:", c.get("detail", ""), "the runner's summary must prove the command complete")
        self.assertNotIn("re-run whole", c.get("detail", ""), "no whole re-run")
        self.assertEqual(doc_of(r)["mechanical_verdict"], "pass", doc_of(r).get("fail_reason"))

    def test_template_reruns_aggregate_suite_at_the_synced_default_branch(self) -> None:
        """TestW403TemplateRerunsAggregateSuiteAtTheSyncedDefaultBranch: the
        planned aggregate command (a `cd … &&` list) declares a re-run
        template. The branch synced the default branch at main 2, which
        breaks the suite; the default branch then moved on to main 3, which
        the verification does not take in. The red suite fails its one
        re-run and fails the same way on main 2 — merge-base HEAD
        origin/main — so it is pre-existing: never judged on a green tag.
        The check does not fail: the light tier needs no skip form, the
        summary proves completeness."""
        f = self.fixture({"app": w403_repo()})
        seed, clone = f.seeds["app"], f.repo("app")
        git_t(seed, "tag", "green/20261101-1")  # the base, where the suite passes: never the light baseline
        git_t(seed, "push", "-q", "origin", "--tags")
        f.commit_in_repo("app", {"web/lib/a.ts": "export const a = 2;\n"}, "feature")
        synced = f.advance_origin("app", {"web/S1_BROKEN": "x\n"}, "main 2: breaks s1")
        # the lane's sync: main 2 merged into the branch
        git_t(clone, "fetch", "-q", "origin")
        git_t(clone, "merge", "-q", "--no-edit", "origin/main")
        # the default branch moves on between the sync and the verification
        f.advance_origin("app", {"web/lib/b.ts": "export const b = 2;\n"}, "main 3")
        agg = "cd web && sh e2e/all.sh"
        engine = fake_engine(f.root, {
            "app": w403_report("full", ["web/lib/a.ts"], w403_rerun("full-1", "full", agg, agg + " -- {unit}")),
        })
        r = self.run_light(f, engine)
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "")
        m = doc["merge_outcomes"].get("app") or {}
        self.assertEqual((m.get("outcome"), m.get("baseline_ref"), m.get("baseline")), ("none", "merge-base", synced),
                         "the baseline must be main 2, the default branch the tested tree carries: %r" % m)
        u = r.unit("e2e/s1.mjs")
        self.assertEqual((u["status"], u.get("file")), ("pre_existing", "web/e2e/s1.mjs"), u)
        self.assertIn(synced[:12], u.get("detail", ""), "the suite must be pre-existing on the synced default branch")
        self.assertIn("web/e2e/s1.mjs: e2e/s1.mjs", doc["pre_existing_failures"])
        c = r.check("app:full-1")
        self.assertEqual(c["status"], "pre_existing", c)
        self.assertIn("complete:", c.get("detail", ""), "the aggregate check must be proven complete by its summary")
        self.assertEqual(doc["mechanical_verdict"], "pass", doc.get("fail_reason"))
        self.assertEqual(doc["tier"]["effective"], "full", "a light request the engine answered full proves the full tier")

    def test_light_red_on_head_only_stays_failed_with_carries(self) -> None:
        """TestW403LightRedOnHeadOnlyStaysFailedWithCarries: the baseline is
        the merge-base, origin/main's head; the suite passes there — the red
        came only with the change — so it stays a failure, after exactly
        one re-run, and the unit carries its suite's globs
        repository-relative."""
        f = self.fixture({"app": w403_repo()})
        main = git_t(f.seeds["app"], "rev-parse", "HEAD")
        f.commit_in_repo("app", {"web/S1_BROKEN": "x\n", "web/lib/a.ts": "export const a = 2;\n"}, "feature")
        agg = "cd web && sh e2e/all.sh"
        engine = fake_engine(f.root, {
            "app": w403_report("full", ["web/S1_BROKEN", "web/lib/a.ts"], w403_rerun("full-1", "full", agg, agg + " -- {unit}")),
        })
        r = self.run_light(f, engine)
        doc = doc_of(r)
        m = doc["merge_outcomes"].get("app") or {}
        self.assertEqual((m.get("outcome"), m.get("baseline_ref"), m.get("baseline")), ("none", "merge-base", main),
                         "the baseline is the merge-base, origin/main's head: %r" % m)
        u = r.unit("e2e/s1.mjs")
        self.assertEqual(u["status"], "fail", u)
        self.assertIn("still failing on 1 re-run(s)", u.get("detail", ""))
        self.assertIn("passes on the base", u.get("detail", ""))
        self.assertEqual(u.get("carries"), ["web/lib/a*", "web/components/A*"])
        self.assertEqual(doc["mechanical_verdict"], "fail")

    def test_template_never_replaces_the_race_guard(self) -> None:
        """TestW403TemplateNeverReplacesTheRaceGuard (w403_review_test.go): a
        failing Go test in a run that reported a data race stays a failure
        when its command declares a re-run template without -race (the
        template is not run), while a template carrying -race may re-run it."""
        out = ("==================\nWARNING: DATA RACE\n--- FAIL: TestRacy (0.00s)\n"
               "    testing.go:1: race detected during execution of test\nFAIL\nFAIL\texample.com/app\t0.1s\n")
        for name, tmpl, ran_want in (
            ("no -race", "touch {unit}.RAN; go test -run {unit} ./...", False),
            ("with -race", "touch {unit}.RAN; go test -race -run {unit} ./...", True),
        ):
            with self.subTest(name):
                f = self.fixture({"app": {"a.txt": "base\n"}})
                f.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
                f.commit_in_repo("app", {"r_test.go": "package app\n\nfunc TestRacy(t *testing.T) {}\n"}, "racy")
                script = os.path.join(f.root, "out", "out.txt")
                write_files(os.path.dirname(script), {"out.txt": out})
                eng = fake_engine(f.root, {"app": report("full", w403_rerun("unit", "full", "cat " + script + "; exit 1", tmpl))})
                r = run_verify(f.capsule, eng, runtime=FakeRuntime(os.path.join(f.root, "box-tmp")))
                u = r.unit("TestRacy")
                ran = os.path.exists(os.path.join(f.capsule, "repos", "app", "TestRacy.RAN"))
                self.assertEqual(ran, ran_want, "template run = %s, want %s (unit %r)" % (ran, ran_want, u))
                if not ran_want:
                    self.assertEqual(u["status"], "fail", u)
                    self.assertIn("data race", u.get("detail", ""), "a race red is never proven flaky by a template without -race")

    # -- a nested root's full fallback (the acceptance criterion; no Go twin)

    def pi_web_fixture(self, root_conf: str, extra: Optional[Mapping[str, str]] = None) -> Fixture:
        """A repo whose pi-web/ is a nested engine root (its own tracked conf,
        stack=npm, and a package.json with lint/test scripts); the change
        touches pi-web/package.json."""
        files = {
            ".polyforge/verify-scope.conf": root_conf,
            "pi-web/.polyforge/verify-scope.conf": "stack=npm\n",
            "pi-web/package.json": '{"name": "pi-web", "scripts": {"lint": "echo PI-LINT", "test": "echo PI-TEST"}}\n',
        }
        files.update(extra or {})
        f = self.fixture({"app": files})
        f.commit_in_repo("app", {"pi-web/package.json":
                                 '{"name": "pi-web", "version": "2.0.0", "scripts": {"lint": "echo PI-LINT", "test": "echo PI-TEST"}}\n'},
                         "touch pi-web/package.json")
        return f

    def test_pi_web_package_json_change_reports_full_fake_engine(self) -> None:
        """A pi-web package.json change reports full (fake engine): the repo
        root's light report is scoped, but the nested root pi-web/ answers
        mode full with a full-tier fallback diagnostic — the run is full,
        its reason names pi-web and the diagnostic, and pi-web's full tail
        runs (its scoped selection does not)."""
        diag = "verify-scope: changed path package.json is the npm analyzer's own config file; falling back to the full tier"
        f = self.pi_web_fixture("stack=go\n")
        engine = fake_engine(f.root, {
            "app": w403_report("scoped", ["pi-web/package.json"], cmd_of("fast-1", "fast", "echo ROOT-FAST")),
            "pi-web": w403_report("full", ["package.json"],
                                  cmd_of("fast-1", "fast", "echo PI-FAST"),
                                  cmd_of("full-1", "full", "echo PI-FULL"),
                                  cmd_of("scoped-1", "scoped", "echo PI-SCOPED"),
                                  diagnostics=[diag]),
        })
        r = self.run_light(f, engine)
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "")
        self.assertIn("pi-web --json --light --base", "\n".join(engine_args(engine)), "the nested root must be asked --light")
        tier = doc["tier"]
        self.assertEqual((tier["requested"], tier["effective"]), ("light", "full"), tier)
        self.assertEqual(tier.get("reasons"), ["repos/app: the nested engine root pi-web fell back to the full tier: " + diag], tier)
        full = r.check("app:pi-web/full-1")
        self.assertEqual(full["status"], "pass", full)
        self.assertIn("`cd pi-web && echo PI-FULL`", full.get("detail", ""), "the nested full tail must run behind cd pi-web")
        self.assertEqual(r.check("app:pi-web/fast-1")["status"], "pass")
        self.assertEqual(r.check("app:fast-1")["status"], "pass")
        self.assertNotIn("app:pi-web/scoped-1", [c["name"] for c in doc["checks"]], "a full nested report runs no scoped selection")
        self.assertIn("Why the run is full:\n\n- repos/app: the nested engine root pi-web", r.report)
        self.assertEqual(doc["mechanical_verdict"], "pass", doc.get("fail_reason"))

    def test_pi_web_package_json_change_scoped_twin_stays_light(self) -> None:
        """The scoped twin of the pi-web package.json case (fake engine): the
        nested root answers scoped — the run stays light, with no reason,
        and pi-web's scoped selection runs instead of its full tail."""
        f = self.pi_web_fixture("stack=go\n")
        engine = fake_engine(f.root, {
            "app": w403_report("scoped", ["pi-web/package.json"], cmd_of("fast-1", "fast", "echo ROOT-FAST")),
            "pi-web": w403_report("scoped", ["package.json"],
                                  cmd_of("fast-1", "fast", "echo PI-FAST"),
                                  cmd_of("full-1", "full", "echo PI-FULL"),
                                  cmd_of("scoped-1", "scoped", "echo PI-SCOPED")),
        })
        r = self.run_light(f, engine)
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "")
        self.assertEqual((doc["tier"]["effective"], doc["tier"].get("reasons")), ("light", None), doc["tier"])
        self.assertEqual(r.check("app:pi-web/scoped-1")["status"], "pass")
        self.assertNotIn("app:pi-web/full-1", [c["name"] for c in doc["checks"]])
        self.assertEqual(doc["mechanical_verdict"], "pass", doc.get("fail_reason"))

    @unittest.skipUnless(HAVE_NODE and HAVE_PYTHON3, "the real engine's npm analyzer needs node, its --json python3")
    def test_pi_web_package_json_change_reports_full_real_engine(self) -> None:
        """A pi-web package.json change reports full (the repository's own
        workflows/_shared/scripts/verify-scope, copied): the repo root (stack=npm, its own fast
        gate) answers scoped under --light, the nested root pi-web/ — whose
        package.json is its analyzer's config file — answers full, so the
        run is full and its reason names pi-web."""
        if not os.path.isfile(REAL_ENGINE):
            self.skipTest("no scripts/verify-scope at %s" % REAL_ENGINE)
        tools = os.path.join(self.root, "tools")
        os.makedirs(tools)
        engine = os.path.join(tools, "verify-scope")
        shutil.copy2(REAL_ENGINE, engine)
        f = self.pi_web_fixture("stack=npm\nfast_gate=echo ROOT-FAST\n",
                                {"package.json": '{"name": "root", "private": true, "scripts": {}}\n'})
        home = os.path.join(f.root, "home")
        os.makedirs(home)
        # npm writes its logs under HOME: a home of the test's own
        r = self.run_light(f, engine, {"HOME": home})
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "", r.stderr)
        rep = (doc["repos"] or [{}])[0]
        self.assertEqual(rep.get("mode"), "light", "the repo root's own report must be scoped under --light: %r" % rep)
        self.assertIn("(in pi-web)", rep.get("engine_ref", ""), "the nested root must be asked: %r" % rep)
        tier = doc["tier"]
        self.assertEqual((tier["requested"], tier["effective"]), ("light", "full"), tier)
        self.assertTrue([x for x in tier.get("reasons", []) if x.startswith("repos/app: the nested engine root pi-web fell back to the full tier: ")
                         and "package.json" in x], tier)
        nested = [c["name"] for c in doc["checks"] if c["name"].startswith("app:pi-web/")]
        self.assertTrue(nested, "pi-web's full plan must run: %r" % doc["checks"])
        self.assertEqual(r.check("app:fast-1")["status"], "pass")


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are needed")
class LightReplayTest(unittest.TestCase):
    """TestW403ReplayRealE2EReds (w403_light_replay_test.go): the real
    failing output of 2026-11-08's pi-web e2e bar (testdata/light-replay:
    each run's failures and summary sections, verbatim) replayed through the
    light tier. The aggregate command is the repo conf's shape — `cd pi-web
    && E2E_SERVER_MODE=start <runner>` with a rerun= template — and the fake
    runner prints the recorded output on the whole run. The light tier
    locates the red suite repository-relative with its carries: header,
    re-runs it alone once and, still red, once on the baseline: a re-run
    that passes is flaky, a red the baseline shares is pre-existing — both
    pass the bar — and only a red that reproduces and passes on the
    baseline stays a failure."""

    CASES: Tuple[Tuple[str, str], ...] = (  # the recorded run -> its red suite
        ("case379-run2", "polyforge-messages.mjs"),
        ("case380-run1", "polyforge-messages.mjs"),
        ("case380-run3", "run.mjs"),
        ("case385-run2", "polyforge-messages.mjs"),
    )
    SCENARIOS: Tuple[Tuple[str, str, str], ...] = (  # rerun, base, status
        ("pass", "pass", "flaky"),
        ("fail", "fail", "pre_existing"),
        ("fail", "pass", "fail"),
    )

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_replay_real_e2e_reds(self) -> None:
        """TestW403ReplayRealE2EReds."""
        n = 0
        for name, suite in self.CASES:
            for rerun, base, status in self.SCENARIOS:
                with self.subTest(name + "/" + status):
                    n += 1
                    root = os.path.join(self.root, "fx%d" % n)
                    os.makedirs(root)
                    self.replay(root, name, suite, rerun, base, status)

    def replay(self, root: str, name: str, suite: str, rerun: str, base: str, status: str) -> None:
        log = os.path.join(TESTDATA, "light-replay", name + ".log")
        self.assertTrue(os.path.isfile(log), log)
        files = {
            ".polyforge/verify-scope.conf": "stack=go\n",
            "pi-web/.polyforge/verify-scope.conf": "stack=npm\n",
            "pi-web/e2e/runner.sh": W403_REPLAY_RUNNER,
        }
        for s, h in W403_REPLAY_HEADERS.items():
            files["pi-web/e2e/" + s] = h + "console.log('suite');\n"
        f = Fixture(root, {"polyforge": files})
        f.commit_in_repo("polyforge", {"pi-web/HEAD_MARK": "x\n"}, "feature")
        agg = "cd pi-web && E2E_SERVER_MODE=start sh e2e/runner.sh"
        engine = fake_engine(root, {
            "polyforge": w403_report("full", ["pi-web/HEAD_MARK"], w403_rerun("full-8", "full", agg, agg + " -- {unit}")),
        })
        env = {"REPLAY_LOG": log, "REPLAY_RERUN": rerun, "REPLAY_BASE": base}
        r = run_verify(f.capsule, engine, env=env, runtime=FakeRuntime(os.path.join(root, "box-tmp")))
        doc = doc_of(r)
        self.assertEqual(doc.get("incomplete", ""), "")
        u = r.unit("e2e/" + suite)
        self.assertEqual((u["status"], u.get("file")), (status, "pi-web/e2e/" + suite),
                         "the red suite must resolve %s, located repository-relative: %r" % (status, u))
        self.assertNotIn("not re-runnable", u.get("detail", ""), "the template makes the suite re-runnable")
        want: List[str] = []
        for line in W403_REPLAY_HEADERS[suite].strip().split("\n"):
            for g in line[len("// carries: "):].split(", "):
                want.append("pi-web/" + g)
        if status == "fail":
            self.assertEqual(u.get("carries"), want)
        self.assertEqual(doc["mechanical_verdict"], "fail" if status == "fail" else "pass", doc.get("fail_reason"))
        if status != "fail":
            self.assertIn("complete: the runner's summary shows all", r.check("polyforge:full-8").get("detail", ""),
                          "the recorded summary proves the aggregate command complete")


if __name__ == "__main__":
    unittest.main()
