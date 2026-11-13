"""gate.py as its callers run it: the invocations it refuses (exit 2, the
reason on stderr), --isolation container the only isolation, the
container's limits as its own flags (D7: min(8, cores) / 16g / 4096 by
default, a malformed one refused before any container starts), a tree
without the Dockerfile or a machine without a container engine not judged
(exit 2, the reason said), the clone mode's stdout and green tag, the plan
alone, and the capsule mode on bare pins. Every result file it writes
satisfies the published schema."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict

import gate
from pfstep import container, result

from . import schemacheck
from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, gate_clone, git_t, green_clone, read_json, run_main

ISO = ["--isolation", "container"]

with open(result.SCHEMA_PATH, encoding="utf-8") as _f:
    SCHEMA: Dict[str, Any] = json.load(_f)


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GateCLITest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.fb = FakeRuntime(os.path.join(self.root, "box-tmp"))

    def tearDown(self) -> None:
        self._td.cleanup()

    def result_of(self, path: str) -> Dict[str, Any]:
        doc = read_json(path)
        self.assertEqual(schemacheck.errors(SCHEMA, doc), [], doc)
        return doc

    def test_unusable_invocations_are_refused(self) -> None:
        capsule = os.path.join(self.root, "capsule")
        os.makedirs(os.path.join(capsule, "repos", "app"))

        def pins(name: str, doc: Any) -> str:
            p = os.path.join(capsule, name)
            with open(p, "w", encoding="utf-8") as f:
                json.dump(doc, f)
            return p

        bad_gated = pins("pins.json", {"heads": {"app": "x"}, "gated": ["lib"]})
        no_gated = pins("nogated.json", {"heads": {"app": "x"}})
        # a result file's pins are its own heads and its details' gated
        detail_heads = pins("detailheads.json", {"schema": "pf-step-result/1", "command": "pf-revert", "heads": {},
                                                 "details": {"heads": {"app": "x"}, "gated": ["app"]}})
        good_pins = pins("good.json", {"heads": {"app": "x"}, "gated": ["app"]})
        gate_res = pins("gate.json", {"schema": "pf-step-result/1", "command": "gate.py", "details": {}})
        # --batch is collect's result: a result file of another command is refused
        not_collect = pins("batch.json", {"schema": "pf-step-result/1", "command": "revert.py", "details": {"items": []}})
        for args, want in (
            ([], "--isolation container is required"),
            (["--isolation", "host"], "never on the host"),
            (ISO + ["--pins", "p"], "need --capsule"),
            (ISO + ["--triage"], "need --capsule"),
            (ISO + ["--capsule", capsule], "--capsule needs --pins"),
            (ISO + ["--capsule", capsule, "--pins", "p", "--repo", "x"], "--repo names a lone clone"),
            (ISO + ["--capsule", capsule, "--pins", "p", "--triage"], "--triage needs --gate"),
            (ISO + ["--capsule", capsule, "--pins", "p", "--gate", "g"], "need --triage"),
            (ISO + ["--capsule", capsule, "--pins", good_pins, "--triage", "--gate", gate_res, "--batch", not_collect],
             'not an integrate.py result file (schema "pf-step-result/1", command "revert.py")'),
            (ISO + ["--capsule", capsule, "--pins", good_pins, "--triage", "--gate", not_collect, "--batch", not_collect],
             "--gate %s: not a gate.py result file" % not_collect),
            (ISO + ["--plan-only", "--json"], "--plan-only prints the plan alone"),
            (ISO + ["--plan-only", "--capsule", capsule, "--pins", "p"], "--plan-only prints the plan alone"),
            (ISO + ["--mark-green"], "--mark-green needs --summary"),
            (ISO + ["--summary", "s"], "needs --mark-green"),
            (ISO + ["--remote", "r"], "needs --mark-green"),
            (ISO + ["--capsule", self.root, "--pins", "p"], "no repos/ directory"),
            (ISO + ["--capsule", capsule, "--pins", os.path.join(capsule, "none.json")], "--pins"),
            (ISO + ["--capsule", capsule, "--pins", bad_gated], "the gated repo lib has no head"),
            (ISO + ["--capsule", capsule, "--pins", no_gated], "no `gated` list"),
            (ISO + ["--capsule", capsule, "--pins", detail_heads], "the gated repo app has no head"),
            (ISO + ["--memory", "8 gigs"], "--memory"),
            (ISO + ["--memory", "5m"], "below docker's 6m floor"),
            (ISO + ["--cpus", "many"], "--cpus"),
            (ISO + ["--pids", "1.5"], "--pids"),
            (ISO + ["--deadline", "soon"], "--deadline"),
            (ISO + ["x"], "unrecognized arguments"),
        ):
            with self.subTest(args):
                m = run_main(args, self.root, runtime=self.fb)
                self.assertEqual(m.exit, 2, m.stderr)
                self.assertIn(want, m.stderr)
        self.assertEqual(self.fb.starts, [], "no container started for an unusable invocation")

    def test_no_dockerfile_in_container_mode_is_not_judged(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\n", dockerfile=False)
        out = os.path.join(self.root, "gate.json")
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs"), "--out", out], d, runtime=self.fb)
        self.assertEqual(m.exit, 2, m.stdout + m.stderr)
        res = self.result_of(out)
        self.assertEqual((res["command"], res["verdict"]), (gate.COMMAND, "error"))
        self.assertIn("carries no .polyforge/Dockerfile.verify", res["summary"])
        self.assertIn("--isolation container", res["summary"])
        self.assertEqual(res["details"]["incomplete"], {"app": res["details"]["repos"]["app"]["result"]["incomplete"]})
        self.assertEqual(self.fb.starts, [])

    def test_no_container_engine_is_not_judged(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\n")
        self.fb.unavailable = "the docker client \"docker\" is not found"
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs"), "--json"], d, runtime=self.fb)
        self.assertEqual(m.exit, 2)
        res = json.loads(m.stdout.strip().split("\n")[-1])
        self.assertIn("the container engine is unavailable: the docker client", res["summary"])

    def test_the_limits_are_the_scripts_flags(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\n")
        logs = os.path.join(self.root, "logs")
        m = run_main(ISO + ["--log-dir", logs], d, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stdout + m.stderr)
        s = self.fb.starts[-1]
        self.assertEqual((s.cpus, s.memory, s.pids), (container.default_cpus(), "16g", 4096), "the defaults: min(8, cores), 16g, 4096")
        m = run_main(ISO + ["--log-dir", logs, "--cpus", "3", "--memory", "3G", "--pids", "333"], d, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stdout + m.stderr)
        s = self.fb.starts[-1]
        self.assertEqual((s.cpus, s.memory, s.pids), (3.0, "3g", 333))
        # a machine's pf CLI config is no input of the gate's limits (D7)
        xdg = os.path.join(self.root, "xdg")
        os.makedirs(os.path.join(xdg, "polyforge"))
        with open(os.path.join(xdg, "polyforge", "cli.toml"), "w", encoding="utf-8") as f:
            f.write("[daemon]\nverify_container_cpus = 3\nverify_container_memory = 8g\n")
        m = run_main(ISO + ["--log-dir", logs], d, env={"XDG_CONFIG_HOME": xdg}, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stdout + m.stderr)
        self.assertEqual(self.fb.starts[-1].cpus, container.default_cpus())

    def test_the_clone_mode_prints_its_progress_and_its_tag(self) -> None:
        d = green_clone(self.root, "stack=none\nfast_gate=echo one\n")
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs"), "--mark-green", "--summary", "by the test"], d, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stdout + m.stderr)
        lines = m.stdout.strip().split("\n")
        self.assertRegex(lines[-1], r"\Agreen/[0-9]{8}-1\Z", "the tag's name is the last stdout line")
        self.assertIn("gate: PASS - 1 commands passed", m.stdout)
        self.assertNotIn("gate.py: the gate passed", m.stdout, "the clone mode prints no summary line")
        tag = lines[-1]
        self.assertIn(tag, git_t(os.path.join(self.root, "origin.git"), "tag", "-l"))
        # --json: the result on stdout, the progress and the tag on stderr
        out = os.path.join(self.root, "gate.json")
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs2"), "--mark-green", "--summary", "again", "--json", "--out", out],
                     d, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stderr)
        res = self.result_of(out)
        self.assertEqual(json.loads(m.stdout), res)
        self.assertEqual(res["details"]["tags"], {"app": tag}, "the same record is not tagged again")
        self.assertIn("not tagged again", m.stderr)
        self.assertTrue(m.stderr.strip().endswith(tag))
        self.assertEqual(list(res["details"]), ["mode", "gated", "repos", "tags", "log_dir"])
        self.assertEqual(list(res["details"]["repos"]["app"]), ["head", "result", "tag"])

    def test_mark_green_needs_a_clean_tree_before_the_gate_runs(self) -> None:
        d = green_clone(self.root, "stack=none\nfast_gate=echo one\n")
        with open(os.path.join(d, "stray.txt"), "w", encoding="utf-8") as f:
            f.write("x\n")
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs"), "--mark-green", "--summary", "s"], d, runtime=self.fb)
        self.assertEqual(m.exit, 2)
        self.assertIn("--mark-green: the worktree is not clean", m.stderr)
        self.assertEqual(self.fb.starts, [])

    def test_a_red_gate_exits_1(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\nfull_command=exit 4\n")
        out = os.path.join(self.root, "gate.json")
        m = run_main(ISO + ["--log-dir", os.path.join(self.root, "logs"), "--out", out], d, runtime=self.fb)
        self.assertEqual(m.exit, 1)
        res = self.result_of(out)
        self.assertEqual(res["summary"], "1 of 2 commands failed: full-1 (exit 4)")
        self.assertEqual(res["details"]["failed"], ["app"])
        self.assertEqual(len(res["logs"]), 2)

    def test_the_plan_alone(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\n")
        m = run_main(ISO + ["--plan-only", "--log-dir", os.path.join(self.root, "logs")], d, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stderr)
        p = json.loads(m.stdout)
        self.assertEqual(list(p), ["commit", "clean", "runner", "runner_version", "plan_digest", "plan"])
        self.assertEqual((p["commit"], p["runner"], p["runner_version"]), (git_t(d, "rev-parse", "HEAD"), "gate.py", "2"))
        self.assertEqual(gate.plan_digest(p["plan"]), p["plan_digest"])
        self.assertIn("gate: PLAN - 1 commands", m.stderr)
        self.assertFalse(os.path.exists(os.path.join(d, "gate.json")))

    def test_the_capsule_mode_on_bare_pins(self) -> None:
        capsule = os.path.join(self.root, "capsule")
        origin = os.path.join(self.root, "app.git")
        seed = gate_clone(self.root, "stack=none\nfast_gate=echo one\n", name="seed")
        git_t(self.root, "init", "-q", "--bare", "-b", "main", origin)
        git_t(seed, "push", "-q", origin, "main")
        git_t(self.root, "clone", "-q", origin, os.path.join(capsule, "repos", "app"))
        head = git_t(seed, "rev-parse", "HEAD")
        pins = os.path.join(capsule, "pins.json")
        out = os.path.join(capsule, "artifacts", "integrate", "gate.json")
        with open(pins, "w", encoding="utf-8") as f:
            json.dump({"heads": {"app": head}, "gated": []}, f)
        m = run_main(ISO + ["--capsule", capsule, "--pins", pins, "--out", out], self.root, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stderr)
        res = self.result_of(out)
        self.assertIn("nothing gated", res["summary"])
        self.assertEqual((res["details"]["mode"], res["details"]["gated"], res["details"]["repos"], res["heads"]), ("capsule", [], {}, {}))
        with open(pins, "w", encoding="utf-8") as f:
            json.dump({"heads": {"app": head}, "gated": ["app"]}, f)
        m = run_main(ISO + ["--capsule", capsule, "--pins", pins, "--out", out], self.root,
                     env={"PF_WI_ID": "wi_batch", "PF_ROUND": "r1"},
                     runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stdout + m.stderr)
        res = self.result_of(out)
        self.assertEqual((res["verdict"], res["heads"], res["round"]), ("pass", {"app": head}, "r1"))
        self.assertIn("gate: the full gate on app main", m.stdout)
        self.assertIn("gate.py: the gate passed in app (pass)", m.stdout)
        labels = self.fb.starts[-1].labels
        self.assertEqual((labels[container.LABEL_DAEMON], labels[container.LABEL_WI]),
                         (container.GATE_OWNER, "wi_batch"), "inside a step the container is the gate's, labelled with the work item")
        self.assertRegex(labels[container.LABEL_BOOT], r"^[0-9a-f]{16}$")
        self.assertNotEqual(labels[container.LABEL_EXPIRES], "")
        self.assertTrue(os.path.isfile(os.path.join(os.path.dirname(out), "logs", ".gitignore")),
                        "the logs beside --out are kept out of the chain")
        d = res["details"]
        self.assertEqual(list(d), ["mode", "pins", "gated", "repos", "log_dir"])
        self.assertEqual(list(d["repos"]["app"]), ["head", "branch", "result"])
        self.assertEqual(d["repos"]["app"]["result"]["commit"], head)
        # outside a step the container is the gate's own
        m = run_main(ISO + ["--capsule", capsule, "--pins", pins, "--out", out], self.root, runtime=self.fb)
        self.assertEqual(m.exit, 0, m.stderr)
        labels = self.fb.starts[-1].labels
        self.assertEqual((labels[container.LABEL_DAEMON], labels[container.LABEL_WI]), (container.GATE_OWNER, gate.GATE_WI))



if __name__ == "__main__":
    unittest.main()
