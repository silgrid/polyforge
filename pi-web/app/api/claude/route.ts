import { NextResponse } from "next/server";
import { buildClaudeEnv } from "@/lib/claude-argv";
import { readClaudePreferences } from "@/lib/claude-preferences";
import { claudePiWebFeatures } from "@/lib/claude-probe";
import { claudeModelsCatalog } from "@/lib/claude-models";
import { CLAUDE_RECORDED_VERSIONS, DEFAULT_CLAUDE_MODEL } from "@/lib/claude-shared";

export const dynamic = "force-dynamic";

// GET /api/claude - whether the claude harness can be offered (a `claude` on PATH) and
// what that claude reports: its model aliases from its `initialize`
// answer (lib/claude-probe.ts: one claude, no prompt, cached per binary) plus the
// custom names of claude's own native modelPicker (85a4: the retired pi-web-stored
// list migrated there), `default` the default; its version, whether the fixtures cover
// it, and its own capabilities verbatim; beside them Pi Web's three claude features with
// their basis (claudePiWebFeatures(): approvals, background tasks, subagent text — from
// Pi Web's own evidence, the child's environment included; approvals from the Pi Web
// approval channel, read only — r3). A failed probe still offers `default` (the alias
// the CLI resolves itself) plus the custom names, with the error. Writes nothing, creates
// no directory and never reads a credential. Behind proxy.ts's password gate.
export async function GET() {
  const catalog = await claudeModelsCatalog();
  const binary = catalog.binary;
  const probe = catalog.probe;
  const info = probe && !("error" in probe) ? probe : null;
  return NextResponse.json({
    available: binary !== null,
    ...(binary ? {} : { reason: "not-on-path" }),
    models: [...catalog.aliases, ...catalog.custom],
    defaultModel: DEFAULT_CLAUDE_MODEL,
    version: info?.version ?? null,
    versionRecorded: info?.version ? info.recorded : null,
    recordedVersions: CLAUDE_RECORDED_VERSIONS,
    capabilities: info?.capabilities ?? [],
    features: claudePiWebFeatures(info?.version ?? null, buildClaudeEnv(process.env), readClaudePreferences().approvalChannel),
    ...(probe && "error" in probe ? { probeError: probe.error } : {}),
  }, { headers: { "Cache-Control": "no-store" } });
}
