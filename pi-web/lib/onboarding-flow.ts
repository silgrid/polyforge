// The onboarding flow's client-safe vocabulary and pure state transitions
// (polyforge#167, 91-onboarding). Kept out of `lib/onboarding-state.ts` so the
// wizard's client bundle never pulls the server file reads: the state library
// imports this module and re-exports it for the server callers.
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


/** The popup's facts (polyforge#167): whether the state API named the image,
 *  whether the wizard was dismissed, whether a session is selected, and
 *  whether 设置 › 常规 asked to reopen it. */
export interface OnboardingPopupFacts {
  imageManaged: boolean;
  dismissed: boolean;
  hasSelectedSession: boolean;
  reopened: boolean;
}

/**
 * The one popup decision the shell calls. Image mode only; a dismissal wins
 * until a reopen's write clears it (no flash ahead of the state it shows);
 * and the automatic first-open popup belongs to the null-session branch — a
 * selected session never auto-opens it, while 设置 › 常规's reopen does
 * (Q6: reopen starts at the first unfinished step).
 */
export function onboardingPopupOpen(facts: OnboardingPopupFacts): boolean {
  return facts.imageManaged && !facts.dismissed && (!facts.hasSelectedSession || facts.reopened);
}
