import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const list = await read("./messages/route.ts");
const history = await read("./messages/[id]/route.ts");
const ack = await read("./messages/ack/route.ts");
const analyze = await read("./messages/analyze/route.ts");
const prompt = await read("./messages/prompt/route.ts");
const lib = await read("../../../lib/polyforge-messages.ts");

test("every messages route re-checks isApiRequestAllowed in-route", () => {
  for (const [name, source] of [["list", list], ["history", history], ["ack", ack], ["analyze", analyze], ["prompt", prompt]]) {
    assert.match(source, /import \{[^}]*isApiRequestAllowed[^}]*\} from "@\/lib\/request-security"/, `${name} imports the gate`);
    assert.match(source, /if \(!isApiRequestAllowed\(request\)\)/, `${name} checks the gate in the route`);
    assert.match(source, /status: 403/, `${name} answers a JSON 403`);
  }
});

test("writes are JSON-only POSTs and validate before the pf spawn", () => {
  for (const [name, source, validator] of [
    ["send", list, /validateSendInput\(body\)/],
    ["ack", ack, /isPositiveSeq\(body\.seq\)/],
    ["analyze", analyze, /isPolyforgeMessageId\(body\.message_id\)/],
    ["prompt", prompt, /isPolyforgeMessageId\(body\.message_id\)/],
  ]) {
    assert.match(source, /hasJsonContentType\(request\)/, `${name} is JSON-only`);
    assert.match(source, /status: 415/, `${name} refuses another content type`);
    const check = source.search(validator);
    const spawn = source.search(/sendPolyforgeMessage\(|acknowledgePolyforgeMessage\(|analyzePolyforgeMessage\(|promptPolyforgeMessage\(/);
    assert.ok(check >= 0, `${name} validates its body`);
    assert.ok(check < spawn, `${name} validates before the spawn`);
  }
});

test("no messages route answers 5xx and no route logs a request or response body", () => {
  for (const [name, source] of [["list", list], ["history", history], ["ack", ack], ["analyze", analyze], ["prompt", prompt]]) {
    assert.doesNotMatch(source, /status:\s*5\d\d/, `${name} carries no 5xx literal`);
    assert.doesNotMatch(source, /console\.(log|error|warn)/, `${name} never logs`);
    assert.doesNotMatch(source, /sender_id|members/, `${name} never echoes the identity fields`);
    assert.doesNotMatch(source, /code:\s*"/, `${name} answers {error} without an invented code field`);
  }
  assert.match(lib, /messageErrorStatus/, "the one status mapping lives in the lib");
  assert.match(lib, /rawBefore|before/, "the history read forwards a validated cursor");
});

test("the wire whitelist drops sender_id/members and the sanitizer never exposes an id", () => {
  assert.match(lib, /sender_id/);
  assert.match(lib, /members/);
  assert.match(lib, /sanitizeConversation/);
  assert.match(lib, /sanitizeMessage/);
  assert.doesNotMatch(lib, /preview\?|conversation\.preview|newest message as a list preview/, "the unrequested preview and its per-conversation spawn are gone");
  assert.doesNotMatch(lib, /PF-UNAVAILABLE/, "no invented unavailable code");
  assert.doesNotMatch(lib, /console\.(log|error|warn)/, "the lib never logs a body, credential or env value");
});

test("a list-read failure stays capable so no false upgrade state replaces an open conversation", () => {
  const start = lib.indexOf("export async function readPolyforgeMessages");
  const end = lib.indexOf("export async function readPolyforgeMessagesPage", start);
  const read = lib.slice(start, end);
  assert.match(read, /capable: true,\s*error: error instanceof Error/);
  assert.match(read, /conversations: \[\]/, "a failed poll returns an empty list, not a capability downgrade");
  assert.match(list, /capable: true,\s*error: "the messages read failed/, "the route's catch does not invent a floor state");
});

test("the list route's answer carries contacts and groups on every branch", () => {
  assert.match(list, /configured `contacts`/, "the GET face documents the contacts field");
  assert.match(list, /project `groups`/, "the GET face documents the groups field");
  assert.match(list, /conversations: \[\],\s*contacts: \[\],\s*groups: \[\],/, "the route's catch answers empty contacts and groups too");
  const start = lib.indexOf("export async function readPolyforgeMessages(");
  const end = lib.indexOf("export async function readPolyforgeMessagesPage", start);
  const read = lib.slice(start, end);
  assert.match(read, /return \{ \.\.\.capability, conversations: \[\], contacts: \[\], groups: \[\] \};/, "below the floor no contact or grouping read starts");
  assert.ok(read.indexOf("readPolyforgeMessagesCapability()") < read.indexOf("readProjectGroups("), "the grouping read starts only after the floor gate");
  assert.equal((read.match(/contacts: await contactsRead/g) ?? []).length, 2, "both the list-failure and success answers carry the contact read");
  assert.equal((read.match(/groups: await groupsRead/g) ?? []).length, 2, "both the list-failure and success answers carry the groups");
  assert.match(lib, /runPfJson\(\["--json", "msg", "contacts"\], CONTACT_READ_TIMEOUT_MS\)/, "one bounded `pf --json msg contacts` spawn");
  assert.match(lib, /runPfJson\(\["--json", "projects", "get", project\], CONTACT_READ_TIMEOUT_MS\)/, "each project read shares the 2 s contact bound");
  assert.match(lib, /const CONTACT_READ_TIMEOUT_MS = 2000;/, "the contact read is bounded well below the 15 s read timeout");
});

test("the analyze route gates on the analysis floor before the spawn and answers the structured body", () => {
  const gate = analyze.search(/const capability = await readPolyforgeAnalysisCapability\(\);/);
  const spawn = analyze.search(/analyzePolyforgeMessage\(/);
  assert.ok(gate >= 0 && gate < spawn, "the analysis floor gates before any analyze spawn");
  assert.doesNotMatch(analyze, /readPolyforgeMessagesCapability/, "the analysis floor, not the Messages floor, gates the analysis");
  assert.match(analyze, /\{ error: capability\.reason \?\? "polyforge is unavailable", upgrade: capability\.pfPresent \},\s*\{ status: 400 \}/);
  assert.match(analyze, /NextResponse\.json\(\{ ok: true, analysis: await analyzePolyforgeMessage\(body\.message_id\) \}\)/);
  assert.doesNotMatch(analyze, /\.conclusion|"conclusion"|conclusion:/, "no conclusion is read or answered");
});

test("the prompt route gates on its own floor before the one spawn and answers the sanitized prompt", () => {
  const gate = prompt.search(/const capability = await readPolyforgePromptCapability\(\);/);
  const spawn = prompt.search(/promptPolyforgeMessage\(body\.message_id\)/);
  assert.ok(gate >= 0 && gate < spawn, "the prompt floor gates before any prompt spawn");
  assert.equal(prompt.split("promptPolyforgeMessage(").length - 1, 1, "exactly one spawn site");
  assert.doesNotMatch(prompt, /readPolyforgeMessagesCapability|readPolyforgeAnalysisCapability/, "the prompt floor, not another floor, gates the action");
  assert.match(prompt, /\{ error: capability\.reason \?\? "polyforge is unavailable", upgrade: capability\.pfPresent \},\s*\{ status: 400 \}/);
  assert.match(prompt, /NextResponse\.json\(\{ ok: true, prompt: await promptPolyforgeMessage\(body\.message_id\) \}\)/);
  assert.match(prompt, /Reach: pi-web's authenticated surface only/, "the route states its reach");
  assert.match(lib, /runPfJson\(\["--json", "msg", "prompt", messageId\], ANALYZE_TIMEOUT_MS, ANALYZE_MAX_BUFFER\)/, "one bounded pf spawn");
  assert.match(lib, /parsePolyforgePrompt\(raw\)/, "the one sanitizer");
});

test("the file-ticket route — reach stated, gate, JSON-only, validation before the one create, no 5xx, no log", async () => {
  const ticket = await read("./messages/file-ticket/route.ts");
  assert.match(ticket, /Reach: whoever can already reach pi-web's authenticated surface/, "the route states its reach");
  assert.match(ticket, /if \(!isApiRequestAllowed\(request\)\)/);
  assert.match(ticket, /status: 403/);
  assert.match(ticket, /hasJsonContentType\(request\)/);
  assert.match(ticket, /status: 415/);
  const gate = ticket.search(/isApiRequestAllowed\(request\)\)/);
  const json = ticket.search(/hasJsonContentType\(request\)/);
  const validate = ticket.search(/validateFileTicketInput\(body, await readPolyforgeMappedProjects\(\)\)/);
  const spawn = ticket.search(/createPolyforgeFileTicket\(/);
  assert.ok(gate < json && json < validate && validate < spawn, "403 → 415 → 400 before the one create");
  assert.equal(ticket.split("createPolyforgeFileTicket(").length - 1, 1, "exactly one create per valid confirm");
  assert.doesNotMatch(ticket, /status:\s*5\d\d/, "never 5xx");
  assert.doesNotMatch(ticket, /console\.(log|error|warn)/, "no logged body");
  assert.doesNotMatch(ticket, /--bundle"|bundle:/, "a registration ticket only");
  assert.match(ticket, /status: messageErrorStatus\(failure\.code\)/, "pf's 4xx kept, DAEMON-500/unreachable a 200 {error}");
  const create = lib.slice(lib.indexOf("export async function createPolyforgeFileTicket("));
  assert.match(create, /\["--json", "file", "create", "--project", input\.project, "--goal", input\.goal, "--content", input\.content\]/);
  assert.doesNotMatch(create.slice(0, create.indexOf("\n}\n")), /--bundle/);
});
