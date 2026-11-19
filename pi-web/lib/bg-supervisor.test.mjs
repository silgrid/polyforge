// carries: lib/bg-supervisor.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  BG_LOG_MAX_BYTES_DEFAULT,
  spawnBgSupervisedTask,
} from "./bg-supervisor.ts";

const POLL_STEP_MS = 25;
const HOST_DEATH_DEADLINE_MS = 10_000;
const STOP_DEADLINE_MS = 5_000;

async function pollUntil(condition, deadlineMs, what) {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    if (await condition()) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, POLL_STEP_MS));
  }
}

/** A group exists while kill(-pgid, 0) finds it (EPERM still means it exists). */
function groupAlive(pgid) {
  if (!Number.isInteger(pgid) || pgid <= 0) return false;
  try {
    process.kill(-pgid, 0);
    return true;
  } catch (error) {
    return error.code === "EPERM";
  }
}

async function spawnTask(command, options = {}) {
  const dir = await mkdtemp(join(tmpdir(), "bg-supervisor-"));
  const logPath = join(dir, "log");
  const task = spawnBgSupervisedTask({ command, cwd: dir, env: process.env, logPath, ...(options.logMaxBytes !== undefined ? { logMaxBytes: options.logMaxBytes } : {}) });
  return { dir, logPath, ...task };
}

function cleanup(task) {
  // Best-effort, bounded by the supervisor's own escalation; a gone group is success.
  void task.stop().catch(() => undefined);
}

test("the default log cap is the ticket's 64 MiB", () => {
  assert.equal(BG_LOG_MAX_BYTES_DEFAULT, 64 * 1024 * 1024);
});

test("a background task runs in its own process group and writes its output to the log", async (t) => {
  const task = await spawnTask("echo hello");
  t.after(() => cleanup(task));
  // Red before the implementation: the skeleton reports pgid 0.
  assert.ok(task.pgid > 0, "the wrapper pid (the task's pgid) must be positive");
  assert.notEqual(task.pgid, process.pid, "the task must not share pi-web's own process");
  assert.ok(groupAlive(task.pgid), "the task's process group must exist");

  await pollUntil(async () => {
    try {
      return (await readFile(task.logPath, "utf8")) === "hello\n";
    } catch (error) {
      if (error.code === "ENOENT") return false; // the wrapper has not created the log yet
      throw error;
    }
  }, 5_000, "the log to hold the command's output");
  const result = await task.done;
  assert.equal(result.exitCode, 0);
  assert.equal(result.reason, null);
  assert.ok((result.bytesWritten ?? 0) >= Buffer.byteLength("hello\n"), "the log byte count must cover the output");
  await pollUntil(() => !groupAlive(task.pgid), 5_000, "the group to be gone once the task ended");
  await rm(task.dir, { recursive: true, force: true });
});

test("the task's done promise reports the shell's exit code and signal", async (t) => {
  const task = await spawnTask("exit 7");
  t.after(() => cleanup(task));
  const result = await task.done;
  assert.equal(result.exitCode, 7);
  assert.equal(result.signal, null);
  await rm(task.dir, { recursive: true, force: true });
});

test("task.stop() kills the whole process group, grandchildren included, and is idempotent", async (t) => {
  const task = await spawnTask("sleep 300 & sleep 300");
  t.after(() => cleanup(task));
  assert.ok(groupAlive(task.pgid), "the task's process group must exist before the stop");

  await task.stop();
  await pollUntil(() => !groupAlive(task.pgid), STOP_DEADLINE_MS, "the stopped group to disappear");
  const result = await task.done;
  assert.equal(result.reason, "stopped");
  // A second stop of the same (gone) group must not throw.
  await task.stop();
  await rm(task.dir, { recursive: true, force: true });
});

test("the wrapper kills the whole group when its host process is SIGKILLed", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "bg-supervisor-host-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const hostPath = join(dir, "host.mjs");
  const libPath = new URL("./bg-supervisor.ts", import.meta.url).pathname;
  const taskDir = join(dir, "task");
  await mkdir(taskDir);
  // A stand-in for the pi-web server: it spawns the supervised task and stays
  // alive, exactly as the server does, until the test kills it.
  await writeFile(hostPath, `import { spawnBgSupervisedTask } from ${JSON.stringify(libPath)};
const task = spawnBgSupervisedTask({ command: "sleep 300", cwd: ${JSON.stringify(taskDir)}, env: process.env, logPath: ${JSON.stringify(join(taskDir, "log"))} });
process.stdout.write("READY " + task.pgid + "\\n");
setInterval(() => {}, 1000);
`);
  const host = spawn(process.execPath, ["--experimental-strip-types", hostPath], { stdio: ["ignore", "pipe", "inherit"] });
  t.after(() => { try { host.kill("SIGKILL"); } catch { /* already gone */ } });
  let pgid = 0;
  let ready = "";
  await new Promise((resolve, reject) => {
    const deadline = setTimeout(() => reject(new Error(`host never reported its task; got: ${ready}`)), 10_000);
    host.stdout.on("data", (chunk) => {
      ready += chunk.toString("utf8");
      const match = /^READY (\d+)\s*$/.exec(ready.trim());
      if (match) {
        clearTimeout(deadline);
        pgid = Number(match[1]);
        resolve();
      }
    });
    host.on("error", (error) => { clearTimeout(deadline); reject(error); });
  });
  assert.ok(pgid > 0, "the host must report the task's pgid");
  assert.ok(groupAlive(pgid), "the task group must exist while its host lives");

  // The supervisor contract: the host's SIGKILL death must take the group with it.
  process.kill(host.pid, "SIGKILL");
  await pollUntil(() => !groupAlive(pgid), HOST_DEATH_DEADLINE_MS, "the group to die with its host");
});

test("a log past its byte cap stops the process group with a log-limit reason", async (t) => {
  const task = await spawnTask("while :; do echo abcdefgh; done", { logMaxBytes: 8 * 1024 });
  t.after(() => cleanup(task));
  const result = await task.done;
  assert.equal(result.reason, "log-limit");
  const size = (await stat(task.logPath)).size;
  assert.equal(size, 8 * 1024, "the log fills the cap exactly, to the byte");
  await pollUntil(() => !groupAlive(task.pgid), 5_000, "the capped group to disappear");
  await rm(task.dir, { recursive: true, force: true });
});

test("the wrapper drains the pipes before killing the group, so the log keeps the command's last bytes", async (t) => {
  // Past the 64 KiB pipe capacity: when the shell exits, the command's tail is
  // still in flight inside the pipe, and only a drained shutdown keeps it.
  const total = 70_000;
  const task = await spawnTask(`head -c ${total} /dev/zero | tr '\\0' x`);
  t.after(() => cleanup(task));
  const result = await task.done;
  assert.equal(result.exitCode, 0);
  assert.equal((result.bytesWritten ?? 0), total, "the report counts every byte the command wrote");
  await pollUntil(() => !groupAlive(task.pgid), 5_000, "the ended group to disappear");
  const size = (await stat(task.logPath)).size;
  assert.equal(size, total, "the log holds the whole output, last bytes included");
  await rm(task.dir, { recursive: true, force: true });
});
