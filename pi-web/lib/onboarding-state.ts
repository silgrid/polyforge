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
// The pure vocabulary and transitions live in `lib/onboarding-flow.ts` (the
// client wizard imports that module, never this one); this file re-exports
// them and adds the file reads. `readFirstBootConnection` publishes #162's
// first-boot record (`<data-dir>/workstation/first-boot.json`,
// docs/workstation.md) as the AIHub step's connection result: a closed
// whitelist of the record's fields, never its `error` text, never anything
// that could be a credential.
import { mkdirSync, readFileSync, renameSync } from "fs";
import { dirname, join } from "path";
import { writePrivateFileAtomicSync } from "./atomic-file";
import { defaultAgentDir } from "./http-dispatcher";
import { serializeByKey } from "./key-serializer";
import { polyforgeDataRoot } from "./polyforge-cards";
import {
  completeOnboardingStep,
  dismissOnboarding,
  mergeOnboardingState,
  projectFirstBootRecord,
  reopenOnboarding,
  skipOnboardingStep,
  type OnboardingConnection,
  type OnboardingState,
  type OnboardingStep,
} from "./onboarding-flow";

export {
  ONBOARDING_STEPS,
  ONBOARDING_VERSION,
  completeOnboardingStep,
  dismissOnboarding,
  emptyOnboardingState,
  followingOnboardingStep,
  mergeOnboardingState,
  nextOnboardingStep,
  onboardingStepStatuses,
  onboardingView,
  projectFirstBootRecord,
  reopenOnboarding,
  skipOnboardingStep,
} from "./onboarding-flow";
export type {
  OnboardingConnection,
  OnboardingState,
  OnboardingStep,
  OnboardingStepStatus,
  OnboardingView,
} from "./onboarding-flow";

/** One file, one write chain (route copies re-evaluate this module). */
export const ONBOARDING_STATE_WRITE_CHAIN = Symbol.for("pi-web.onboardingStateWrites");

export function onboardingStatePath(agentDir: string = defaultAgentDir()): string {
  return join(agentDir, "pi-web-onboarding.json");
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
  if (text === null) return mergeOnboardingState(undefined);
  try {
    return mergeOnboardingState(JSON.parse(text));
  } catch {
    return mergeOnboardingState(undefined);
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