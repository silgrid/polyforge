// lib/polyforge-params.ts — polyforge#79: the composed params read face:
// `pf [--profile p] --json params <wi>` (one row per declared step, every
// param the lane declares on it with its FINAL value and the layer it
// comes from — "step override" (the per-ticket layer: the capsule's
// overlay record of a claimed work item, or the reserved step_params
// attrs key of a queued one), steps.<project>.<step>.params.<name> (the
// project's yaml) or bundle <lane> step <step> params.<name> (the lane
// default)). The overlay view (polyforge-overrides.ts) stays the overlay
// layer alone, per #78's ruling; this is the composed one the job
// detail, the work-item detail and the watch route read.
//
// THIS COMMIT IS THE SIGNATURE STUB ahead of u5's red replay (review
// r1#2): the exports the tests name, no behaviour — every sanitize and
// every read answers null, the floor is the empty string — so the unit's
// recorded run fails on its assertions, never on a missing module. The
// full face lands in the next commits.

/** The capability floor: the first release whose pf carries the params
 *  verb. Documentation only — nothing gates on it; an older pf refuses
 *  the unknown verb and the readers degrade to absent (the same floor
 *  shape the override view's read carries). */
export const POLYFORGE_PARAMS_MINIMUM_PF_VERSION = "";

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

/** sanitizeParamsView keeps the view's posture and rows in lane order.
 *  Null when the body is no view (no work item, no posture, no rows, a
 *  row without an id). Pure, never throws. */
export function sanitizeParamsView(_machine: unknown): PolyforgeParamsView | null {
  return null;
}

/** getPolyforgeParams reads one work item's params view:
 *  `pf [--profile p] --json params <wi>`, bounded by the one read
 *  timeout every pf read shares (`READ_TIMEOUT_MS`). Null on an
 *  unreadable body (a garbage answer is no view, never a 5xx); throws
 *  PolyforgeError on pf's refusal (its typed envelope) or a spawn failure
 *  (a timeout included). The callers compose: the watch route rides it
 *  into its own answer (G11 — the controls fetch nothing on selection)
 *  and the work-item detail route reads it once per open. */
export async function getPolyforgeParams(_wi: string, _profile?: string): Promise<PolyforgeParamsView | null> {
  return null;
}
