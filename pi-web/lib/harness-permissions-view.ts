// The permission page's client-safe view model (polyforge#150 85a4): the
// shapes GET /api/harness/compare and the per-harness permission reads answer,
// the mapping of the unified approval values onto claude's native modes
// (Q1 A: 询问→default / 自动审查→auto / 不询问即拒绝→dontAsk), the unified
// blocks' per-harness values, the trust unify target and the rules text
// format. Pure: no fetch, no I/O — the page component owns the reads and
// writes, the tests pin these mappings and derivations.
import type { ApprovalMode, PermissionPayload } from "./harness/concepts";
import type { UnifiedSettingHarnessValue } from "@/components/UnifiedSetting";

/** One compared field as the compare route answers it (the client's own
 * structural type: a client module never imports the server compare module). */
export interface ComparedField {
  field: string;
  status: "consistent" | "inconsistent" | "unsupported";
  values: Record<string, { supported: boolean; neutral: string | null; raw: string | null }>;
}

/** One harness's permission payload as the page received it. */
export interface PermissionReads {
  pi: PermissionPayload | null;
  claude: PermissionPayload | null;
}

/** The unified approval-mode values the page offers (the ticket's three). */
export const UNIFIED_APPROVAL_MODES = ["ask", "auto", "deny-unprompted"] as const;
export type UnifiedApprovalMode = (typeof UNIFIED_APPROVAL_MODES)[number];

/** Q1 A: the unified value each harness's native mode maps from — claude only today. */
export const UNIFIED_TO_CLAUDE_MODE: Readonly<Record<UnifiedApprovalMode, string>> = {
  ask: "default",
  auto: "auto",
  "deny-unprompted": "dontAsk",
};

/** The three values' label keys (one fact, the pages and the tests read it). */
export const UNIFIED_APPROVAL_LABEL_KEYS: Readonly<Record<UnifiedApprovalMode, string>> = {
  ask: "settings.permissionAsk",
  auto: "settings.permissionAutoReview",
  "deny-unprompted": "settings.permissionDenyUnprompted",
};

/** Which unified value a harness's neutral approval mode is (null when it is a claude-specific extra or unknown). */
export function unifiedApprovalOf(neutral: ApprovalMode | null): UnifiedApprovalMode | null {
  return neutral !== null && (UNIFIED_APPROVAL_MODES as readonly string[]).includes(neutral)
    ? (neutral as UnifiedApprovalMode)
    : null;
}

/** The compare result of one field, by its id. */
export function compareFieldOf(results: readonly ComparedField[], field: string): ComparedField | null {
  return results.find((entry) => entry.field === field) ?? null;
}

/** The unified block's per-harness values for one compared field. A harness
 * that does not support the field shows its own visible note (pi's 不做审批 /
 * 无沙箱; the trust field's no-project note). */
export function unifiedValues(
  field: "approval-mode" | "sandbox" | "project-trust",
  compare: ComparedField | null,
  notes: { pi: string; claude: string; unset?: string },
): UnifiedSettingHarnessValue[] {
  if (compare === null) return [];
  const values: UnifiedSettingHarnessValue[] = [];
  for (const harness of ["pi", "claude"] as const) {
    const value = compare.values[harness];
    if (!value.supported) {
      values.push({ harness, status: "unsupported", value: null, reason: notes[harness] });
    } else if (value.neutral === null && notes.unset !== undefined) {
      // polyforge#256 B6: supported but unset is its own state, never the
      // unsupported one. The display carries the localized 「未设置」 text (the
      // UnifiedSetting protocol keeps `ready`; the stored value is not
      // rewritten to false here — no silent write, no fake default).
      values.push({ harness, status: "ready", value: notes.unset });
    } else {
      values.push({ harness, status: "ready", value: value.neutral });
    }
  }
  return values;
}

/** The trust unify target: every supporting harness whose value differs, set to the first ready harness's value (the「当前值」shown). */
export function trustUnifyTargets(
  reads: PermissionReads,
): { trusted: boolean; harnesses: ("pi" | "claude")[] } | null {
  const entries = (["pi", "claude"] as const).flatMap((harness) => {
    const trusted = reads[harness]?.projectTrust?.trusted;
    return typeof trusted === "boolean" ? [{ harness, trusted } as const] : [];
  });
  if (entries.length < 2) return null;
  const current = entries[0].trusted;
  const differs = entries.filter((entry) => entry.trusted !== current).map((entry) => entry.harness);
  if (differs.length === 0) return null;
  return { trusted: current, harnesses: differs };
}

/** One rules list as the textarea's text: one rule per line. */
export function rulesToText(rules: readonly string[]): string {
  return rules.join("\n");
}

/** The textarea's text as one rules list: trimmed, empty lines dropped, duplicates kept once. */
export function rulesFromText(text: string): string[] {
  const seen = new Set<string>();
  const rules: string[] = [];
  for (const line of text.split("\n")) {
    const rule = line.trim();
    if (!rule || seen.has(rule)) continue;
    seen.add(rule);
    rules.push(rule);
  }
  return rules;
}

/** Whether a field's native source makes its control read-only (Q3 B: managed — and the command line — display read-only). */
export function sourceIsReadOnly(source: PermissionPayload["sources"][keyof PermissionPayload["sources"]]): boolean {
  return source?.scope === "managed";
}
