// The permission page's client-safe view model (polyforge#150 85a4): the
// shapes GET /api/harness/compare and the per-harness permission reads answer,
// the mapping of the unified approval values onto claude's native modes
// (Q1 A: 询问→default / 自动审查→auto / 不询问即拒绝→dontAsk), the unified
// blocks' per-harness values, the trust unify target and the rules text
// format. Pure: no fetch, no I/O — the page component owns the reads and
// writes, the tests pin these mappings and derivations.
//
// SKELETON (the pre-test ordinary commit): the signatures exist so the
// suite's red is its assertions, never a missing symbol; the derivations land
// with this unit's implementation commit.

/** One harness's permission payload as the page received it. */
export interface PermissionReads {
  pi: unknown | null;
  claude: unknown | null;
}

/** One compared field as the compare route answers it (the client's own
 * structural type: a client module never imports the server compare module). */
export interface ComparedField {
  field: string;
  status: "consistent" | "inconsistent" | "unsupported";
  values: Record<string, { supported: boolean; neutral: string | null; raw: string | null }>;
}

/** One harness's read of the unified value (UnifiedSetting's shape). */
export interface UnifiedSettingHarnessValue {
  harness: string;
  status: "ready" | "unsupported" | "not-installed" | "unknown" | "error";
  value: string | null;
  reason?: string | null;
}

/** The unified approval-mode values the page offers (the ticket's three). */
export const UNIFIED_APPROVAL_MODES = ["ask", "auto", "deny-unprompted"] as const;
export type UnifiedApprovalMode = (typeof UNIFIED_APPROVAL_MODES)[number];

/** Q1 A: the unified value each harness's native mode maps from — claude only today. */
export const UNIFIED_TO_CLAUDE_MODE: Readonly<Record<UnifiedApprovalMode, string>> = {
  ask: "",
  auto: "",
  "deny-unprompted": "",
};

/** The three values' label keys (one fact, the pages and the tests read it). */
export const UNIFIED_APPROVAL_LABEL_KEYS: Readonly<Record<UnifiedApprovalMode, string>> = {
  ask: "settings.permissionAsk",
  auto: "settings.permissionAutoReview",
  "deny-unprompted": "settings.permissionDenyUnprompted",
};

/** Which unified value a harness's neutral approval mode is (null when it is a claude-specific extra or unknown). */
export function unifiedApprovalOf(_neutral: unknown): UnifiedApprovalMode | null {
  return null;
}

/** The compare result of one field, by its id. */
export function compareFieldOf(_results: readonly ComparedField[], _field: string): ComparedField | null {
  return null;
}

/** The unified block's per-harness values for one compared field. A harness
 * that does not support the field shows its own visible note (pi's 不做审批 /
 * 无沙箱; the trust field's no-project note). */
export function unifiedValues(
  _field: "approval-mode" | "sandbox" | "project-trust",
  _compare: ComparedField | null,
  _notes: { pi: string; claude: string },
): UnifiedSettingHarnessValue[] {
  return [];
}

/** The trust unify target: every supporting harness whose value differs, set to the first ready harness's value (the「当前值」shown). */
export function trustUnifyTargets(_reads: PermissionReads): { trusted: boolean; harnesses: ("pi" | "claude")[] } | null {
  return null;
}

/** One rules list as the textarea's text: one rule per line. */
export function rulesToText(_rules: readonly string[]): string {
  return "";
}

/** The textarea's text as one rules list: trimmed, empty lines dropped, duplicates kept once. */
export function rulesFromText(_text: string): string[] {
  return [];
}

/** Whether a field's native source makes its control read-only (Q3 B: managed — and the command line — display read-only). */
export function sourceIsReadOnly(_source: unknown): boolean {
  return false;
}
