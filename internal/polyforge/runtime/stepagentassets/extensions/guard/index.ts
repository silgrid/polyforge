// index.ts: the step guard pi extension (polyforge#214). tool_call blocks
// a bash command that leaves a background job unwaited or sleeps too long
// in one go; tool_result appends a syntax-check note for a file just
// written or edited. Everything decisional lives in ./rules.ts.
// STUB (polyforge#214 u2): signatures with zero-value bodies so the
// test-first red is an assertion failure, never a missing module.

export interface StepGuardOptions {
	env?: NodeJS.ProcessEnv;
	timeoutMs?: number;
}

export function createStepGuard(_opts?: StepGuardOptions): (pi: unknown) => void {
	return (_pi: unknown) => {};
}

export async function runSyntaxCheck(
	_absPath: string,
	_opts?: StepGuardOptions,
): Promise<{ plan: unknown; outcome: unknown } | undefined> {
	return undefined;
}

export default createStepGuard();
