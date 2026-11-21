/**
 * The step-tier catalog's pure projection (polyforge#151, 85b2) — the one
 * import-free module both the browser and the server build the tier views
 * from: parsing a catalog candidate, inferring the per-row tier state from
 * pf's tier→candidates lists, planning one tier change's key writes, and
 * grouping the shared candidate picker. pf's Host file stays the only truth
 * (`[step_tiers]`, one `step_tiers.<tier>` key per tier, its value a list of
 * `<harness>:<model>[:<effort>]` candidates — internal/polyforge/cli/
 * step_tiers.go); nothing here reads or writes a file.
 */

/** A catalog candidate `<harness>:<model>[:<effort>]` in its parsed form. */
export interface StepTierCandidate {
  harness: "pi" | "claude";
  model: string;
  effort: string;
}

/** Parse one catalog candidate, or null when it is not
 *  `<pi|claude>:<model>[:<effort>]` (pf's own candidate grammar — a model
 *  name cannot carry a colon). */
export function parseStepTierCandidate(candidate: string): StepTierCandidate | null {
  return null;
}
