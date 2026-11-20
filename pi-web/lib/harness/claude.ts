// The claude adapter (polyforge#85 85a1): reads claude's native config —
// the settings layers (managed > .claude/settings.local.json >
// .claude/settings.json > $CLAUDE_CONFIG_DIR/settings.json), the state file
// .claude.json, .mcp.json and the skills directories — through the same
// redaction allow-list as pi. The version probe reuses probeClaude()
// (lib/claude-probe.ts), the one probe implementation; CLAUDE_RECORDED_VERSIONS
// keeps its fixture semantics and the write gate uses its own range (any 2.x,
// 85a3's Q4 ruling).
import { existsSync, mkdirSync, readFileSync, readdirSync } from "node:fs";
import { basename, dirname, join } from "node:path";
import { writePrivateFileAtomicSync } from "../atomic-file";
import { serializeByKey } from "../key-serializer";
import { claudeConfigDir } from "../claude-config-dir";
import { probeClaude } from "../claude-probe";
import { findClaudeBinary } from "../claude-session";
import { CLAUDE_KNOWN_RANGE, versionAtLeast, versionInKnownRange, writeGate, gatedWrite, type HarnessAdapter, type HarnessReadContext, type VersionProbeResult, type WriteChange, type WriteResult } from "./adapter";
import { applyClaudeModelChange, applyClaudePermissionChange, claudeModelChangeKeys, CLAUDE_MODEL_KEY_MIN_VERSIONS, parseClaudeModelChange, parseClaudePermissionChange } from "./claude-native-write";
import { claudeSettingsLayers, claudeStateFilePath, modelPickerOptions, type ClaudeSettingsLayer } from "./claude-layers";
import {
  neutralApprovalMode,
  neutralReasoningEffort,
  type PermissionFieldSource,
  type AuthPayload,
  type HarnessLoginCommand,
  type ConceptId,
  type ConceptPayload,
  type ConfigRootPayload,
  type EnvPayload,
  type MaskedMcpServer,
  type McpPayload,
  type ModelPayload,
  type PermissionPayload,
  type PermissionRules,
  type PermissionSandbox,
  type ScopeId,
  type SkillRef,
  type SkillsPayload,
} from "./concepts";
import { claudeDiagnose } from "./diagnostics";
import { claudeEffectiveConfig } from "./effective";
import {
  automationEnvStatus,
  credentialRef,
  maskedMcpServer,
  readJsonRecord,
} from "./redact";

/** The credential environment variables claude answers, names only. */
export const CLAUDE_AUTOMATION_ENV_KEYS = [
  "ANTHROPIC_API_KEY",
  "ANTHROPIC_AUTH_TOKEN",
  "CLAUDE_CODE_OAUTH_TOKEN",
] as const;

type SettingsLayer = ClaudeSettingsLayer;

/** The first layer whose record holds `key` (the effective value), highest priority first. */
function effectiveLayer(
  layers: SettingsLayer[],
  key: string,
): { layer: SettingsLayer; value: unknown } | null {
  for (const layer of layers) {
    if (layer.record && key in layer.record)
      return { layer, value: layer.record[key] };
  }
  return null;
}

/** The `mcpServers` records of every source, in the order they are listed. */
function mcpSources(
  ctx: HarnessReadContext | undefined,
  cwd: string | undefined,
  layers: SettingsLayer[],
): { scope: ScopeId; servers: Record<string, unknown> }[] {
  const sources: { scope: ScopeId; servers: Record<string, unknown> }[] = [];
  const state = readJsonRecord(claudeStateFilePath(ctx));
  if (state && isRecord(state.mcpServers))
    sources.push({ scope: "user", servers: state.mcpServers });
  const projectState =
    cwd && state && isRecord(state.projects)
      ? (state.projects as Record<string, unknown>)[cwd]
      : undefined;
  if (
    projectState &&
    isRecord(projectState) &&
    isRecord((projectState as Record<string, unknown>).mcpServers)
  ) {
    sources.push({
      scope: "project",
      servers: (projectState as Record<string, unknown>).mcpServers as Record<
        string,
        unknown
      >,
    });
  }
  for (const layer of [...layers].reverse()) {
    if (layer.record && isRecord(layer.record.mcpServers))
      sources.push({ scope: layer.scope, servers: layer.record.mcpServers });
  }
  if (cwd) {
    const dotMcp = readJsonRecord(join(cwd, ".mcp.json"));
    if (dotMcp && isRecord(dotMcp.mcpServers))
      sources.push({ scope: "project", servers: dotMcp.mcpServers });
  }
  return sources;
}

