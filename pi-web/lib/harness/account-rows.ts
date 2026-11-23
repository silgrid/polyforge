// The account page's Cell-row model (polyforge#142, 85a2 AC1/AC7):
// client-safe, pure — one row per Cell × harness, built from the doctor
// cells facts (lib/polyforge-doctor's sanitized shape), the adapter
// capabilities and the per-source pi checks. Every field of the ticket's
// list has its place: 状态 (credential / check status), 来源含「共享
// default」(source + sourceKind), 配置根 (configRoot), 凭据是否跟随配置目录
// (credentialsFollow, with the keychain hint when it does not — AC7),
// 步骤能否使用及原因 (usable + reason). No value ever flows through here.
import type {
  AccountCellRow,
  AccountHarnessRow,
  DoctorCellFacts,
  DoctorHarnessFacts,
  HarnessCapabilityFacts,
  PiStepCheck,
} from "./account-rows-facts";

export type {
  AccountCellRow,
  AccountHarnessRow,
  DoctorCellFacts,
  DoctorHarnessFacts,
  HarnessCapabilityFacts,
  PiStepCheck,
} from "./account-rows-facts";

/** The claude env block's credential keys the doctor reports facts about. */
const CLAUDE_CREDENTIAL_KEYS = ["ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"] as const;

/** claude's credential kinds from the daemon's set/unset env facts (Q3):
 * a setup-token mint (CLAUDE_CODE_OAUTH_TOKEN), an API key, or the gateway
 * pair (BASE_URL + AUTH_TOKEN). */
export function claudeCredentialKinds(settingsEnv: Record<string, string> | undefined): { oauthToken: boolean; apiKey: boolean; gateway: boolean } {
  const isSet = (key: string): boolean => settingsEnv?.[key] === "set";
  return {
    oauthToken: isSet("CLAUDE_CODE_OAUTH_TOKEN"),
    apiKey: isSet("ANTHROPIC_API_KEY"),
    gateway: isSet("ANTHROPIC_AUTH_TOKEN") && isSet("ANTHROPIC_BASE_URL"),
  };
}

/** claude's credential fact from the env facts: set when any credential key
 * is set, unset otherwise, null when no facts arrived. */
function claudeCredential(facts: DoctorHarnessFacts | undefined): "set" | "unset" | null {
  if (!facts?.settingsEnv) return null;
  return CLAUDE_CREDENTIAL_KEYS.some((key) => facts.settingsEnv?.[key] === "set") ? "set" : "unset";
}

/** The pi check of a Cell's source dir, when one ran. */
function piCheckOf(cell: DoctorCellFacts, facts: DoctorHarnessFacts | undefined, checks: Map<string, PiStepCheck>): PiStepCheck | null {
  const source = facts?.source ?? cell.source;
  if (!source) return null;
  return checks.get(source) ?? null;
}

/**
 * One Cell's account row: every harness with a capability or a doctor fact,
 * in the account page's order (pi, Claude, then any later adapter). A Cell
 * whose probe failed answers honest unknowns and its probe error as the
 * reason — never a faked fact.
 */
export function buildAccountCellRow(
  cell: DoctorCellFacts,
  capabilities: Record<string, HarnessCapabilityFacts>,
  piChecks: Map<string, PiStepCheck>,
): AccountCellRow {
  const executionDisabled = typeof cell.executionDisabled === "string" && cell.executionDisabled !== "" ? cell.executionDisabled : null;
  const probeError = typeof cell.probeError === "string" && cell.probeError !== "" ? cell.probeError : null;
  const ids = new Set<string>([...Object.keys(capabilities), ...Object.keys(cell.harnesses ?? {})]);
  const order = ["pi", "claude", ...[...ids].filter((id) => id !== "pi" && id !== "claude").sort()];
  const harnesses: Record<string, AccountHarnessRow> = {};
  for (const id of order) {
    const cap = capabilities[id];
    const facts = cell.harnesses?.[id];
    const credentialsFollow = cap?.credentialsFollowConfigRoot ?? true;
    const base = {
      id,
      credentialsFollow,
      keychainHint: !credentialsFollow,
      source: facts?.source ?? cell.source ?? null,
      sourceKind: facts?.sourceKind ?? cell.sourceKind ?? null,
    };
    if (probeError) {
      harnesses[id] = {
        ...base,
        configRoot: null, credential: null, status: "unknown", authType: null, checkReason: null,
        credentialKinds: null, credentialsFile: null, usable: false, reason: probeError,
      };
      continue;
    }
    if (id === "claude") {
      const credential = claudeCredential(facts);
      const kinds = facts?.settingsEnv ? claudeCredentialKinds(facts.settingsEnv) : null;
      const credentialsFile = facts?.credentialsFile === "present" || facts?.credentialsFile === "absent" ? facts.credentialsFile : null;
      const hasCredential = credential === "set" || credentialsFile === "present";
      const usable = executionDisabled === null && hasCredential;
      harnesses[id] = {
        ...base,
        configRoot: facts?.configRoot ?? null,
        credential,
        status: hasCredential ? "ready" : "not_ready",
        authType: null,
        checkReason: null,
        credentialKinds: kinds,
        credentialsFile,
        usable,
        reason: executionDisabled !== null
          ? executionDisabled
          : hasCredential
            ? null
            : "no credential in the claude-config env block",
      };
      continue;
    }
    const credential = facts?.credential === "set" || facts?.credential === "unset" ? facts.credential : null;
    const check = piCheckOf(cell, facts, piChecks);
    const ready = check?.status === "ready" || (check === null && credential === "set");
    const checkReason = check?.reason ?? null;
    harnesses[id] = {
      ...base,
      configRoot: facts?.configRoot ?? null,
      credential,
      status: check?.status ?? "unknown",
      authType: check?.authType ?? null,
      checkReason,
      credentialKinds: null,
      credentialsFile: null,
      usable: executionDisabled === null && ready,
      reason: executionDisabled !== null
        ? executionDisabled
        : ready
          ? null
          : checkReason ?? "no credential in the step agent dir",
    };
  }
  return { profile: cell.profile, executionDisabled, probeError, harnessOrder: order, harnesses };
}
