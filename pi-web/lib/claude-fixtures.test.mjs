// The recordings of the real claude 2.1.312 (model switch, effort,
// compact) at their falsifiable points, and the e2e claude stub replaying them
// (lib/__fixtures__/claude-2.1.312/README.md).
import "#test-env";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtures = path.join(here, "__fixtures__", "claude-2.1.312");
const e2eStub = path.join(here, "..", "e2e", "claude-stub", "claude.mjs");
const jsonl = (file) => fs.readFileSync(path.join(fixtures, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));

test("model-switch/: claude refuses a bogus model with check_failed and switches to a full name in place", () => {
  const stdin = jsonl("model-switch/stdin.jsonl");
  assert.deepEqual(stdin.map((line) => line.request?.subtype ?? line.type), ["user", "set_model", "set_model", "user"]);
  const stream = jsonl("model-switch/stream.jsonl");
  const responses = stream.filter((event) => event.type === "control_response").map((event) => event.response);
  const refusal = responses.find((response) => response.request_id === stdin[1].request_id);
  assert.equal(refusal.subtype, "error");
  assert.equal(refusal.error_code, "check_failed");
  assert.match(refusal.error, /model not changed/);
  assert.equal(refusal.model, "claude-opus-5-5", "the refusal names the model kept");
  const success = responses.find((response) => response.request_id === stdin[2].request_id);
  assert.equal(success.subtype, "success");
  assert.deepEqual(success.response, { model: "claude-sonnet-5-5" });
  const assistants = stream.filter((event) => event.type === "assistant").map((event) => event.message.model);
  assert.deepEqual(assistants, ["claude-opus-5-5", "claude-sonnet-5-5"], "the turn after the switch runs on the new model");
  const ids = new Set(stream.map((event) => event.session_id).filter(Boolean));
  assert.equal(ids.size, 1, "the switch keeps the conversation");
  const transcript = jsonl("model-switch/transcript.jsonl");
  assert.deepEqual(transcript.filter((entry) => entry.type === "assistant").map((entry) => entry.message.model), ["claude-opus-5-5", "claude-sonnet-5-5"]);
  assert.ok(transcript.some((entry) => entry.type === "user" && String(entry.message.content).startsWith("<local-command-stdout>Set model to")), "claude's own /model bookkeeping entry is recorded");
});

test("effort/: apply_flag_settings changes the next request's effort on the live child", () => {
  const stdin = jsonl("effort/stdin.jsonl");
  const request = stdin.find((line) => line.type === "control_request");
  assert.deepEqual(request.request, { subtype: "apply_flag_settings", settings: { effortLevel: "max" } });
  const answer = jsonl("effort/stream.jsonl").find((event) => event.type === "control_response");
  assert.deepEqual(answer.response, { subtype: "success", request_id: request.request_id });
  const probe = jsonl("effort/runtime-probe.jsonl");
  assert.deepEqual(probe.map((entry) => entry.params.output_config?.effort), ["low", "max"]);
  const transcript = jsonl("effort/transcript.jsonl").filter((entry) => entry.type === "assistant");
  assert.deepEqual(transcript.map((entry) => entry.effort), ["low", "max"]);
});

test("compact/: a bare /compact line is claude's own compaction, with a boundary and its token figure", () => {
  const stdin = jsonl("compact/stdin.jsonl");
  assert.deepEqual(stdin.at(-1), { type: "user", message: { role: "user", content: "/compact" } });
  const stream = jsonl("compact/stream.jsonl");
  const boundary = stream.find((event) => event.type === "system" && event.subtype === "compact_boundary");
  assert.ok(boundary, "the stream holds compact_boundary");
  assert.equal(typeof boundary.compact_metadata.pre_tokens, "number");
  const results = stream.filter((event) => event.type === "result");
  assert.equal(results.length, 3, "the compaction ends with a result of its own");
  assert.ok(stream.indexOf(boundary) < stream.indexOf(results[2]));
  const transcript = jsonl("compact/transcript.jsonl");
  const marker = transcript.find((entry) => entry.type === "system" && entry.subtype === "compact_boundary");
  assert.equal(marker.compactMetadata.preTokens, boundary.compact_metadata.pre_tokens);
  assert.ok(transcript.some((entry) => entry.isCompactSummary === true && entry.isVisibleInTranscriptOnly === true), "the summary is claude's hidden entry");
  const before = transcript.slice(0, transcript.indexOf(marker)).filter((entry) => entry.type === "user" && entry.promptSource === "sdk");
  assert.equal(before.length, 2, "the pre-compaction user messages stay in the transcript");
});

