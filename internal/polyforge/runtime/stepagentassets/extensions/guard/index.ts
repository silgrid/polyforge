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
	judgeFullSuite,
	normalizeArgv,
	parseCommandSegments,
	syntaxCheckPlan,
	syntaxNote,
	testRunner,
	type CheckerRun,
	type CommandSegment,
	type RootProjection,
	type SyntaxCheckPlan,
	type SyntaxOutcome,
} from "./rules.ts";

const GUARD_DESCRIPTION =
	"Step guard: blocks unwaited background jobs, sleeps over 3 s and (in build) whole-suite test commands; appends syntax errors after edit/write";

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

// --- build's whole-suite test gate (polyforge#243) -------------------------
//
// Only the build step is gated (both lanes whose step id is `build`, the
// owner's plan-review ruling 1). The command is split into segments; when a
// segment runs tests, its effective directory is walked up to every engine
// root (a repo directly under the capsule's `repos/`, and each nested root
// with a `.polyforge/verify-scope.conf`), and each root's full-phase
// projection is read through the shared engine. Projections are cached by
// the conf's mtime and size; any failure (no engine, a non-zero exit, bad
// JSON) fails open, exactly like the rules' other catch-alls.

interface CachedProjection {
	key: string;
	promise: Promise<RootProjection | undefined>;
}

const projections = new Map<string, CachedProjection>();

function confKey(root: string): string {
	try {
		const st = fs.statSync(path.join(root, ".polyforge", "verify-scope.conf"));
		return `${st.mtimeMs}:${st.size}`;
	} catch {
		return "absent";
	}
}

function engineProjection(root: string, env: NodeJS.ProcessEnv, timeoutMs: number): Promise<unknown | undefined> {
	return new Promise((resolve) => {
		const shared = env.PF_SHARED_DIR ?? "";
		if (!shared) return resolve(undefined);
		const script = path.join(shared, "scripts", "verify-scope");
		let out = "";
		try {
			const child = spawn(env.PF_PYTHON || "python3", [script, "--full", "--json"], { cwd: root, env, timeout: timeoutMs });
			child.stdout?.on("data", (d: Buffer) => (out += d.toString()));
			child.on("error", () => resolve(undefined));
			child.on("close", (code) => {
				if (code !== 0) return resolve(undefined);
				try {
					resolve(JSON.parse(out));
				} catch {
					resolve(undefined);
				}
			});
		} catch {
			resolve(undefined);
		}
	});
}

function toProjection(root: string, report: unknown): RootProjection | undefined {
	const r = report as { commands?: unknown; base_setup?: unknown } | undefined;
	if (!r || !Array.isArray(r.commands)) return undefined;
	const full: CommandSegment[] = [];
	const cheap: CommandSegment[] = [];
	const add = (list: CommandSegment[], cmd: unknown) => {
		if (typeof cmd !== "string") return;
		for (const seg of parseCommandSegments(cmd, root)) list.push({ dir: seg.dir, argv: normalizeArgv(seg.argv) });
	};
	for (const c of r.commands) {
		const cmd = (c as { cmd?: unknown } | undefined)?.cmd;
		const phase = (c as { phase?: unknown } | undefined)?.phase;
		if (typeof cmd !== "string") continue;
		if (phase === "full") add(full, cmd);
		else add(cheap, cmd);
	}
	if (Array.isArray(r.base_setup)) for (const cmd of r.base_setup) add(cheap, cmd);
	return { root, full: full.filter((e) => testRunner(e.argv) !== undefined), cheap };
}

function rootsFor(dir: string, capsuleDir: string): string[] {
	const reposRoot = path.resolve(capsuleDir, "repos");
	const out: string[] = [];
	let d = path.resolve(dir);
	if (d === reposRoot || !d.startsWith(reposRoot + path.sep)) return out;
	while (d !== reposRoot) {
		if (path.dirname(d) === reposRoot || fs.existsSync(path.join(d, ".polyforge", "verify-scope.conf"))) {
			if (!out.includes(d)) out.push(d);
		}
		const parent = path.dirname(d);
		if (parent === d) break;
		d = parent;
	}
	return out;
}

async function loadProjection(root: string, env: NodeJS.ProcessEnv, timeoutMs: number): Promise<RootProjection | undefined> {
	const key = confKey(root);
	const cached = projections.get(root);
	if (cached && cached.key === key) return cached.promise;
	const promise = engineProjection(root, env, timeoutMs).then((report) => toProjection(root, report));
	projections.set(root, { key, promise });
	return promise;
}

async function checkBuildWholeSuite(
	command: string,
	cwd: string,
	env: NodeJS.ProcessEnv,
	timeoutMs: number,
): Promise<{ block: true; reason: string } | undefined> {
	if (env.PF_STEP !== "build") return undefined;
	const capsule = env.PF_CAPSULE ?? "";
	if (!capsule || !env.PF_SHARED_DIR) return undefined;
	const segments = parseCommandSegments(command, cwd);
	const dirs = [...new Set(segments.filter((s) => testRunner(s.argv) !== undefined).map((s) => s.dir))];
	if (dirs.length === 0) return undefined;
	const roots = new Set<string>();
	for (const dir of dirs) for (const r of rootsFor(dir, capsule)) roots.add(r);
	if (roots.size === 0) return undefined;
	const loaded: RootProjection[] = [];
	for (const r of roots) {
		const p = await loadProjection(r, env, timeoutMs);
		if (p) loaded.push(p);
	}
	if (loaded.length === 0) return undefined;
	return judgeFullSuite(segments, loaded, capsule);
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
		pi.on("tool_call", async (event, ctx) => {
			try {
				if (event.toolName !== "bash") return undefined;
				const command = String((event.input as { command?: unknown })?.command ?? "");
				const verdict = checkBashCommand(command);
				if (verdict.block) return { block: true, reason: verdict.reason };
				const whole = await checkBuildWholeSuite(command, ctx.cwd, env, timeoutMs);
				if (whole?.block) return { block: true, reason: whole.reason };
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
