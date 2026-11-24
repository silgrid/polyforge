/**
 * The step-tier catalog's pure projection (polyforge#151, 85b2) — the one
 * import-free module both the browser and the server build the tier views
 * from: parsing a catalog candidate, inferring the per-row tier state from
 * pf's tier→candidates lists, planning one tier change's key writes, and
 * grouping the shared candidate picker. pf's Host file stays the only truth
 * (`[step_tiers]`, one `step_tiers.<tier>` key per tier, its value a list of
 * `<harness>:<model>[:<effort>]` candidates — internal/polyforge/cli/
 * step_tiers.go); nothing here reads or writes a file. Its one import is
 * the fellow browser-safe schema module (the tier-name grammar is one fact
 * there), so this module never enters a server-only dependency chain.
 */

import { parseStepTierKey } from "./polyforge-config-schema";

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
  const out: StepTierItem[] = [];
  for (const item of items) {
    const tier = parseStepTierKey(item.key);
    if (tier === null) continue;
    if (!Array.isArray(item.value) || item.value.some((entry) => typeof entry !== "string")) continue;
    out.push({ tier, candidates: item.value as string[] });
  }
  return out;
}

/** A row's tier view across its harnesses. */
export type StepTierRowView =
  | { kind: "unset" }
  | { kind: "all"; tier: string }
  | { kind: "specific"; per: { harness: string; tiers: string[] }[] };

/** The entries of one tier's list that name the identity (their exact
 *  strings, efforts included), in list order, deduped. */
function identityEntries(candidates: readonly string[], identity: { harness: "pi" | "claude"; model: string }): string[] {
  const out: string[] = [];
  for (const candidate of candidates) {
    const parsed = parseStepTierCandidate(candidate);
    if (parsed && parsed.harness === identity.harness && parsed.model === identity.model && !out.includes(candidate)) out.push(candidate);
  }
  return out;
}

/** The display order the ticket settled: `default` first, the rest
 *  alphabetical (85b's own list order). */
export function stepTierDisplayOrder(tiers: readonly string[]): string[] {
  return [...tiers].sort((a, b) => (a === b ? 0 : a === "default" ? -1 : b === "default" ? 1 : a < b ? -1 : 1));
}

/** The tiers covering one model identity, in display order (default
 *  first, then the 85b catalog's alphabetical order). */
export function stepTierState(catalog: ReadonlyArray<StepTierItem>, identity: { harness: "pi" | "claude"; model: string }): string[] {
  const tiers: string[] = [];
  for (const item of catalog) {
    if (identityEntries(item.candidates, identity).length > 0) tiers.push(item.tier);
  }
  return stepTierDisplayOrder(tiers);
}

/** The row's display state: every harness the same single tier (all), no
 *  harness set (unset), or the per-harness listing (specific). */
export function stepTierRowView(harnesses: readonly string[], perHarness: Readonly<Record<string, readonly string[]>>): StepTierRowView {
  const per = harnesses.map((harness) => ({ harness, tiers: [...(perHarness[harness] ?? [])] }));
  if (per.every((entry) => entry.tiers.length === 0)) return { kind: "unset" };
  const single = per.every((entry) => entry.tiers.length === 1) && new Set(per.map((entry) => entry.tiers[0])).size === 1;
  if (single) return { kind: "all", tier: per[0].tiers[0] };
  return { kind: "specific", per };
}

/** One picker group: a tier's candidates, or the null tier's defined models
 *  no catalog candidate covers (the「未设置」group). */
export interface StepTierPickerGroup {
  tier: string | null;
  candidates: { value: string; invalid: boolean }[];
}

/** Whether a parsed candidate's identity is a defined model of its
 *  harness (Q2's identity rule: pi's provider/id, claude's name). */
function isDefinedIdentity(parsed: { harness: "pi" | "claude"; model: string }, defined: { pi: readonly string[]; claude: readonly string[] }): boolean {
  return (parsed.harness === "pi" ? defined.pi : defined.claude).includes(parsed.model);
}

/** The catalog candidates no defined model answers (the invalid references
 *  the picker marks and the models page notes), in catalog order. */
