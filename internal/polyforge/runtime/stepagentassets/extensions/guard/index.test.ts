// carries: internal/polyforge/runtime/stepagentassets/extensions/guard/index*
//
// index.test.ts: the extension's handlers over a fake pi object — the
// tool_result notes for a file just written or edited (AC2), the bash
// tool_call verdicts, and the untouched paths (valid file, no checker,
// failed edit). Expected positions come from the ticket's case table and
// the checkers' observed output, never from the implementation.
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { createStepGuard } from "./index.ts";

interface Handlers {
	[name: string]: ((event: unknown, ctx: unknown) => Promise<unknown> | unknown) | undefined;
}

function loadGuard(opts?: Parameters<typeof createStepGuard>[0]) {
	const handlers: Handlers = {};
	const pi = {
		on: (name: string, h: (event: unknown, ctx: unknown) => unknown) => {
			handlers[name] = h;
		},
		registerCommand: () => {},
	};
	createStepGuard(opts)(pi as never);
	return handlers;
}

function tmpWorkspace(t: { after(fn: () => void): void }): string {
	const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "guard-"));
	t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
	return tmp;
}

interface Event {
	toolName: string;
	input: Record<string, unknown>;
	content: Array<{ type: "text"; text: string }>;
	isError: boolean;
	structuredContent?: unknown;
}

async function toolResult(handlers: Handlers, event: Event, cwd: string) {
	const ctx = { cwd, hasUI: false };
	return handlers["tool_result"](event, ctx);
}

function textEvent(path0: string, isError = false, structuredContent?: unknown): Event {
	return {
		toolName: "write",
		input: { path: path0 },
		content: [{ type: "text", text: "ok" }],
		isError,
		structuredContent,
	};
}

test("write .ts with a stray brace gets the error location", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.ts"), "export function f(x: number): number {\n  return x + 1;\n}\n}\n");
	const handlers = loadGuard();
	const r = (await toolResult(handlers, textEvent("bad.ts"), tmp)) as
		| { content: Array<{ type: string; text: string }> }
		| undefined;
	assert.ok(r, "a note was appended");
	assert.equal(r.content.length, 2);
	assert.equal(r.content[0].text, "ok");
	assert.match(r.content[1].text, /step guard: syntax check \(typescript\) found an error in bad\.ts:4(:\d+)?: /);
});

test("valid .ts is returned untouched", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "good.ts"), "export const a: number = 1;\n");
	const handlers = loadGuard();
	const r = await toolResult(handlers, textEvent("good.ts"), tmp);
	assert.equal(r, undefined);
});

test("edit .go reports gofmt position", async (t) => {
	if (spawnSync("gofmt", ["--help"]).error) {
		t.skip("gofmt is not on PATH");
		return;
	}
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.go"), "package main\n\nfunc main() {\n}\n}\n");
	const handlers = loadGuard();
	const event = textEvent("bad.go");
	event.toolName = "edit";
	const r = (await toolResult(handlers, event, tmp)) as { content: Array<{ text: string }> } | undefined;
	assert.ok(r, "a note was appended");
	assert.ok(r.content[1].text.includes("bad.go:5:1:"), r.content[1].text);
});

test(".py error and no pycache", async (t) => {
	if (spawnSync("python3", ["--version"]).error) {
		t.skip("python3 is not on PATH");
		return;
	}
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.py"), "def f():\n    return 1\n)\n");
	const handlers = loadGuard();
	const r = (await toolResult(handlers, textEvent("bad.py"), tmp)) as { content: Array<{ text: string }> } | undefined;
	assert.ok(r, "a note was appended");
	assert.ok(r.content[1].text.includes("bad.py:3:1:"), r.content[1].text);
	assert.equal(fs.existsSync(path.join(tmp, "__pycache__")), false, "no __pycache__ beside the edited file");
});

