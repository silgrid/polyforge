// carries: internal/polyforge/runtime/stepagentassets/extensions/guard/rules*
//
// rules.test.ts: checkBashCommand's verdicts over the ticket's case table
// (polyforge#214 AC1) and the syntax-check plan/interpretation pure
// functions (AC2's logic). Expected values are derived from the ticket's
// rule spec and from the checkers' observed output formats, never from the
// implementation's own output.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
	MAX_SLEEP_SECONDS,
	checkBashCommand,
	syntaxCheckPlan,
	interpretCheckerRun,
	checkJsonText,
	type SyntaxCheckPlan,
} from "./rules.ts";

// ---------------------------------------------------------------- bash rules

// The ticket's case table: input -> block? + rule + trigger. Blocked cases
// additionally assert the reason prefix and, where named, the seconds.
const blocked: Array<[string, string, string]> = [
	// [input, rule, trigger]
	["(npm ci > log; echo exit=$? >> log) &", "background", "`&`"],
	["for i in 1 2 3; do curl -s localhost:8080 && break; sleep 5; done", "long-sleep", "`sleep 5`"],
	["sleep 300; tail -n 20 build.log", "long-sleep", "`sleep 300`"],
	["sleep 3.5", "long-sleep", "`sleep 3.5`"],
	["sleep 1m", "long-sleep", "`sleep 1m`"],
	["sleep 1 2 1", "long-sleep", "`sleep 1 2 1`"],
	["sleep infinity", "long-sleep", "`sleep infinity`"],
	["nohup ./server > log 2>&1 &", "detach", "`nohup`"],
	["setsid ./server", "detach", "`setsid`"],
	["./server & disown", "detach", "`disown`"],
	["a & wait; b &", "background", "`&`"],
	["x=1 sleep 5", "long-sleep", "`sleep 5`"],
	["env FOO=1 sleep 5", "long-sleep", "`sleep 5`"],
	["cmd && sleep 300", "long-sleep", "`sleep 300`"],
	["make |& sleep 300", "long-sleep", "`sleep 300`"],
	["a || sleep 300", "long-sleep", "`sleep 300`"],
	["npm ci > log 2>&1 &\necho started", "background", "`&`"],
];
for (const [input, rule, trigger] of blocked) {
	test(`checkBashCommand blocks: ${JSON.stringify(input)}`, () => {
		const v = checkBashCommand(input);
		assert.equal(v.block, true, `must block: ${JSON.stringify(input)}`);
		if (!v.block) return;
		assert.equal(v.rule, rule);
		assert.equal(v.trigger, trigger);
		assert.ok(v.reason.startsWith("step guard: blocked"), `reason prefix: ${v.reason}`);
	});
}

const allowed: string[] = [
	"cmd & wait",
	"a && b",
	"for i in $(seq 1 30); do test -f done.flag && break; sleep 2; done",
	"sleep 3",
	"sleep 0.5",
	'sleep "$n"',
	"npm test 2>&1 | tee log",
	"go test ./... &> log",
	"make |& tee x",
	"echo a >&2",
	"server & pid=$!; curl -s localhost; kill $pid",
	"trap 'kill $pid' EXIT; server & pid=$!; curl -s localhost",
	'echo "a & b"',
	"echo 'nohup'",
	"grep -n setsid f",
	'git commit -m "sleep 10 &"',
	"cat > x.sh <<'EOF'\nsleep 10 &\nEOF",
	"ls # sleep 10 &",
	"echo $((5 & 3))",
	"bash -c 'sleep 10 &'",
	"",
	"while sleep 2; do test -f done && break; done",
];
for (const input of allowed) {
	test(`checkBashCommand allows: ${JSON.stringify(input)}`, () => {
		assert.deepEqual(checkBashCommand(input), { block: false });
	});
}

// A `while`/`if`/`until` keyword resumes the command position, so the
// sleep it guards is judged too (owner plan-review correction #5).
test("checkBashCommand: while sleep 5; do … is a long sleep", () => {
	const v = checkBashCommand("while sleep 5; do curl -s localhost > /dev/null; done");
	assert.equal(v.block, true);
	if (v.block) {
		assert.equal(v.rule, "long-sleep");
		assert.ok(v.reason.includes("5 s"), `reason names the seconds: ${v.reason}`);
	}
});

