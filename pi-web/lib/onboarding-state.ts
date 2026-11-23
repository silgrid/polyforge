// The first-open onboarding's server-side state (polyforge#167, 91-onboarding).
//
// One file in the pi agent dir — `pi-web-onboarding.json` — holding the
// versioned completion state of the four steps, and nothing a step's own
// pages already store:
//
//   { "version": 1,
//     "steps": { "aihub": true, "account": false, "workspace": false, "ticket": false },
//     "dismissed": false }
//
// `steps.<id>` is true only for a finished step; a skipped step stays false
// (the ticket's 「跳过的步骤标为未完成」 — skipped and not-yet-done are the same
// state). `dismissed` is the one fact that stops the automatic popup: closing
// the wizard (完成 or 跳过) sets it, 设置 › 常规 reopening clears it, and the
// wizard then starts at the first unfinished step (owner ruling Q6).
//
// The reader never throws: a missing, torn or non-object file reads as the
// empty state. A write is one read-merge-write inside the file's serial chain,
// so two tabs completing two different steps keep both, never last-writer-
// wins over the whole file. A file that no longer parses is moved aside
// (`<path>.corrupt`) before the first fresh write, never silently discarded.
//
// `readFirstBootConnection` publishes #162's first-boot record
// (`<data-dir>/workstation/first-boot.json`, docs/workstation.md) as the
// AIHub step's connection result: a closed whitelist of the record's fields,
// never its `error` text, never anything that could be a credential.
import { mkdirSync, readFileSync, renameSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";
import { polyforgeDataRoot } from "./polyforge-cards";

export const ONBOARDING_VERSION = 1;

/** The four steps, in the order the wizard walks them (the ticket's AC2). */
export const ONBOARDING_STEPS = ["aihub", "account", "workspace", "ticket"] as const;

export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
export type OnboardingStepStatus = "complete" | "incomplete";

export interface OnboardingState {
  version: typeof ONBOARDING_VERSION;
  steps: Record<OnboardingStep, boolean>;
  dismissed: boolean;
}

/** The AIHub step's read of #162's first-boot record: the published fields
 *  (`phase`, `mode`, `defaultCell`, `server`, `project`, `userReady`,
 *  `keyReady`) and nothing else. `mode` `external` is the team mode. */
export interface OnboardingConnection {
  phase: string | null;
  mode: "local" | "external" | null;
  defaultCell: string | null;
  server: string | null;
  project: string | null;
  userReady: boolean;
  keyReady: boolean;
}

/** The client-safe view of the state the state API answers (the route adds
 *  `imageManaged`). */
export interface OnboardingView {
  version: number;
  steps: Record<OnboardingStep, OnboardingStepStatus>;
  nextStep: OnboardingStep;
  dismissed: boolean;
  connection: OnboardingConnection | null;
}

/** One file, one write chain (route copies re-evaluate this module). */
export const ONBOARDING_STATE_WRITE_CHAIN = Symbol.for("pi-web.onboardingStateWrites");

export function onboardingStatePath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "pi-web-onboarding.json");
}

