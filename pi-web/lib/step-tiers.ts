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

/** One tier of the catalog, as `pf config list` reports it: the tier's name
 *  and its ordered candidate list. */
export interface StepTierItem {
  tier: string;
  candidates: string[];
}

/** The step_tiers items of one `pf config list` answer (the page hands the
 *  route's items here; a key with a bad tier name or a non-list value is
 *  not a catalog tier). */
export function stepTierItems(items: ReadonlyArray<{ key: string; value?: unknown }>): StepTierItem[] {
  void items;
  return [];
}

/** A row's tier view across its harnesses. */
export type StepTierRowView =
  | { kind: "unset" }
  | { kind: "all"; tier: string }
  | { kind: "specific"; per: { harness: string; tiers: string[] }[] };

/** One planned key write of a tier change (the write route's POST face). */
export interface StepTierWrite {
  action: "set" | "unset";
  tier: string;
  candidates: string[];
}

/** The tier names covering one model identity, in display order. */
export function stepTierState(catalog: ReadonlyArray<StepTierItem>, identity: { harness: "pi" | "claude"; model: string }): string[] {
  void catalog;
  void identity;
  return [];
}

/** The row's display state: every harness the same single tier (all), no
 *  harness set (unset), or the per-harness listing (specific). */
export function stepTierRowView(harnesses: readonly string[], perHarness: Readonly<Record<string, readonly string[]>>): StepTierRowView {
  void harnesses;
  void perHarness;
  return { kind: "unset" };
}

/** The key writes that move one harness's model identity to a target tier
 *  (or unset it): add-first, effort entries as they are, a vacated tier
 *  unset. */
export function stepTierWritePlan(catalog: ReadonlyArray<StepTierItem>, identity: { harness: "pi" | "claude"; model: string }, target: string | null): StepTierWrite[] {
  void catalog;
  void identity;
  void target;
  return [];
}

/** One picker group: a tier's candidates, or the null tier's defined models
 *  no catalog candidate covers (the「未设置」group). */
export interface StepTierPickerGroup {
  tier: string | null;
  candidates: { value: string; invalid: boolean }[];
}

/** The catalog candidates no defined model answers (the invalid references
 *  the picker marks and the models page notes), in catalog order. */
export function stepTierInvalidReferences(catalog: ReadonlyArray<StepTierItem>, defined: { pi: readonly string[]; claude: readonly string[] }): string[] {
  void catalog;
  void defined;
  return [];
}

/** The picker's groups: the tiers in display order (default first, then
 *  85b's alphabetical), each with its candidates (an invalid one marked),
 *  then the「未设置」group of defined models no candidate covers. */
export function stepTierPickerGroups(catalog: ReadonlyArray<StepTierItem>, defined: { pi: readonly string[]; claude: readonly string[] }): StepTierPickerGroup[] {
  void catalog;
  void defined;
  return [];
}