test("checkBashCommand: the boundary is MAX_SLEEP_SECONDS", () => {
	assert.equal(MAX_SLEEP_SECONDS, 3);
});

test("checkBashCommand: seconds are summed and formatted per arg", () => {
	const v = checkBashCommand("sleep 2.5 2.5");
	assert.equal(v.block, true);
	if (v.block) {
		assert.equal(v.rule, "long-sleep");
		assert.ok(v.reason.includes("5 s"), `2.5 + 2.5 -> 5: ${v.reason}`);
	}
	const inf = checkBashCommand("sleep infinity");
	assert.equal(inf.block, true);
	if (inf.block) assert.ok(inf.reason.includes("waits infinity s"), `infinity verbatim: ${inf.reason}`);
});

// ------------------------------------------------- syntax check plan

test("syntaxCheckPlan: types without a checker are not handled", () => {
	assert.equal(syntaxCheckPlan("/x/a.md"), undefined);
	assert.equal(syntaxCheckPlan("/x/noext"), undefined);
});

test("syntaxCheckPlan: JSONC config files are not checked", () => {
	assert.equal(syntaxCheckPlan("/x/tsconfig.json"), undefined);
	assert.equal(syntaxCheckPlan("/x/jsconfig.json"), undefined);
	assert.equal(syntaxCheckPlan("/x/devcontainer.json"), undefined);
	assert.equal(syntaxCheckPlan("/x/.vscode/settings.json"), undefined);
});

test("syntaxCheckPlan: extensions are case-insensitive", () => {
	const p = syntaxCheckPlan("/x/A.TS");
	assert.ok(p, "A.TS gets a plan");
	assert.equal(p.checker, "typescript");
});

test("syntaxCheckPlan: go goes to gofmt -e", () => {
	const p = syntaxCheckPlan("/x/a.go");
	assert.ok(p);
	assert.equal(p.checker, "gofmt");
	assert.equal(p.tool, "gofmt");
	assert.deepEqual(p.argv, ["gofmt", "-e", "/x/a.go"]);
});

test("syntaxCheckPlan: js/mjs/cjs go to node --check", () => {
	for (const f of ["/x/a.js", "/x/a.mjs", "/x/a.cjs"]) {
		const p = syntaxCheckPlan(f);
		assert.ok(p, f);
		assert.equal(p!.checker, "node-check");
		assert.deepEqual(p!.argv, ["node", "--check", f]);
	}
});

test("syntaxCheckPlan: ts/tsx go to the two-layer typescript check", () => {
	for (const f of ["/x/a.ts", "/x/a.mts", "/x/a.cts", "/x/a.tsx"]) {
		const p = syntaxCheckPlan(f);
		assert.ok(p, f);
		assert.equal(p!.checker, "typescript");
		assert.equal(p!.argv[0], "node");
		assert.equal(p!.argv[1], "--no-warnings");
	}
});

test("syntaxCheckPlan: py goes to the in-process compile() script", () => {
	const p = syntaxCheckPlan("/x/a.py");
	assert.ok(p);
	assert.equal(p.checker, "python");
	assert.equal(p.argv[0], "python3");
	assert.equal(p.argv[1], "-I");
});

test("syntaxCheckPlan: json is parsed in-process, no argv", () => {
	const p = syntaxCheckPlan("/x/a.json");
	assert.ok(p);
	assert.equal(p.checker, "json");
	assert.equal(p.tool, "");
	assert.deepEqual(p.argv, []);
});

// --------------------------------------------- interpretCheckerRun

const goPlan = syntaxCheckPlan("/x/a.go") as SyntaxCheckPlan;
const jsPlan = syntaxCheckPlan("/x/a.js") as SyntaxCheckPlan;
const tsPlan = syntaxCheckPlan("/x/a.ts") as SyntaxCheckPlan;
const pyPlan = syntaxCheckPlan("/x/a.py") as SyntaxCheckPlan;

test("interpretCheckerRun: exit 0 is ok", () => {
	assert.deepEqual(interpretCheckerRun(goPlan, { code: 0, stdout: "", stderr: "" }), { kind: "ok" });
});

