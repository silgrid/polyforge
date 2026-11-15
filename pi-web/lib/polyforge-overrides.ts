// lib/polyforge-overrides.ts — polyforge#25: the per-step overlay face
// the panel reaches: `pf [--profile p] --json override <wi>` (the view:
// one row per declared step, its status, the lane's skippable
// declaration, the overlay aspects and the effective chain's source)
// and its one-action writes (set/clear the chain, skip/unskip a
// lane-declared skippable step). The overlay is the owner's move on a
// running ticket — parked (paused/blocked) included — never a step
// agent's; the daemon enforces that (DAEMON-403). Pure helpers here, one
// subprocess face in lib/polyforge-status's shape: execFile, no shell,
// degradation-first (a refusal is a thrown PolyforgeError with pf's
// typed words).

import { execFileAsync } from "@/lib/exec-file";
import { polyforgePfBin } from "@/lib/polyforge-pf-bin";
import {
  PolyforgeError,
  maskPolyforgeCredentials,
  parseOwnerMessageRefusal,
} from "@/lib/polyforge-status";

/** toTrimmedString keeps a string's trimmed form ("" for anything else). */
function toTrimmedString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** toOptionalTrimmedString keeps a non-empty trimmed string, else
 *  undefined. */
function toOptionalTrimmedString(value: unknown): string | undefined {
  const trimmed = toTrimmedString(value);
  return trimmed ? trimmed : undefined;
}

/** profileArgv is the named Cell's argv prefix (no prefix for the default
 *  Cell) — the same shape every other polyforge spawn builds. */
function profileArgv(profile: string | undefined): string[] {
  return profile ? ["--profile", profile] : [];
}

/** The capability floor: the first release whose pf carries the override
 *  verb. Documentation only — nothing gates on it; an older pf refuses the
 *  unknown verb and the watch route's composed view degrades to absent
 *  (the controls show the none-source line; the switch still renders from
 *  the snapshot's own fields). */
export const POLYFORGE_OVERRIDE_MINIMUM_PF_VERSION = "2.21.0";

/** One step row of the view. */
export interface PolyforgeOverrideStepRow {
  id: string;
  status: string;
  skippable: boolean;
  models?: string[];
  modelsBy?: string;
  skip?: boolean;
  skipBy?: string;
  pending?: boolean;
  effective?: string[];
  source?: string;
}

/** The view: the work item's posture on this daemon and one row per
 *  declared step, in lane order. */
export interface PolyforgeOverrideView {
  wi: string;
  posture: string;
  steps: PolyforgeOverrideStepRow[];
}

/** One write's answer (the step's row carries the applied aspect). */
export interface PolyforgeOverrideWriteResult {
  wi: string;
  changed: boolean;
  posture: string;
  published: boolean;
  note?: string;
  step: PolyforgeOverrideStepRow;
}

/** One overlay action, exactly one per write. */
export type PolyforgeOverrideAction =
  | { kind: "models"; step: string; models: string[] }
  | { kind: "clearModels"; step: string }
  | { kind: "skip"; step: string; value: boolean };

/** overrideActionArgv answers the pf argv of one action (the profile
 *  prefix first, exactly as every other polyforge spawn builds it), or
 *  null when the action is invalid: a blank step, an empty chain, a
 *  candidate that is blank or carries the list's own comma separator, or
 *  an unknown kind. Pure; the route refuses the body before any spawn. */
export function overrideActionArgv(wi: string, action: PolyforgeOverrideAction | null, profile?: string): string[] | null {
  if (!action || typeof action !== "object") return null;
  const step = typeof action.step === "string" ? action.step.trim() : "";
  if (!step || step.includes(" ")) return null;
  const base = [...profileArgv(profile), "--json", "override", wi, "--step", step];
  switch (action.kind) {
    case "models": {
      if (!Array.isArray(action.models) || action.models.length === 0) return null;
      const models: string[] = [];
      for (const candidate of action.models) {
        if (typeof candidate !== "string") return null;
        const trimmed = candidate.trim();
        if (!trimmed || trimmed.includes(",")) return null;
        models.push(trimmed);
      }
      return [...base, "--models", models.join(",")];
    }
    case "clearModels":
      return [...base, "--clear-models"];
    case "skip":
      return action.value === true ? [...base, "--skip"] : action.value === false ? [...base, "--unskip"] : null;
    default:
      return null;
  }
}

/** sanitizeStepRow keeps one view row's facts: the trimmed id (required),
 *  the status and source strings, the skippable declaration, the chain
 *  (a list of non-empty strings), the skip switch and the pending mark.
 *  Junk fields drop; null when the id is missing. Pure. */
function sanitizeStepRow(value: unknown): PolyforgeOverrideStepRow | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const row = value as Record<string, unknown>;
  const id = toTrimmedString(row.id);
  if (!id) return null;
  const strings = (key: string) => toOptionalTrimmedString(row[key]);
  const list = (key: string): string[] | undefined => {
    const raw = row[key];
    if (!Array.isArray(raw)) return undefined;
    const out = raw.map((entry) => (typeof entry === "string" ? entry.trim() : "")).filter(Boolean);
    return out.length > 0 ? out : undefined;
  };
  const out: PolyforgeOverrideStepRow = {
    id,
    status: toTrimmedString(row.status) || "not started",
    skippable: row.skippable === true,
    ...(list("models") ? { models: list("models") } : {}),
    ...(strings("models_by") ? { modelsBy: strings("models_by") } : {}),
    ...(typeof row.skip === "boolean" ? { skip: row.skip } : {}),
    ...(strings("skip_by") ? { skipBy: strings("skip_by") } : {}),
    ...(row.pending === true ? { pending: true } : {}),
    ...(list("effective") ? { effective: list("effective") } : {}),
    ...(strings("source") ? { source: strings("source") } : {}),
  };
  return out;
}

