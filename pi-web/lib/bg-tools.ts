import { Type } from "typebox";
import { defineTool, type InlineExtension, type ToolExposure } from "@earendil-works/pi-coding-agent";
import { BG_LOG_MAX_BYTES, BG_MAX_CONCURRENT_PER_SESSION, BG_OUTPUT_MAX_BYTES, utf8Truncate, type BgTaskInfo, type BgTaskOutput } from "./bg-protocol";
import type { BgTaskController } from "./bg-runtime";

/**
 * The four background-task tools of a normal pi session (polyforge#172,
 * AC1/AC2): `bg_run` starts a command in its own process group behind the
 * module-level executor, `bg_status` lists the session's tasks, `bg_output`
 * reads a bounded tail of a task's log, `bg_stop` stops a task's process
 * group. Only rpc-manager's normal-session branch mounts this factory:
 * subagent sessions and Chat-only sessions never see the tools (AC5).
 */

export const BG_TOOLS_EXTENSION_NAME = "pi-web-bg-tasks";
export const BG_TOOL_NAMES = ["bg_run", "bg_status", "bg_output", "bg_stop"] as const;
const BG_TOOL_EXPOSURE = "model-only" satisfies ToolExposure;

/** Bytes reserved for bg_output's own prompt lines (header, range, log path). */
const BG_OUTPUT_RESERVE_BYTES = 256;

interface ToolSession {
  sessionId: string;
  cwd: string;
}

function toolSession(ctx: unknown): ToolSession {
  const source = ctx as { cwd?: string; sessionManager?: { getSessionId?: () => string } } | undefined;
  let sessionId = "";
  try {
    sessionId = source?.sessionManager?.getSessionId?.() ?? "";
  } catch {
    sessionId = "";
  }
  let cwd = "";
  try {
    cwd = source?.cwd ?? "";
  } catch {
    cwd = "";
  }
  return { sessionId, cwd };
}

const utf8Bytes = (text: string): number => Buffer.byteLength(text, "utf8");

