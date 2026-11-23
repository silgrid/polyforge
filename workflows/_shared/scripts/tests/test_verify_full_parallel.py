"""verify.py's full tier runs a repository's declared groups side by side
(polyforge#253): the develop lane's verification reuses polyforge#244's
declaration (the conf's ``group=`` lines, the report's ``full_parallel``),
its container isolation (each group a container of its own), its log
naming (the plan's index, the group's own log directory for its container's
notes) and its result order (plan order, whatever finished first).

The cases below pin the behaviour the ticket asks for: two declared groups
really overlap while each group keeps plan order; a flaky unit's re-run
stays inside its group's container, right after its command; a group's
container that cannot start or is lost is not judged (never a red, never a
host stand-in); the build's unit runs and the analysis's supplements run
after every group, in the repository's own container; and a plan without a
group, with one group, at parallelism 1, or a repository that runs on the
host keeps today's serial path.

Overlap is proven by the commands themselves, never by a sleep posing as
synchronization: each group's first command waits — bounded — for the other
group's start marker, so the same case fails red under a serial run.
"""

import json
import os
import re
import tempfile
import unittest
from typing import Any, Callable, Dict, List, Optional, Tuple

from pfstep import container, runner

from .capsuleutil import TEST_DOCKERFILE
from .fakebox import FakeRuntime
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, Run, cmd_of, fake_engine, report, run_verify

DOCKERFILE = container.DOCKERFILE_VERIFY

# The bounded wait a first command of a group makes for the other one's
# start marker: 600 * 0.02 s = 12 s, far over any scheduling delay and far
# under a red run's limit.
WAIT_LOOP = "for i in $(seq 1 600); do [ -f %s ] && break; sleep 0.02; done"


def wait_for(marker: str, then: str) -> str:
    """A command line that waits (bounded) for marker, then runs `then`."""
    return WAIT_LOOP % marker + "; " + then


def marker_cmds() -> List[Dict[str, Any]]:
    """The plan two declared groups share: a fast gate before the grouped
    span, go's two commands, pi-web's two, and the engine's ungrouped tail
    command after it. Every command carries a unique token the test finds
    its own exec by."""
    return [
        cmd_of("build", "fast", ": PF253BUILD; touch build.done"),
        cmd_of("go1", "full", ": PF253GO1; touch go1.going; " + wait_for(
            "web1.going", "test -f web1.going && touch go1.done"), group="go"),
        cmd_of("go2", "full", ": PF253GO2; test -f go1.done && touch go2.done", group="go"),
        cmd_of("web1", "full", ": PF253WEB1; touch web1.going; " + wait_for(
            "go1.going", "test -f go1.going && touch web1.done"), group="pi-web"),
        cmd_of("web2", "full", ": PF253WEB2; test -f web1.done && touch web2.done", group="pi-web"),
        cmd_of("audit", "full", ": PF253AUDIT; test -f go2.done && test -f web2.done && touch audit.done"),
    ]


def cmd_argvs(rt: FakeRuntime, token: str) -> List[Tuple[str, List[str]]]:
    """The (container id, argv) of every exec whose argv carries token."""
    return [(cid, argv) for cid, _workdir, _env, argv in rt.execs if token in " ".join(argv)]


def one_exec(rt: FakeRuntime, token: str) -> Tuple[str, List[str]]:
    hits = cmd_argvs(rt, token)
    if len(hits) != 1:
        raise AssertionError("want exactly one exec carrying %s, got %r" % (token, rt.execs))
    return hits[0]


def command_names(r: Run) -> List[str]:
    assert r.doc is not None, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr)
    return [c["name"] for c in r.doc["repos"][0]["commands"]]


def log_names(r: Run) -> List[str]:
    assert r.doc is not None
    return [os.path.basename(c.get("log_path", "")) for c in r.doc["repos"][0]["commands"]]


# A command's start line: gate.py's format, [i/n] name (phase): cmd
START_LINE = re.compile(r"^verify\.py: \[\d+/\d+\] \S+ \((?:fast|full|scoped)\): ", re.ASCII)


