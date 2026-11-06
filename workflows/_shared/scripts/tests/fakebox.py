"""A fake container engine (pfstep.container.ContainerRuntime) that runs
every "container" command on the host — in the same directory, under
``env -i`` with only the image's environment plus what the run adds, an
in-container /tmp path mapped under a directory of its own — and records
every call. Its containers hold their labels, and ``list_labelled``
filters by them as the engine does; its volumes are listed as unused, and
removed, only while no container references them, as the engine does."""

import os
import shutil
import threading
from typing import IO, Dict, List, Mapping, Optional, Sequence, Set, Tuple

from pfstep.container import ContainerError, ContainerSpec


class FakeRuntime:
    def __init__(self, tmp_root: str) -> None:
        self.tmp_root = tmp_root
        self.image_environ: List[str] = ["PATH=" + os.environ.get("PATH", "/usr/bin:/bin"), "IMAGE_ONLY=from-image", "HOME=/root"]
        self.present: Dict[str, bool] = {}
        self.commands: Dict[str, bool] = {}
        # set an error text to make the call fail
        self.unavailable = ""
        self.build_err = ""
        self.start_err = ""
        self.reap_err = ""
        self.remove_err = ""
        self.list_err = ""
        self.volume_list_err = ""
        self.volume_remove_err = ""
        self.builds: List[Tuple[str, bytes, Dict[str, str]]] = []
        self.starts: List[ContainerSpec] = []
        self.execs: List[Tuple[str, str, List[str], List[str]]] = []
        self.reaps: List[List[str]] = []
        self.removes: List[str] = []
        self.listed: List[Tuple[Dict[str, str], str]] = []
        # containers: id -> its labels (the started ones, and a test's seeds)
        self.containers: Dict[str, Dict[str, str]] = {}
        # volumes: the named volumes there are (the started containers', and a test's seeds)
        self.volumes: Set[str] = set()
        # mounts: container id -> the volumes it references (a test may seed one)
        self.mounts: Dict[str, List[str]] = {}
        self.volume_removes: List[str] = []
        self._mu = threading.Lock()

    def map_tmp(self, p: str) -> str:
        if p.startswith("/tmp/"):
            return os.path.join(self.tmp_root, p.lstrip("/"))
        return p

    def available(self) -> None:
        if self.unavailable:
            raise ContainerError(self.unavailable)

    def image_exists(self, tag: str) -> bool:
        return self.present.get(tag, False)

    def build_image(self, tag: str, dockerfile: bytes, labels: Mapping[str, str], log: IO[bytes], timeout: float) -> None:
        self.builds.append((tag, dockerfile, dict(labels)))
        log.write(b"Step 1/1 : " + dockerfile.split(b"\n", 1)[0] + b"\n")
        if self.build_err:
            log.write(b"E: the build broke\n")
            raise ContainerError(self.build_err)
        self.present[tag] = True

    def image_env(self, tag: str) -> List[str]:
        return list(self.image_environ)

    def start(self, spec: ContainerSpec) -> str:
        if self.start_err:
            raise ContainerError(self.start_err)
        self.starts.append(spec)
        cid = "cid-%d" % len(self.starts)
        self.containers[cid] = dict(spec.labels)
        self.mounts[cid] = sorted(spec.volumes)
        self.volumes.update(spec.volumes)
        return cid

    def exec_argv(self, cid: str, workdir: str, env: Sequence[str], argv: Sequence[str]) -> List[str]:
        with self._mu:
            self.execs.append((cid, workdir, list(env), list(argv)))
        out = ["env", "-i", *self.image_environ]
        for kv in env:
            if kv.startswith("TMPDIR="):
                kv = "TMPDIR=" + self.map_tmp(kv[len("TMPDIR="):])
            out.append(kv)
        return out + list(argv)

    def client_env(self) -> Optional[Dict[str, str]]:
        return None

    def has_command(self, cid: str, name: str) -> bool:
        return self.commands.get(name, False)

    def reap(self, cid: str, remove: Sequence[str]) -> None:
        self.reaps.append(list(remove))
        for p in remove:
            shutil.rmtree(self.map_tmp(p), ignore_errors=True)
        if self.reap_err:
            raise ContainerError(self.reap_err)

    def remove(self, cid: str) -> None:
        self.removes.append(cid)
        if self.remove_err:
            raise ContainerError(self.remove_err)
        self.containers.pop(cid, None)

    def list_labelled(self, labels: Mapping[str, str], key: str) -> Dict[str, str]:
        self.listed.append((dict(labels), key))
        if self.list_err:
            raise ContainerError(self.list_err)
        return {
            cid: have.get(key, "")
            for cid, have in self.containers.items()
            if all(have.get(k) == v for k, v in labels.items())
        }

    def _in_use(self, volume: str) -> bool:
        return any(volume in self.mounts.get(cid, []) for cid in self.containers)

    def unused_volumes(self, name: str) -> List[str]:
        if self.volume_list_err:
            raise ContainerError(self.volume_list_err)
        return sorted(v for v in self.volumes if name in v and not self._in_use(v))

    def remove_volume(self, name: str) -> None:
        self.volume_removes.append(name)
        if self.volume_remove_err:
            raise ContainerError(self.volume_remove_err)
        if self._in_use(name):
            raise ContainerError("volume is in use")
        self.volumes.discard(name)

    def command_execs(self, needle: str) -> List[Tuple[str, str, List[str], List[str]]]:
        """The execs whose argv mentions needle."""
        return [e for e in self.execs if needle in " ".join(e[3])]
