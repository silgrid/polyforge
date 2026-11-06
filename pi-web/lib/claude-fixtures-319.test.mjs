// carries: lib/__fixtures__/claude-2.1.319/*
// The real claude 2.1.319 recordings of the turn-boundary cases (
// lib/__fixtures__/claude-2.1.319/README.md), at their falsifiable points: the
// lifecycle frames a uuid-carrying user line gets, where `init` and `result` fall,
// the absorbed task notification and the idle interrupt.
import "#test-env";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtures = path.join(here, "__fixtures__", "claude-2.1.319");
const RECORDINGS = ["absorbed-notification", "steer-absorbed", "steer-queued", "idle-interrupt"];
const exists = (file) => fs.existsSync(path.join(fixtures, file));
const jsonl = (file) => {
  assert.ok(exists(file), `${file} is not recorded`);
  return fs.readFileSync(path.join(fixtures, file), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
};
/** The stream without token deltas: what the turn boundary reads. */
const frames = (name) => jsonl(`${name}/stream.jsonl`).filter((event) => event.type !== "stream_event");
/** One short token per boundary frame, in order: `queued:<n>`, `started:<n>`, `completed:<n>`, `init`, `result[<n>,…]`. */
function order(name) {
  const users = jsonl(`${name}/stdin.jsonl`).filter((line) => line.type === "user").map((line) => line.uuid);
  const n = (uuid) => users.indexOf(uuid);
  return frames(name).flatMap((event) => {
    if (event.type === "command_lifecycle") return [`${event.state}:${n(event.command_uuid)}`];
    if (event.type === "system" && event.subtype === "init") return ["init"];
    if (event.type === "result") return [`result[${(event.user_message_uuids ?? []).map(n).join(",")}]`];
    if (event.type === "control_response") return ["control_response"];
    return [];
  });
}

test("every recording is claude 2.1.319's, its user lines carry a uuid, and it is scrubbed", () => {
  for (const name of RECORDINGS) {
    for (const file of ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]) assert.ok(exists(`${name}/${file}`), `${name}/${file} is not recorded`);
    const init = frames(name).find((event) => event.type === "system" && event.subtype === "init");
    assert.equal(init?.claude_code_version, "2.1.319", `${name} is not a 2.1.319 recording`);
    for (const line of jsonl(`${name}/stdin.jsonl`).filter((entry) => entry.type === "user")) {
      assert.match(line.uuid, /^[0-9a-f-]{36}$/, `${name}: a user line without its uuid`);
      assert.equal(typeof line.message.content, "string", `${name}: not pi-web's text line`);
    }
    for (const file of ["stdin.jsonl", "stream.jsonl", "transcript.jsonl"]) {
      const text = fs.readFileSync(path.join(fixtures, name, file), "utf8");
      assert.ok(!/sk-ant|ANTHROPIC_(AUTH_TOKEN|API_KEY|BASE_URL)|\/root\/|api-request/.test(text), `${name}/${file} is not scrubbed`);
    }
  }
});

test("① absorbed-notification: the background run's notification is absorbed mid-turn; the next message is one init and one result", () => {
  assert.deepEqual(order("absorbed-notification"), [
    "queued:0", "started:0", "init", "result[0]", "completed:0",
    "queued:1", "started:1", "init", "result[1]", "completed:1",
  ]);
  const stream = frames("absorbed-notification");
  const firstResult = stream.findIndex((event) => event.type === "result");
  const notification = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.output_file);
  assert.ok(notification > 0 && notification < firstResult, "the background notification did not arrive inside the first turn");
  const background = stream.find((event) => event.type === "system" && event.subtype === "task_started" && event.is_backgrounded === true);
  assert.equal(stream[notification].tool_use_id, background?.tool_use_id, "the notification is not the background run's");
  const transcript = jsonl("absorbed-notification/transcript.jsonl");
  assert.ok(transcript.some((entry) => entry.type === "queue-operation" && entry.operation === "remove" && entry.reason === "absorbed_mid_turn" && String(entry.content).includes("<task-notification>")),
    "the transcript does not show the notification absorbed_mid_turn");
  const results = stream.filter((event) => event.type === "result");
  assert.deepEqual(results.map((event) => [event.result_index, event.queued_turn_count]), [[0, 0], [1, 0]]);
});