test(".json error", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.json"), '{\n "a": 1\n}}\n');
	const handlers = loadGuard();
	// a tool result that carries structuredContent must get it back
	// verbatim: replacing content alone drops it (pi's tool_result rule)
	const structured = { marker: 1 };
	const r = (await toolResult(handlers, textEvent("bad.json", false, structured), tmp)) as
		| { content: Array<{ text: string }>; structuredContent?: unknown }
		| undefined;
	assert.ok(r, "a note was appended");
	assert.ok(r.content[1].text.includes("bad.json:3:2:"), r.content[1].text);
	assert.deepEqual(r.structuredContent, structured, "the note must carry the structuredContent back");
});

test(".js error", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.js"), "const a = 1;\n}\n");
	const handlers = loadGuard();
	const r = (await toolResult(handlers, textEvent("bad.js"), tmp)) as { content: Array<{ text: string }> } | undefined;
	assert.ok(r, "a note was appended");
	assert.ok(r.content[1].text.includes("bad.js:2:"), r.content[1].text);
});

test("type without a checker is returned untouched", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "notes.md"), "}}}");
	const handlers = loadGuard();
	const r = await toolResult(handlers, textEvent("notes.md"), tmp);
	assert.equal(r, undefined);
});

test("jsonc config is not checked", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "tsconfig.json"), "{ // c\n}");
	const handlers = loadGuard();
	const r = await toolResult(handlers, textEvent("tsconfig.json"), tmp);
	assert.equal(r, undefined);
});

test("failed edit is not checked", async (t) => {
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.ts"), "export function f(x: number): number {\n  return x + 1;\n}\n}\n");
	const handlers = loadGuard();
	const r = await toolResult(handlers, textEvent("bad.ts", true), tmp);
	assert.equal(r, undefined);
});

test("missing checker is noted, edit not blocked", async (t) => {
	if (spawnSync("python3", ["--version"]).error) {
		t.skip("python3 is not on PATH");
		return;
	}
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.py"), "def f():\n    return 1\n)\n");
	const handlers = loadGuard({ env: { PATH: "" } });
	const r = (await toolResult(handlers, textEvent("bad.py"), tmp)) as { content: Array<{ text: string }> } | undefined;
	assert.ok(r, "the skip note was appended");
	assert.ok(r.content[1].text.includes("syntax check skipped for bad.py"), r.content[1].text);
	assert.ok(r.content[1].text.includes("not found on PATH"), r.content[1].text);
	assert.equal(r.content[0].text, "ok");
});

test("a checker the timeout kills is a failed note naming the timeout (r1#8)", async (t) => {
	if (spawnSync("python3", ["--version"]).error) {
		t.skip("python3 is not on PATH");
		return;
	}
	const tmp = tmpWorkspace(t);
	fs.writeFileSync(path.join(tmp, "bad.py"), "def f():\n    return 1\n)\n");
	const handlers = loadGuard({ timeoutMs: 1 });
	const r = (await toolResult(handlers, textEvent("bad.py"), tmp)) as { content: Array<{ text: string }> } | undefined;
	assert.ok(r, "the failure note was appended");
	assert.ok(r.content[1].text.includes("syntax check could not run for bad.py"), r.content[1].text);
	assert.ok(r.content[1].text.includes("timed out"), r.content[1].text);
});

test("tool_call blocks a background bash command", async () => {
	const handlers = loadGuard();
	const r = (await handlers["tool_call"]({ toolName: "bash", input: { command: "sleep 30 &" } }, { cwd: "/" })) as
		| { block: boolean; reason: string }
		| undefined;
	assert.ok(r, "the call was blocked");
	assert.equal(r.block, true);
	assert.ok(r.reason.startsWith("step guard: blocked"), r.reason);
});

test("tool_call ignores other tools and clean commands", async () => {
	const handlers = loadGuard();
	const read = await handlers["tool_call"]({ toolName: "read", input: { path: "x" } }, { cwd: "/" });
	assert.equal(read, undefined);
	const clean = await handlers["tool_call"]({ toolName: "bash", input: { command: "ls -la" } }, { cwd: "/" });
	assert.equal(clean, undefined);
});
