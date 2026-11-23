// polyforge#167 (91-onboarding): the onboarding state library's signatures,
// zero-value bodies. The unit's test commits on top of this and is red on
// assertions, then the implementation fills these in.
export const ONBOARDING_VERSION = 0;

export const ONBOARDING_STEPS = ["aihub", "account", "workspace", "ticket"] as const;

export type OnboardingStep = (typeof ONBOARDING_STEPS)[number];
export type OnboardingStepStatus = "complete" | "incomplete";

export interface OnboardingState {
  version: typeof ONBOARDING_VERSION;
  steps: Record<OnboardingStep, boolean>;
  dismissed: boolean;
}

export interface OnboardingConnection {
  phase: string | null;
  mode: "local" | "external" | null;
  defaultCell: string | null;
  server: string | null;
  project: string | null;
  userReady: boolean;
  keyReady: boolean;
}

export interface OnboardingView {
  version: number;
  steps: Record<OnboardingStep, OnboardingStepStatus>;
  nextStep: OnboardingStep;
  dismissed: boolean;
  connection: OnboardingConnection | null;
}

export const ONBOARDING_STATE_WRITE_CHAIN = Symbol.for("pi-web.onboardingStateWrites");

export function onboardingStatePath(_agentDir?: string): string {
  return "";
}

export function emptyOnboardingState(): OnboardingState {
  return { version: ONBOARDING_VERSION, steps: { aihub: false, account: false, workspace: false, ticket: false }, dismissed: false };
}

export function mergeOnboardingState(_raw: unknown): OnboardingState {
  return emptyOnboardingState();
}

export function onboardingStepStatuses(_state: OnboardingState): Record<OnboardingStep, OnboardingStepStatus> {
  return { aihub: "incomplete", account: "incomplete", workspace: "incomplete", ticket: "incomplete" };
}

export function nextOnboardingStep(_state: OnboardingState): OnboardingStep {
  return "aihub";
}

export function followingOnboardingStep(_step: OnboardingStep): OnboardingStep | null {
  return null;
}

export function completeOnboardingStep(state: OnboardingState, _step: OnboardingStep): OnboardingState {
  return state;
}

export function skipOnboardingStep(state: OnboardingState): OnboardingState {
  return state;
}

export function dismissOnboarding(state: OnboardingState): OnboardingState {
  return state;
}

export function reopenOnboarding(state: OnboardingState): OnboardingState {
  return state;
}

export function onboardingView(_state: OnboardingState, _connection: OnboardingConnection | null): OnboardingView {
  return { version: 0, steps: onboardingStepStatuses(_state), nextStep: "aihub", dismissed: false, connection: null };
}

export function projectFirstBootRecord(_raw: unknown): OnboardingConnection | null {
  return null;
}

export function firstBootRecordPath(_dataRoot?: string): string {
  return "";
}

export function readFirstBootConnection(_dataRoot?: string): OnboardingConnection | null {
  return null;
}

export function readOnboardingState(_agentDir?: string): OnboardingState {
  return emptyOnboardingState();
}

export function writeOnboardingState(mutate: (current: OnboardingState) => OnboardingState, _agentDir?: string): Promise<OnboardingState> {
  return Promise.resolve(mutate(emptyOnboardingState()));
}

export function applyOnboardingAction(_action: "complete" | "skip" | "dismiss" | "reopen", _step: OnboardingStep | null, _agentDir?: string): Promise<OnboardingState> {
  return Promise.resolve(emptyOnboardingState());
}
