// The claude child's argv and environment (#250, #272, #445). Pure: no I/O.
import { agentLanguageInstruction, type AgentLanguage } from "./agent-language";
import {
  CLAUDE_EFFORT_DEFAULT,
  CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT,
  type ClaudeEffort,
  type ClaudeLaunchPermissionMode,
  type ClaudePermissionMode,
} from "./claude-shared";

// Flags as `claude 2.1.312 --help` documents them (`--forward-subagent-text` is listed by
// 2.1.312, 2.1.319 and 2.1.322 alike; 2.1.322 forwards a subagent's text only with it,
//; `--permission-mode` and `--include-hook-events` as 2.1.322 lists them,
//), plus the hidden `--resume-session-at`
// of an in-session edit (; read from the 2.1.312 bundle, see
// docs/agents/claude-sessions.md); that doc states the one reduced set and the tests pin it.

/** Print mode, NDJSON user messages on stdin, stream-json events (token deltas and subagent text included) on stdout. */
export const CLAUDE_STREAM_FLAGS: readonly string[] = [
  "-p",
  "--input-format", "stream-json",
  "--output-format", "stream-json",
  "--verbose",
  "--include-partial-messages",
  "--forward-subagent-text",
];

export interface ClaudeArgvOptions {
  /** The conversation id: `--session-id` on the first launch, `--resume` on a restart. */
  sessionId: string;
  resume: boolean;
  /**
   * An in-session edit's truncating resume: the chain entry the resumed
   * conversation keeps up to, included (`--resume-session-at`, a hidden flag of claude
   * 2.1.312's print mode). Only with `resume`.
   */
  resumeAt?: string;
  /** The session's full model name, passed verbatim. */
  model: string;
  /** The session's effort: `--effort <level>` unless it is the default entry. */
  effort?: ClaudeEffort;
  /**
   * The session's Agent language: 中文 adds `--append-system-prompt`
   * with the fixed instruction; English or absent adds nothing.
   */
  agentLanguage?: AgentLanguage;
  /**
   * The permission mode the session was created with: `--permission-mode
   * <mode>` for acceptEdits / plan; default or absent passes none (claude's own setting).
   */
  launchPermissionMode?: ClaudeLaunchPermissionMode;
  /** Settings › Claude's hook switch at creation: `--include-hook-events`. */
  includeHookEvents?: boolean;
  /**
   * The session's permission mode: `ask` routes claude's permission
   * prompts to Pi Web over stdio (`--permission-prompts host --permission-prompt-tool
   * stdio`), `deny` keeps `--permission-prompts none`.
   */
  permissionMode: ClaudePermissionMode;
}

function permissionPromptFlags(mode: ClaudePermissionMode): string[] {
  return mode === "ask"
    ? ["--permission-prompts", "host", "--permission-prompt-tool", "stdio"]
    : ["--permission-prompts", "none"];
}

function appendSystemPromptFlag(language: AgentLanguage | undefined): string[] {
  const instruction = agentLanguageInstruction(language);
  return instruction === undefined ? [] : ["--append-system-prompt", instruction];
}

/**
 * The reduced argv: no preset or tool flag and no `--cwd` (the session folder is the
 * spawn's cwd). Two sets of permission words, each from its own source: the launch
 * permission mode (the session record's `launchPermissionMode`) adds
 * `--permission-mode <mode>` only when it is not the default, and `--include-hook-events`
 * follows when the record's hook switch is on; then the approval mode's words
 * (the record's `permissionMode` override or the global mode). Tools, MCP
 * servers, hooks and CLAUDE.md are the user's claude config's, and claude's own rules
 * decide every call they cover. An action that would still ask is sent to Pi Web over
 * stdio in the `ask` mode; in the `deny` mode `--permission-prompts none` makes claude
 * deny it.
 */
export function buildClaudeArgv(options: ClaudeArgvOptions): string[] {
  return [
    ...CLAUDE_STREAM_FLAGS,
    "--model", options.model,
    ...(options.effort && options.effort !== CLAUDE_EFFORT_DEFAULT ? ["--effort", options.effort] : []),
    ...appendSystemPromptFlag(options.agentLanguage),
    ...(options.launchPermissionMode && options.launchPermissionMode !== CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT ? ["--permission-mode", options.launchPermissionMode] : []),
    ...(options.includeHookEvents ? ["--include-hook-events"] : []),
    ...permissionPromptFlags(options.permissionMode),
    ...(options.resume ? ["--resume", options.sessionId] : ["--session-id", options.sessionId]),
    ...(options.resume && options.resumeAt ? ["--resume-session-at", options.resumeAt] : []),
  ];
}

/** Whether a variable of pi-web's environment stays out of the child's. */
export function isWithheldFromClaude(name: string): boolean {
  // pi-web's own password never leaves it; CLAUDECODE marks a process running inside
  // claude code, which the child must not believe of itself. NODE_ENV is the Next
  // server's mode (`next start` sets production, so the session's `npm ci` would skip
  // devDependencies) and TMUX / TMUX_PANE name the tmux pi-web runs in: without them the
  // session no longer runs inside pi-web's tmux and is no longer told a non-default
  // socket. A pi-web on tmux's default socket is still reachable by a bare `tmux` command
  // (it falls back to that socket).
  return name === "PI_WEB_PASSWORD"
    || name === "CLAUDECODE"
    || name === "NODE_ENV"
    || name === "TMUX"
    || name === "TMUX_PANE";
}

/**
 * The child's environment (#397): a copy of `base` without the names
 * `isWithheldFromClaude()` lists. The system claude runs with the user's own
 * ANTHROPIC_* / CLAUDE_* variables (CLAUDE_CONFIG_DIR only when pi-web inherited it),
 * and an `env` in the user's claude settings still applies (claude sets it itself).
 * Nothing is added. `base` is left as it is.
 */
export function buildClaudeEnv(base: NodeJS.ProcessEnv): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [name, value] of Object.entries(base)) {
    if (value === undefined || isWithheldFromClaude(name)) continue;
    env[name] = value;
  }
  return env;
}