export function emptyOnboardingState(): OnboardingState {
  return {
    version: ONBOARDING_VERSION,
    steps: { aihub: false, account: false, workspace: false, ticket: false },
    dismissed: false,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * One stored (or future) state → the state this version understands. A known
 * step survives as complete only when its stored value is exactly `true`;
 * every other value (absent, a string, a future step id, a nested object) is
 * incomplete. An unknown `version` is migrated the same way — the known
 * steps stay, the version is rewritten to this one, nothing is cleared
 * (owner ruling Q2). Any other shape is the empty state.
 */
export function mergeOnboardingState(raw: unknown): OnboardingState {
  if (!isRecord(raw)) return emptyOnboardingState();
  const stored = isRecord(raw.steps) ? raw.steps : {};
  const steps = {} as Record<OnboardingStep, boolean>;
  for (const step of ONBOARDING_STEPS) steps[step] = stored[step] === true;
  return { version: ONBOARDING_VERSION, steps, dismissed: raw.dismissed === true };
}

export function onboardingStepStatuses(state: OnboardingState): Record<OnboardingStep, OnboardingStepStatus> {
  const out = {} as Record<OnboardingStep, OnboardingStepStatus>;
  for (const step of ONBOARDING_STEPS) out[step] = state.steps[step] ? "complete" : "incomplete";
  return out;
}

/** The first unfinished step; with every step finished the first step is the
 *  review starting point (Q6: completed steps stay reviewable). */
export function nextOnboardingStep(state: OnboardingState): OnboardingStep {
  return ONBOARDING_STEPS.find((step) => !state.steps[step]) ?? ONBOARDING_STEPS[0];
}

/** The step after `step`, or null at the last one. */
export function followingOnboardingStep(step: OnboardingStep): OnboardingStep | null {
  const index = ONBOARDING_STEPS.indexOf(step);
  return index >= 0 && index < ONBOARDING_STEPS.length - 1 ? ONBOARDING_STEPS[index + 1] : null;
}

export function completeOnboardingStep(state: OnboardingState, step: OnboardingStep): OnboardingState {
  return { ...state, steps: { ...state.steps, [step]: true } };
}

/** A skip changes no step record: the step stays 「未完成」. */
export function skipOnboardingStep(state: OnboardingState): OnboardingState {
  return state;
}

export function dismissOnboarding(state: OnboardingState): OnboardingState {
  return { ...state, dismissed: true };
}

export function reopenOnboarding(state: OnboardingState): OnboardingState {
  return { ...state, dismissed: false };
}

/** The client view the router answers (without `imageManaged`). */
export function onboardingView(state: OnboardingState, connection: OnboardingConnection | null): OnboardingView {
  return {
    version: state.version,
    steps: onboardingStepStatuses(state),
    nextStep: nextOnboardingStep(state),
    dismissed: state.dismissed,
    connection,
  };
}

function boundedText(value: unknown, max: number): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  return trimmed !== "" && trimmed.length <= max ? trimmed : null;
}

const PHASE_PATTERN = /^[a-z][a-z0-9-]*(?:\/[a-z][a-z0-9-]*)?$/;
const PROFILE_PATTERN = /^[a-z0-9][a-z0-9-]{0,31}$/;
const PROJECT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;
const SERVER_PATTERN = /^https?:\/\/[^\s]{1,500}$/;

/**
 * #162's first-boot record → the AIHub step's connection result. A closed
 * whitelist: the published fields only, each through its own vocabulary —
 * `error` and every undeclared key (a credential, a hash, a token) never
 * cross. A non-object, or an object carrying neither a phase nor a mode, is
 * not a record (null).
 */
export function projectFirstBootRecord(raw: unknown): OnboardingConnection | null {
  if (!isRecord(raw)) return null;
  const phase = boundedText(raw.phase, 64);
  const mode = raw.mode === "local" || raw.mode === "external" ? raw.mode : null;
  if (phase === null && mode === null) return null;
  const defaultCell = boundedText(raw.defaultCell, 32);
  const server = boundedText(raw.server, 500);
  const project = boundedText(raw.project, 128);
  return {
    phase: phase !== null && PHASE_PATTERN.test(phase) ? phase : null,
    mode,
    defaultCell: defaultCell !== null && PROFILE_PATTERN.test(defaultCell) ? defaultCell : null,
    server: server !== null && SERVER_PATTERN.test(server) ? server : null,
    project: project !== null && PROJECT_PATTERN.test(project) ? project : null,
    userReady: raw.userReady === true,
    keyReady: raw.keyReady === true,
  };
}

/** The first-boot record's path (the data dir's published location). */
export function firstBootRecordPath(dataRoot: string = polyforgeDataRoot()): string {
  return join(dataRoot, "workstation", "first-boot.json");
}

/** The sanitized first-boot connection, or null when the record is absent,
 *  unreadable or not a record. Never throws. */
export function readFirstBootConnection(dataRoot: string = polyforgeDataRoot()): OnboardingConnection | null {
  try {
    return projectFirstBootRecord(JSON.parse(readFileSync(firstBootRecordPath(dataRoot), "utf8")));
  } catch {
    return null;
  }
}

function readText(path: string): string | null {
  try {
    return readFileSync(path, "utf8");
  } catch {
    return null;
  }
}

/** The stored state; absent, unreadable, torn or non-object reads as the
 *  empty state (the wizard must never be bricked by its own file). */
export function readOnboardingState(agentDir: string = defaultAgentDir()): OnboardingState {
  const text = readText(onboardingStatePath(agentDir));
  if (text === null) return emptyOnboardingState();
  try {
    return mergeOnboardingState(JSON.parse(text));
  } catch {
    return emptyOnboardingState();
  }
}

/**
 * One read-merge-write of the state file: `mutate` sees the current state and
 * answers the next one; writes are serialized per file, replaced atomically
 * with mode 0600. A file that does not parse is moved to `<path>.corrupt`
 * first, so a fresh state never silently erases an unreadable record.
 */
export function writeOnboardingState(
  mutate: (current: OnboardingState) => OnboardingState,
  agentDir: string = defaultAgentDir(),
): Promise<OnboardingState> {
  const path = onboardingStatePath(agentDir);
  return serializeByKey(ONBOARDING_STATE_WRITE_CHAIN, path, async () => {
    const text = readText(path);
    if (text !== null) {
      try {
        JSON.parse(text);
      } catch {
        renameSync(path, `${path}.corrupt`);
      }
    }
    const next = mergeOnboardingState(mutate(readOnboardingState(agentDir)));
    mkdirSync(dirname(path), { recursive: true });
    writePrivateFileAtomicSync(path, `${JSON.stringify(next, null, 2)}\n`);
    return next;
  });
}

/** Apply one state API action to the stored state and answer the new state. */
export async function applyOnboardingAction(
  action: "complete" | "skip" | "dismiss" | "reopen",
  step: OnboardingStep | null,
  agentDir: string = defaultAgentDir(),
): Promise<OnboardingState> {
  return writeOnboardingState((current) => {
    if (action === "complete" && step) return completeOnboardingStep(current, step);
    if (action === "skip") return skipOnboardingStep(current);
    if (action === "dismiss") return dismissOnboarding(current);
    if (action === "reopen") return reopenOnboarding(current);
    return current;
  }, agentDir);
}