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
import { fileURLToPath } from "node:url";
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

// ------------------------------------------------ build whole-suite gate

// The real shared engine, so the handler is driven through the production
// path: verify-scope's schema-1 projection over a temporary git fixture.
const sharedDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../../../../workflows/_shared");

interface Fixture {
	conf?: string;
	files?: Record<string, string>;
}

function git(cwd: string, ...args: string[]): void {
	const r = spawnSync("git", ["-C", cwd, ...args], { encoding: "utf8" });
	if (r.status !== 0) throw new Error(`git ${args.join(" ")}: ${r.stderr}`);
}

function fixtureRepo(t: { after(fn: () => void): void }, f: Fixture = {}): string {
	const capsule = fs.mkdtempSync(path.join(os.tmpdir(), "guard-build-"));
	t.after(() => fs.rmSync(capsule, { recursive: true, force: true }));
	const repo = path.join(capsule, "repos", "r");
	fs.mkdirSync(repo, { recursive: true });
	if (f.conf !== undefined) {
		fs.mkdirSync(path.join(repo, ".polyforge"), { recursive: true });
		fs.writeFileSync(path.join(repo, ".polyforge", "verify-scope.conf"), f.conf);
	}
	for (const [rel, content] of Object.entries(f.files ?? {})) {
		const p = path.join(repo, rel);
		fs.mkdirSync(path.dirname(p), { recursive: true });
		fs.writeFileSync(p, content);
	}
	git(repo, "init", "-q", "-b", "main");
	git(repo, "config", "user.email", "a@b.c");
	git(repo, "config", "user.name", "a");
	git(repo, "add", "-A");
	git(repo, "commit", "-qm", "init");
	return capsule;
}

function buildEnv(capsule: string, over: NodeJS.ProcessEnv = {}): NodeJS.ProcessEnv {
	return {
		PATH: process.env.PATH,
		PF_STEP: "build",
		PF_CAPSULE: capsule,
		PF_SHARED_DIR: sharedDir,
		PF_PYTHON: process.env.PF_PYTHON || "python3",
		...over,
	};
}

interface Blocked {
	block: boolean;
	reason: string;
}

async function bashCall(opts: Parameters<typeof createStepGuard>[0], cwd: string, command: string): Promise<Blocked | undefined> {
	const handlers = loadGuard(opts);
	return (await handlers["tool_call"]({ toolName: "bash", input: { command } }, { cwd, hasUI: false })) as Blocked | undefined;
}

test("build blocks the analyzer's default go whole-tree run", async (t) => {
	const capsule = fixtureRepo(t, { files: { "go.mod": "module example.com/r\n\ngo 1.21\n", "pkg/a.go": "package pkg\n" } });
	const r = await bashCall({ env: buildEnv(capsule) }, path.join(capsule, "repos", "r"), "GOWORK=off go test ./... -count=1");
	assert.ok(r, "the whole-tree run is blocked");
	assert.equal(r.block, true);
	assert.match(r.reason, /^step guard: blocked/);
	assert.match(r.reason, /fast_checks\.py" --repo r --close-scoped/);
	assert.match(r.reason, /-run/);
});

test("build blocks the declared substitution form against a whole-tree run", async (t) => {
	const capsule = fixtureRepo(t, {
		conf: "stack=none\nfull_command=GOWORK=off go test $(GOWORK=off go list ./... | grep -v '/test$') -count=1\n",
	});
	const r = await bashCall({ env: buildEnv(capsule) }, path.join(capsule, "repos", "r"), "go test ./...");
	assert.ok(r && r.block);
});

test("build tracks a leading cd from the capsule root", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const r = await bashCall({ env: buildEnv(capsule) }, capsule, "cd repos/r && go test ./... > l 2>&1; echo exit=$?");
	assert.ok(r && r.block, "the effective directory is the repo root");
});

test("build blocks a nested declaration at its effective directory", async (t) => {
	const capsule = fixtureRepo(t, {
		conf: "stack=none\nfull_command=cd sub && npm run test:e2e:all\n",
		files: { "sub/x.txt": "x\n" },
	});
	const r = await bashCall({ env: buildEnv(capsule) }, path.join(capsule, "repos", "r"), "cd sub && npm run test:e2e:all");
	assert.ok(r && r.block);
});