test("② steer-absorbed: a message written mid-turn starts inside the running turn and its one result lists both messages", () => {
  assert.deepEqual(order("steer-absorbed"), ["queued:0", "started:0", "init", "queued:1", "started:1", "completed:1", "result[0,1]", "completed:0"]);
  const transcript = jsonl("steer-absorbed/transcript.jsonl");
  const second = jsonl("steer-absorbed/stdin.jsonl")[1];
  assert.ok(transcript.some((entry) => entry.type === "attachment" && entry.attachment?.type === "queued_command" && entry.attachment.source_uuid === second.uuid),
    "the absorbed message is not claude's queued_command attachment");
  assert.ok(!transcript.some((entry) => entry.type === "user" && entry.uuid === second.uuid), "an absorbed message has no user entry of its own");
  assert.ok(transcript.some((entry) => entry.type === "queue-operation" && entry.reason === "absorbed_mid_turn"));
});

test("② steer-queued: a message written mid-turn is queued, starts after the first result and is a turn of its own", () => {
  assert.deepEqual(order("steer-queued"), ["queued:0", "started:0", "init", "queued:1", "result[0]", "completed:0", "started:1", "init", "result[1]", "completed:1"]);
  const results = frames("steer-queued").filter((event) => event.type === "result");
  assert.deepEqual(results.map((event) => [event.result_index, event.queued_turn_count]), [[0, 0], [1, 0]], "queued_turn_count does not count the queued message");
  const second = jsonl("steer-queued/stdin.jsonl")[1];
  assert.ok(jsonl("steer-queued/transcript.jsonl").some((entry) => entry.type === "user" && entry.uuid === second.uuid), "the queued message is a user entry of its own");
});

test("③ idle-interrupt: claude answers an interrupt while idle with nothing queued, and it stops the running background agent", () => {
  assert.deepEqual(order("idle-interrupt"), ["queued:0", "started:0", "init", "result[0]", "completed:0", "control_response"]);
  const stdin = jsonl("idle-interrupt/stdin.jsonl");
  assert.deepEqual(stdin.map((line) => line.request?.subtype ?? line.type), ["user", "interrupt"]);
  const stream = frames("idle-interrupt");
  const answer = stream.find((event) => event.type === "control_response");
  assert.deepEqual(answer.response, { subtype: "success", request_id: stdin[1].request_id, response: { still_queued: [] } });
  const agent = stream.find((event) => event.type === "system" && event.subtype === "task_started" && event.is_backgrounded === true);
  assert.equal(agent?.task_type, "local_agent", "the background run is not an agent");
  const firstResult = stream.findIndex((event) => event.type === "result");
  const stopped = stream.findIndex((event) => event.type === "system" && event.subtype === "task_notification" && event.tool_use_id === agent.tool_use_id);
  assert.ok(stopped > firstResult, "the agent's end did not come after the turn ended");
  assert.equal(stream[stopped].status, "stopped", "the idle interrupt did not stop the background agent");
});

test("the README records each recording's order of init / result / queued_turn_count / result_index", () => {
  assert.ok(exists("README.md"), "no README");
  const readme = fs.readFileSync(path.join(fixtures, "README.md"), "utf8");
  for (const name of RECORDINGS) assert.ok(readme.includes(`\`${name}/\``), `the README does not list ${name}/`);
  for (const word of ["init", "result", "queued_turn_count", "result_index", "command_lifecycle", "user_message_uuids", "absorbed_mid_turn", "2.1.319"]) {
    assert.ok(readme.includes(word), `the README does not record ${word}`);
  }
});