/** Whether `value` is a plain record. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** The key names of an entry's headers/env blocks, values never read. */
function keyNames(value: unknown): string[] {
  return isRecord(value) ? Object.keys(value).sort() : [];
}

/** One skills directory's SKILL.md files as entries. */
function skillDirEntries(dir: string, source: ScopeId): SkillRef[] {
  let dirents;
  try {
    dirents = readdirSync(dir, { withFileTypes: true });
  } catch {
    return [];
  }
  return dirents
    .filter((entry) => entry.isDirectory())
    .map((entry) => ({
      name: entry.name,
      path: join(dir, entry.name, "SKILL.md"),
      source,
    }))
    .filter((entry) => existsSync(entry.path));
}

/**
 * claude's credentials follow its config root on Linux and Windows (the
 * files live under the config dir); on macOS the Keychain holds them per
 * config-dir entry, so they do not follow (polyforge#142, 85a2 AC7 — the
 * account page's Cell-row hint). The container images are Linux; the
 * declaration is platform-derived so a macOS host reports the truth.
 */
export function claudeCredentialsFollowConfigRoot(
  platform: NodeJS.Platform = process.platform,
): boolean {
  return platform !== "darwin";
}

/**
 * The terminal login commands claude declares (85a2 AC3, the 2026-11-19 Q3
 * revision): the interactive session's OAuth login and the long-lived
 * setup-token mint — both run on the operator's interactive environment
 * (their credentials come from the operator's own claude login), so neither
 * is Cell-scoped. The account page's 「打开终端」 buttons run exactly these.
 */
export const CLAUDE_LOGIN_COMMANDS: readonly HarnessLoginCommand[] = [
  { id: "auth-login", argv: ["claude", "auth", "login"], cell: false },
  { id: "setup-token", argv: ["claude", "setup-token"], cell: false },
];

/** The credential keys each auth write kind lands in the env block (Q3). */
const AUTH_WRITE_KEYS: Record<
  string,
  { valueKey: string; baseUrlKey?: string }
> = {
  "oauth-token": { valueKey: "CLAUDE_CODE_OAUTH_TOKEN" },
  "api-key": { valueKey: "ANTHROPIC_API_KEY" },
  gateway: {
    valueKey: "ANTHROPIC_AUTH_TOKEN",
    baseUrlKey: "ANTHROPIC_BASE_URL",
  },
};

/** One auth write, serialized per settings file: the read-modify-write of
 * the native env block never interleaves with another. */
const AUTH_WRITE_CHAIN = Symbol.for("pi-web.harness.claudeAuthWrites");

