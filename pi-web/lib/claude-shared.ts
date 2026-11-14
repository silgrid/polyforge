// Client-safe vocabulary of the claude code harness (#250): which
// harness a session runs on and the names its model can take (claude's
// own aliases and any full name, no fixed list).
// No Node import: the composer and the sidebar read it too.

export const PI_HARNESS = "pi";
export const CLAUDE_HARNESS = "claude";
export type SessionHarness = typeof PI_HARNESS | typeof CLAUDE_HARNESS;

/**
 * A new claude session's model: claude's own `default` alias, which the
 * CLI resolves itself (`--model default`). The list the composer offers is claude's
 * aliases from its `initialize` answer (lib/claude-probe.ts) plus Settings › Claude's
 * custom names; nothing is pinned here.
 */
export const DEFAULT_CLAUDE_MODEL = "default";

/** The provider name a claude session's model reports, beside its full name as the model id. */
export const CLAUDE_PROVIDER = "claude";

export function isClaudeHarness(value: unknown): value is typeof CLAUDE_HARNESS {
  return value === CLAUDE_HARNESS;
}

export function isSessionHarness(value: unknown): value is SessionHarness {
  return value === PI_HARNESS || value === CLAUDE_HARNESS;
}

/**
 * A stored model value: a name `--model` takes stays as it is (an alias such as `sonnet`
 * included, passed verbatim), anything else is the default. Never throws, so no record
 * is dropped.
 */
export function normalizeClaudeModel(value: unknown): string {
  return isClaudeModelName(value) ? value : DEFAULT_CLAUDE_MODEL;
}

/**
 * claude's reasoning effort levels, as `claude 2.1.312 --help` lists
 * them for `--effort`. There is no off / minimal / auto.
 */
export const CLAUDE_EFFORT_LEVELS = ["low", "medium", "high", "xhigh", "max"] as const;
export type ClaudeEffortLevel = typeof CLAUDE_EFFORT_LEVELS[number];
/** The 「默认」 entry: no `--effort`, claude's own default applies. */
export const CLAUDE_EFFORT_DEFAULT = "default";
export type ClaudeEffort = ClaudeEffortLevel | typeof CLAUDE_EFFORT_DEFAULT;

export function isClaudeEffortLevel(value: unknown): value is ClaudeEffortLevel {
  return typeof value === "string" && (CLAUDE_EFFORT_LEVELS as readonly string[]).includes(value);
}

export function isClaudeEffort(value: unknown): value is ClaudeEffort {
  return value === CLAUDE_EFFORT_DEFAULT || isClaudeEffortLevel(value);
}

/** A stored effort value: a level stays, anything else (missing, pre-#272, unknown) is the default. Never throws. */
export function normalizeClaudeEffort(value: unknown): ClaudeEffort {
  return isClaudeEffortLevel(value) ? value : CLAUDE_EFFORT_DEFAULT;
}

/**
 * Levels a model refuses, with the reason shown beside it. Evidence only: an entry
 * needs a recording of claude rejecting that level for that model, and the
 * recordings show none (lib/__fixtures__/claude-2.1.312/README.md).
 */
export const CLAUDE_EFFORT_EXCLUSIONS: Readonly<Record<string, Readonly<Partial<Record<ClaudeEffortLevel, string>>>>> = {};

/**
 * How a claude session answers a tool call claude would ask about:
 * `ask` routes claude's prompt to Pi Web (`--permission-prompts host
 * --permission-prompt-tool stdio`), `deny` lets claude deny it on its own
 * (`--permission-prompts none`). claude's own rules and permission mode decide every
 * other call either way.
 */
export const CLAUDE_PERMISSION_MODES = ["ask", "deny"] as const;
export type ClaudePermissionMode = typeof CLAUDE_PERMISSION_MODES[number];
/** The global default when nothing is stored. */
export const CLAUDE_PERMISSION_MODE_DEFAULT: ClaudePermissionMode = "ask";

export function isClaudePermissionMode(value: unknown): value is ClaudePermissionMode {
  return typeof value === "string" && (CLAUDE_PERMISSION_MODES as readonly string[]).includes(value);
}

/** The first message an image-only claude prompt is listed with, in every locale. */
export const CLAUDE_IMAGE_FIRST_MESSAGE = "[Image]";

/**
 * A claude session's first message: the prompt's text as typed when there is any,
 * else `[Image]` when it carries images, else "". The client's transient row and the
 * server's index record both use it.
 */
export function claudeFirstMessage(text: string, imageCount: number): string {
  if (text.trim()) return text;
  return imageCount > 0 ? CLAUDE_IMAGE_FIRST_MESSAGE : "";
}

/**
 * What claude did with a message sent while a turn ran: took it into the
 * running turn (`absorbed`), or kept it queued and ran it as its next turn (`queued`).
 * Carried by that user message, live and in history, as its label.
 */
export type ClaudeDelivery = "absorbed" | "queued";

/**
 * The permission modes Settings › Claude offers a new claude session,
 * as claude 2.1.322's init and `set_permission_mode` name them. `default` passes no
 * `--permission-mode` (claude's own `permissions.defaultMode` applies); claude's other
 * modes (auto, dontAsk, bypassPermissions) are not offered.
 */
export const CLAUDE_LAUNCH_PERMISSION_MODES = ["default", "acceptEdits", "plan"] as const;
export type ClaudeLaunchPermissionMode = typeof CLAUDE_LAUNCH_PERMISSION_MODES[number];
export const CLAUDE_LAUNCH_PERMISSION_MODE_DEFAULT: ClaudeLaunchPermissionMode = "default";