/** sanitizeOverrideView keeps the view's posture and rows in lane order.
 *  Null when the body is no view (no work item, no rows, a row without
 *  an id). Pure, never throws. */
export function sanitizeOverrideView(machine: unknown): PolyforgeOverrideView | null {
  if (!machine || typeof machine !== "object" || Array.isArray(machine)) return null;
  const view = machine as Record<string, unknown>;
  const wi = toTrimmedString(view.work_item);
  const posture = toTrimmedString(view.posture);
  if (!wi || !posture || !Array.isArray(view.steps) || view.steps.length === 0) return null;
  const steps: PolyforgeOverrideStepRow[] = [];
  for (const entry of view.steps) {
    const row = sanitizeStepRow(entry);
    if (!row) return null;
    steps.push(row);
  }
  return { wi, posture, steps };
}

/** sanitizeOverrideWriteResult keeps one write's outcome — the applied
 *  aspect on the step's row, the posture, the published flag and the
 *  masked, bounded note. Null when the body is no answer. Pure. */
export function sanitizeOverrideWriteResult(machine: unknown): PolyforgeOverrideWriteResult | null {
  if (!machine || typeof machine !== "object" || Array.isArray(machine)) return null;
  const res = machine as Record<string, unknown>;
  const wi = toTrimmedString(res.work_item);
  if (!wi) return null;
  const step = sanitizeStepRow(res.step);
  if (!step) return null;
  const posture = toTrimmedString(res.posture);
  const note = toOptionalTrimmedString(res.note);
  return {
    wi,
    changed: res.changed === true,
    posture: posture || "running",
    published: res.published === true,
    ...(note ? { note: maskPolyforgeCredentials(note).slice(0, STDERR_CAP) } : {}),
    step,
  };
}

/** The write's spawn bound: the daemon's own answer waits on the actor
 *  turn and the chain commit, both bounded server-side. */
const OVERRIDE_TIMEOUT_MS = 120_000;

/** The note's cap (the watch tail's own stderr bound). */
const STDERR_CAP = 512;

/** getPolyforgeOverride reads one work item's override view:
 *  `pf [--profile p] --json override <wi>`. Null on an unreadable body
 *  (a garbage answer is no view, never a 5xx); throws PolyforgeError on
 *  pf's refusal (its typed envelope) or a spawn failure. polyforge#42
 *  (G11): the caller is the watch route, which composes the view into its
 *  response and degrades a failed read to a snapshot without the field —
 *  the client's only read face for the view. */
export async function getPolyforgeOverride(wi: string, profile?: string): Promise<PolyforgeOverrideView | null> {
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(polyforgePfBin(), [...profileArgv(profile), "--json", "override", wi]));
  } catch (error) {
    if (error instanceof PolyforgeError) throw error;
    if (error && typeof error === "object" && (error as { killed?: unknown }).killed === true) {
      throw new PolyforgeError(`pf did not answer within ${Math.round(OVERRIDE_TIMEOUT_MS / 1000)} s`);
    }
    const refusal = error instanceof Error ? parseOwnerMessageRefusal(String((error as { stderr?: unknown }).stderr ?? "")) : null;
    if (refusal) throw new PolyforgeError(refusal.text, refusal.reason);
    throw PolyforgeError.fromSpawnError(error);
  }
  let raw: unknown;
  try {
    raw = JSON.parse(stdout);
  } catch {
    return null;
  }
  return sanitizeOverrideView(raw);
}

/** sendPolyforgeOverrideAction applies one overlay action:
 *  `pf [--profile p] --json override <wi> …` (the action's own argv).
 *  Throws PolyforgeError on an invalid action or pf's refusal; answers
 *  the write's result (a garbage body is a thrown error — the write's
 *  own result is the proof, unlike a read). */
export async function sendPolyforgeOverrideAction(
  wi: string,
  action: PolyforgeOverrideAction,
  profile?: string,
): Promise<PolyforgeOverrideWriteResult> {
  const argv = overrideActionArgv(wi, action, profile);
  if (!argv) throw new PolyforgeError("the override action is invalid (a step, and one action with its whole value)");
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(polyforgePfBin(), argv, { timeout: OVERRIDE_TIMEOUT_MS }));
  } catch (error) {
    if (error && typeof error === "object" && (error as { killed?: unknown }).killed === true) {
      throw new PolyforgeError(`pf did not answer within ${Math.round(OVERRIDE_TIMEOUT_MS / 1000)} s: the change may still be recorded — read the view before you send it again`);
    }
    const refusal = error instanceof Error ? parseOwnerMessageRefusal(String((error as { stderr?: unknown }).stderr ?? "")) : null;
    if (refusal) throw new PolyforgeError(refusal.text, refusal.reason);
    throw PolyforgeError.fromSpawnError(error);
  }
  let raw: unknown;
  try {
    raw = JSON.parse(stdout);
  } catch {
    throw new PolyforgeError("pf's answer was not readable — read the view before you send the change again");
  }
  const result = sanitizeOverrideWriteResult(raw);
  if (!result) throw new PolyforgeError("pf's answer was not the write's result — read the view before you send the change again");
  return result;
}
