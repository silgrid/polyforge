// pi_child_harness.mjs — the fake CLI child base of polyforge#228 AC1.
//
// The Go tests copy these bytes into a throwaway node project whose entry
// script sits under a directory literally named pi-coding-agent, the shape
// the embedded subagent extension's getPiInvocation keys on. Two modes:
//
//   parent (argv has no --mode): imports the embedded extension, runs the
//   registered `subagent` tool once against the scenario's params, and
//   prints one PFEXT-RESULT line (the parent's execPath, the extension
//   result and the updates pi would have turned into tool_execution_*
//   records).
//
//   child (argv carries --mode and PF_SUBAGENT_ID): the extension spawned
//   these bytes as its child CLI. It records its OWN birth — process.execPath,
//   the full argv, cwd, pid/ppid and the inherited marker — into
//   FAKEPI_ARGV_FILE, prints the scenario's predefined message_end records
//   as the production protocol, and exits with FAKEPI_CHILD_EXIT.
//
// The child writes the record before any protocol output, so the parent
// test can never observe a result without the argv that produced it.

import * as fs from "node:fs";

const argv = process.argv.slice(1);
const childMode = argv.includes("--mode") && Boolean(process.env.PF_SUBAGENT_ID);

if (childMode) {
	const record = {
		execPath: process.execPath,
		argv,
		cwd: process.cwd(),
		pid: process.pid,
		ppid: process.ppid,
		env: { PF_SUBAGENT_ID: process.env.PF_SUBAGENT_ID || "" },
	};
	if (process.env.FAKEPI_ARGV_FILE) {
		fs.writeFileSync(process.env.FAKEPI_ARGV_FILE, JSON.stringify(record, null, 2));
	}
	const messages = process.env.FAKEPI_CHILD_MESSAGES
		? JSON.parse(process.env.FAKEPI_CHILD_MESSAGES)
		: [
				{ role: "assistant", content: [{ type: "text", text: "M1" }], timestamp: 1000 },
				{ role: "assistant", content: [{ type: "text", text: "M2" }], timestamp: 2000 },
			];
	for (const message of messages) {
		process.stdout.write(JSON.stringify({ type: "message_end", message }) + "\n");
	}
	const code = Number.parseInt(process.env.FAKEPI_CHILD_EXIT || "0", 10);
	process.exit(Number.isFinite(code) ? code : 0);
} else {
	const scenario = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
	const out = { execPath: process.execPath, argv };
	try {
		const mod = await import("./index.ts");
		let tool;
		mod.default({ registerTool: (t) => (tool = t) });
		const controller = new AbortController();
		if (scenario.abortAfterMs) setTimeout(() => controller.abort(), scenario.abortAfterMs);
		const ctx = {
			cwd: process.cwd(),
			model: undefined,
			thinkingLevel: undefined,
			hasUI: false,
			isProjectTrusted: () => true,
		};
		const toolCallId = scenario.toolCallId || "call_pi_child_1";
		const updates = [];
		const recordLines = [];
		out.result = await tool.execute(
			toolCallId,
			scenario.params,
			controller.signal,
			(partial) => {
				updates.push(partial);
				if (scenario.producerFile) {
					// pi serializes each update the moment it is emitted; the
					// extension hands the SAME live result object every time,
					// so a snapshot taken later would turn every update into the
					// final conversation. Serialize here, exactly as pi does.
					recordLines.push(
						JSON.stringify({ type: "tool_execution_update", toolCallId, toolName: "subagent", partialResult: partial }),
					);
				}
			},
			ctx,
		);
		out.updates = updates;
		if (scenario.producerFile) {
			recordLines.push(JSON.stringify({ type: "tool_execution_end", toolCallId, toolName: "subagent", result: out.result }));
			fs.writeFileSync(scenario.producerFile, recordLines.join("\n") + "\n");
		}
	} catch (err) {
		out.error = String((err && err.stack) || err);
	}
	console.log("PFEXT-RESULT " + JSON.stringify(out));
	process.exit(0);
}
