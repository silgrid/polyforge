// lib/polyforge-params.ts — polyforge#79: the composed params read face:
// `pf [--profile p] --json params <wi>` (one row per declared step, every
// param the lane declares on it with its FINAL value and the layer it
// comes from — "step override" (the per-ticket layer: the capsule's
// overlay record of a claimed work item, or the reserved step_params
// attrs key of a queued one), steps.<project>.<step>.params.<name> (the
// project's yaml) or bundle <lane> step <step> params.<name> (the lane
// default)). The overlay view (polyforge-overrides.ts) stays the overlay
// layer alone, per #78's ruling; this is the composed one the job
// detail, the work-item detail and the watch route read. Pure halves
// here plus one subprocess face in the shared shape: execFile, no
// shell, degradation-first (a refusal is a thrown PolyforgeError with
// pf's typed words, an undecodable body is null — never a 5xx).

import { execFileAsync } from "@/lib/exec-file";
import { polyforgePfBin } from "@/lib/polyforge-pf-bin";
import {
  PolyforgeError,
  parseOwnerMessageRefusal,
  READ_TIMEOUT_MS,
} from "@/lib/polyforge-status";
// the pure helpers are the overrides face's own (principle 7: reuse before
// writing — review r1 N3)
import { profileArgv, toOptionalTrimmedString, toTrimmedString } from "./polyforge-overrides";

/** The capability floor: the first release whose pf carries the params
 *  verb. Documentation only — nothing gates on it; an older pf refuses
 *  the unknown verb and the readers degrade to absent (the same floor
 *  shape the override view's read carries). */
export const POLYFORGE_PARAMS_MINIMUM_PF_VERSION = "2.22.0";

/** One declared param's composed answer: its final value and the layer
 *  it comes from. */
export interface PolyforgeParamFinal {
  name: string;
  value: string;
  source?: string;
}

/** One step row of the params view: every param the lane declares on the
 *  step, sorted by name (a step that declares none carries an empty
 *  list). */
export interface PolyforgeParamsStepRow {
  id: string;
  status: string;
  params: PolyforgeParamFinal[];
}

/** The params view: the work item's posture and one row per declared
 *  step, in lane order. */
export interface PolyforgeParamsView {
  wi: string;
  posture: string;
  steps: PolyforgeParamsStepRow[];
}

/** sanitizeParamsStepRow keeps one row's facts: the trimmed id (required),
 *  the status ("not started" when none) and the declared params — each
 *  one's name (required), final value ("" for anything else) and source.
 *  Junk drops; null when the id is missing. Pure. */
function sanitizeParamsStepRow(value: unknown): PolyforgeParamsStepRow | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const row = value as Record<string, unknown>;
  const id = toTrimmedString(row.id);
  if (!id) return null;
  const params: PolyforgeParamFinal[] = [];
  if (Array.isArray(row.params)) {
    for (const entry of row.params) {
      if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
      const p = entry as Record<string, unknown>;
      const name = toTrimmedString(p.name);
      if (!name) continue;
      params.push({
        name,
        value: typeof p.value === "string" ? p.value : "",
        ...(toOptionalTrimmedString(p.source) ? { source: toOptionalTrimmedString(p.source) } : {}),
      });
    }
  }
  return { id, status: toTrimmedString(row.status) || "not started", params };
}

/** sanitizeParamsView keeps the view's posture and rows in lane order.
 *  Null when the body is no view (no work item, no posture, no rows, a
 *  row without an id). Pure, never throws. */
export function sanitizeParamsView(machine: unknown): PolyforgeParamsView | null {
  if (!machine || typeof machine !== "object" || Array.isArray(machine)) return null;
  const view = machine as Record<string, unknown>;
  const wi = toTrimmedString(view.work_item);
  const posture = toTrimmedString(view.posture);
  if (!wi || !posture || !Array.isArray(view.steps) || view.steps.length === 0) return null;
  const steps: PolyforgeParamsStepRow[] = [];
  for (const entry of view.steps) {
    const row = sanitizeParamsStepRow(entry);
    if (!row) return null;
    steps.push(row);
  }
  return { wi, posture, steps };
}

/** getPolyforgeParams reads one work item's params view:
 *  `pf [--profile p] --json params <wi>`, bounded by the one read
 *  timeout every pf read shares (`READ_TIMEOUT_MS`, polyforge#42 r1#1: a
 *  hung pf must degrade within that bound). Null on an unreadable body
 *  (a garbage answer is no view, never a 5xx); throws PolyforgeError on
 *  pf's refusal (its typed envelope) or a spawn failure (a timeout
 *  included). The callers compose: the watch route rides it into its own
 *  answer (G11 — the controls fetch nothing on selection) and the
 *  work-item detail route reads it once per open. */
export async function getPolyforgeParams(wi: string, profile?: string): Promise<PolyforgeParamsView | null> {
  let stdout: string;
  try {
    ({ stdout } = await execFileAsync(polyforgePfBin(), [...profileArgv(profile), "--json", "params", wi], { timeout: READ_TIMEOUT_MS }));
  } catch (error) {
    if (error instanceof PolyforgeError) throw error;
    if (error && typeof error === "object" && (error as { killed?: unknown }).killed === true) {
      throw new PolyforgeError(`pf did not answer within ${Math.round(READ_TIMEOUT_MS / 1000)} s`);
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
  return sanitizeParamsView(raw);
}
