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

``--capture`` runs the eight automatic-loading probes of #165 §2 twice over
one scratch tree: the positive control reproduces old loading (no isolation
flags, project resources approved), the negative control is the production
child profile (the five isolation flags). It also runs the
``.pi/agents/worker.md`` shadow case through the real builtin extension and
the fork's project-scope refusal.

The report carries raw observations (times, argv, per-probe booleans,
digests, capture paths). The Go tests derive the expectations; the script
asserts nothing about what a result should be.
"""

import argparse
import hashlib
import json
import os
import secrets
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
BUILTIN_WORKER_MARKER = "You are the daemon built in worker."


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def now_ms(t0):
    return int((time.monotonic() - t0) * 1000)


def nonce():
    return secrets.token_hex(4).upper()


# ---------------------------------------------------------------------------
# the loopback provider


class Provider:
    """Thread-safe request recorder plus the scenarios' answers."""

    def __init__(self, mode, hold_ms=900):
        self.mode = mode
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
        child = ("Task: alpha" in text or "Task: beta" in text or "Task: shadow probe" in text) and not offers
        task = ""
        if child:
            for candidate in ("alpha", "beta", "shadow probe"):
                if "Task: " + candidate in text:
                    task = candidate
                    break
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
            (self.capture_dir / ("request-%03d.json" % n)).write_text(
                json.dumps(body, ensure_ascii=False, indent=1), encoding="utf-8")
        except OSError:
            pass

    def tool_results(self, body):
        return sum(1 for m in (body.get("messages") or []) if m.get("role") == "tool")

    def answer(self, body):
        task, offers = self.record(body)
        if task == "alpha":
            time.sleep(self.hold_ms / 1000.0)
            with self.lock:
                self.alpha_response = now_ms(self.t0)
            return text_chunks("alpha done")
        if task == "beta":
            return text_chunks("beta done")
        if task == "shadow probe":
            return text_chunks("shadow child done")
        results = self.tool_results(body)
        if self.mode == "schedule" and offers and results == 0:
            return tool_call_chunks(
                [("call_alpha", {"agent": "worker", "task": "alpha"}),
                 ("call_beta", {"agent": "worker", "task": "beta"})])
        if self.mode == "shadow" and offers and results == 0:
            return tool_call_chunks([("call_shadow", {"agent": "worker", "task": "shadow probe"})])
        if self.mode == "shadow" and offers and results == 1:
            return tool_call_chunks(
                [("call_scope", {"agent": "worker", "task": "project probe", "agentScope": "project"})])
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


def rpc(pi_bin, agent_dir, home_dir, tmp_dir, work_dir, flags, timeout_s,
        prompt=None, command=None, child_seen=None):
    """One real pi RPC session. Sends either a prompt (waiting for
    agent_settled) or one command (waiting for its response). Returns the
    parsed records, the response data (commands only) and the stderr tail."""
    args = ["--mode", "rpc", "--no-session"] + flags + ["--model", "%s/%s" % (PROVIDER_ID, MODEL_ID)]
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
            if command is not None:
                if rec.get("type") == "response" and rec.get("command") == command.get("type"):
                    done.set()
            elif rec.get("type") == "agent_settled":
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
        payload = command if command is not None else {"id": "probe", "type": "prompt", "message": prompt}
        try:
            proc.stdin.write(json.dumps(payload) + "\n")
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
    data = {}
    if command is not None:
        for rec in records:
            if rec.get("type") == "response" and rec.get("command") == command.get("type"):
                data = rec.get("data") or {}
                break
    return records, data, "".join(stderr_lines[-20:])


# ---------------------------------------------------------------------------
# probe assets


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def skill_md(name, marker):
    return ("---\nname: %s\ndescription: %s probe skill\n---\n\n%s skill body.\n"
            % (name, marker, name))


def prompt_md(marker):
    return "---\ndescription: %s probe prompt template\n---\n\nProbe prompt body.\n" % marker


