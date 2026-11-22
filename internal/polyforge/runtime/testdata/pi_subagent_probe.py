#!/usr/bin/env python3
"""Live pi probes of the daemon-owned step agent (polyforge#228 AC1/AC2).

The caller supplies a step-agent copy produced by the daemon's own refresh
(``TestExportPiSubagentProbeStepAgent`` writes one to ``PF_PROBE_EXPORT_DIR``).
Everything else happens in a scratch tree: a throwaway HOME, TMPDIR and cwd,
a loopback OpenAI-compatible provider with a literal fake key, and a minimal
environment that inherits none of the operator's or daemon's configuration.
No request leaves 127.0.0.1 and no real credential is read.

``--schedule`` drives a REAL pi whose one model turn asks for two subagent
calls, and reports the raw request/response times of the two children (the
scheduler proof) plus the children's /proc argv.

The report carries raw observations (times, argv, booleans, digests, capture
paths). The Go tests derive the expectations; the script asserts nothing
about what a result should be.
"""

import argparse
import hashlib
import json
import os
import shutil
import signal
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SCHEMA = "pf-pi-subagent-probe/1"
PROVIDER_ID = "pf-probe"
MODEL_ID = "pf-probe"
ISOLATION_FLAGS = ["--no-skills", "--no-context-files", "--no-prompt-templates", "--no-extensions", "--no-approve"]
# the parent's profile: the extension must load, so --no-extensions is absent
PARENT_FLAGS = ["--no-skills", "--no-context-files", "--no-prompt-templates", "--no-approve"]


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def now_ms(t0):
    return int((time.monotonic() - t0) * 1000)


# ---------------------------------------------------------------------------
# the loopback provider


class Provider:
    """Thread-safe request recorder plus the schedule scenario's answers."""

    def __init__(self, hold_ms=900):
        self.hold_ms = hold_ms
        self.lock = threading.Lock()
        self.requests = []
        self.alpha_request = 0
        self.alpha_response = 0
        self.beta_request = 0
        self.tasks = []
        self.t0 = 0
        self.capture_dir = None

    def record(self, body):
        text = json.dumps(body, ensure_ascii=False)
        offers = any((t.get("function") or {}).get("name") == "subagent" for t in (body.get("tools") or []))
        child = ("Task: alpha" in text or "Task: beta" in text) and not offers
        task = ""
        if child:
            task = "alpha" if "Task: alpha" in text else "beta"
        with self.lock:
            n = len(self.requests) + 1
            at = now_ms(self.t0)
            self.requests.append({"n": n, "at": at, "child": child, "task": task,
                                  "offers_subagent": offers, "body": body})
            if task == "alpha":
                self.alpha_request = at
            elif task == "beta":
                self.beta_request = at
            if task and task not in self.tasks:
                self.tasks.append(task)
        self._capture(n, body)
        return task, offers

    def _capture(self, n, body):
        if not self.capture_dir:
            return
        try:
            (Path(self.capture_dir) / ("request-%03d.json" % n)).write_text(
                json.dumps(body, ensure_ascii=False, indent=1), encoding="utf-8")
        except OSError:
            pass

    def answer(self, body):
        task, offers = self.record(body)
        if task == "alpha":
            time.sleep(self.hold_ms / 1000.0)
            with self.lock:
                self.alpha_response = now_ms(self.t0)
            return text_chunks("alpha done")
        if task == "beta":
            return text_chunks("beta done")
        tool_results = sum(1 for m in (body.get("messages") or []) if m.get("role") == "tool")
        if offers and tool_results == 0:
            return tool_call_chunks(
                [("call_alpha", {"agent": "worker", "task": "alpha"}),
                 ("call_beta", {"agent": "worker", "task": "beta"})])
        return text_chunks("probe complete")


def chunk(delta, finish=None):
    c = {"id": "chatcmpl-pf-probe", "object": "chat.completion.chunk", "created": 1, "model": MODEL_ID,
         "choices": [{"index": 0, "delta": delta}]}
    if finish:
        c["choices"][0]["finish_reason"] = finish
    return c


def text_chunks(text):
    return [chunk({"role": "assistant", "content": text}), chunk({}, "stop")]


def tool_call_chunks(calls):
    tool_calls = []
    for i, (call_id, args) in enumerate(calls):
        tool_calls.append({"index": i, "id": call_id, "type": "function",
                           "function": {"name": "subagent", "arguments": json.dumps(args)}})
    return [chunk({"role": "assistant", "tool_calls": tool_calls}), chunk({}, "tool_calls")]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def do_POST(self):
        if not self.path.endswith("/chat/completions"):
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            self.send_error(400)
            return
        chunks = self.server.provider.answer(body)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        for c in chunks:
            self.wfile.write(("data: " + json.dumps(c) + "\n\n").encode())
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


