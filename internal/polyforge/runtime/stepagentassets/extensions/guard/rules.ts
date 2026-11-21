// rules.ts: the step guard's pure rule module — project-neutral, no pi
// imports, node: builtins only, so the claude side can reuse it later.
// checkBashCommand decides the bash tool_call verdicts; the syntax side
// plans a checker per file extension and interprets its run.
// STUB (polyforge#214 u1): signatures with zero-value bodies so the
// test-first red is an assertion failure, never a missing module.

export const MAX_SLEEP_SECONDS = 3;

export type BashRule = "background" | "detach" | "long-sleep";

export type BashVerdict =
	| { block: false }
	| { block: true; rule: BashRule; trigger: string; reason: string };

export function checkBashCommand(_command: string): BashVerdict {
	return { block: false };
}

export type SyntaxCheckerId = "gofmt" | "node-check" | "typescript" | "python" | "json";

export interface SyntaxCheckPlan {
	checker: SyntaxCheckerId;
	tool: string;
	argv: string[];
}

export function syntaxCheckPlan(_absPath: string): SyntaxCheckPlan | undefined {
	return undefined;
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

export function interpretCheckerRun(_plan: SyntaxCheckPlan, _run: CheckerRun): SyntaxOutcome {
	return { kind: "failed", why: "stub" };
}

export function checkJsonText(_text: string): SyntaxOutcome {
	return { kind: "ok" };
}

export function syntaxNote(
	_displayPath: string,
	_plan: SyntaxCheckPlan,
	_outcome: SyntaxOutcome,
): string | undefined {
	return undefined;
}