test("the three recordings are scrubbed and listed in the README", () => {
  const readme = fs.readFileSync(path.join(fixtures, "README.md"), "utf8");
  for (const name of ["model-switch", "effort", "compact"]) {
    assert.ok(readme.includes(`\`${name}/\``), `README lists ${name}/`);
    for (const file of fs.readdirSync(path.join(fixtures, name))) {
      const text = fs.readFileSync(path.join(fixtures, name, file), "utf8");
      for (const leak of ["sk-ant", "/root/", "Bearer ", "api-request-blob", "prompt_snapshot"]) assert.ok(!text.includes(leak), `${name}/${file} holds ${leak}`);
    }
  }
});

/** Runs the e2e stub, writes `lines` one at a time (each after `until` saw its answer), returns stdout events. */
async function driveStub({ env = {}, args = [], steps }) {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pi-web-claude-fixtures-"));
  const cwd = path.join(scratch, "work");
  fs.mkdirSync(cwd);
  const child = spawn(process.execPath, [e2eStub, "--model", "claude-opus-5-5", "--session-id", "11111111-2222-4333-8444-555555555555", ...args], {
    cwd,
    env: { ...process.env, CLAUDE_CONFIG_DIR: path.join(scratch, "config"), PI_E2E_CLAUDE_STUB_LOG: "", ...env },
    stdio: ["pipe", "pipe", "ignore"],
  });
  const events = [];
  let buffer = "";
  let waiter = null;
  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    buffer += chunk;
    let at;
    while ((at = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, at);
      buffer = buffer.slice(at + 1);
      if (!line.trim()) continue;
      const event = JSON.parse(line);
      events.push(event);
      if (waiter?.until(event)) {
        const { resolve } = waiter;
        waiter = null;
        resolve();
      }
    }
  });
  const transcript = () => {
    const dir = path.join(scratch, "config", "projects");
    if (!fs.existsSync(dir)) return [];
    return fs.readdirSync(dir).flatMap((sub) => fs.readdirSync(path.join(dir, sub)).flatMap((file) =>
      fs.readFileSync(path.join(dir, sub, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line))));
  };
  const snapshots = [];
  try {
    for (const step of steps) {
      const answered = new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(`the stub did not answer ${JSON.stringify(step.line).slice(0, 80)}`)), 20_000);
        waiter = { until: step.until, resolve: () => { clearTimeout(timer); resolve(); } };
      });
      child.stdin.write(`${JSON.stringify(step.line)}\n`);
      await answered;
      snapshots.push(transcript());
    }
    return { events, snapshots };
  } finally {
    child.kill("SIGKILL");
    await new Promise((resolve) => (child.exitCode !== null || child.signalCode !== null ? resolve() : child.once("close", resolve)));
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
const user = (content) => ({ type: "user", message: { role: "user", content } });
const control = (id, request) => ({ type: "control_request", request_id: id, request });
const resultOf = (event) => event.type === "result";
const responseTo = (id) => (event) => event.type === "control_response" && event.response?.request_id === id;

test("e2e stub default mode: set_model switches (or refuses), effort is accepted, /compact writes its boundary before result", async () => {
  const { events, snapshots } = await driveStub({
    steps: [
      { line: control("bogus", { subtype: "set_model", model: "claude-bogus-0-0" }), until: responseTo("bogus") },
      { line: control("ok", { subtype: "set_model", model: "claude-sonnet-5-5" }), until: responseTo("ok") },
      { line: control("effort", { subtype: "apply_flag_settings", settings: { effortLevel: "max" } }), until: responseTo("effort") },
      { line: user("hello"), until: resultOf },
      { line: user("/compact"), until: resultOf },
    ],
  });
  const responses = events.filter((event) => event.type === "control_response").map((event) => event.response);
  assert.equal(responses[0].subtype, "error");
  assert.equal(responses[0].error_code, "check_failed");
  assert.deepEqual(responses[1].response, { model: "claude-sonnet-5-5" });
  assert.equal(responses[2].subtype, "success");
  assert.ok(events.filter((event) => event.type === "assistant").every((event) => event.message.model === "claude-sonnet-5-5"), "replies after the switch report the new model");
  assert.ok(events.some((event) => event.type === "system" && event.subtype === "compact_boundary" && typeof event.compact_metadata.pre_tokens === "number"));
  assert.ok(snapshots.at(-1).some((entry) => entry.subtype === "compact_boundary"), "the transcript holds the boundary when result arrives");
});

for (const [mode, dir, steps, check] of [
  ["switch", "model-switch", [
    { line: user("one"), until: resultOf },
    { line: control("a", { subtype: "set_model", model: "claude-bogus-0-0" }), until: responseTo("a") },
    { line: control("b", { subtype: "set_model", model: "claude-sonnet-5-5" }), until: responseTo("b") },
    { line: user("two"), until: resultOf },
  ], ({ events, snapshots }) => {
    assert.equal(events.find(responseTo("a")).response.error_code, "check_failed");
    assert.equal(events.filter((event) => event.type === "assistant").at(-1).message.model, "claude-sonnet-5-5");
    assert.equal(snapshots.at(-1).filter((entry) => entry.type === "assistant").at(-1).message.model, "claude-sonnet-5-5");
  }],
  ["effort", "effort", [
    { line: user("low"), until: resultOf },
    { line: control("e", { subtype: "apply_flag_settings", settings: { effortLevel: "max" } }), until: responseTo("e") },
    { line: user("max"), until: resultOf },
  ], ({ events }) => {
    assert.equal(events.find(responseTo("e")).response.subtype, "success");
    assert.equal(events.filter(resultOf).length, 2);
  }],
  ["compact", "compact", [
    { line: user("one"), until: resultOf },
    { line: user("two"), until: resultOf },
    { line: user("/compact"), until: resultOf },
  ], ({ events, snapshots }) => {
    assert.ok(events.some((event) => event.subtype === "compact_boundary"));
    assert.ok(snapshots.at(-1).some((entry) => entry.subtype === "compact_boundary"), "the boundary is in the transcript before result");
  }],
]) {
  test(`e2e stub ${mode} mode replays the ${dir}/ recording under this child's session id`, async () => {
    const run = await driveStub({ env: { PI_E2E_CLAUDE_STUB_MODE: mode, [`PI_E2E_CLAUDE_${mode.toUpperCase()}_FIXTURE`]: path.join(fixtures, dir) }, steps });
    check(run);
    const ids = new Set(run.events.map((event) => event.session_id).filter(Boolean));
    assert.deepEqual([...ids], ["11111111-2222-4333-8444-555555555555"]);
  });
}

test("language/: the 中文 instruction run answers an English prompt in Simplified Chinese, scrubbed and listed", () => {
  const cjk = /[一-鿿]/;
  const [line] = jsonl("language/stdin.jsonl");
  assert.equal(line.type, "user");
  assert.equal(line.message.content, "In two short sentences, what does `git rebase` do?");
  assert.ok(!cjk.test(line.message.content), "the prompt itself is English");
  const stream = jsonl("language/stream.jsonl");
  const init = stream.find((event) => event.type === "system" && event.subtype === "init");
  assert.equal(init.claude_code_version, "2.1.312");
  assert.equal(init.model, "claude-haiku-5-5");
  const result = stream.find((event) => event.type === "result");
  assert.equal(result.subtype, "success");
  assert.match(result.result, cjk, "the reply is Chinese");
  assert.ok(result.result.includes("git rebase"), "the technical term stays as written");
  const transcript = jsonl("language/transcript.jsonl");
  const text = transcript.filter((entry) => entry.type === "assistant").flatMap((entry) => entry.message.content)
    .filter((block) => block.type === "text").map((block) => block.text).join("");
  assert.match(text, cjk);
  assert.equal(text, result.result);
  const readme = fs.readFileSync(path.join(fixtures, "README.md"), "utf8");
  assert.ok(readme.includes("`language/`"), "README lists language/");
  assert.ok(readme.includes("--append-system-prompt \"Use Simplified Chinese (简体中文) for replies, narration and reports."), "README states the recorded argv");
  for (const file of ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]) {
    assert.ok(readme.includes(`language/${file}`), `README lists language/${file}`);
    const raw = fs.readFileSync(path.join(fixtures, "language", file), "utf8");
    for (const leak of ["sk-ant", "/root/", "Bearer ", "api-request", "claude-lang-rec-", "claude-lang-cfg-"]) assert.ok(!raw.includes(leak), `language/${file} holds ${leak}`);
  }
  assert.deepEqual(fs.readdirSync(path.join(fixtures, "language")).sort(), ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]);
});
