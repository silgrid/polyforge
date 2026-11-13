"""gate.py against a real docker engine (skipped when none answers, the
small public image cannot be had, or the engine does not see this
machine's directories at their own paths): the gate's commands never see
the host — no docker client, no docker socket, no host-only environment —
while the same probes fail on the host (the negative control); the gate
removes its container; and a gate container whose deadline passed is swept
by the next gate of the same owner. The Go side's twin was
verifyrun/w405_gate_real_test.go."""

import os
import secrets
import shutil
import subprocess
import tempfile
import time
import unittest
from typing import List

import gate
from pfstep import container, duration, proc

from .gatefix import commit_all, git_t, new_gate, write_files
from .test_container_docker import ENGINE, PROBE_IMAGE, REAL_DOCKERFILE, _sees

PROBE_CLIENT = 'if command -v docker >/dev/null 2>&1; then echo "host docker client visible: $(command -v docker)"; exit 1; fi; echo "no docker client"'
PROBE_SOCKET = 'if [ -e /var/run/docker.sock ]; then echo "docker socket visible"; exit 1; fi'
PROBE_ENV = 'if [ -n "${PF_W405_HOST_ONLY:-}${TMUX:-}" ]; then echo "host environment visible"; exit 1; fi'

# a scope engine the busybox image can run: it plans the three probes as the
# full tier's commands
ENGINE_SCRIPT = """#!/bin/sh
cat <<'JSON'
{"schema":1,"root":".","stack":"none","analyzer":null,"conf":"present","conf_changed_in_diff":false,"base":"HEAD","head":"HEAD",
 "default_branch":"main","mode":"full","paths":[],
 "commands":[
  {"name":"probe-docker-client","phase":"fast","cmd":"PROBE_CLIENT","requires_env":[],"requires_cmd":[],"timeout":null,"report":null,"source":"conf"},
  {"name":"probe-docker-socket","phase":"full","cmd":"PROBE_SOCKET","requires_env":[],"requires_cmd":[],"timeout":null,"report":null,"source":"conf"},
  {"name":"probe-host-env","phase":"full","cmd":"PROBE_ENV","requires_env":[],"requires_cmd":[],"timeout":null,"report":null,"source":"conf"}],
 "test_glob":[],"base_setup":[],"runner":null,"diagnostics":[]}
JSON
"""


def json_str(s: str) -> str:
    return s.replace("\\", "\\\\").replace('"', '\\"')


@unittest.skipUnless(ENGINE is not None and shutil.which("git") is not None, "no docker engine (or %s, git)" % PROBE_IMAGE)
class RealDockerGateTest(unittest.TestCase):
    def setUp(self) -> None:
        assert ENGINE is not None
        self.d = ENGINE
        self.owner = "w477-real-" + secrets.token_hex(5)
        self.repo = "w477-real-" + secrets.token_hex(5)
        self.dirs: List[str] = []

    def tearDown(self) -> None:
        try:
            for cid in self.d.list_labelled({container.LABEL_DAEMON: self.owner}, container.LABEL_BOOT):
                try:
                    self.d.remove(cid)
                except container.ContainerError:
                    pass
        except container.ContainerError:
            pass
        proc.run([self.d.bin, "rmi", "--force", container.image_tag(self.repo, REAL_DOCKERFILE.encode())], timeout=60)
        proc.run([self.d.bin, "volume", "rm", "--force", container.cache_volume(self.repo, REAL_DOCKERFILE.encode())], timeout=60)
        for p in self.dirs:
            shutil.rmtree(p, ignore_errors=True)

    def visible_dir(self) -> str:
        for base in (tempfile.gettempdir(), os.getcwd()):
            try:
                p = os.path.realpath(tempfile.mkdtemp(prefix=".w477-gate-real-", dir=base))
            except OSError:
                continue
            self.dirs.append(p)
            if _sees(self.d, p):
                return p
        self.skipTest("the docker engine does not see this machine's directories at their own paths")

    def test_the_gate_never_sees_the_host(self) -> None:
        clone = os.path.join(self.visible_dir(), self.repo)
        os.makedirs(clone)
        git_t(clone, "init", "-q", "-b", "main")
        engine = ENGINE_SCRIPT.replace("PROBE_CLIENT", json_str(PROBE_CLIENT)).replace("PROBE_SOCKET", json_str(PROBE_SOCKET)) \
            .replace("PROBE_ENV", json_str(PROBE_ENV))
        write_files(clone, {gate.GATE_ENGINE_REL: engine}, 0o755)
        write_files(clone, {container.DOCKERFILE_VERIFY: REAL_DOCKERFILE})
        commit_all(clone, "tree")
        host_env = dict(os.environ, PF_W405_HOST_ONLY="1", TMUX="/tmp/tmux-0/default,1,0")
        # negative control: on the host the probes see what the gate must not
        if shutil.which("docker"):
            c = subprocess.run(["bash", "-c", PROBE_CLIENT], env=host_env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            self.assertNotEqual(c.returncode, 0, "the client probe passed on the host: it cannot tell the host from the gate")
        self.assertNotEqual(subprocess.run(["bash", "-c", PROBE_ENV], env=host_env, stdout=subprocess.DEVNULL).returncode, 0)

        lines: List[str] = []
        g = new_gate(self.d, env=host_env, deadline=5 * duration.MINUTE, cpus=1, memory="256m", pids=128, lines=lines, owner=self.owner)
        res = g.run(clone, self.repo, os.path.join(self.dirs[0], "logs"))
        self.assertEqual(res.incomplete, "", "\n".join(lines))
        self.assertTrue(res.passed, "\n".join(lines))
        self.assertEqual(len(res.commands), 3)
        left = proc.run([self.d.bin, "ps", "--all", "--quiet", "--filter", "name=^" + res.container + "$"], timeout=60)
        self.assertEqual(left.stdout.strip(), b"", "the gate's container is still there")

    def test_a_gate_sweeps_its_owners_expired_containers(self) -> None:
        now = int(time.time())
        old = self.d.start(container.ContainerSpec(name=self.owner + "-old", image=PROBE_IMAGE, cpus=1, memory="256m", pids=64,
                                                   labels={container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b",
                                                           container.LABEL_EXPIRES: str(now - 60)}))
        live = self.d.start(container.ContainerSpec(name=self.owner + "-live", image=PROBE_IMAGE, cpus=1, memory="256m", pids=64,
                                                    labels={container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b",
                                                            container.LABEL_EXPIRES: str(now + 3600)}))
        g = new_gate(self.d, owner=self.owner)
        container.sweep_run(g.co, lambda _: None)
        left = self.d.list_labelled({container.LABEL_DAEMON: self.owner}, container.LABEL_BOOT)
        self.assertNotIn(old, left)
        self.assertIn(live, left)


if __name__ == "__main__":
    unittest.main()
