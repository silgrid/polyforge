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

/** pf's candidate effort tokens (internal/common/harness/harness.go: the
 *  global Efforts list; claude accepts its own subset). Mirrored here like
 *  the schema's tier-name grammar — the one grammar both sides must agree
 *  on, with no import (this module is browser-safe). */
const TIER_EFFORTS: readonly string[] = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
/** The efforts claude accepts (harness.go's harnessEfforts subset). */
const CLAUDE_TIER_EFFORTS: readonly string[] = ["low", "medium", "high", "xhigh", "max"];

/** Parse one catalog candidate, or null when it is not
 *  `<pi|claude>:<model>[:<effort>]` — pf's own grammar, mirrored: the
 *  harness prefix up to the first colon, the effort only when the suffix
 *  after the model's last colon is one of pf's effort tokens, and pf's own
 *  refusals (an unknown harness, a model name that is also a two-effort
 *  shape, an effort the harness does not accept) — so a candidate pf would
 *  refuse never parses as defined either. A model name may itself carry
 *  colons (claude's `us.anthropic…:0` names) — only a valid effort suffix
 *  splits. */
export function parseStepTierCandidate(candidate: string): StepTierCandidate | null {
  const raw = candidate.trim();
  const colon = raw.indexOf(":");
  if (colon < 0) return null;
  const harness = raw.slice(0, colon).trim();
  if (harness !== "pi" && harness !== "claude") return null;
  let model = raw.slice(colon + 1).trim();
  let effort = "";
  const last = model.lastIndexOf(":");
  if (last >= 0) {
    const suffix = model.slice(last + 1).trim();
    if (TIER_EFFORTS.includes(suffix)) {
      effort = suffix;
      model = model.slice(0, last).trim();
      const again = model.lastIndexOf(":");
      if (again >= 0 && TIER_EFFORTS.includes(model.slice(again + 1).trim())) return null; // two efforts — pf refuses
      if (harness === "claude" && !CLAUDE_TIER_EFFORTS.includes(effort)) return null; // pf: claude does not accept it
    }
  }
  if (model === "") return null;
  return { harness, model, effort };
}
