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
	commandKey,
	isWholeTreeRun,
	judgeFullSuite,
	normalizeArgv,
	parseCommandSegments,
	syntaxCheckPlan,
	interpretCheckerRun,
	checkJsonText,
	testRunner,
	type RootProjection,
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
	["timeout 10s sleep 60", "long-sleep", "`sleep 60`"],
	["nice -n 10 sleep 60", "long-sleep", "`sleep 60`"],
	["sudo -u bob sleep 60", "long-sleep", "`sleep 60`"],
	["env -u X sleep 60", "long-sleep", "`sleep 60`"],
	["timeout 5 nohup ./srv", "detach", "`nohup`"],
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
	"timeout 300 npm ci",
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

test("checkBashCommand: summed seconds keep one decimal (owner plan-review #7)", () => {
	const v = checkBashCommand("sleep 1.1 2.2");
	assert.equal(v.block, true);
	if (v.block) {
		assert.equal(v.rule, "long-sleep");
		assert.ok(v.reason.includes("waits 3.3 s"), `1.1 + 2.2 -> 3.3: ${v.reason}`);
	}
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

// ------------------------------------------------ build full-suite guard

const guardRoot = "/caps/repos/r";

function entry(dir: string, ...argv: string[]): { dir: string; argv: string[] } {
	return { dir, argv };
}

test("parseCommandSegments: a leading cd sets the effective directory", () => {
	const segs = parseCommandSegments("cd repos/r && go test ./... > l 2>&1; echo exit=$?", "/caps");
	assert.equal(segs.length, 2, JSON.stringify(segs));
	assert.deepEqual(segs[0], { dir: "/caps/repos/r", argv: ["go", "test", "./..."] });
	assert.equal(segs[1].dir, "/caps/repos/r");
	assert.deepEqual(segs[1].argv, ["echo", "exit=$?"]);
});

test("parseCommandSegments: assignments and wrappers are not the command word", () => {
	assert.deepEqual(parseCommandSegments("GOWORK=off go test ./... -count=1", guardRoot), [
		{ dir: guardRoot, argv: ["go", "test", "./...", "-count=1"] },
	]);
	assert.deepEqual(parseCommandSegments("timeout 10s nice -n 5 go test ./...", guardRoot), [
		{ dir: guardRoot, argv: ["go", "test", "./..."] },
	]);
});

test("parseCommandSegments: an unresolvable cd target is not guessed", () => {
	const segs = parseCommandSegments('cd "$PF_CAPSULE/repos/r" && go test ./...', "/caps");
	assert.equal(segs.length, 1);
	assert.equal(segs[0].dir, "/caps");
});

test("testRunner: the known test runners and their tails", () => {
	assert.deepEqual(testRunner(["go", "test", "./...", "-count=1"]), { words: ["go", "test"], rest: ["./...", "-count=1"] });
	assert.deepEqual(testRunner(["npm", "test"]), { words: ["npm", "test"], rest: [] });
	assert.deepEqual(testRunner(["npm", "run", "test:e2e:all"]), { words: ["npm", "run", "test:e2e:all"], rest: [] });
	assert.deepEqual(testRunner(["pytest"]), { words: ["pytest"], rest: [] });
	assert.deepEqual(testRunner(["cargo", "test"]), { words: ["cargo", "test"], rest: [] });
	for (const argv of [
		["npm", "ci"],
		["npm", "run", "lint"],
		["node_modules/.bin/tsc", "--noEmit"],
		["sh", "test/e2e-parallel.sh"],
		["python", "-m", "compileall", "-q"],
		["$PF_PYTHON", "$PF_SHARED_DIR/scripts/fast_checks.py", "--repo", "r"],
	]) {
		assert.equal(testRunner(argv), undefined, JSON.stringify(argv));
	}
});

test("isWholeTreeRun: tree targets and bare runners are whole; filters and narrow targets are not", () => {
	for (const argv of [
		["go", "test", "./..."],
		["go", "test", "./...", "-count=1"],
		["go", "test"],
		["go", "test", "-v", "./..."],
		["npm", "test"],
		["npm", "run", "test:e2e:all"],
		["pytest"],
		["cargo", "test"],
	]) {
		assert.equal(isWholeTreeRun(argv), true, JSON.stringify(argv));
	}
	for (const argv of [
		["go", "test", "./...", "-run", "TestX"],
		["go", "test", "./internal/x/", "-count=1"],
		["go", "test", "./pkg/"],
		["go", "test", "./internal/x/..."],
		["go", "test", "./pkg/...", "-count=1"],
		["npm", "test", "--", "one.test.ts"],
		["npm", "test", "one.test.ts"],
		["pytest", "-k", "x"],
		["pytest", "tests/test_x.py"],
		["cargo", "test", "--", "--exact", "name"],
	]) {
		assert.equal(isWholeTreeRun(argv), false, JSON.stringify(argv));
	}
});

test("normalizeArgv: quotes drop and -count moves to the end", () => {
	assert.deepEqual(normalizeArgv(["go", "test", "-count", "1", '"./pkg/"', "-v"]), ["go", "test", "./pkg/", "-v", "-count=1"]);
	assert.equal(
		commandKey(guardRoot, ["go", "test", "-count", "1", '"./..."']),
		commandKey(guardRoot, ["go", "test", "./...", "-count=1"]),
	);
});

test("judgeFullSuite: the declared full test command blocks literally", () => {
	const full: RootProjection = {
		root: guardRoot,
		full: [entry(guardRoot, "go", "test", "-race", "-count=1", "-timeout", "30m", "./pkg/")],
		cheap: [],
	};
	const v = judgeFullSuite(parseCommandSegments("go test -race -timeout 30m ./pkg/ -count=1", guardRoot), [full], "/caps");
	assert.ok(v, "the literal declared command is blocked");
	assert.equal(v.rule, "full-test");
	assert.match(v.reason, /fast_checks\.py" --repo r --close-scoped/);
	assert.match(v.reason, /-run/);
});

test("judgeFullSuite: a whole-tree agent run matches a runner declared with a substitution", () => {
	const full: RootProjection = { root: guardRoot, full: [entry(guardRoot, "go", "test")], cheap: [] };
	assert.ok(judgeFullSuite(parseCommandSegments("GOWORK=off go test ./... -count=1", guardRoot), [full], "/caps"));
	assert.equal(judgeFullSuite(parseCommandSegments("go test ./... -run TestX", guardRoot), [full], "/caps"), undefined);
	assert.equal(judgeFullSuite(parseCommandSegments("go test ./internal/polyforge/runtime/ -count=1", guardRoot), [full], "/caps"), undefined);
});

test("judgeFullSuite: the cd-tracking flag reaches a capsule-root command", () => {
	const full: RootProjection = { root: guardRoot, full: [entry(guardRoot, "go", "test")], cheap: [] };
	const segs = parseCommandSegments("cd repos/r && go test ./... > l 2>&1; echo exit=$?", "/caps");
	assert.ok(judgeFullSuite(segs, [full], "/caps"));
});

test("judgeFullSuite: a nested root's declaration is matched at its effective directory", () => {
	const sub = "/caps/repos/r/sub";
	const full: RootProjection = { root: sub, full: [entry(sub, "npm", "run", "test:e2e:all")], cheap: [] };
	assert.ok(judgeFullSuite(parseCommandSegments("cd sub && npm run test:e2e:all", guardRoot), [full], "/caps"));
});

test("judgeFullSuite: a cheaper-phase declaration at the same directory subtracts the full one", () => {
	const sub = "/caps/repos/r/pi-web";
	const rootProj: RootProjection = { root: guardRoot, full: [entry(sub, "npm", "test")], cheap: [] };
	const nested: RootProjection = { root: sub, full: [], cheap: [entry(sub, "npm", "test")] };
	assert.equal(judgeFullSuite(parseCommandSegments("cd pi-web && npm test", guardRoot), [rootProj, nested], "/caps"), undefined);
});

test("judgeFullSuite: a fast_checks.py command passes, and a whole-suite segment beside it does not", () => {
	const full: RootProjection = { root: guardRoot, full: [entry(guardRoot, "go", "test")], cheap: [] };
	const pass = parseCommandSegments('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo r --phase fast', guardRoot);
	assert.equal(judgeFullSuite(pass, [full], "/caps"), undefined);
	const compound = parseCommandSegments('"$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo r --phase fast && go test ./...', guardRoot);
	assert.ok(judgeFullSuite(compound, [full], "/caps"));
});

test("judgeFullSuite: a command outside the capsule's repos or a non-test command is not judged", () => {
	const full: RootProjection = { root: guardRoot, full: [entry(guardRoot, "go", "test")], cheap: [] };
	assert.equal(judgeFullSuite(parseCommandSegments("go test ./...", "/elsewhere"), [full], "/caps"), undefined);
	assert.equal(judgeFullSuite(parseCommandSegments("npm ci", guardRoot), [full], "/caps"), undefined);
});