# ---------------------------------------------------------------------------
# scratch environment and the real pi process


def write_models(agent_dir, base_url):
    doc = {"providers": {PROVIDER_ID: {
        "baseUrl": base_url,
        "api": "openai-completions",
        "apiKey": "pf-probe-fake",
        "models": [{"id": MODEL_ID, "name": MODEL_ID,
                    "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}],
    }}}
    (Path(agent_dir) / "models.json").write_text(json.dumps(doc) + "\n", encoding="utf-8")


def probe_env(agent_dir, home_dir, tmp_dir):
    return {
        "PATH": os.environ.get("PATH") or "/usr/local/bin:/usr/bin:/bin",
        "HOME": str(home_dir),
        "TMPDIR": str(tmp_dir),
        "LANG": "C.UTF-8",
        "PI_CODING_AGENT_DIR": str(agent_dir),
        "PI_OFFLINE": "1",
        "PI_SKIP_VERSION_CHECK": "1",
        "PI_TELEMETRY": "0",
    }


def proc_cmdline(pid):
    try:
        with open("/proc/%d/cmdline" % pid, "rb") as f:
            raw = f.read()
    except OSError:
        return []
    return [part.decode("utf-8", "replace") for part in raw.split(b"\0") if part]


def scan_descendants(root_pid, seen, stop):
    """Poll /proc for root_pid's descendants until stop is set; each new
    command line is recorded once."""
    while not stop.is_set():
        parents = {}
        try:
            entries = os.listdir("/proc")
        except OSError:
            return
        for entry in entries:
            if not entry.isdigit():
                continue
            try:
                with open("/proc/%s/stat" % entry, "rb") as f:
                    stat = f.read().decode("utf-8", "replace")
                parents[int(entry)] = int(stat[stat.rfind(")") + 2:].split()[1])
            except (OSError, ValueError, IndexError):
                continue
        tree, queue = set(), [root_pid]
        while queue:
            p = queue.pop()
            for pid, ppid in parents.items():
                if ppid == p and pid not in tree:
                    tree.add(pid)
                    queue.append(pid)
        for pid in tree:
            argv = proc_cmdline(pid)
            if argv and argv not in seen:
                seen.append(argv)
        time.sleep(0.02)


def rpc_session(pi_bin, agent_dir, home_dir, tmp_dir, work_dir, prompt, timeout_s, child_seen=None):
    """Run one real pi RPC session: send the prompt, collect records until
    agent_settled, the process exits, or the deadline. Returns the parsed
    records, the exit code and the stderr tail."""
    args = ["--mode", "rpc", "--no-session"] + PARENT_FLAGS + [
        "--model", "%s/%s" % (PROVIDER_ID, MODEL_ID)]
    proc = subprocess.Popen(
        [pi_bin] + args,
        cwd=str(work_dir),
        env=probe_env(agent_dir, home_dir, tmp_dir),
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
        start_new_session=True,
    )
    records, stderr_lines = [], []
    done, stop = threading.Event(), threading.Event()
    if child_seen is not None:
        threading.Thread(target=scan_descendants, args=(proc.pid, child_seen, stop), daemon=True).start()

    def read_stdout():
        for line in proc.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            records.append(rec)
            if rec.get("type") == "agent_settled":
                done.set()

    def read_stderr():
        for line in proc.stderr:
            stderr_lines.append(line)
            if len(stderr_lines) > 200:
                del stderr_lines[0]

    out_t = threading.Thread(target=read_stdout, daemon=True)
    err_t = threading.Thread(target=read_stderr, daemon=True)
    out_t.start()
    err_t.start()
    try:
        try:
            proc.stdin.write(json.dumps({"id": "probe", "type": "prompt", "message": prompt}) + "\n")
            proc.stdin.flush()
        except (BrokenPipeError, OSError):
            pass
        deadline = time.monotonic() + timeout_s
        while time.monotonic() < deadline:
            if done.is_set() or proc.poll() is not None:
                break
            time.sleep(0.05)
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            pass
    finally:
        if proc.poll() is None:
            try:
                os.killpg(proc.pid, signal.SIGTERM)
            except OSError:
                pass
            try:
                proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except OSError:
                    pass
                try:
                    proc.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    pass
        stop.set()
    out_t.join(timeout=2)
    err_t.join(timeout=2)
    return records, proc.returncode, "".join(stderr_lines[-20:])


# ---------------------------------------------------------------------------
# modes


def prepare_agent(step_agent_copy, root):
    agent_dir = root / "agent"
    if agent_dir.exists():
        shutil.rmtree(agent_dir)
    shutil.copytree(step_agent_copy, agent_dir)
    return agent_dir


def run_schedule(ns, step_agent_copy, root, provider, t0):
    agent_dir = prepare_agent(step_agent_copy, root)
    work_dir, home_dir, tmp_dir = root / "work", root / "home", root / "tmp"
    for d in (work_dir, home_dir, tmp_dir):
        d.mkdir(parents=True, exist_ok=True)
    provider.t0 = t0
    provider.capture_dir = root / "captures"
    provider.capture_dir.mkdir(exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = True
    server.provider = provider
    threading.Thread(target=server.serve_forever, daemon=True).start()
    write_models(agent_dir, "http://127.0.0.1:%d/v1" % server.server_address[1])
    child_seen = []
    try:
        records, code, stderr = rpc_session(
            ns.pi, agent_dir, home_dir, tmp_dir, work_dir,
            "run the schedule probe", ns.timeout, child_seen)
    finally:
        server.shutdown()
        server.server_close()
    tool_ends = sum(1 for r in records
                    if r.get("type") == "tool_execution_end" and r.get("toolName") == "subagent")
    return {
        "sequential": {
            "alpha_request_ms": provider.alpha_request,
            "alpha_response_ms": provider.alpha_response,
            "beta_request_ms": provider.beta_request,
            "tasks": provider.tasks,
            "parent_tool_end": tool_ends,
            "parent_settled": any(r.get("type") == "agent_settled" for r in records),
        },
        "child_argv": child_seen,
        "child_argv_note": "scanned from /proc while the parent ran; empty means no descendant was visible",
        "captures": str(provider.capture_dir),
        "parent_exit": code,
        "stderr_tail": stderr,
        "requests": provider.requests,
    }


def pi_version(pi_bin):
    try:
        env = {"PATH": os.environ.get("PATH") or "/usr/bin:/bin", "HOME": os.environ.get("HOME") or "/root"}
        out = subprocess.run([pi_bin, "--version"], capture_output=True, text=True, timeout=20, env=env)
        return out.stdout.strip().split("\n")[0]
    except (OSError, subprocess.SubprocessError):
        return ""


def step_agent_digests(step_agent_copy):
    copy = Path(step_agent_copy)
    worker = copy / "agents" / "worker.md"
    ext = copy / "extensions" / "subagent" / "index.ts"
    return {
        "worker_present": worker.is_file(),
        "extension_present": ext.is_file(),
        "worker_sha256": sha256(worker) if worker.is_file() else "",
        "extension_sha256": sha256(ext) if ext.is_file() else "",
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--step-agent-copy", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--schedule", action="store_true")
    ap.add_argument("--pi", default="pi")
    ap.add_argument("--timeout", type=int, default=120)
    ap.add_argument("--work-dir", type=Path, default=None)
    ns = ap.parse_args()
    scratch = None
    if ns.work_dir is not None:
        root = ns.work_dir.resolve()
        root.mkdir(parents=True, exist_ok=True)
    else:
        scratch = Path(tempfile.mkdtemp(prefix="pf-probe-"))
        root = scratch
    t0 = time.monotonic()
    report = {
        "schema": SCHEMA,
        "mode": "schedule" if ns.schedule else "",
        "pi_version": pi_version(ns.pi),
        "step_agent_copy": str(ns.step_agent_copy),
        "digests": step_agent_digests(ns.step_agent_copy),
        "work_dir": str(root),
        "ok": False,
    }
    try:
        if ns.schedule:
            provider = Provider(hold_ms=900)
            report.update(run_schedule(ns, ns.step_agent_copy, root, provider, t0))
            latched = report["sequential"]
            report["ok"] = (
                len(latched["tasks"]) == 2
                and latched["alpha_request_ms"] > 0
                and latched["alpha_response_ms"] >= latched["alpha_request_ms"]
                and latched["beta_request_ms"] > 0
                and latched["parent_settled"]
            )
        else:
            report["error"] = "nothing to do: pass --schedule"
    except Exception as exc:
        report["error"] = "%s: %s" % (type(exc).__name__, exc)
    report["elapsed_ms"] = now_ms(t0)
    ns.out.parent.mkdir(parents=True, exist_ok=True)
    ns.out.write_text(json.dumps(report, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    if scratch is not None:
        shutil.rmtree(scratch, ignore_errors=True)
    if not report["ok"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