export function createClaudeAdapter(): HarnessAdapter {
  // ctx.env is authoritative for the config root when the caller gives one,
  // so a read and the state-file resolution cannot disagree.
  const configDirOf = (ctx?: HarnessReadContext): string => ctx?.claudeConfigDir ?? claudeConfigDir(ctx?.env ?? process.env);
  const envOf = (ctx?: HarnessReadContext): NodeJS.ProcessEnv => ctx?.env ?? process.env;

  async function probeVersion(
    ctx?: HarnessReadContext,
  ): Promise<VersionProbeResult> {
    const configDir = configDirOf(ctx);
    const binary = ctx?.claudeBinary ?? findClaudeBinary();
    const source = "claude initialize";
    if (!binary)
      return {
        version: null,
        known: false,
        reason: "claude was not found on PATH",
        source,
      };
    const probed = await probeClaude(binary);
    if ("error" in probed)
      return { version: null, known: false, reason: probed.error, source };
    const version = probed.version;
    if (version === null)
      return {
        version: null,
        known: false,
        reason: "claude did not report a version",
        source,
      };
    if (!versionInKnownRange(version, CLAUDE_KNOWN_RANGE)) {
      return {
        version,
        known: false,
        knownRange: CLAUDE_KNOWN_RANGE.label,
        source,
        reason: `version ${version} is outside the known range ${CLAUDE_KNOWN_RANGE.label}`,
      };
    }
    // The structure probe of the write gate (Q2 C): a settings layer or the
    // state file that exists but is not a JSON object refuses the write.
    for (const layer of claudeSettingsLayers(
      configDir,
      ctx?.cwd,
      ctx?.managedSettingsPath,
    )) {
      if (existsSync(layer.path) && layer.record === null) {
        return {
          version,
          known: false,
          knownRange: CLAUDE_KNOWN_RANGE.label,
          source,
          reason: `${basename(layer.path)} has an unrecognized top-level structure (expected a JSON object)`,
        };
      }
    }
    const statePath = claudeStateFilePath(ctx);
    if (existsSync(statePath) && readJsonRecord(statePath) === null) {
      return {
        version,
        known: false,
        knownRange: CLAUDE_KNOWN_RANGE.label,
        source,
        reason: `${basename(statePath)} has an unrecognized top-level structure (expected a JSON object)`,
      };
    }
    return {
      version,
      known: true,
      knownRange: CLAUDE_KNOWN_RANGE.label,
      source,
    };
  }

  return {
    id: "claude",
    capabilities: {
      id: "claude",
      scopes: ["managed", "user", "project", "local"],
      // #85 85c (Q1 A): claude is written only through its CLI, has no
      // enable/disable of an entry (unapplying removes it) and no tool
      // filter; it holds the local scope and takes sse and ws entries.
      mcp: {
        scopes: ["user", "project", "local"],
        transports: ["stdio", "http", "sse", "ws"],
        cliAdd: true,
        enableToggle: false,
        toolFilter: false,
      },
      concepts: ["auth", "config-root", "model", "permission", "skills", "mcp", "env", "effective-config"],
      writableConcepts: ["auth", "permission", "model"],
      readOnlyConcepts: ["env", "effective-config"],
      configRootEnv: "CLAUDE_CONFIG_DIR",
      credentialsFollowConfigRoot: claudeCredentialsFollowConfigRoot(),
      loginCommands: CLAUDE_LOGIN_COMMANDS,
      // 85a1b (AC6): the fixed safe `claude doctor` runs, with its deadline
      // and the credential filter (lib/harness/diagnostics.ts).
      nativeDiagnostics: { declared: true, implemented: true },
    },
    probeVersion,
    async read(concept: ConceptId, ctx): Promise<ConceptPayload | null> {
      const configDir = configDirOf(ctx);
      const env = envOf(ctx);
      const cwd = ctx?.cwd;
      const layers = claudeSettingsLayers(
        configDir,
        cwd,
        ctx?.managedSettingsPath,
      );
      switch (concept) {
        case "auth": {
          const payload: AuthPayload = {
            status: "unknown",
            credentialStore: credentialRef(
              join(configDir, ".credentials.json"),
            ),
            automationEnv: automationEnvStatus(env, CLAUDE_AUTOMATION_ENV_KEYS),
            credentialsFollow: claudeCredentialsFollowConfigRoot(),
          };
          return payload;
        }
        case "config-root": {
          const payload: ConfigRootPayload = {
            path: configDir,
            env: "CLAUDE_CONFIG_DIR",
            credentialsFollow: claudeCredentialsFollowConfigRoot(),
          };
          return payload;
        }
        case "model": {
          const model = effectiveLayer(layers, "model");
          const effort = effectiveLayer(layers, "effortLevel");
          const rawEffort =
            effort && typeof effort.value === "string" ? effort.value : null;
          // The per-model layer (85a3): modelSettings, the first settings layer
          // naming it, keyed by model name (an entry's effort alone is read).
          const perModel: ModelPayload["perModelReasoningEffort"] = {};
          const modelSettings = effectiveLayer(layers, "modelSettings");
          if (modelSettings && isRecord(modelSettings.value)) {
            for (const [model, entry] of Object.entries(modelSettings.value as Record<string, unknown>)) {
              if (isRecord(entry) && typeof entry.effort === "string") {
                perModel[model] = { neutral: neutralReasoningEffort(entry.effort), raw: entry.effort };
              }
            }
          }
          const payload: ModelPayload = {
            defaultProvider: null,
            defaultModel:
              model && typeof model.value === "string" ? model.value : null,
            reasoningEffort:
              rawEffort === null
                ? null
                : {
                    neutral: neutralReasoningEffort(rawEffort),
                    raw: rawEffort,
                  },
            perModelReasoningEffort: perModel,
            providers: [],
            // 85a4: the custom names are claude's own modelPicker.options
            // (claude-layers' one merge of them).
            customModels: modelPickerOptions(layers),
          };
          return payload;
        }
        case "permission": {
          // The first layer whose permissions record names a defaultMode, so a
          // higher layer with permissions but no defaultMode does not shadow it.
          let raw: string | null = null;
          let defaultModeSource: PermissionFieldSource | null = null;
          for (const layer of layers) {
            const permissions = layer.record?.permissions;
            if (
              isRecord(permissions) &&
              typeof permissions.defaultMode === "string"
            ) {
              raw = permissions.defaultMode;
              defaultModeSource = { scope: layer.scope, path: layer.path };
              break;
            }
          }
          // denyInsteadOfPrompting: claude's own boolean key (v2.1.319+), the
          // first layer that names it.
          let denyInsteadOfPrompting: boolean | null = null;
          let denySource: PermissionFieldSource | null = null;
          for (const layer of layers) {
            const permissions = layer.record?.permissions;
            if (isRecord(permissions) && typeof permissions.denyInsteadOfPrompting === "boolean") {
              denyInsteadOfPrompting = permissions.denyInsteadOfPrompting;
              denySource = { scope: layer.scope, path: layer.path };
              break;
            }
          }
          // The sandbox object: enabled / autoAllowBashIfSandboxed as booleans,
          // claude's other sandbox keys by name only (they are claude's detail).
          const sandboxLayer = effectiveLayer(layers, "sandbox");
          const sandboxRecord = sandboxLayer && isRecord(sandboxLayer.value) ? sandboxLayer.value : null;
          const booleanOf = (key: string): boolean | null =>
            sandboxRecord && typeof sandboxRecord[key] === "boolean" ? sandboxRecord[key] as boolean : null;
          const sandbox: PermissionSandbox = {
            supported: true,
            enabled: booleanOf("enabled"),
            autoAllowBashIfSandboxed: booleanOf("autoAllowBashIfSandboxed"),
            otherKeys: sandboxRecord
              ? Object.keys(sandboxRecord).filter((key) => key !== "enabled" && key !== "autoAllowBashIfSandboxed").sort()
              : [],
          };
          // The rules: claude merges its permission lists across layers, so the
          // read merges them the same way (deduped, first occurrence wins the
          // order); the source is the highest layer that names any list.
          const rules: PermissionRules = { allow: [], ask: [], deny: [] };
          let rulesSource: PermissionFieldSource | null = null;
          for (const layer of layers) {
            const permissions = layer.record?.permissions;
            if (!isRecord(permissions)) continue;
            if (rulesSource === null && (["allow", "ask", "deny"] as const).some((list) => Array.isArray(permissions[list]))) {
              rulesSource = { scope: layer.scope, path: layer.path };
            }
            for (const list of ["allow", "ask", "deny"] as const) {
              const entries = permissions[list];
              if (!Array.isArray(entries)) continue;
              for (const entry of entries) {
                if (typeof entry === "string" && !rules[list].includes(entry)) rules[list].push(entry);
              }
            }
          }
          let projectTrust: PermissionPayload["projectTrust"] = null;
          if (cwd) {
            const state = readJsonRecord(claudeStateFilePath(ctx));
            const entry =
              state && isRecord(state.projects)
                ? (state.projects as Record<string, unknown>)[cwd]
                : undefined;
            const accepted =
              isRecord(entry) && entry.hasTrustDialogAccepted === true;
            projectTrust = { trusted: accepted };
          }
          const payload: PermissionPayload = {
            approvalMode: {
              supported: true,
              neutral: raw === null ? null : neutralApprovalMode(raw),
              raw,
            },
            projectTrust,
            sandbox,
            rules,
            denyInsteadOfPrompting,
            sources: {
              defaultMode: defaultModeSource,
              sandbox: sandboxLayer ? { scope: sandboxLayer.layer.scope, path: sandboxLayer.layer.path } : null,
              rules: rulesSource,
              denyInsteadOfPrompting: denySource,
            },
          };
          return payload;
        }
        case "skills": {
          const entries = [
            ...skillDirEntries(join(configDir, "skills"), "user"),
            ...(cwd
              ? skillDirEntries(join(cwd, ".claude", "skills"), "project")
              : []),
          ];
          const overrides: Record<string, string> = {};
          for (const layer of [...layers].reverse()) {
            if (!layer.record || !isRecord(layer.record.skillOverrides))
              continue;
            for (const [name, state] of Object.entries(
              layer.record.skillOverrides as Record<string, unknown>,
            )) {
              if (typeof state === "string") overrides[name] = state;
            }
          }
          const payload: SkillsPayload =
            Object.keys(overrides).length > 0
              ? { entries, overrides }
              : { entries };
          return payload;
        }
        case "mcp": {
          const servers: MaskedMcpServer[] = [];
          for (const { scope, servers: entries } of mcpSources(
            ctx,
            cwd,
            layers,
          )) {
            for (const [name, entry] of Object.entries(entries)) {
              if (!isRecord(entry)) continue;
              servers.push(
                maskedMcpServer(scope, {
                  name,
                  url: typeof entry.url === "string" ? entry.url : undefined,
                  command:
                    typeof entry.command === "string"
                      ? entry.command
                      : undefined,
                  args:
                    Array.isArray(entry.args) &&
                    entry.args.every((arg) => typeof arg === "string")
                      ? (entry.args as string[])
                      : undefined,
                  headerNames: keyNames(entry.headers),
                  envNames: keyNames(entry.env),
                }),
              );
            }
          }
          const payload: McpPayload = { servers };
          return payload;
        }
        case "env": {
          const keys = new Set<string>();
          for (const layer of layers) {
            if (layer.record && isRecord(layer.record.env)) {
              for (const key of Object.keys(
                layer.record.env as Record<string, unknown>,
              ))
                keys.add(key);
            }
          }
          const payload: EnvPayload = {
            target: "claude",
            setKeys: [...keys].sort(),
          };
          return payload;
        }
        case "effective-config":
          return claudeEffectiveConfig(ctx);
        default:
          return null;
      }
    },
    async write(concept: ConceptId, change: WriteChange, ctx): Promise<WriteResult> {
      // The shared gate first (adapter.ts): an undetected or unrecognized
      // version never reaches a write. auth has 85a2's write (the credential
      // into the native env block), the permission concept 85a4's and the
      // model 概念只写 model / effortLevel，按模型 modelSettings 待格式
      // 核实后在 85a3-2 实现；其他概念沿用 gatedWrite 的 not-implemented。
      if (concept !== "permission" && concept !== "auth" && concept !== "model") return gatedWrite(this, concept, change, ctx);
      const probe = await probeVersion(ctx);
      const gate = writeGate(probe);
      if (!gate.allowed) return { status: "refused", reason: gate.reason };
      if (concept === "model") {
        const parsed = parseClaudeModelChange(change);
        if ("reason" in parsed) return { status: "refused", reason: parsed.reason };
        // The registered key minimums (Q4): the gate knows the major; a
        // version below a key's floor does not write that key.
        for (const key of claudeModelChangeKeys(parsed.change)) {
          const floor = CLAUDE_MODEL_KEY_MIN_VERSIONS[key];
          if (probe.version !== null && !versionAtLeast(probe.version, floor)) {
            return { status: "refused", reason: `claude ${probe.version} is below the registered minimum ${floor} for ${key}` };
          }
        }
        const applied = await applyClaudeModelChange(parsed.change, configDirOf(ctx));
        if (!applied.ok) return { status: "refused", reason: applied.reason };
        return { status: "written", summary: `${applied.summary} (claude ${probe.version}, user-level settings.json)` };
      }
      if (concept === "permission") {
        const parsed = parseClaudePermissionChange(change, ctx?.cwd);
        if ("reason" in parsed) return { status: "refused", reason: parsed.reason };
        const applied = await applyClaudePermissionChange(parsed.change, configDirOf(ctx), claudeStateFilePath(ctx));
        if (!applied.ok) return { status: "refused", reason: applied.reason };
        return { status: "written", summary: `${applied.summary} (claude ${probe.version}, user-level settings.json)` };
      }
      return writeClaudeAuth(change, configDirOf(ctx));
    },
    diagnose: (ctx) => claudeDiagnose(ctx),
  };
}

