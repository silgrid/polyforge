import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// the install entry's two routes. Their behavior runs against
// a fake install.sh in lib/polyforge-install.test.mjs; here: who can reach
// them, what they accept, and that they never answer 5xx.

const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
const route = await read("./install/route.ts");
const events = await read("./install/events/route.ts");
const proxy = await read("../../../proxy.ts");

test("AC5: reach is the logged-in surface — the proxy's login covers every /api path, and each handler re-checks the gate", () => {
  assert.match(proxy, /matcher: \["\/", "\/login", "\/api\/:path\*"\]/, "the login gate covers /api/polyforge/install");
  assert.match(proxy, /if \(!authenticated\) \{/);
  for (const [name, source, handlers] of [["install", route, ["GET", "POST"]], ["install/events", events, ["GET"]]]) {
    for (const handler of handlers) assert.match(source, new RegExp(`export async function ${handler}\\(request: Request\\)`), `${name} ${handler}`);
    assert.equal((source.match(/if \(!isApiRequestAllowed\(request\)\) \{\s*return NextResponse\.json\(\{ error: "Forbidden" \}, \{ status: 403 \}\);/g) ?? []).length, handlers.length, `${name}: every handler re-checks the gate`);
    assert.doesNotMatch(source, /status: 5\d\d/, `${name} never answers 5xx`);
  }
});

test("the start is JSON-only, answers 202 with the run, and keeps the module's 4xx (one run at a time, a force only after a refusal)", () => {
  assert.match(route, /if \(!hasJsonContentType\(request\)\) \{\s*return NextResponse\.json\(\{ error: "Content-Type must be application\/json" \}, \{ status: 415 \}\);/);
  assert.match(route, /NextResponse\.json\(\{ ok: true, run: await startPolyforgeInstall\(body\) \}, \{ status: 202 \}\)/);
  assert.match(route, /error instanceof PolyforgeInstallError\) \{\s*return NextResponse\.json\(\{ error: error\.message \}, \{ status: error\.status \}\);/);
  assert.doesNotMatch(route, /apiKey[^\n]*NextResponse|console\./, "the key is never echoed or logged");
});

test("AC5: the output stream replays a run from its cursor, ends with the run, and never stops the run", () => {
  assert.match(events, /"Content-Type": "text\/event-stream"/);
  assert.match(events, /request\.headers\.get\("last-event-id"\) \?\? new URL\(request\.url\)\.searchParams\.get\("after"\)/);
  assert.match(events, /subscribePolyforgeInstall\(after, send\)/);
  assert.match(events, /if \(subscription\.run\.state !== "running"\) send\(\{ type: "end", run: subscription\.run \}\)/, "a finished run ends at once after its lines");
  assert.match(events, /\{ error: "no install has run" \}, \{ status: 404 \}/);
  assert.doesNotMatch(events, /kill|abort\(\)\s*;?\s*\/\/ run/i, "a closed stream only unsubscribes");
});
