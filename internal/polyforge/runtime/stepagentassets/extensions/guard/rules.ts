// rules.ts: the step guard's pure rule module — project-neutral, node:
// builtins only, never importing pi, so the claude side can reuse the
// verdict logic later. Two halves:
//
//   checkBashCommand — the bash tool_call verdicts (polyforge#214 AC1):
//   a background `&` this call never waits for, a detaching nohup /
//   setsid / disown, or one sleep over 3 s (MAX_SLEEP_SECONDS) is blocked
//   with the reason and the sanctioned shape to write instead.
//
//   the syntax half (AC2) — syntaxCheckPlan picks a checker per file
//   extension, interpretCheckerRun turns its run into an outcome,
//   checkJsonText is the in-process JSON parser and syntaxNote renders
//   the note appended to an edit/write result.
//
// The bash lexer is hand-written on purpose (no dependency): quotes are
// opaque, `#` comments out a line from a word start, heredoc bodies are
// skipped to their delimiter line, `$((…))` is opaque, and `$(…)`,
// backticks, `( )` and `{ }` are scanned flat (no scope is opened). It
// misses what a real parser would see inside `bash -c '…'`, `eval` or a
// script file — accepted (owner ruling Q3: only direct commands, no
// anti-bypass arms race).

import * as path from "node:path";

export const MAX_SLEEP_SECONDS = 3;

// ---------------------------------------------------------------------------
// bash verdicts

export type BashRule = "background" | "detach" | "long-sleep";

export type BashVerdict =
	| { block: false }
	| { block: true; rule: BashRule; trigger: string; reason: string };

// The rejection texts. The do-instead shapes follow the waiting principle:
// foreground with a logged exit code (a `timeout <seconds>` prefix when it
// can hang), or end the job inside the same call with a bounded wait.
const DO_INSTEAD_JOB =
	"Do instead:\n" +
	'- run it in the foreground and log the exit code in the same call, prefixing `timeout <seconds>` when it can hang: cmd > "$TMPDIR/cmd.log" 2>&1; echo "exit=$?" >> "$TMPDIR/cmd.log"; tail -n 50 "$TMPDIR/cmd.log"\n' +
	'- if it must run alongside other work, end it in this same call: cmd & pid=$!; ...; then wait for it with a bounded loop that checks whether it is done, sleeping at most 3 s per check (or kill it by process group before the call returns, with a trap as the backstop).';

function blockBackground(trigger: string): BashVerdict {
	return {
		block: true,
		rule: "background",
		trigger,
		reason:
			`step guard: blocked — ${trigger} starts a background job that this call does not wait for; ` +
			`once the call returns nothing watches it or reports its exit code.\n` +
			DO_INSTEAD_JOB,
	};
}

function blockDetach(trigger: string): BashVerdict {
	return {
		block: true,
		rule: "detach",
		trigger,
		reason:
			`step guard: blocked — ${trigger} detaches a process so it outlives this call.\n` +
			DO_INSTEAD_JOB,
	};
}

function blockLongSleep(trigger: string, secs: string): BashVerdict {
	return {
		block: true,
		rule: "long-sleep",
		trigger,
		reason:
			`step guard: blocked — ${trigger} waits ${secs} s in one go; a single sleep may wait at most ${MAX_SLEEP_SECONDS} s.\n` +
			'Do instead: run the work in the foreground, or wait for a real condition in this same call with a deadline, ' +
			'sleeping at most 3 s per check: for i in $(seq 1 100); do test -f "$TMPDIR/done" && break; sleep 3; done',
	};
}

// --- lexer ---------------------------------------------------------------
//
// Tokens: control operators, words (raw text kept — a `sleep 1m` trigger
// and a `trap 'kill …'` argument are read from it), and the positions a
// new command can start at.

interface Token {
	kind: "op" | "word";
	text: string; // the raw text (`'`"$n'` keeps its quotes)
	op: string; // the operator, ops only
	cmdPos: boolean; // a word may begin a command here
}

const OPS = ["&&", "||", ";;&", ";;", ";&", "|&", "|", ";", "&", "\n"];
const WRAPPERS = new Set(["command", "builtin", "exec", "env", "nice", "sudo", "timeout"]);
const RESUMERS = new Set(["do", "then", "else", "elif", "!", "time", "if", "while", "until"]);
const ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*=/;