export function isClaudeLaunchPermissionMode(value: unknown): value is ClaudeLaunchPermissionMode {
  return typeof value === "string" && (CLAUDE_LAUNCH_PERMISSION_MODES as readonly string[]).includes(value);
}

/**
 * Whether plan mode can be offered to a claude child launched with the approval
 * mode `approvalMode` (r3, plan D11): only `ask`, the mode whose
 * `--permission-prompts host --permission-prompt-tool stdio` brings claude's approval
 * requests to Pi Web's dialog. Leaving plan is ExitPlanMode, which always asks; on `deny`
 * (`--permission-prompts none`) claude refuses what would ask, so a plan session could
 * never leave plan.
 */
export function claudePlanModeAllowed(approvalMode: ClaudePermissionMode): boolean {
  return approvalMode === "ask";
}
/** Why plan is unavailable (the API's refusal; the pages show their own localized text). */
export const CLAUDE_PLAN_UNAVAILABLE_REASON = "Plan mode needs claude tool permissions set to Ask in a dialog (Settings › General): on Deny silently claude would refuse to leave plan mode";

/**
 * A claude model name passed to `--model` verbatim: one of claude's
 * aliases (`opus`, `opus[1m]`) or a full name, Bedrock / Vertex ids included. 1–128
 * characters, never starting with `-` (it would read as a flag).
 */
const CLAUDE_MODEL_NAME = /^[A-Za-z0-9][A-Za-z0-9._:@/[\]-]*$/;
export const CLAUDE_MODEL_NAME_MAX = 128;

export function isClaudeModelName(value: unknown): value is string {
  return typeof value === "string" && value.length <= CLAUDE_MODEL_NAME_MAX && CLAUDE_MODEL_NAME.test(value);
}

/**
 * The claude versions Pi Web's fixtures were recorded with, the
 * directories lib/__fixtures__/claude-<version>/ (lib/claude-shared.test.mjs pins the
 * equality). A detected version outside it is shown as not covered by the fixtures.
 * Recording a new version adds its directory and its entry here together.
 */
export const CLAUDE_RECORDED_VERSIONS = ["2.1.312", "2.1.319", "2.1.322"] as const;

export function isClaudeRecordedVersion(value: unknown): boolean {
  return typeof value === "string" && (CLAUDE_RECORDED_VERSIONS as readonly string[]).includes(value);
}

/**
 * One entry of GET /api/claude's model list: one of claude's aliases from
 * its `initialize` answer (`source: "alias"`, with the full name it resolves to), or one
 * of Settings › Claude's custom names (`source: "custom"`). `value` goes to `--model`.
 */
export interface ClaudeModelOption {
  value: string;
  displayName?: string;
  resolvedModel?: string;
  source: "alias" | "custom";
}

/**
 * The claude features Pi Web relies on (owner ruling Q1=A), judged from Pi
 * Web's own evidence — never from claude's capability names (none of them names these)
 * nor from `claude --help`: host approvals (`approvals`), background tasks
 * (`backgroundTasks`) and the subagent's forwarded text (`subagentText`).
 */
export const CLAUDE_PI_WEB_FEATURES = ["approvals", "backgroundTasks", "subagentText"] as const;
export type ClaudeFeatureId = typeof CLAUDE_PI_WEB_FEATURES[number];
/**
 * Why a feature is judged so: `host-prompts` / `no-host-prompts` (the global
 * permission mode ask / deny: `--permission-prompts host` / `none`; r3),
 * `recorded` (the version is at least the recorded threshold), `below-recorded`,
 * `env-disabled` (CLAUDE_CODE_DISABLE_BACKGROUND_TASKS in the child's environment) and
 * `version-unknown` (no version: the probe failed). The pages render it as text.
 */
export type ClaudeFeatureBasis = "no-host-prompts" | "host-prompts" | "recorded" | "below-recorded" | "env-disabled" | "version-unknown";
export interface ClaudeFeature {
  id: ClaudeFeatureId;
  /** null: unknown. */
  available: boolean | null;
  basis: ClaudeFeatureBasis;
}

/**
 * The first claude version a recording proves each feature in (the 2.1.322
 * `subagent-forward/` and `relay/` recordings, lib/__fixtures__/claude-2.1.322/). Each is
 * one of CLAUDE_RECORDED_VERSIONS (lib/claude-shared.test.mjs); recording a newer claude
 * that changes a feature moves its threshold with that recording.
 */
export const CLAUDE_FEATURE_MIN_VERSION = { backgroundTasks: "2.1.322", subagentText: "2.1.322" } as const;

/**
 * `a` against `b`, segment by segment as numbers (a missing segment is 0): negative,
 * 0 or positive; null when either is not dot-separated digits.
 */
export function compareClaudeVersions(a: unknown, b: unknown): number | null {
  const parse = (value: unknown) => (typeof value === "string" && /^\d+(\.\d+)*$/.test(value) ? value.split(".").map(Number) : null);
  const left = parse(a);
  const right = parse(b);
  if (!left || !right) return null;
  for (let index = 0; index < Math.max(left.length, right.length); index++) {
    const difference = (left[index] ?? 0) - (right[index] ?? 0);
    if (difference !== 0) return difference;
  }
  return 0;
}

/** How many custom model names Settings › Claude keeps. */
export const CLAUDE_CUSTOM_MODELS_MAX = 50;

/** Settings › Claude: what a new claude session starts with. */
export interface ClaudeSettings {
  launchPermissionMode: ClaudeLaunchPermissionMode;
  /** Model names offered beside claude's aliases, valid (`isClaudeModelName`) and distinct. */
  customModels: string[];
  /** `--include-hook-events`: hook activity shown in the session. */
  includeHookEvents: boolean;
}
