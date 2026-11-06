import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

// Internal feature #698: a quoted selection can be asked in a new chat that
// forks the conversation at the quoted message (fork_branch). The public
// v0.14.2 merge replaced it with side chats and dropped this wiring; the
// internal line keeps the fork.
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");

test("AppShell wires 'Ask in a new chat' into the main ChatWindow", async () => {
  const source = await read("./AppShell.tsx");
  const handler = source.match(/const\s+handleAskInNewChat\s*=\s*useCallback\(\s*async\s*\([\s\S]*?\}\s*,\s*\[[^\]]*\]\s*\)\s*;/);
  assert.ok(handler, "expected handleAskInNewChat in AppShell");
  assert.match(handler[0], /sendAgentCommand[\s\S]*type: "fork_branch"/);
  assert.match(handler[0], /setPendingQuotePrompt\(\{ sessionId: result\.newSessionId, text: prompt \}\)/);
  assert.match(handler[0], /handleSessionForked\(result\.newSessionId\)/);
  assert.match(source, /onAskInNewChat=\{handleAskInNewChat\}/);
  assert.match(source, /initialPrompt=\{pendingQuotePrompt\?\.sessionId === selectedSession\?\.id \? pendingQuotePrompt\?\.text : undefined\}/);
  assert.match(source, /onInitialPromptConsumed=\{\(\) => setPendingQuotePrompt\(null\)\}/);
});

test("every literal t() key the chat window and composer use exists in en", async () => {
  const en = await read("../lib/i18n/messages/en.ts");
  for (const path of ["./ChatWindow.tsx", "./ChatInput.tsx"]) {
    const source = await read(path);
    const keys = new Set([...source.matchAll(/\bt\("([^"]+)"/g)].map((m) => m[1]));
    assert.ok(keys.size > 0, `expected t() calls in ${path}`);
    const missing = [...keys].filter((key) => !en.includes(`"${key}":`));
    assert.deepEqual(missing, [], `${path} uses keys missing from en.ts`);
  }
});