function isOpStart(c: string): boolean {
	return ";&|\n".includes(c);
}

function lex(input: string): Token[] {
	const toks: Token[] = [];
	let i = 0;
	// opensAtWord: the next word is at a command position (start of input,
	// after an op, after `(`/`{`/`$(`/backtick, or after a resumer word).
	let opensAtWord = true;
	const pushWord = (text: string) => {
		const bare = text.replace(/^['"]|['"]$/g, "");
		toks.push({ kind: "word", text, op: "", cmdPos: opensAtWord });
		// a resumer keyword (do/then/else/elif/!/time/if/while/until) keeps
		// the command position open for the word that follows it
		opensAtWord = RESUMERS.has(bare);
	};
	const pushOp = (op: string, opensNext = true) => {
		toks.push({ kind: "op", text: op, op, cmdPos: false });
		opensAtWord = opensNext;
	};
	while (i < input.length) {
		const c = input[i];
		if (c === " " || c === "\t" || c === "\r") {
			i++;
			continue;
		}
		if (c === "\\" && i + 1 < input.length) {
			i += 2; // escaped character: consumed, not interpreted
			continue;
		}
		if (c === "'") {
			const end = input.indexOf("'", i + 1);
			const stop = end === -1 ? input.length : end;
			pushWord(input.slice(i, stop + (end === -1 ? 0 : 1)));
			i = end === -1 ? input.length : end + 1;
			continue;
		}
		if (c === '"') {
			const end = input.indexOf('"', i + 1);
			const stop = end === -1 ? input.length : end;
			pushWord(input.slice(i, stop + (end === -1 ? 0 : 1)));
			i = end === -1 ? input.length : end + 1;
			continue;
		}
		if (c === "#") {
			// a comment runs to the end of the line; only from a word start
			if (!opensAtWord && toks.length > 0) {
				const prev = toks[toks.length - 1];
				if (prev.kind === "word" && !/[ \t]/.test(input[Math.max(0, i) - 1] ?? " ")) {
					// mid-word `#` (a#b): fall through to the word lexer
				} else {
					const nl = input.indexOf("\n", i);
					i = nl === -1 ? input.length : nl;
					continue;
				}
			} else {
				const nl = input.indexOf("\n", i);
				i = nl === -1 ? input.length : nl;
				continue;
			}
		}
		if (c === "$" && input.startsWith("$((", i)) {
			const close = input.indexOf("))", i + 3);
			i = close === -1 ? input.length : close + 2;
			pushWord("$((…))");
			continue;
		}
		if (c === "$" && input[i + 1] === "(") {
			pushOp("(", true); // $(…): scanned flat, like a group
			i += 2;
			continue;
		}
		if (c === "`") {
			pushOp("(", true); // backtick: scanned flat
			i += 1;
			continue;
		}
		if (c === "(" || c === "{") {
			pushOp(c, true);
			i += 1;
			continue;
		}
		if (c === ")" || c === "}") {
			pushOp(c, true); // a close can open the next command: `( … ) &`
			i += 1;
			continue;
		}
		if (isOpStart(c)) {
			if (c === "\n") {
				pushOp("\n");
				i += 1;
				continue;
			}
			// redirections swallow their `&` (>&, <&, N>&M) — a `>` or `<`
			// reached here as a word character instead
			if (c === "&") {
				if (input[i + 1] === "&") {
					pushOp("&&", true); // a command may follow &&
					i += 2;
					continue;
				}
				if (input[i + 1] === ">") {
					// &> or &>>: redirection, never a background marker
					i += input[i + 2] === ">" ? 3 : 2;
					pushWord("&>");
					continue;
				}
				pushOp("&", true); // the background marker: the next word starts a command (`a & wait`)
				i += 1;
				continue;
			}
			if (c === "|" && input[i + 1] === "&") {
				pushOp("|&", true); // a command may follow |&
				i += 2;
				continue;
			}
			if (c === "|" && input[i + 1] === "|") {
				pushOp("||", true); // a command may follow ||
				i += 2;
				continue;
			}
			const three = input.slice(i, i + 3);
			const two = input.slice(i, i + 2);
			if (three === ";;&") {
				pushOp(";;&");
				i += 3;
				continue;
			}
			if (two === ";;" || two === ";&") {
				pushOp(two);
				i += 2;
				continue;
			}
			pushOp(c === ";" ? ";" : "|", true);
			i += 1;
			continue;
		}
		if (c === "<" && input[i + 1] === "<") {
			// heredoc: skip the body to the delimiter line
			i += 2;
			if (input[i] === "-") i++;
			let delim = "";
			let quoted = false;
			if (input[i] === "'" || input[i] === '"') {
				quoted = true;
				const q = input[i];
				const end = input.indexOf(q, i + 1);
				delim = input.slice(i + 1, end === -1 ? input.length : end);
				i = end === -1 ? input.length : end + 1;
			} else {
				while (i < input.length && /[^\s;|&\n]/.test(input[i])) delim += input[i++];
			}
			// the rest of the line holds other tokens; the body follows the
			// next newline. Scan the line normally by remembering to skip
			// after its end.
			let lineEnd = input.indexOf("\n", i);
			if (lineEnd === -1) lineEnd = input.length;
			const bodyFrom = lineEnd + 1;
			// lex the remainder of the line by recursion-free continuation:
			// simplest is to find the delimiter line and cut it out.
			let bodyEnd = bodyFrom;
			let found = false;
			while (bodyEnd <= input.length) {
				const nl = input.indexOf("\n", bodyEnd);
				const stop = nl === -1 ? input.length : nl;
				const line = quoted ? input.slice(bodyEnd, stop) : input.slice(bodyEnd, stop).replace(/^\t+/, "");
				if (delim !== "" && line === delim) {
					found = true;
					bodyEnd = nl === -1 ? input.length : nl + 1; // lex resumes AFTER the delimiter line
					break;
				}
				if (nl === -1) break;
				bodyEnd = nl + 1;
			}
			if (found && bodyEnd > 0) {
				// continue lexing after the delimiter line, keeping this
				// token stream flat
				const rest = lex(input.slice(bodyEnd));
				const head = lex(input.slice(i, lineEnd + 1));
				for (const t of head) toks.push(t);
				for (const t of rest) toks.push(t);
				return toks;
			}
			// no delimiter line: treat the whole tail as the heredoc body
			const head2 = lex(input.slice(i, lineEnd + 1));
			for (const t of head2) toks.push(t);
			return toks;
		}
		// a word: up to the next whitespace, quote, comment, op or redirect
		let word = "";
		while (i < input.length) {
			const w = input[i];
			if (w === " " || w === "\t" || w === "\r" || w === "\n") break;
			if (w === "'" || w === '"' || w === "`") break;
			if (w === "\\" && i + 1 < input.length) {
				word += input.slice(i, i + 2);
				i += 2;
				continue;
			}
			if (w === "#") break;
			if (isOpStart(w)) break;
			if (w === "$" && input.startsWith("$((", i)) break;
			if (w === "$" && input[i + 1] === "(") break;
			if (w === "(" || w === ")" || w === "{") break;
			if (w === ">" || w === "<") {
				// a redirection glued to the word (2>&1, cmd>log): end the
				// word; the redirect (with any & it swallows) is lexed next
				break;
			}
			word += w;
			i += 1;
		}
		// the redirect itself: >, >>, >&, <&, N>&M — never a background &
		if (input[i] === ">" || input[i] === "<") {
			let j = i;
			if (input[j + 1] === input[j]) j++; // >> <<
			if (input[j + 1] === "&") {
				j++;
				while (j + 1 < input.length && /[0-9-]/.test(input[j + 1])) j++;
			}
			pushOp(input.slice(i, j + 1), false);
			i = j + 1;
			continue;
		}
		if (word !== "") pushWord(word);
		else i += 1;
	}
	return toks;
}

// --- command position -----------------------------------------------------
//
// A word at a command position may still be a prefix assignment
// (`NAME=…`), a wrapper (command/builtin/exec/env/nice/sudo/timeout, env's
// own assignments included) or a wrapper's option — the command is the
// first word after all of those.

interface CmdWord {
	index: number;
	text: string;
	args: string[]; // the raw words that follow it (no ops)
}

// WRAPPER_VALUE_OPTS: a wrapper's short options that take their own
// word as a value (owner plan-review #6: skip a wrapper's options, then
// judge the real command) — the value word is skipped with its option.
const WRAPPER_VALUE_OPTS: Record<string, string[]> = {
	timeout: ["s", "k", "signal", "kill-after"],
	nice: ["n"],
	sudo: ["u", "g"],
	env: ["u"],
};

function commandWords(toks: Token[]): CmdWord[] {
	const out: CmdWord[] = [];
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i];
		if (t.kind !== "word" || !t.cmdPos) continue;
		let j = i;
		// prefix assignments and wrappers: assignments skip, a wrapper
		// skips its options (and each value option's value word), and
		// `timeout` additionally skips its DURATION operand — the first
		// word after its options. Wrappers may chain (timeout 10s nice …).
		for (;;) {
			const w = toks[j];
			if (!w || w.kind !== "word") break;
			if (ASSIGNMENT.test(w.text)) {
				j++;
				continue;
			}
			const name = strip(w.text);
			if (!WRAPPERS.has(name)) break;
			j++;
			// a wrapper's options are skipped before its command; an option
			// in WRAPPER_VALUE_OPTS takes its value word with it
			for (;;) {
				const o = toks[j];
				if (!o || o.kind !== "word") break;
				const text = strip(o.text);
				if (text !== "-" && text.startsWith("-")) {
					j++;
					const letter = text.replace(/^-+/, "");
					if ((WRAPPER_VALUE_OPTS[name] || []).includes(letter) && toks[j] && toks[j].kind === "word") {
						j++;
					}
					continue;
				}
				break;
			}
			// `timeout`'s DURATION operand: the first word after the options
			if (name === "timeout" && toks[j] && toks[j].kind === "word") j++;
			continue;
		}
		const cmd = toks[j];
		if (!cmd || cmd.kind !== "word") continue;
		const args: string[] = [];
		let k = j + 1;
		while (toks[k] && toks[k].kind === "word") {
			args.push(toks[k].text);
			k++;
		}
		out.push({ index: j, text: cmd.text, args });
		i = Math.max(i, j); // the command word is not itself re-judged
	}
	return out;
}

