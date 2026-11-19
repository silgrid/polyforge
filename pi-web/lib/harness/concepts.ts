// Harness adapter framework (polyforge#85, unit 85a1): the neutral concept
// vocabulary every harness adapter speaks — concept ids, scopes, the neutral
// reasoning-effort and approval-mode enums, the payload types a per-concept
// read returns, and the capability declaration each adapter makes. Client-safe:
// never import a server-only module here; 85a1b's UI reads these types.
//
// The native config files are the single source of truth; the neutral enums are
// the unified model of parent ticket #85. A harness's own spelling maps onto a
// neutral value by neutralReasoningEffort()/neutralApprovalMode(); an unknown
// spelling maps to null and callers keep the raw value for display.

/** The harnesses pi-web has an adapter for. New harness = new adapter. */
export type HarnessId = "pi" | "claude";

/** The unified concepts of the first version (#85 c2 subset). */
export type ConceptId =
  | "auth"
  | "config-root"
  | "model"
  | "permission"
  | "skills"
  | "mcp"
  | "env"
  | "effective-config";

/**
 * The scope vocabulary of #85's unified model. `pi-web` is the pseudo-scope of
 * the settings Pi Web stores itself (pi-web-claude-settings.json,
 * claude-permissions.json) until they migrate back to native files (#147, #150).
 */
export type ScopeId = "managed" | "user" | "project" | "local" | "session" | "pi-web";

/** The neutral reasoning-effort levels, ordered. */
export const REASONING_EFFORT_LEVELS = [
  "off",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
] as const;
export type ReasoningEffort = (typeof REASONING_EFFORT_LEVELS)[number];

/** The neutral approval modes of the unified model. */
export const APPROVAL_MODES = [
  "ask",
  "accept-edits",
  "plan",
  "auto",
  "deny-unprompted",
  "full",
] as const;
export type ApprovalMode = (typeof APPROVAL_MODES)[number];

/**
 * A harness spelling of a reasoning effort onto the neutral enum; pi's
 * `defaultThinkingLevel` and claude's `effortLevel` both use these spellings
 * verbatim. An unknown spelling maps to null (callers show the raw value).
 */
export function neutralReasoningEffort(raw: unknown): ReasoningEffort | null {
  // stub (85a1 u1): zero-value body, the red run's target
  return null;
}

/** A harness spelling of an approval mode onto the neutral enum; unknown → null. */
export function neutralApprovalMode(raw: unknown): ApprovalMode | null {
  // stub (85a1 u1): zero-value body, the red run's target
  return null;
}

/** A credential file's existence, never its contents. */
export interface CredentialRef {
  exists: boolean;
  path: string;
}

/** The auth concept (85a1: only what needs no command; status is unknown, Q4 A). */
export interface AuthPayload {
  status: "unknown";
  credentialStore: CredentialRef | null;
  /** Automation credential variable names and whether each is set — names, never values. */
  automationEnv: Record<string, boolean>;
  /** Whether the credentials live inside the config root directory. */
  credentialsFollow: boolean;
}

/** The config-root concept. */
export interface ConfigRootPayload {
  path: string;
  /** The environment variable that overrides the root, when one exists. */
  env: string | null;
  credentialsFollow: boolean;
}

/** A provider as the model concept lists it: never a key, only whether one is stored. */
export interface ProviderRef {
  name: string;
  hasApiKey: boolean;
}

/** The model concept. */
export interface ModelPayload {
  defaultProvider: string | null;
  defaultModel: string | null;
  /** The default reasoning effort, neutral plus the harness's raw spelling. */
  reasoningEffort: { neutral: ReasoningEffort | null; raw: string | null } | null;
  providers: ProviderRef[];
  /** pi-web-stored custom model names (claude only, until #147/#150 migrate them). */
  customModels: string[];
}

/** The permission concept. */
export interface PermissionPayload {
  approvalMode: {
    supported: boolean;
    neutral: ApprovalMode | null;
    raw: string | null;
  };
  projectTrust: { trusted: boolean | null; note?: string } | null;
}

/** One skill as the skills concept lists it: name/path/source only (#146: state
 * comes from the native config, never from frontmatter). */
export interface SkillRef {
  name: string;
  path: string;
  source: ScopeId;
}

/** The skills concept. */
export interface SkillsPayload {
  entries: SkillRef[];
}

/** One MCP server as the mcp concept lists it — masked (lib/mcp-secrets.ts), url and args only as maskUrl/maskArgs give them, header/env names without values. */
export interface MaskedMcpServer {
  name: string;
  scope: ScopeId;
  url?: { masked: string; hadSecret: boolean };
  command?: string;
  args?: { args: string[]; masked: boolean };
  headerNames?: string[];
  envNames?: string[];
  disabled?: boolean;
}

/** The mcp concept. */
export interface McpPayload {
  servers: MaskedMcpServer[];
}

/** The env concept: read-only; key names only, never values. */
export interface EnvPayload {
  /** Which environment the keys belong to (the harness's own settings env block or its automation variables). */
  target: string;
  setKeys: string[];
}

/** One effective value with its provenance (#85 d-5: self-computed, always `inferred`). */
export interface EffectiveValue {
  value: unknown;
  sourceScope: ScopeId;
  sourcePath: string;
  overriddenBy?: string;
  inferred: true;
}

/** The effective-config concept (#85 c2): per-field effective values and sources. */
export interface EffectiveConfigPayload {
  fields: Record<string, EffectiveValue>;
}

/** What any adapter's per-concept read returns (the payload shapes above). */
export type ConceptPayload =
  | AuthPayload
  | ConfigRootPayload
  | ModelPayload
  | PermissionPayload
  | SkillsPayload
  | McpPayload
  | EnvPayload
  | EffectiveConfigPayload;

/** The capability declaration each adapter makes (AC1). */
export interface HarnessCapabilities {
  id: HarnessId;
  /** The scopes this harness's config layers use. */
  scopes: ScopeId[];
  /** The concepts this adapter reads. */
  concepts: ConceptId[];
  /** The concepts this adapter writes; empty in 85a1 (the writes land in 85a2–85d). */
  writableConcepts: readonly ConceptId[];
  /** Read-only concepts a caller must never be offered a write for. */
  readOnlyConcepts: readonly ConceptId[];
  /** The environment variable that overrides the config root, when one exists. */
  configRootEnv: string | null;
  /** Whether the credentials live inside the config root directory. */
  credentialsFollowConfigRoot: boolean;
  /** Native diagnostics: declared per Q5 A, executed only from 85a1b (#149). */
  nativeDiagnostics: { declared: true; implemented: false };
}
