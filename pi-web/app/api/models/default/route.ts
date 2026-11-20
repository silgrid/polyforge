import { stat } from "fs/promises";
import { resolve } from "path";
import { createAgentSessionServices, getAgentDir } from "@earendil-works/pi-coding-agent";
import {
  isThinkingLevel,
  projectSettingsPath,
  shadowingProjectKeys,
  type DefaultPreferencesEdit,
} from "@/lib/default-preferences";
import { getAllowedFileRoots, isExistingFilePathAllowed } from "@/lib/file-access";
import { resolveVisibleModels } from "@/lib/model-scope";
import { withDeferredProviderModels } from "@/lib/deferred-provider-models";
import { invalidateModelsCache } from "@/lib/models-cache";
import { projectTrustReloadOptions } from "@/lib/project-trust";
import { createPiAdapter } from "@/lib/harness/pi";
import { createClaudeAdapter } from "@/lib/harness/claude";
import { unifiedGlobalEffortWrite } from "@/lib/harness/effort-levels";
import { readDefaultModelPreference, writeDefaultModelPreference } from "@/lib/default-model-preference";
import {
  DEFAULT_CLAUDE_MODEL,
  isClaudeModelName,
  normalizeClaudeEffort,
  type ClaudeEffort,
} from "@/lib/claude-shared";

export const dynamic = "force-dynamic";

interface DefaultPreferencesRequest {
  cwd?: unknown;
  provider?: unknown;
  modelId?: unknown;
  thinkingLevel?: unknown;
  /** 85a3-1: the combined default's harness; "claude" takes the claude branch. */
  harness?: unknown;
  /** With harness "claude": the model name the native write lands. */
  model?: unknown;
}

function parseEdit(body: DefaultPreferencesRequest): DefaultPreferencesEdit | null {
  const edit: DefaultPreferencesEdit = {};
  if (body.provider !== undefined || body.modelId !== undefined) {
    if (typeof body.provider !== "string" || !body.provider) return null;
    if (typeof body.modelId !== "string" || !body.modelId) return null;
    edit.model = { provider: body.provider, modelId: body.modelId };
  }
  if (body.thinkingLevel !== undefined) {
    if (!isThinkingLevel(body.thinkingLevel)) return null;
    edit.thinkingLevel = body.thinkingLevel;
  }
  return edit.model || edit.thinkingLevel ? edit : null;
}

/**
 * The claude half of the star (85a3-1, AC2): the model lands in claude's
 * native user-level settings.json through the adapter's gated write — the
 * same concept write the harness route serves — and the combined default
 * (「harness + 模型」) records harness "claude" beside the snapshot, the same
 * setting the pi star writes. No cwd: claude's defaults are user-level.
 */
async function putClaudeDefault(model: unknown): Promise<Response> {
  if (!isClaudeModelName(model)) {
    return Response.json({ error: `A claude default needs a model name --model takes: ${String(model)}` }, { status: 400 });
  }
  const written = await createClaudeAdapter().write("model", { model }, {});
  if (written.status !== "written") {
    return Response.json({ error: written.reason }, { status: 400 });
  }
  await writeDefaultModelPreference({ harness: "claude", claudeModel: model });
  return Response.json({ ok: true, harness: "claude", model });
}

/**
 * GET: the combined default the composer starts from — the stored default
 * harness (Pi Web's own preference, pi until the first star says otherwise)
 * and claude's native defaults read through the adapter (Q2 A: a new claude
 * session's initial model is the native `model`, "default" when unset; its
 * effort shows the native `effortLevel`, "default" when unset). Reads are
 * open to any version; nothing here spawns or writes.
 */
export async function GET() {
  const preference = readDefaultModelPreference();
  const read = await createClaudeAdapter().read("model", {});
  const payload = read !== null && "defaultModel" in read ? read : null;
  const claudeDefaultModel = payload !== null && isClaudeModelName(payload.defaultModel)
    ? payload.defaultModel
    : DEFAULT_CLAUDE_MODEL;
  const claudeDefaultEffort: ClaudeEffort = normalizeClaudeEffort(payload?.reasoningEffort?.raw ?? null);
  return Response.json({
    defaultHarness: preference.harness,
    claudeDefaultModel,
    claudeDefaultEffort,
  });
}

/**
 * Save the default model and/or reasoning level new sessions start with.
 *
 * Picking a model for one chat is session-scoped, as in the TUI; this is the
 * explicit "save as default" behind the selectors' star. The default model
 * is a 「harness + 模型」 combination (85a3-1): the pi branch writes pi's
 * native defaults through the pi adapter's gated write and records the same
 * combined setting, the claude branch (harness "claude") writes claude's
 * native `model` the same way. The reasoning level is the unified global
 * default (一写多读): a level both harnesses spell (low..max) reaches pi's
 * `defaultThinkingLevel` and claude's `effortLevel` alike, pi's own levels
 * (off, minimal) stay pi's alone. The cwd decides which project settings
 * could shadow the global value and which models are in scope, so it goes
 * through the same allow-list as `/api/models`.
 */