export function stepTierInvalidReferences(catalog: ReadonlyArray<StepTierItem>, defined: { pi: readonly string[]; claude: readonly string[] }): string[] {
  const out: string[] = [];
  for (const item of catalog) {
    for (const candidate of item.candidates) {
      const parsed = parseStepTierCandidate(candidate);
      if (parsed && !isDefinedIdentity(parsed, defined)) out.push(candidate);
    }
  }
  return out;
}

/** The picker's groups: the tiers in display order (default first, then
 *  85b's alphabetical), each with its candidates (an invalid one marked),
 *  then the「未设置」group of defined models no candidate covers. */
export function stepTierPickerGroups(catalog: ReadonlyArray<StepTierItem>, defined: { pi: readonly string[]; claude: readonly string[] }): StepTierPickerGroup[] {
  const groups: StepTierPickerGroup[] = [];
  for (const item of [...catalog].sort((a, b) => (a.tier === b.tier ? 0 : a.tier === "default" ? -1 : b.tier === "default" ? 1 : a.tier < b.tier ? -1 : 1))) {
    groups.push({
      tier: item.tier,
      candidates: item.candidates.map((candidate) => {
        const parsed = parseStepTierCandidate(candidate);
        return { value: candidate, invalid: parsed === null ? true : !isDefinedIdentity(parsed, defined) };
      }),
    });
  }
  const covered = new Set<string>();
  for (const item of catalog) {
    for (const candidate of item.candidates) {
      const parsed = parseStepTierCandidate(candidate);
      if (parsed && isDefinedIdentity(parsed, defined)) covered.add(`${parsed.harness}:${parsed.model}`);
    }
  }
  const unset: { value: string; invalid: boolean }[] = [];
  for (const identity of defined.pi) {
    if (!covered.has(`pi:${identity}`)) unset.push({ value: `pi:${identity}`, invalid: false });
  }
  for (const name of defined.claude) {
    if (!covered.has(`claude:${name}`)) unset.push({ value: `claude:${name}`, invalid: false });
  }
  if (unset.length > 0) groups.push({ tier: null, candidates: unset });
  return groups;
}

/** One key write a tier move plans. */
export interface StepTierWrite {
  action: "set" | "unset";
  tier: string;
  candidates: string[];
}

/** The key writes that move one harness's model identity to a target tier
 *  (or unset it): the add to the target comes first (a crash between the
 *  two keys leaves a duplicate, never a loss — the ticket's 先加后删), the
 *  identity's effort entries move exactly as they are, and a tier the
 *  removal would leave empty unsets (pf refuses an empty list). A no-op
 *  move plans nothing. */
export function stepTierWritePlan(catalog: ReadonlyArray<StepTierItem>, identity: { harness: "pi" | "claude"; model: string }, target: string | null): StepTierWrite[] {
  const entries: string[] = [];
  for (const item of catalog) {
    for (const entry of identityEntries(item.candidates, identity)) {
      if (!entries.includes(entry)) entries.push(entry);
    }
  }
  // A model no tier names yet still gains the target: its candidate is the
  // identity itself, no effort suffix (a plan that only moved existing
  // entries would leave an unset model's set a silent no-op).
  const moving = entries.length > 0 ? entries : [`${identity.harness}:${identity.model}`];
  const plan: StepTierWrite[] = [];
  if (target !== null) {
    const current = catalog.find((item) => item.tier === target)?.candidates ?? [];
    const next = [...current];
    for (const entry of moving) if (!next.includes(entry)) next.push(entry);
    if (next.length !== current.length) {
      plan.push({ action: "set", tier: target, candidates: next });
    }
  }
  for (const item of catalog) {
    if (target !== null && item.tier === target) continue;
    const remaining = item.candidates.filter((candidate) => !moving.includes(candidate));
    if (remaining.length === item.candidates.length) continue;
    if (remaining.length === 0) plan.push({ action: "unset", tier: item.tier, candidates: [] });
    else plan.push({ action: "set", tier: item.tier, candidates: remaining });
  }
  return plan;
}