def start_lines(r: Run) -> List[str]:
    """The progress stream's command start lines, in the order they were
    said."""
    return [line for line in r.stderr.split("\n") if START_LINE.match(line)]


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are needed")
class FullParallelBase(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def fixture(self, *, boxed: bool = True, sub: str = "fx") -> Tuple[Fixture, str]:
        """A touched repo (boxed: carrying .polyforge/Dockerfile.verify on
        its task branch) and a fake engine whose full report the caller
        writes."""
        root = os.path.join(self.root, sub)
        os.makedirs(root, exist_ok=True)
        fx = Fixture(root, {"app": {"a.txt": "a\n", "retry.sh": "#!/bin/bash\n"
                                    "echo '--- PASS: TestA (0.00s)'\n"
                                    "echo 'ok  example.com/x  0.01s'\n"
                                    "touch go1.rerun.done\nexit 0\n"}})
        files: Dict[str, str] = {"a.txt": "a2\n"}
        if boxed:
            files[DOCKERFILE] = TEST_DOCKERFILE
        fx.commit_in_repo("app", files, "change")
        eng = fake_engine(root, {"app": report("full")})
        self.write_report(eng, report("full"))
        return fx, eng

    def write_report(self, eng: str, rep: Dict[str, Any]) -> None:
        """Rewrite the fake engine's canned full report (its directory is
        the engine's own): the full tier asks the engine --full."""
        with open(os.path.join(os.path.dirname(eng), "reports", "app.full.json"), "w",
                  encoding="utf-8") as f:
            f.write(json.dumps(rep))

    def runtime(self, sub: str = "box",
                make: Optional[Callable[[str], FakeRuntime]] = None) -> FakeRuntime:
        d = os.path.join(self.root, sub)
        os.makedirs(d, exist_ok=True)
        return (make or FakeRuntime)(d)

    def out(self, sub: str) -> str:
        return os.path.join(self.root, "res-" + sub, "result.json")


class SerialFallbackTest(FullParallelBase):
    def test_undeclared_plan_runs_the_serial_path(self) -> None:
        """A report that binds no command to a group (an undeclared
        repository's) runs the plan in order in the one repository
        container: the progress lines come in plan order, one command at a
        time, and the command logs take the plan's numbers (the engine
        invocation holds the first)."""
        fx, eng = self.fixture()
        cmds = [cmd_of("build", "fast", ": PF253BUILD; touch build.done"),
                cmd_of("go", "full", ": PF253GO; touch go.done"),
                cmd_of("web", "full", ": PF253WEB; touch web.done")]
        self.write_report(eng, report("full", *cmds))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("serial"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(len(rt.starts), 1, "only the repository's container starts: %r" % rt.starts)
        self.assertEqual(command_names(r), ["build", "go", "web"])
        # the engine invocation takes 1.log; plan index i takes i + 2
        self.assertEqual(log_names(r), ["2.log", "3.log", "4.log"])
        names = [line.split("] ", 1)[1].split(" ", 1)[0] for line in start_lines(r)]
        self.assertEqual(names, ["build", "go", "web"], start_lines(r))

    def test_single_group_and_parallel_one_run_the_serial_path(self) -> None:
        """A single declared group, and two groups at full_parallel=1, keep
        the serial path: one container, plan order."""
        for sub, cmds, extra in [
            ("one-group", [cmd_of("go1", "full", ": PF253G1; touch g1.done", group="go"),
                           cmd_of("go2", "full", ": PF253G2; test -f g1.done && touch g2.done", group="go")],
             {}),
            ("parallel-one", [cmd_of("go1", "full", ": PF253G1; touch g1.done", group="go"),
                              cmd_of("web1", "full", ": PF253W1; touch w1.done", group="pi-web")],
             {"full_parallel": 1}),
        ]:
            fx, eng = self.fixture(sub="fx-" + sub)
            self.write_report(eng, report("full", *cmds, **extra))
            rt = self.runtime(sub="box-" + sub)
            r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out(sub))
            self.assertEqual(r.exit, 0, "%s: %s" % (sub, r.result.get("summary")))
            self.assertEqual(len(rt.starts), 1, "%s: one container only: %r" % (sub, rt.starts))
            self.assertEqual(command_names(r), [c["name"] for c in cmds], sub)


class GroupedBarTest(FullParallelBase):
    def test_two_groups_overlap_in_their_own_containers(self) -> None:
        """The declared groups really run side by side — each first command
        waits for the other's start marker, which a serial run never
        writes — and every command runs in its group's container, in plan
        order inside it; the results stay in plan order and the command
        logs keep the plan's numbers."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("grouped"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        # the repository's container and one per group, no more
        self.assertEqual(len(rt.starts), 3, "want the repo's and the two groups': %r" % rt.starts)
        repo_cid = "cid-1"
        build_cid, _ = one_exec(rt, "PF253BUILD")
        go1_cid, _ = one_exec(rt, "PF253GO1")
        go2_cid, _ = one_exec(rt, "PF253GO2")
        web1_cid, _ = one_exec(rt, "PF253WEB1")
        web2_cid, _ = one_exec(rt, "PF253WEB2")
        audit_cid, _ = one_exec(rt, "PF253AUDIT")
        self.assertEqual(build_cid, repo_cid, "the ungrouped head command runs in the repository's container")
        self.assertEqual(audit_cid, repo_cid, "the ungrouped tail command runs in the repository's container")
        self.assertNotEqual(go1_cid, repo_cid, "a group's commands run in a container of their own")
        self.assertEqual(go1_cid, go2_cid, "one group, one container")
        self.assertEqual(web1_cid, web2_cid, "one group, one container")
        self.assertNotEqual(go1_cid, web1_cid, "two groups, two containers")
        # results and logs in plan order, whatever finished first
        self.assertEqual(command_names(r), [c["name"] for c in cmds])
        self.assertEqual(log_names(r), ["%d.log" % (i + 2) for i in range(len(cmds))])
        # every progress line is whole and numbered by the plan; the two
        # groups' lines interleave, so the plan index is the order
        lines = start_lines(r)
        self.assertEqual(len(lines), len(cmds), lines)
        for line in lines:
            m = re.match(r"^verify\.py: \[(\d+)/(\d+)\] (\S+) \((?:fast|full|scoped)\): ", line, re.ASCII)
            self.assertIsNotNone(m, "a whole start line: %r" % line)
            assert m is not None
            self.assertEqual(int(m.group(1)), [c["name"] for c in cmds].index(m.group(3)) + 1, line)
            self.assertEqual(m.group(3), cmds[int(m.group(1)) - 1]["name"], line)

    def test_group_container_logs_are_their_own(self) -> None:
        """Each group writes its container's notes into a log directory of
        its own, so the repository's log directory is nobody's scratch: the
        run's command logs stay where the plan named them."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("group-logs"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        logs = os.path.join(os.path.dirname(os.path.abspath(self.out("group-logs"))), "logs", "app")
        dirs = sorted(os.listdir(logs)) if os.path.isdir(logs) else []
        self.assertIn("group-1", dirs, "the first group's own log directory: %r" % dirs)
        self.assertIn("group-2", dirs, "the second group's own log directory: %r" % dirs)


class FlakyInGroupTest(FullParallelBase):
    def test_flaky_rerun_stays_in_its_group(self) -> None:
        """A unit that fails once and passes on its re-run is flaky, and the
        re-run stays inside its group: it runs in the group's container
        right after its command, before the group's next command starts."""
        fx, eng = self.fixture()
        go1 = (": PF253GO1; if [ -f go1.attempt ]; then echo '--- PASS: TestA (0.00s)';"
               " echo 'ok  example.com/x  0.01s'; exit 0; else : > go1.attempt;"
               " echo '--- FAIL: TestA (0.00s)'; exit 1; fi")
        cmds = [
            cmd_of("go1", "full", go1, group="go", rerun="go1|bash ./retry.sh {unit}"),
            cmd_of("go2", "full", ": PF253GO2; test -f go1.rerun.done", group="go"),
            cmd_of("web1", "full", ": PF253WEB1; touch web1.done", group="pi-web"),
        ]
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("flaky"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        u = r.unit("TestA")
        self.assertEqual(u["status"], "flaky", u)
        self.assertIn("flaky 1/2", u.get("detail", ""), u)
        # go1 ran its first run and (the confirmed whole command) once more
        # in the repository's container; its group's run is the first
        go1_cids = [cid for cid, _w, _e, argv in rt.execs
                    if "PF253GO1" in " ".join(argv) and "TMPDIR" in " ".join(_e)]
        go2_cid, _ = one_exec(rt, "PF253GO2")
        reruns = [cid for cid, _w, _e, argv in rt.execs if "retry.sh" in " ".join(argv)]
        self.assertEqual(len(reruns), 1, "one targeted re-run: %r" % rt.execs)
        self.assertEqual(reruns[0], go1_cids[0], "the re-run stays in the group's container")
        self.assertEqual(go2_cid, go1_cids[0], "the group keeps its container")
        self.assertNotEqual(go1_cids[0], "cid-1", "the group's container is not the repository's")


class GroupBoxFailureTest(FullParallelBase):
    def test_group_container_start_failure_not_judged(self) -> None:
        """A group's container that cannot start makes the run not judged
        (exit 2), and nothing falls back to the host."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime(make=lambda d: SecondStartFailsRuntime(d))
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("start-fail"))
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        summary = r.result.get("summary", "")
        self.assertIn("the verification container could not be prepared", summary)
        self.assertIn("repos/app", summary)
        # a host run would be no FakeRuntime exec: the fallback shows in the
        # worktree, where the group's command would leave its marker
        self.assertFalse(os.path.exists(os.path.join(fx.repo("app"), "go1.going")),
                         "no group command runs on the host (its marker would be in the worktree)")
        self.assertFalse(os.path.exists(os.path.join(fx.repo("app"), "web1.going")), "nor the other group's")

    def test_group_container_lost_incomplete(self) -> None:
        """A group's container lost while the bar ran leaves the record
        proving nothing (exit 2), never a red of the change's."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime(make=lambda d: GroupBoxLostRuntime(d))
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("lost"))
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        self.assertIn("became unusable", r.result.get("summary", ""))


class WorkerFaultTest(FullParallelBase):
    def test_a_worker_fault_leaves_the_run_not_judged(self) -> None:
        """A fault of the script inside a group worker never becomes a pass:
        the run is not judged (exit 2) and the summary names the command and
        the fault — the serial path's own rule (a fault is never a verdict)."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime(make=lambda d: FaultyRuntime(d, "PF253GO2", RuntimeError("the report reader broke")))
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("fault"))
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        summary = r.result.get("summary", "")
        self.assertIn("the verification could not run", summary)
        self.assertIn("go2", summary)
        self.assertIn("RuntimeError", summary)
        self.assertIn("the report reader broke", summary)


class GroupInterruptTest(FullParallelBase):
    def test_an_interrupt_in_a_group_stops_the_run(self) -> None:
        """An interrupt while a group's command runs stops the whole run: exit
        2, and the phases after the bar — here the build's unit run — never
        start (the serial path's own behaviour)."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        units = os.path.join(self.root, "units-interrupt.json")
        with open(units, "w", encoding="utf-8") as f:
            f.write(json.dumps([{
                "id": "u1", "test_commit": fx.head("app"), "impl_commit": fx.head("app"),
                "run": {"repo": "app", "cwd": ".", "argv": [
                    "bash", "-c", ": PF253UNIT; touch unit.ran; echo UNIT_OK"]},
                "marker": "UNIT_OK",
            }]))
        rt = self.runtime(make=lambda d: FaultyRuntime(d, "PF253GO2", runner.Interrupted(15)))
        r = run_verify(fx.capsule, eng, "--unit-runs", units, tier="full", runtime=rt,
                       out=self.out("interrupt"))
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        self.assertIn("interrupted", r.result.get("summary", ""))
        self.assertFalse(os.path.exists(os.path.join(fx.repo("app"), "unit.ran")),
                         "the unit run after the bar never starts: its marker is not written")


class FaultyRuntime(FakeRuntime):
    """A fake container engine whose exec raises a fault for the command
    carrying token — a script's fault (RuntimeError) or an interrupt
    (runner.Interrupted), never a verdict of the command itself."""

    def __init__(self, tmp_root: str, token: str, exc: BaseException) -> None:
        super().__init__(tmp_root)
        self.token = token
        self.exc = exc

    def exec_argv(self, cid: str, workdir: str, env: Any, argv: Any) -> List[str]:
        if self.token in " ".join(argv):
            raise self.exc
        return super().exec_argv(cid, workdir, env, argv)


class SecondStartFailsRuntime(FakeRuntime):
    """The repository's container starts; a group's does not."""

    def start(self, spec: Any) -> str:
        if len(self.starts) >= 1:
            raise container.ContainerError("the engine answered 500")
        return super().start(spec)


class GroupBoxLostRuntime(FakeRuntime):
    """The repository's container answers; a group's container does not
    answer after a command ran in it."""

    def __init__(self, tmp_root: str) -> None:
        super().__init__(tmp_root)
        self.group_cids: List[str] = []

    def start(self, spec: Any) -> str:
        cid = super().start(spec)
        if len(self.starts) > 1:
            self.group_cids.append(cid)
        return cid

    def reap(self, cid: str, remove: Any) -> None:
        super().reap(cid, remove)
        if cid in self.group_cids:
            raise container.ContainerError("Error: No such container: " + cid)


class AfterGroupsTest(FullParallelBase):
    def test_unit_runs_and_analysis_wait_for_all_groups(self) -> None:
        """The build's unit run and the analysis's supplement run after every
        group ended — their commands need both groups' markers — and in the
        repository's own container, never a group's."""
        fx, eng = self.fixture()
        cmds = marker_cmds()
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        units = os.path.join(self.root, "units.json")
        with open(units, "w", encoding="utf-8") as f:
            f.write(json.dumps([{
                "id": "u1", "test_commit": fx.head("app"), "impl_commit": fx.head("app"),
                "run": {"repo": "app", "cwd": ".", "argv": [
                    "bash", "-c", ": PF253UNIT; test -f go2.done && test -f web2.done && echo UNIT_OK"]},
                "marker": "UNIT_OK",
            }]))
        analysis = os.path.join(self.root, "analysis.json")
        with open(analysis, "w", encoding="utf-8") as f:
            f.write(json.dumps({
                "supplement_limit": 1, "changed": [], "uncovered": {}, "selected": {},
                "tests": [{"id": "a1", "repo": "app", "file": "a.txt", "cwd": ".",
                           "argv": ["bash", "-c", ": PF253ANALYSIS; test -f go2.done && test -f web2.done"
                                    " && echo ANALYSIS_OK"],
                           "marker": "ANALYSIS_OK", "why": "pins the bar", "evidence": "the plan"}],
            }))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, "--unit-runs", units, "--analysis", analysis,
                       tier="full", runtime=rt, out=self.out("after"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        repo_cid = "cid-1"
        unit_cid, _ = one_exec(rt, "PF253UNIT")
        analysis_cid, _ = one_exec(rt, "PF253ANALYSIS")
        self.assertEqual(unit_cid, repo_cid, "the unit run uses the repository's container")
        self.assertEqual(analysis_cid, repo_cid, "the analysis run uses the repository's container")
        # the group containers are gone by then
        self.assertEqual(rt.removes[:2], ["cid-2", "cid-3"],
                         "the group containers are removed at the bar's end, before the repository's")


class HostRunStaysSerialTest(FullParallelBase):
    def test_host_repo_keeps_the_serial_bar(self) -> None:
        """A repository that runs on the host has no container to isolate a
        group in: its declared groups keep the serial bar — plan order, no
        container — even at full_parallel=2. The two first commands wait
        for each other only long enough to fail if they were raced."""
        fx, eng = self.fixture(boxed=False)
        short = "for i in $(seq 1 100); do [ -f %s ] && break; sleep 0.02; done"
        cmds = [
            cmd_of("go1", "full", ": PF253GO1; touch go1.going; " + (short % "web1.going") + "; true",
                   group="go"),
            cmd_of("web1", "full", ": PF253WEB1; touch web1.going; " + (short % "go1.going") + "; true",
                   group="pi-web"),
        ]
        self.write_report(eng, report("full", *cmds, full_parallel=2))
        rt = self.runtime()
        r = run_verify(fx.capsule, eng, tier="full", runtime=rt, out=self.out("host"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(rt.starts, [], "no container is started for a host repository")
        self.assertEqual(command_names(r), ["go1", "web1"])
        lines = start_lines(r)
        self.assertEqual([line.split("] ", 1)[1].split(" ", 1)[0] for line in lines], ["go1", "web1"])


if __name__ == "__main__":
    unittest.main()