export async function PUT(req: Request) {
  let body: DefaultPreferencesRequest;
  try {
    body = await req.json() as DefaultPreferencesRequest;
  } catch {
    return Response.json({ error: "Invalid JSON body" }, { status: 400 });
  }
  // A JSON-null body parses successfully and would surface as a TypeError
  // (reading .cwd/.provider off null) instead of a controlled 400 (review
  // finding: non-blocking, fixed alongside).
  if (body === null || typeof body !== "object") {
    return Response.json({ error: "Invalid JSON body" }, { status: 400 });
  }
  // The combined default's harness: "claude" takes its own branch; "pi" or
  // absent keeps the pi flow; anything else is refused before anything runs.
  if (body.harness === "claude") {
    return putClaudeDefault(body.model);
  }
  if (body.harness !== undefined && body.harness !== "pi") {
    return Response.json({ error: `Unknown harness: ${String(body.harness)}` }, { status: 400 });
  }
  const edit = parseEdit(body);
  if (!edit) {
    return Response.json({ error: "Expected provider and modelId, or a valid thinkingLevel" }, { status: 400 });
  }

  const cwd = resolve(typeof body.cwd === "string" && body.cwd ? body.cwd : process.cwd());
  let cwdStat;
  try {
    cwdStat = await stat(cwd);
  } catch {
    return Response.json({ error: `Directory does not exist: ${cwd}` }, { status: 400 });
  }
  if (!cwdStat.isDirectory()) {
    return Response.json({ error: `Not a directory: ${cwd}` }, { status: 400 });
  }
  const allowedRoots = await getAllowedFileRoots();
  if (!isExistingFilePathAllowed(cwd, allowedRoots)) {
    return Response.json({ error: "Access denied" }, { status: 403 });
  }

  try {
    const agentDir = getAgentDir();
    const trustReloadOptions = projectTrustReloadOptions(cwd, agentDir);
    const services = await createAgentSessionServices({
      cwd,
      agentDir,
      ...(trustReloadOptions ? { resourceLoaderReloadOptions: trustReloadOptions } : {}),
    });
    const { settingsManager } = services;

    const shadowed = shadowingProjectKeys(settingsManager, edit);
    if (shadowed.length > 0) {
      const settingsPath = projectSettingsPath(cwd);
      return Response.json({
        error: `${settingsPath} sets ${shadowed.join(", ")} for this project, so a global default would not apply here.`,
        reason: "project-scope",
        settingsPath,
        keys: shadowed,
      }, { status: 409 });
    }

    if (edit.model) {
      // Only a model the selector can offer is a default that actually takes
      // effect: startup falls back to the first scoped model otherwise.
      const scope = await resolveVisibleModels(withDeferredProviderModels(services.modelRuntime), settingsManager.getEnabledModels());
      const { provider, modelId } = edit.model;
      if (!scope.visible.some((model) => model.provider === provider && model.id === modelId)) {
        return Response.json({ error: `Model not available: ${provider}/${modelId}` }, { status: 404 });
      }
    }

    // The native writes go through the adapters' gated writes (85a3-1 AC1):
    // pi's settings.json defaults, one field per write, unknown keys kept.
    const piAdapter = createPiAdapter();
    if (edit.model) {
      const written = await piAdapter.write("model", { model: { provider: edit.model.provider, modelId: edit.model.modelId } }, { agentDir });
      if (written.status !== "written") {
        return Response.json({ error: written.reason }, { status: 400 });
      }
    }
    let unified: { pi: string; claude: string; claudeReason?: string } | undefined;
    if (edit.thinkingLevel) {
      const written = await piAdapter.write("model", { thinkingLevel: edit.thinkingLevel }, { agentDir });
      if (written.status !== "written") {
        return Response.json({ error: written.reason }, { status: 400 });
      }
      // The unified global default (一写多读, AC3): a level both harnesses
      // spell reaches claude's effortLevel too; pi's own level (off, minimal)
      // stays pi's alone. A claude half that cannot run (no claude, a version
      // the gate refuses) never rolls pi's half back — the response names it.
      const mapping = unifiedGlobalEffortWrite(edit.thinkingLevel);
      if (mapping.claude === null) {
        unified = {
          pi: "written",
          claude: "skipped",
          claudeReason: `${edit.thinkingLevel} is pi's own level; claude keeps its own default`,
        };
      } else {
        const claudeResult = await createClaudeAdapter().write("model", { effortLevel: mapping.claude }, {});
        unified = claudeResult.status === "written"
          ? { pi: "written", claude: "written" }
          : { pi: "written", claude: "refused", claudeReason: claudeResult.reason };
      }
    }
    // The combined default (AC2): the star writes the same 「harness + 模型」
    // setting the claude star writes. A reasoning-level star alone is not a
    // model choice and leaves the stored default as it is.
    if (edit.model) {
      await writeDefaultModelPreference({ harness: "pi", model: { provider: edit.model.provider, modelId: edit.model.modelId } });
    }
    invalidateModelsCache();
    return Response.json({
      ok: true,
      ...(edit.model ? { defaultModel: edit.model } : {}),
      ...(edit.thinkingLevel ? { defaultThinkingLevel: edit.thinkingLevel, ...(unified ? { unified } : {}) } : {}),
    });
  } catch (error) {
    return Response.json({ error: error instanceof Error ? error.message : String(error) }, { status: 500 });
  }
}