def lay_probe_assets(agent_dir, project):
    """Write the eight probes' marker files plus the shadow worker. Returns
    {probe id: {path, marker, channel}}."""
    marks = {
        "agent-agents": (agent_dir / "AGENTS.md", "PFPROBE-AG-" + nonce(), "system"),
        "agent-skill": (agent_dir / "skills" / "probe-agent-skill" / "SKILL.md", "PFPROBE-AS-" + nonce(), "commands"),
        "agent-prompt": (agent_dir / "prompts" / "probe-agent-prompt.md", "PFPROBE-AP-" + nonce(), "commands"),
        "cwd-agents": (project / "AGENTS.md", "PFPROBE-CA-" + nonce(), "system"),
        "cwd-agents-skills": (project / ".agents" / "skills" / "probe-cwd-agents-skill" / "SKILL.md", "PFPROBE-CS-" + nonce(), "commands"),
        "cwd-pi-skills": (project / ".pi" / "skills" / "probe-cwd-pi-skill" / "SKILL.md", "PFPROBE-PS-" + nonce(), "commands"),
        "cwd-pi-prompts": (project / ".pi" / "prompts" / "probe-cwd-pi-prompt.md", "PFPROBE-PP-" + nonce(), "commands"),
        "workspace-agents": (project.parent / "AGENTS.md", "PFPROBE-WA-" + nonce(), "system"),
    }
    for pid, (path, marker, _channel) in marks.items():
        if pid.endswith("-agents"):
            write(path, marker + "\n")
        elif pid.endswith("-skill") or pid == "cwd-agents-skills" or pid == "cwd-pi-skills":
            write(path, skill_md(path.parent.name, marker))
        else:
            write(path, prompt_md(marker))
    # the shadow a project-scope discovery would prefer
    shadow_marker = "PFPROBE-SHADOW-" + nonce()
    write(project / ".pi" / "agents" / "worker.md",
          "---\nname: worker\ndescription: shadow probe worker\n---\n\n%s this shadow must never reach a child.\n"
          % shadow_marker)
    return {pid: {"path": str(path), "marker": marker, "channel": channel}
            for pid, (path, marker, channel) in marks.items()}, shadow_marker


class ProbeServer:
    def __init__(self, provider):
        self.provider = provider
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.httpd.daemon_threads = True
        self.httpd.provider = provider
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.base = "http://127.0.0.1:%d/v1" % self.httpd.server_address[1]

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def run_control(ns, agent_dir, dirs, project, provider, flags):
    server = ProbeServer(provider)
    write_models(agent_dir, server.base)
    provider.t0 = now_ms(time.monotonic())
    provider.capture_dir = dirs["captures"]
    try:
        records, _data, stderr = rpc(ns.pi, agent_dir, dirs["home"], dirs["tmp"], project,
                                     flags, ns.timeout, prompt="run the isolation probe")
        _recs, commands, cstderr = rpc(ns.pi, agent_dir, dirs["home"], dirs["tmp"], project,
                                       flags, ns.timeout, command={"type": "get_commands"})
    finally:
        server.close()
    return records, commands, stderr or cstderr, provider


def judge_control(assets, requests, commands):
    req_text = "\n".join(json.dumps(r["body"], ensure_ascii=False) for r in requests)
    cmd_text = json.dumps(commands, ensure_ascii=False)
    out = {}
    for pid, a in assets.items():
        visible = a["marker"] in (req_text if a["channel"] == "system" else cmd_text)
        out[pid] = {"marker": a["marker"], "channel": a["channel"], "visible": visible}
    return out


