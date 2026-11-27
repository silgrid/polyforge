// Client-memory only. Never persist, log, or render the secret fields in this record.
export type CredentialKind = "api-key" | "gateway" | "oauth-token";
export interface CredentialAttempt {
  kind: CredentialKind;
  targets: string[];
  value: string;
  baseUrl: string;
  useInteractive: boolean;
}
export interface HeldCredentialDraft extends Omit<CredentialAttempt, "targets"> {
  target: string;
  state: "pending" | "failed" | "unconfirmed";
}
export type HeldCredentialDrafts = Record<string, HeldCredentialDraft>;
export const credentialDraftKey = (target: string, kind: CredentialKind) => JSON.stringify([target, kind]);
const configurationOf = (attempt: CredentialAttempt) => ({ kind: attempt.kind, value: attempt.value, baseUrl: attempt.baseUrl, useInteractive: attempt.useInteractive });

/** Only targets attempted in this request can be settled. Other failed drafts remain intact. */
export function settleCredentialDrafts(held: HeldCredentialDrafts, attempt: CredentialAttempt, results: readonly { target: string; status: string }[] = []): HeldCredentialDrafts {
  const next = { ...held };
  for (const target of attempt.targets) {
    const key = credentialDraftKey(target, attempt.kind);
    const result = results.find((entry) => entry.target === target);
    if (result?.status === "written") delete next[key];
    else {
      next[key] = { ...configurationOf(attempt), target, state: result ? "failed" : "unconfirmed" };
    }
  }
  return next;
}

/** Restoring one failed entry must not erase a different target currently being edited. */
export function retainCredentialDrafts(held: HeldCredentialDrafts, attempt: CredentialAttempt, restoringKey: string): HeldCredentialDrafts {
  if (!attempt.value && !attempt.baseUrl && !attempt.useInteractive) return held;
  const next = { ...held };
  const configuration = configurationOf(attempt);
  for (const target of attempt.targets) {
    const key = credentialDraftKey(target, attempt.kind);
    if (key !== restoringKey && !next[key]) next[key] = { ...configuration, target, state: "pending" };
  }
  return next;
}

export function credentialRestoreConflicts(held: HeldCredentialDrafts, attempt: CredentialAttempt, restoringKey: string): boolean {
  if (!attempt.value && !attempt.baseUrl && !attempt.useInteractive) return false;
  return attempt.targets.some((target) => {
    const key = credentialDraftKey(target, attempt.kind);
    return key !== restoringKey && held[key] !== undefined && !matchesCredentialDraft(held[key], attempt);
  });
}

export function matchesCredentialDraft(draft: HeldCredentialDraft, attempt: CredentialAttempt): boolean {
  return draft.kind === attempt.kind && attempt.targets.includes(draft.target)
    && draft.value === attempt.value && draft.baseUrl === attempt.baseUrl && draft.useInteractive === attempt.useInteractive;
}