test("interpretCheckerRun: gofmt reports line and column", () => {
	const o = interpretCheckerRun(goPlan, {
		code: 2,
		stdout: "",
		stderr: "/x/a.go:5:1: expected declaration, found '}'\n",
	});
	assert.equal(o.kind, "error");
	if (o.kind === "error") {
		assert.equal(o.finding.line, 5);
		assert.equal(o.finding.column, 1);
		assert.equal(o.finding.message, "expected declaration, found '}'");
	}
});

test("interpretCheckerRun: gofmt output that matches no position is a failed check", () => {
	const o = interpretCheckerRun(goPlan, { code: 2, stdout: "", stderr: "panic: boom\n" });
	assert.equal(o.kind, "failed");
});

test("interpretCheckerRun: node --check reports the line, message from the SyntaxError line", () => {
	const o = interpretCheckerRun(jsPlan, {
		code: 1,
		stdout: "",
		stderr: "/x/a.js:2\n}\n^\n\nSyntaxError: Unexpected token '}'\n    at wrapSafe\n",
	});
	assert.equal(o.kind, "error");
	if (o.kind === "error") {
		assert.equal(o.finding.line, 2);
		assert.equal(o.finding.column, undefined);
		assert.equal(o.finding.message, "Unexpected token '}'");
	}
});

test("interpretCheckerRun: typescript first layer reports line[:col]: msg", () => {
	const withCol = interpretCheckerRun(tsPlan, {
		code: 1,
		stdout: "",
		stderr: "/x/a.ts:4:1: Declaration or statement expected.\n",
	});
	assert.equal(withCol.kind, "error");
	if (withCol.kind === "error") {
		assert.equal(withCol.finding.line, 4);
		assert.equal(withCol.finding.column, 1);
		assert.equal(withCol.finding.message, "Declaration or statement expected.");
	}
	const bare = interpretCheckerRun(tsPlan, {
		code: 1,
		stdout: "",
		stderr: "/x/a.ts:4: Expression expected\n",
	});
	assert.equal(bare.kind, "error");
	if (bare.kind === "error") assert.equal(bare.finding.line, 4);
});

test("interpretCheckerRun: typescript exit 3 is a skip with the stdout reason", () => {
	const o = interpretCheckerRun(tsPlan, {
		code: 3,
		stdout: "no TypeScript parser resolvable from the file",
		stderr: "",
	});
	assert.equal(o.kind, "skipped");
	if (o.kind === "skipped") assert.equal(o.why, "no TypeScript parser resolvable from the file");
});

test("interpretCheckerRun: python reports line and column", () => {
	const o = interpretCheckerRun(pyPlan, {
		code: 1,
		stdout: "",
		stderr: "/x/a.py:3:1: unmatched ')'\n",
	});
	assert.equal(o.kind, "error");
	if (o.kind === "error") {
		assert.equal(o.finding.line, 3);
		assert.equal(o.finding.column, 1);
		assert.equal(o.finding.message, "unmatched ')'");
	}
});

test("interpretCheckerRun: a missing checker is a skip, not a failure", () => {
	const o = interpretCheckerRun(goPlan, { code: null, stdout: "", stderr: "", spawnErrorCode: "ENOENT" });
	assert.equal(o.kind, "skipped");
	if (o.kind === "skipped") assert.ok(o.why.includes("not found on PATH"), o.why);
});

test("interpretCheckerRun: a timed-out or crashed check failed, never blocks the edit", () => {
	assert.equal(interpretCheckerRun(goPlan, { code: null, stdout: "", stderr: "", timedOut: true }).kind, "failed");
	assert.equal(interpretCheckerRun(goPlan, { code: null, stdout: "", stderr: "", spawnErrorCode: "EACCES" }).kind, "failed");
});

// ------------------------------------------------- checkJsonText

test("checkJsonText: valid text is ok", () => {
	assert.deepEqual(checkJsonText('{"a": 1}'), { kind: "ok" });
});

test("checkJsonText: the error carries the line and column from the message", () => {
	const o = checkJsonText('{\n "a": 1\n}}\n');
	assert.equal(o.kind, "error");
	if (o.kind === "error") {
		assert.equal(o.finding.line, 3);
		assert.equal(o.finding.column, 2);
	}
});