def run_capture(ns, step_agent_copy, root, t0):
    agent_dir = prepare_agent(step_agent_copy, root)
    dirs = {name: root / name for name in ("home", "tmp", "captures")}
    for d in dirs.values():
        d.mkdir(parents=True, exist_ok=True)
    project = root / "workspace" / "project"
    project.mkdir(parents=True, exist_ok=True)
    assets, shadow_marker = lay_probe_assets(agent_dir, project)

    controls = {}
    for name, flags in (("positive", ["--approve"]), ("negative", list(ISOLATION_FLAGS))):
        provider = Provider("capture")
        provider.capture_dir = dirs["captures"] / name
        provider.capture_dir.mkdir(parents=True, exist_ok=True)
        records, commands, stderr, provider = run_control(ns, agent_dir, dirs, project, provider, flags)
        controls[name] = {
            "flags": flags,
            "probes": judge_control(assets, provider.requests, commands),
            "requests": len(provider.requests),
            "commands": len((commands or {}).get("commands") or []),
            "stderr_tail": stderr,
        }

    # the shadow case: the real builtin extension over the shadowing cwd
    provider = Provider("shadow")
    provider.capture_dir = dirs["captures"] / "shadow"
    provider.capture_dir.mkdir(parents=True, exist_ok=True)
    child_seen = []
    server = ProbeServer(provider)
    write_models(agent_dir, server.base)
    provider.t0 = now_ms(time.monotonic())
    try:
        records, _data, stderr = rpc(ns.pi, agent_dir, dirs["home"], dirs["tmp"], project,
                                     list(PARENT_FLAGS), ns.timeout,
                                     prompt="run the shadow probe", child_seen=child_seen)
    finally:
        server.close()
    ends = [r for r in records if r.get("type") == "tool_execution_end" and r.get("toolName") == "subagent"]
    worker_result = {"agent": "", "agent_source": "", "exit_code": 0, "is_error": False}
    scope_result = {"is_error": False, "refusal": False}
    if ends:
        res = ends[0].get("result") or {}
        results = ((res.get("details") or {}).get("results") or [{}])
        worker_result = {
            "agent": results[0].get("agent", ""),
            "agent_source": results[0].get("agentSource", ""),
            "exit_code": results[0].get("exitCode", 0),
            "is_error": bool(res.get("isError")),
        }
    if len(ends) > 1:
        res = ends[1].get("result") or {}
        text = json.dumps(res, ensure_ascii=False)
        scope_result = {
            "is_error": bool(res.get("isError")),
            "refusal": "accepts exactly agent=worker" in text,
        }
    child_bodies = [json.dumps(r["body"], ensure_ascii=False) for r in provider.requests
                    if r["task"] == "shadow probe"]
    child_text = "\n".join(child_bodies)
    return {
        "probe_paths": {pid: a["path"] for pid, a in assets.items()},
        "controls": controls,
        "shadow": {
            "child_requests": len(child_bodies),
            "child_argv": child_seen,
            "builtin_worker_visible": BUILTIN_WORKER_MARKER in child_text,
            "shadow_marker_visible": shadow_marker in child_text,
            "shadow_marker": shadow_marker,
            "worker_result": worker_result,
            "project_scope_result": scope_result,
            "parent_stderr_tail": stderr,
        },
        "captures": str(dirs["captures"]),
        "requests": provider.requests,
        "ok": True,
    }


# ---------------------------------------------------------------------------
# schedule mode


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
    server = ProbeServer(provider)
    write_models(agent_dir, server.base)
    child_seen = []
    try:
        records, _data, stderr = rpc(ns.pi, agent_dir, home_dir, tmp_dir, work_dir,
                                     list(PARENT_FLAGS), ns.timeout,
                                     prompt="run the schedule probe", child_seen=child_seen)
    finally:
        server.close()
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
        "parent_exit": 0,
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
    ap.add_argument("--capture", action="store_true")
    ap.add_argument("--schedule", action="store_true")
    ap.add_argument("--pi", default="pi")
    ap.add_argument("--timeout", type=int, default=120)
    ap.add_argument("--work-dir", type=Path, default=None)
    ns = ap.parse_args()
    if ns.capture and ns.schedule:
        ap.error("pass exactly one of --capture and --schedule")
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
        "mode": "capture" if ns.capture else ("schedule" if ns.schedule else ""),
        "pi_version": pi_version(ns.pi),
        "step_agent_copy": str(ns.step_agent_copy),
        "digests": step_agent_digests(ns.step_agent_copy),
        "work_dir": str(root),
        "ok": False,
    }
    try:
        if ns.schedule:
            provider = Provider("schedule", hold_ms=900)
            report.update(run_schedule(ns, ns.step_agent_copy, root, provider, t0))
            latched = report["sequential"]
            report["ok"] = (
                len(latched["tasks"]) == 2
                and latched["alpha_request_ms"] > 0
                and latched["alpha_response_ms"] >= latched["alpha_request_ms"]
                and latched["beta_request_ms"] > 0
                and latched["parent_settled"]
            )
        elif ns.capture:
            report.update(run_capture(ns, ns.step_agent_copy, root, t0))
        else:
            report["error"] = "nothing to do: pass --schedule or --capture"
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
