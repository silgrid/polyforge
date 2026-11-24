// carries: lib/server-error.ts
// polyforge#259 (T4 AC2): 中文界面里没有未翻译的服务端英文串，英文兜底只放在
// 「详情」折叠内。期望值来自这条要求，不取自实现输出：已知 code 给出对应中文
// 文案的 key，未知 code 与只有自由文本时给通用兜底，原文一律进 details。
import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";

const { serverErrorText } = await import("./server-error.ts");

test("a known code answers its own message key and keeps the server's words as details", () => {
  const denied = serverErrorText({ code: "http-403", message: "forbidden: /root/.pi/agent/settings.json is read-only" });
  assert.match(denied.summaryKey, /^serverError\./, "a known code has no message key");
  assert.equal(denied.details, "forbidden: /root/.pi/agent/settings.json is read-only");
  const conflict = serverErrorText({ code: "http-409" });
  assert.notEqual(conflict.summaryKey, denied.summaryKey, "two codes share one message");
  assert.equal(conflict.details, null, "a code without a message claims details it has not got");
});

test("the code's params ride along for the message, and a missing param never leaks a placeholder", () => {
  const withParams = serverErrorText({ code: "http-404", params: { path: "/root/.pi/agent/mcp.json" }, message: "not found" });
  assert.deepEqual(withParams.summaryParams, { path: "/root/.pi/agent/mcp.json" });
  const empty = serverErrorText({ code: "http-404", params: {}, message: "not found" });
  assert.equal(empty.summaryParams, undefined, "empty params are passed to the message");
});

test("an unknown code or a bare English string answers the generic Chinese line, never the English as the main text", () => {
  const unknown = serverErrorText({ code: "core-4711", message: "the step-tier catalog needs pf 0.2.4 or newer (found v0.2.4-dev.49)" });
  const bare = serverErrorText("pf: daemon not reachable (connection refused)");
  assert.match(unknown.summaryKey, /^serverError\./);
  assert.equal(unknown.summaryKey, bare.summaryKey, "a bare text and an unknown code read differently");
  assert.equal(unknown.details, "the step-tier catalog needs pf 0.2.4 or newer (found v0.2.4-dev.49)");
  assert.equal(bare.details, "pf: daemon not reachable (connection refused)");
  assert.ok(!unknown.summaryKey.includes("catalog"), "the English text became the main message");
});

test("nothing to say is not an error message: an empty payload answers the generic line with no details", () => {
  for (const empty of [null, undefined, {}, { code: "   " }, { message: "  " }]) {
    const text = serverErrorText(empty);
    assert.match(text.summaryKey, /^serverError\./);
    assert.equal(text.details, null);
  }
});
// polyforge#259 T4 AC2 (owner 2A): the routes append a code beside their own words,
// so one text says which case it is. The expected values are the texts the routes
// and pf really produce; an implementation that returns anything for everything
// (or nothing for the CAS case) turns these red.
test("a server text names its code, and an unknown one names none", async () => {
  const { codeFromError } = await import("./server-error.ts");
  assert.equal(codeFromError("pf: config-cas-409: the file changed since it was read"), "config-cas-409");
  assert.equal(codeFromError("request-denied"), "http-403");
  assert.equal(codeFromError("Untrusted API request"), "http-403");
  assert.equal(codeFromError("HTTP 404"), "http-404");
  assert.equal(codeFromError("the body is not valid JSON"), null, "an unknown text claims a code");
  assert.equal(codeFromError(""), null);
  assert.equal(codeFromError(null), null);
  // A code that a route sends explicitly still answers its own message.
  const text = serverErrorText({ code: codeFromError("pf: config-cas-409: nope") });
  assert.equal(text.summaryKey, "serverError.conflict");
});