function strip(word: string): string {
	return word.replace(/^['"]|['"]$/g, "");
}

const SLEEP_ARG = /^(\d+(?:\.\d+)?)([smhd]?)$/;
const SLEEP_UNITS: Record<string, number> = { "": 1, s: 1, m: 60, h: 3600, d: 86400 };

function formatSeconds(total: number): string {
	// one decimal at most, an integer keeps none (owner plan-review #7)
	const rounded = Math.round(total * 10) / 10;
	return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

export function checkBashCommand(command: string): BashVerdict {
	if (typeof command !== "string" || command === "") return { block: false };
	const toks = lex(command);
	const cmds = commandWords(toks);

	// 1. detach — nohup / setsid / disown at a command position, whatever
	//    follows (owner ruling Q5: these exist to outlive the call).
	for (const c of cmds) {
		if (c.text === "nohup" || c.text === "setsid" || c.text === "disown") {
			return blockDetach(`\`${c.text}\``);
		}
	}

	// 2. background — every background `&` must be waited or killed later
	//    in the same command, or covered by a trap that kills (Q1/Q1b).
	const traps = cmds.filter((c) => c.text === "trap");
	const trapKills = traps.some((c) => c.args.some((a) => /(^|[^A-Za-z0-9_])kill([^A-Za-z0-9_]|$)/.test(a)));
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i];
		if (t.kind !== "op" || t.op !== "&") continue;
		const ended = cmds.some((c) => c.index > i && (c.text === "wait" || c.text === "kill"));
		if (ended || trapKills) continue;
		return blockBackground("`&`");
	}

	// 3. long-sleep — a command-position sleep whose literal args sum to
	//    more than MAX_SLEEP_SECONDS (owner ruling Q4: anywhere, not only
	//    in a loop). A non-literal argument ($n, "$x") is never judged.
	for (const c of cmds) {
		if (c.text !== "sleep") continue;
		if (c.args.length === 0) continue;
		let total = 0;
		let literal = true;
		for (const a of c.args) {
			if (a === "infinity") {
				total = Number.POSITIVE_INFINITY;
				continue;
			}
			const m = SLEEP_ARG.exec(strip(a));
			if (!m) {
				literal = false;
				break;
			}
			total += Number(m[1]) * SLEEP_UNITS[m[2]];
		}
		if (!literal || total <= MAX_SLEEP_SECONDS) continue;
		const secs = Number.isFinite(total) ? formatSeconds(total) : "infinity";
		return blockLongSleep(`\`sleep ${c.args.join(" ")}\``, secs);
	}
	return { block: false };
}

// ---------------------------------------------------------------------------
// syntax checks (AC2)

export type SyntaxCheckerId = "gofmt" | "node-check" | "typescript" | "python" | "json";

export interface SyntaxCheckPlan {
	checker: SyntaxCheckerId;
	tool: string;
	argv: string[];
}

export interface SyntaxFinding {
	line?: number;
	column?: number;
	message: string;
}

export type SyntaxOutcome =
	| { kind: "ok" }
	| { kind: "error"; finding: SyntaxFinding }
	| { kind: "skipped"; why: string }
	| { kind: "failed"; why: string };

export interface CheckerRun {
	code: number | null;
	stdout: string;
	stderr: string;
	spawnErrorCode?: string;
	timedOut?: boolean;
}

// TS_CHECK_SCRIPT: two layers. First createRequire(abs).resolve("typescript")
// — a project that ships the compiler gets real diagnostics. Without it,
// node's built-in stripTypeScriptTypes answers (experimental; the line
// number comes from the error's stack, `?` when it cannot be parsed). A
// .tsx file without a project compiler is skipped: strip cannot parse JSX.
const TS_CHECK_SCRIPT = `
const fs = require("node:fs");
const { createRequire } = require("node:module");
const abs = process.argv[1];
let ts = null;
try { ts = createRequire(abs)("typescript"); } catch {}
if (ts) {
	const src = fs.readFileSync(abs, "utf8");
	const out = ts.transpileModule(src, { fileName: abs, reportDiagnostics: true, compilerOptions: { jsx: ts.JsxEmit.Preserve } });
	const d = (out.diagnostics || [])[0];
	if (d) {
		let line = 0, col = 0;
		if (d.file && typeof d.start === "number") {
			const p = d.file.getLineAndCharacterOfPosition(d.start);
			line = p.line + 1; col = p.character + 1;
		}
		process.stderr.write(abs + ":" + line + ":" + col + ": " + ts.flattenDiagnosticMessageText(d.messageText, " "));
		process.exit(1);
	}
	process.exit(0);
}
if (/\\.tsx$/.test(abs)) {
	process.stdout.write("no TypeScript parser resolvable from the file");
	process.exit(3);
}
const M = require("node:module");
const src = fs.readFileSync(abs, "utf8");
try {
	M.stripTypeScriptTypes(src);
	process.exit(0);
} catch (e) {
	const m = /^[^\\n]*:(\\d+)\\n/.exec(e && e.stack ? e.stack : "");
	process.stderr.write(abs + ":" + (m ? m[1] : "?") + ": " + ((e && e.message) || "syntax error"));
	process.exit(1);
}
`;

// PY_CHECK_SCRIPT: compile() in an isolated interpreter — never
// python3 -m py_compile, which would drop a __pycache__/*.pyc beside the
// file just written and dirty the working tree.
const PY_CHECK_SCRIPT = `
import sys
p = sys.argv[1]
try:
    with open(p, "rb") as f:
        compile(f.read(), p, "exec", dont_inherit=True)
except SyntaxError as e:
    sys.stderr.write(f"{p}:{e.lineno}:{e.offset}: {e.msg}")
    sys.exit(1)
`;

function basename(p: string): string {
	const cut = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
	return cut === -1 ? p : p.slice(cut + 1);
}

function isJsoncConfig(absPath: string): boolean {
	const b = basename(absPath).toLowerCase();
	if (/^(tsconfig|jsconfig).*\.json$/.test(b)) return true;
	if (b === "devcontainer.json") return true;
	return /(^|\/)\.vscode\//.test(absPath);
}

export function syntaxCheckPlan(absPath: string): SyntaxCheckPlan | undefined {
	const ext = absPath.slice(absPath.lastIndexOf(".")).toLowerCase();
	if (ext === ".json") {
		if (isJsoncConfig(absPath)) return undefined;
		return { checker: "json", tool: "", argv: [] };
	}
	switch (ext) {
		case ".go":
			return { checker: "gofmt", tool: "gofmt", argv: ["gofmt", "-e", absPath] };
		case ".js":
		case ".mjs":
		case ".cjs":
			return { checker: "node-check", tool: "node", argv: ["node", "--check", absPath] };
		case ".ts":
		case ".mts":
		case ".cts":
		case ".tsx":
			return { checker: "typescript", tool: "node", argv: ["node", "--no-warnings", "-e", TS_CHECK_SCRIPT, "--", absPath] };
		case ".py":
			return { checker: "python", tool: "python3", argv: ["python3", "-I", "-c", PY_CHECK_SCRIPT, absPath] };
		default:
			return undefined;
	}
}

function firstLine(s: string): string {
	const i = s.indexOf("\n");
	return i === -1 ? s : s.slice(0, i);
}

export function interpretCheckerRun(plan: SyntaxCheckPlan, run: CheckerRun): SyntaxOutcome {
	if (run.code === 0) return { kind: "ok" };
	if (run.spawnErrorCode === "ENOENT") return { kind: "skipped", why: `${plan.tool || plan.checker} not found on PATH` };
	if (run.timedOut) return { kind: "failed", why: `${plan.tool || plan.checker} timed out` };
	if (run.code === null || run.code === undefined) {
		return { kind: "failed", why: run.spawnErrorCode ? `${plan.tool || plan.checker} failed to run: ${run.spawnErrorCode}` : `${plan.tool || plan.checker} failed to run` };
	}
	switch (plan.checker) {
		case "gofmt": {
			const m = /^(.*):(\d+):(\d+): (.*)$/.exec(firstLine(run.stderr));
			if (!m) return { kind: "failed", why: `${plan.tool} exited ${run.code} with no position` };
			return { kind: "error", finding: { line: Number(m[2]), column: Number(m[3]), message: m[4] } };
		}
		case "node-check": {
			if (run.code !== 1) return { kind: "failed", why: `node --check exited ${run.code}` };
			const head = /^(.*):(\d+)$/.exec(firstLine(run.stderr));
			const msgLine = run.stderr.split("\n").find((l) => l.startsWith("SyntaxError:"));
			if (!head || !msgLine) return { kind: "failed", why: `node --check output matched no position` };
			return { kind: "error", finding: { line: Number(head[2]), message: msgLine.replace(/^SyntaxError: ?/, "") } };
		}
		case "typescript": {
			if (run.code === 3) return { kind: "skipped", why: run.stdout.trim() || "no TypeScript parser resolvable from the file" };
			if (run.code !== 1) return { kind: "failed", why: `the typescript check exited ${run.code}` };
			const m = /^(.*?):(\d+)(?::(\d+))?: (.*)$/.exec(firstLine(run.stderr));
			if (!m) return { kind: "failed", why: `the typescript check output matched no position` };
			return {
				kind: "error",
				finding: { line: Number(m[2]), column: m[3] === undefined ? undefined : Number(m[3]), message: m[4] },
			};
		}
		case "python": {
			if (run.code !== 1) return { kind: "failed", why: `the python check exited ${run.code}` };
			const m = /^(.*):(\d+):(\d+): (.*)$/.exec(firstLine(run.stderr));
			if (!m) return { kind: "failed", why: `the python check output matched no position` };
			return { kind: "error", finding: { line: Number(m[2]), column: Number(m[3]), message: m[4] } };
		}
		default:
			return { kind: "failed", why: `${plan.checker} is not a subprocess checker` };
	}
}

export function checkJsonText(text: string): SyntaxOutcome {
	try {
		JSON.parse(text);
		return { kind: "ok" };
	} catch (e) {
		const message = e instanceof Error ? e.message : String(e);
		const m = /line (\d+) column (\d+)/.exec(message);
		return {
			kind: "error",
			finding: m ? { line: Number(m[1]), column: Number(m[2]), message } : { message },
		};
	}
}

export function syntaxNote(displayPath: string, plan: SyntaxCheckPlan, outcome: SyntaxOutcome): string | undefined {
	switch (outcome.kind) {
		case "ok":
			return undefined;
		case "error": {
			const at = outcome.finding.line !== undefined ? `:${outcome.finding.line}` + (outcome.finding.column !== undefined ? `:${outcome.finding.column}` : "") : "";
			return `step guard: syntax check (${plan.checker}) found an error in ${displayPath}${at}: ${outcome.finding.message}`;
		}
		case "skipped":
			return `step guard: syntax check skipped for ${displayPath}: ${outcome.why}`;
		case "failed":
			return `step guard: syntax check could not run for ${displayPath}: ${outcome.why}`;
	}
}

// ---------------------------------------------------------------------------
// build's whole-suite test guard (polyforge#243)
//
// In the build step a `bash` call that runs a repository's whole-suite
// tests — a test command declared in the full phase, run against the whole
// tree — is blocked with the sanctioned form: the build's close runs it
// once, through `fast_checks.py`, and one unit's test is run with the test
// runner's own name filter. The pattern comes from the repository
// declaration (the `verify-scope --full --json` projection, schema 1),
// never from a hardcoded command: this module only splits a command into
// (effective directory, argv) segments and compares them with the
// projection. It is pure — the caller supplies the projection and the
// starting directory.

export interface CommandSegment {
	dir: string;
	argv: string[];
}

export interface TestEntry {
	dir: string;
	argv: string[];
}

export interface RootProjection {
	root: string;
	full: TestEntry[];
	cheap: TestEntry[];
}

export interface FullSuiteVerdict {
	block: true;
	rule: "full-test";
	trigger: string;
	reason: string;
}

// The test-runner vocabulary: the leading words a declared command uses to
// run tests. `npm run <script>` counts only when the script's name carries
// `test` (a lint or build script stays out). This is stack knowledge, not a
// repository's command text (owner plan-review ruling 2).
const TEST_RUNNERS: Array<{ words: string[]; scriptIncludesTest?: boolean }> = [
	{ words: ["go", "test"] },
	{ words: ["npm", "test"] },
	{ words: ["npm", "run"], scriptIncludesTest: true },
	{ words: ["pytest"] },
	{ words: ["cargo", "test"] },
];

// parseCommandSegments splits a bash command into its command words, each
// with the effective directory a leading `cd <dir> &&` chain leaves it in.
// Prefix assignments and wrappers are dropped (commandWords), so
// `GOWORK=off go test ./...` is `go test ./...`. A `cd` target that only
// an expansion could resolve ($VAR, backtick) is not guessed: the segment
// keeps the old directory, so the caller's root lookup cannot match and the
// command passes (fail-open, #214's ruling).
export function parseCommandSegments(command: string, startDir: string): CommandSegment[] {
	if (typeof command !== "string" || command === "") return [];
	const cmds = commandWords(lex(command));
	const out: CommandSegment[] = [];
	let dir = path.resolve(startDir);
	for (const c of cmds) {
		const argv = [strip(c.text), ...c.args.map(strip)];
		if (argv[0] === "cd") {
			const target = argv.slice(1).find((a) => !a.startsWith("-"));
			if (target && !/[$`]/.test(target)) dir = path.resolve(dir, target);
			continue;
		}
		out.push({ dir, argv });
	}
	return out;
}

// normalizeArgv strips the quotes the lexer kept and moves `-count=<n>` (in
// either spelling) to the end, so two spellings of one command that differ
// only in environment-assignment order or `-count` placement share a key.
export function normalizeArgv(argv: string[]): string[] {
	const out: string[] = [];
	const counts: string[] = [];
	for (let i = 0; i < argv.length; i++) {
		const a = strip(argv[i]);
		if (a === "-count" && i + 1 < argv.length) {
			counts.push(`-count=${strip(argv[i + 1])}`);
			i++;
			continue;
		}
		if (a.startsWith("-count=")) {
			counts.push(`-count=${a.slice("-count=".length)}`);
			continue;
		}
		out.push(a);
	}
	return out.concat(counts);
}

export function commandKey(dir: string, argv: string[]): string {
	return path.resolve(dir) + "\u0000" + normalizeArgv(argv).join("\u0001");
}

// testRunner classifies an argv: the runner words to compare and the tail
// the whole-tree decision reads, or undefined when the command does not run
// tests at a command position.
export function testRunner(argv: string[]): { words: string[]; rest: string[] } | undefined {
	const norm = normalizeArgv(argv);
	for (const r of TEST_RUNNERS) {
		if (norm.length < r.words.length) continue;
		let same = true;
		for (let i = 0; i < r.words.length; i++) {
			if (norm[i] !== r.words[i]) {
				same = false;
				break;
			}
		}
		if (!same) continue;
		const tail = norm.slice(r.words.length);
		if (r.scriptIncludesTest && !/test/i.test(tail[0] ?? "")) continue;
		return r.scriptIncludesTest
			? { words: r.words.concat(tail[0]), rest: tail.slice(1) }
			: { words: r.words, rest: tail };
	}
	return undefined;
}

// isWholeTreeRun decides whether a test-runner invocation targets the whole
// tree: `./...` (or a bare runner, which the declaration uses for the same
// run) with no name filter. A narrower package/path/file argument or a
// filter option makes it a targeted run, which the build may make.
export function isWholeTreeRun(argv: string[]): boolean {
	const r = testRunner(argv);
	if (!r) return false;
	const rest = r.rest;
	const has = (...flags: string[]) => rest.some((a) => flags.some((f) => a === f || a.startsWith(f + "=")));
	const runner = r.words.join(" ");
	const positional = rest.filter((a) => !a.startsWith("-"));
	switch (runner) {
		case "go test":
			if (has("-run", "-skip", "-bench", "-fuzz", "-example", "-list")) return false;
			return positional.length === 0 || positional.every((a) => a === "./..." || a.endsWith("/..."));
		case "npm test":
			if (has("-t", "--testNamePattern", "--testPathPattern", "--grep", "-g", "--filter")) return false;
			if (rest.includes("--")) return false;
			return true;
		case "pytest":
			if (has("-k", "-m", "--deselect", "--ignore", "--lf", "--last-failed", "--sw")) return false;
			return positional.length === 0;
		case "cargo test":
			if (has("--test", "--lib", "--bin", "--example", "--bench")) return false;
			if (rest.includes("--")) return false;
			return positional.length === 0;
		default:
			// `npm run <script>`: the script name is the runner; its own
			// `--` arguments name a subset.
			if (runner.startsWith("npm run ")) {
				if (has("-t", "--testNamePattern", "--testPathPattern", "--grep", "-g", "--filter")) return false;
				if (rest.includes("--")) return false;
				return true;
			}
			return false;
	}
}

// judgeFullSuite blocks the first segment that runs a whole-suite test
// command declared at its effective directory. The full-phase entries are
// the candidates; an entry a cheaper phase (fast/scoped/micro or a
// base_setup) declares at the same directory is subtracted first, so a
// command the declaration itself calls cheaper stays allowed.
export function judgeFullSuite(segments: CommandSegment[], projections: RootProjection[], capsuleDir: string): FullSuiteVerdict | undefined {
	const cheap = new Set<string>();
	for (const p of projections) for (const e of p.cheap) cheap.add(commandKey(e.dir, e.argv));
	const reposRoot = path.resolve(capsuleDir, "repos");
	for (const seg of segments) {
		const agentRunner = testRunner(seg.argv);
		if (!agentRunner) continue;
		const whole = isWholeTreeRun(seg.argv);
		const agentKey = commandKey(seg.dir, seg.argv);
		for (const p of projections) {
			for (const e of p.full) {
				const entryKey = commandKey(e.dir, e.argv);
				if (cheap.has(entryKey)) continue;
				if (entryKey === agentKey) return blockFullSuite(seg, reposRoot);
				if (path.resolve(e.dir) !== path.resolve(seg.dir)) continue;
				const entryRunner = testRunner(e.argv);
				if (!entryRunner) continue;
				if (entryRunner.words.join(" ") === agentRunner.words.join(" ") && whole) {
					return blockFullSuite(seg, reposRoot);
				}
			}
		}
	}
	return undefined;
}

function blockFullSuite(seg: CommandSegment, reposRoot: string): FullSuiteVerdict {
	const trigger = "`" + seg.argv.join(" ") + "`";
	const rel = path.relative(reposRoot, seg.dir);
	const name = rel !== "" && !rel.startsWith("..") && !path.isAbsolute(rel) ? rel.split(path.sep)[0] : "<repo>";
	const scoped =
		`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo ${name} --close-scoped ` +
		`--unit-runs artifacts/build/units.json --json --out artifacts/build/scoped-close/${name}.json`;
	return {
		block: true,
		rule: "full-test",
		trigger,
		reason:
			`step guard: blocked — ${trigger} runs a repository's whole-suite tests, which the build's close runs once through the written fast-check command.\n` +
			`Do instead:\n` +
			`- the close's scoped selection, from the capsule root: ${scoped}\n` +
			`- one unit's test, filtered by the test runner's own name filter: ` +
			"`go test <package> -run '^<TestName>$' -count=1`, `pytest -k <pattern>`, or `npm test -- <file>` — " +
			`a suite with no filter runs once, in the scoped command.`,
	};
}