test("build allows a test the declaration puts in the fast phase", async (t) => {
	const capsule = fixtureRepo(t, {
		files: { "package.json": '{"name":"r","version":"1.0.0","scripts":{"test":"vitest run"}}\n' },
	});
	const r = await bashCall({ env: buildEnv(capsule) }, path.join(capsule, "repos", "r"), "npm test");
	assert.equal(r, undefined, "a zero-config npm repo declares npm test in the fast phase");
});

test("build allows the written fast-check command itself", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const r = await bashCall(
		{ env: buildEnv(capsule) },
		path.join(capsule, "repos", "r"),
		'"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo r --phase fast',
	);
	assert.equal(r, undefined);
});

test("build blocks a whole-tree segment beside the fast-check command", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const r = await bashCall(
		{ env: buildEnv(capsule) },
		path.join(capsule, "repos", "r"),
		'"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo r --phase fast && go test ./...',
	);
	assert.ok(r && r.block, "the fast_checks.py segment exempts nothing else in the same command");
});

test("build allows a targeted package run and a filtered run", async (t) => {
	const capsule = fixtureRepo(t, { files: { "go.mod": "module example.com/r\n\ngo 1.21\n", "pkg/a.go": "package pkg\n" } });
	const repo = path.join(capsule, "repos", "r");
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "go test ./pkg/ -count=1"), undefined);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "go test ./internal/polyforge/... -count=1"), undefined);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "go test ./... -run TestX"), undefined);
});

test("build blocks a declared single-package full run only literally", async (t) => {
	const capsule = fixtureRepo(t, {
		conf: "stack=none\nfull_command=go test -race -count=1 -timeout 30m ./pkg/\n",
		files: { "pkg/a.go": "package pkg\n" },
	});
	const repo = path.join(capsule, "repos", "r");
	assert.ok((await bashCall({ env: buildEnv(capsule) }, repo, "go test -race -timeout 30m ./pkg/ -count=1"))?.block);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "go test ./pkg/ -run TestX"), undefined);
});

test("build allows a filtered npm run and blocks the bare one", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=npm test\n" });
	const repo = path.join(capsule, "repos", "r");
	assert.ok((await bashCall({ env: buildEnv(capsule) }, repo, "npm test"))?.block);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "npm test -- one.test.ts"), undefined);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "npm test one.test.ts"), undefined);
});

test("a non-build step passes a whole-tree run", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const repo = path.join(capsule, "repos", "r");
	assert.equal(await bashCall({ env: buildEnv(capsule, { PF_STEP: "verification" }) }, repo, "go test ./..."), undefined);
	assert.equal(await bashCall({ env: buildEnv(capsule, { PF_STEP: "" }) }, repo, "go test ./..."), undefined);
});

test("the guard fails open without an engine or outside the capsule's repos", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const repo = path.join(capsule, "repos", "r");
	assert.equal(await bashCall({ env: buildEnv(capsule, { PF_SHARED_DIR: "" }) }, repo, "go test ./..."), undefined);
	assert.equal(
		await bashCall({ env: buildEnv(capsule, { PF_SHARED_DIR: path.join(capsule, "missing") }) }, repo, "go test ./..."),
		undefined,
	);
	assert.equal(await bashCall({ env: buildEnv(capsule) }, capsule, "go test ./..."), undefined);
});

test("the existing background rule still blocks a whole-tree run first", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const r = await bashCall({ env: buildEnv(capsule) }, path.join(capsule, "repos", "r"), "go test ./... &");
	assert.ok(r && r.block);
	assert.match(r.reason, /starts a background job/);
});

test("build reloads the projection when the conf changes", async (t) => {
	const capsule = fixtureRepo(t, { conf: "stack=none\nfull_command=go test ./...\n" });
	const repo = path.join(capsule, "repos", "r");
	assert.ok((await bashCall({ env: buildEnv(capsule) }, repo, "go test ./..."))?.block);
	fs.writeFileSync(path.join(repo, ".polyforge", "verify-scope.conf"), "stack=none\n# no full command any more\n");
	assert.equal(await bashCall({ env: buildEnv(capsule) }, repo, "go test ./..."), undefined);
});