/**
 * The auth write (85a2 AC6): the credential lands in the native env block
 * of the target's settings.json — the interactive session's
 * $CLAUDE_CONFIG_DIR/settings.json, or a Cell's claude-config/settings.json
 * (the caller resolves the dir; the client never sends a path). The file's
 * other keys are kept, an existing file whose top level is not a recognized
 * JSON object is refused (never overwritten), the write is atomic and
 * 0600, and the value never appears in the result.
 */
async function writeClaudeAuth(
  change: WriteChange,
  configDir: string,
): Promise<WriteResult> {
  const kind = typeof change.kind === "string" ? change.kind : "";
  const shape = AUTH_WRITE_KEYS[kind];
  if (!shape)
    return {
      status: "refused",
      reason: `unknown credential kind ${JSON.stringify(kind)}`,
    };
  const value = change.value;
  if (typeof value !== "string" || value === "")
    return { status: "refused", reason: "the credential value is missing" };
  const baseUrl = shape.baseUrlKey ? change.baseUrl : undefined;
  if (shape.baseUrlKey && (typeof baseUrl !== "string" || baseUrl === "")) {
    return {
      status: "refused",
      reason: "a gateway credential needs its base URL",
    };
  }
  const path = join(configDir, "settings.json");
  let summary: string;
  try {
    summary = await serializeByKey(
      AUTH_WRITE_CHAIN,
      path,
      async (): Promise<string> => {
        let current: Record<string, unknown> = {};
        let text: string | undefined;
        try {
          text = readFileSync(path, "utf8");
        } catch {
          // absent: a fresh file (claude itself writes this shape)
        }
        if (text !== undefined) {
          let parsed: unknown;
          try {
            parsed = JSON.parse(text);
          } catch {
            throw new Error(
              `${basename(path)} has an unrecognized format (not JSON) — refusing to overwrite it`,
            );
          }
          if (
            typeof parsed !== "object" ||
            parsed === null ||
            Array.isArray(parsed)
          ) {
            throw new Error(
              `${basename(path)} has an unrecognized format (expected a JSON object) — refusing to overwrite it`,
            );
          }
          current = parsed as Record<string, unknown>;
        }
        const env: Record<string, unknown> =
          typeof current.env === "object" &&
          current.env !== null &&
          !Array.isArray(current.env)
            ? { ...(current.env as Record<string, unknown>) }
            : {};
        env[shape.valueKey] = value;
        if (shape.baseUrlKey && baseUrl !== undefined)
          env[shape.baseUrlKey] = baseUrl;
        current.env = env;
        mkdirSync(dirname(path), { recursive: true });
        writePrivateFileAtomicSync(
          path,
          `${JSON.stringify(current, null, 2)}\n`,
        );
        return shape.baseUrlKey && baseUrl !== undefined
          ? `${shape.valueKey} and ${shape.baseUrlKey} set in ${path}`
          : `${shape.valueKey} set in ${path}`;
      },
    );
  } catch (error) {
    return {
      status: "refused",
      reason: error instanceof Error ? error.message : "the write failed",
    };
  }
  return { status: "written", summary };
}
