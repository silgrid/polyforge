import { Type } from "typebox";
import { defineTool, type InlineExtension, type ToolExposure } from "@earendil-works/pi-coding-agent";
import { BG_OUTPUT_MAX_BYTES, utf8Truncate } from "./bg-protocol";
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

export function createBgTasksExtension(controller: BgTaskController): InlineExtension {
  void controller;
  void Type;
  void defineTool;
  void utf8Truncate;
  void BG_OUTPUT_MAX_BYTES;
  void BG_TOOL_EXPOSURE;
  return {
    name: BG_TOOLS_EXTENSION_NAME,
    hidden: true,
    factory: () => {},
  };
}
