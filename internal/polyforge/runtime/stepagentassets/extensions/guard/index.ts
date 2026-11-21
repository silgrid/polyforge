// index.ts: the step guard pi extension (polyforge#214). tool_call blocks
// a bash command that leaves a background job unwaited or sleeps more
// than 3 s in one go; tool_result appends a syntax-check note for a file
// just written or edited. Every verdict lives in ./rules.ts — this file
// only wires the handlers, resolves paths and runs the checkers.
//
// pi is imported as a TYPE only, so the test suite runs under plain node.

import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import {
	checkBashCommand,
	checkJsonText,
	interpretCheckerRun,
	syntaxCheckPlan,
	syntaxNote,
	type CheckerRun,
	type SyntaxCheckPlan,
	type SyntaxOutcome,
} from "./rules.ts";

const GUARD_DESCRIPTION =
	"Step guard: blocks unwaited background jobs and sleeps over 3 s; appends syntax errors after edit/write";

export interface StepGuardOptions {
	env?: NodeJS.ProcessEnv;
	timeoutMs?: number;
}

// resolveTarget turns the tool's file argument into an absolute path: a
// leading `@` is dropped, `~` expands, anything else resolves against the
// working directory.
function resolveTarget(cwd: string, p: string): string {
	let t = p;
	while (t.startsWith("@")) t = t.slice(1);
	if (t === "~") t = os.homedir();
	else if (t.startsWith("~/")) t = path.join(os.homedir(), t.slice(2));
	return path.resolve(cwd, t);
}

// displayPath stays relative while the file lives inside the working
// directory, absolute otherwise.
function displayPath(cwd: string, abs: string): string {
	const rel = path.relative(cwd, abs);
	if (rel !== "" && !rel.startsWith("..") && !path.isAbsolute(rel)) return rel;
	return abs;
}

// runChecker runs one checker subprocess and collects its outcome; the
// check itself never blocks the edit, so every failure mode maps to a
// CheckerRun shape instead of throwing.
function runChecker(plan: SyntaxCheckPlan, env: NodeJS.ProcessEnv, timeoutMs: number): Promise<CheckerRun> {
	return new Promise((resolve) => {
		let stdout = "";
		let stderr = "";
		try {
			const child = spawn(plan.argv[0], plan.argv.slice(1), { env, timeout: timeoutMs });
			child.stdout?.on("data", (d: Buffer) => (stdout += d.toString()));
			child.stderr?.on("data", (d: Buffer) => (stderr += d.toString()));
			child.on("error", (err: NodeJS.ErrnoException & { killed?: boolean }) => {
				resolve({
					code: null,
					stdout,
					stderr,
					spawnErrorCode: err.code,
					timedOut: err.killed === true,
				});
			});
			child.on("close", (code, signal) => {
				// node's spawn timeout kills the child and this is the callback
			// that fires (code=null, signal set) — the error event is not
				// how a timeout arrives (r1#8)
				resolve({ code, stdout, stderr, timedOut: signal !== null });
			});
		} catch (e) {
			resolve({ code: null, stdout, stderr, spawnErrorCode: `spawn failed: ${e}` });
		}
	});
}

export async function runSyntaxCheck(
	absPath: string,
	opts: StepGuardOptions = {},
): Promise<{ plan: SyntaxCheckPlan; outcome: SyntaxOutcome } | undefined> {
	const plan = syntaxCheckPlan(absPath);
	if (!plan) return undefined;
	const env = opts.env ?? process.env;
	const timeoutMs = opts.timeoutMs ?? 5000;
	if (plan.checker === "json") {
		try {
			return { plan, outcome: checkJsonText(fs.readFileSync(absPath, "utf8")) };
		} catch (e) {
			return { plan, outcome: { kind: "failed", why: `the file could not be read: ${e}` } };
		}
	}
	const run = await runChecker(plan, env, timeoutMs);
	return { plan, outcome: interpretCheckerRun(plan, run) };
}

export function createStepGuard(opts: StepGuardOptions = {}) {
	const env = opts.env ?? process.env;
	const timeoutMs = opts.timeoutMs ?? 5000;
	return (pi: ExtensionAPI): void => {
		pi.on("tool_call", async (event) => {
			try {
				if (event.toolName !== "bash") return undefined;
				const command = String((event.input as { command?: unknown })?.command ?? "");
				const verdict = checkBashCommand(command);
				if (verdict.block) return { block: true, reason: verdict.reason };
				return undefined;
			} catch {
				// the guard never breaks a tool call of its own: a rule
				// error passes the command through
				return undefined;
			}
		});
		pi.on("tool_result", async (event, ctx) => {
			try {
				if ((event.toolName !== "edit" && event.toolName !== "write") || event.isError) return undefined;
				const p = String((event.input as { path?: unknown })?.path ?? "");
				if (!p) return undefined;
				const abs = resolveTarget(ctx.cwd, p);
				const res = await runSyntaxCheck(abs, { env, timeoutMs });
				if (!res) return undefined;
				const note = syntaxNote(displayPath(ctx.cwd, abs), res.plan, res.outcome);
				if (!note) return undefined;
				// structuredContent rides along: replacing content alone
				// would drop it
				return {
					content: [...event.content, { type: "text", text: "\n" + note }],
					structuredContent: event.structuredContent,
				};
			} catch {
				// a failed check never blocks or rewrites the edit
				return undefined;
			}
		});
		pi.registerCommand("step-guard", {
			description: GUARD_DESCRIPTION,
			handler: async (_args, ctx) => {
				if (ctx.hasUI) ctx.ui.notify(GUARD_DESCRIPTION, "info");
			},
		});
	};
}

export default createStepGuard();