function formatBytes(bytes: number | null): string {
  if (bytes === null || !Number.isFinite(bytes)) return "";
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

function formatDuration(startedAt: number, endedAt: number | null): string {
  const end = endedAt ?? Date.now();
  const seconds = Math.max(0, (end - startedAt) / 1000);
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m${Math.round(seconds % 60)}s`;
  return `${Math.floor(seconds / 3600)}h${Math.floor((seconds % 3600) / 60)}m`;
}

function statusLine(info: BgTaskInfo): string {
  const id = info.id;
  const running = info.status === "running";
  const duration = running || info.endedAt !== null ? formatDuration(info.startedAt, info.endedAt) : "";
  const outcome = info.status === "succeeded"
    ? `succeeded${info.exitCode !== null ? ` (exit ${info.exitCode})` : ""}`
    : info.status === "failed"
      ? info.reason === "log-limit"
        ? `failed: log reached the ${BG_LOG_MAX_BYTES / (1024 * 1024)} MiB cap`
        : info.exitCode !== null ? `failed: exit ${info.exitCode}` : info.signal !== null ? `failed: ${info.signal}` : "failed"
      : info.status === "stopped"
        ? "stopped"
        : info.status === "interrupted"
          ? "interrupted"
          : "running";
  const size = info.bytesWritten !== null ? ` ${formatBytes(info.bytesWritten)}` : "";
  return `- ${id} ${outcome}${duration ? ` ${duration}` : ""}${size} · ${info.command}`;
}

/** bg_output's answer: the excerpt, then the byte range and the full log path, all within the budget. */
function formatOutput(id: string, output: BgTaskOutput): { content: { type: "text"; text: string }[] } {
  const header = `Output of background task ${id}:\n`;
  let text = output.text;
  let footer = `\n--- log: ${output.logPath} (bytes ${output.startByte}-${output.startByte + utf8Bytes(text)} of ${output.totalBytes}${output.truncated ? ", tail excerpt" : ", full log"})`;
  // The whole answer, prompt lines included, stays within the budget (AC2);
  // a re-cut keeps the range in the footer honest.
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const total = utf8Bytes(header) + utf8Bytes(text) + utf8Bytes(footer);
    if (total <= BG_OUTPUT_MAX_BYTES) break;
    text = utf8Truncate(text, utf8Bytes(text) - (total - BG_OUTPUT_MAX_BYTES));
    footer = `\n--- log: ${output.logPath} (bytes ${output.startByte}-${output.startByte + utf8Bytes(text)} of ${output.totalBytes}${output.truncated ? ", tail excerpt" : ", full log"})`;
  }
  return { content: [{ type: "text", text: `${header}${text}${footer}` }] };
}

export function createBgTasksExtension(controller: BgTaskController): InlineExtension {
  return {
    name: BG_TOOLS_EXTENSION_NAME,
    hidden: true,
    factory: (pi) => {
      void pi;
      pi.registerTool(defineTool({
        name: "bg_run",
        label: "Run in background",
        description: `Run a shell command in the background of this session and return at once with a task id. The command runs as /bin/sh -c in the session's cwd, in its own process group, its output written to a log file (capped at ${BG_LOG_MAX_BYTES / (1024 * 1024)} MiB; past the cap the task is stopped and fails). At most ${BG_MAX_CONCURRENT_PER_SESSION} tasks may run at once per session. When the command ends, this session receives one notification with the exit code or signal, the duration and the log path. Read output with bg_output, check state with bg_status, stop a task with bg_stop.`,
        promptSnippet: "Run long shell commands in the background",
        exposure: BG_TOOL_EXPOSURE,
        promptGuidelines: [
          "Use bg_run for commands whose output nobody waits on (builds, watches, long tests).",
          "A bg_run task ends on its own; the session is notified once it does.",
        ],
        executionMode: "parallel",
        parameters: Type.Object({
          command: Type.String({ description: "The shell command line, run as /bin/sh -c in the session cwd." }),
        }),
        async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
          const command = params.command?.trim();
          if (!command) throw new Error("bg_run: command must be a non-empty string");
          const { sessionId, cwd } = toolSession(ctx);
          if (!sessionId) throw new Error("bg_run: no session to run the task in");
          const info = controller.run(sessionId, cwd, command);
          return {
            content: [{
              type: "text",
              text: `Started background task ${info.id}.\nLog: ${info.logPath}\nThe session will be notified once it ends; bg_status/bg_output/bg_stop take this task id.`,
            }],
            details: { task: info },
          };
        },
      }));

      pi.registerTool(defineTool({
        name: "bg_status",
        label: "Background task status",
        description: "List this session's background tasks (id, state, duration, output size, command), or report one task when task_id names it.",
        exposure: BG_TOOL_EXPOSURE,
        executionMode: "parallel",
        parameters: Type.Object({
          task_id: Type.Optional(Type.String({ description: "One task's id (as bg_run returned). Omit to list every task of this session." })),
        }),
        async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
          const { sessionId } = toolSession(ctx);
          const taskId = params.task_id?.trim() || undefined;
          const tasks = controller.status(sessionId, taskId);
          if (tasks.length === 0) {
            return { content: [{ type: "text", text: taskId ? `No background task ${taskId} in this session.` : "This session runs no background tasks." }] };
          }
          const lines = tasks.map(statusLine);
          return {
            content: [{ type: "text", text: `${tasks.length} background task(s):\n${lines.join("\n")}` }],
            details: { tasks },
          };
        },
      }));

      pi.registerTool(defineTool({
        name: "bg_output",
        label: "Read background output",
        description: `Read the tail of one background task's log. The whole answer (excerpt, byte range and log path) stays within ${BG_OUTPUT_MAX_BYTES} bytes; the range names where the excerpt sits in the full log, whose path the answer also carries.`,
        exposure: BG_TOOL_EXPOSURE,
        executionMode: "parallel",
        parameters: Type.Object({
          task_id: Type.String({ description: "The task's id (as bg_run returned)." }),
          max_bytes: Type.Optional(Type.Integer({
            minimum: 1,
            maximum: BG_OUTPUT_MAX_BYTES,
            description: `The excerpt's own byte cap, at most ${BG_OUTPUT_MAX_BYTES}. Default: the whole budget.`,
          })),
        }),
        async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
          const { sessionId } = toolSession(ctx);
          const taskId = params.task_id?.trim();
          if (!taskId) throw new Error("bg_output: task_id must be a non-empty string");
          const requested = params.max_bytes;
          const maxBytes = Number.isInteger(requested) && requested > 0
            ? Math.min(requested, BG_OUTPUT_MAX_BYTES - BG_OUTPUT_RESERVE_BYTES)
            : undefined;
          const output = controller.readOutput(sessionId, taskId, maxBytes);
          return formatOutput(taskId, output);
        },
      }));

      pi.registerTool(defineTool({
        name: "bg_stop",
        label: "Stop background task",
        description: "Stop one background task: its whole process group is terminated. Calling it again, or naming a task that already ended, is fine — the task keeps its terminal state.",
        exposure: BG_TOOL_EXPOSURE,
        executionMode: "parallel",
        parameters: Type.Object({
          task_id: Type.String({ description: "The task's id (as bg_run returned)." }),
        }),
        async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
          const { sessionId } = toolSession(ctx);
          const taskId = params.task_id?.trim();
          if (!taskId) throw new Error("bg_stop: task_id must be a non-empty string");
          const [current] = controller.status(sessionId, taskId);
          if (current && current.status !== "running") {
            return { content: [{ type: "text", text: `Background task ${taskId} already ${current.status}; nothing to stop.` }] };
          }
          const stopped = controller.stop(sessionId, taskId);
          return {
            content: [{ type: "text", text: `Stopped background task ${stopped.id}.` }],
            details: { task: stopped },
          };
        },
      }));
    },
  };
}
