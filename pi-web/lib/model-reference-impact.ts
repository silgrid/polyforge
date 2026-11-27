import { parseStepTierCandidate, stepTierItems } from "./step-tiers";
import { buildWorkflowRows, workflowUnion } from "./polyforge-workflows-pure";
import type { PolyforgeWorkflowsRead } from "./polyforge-workflows";

type ReadJson = (url: string) => Promise<Record<string, unknown>>;
const PROJECT_REQUIRED = "model-reference-project-required";
export function modelReferenceError(error: unknown, t: (key: string) => string): string {
  if (error instanceof Error && error.message === PROJECT_REQUIRED) return t("models.referenceNeedsProject");
  return error instanceof Error ? error.message : String(error);
}
const get: ReadJson = async (url) => {
  const response = await fetch(url);
  const body = await response.json();
  if (!response.ok || body.error) throw new Error(body.error ?? `HTTP ${response.status}`);
  return body;
};

export async function providerReferenceImpact(provider: string, cwd: string | null, read: ReadJson = get): Promise<string[]> {
  if (!cwd) throw new Error(PROJECT_REQUIRED);
  const cache = new Map<string, Promise<Record<string, unknown>>>();
  const cached: ReadJson = (url) => {
    if (!cache.has(url)) cache.set(url, read(url));
    return cache.get(url)!;
  };
  const query = cwd ? `?cwd=${encodeURIComponent(cwd)}` : "";
  const [native, catalog, host] = await Promise.all([
    cached(`/api/harness/pi/model${query}`),
    cached(`/api/models?scope=all${cwd ? `&cwd=${encodeURIComponent(cwd)}` : ""}`),
    cached("/api/polyforge/config?host=1"),
  ]);
  const refs = new Set(((catalog.modelList ?? []) as { provider: string; id: string }[]).filter((model) => model.provider === provider).map((model) => `${provider}/${model.id}`));
  const model = native.data as { defaultProvider?: string; defaultModel?: string } | undefined;
  if (model?.defaultProvider === provider && model.defaultModel) refs.add(`${provider}/${model.defaultModel}`);
  for (const tier of stepTierItems((host.items ?? []) as { key: string; value?: unknown }[])) for (const raw of tier.candidates) {
    const candidate = parseStepTierCandidate(raw);
    if (candidate?.harness === "pi" && candidate.model.startsWith(`${provider}/`)) refs.add(candidate.model);
  }
  return modelReferenceImpact([...refs], cwd, cached, "pi", provider);
}

/** Enumerates persisted references without changing any candidate, default or credential. */
export async function modelReferenceImpact(refs: readonly string[], cwd: string | null, read: ReadJson = get, harness: "pi" | "claude" = "pi", provider?: string): Promise<string[]> {
  // The profiles API needs a real project even to list global profiles. An
  // absent cwd is an unknown reference set, never evidence that it is empty.
  if (harness === "pi" && !cwd) throw new Error(PROJECT_REQUIRED);
  const matches = (candidate: string) => {
    const parsed = parseStepTierCandidate(candidate.startsWith("pi:") || candidate.startsWith("claude:") ? candidate : `pi:${candidate}`);
    return parsed?.harness === harness && (
      (provider !== undefined && parsed.model.startsWith(`${provider}/`))
      || refs.some((ref) => ref === parsed.model || (harness === "pi" && ref.slice(ref.indexOf("/") + 1) === parsed.model))
    );
  };
  const [native, host, profiles, accounts] = await Promise.all([
    read(`/api/harness/${harness}/model${cwd ? `?cwd=${encodeURIComponent(cwd)}` : ""}`),
    read("/api/polyforge/config?host=1"),
    cwd ? read(`/api/subagents/profiles?cwd=${encodeURIComponent(cwd)}`) : Promise.resolve({ profiles: [] }),
    read("/api/settings/account"),
  ]);
  if (!native.data || typeof native.data !== "object") throw new Error("Cannot verify the saved native model default");
  if (host.pfPresent === true && (host.capable === false || accounts.doctorError)) throw new Error("Cannot verify Host and Cell model references");
  const impact: string[] = [];
  const model = native.data as { defaultProvider?: string; defaultModel?: string } | undefined;
  if (harness === "claude" && model?.defaultModel && refs.includes(model.defaultModel)) impact.push(`default · claude:${model.defaultModel}`);
  if (harness === "pi" && model?.defaultProvider && model.defaultModel && (model.defaultProvider === provider || refs.includes(`${model.defaultProvider}/${model.defaultModel}`))) impact.push(`default · pi:${model.defaultProvider}/${model.defaultModel}`);
  for (const tier of stepTierItems((host.items ?? []) as { key: string; value?: unknown }[])) {
    for (const candidate of tier.candidates) if (matches(candidate)) impact.push(`Host · ${tier.tier} · ${candidate}`);
  }
  for (const profile of (profiles.profiles ?? []) as { name: string; scope: string; model?: string }[]) {
    if (profile.model && matches(profile.model)) impact.push(`subagent · ${profile.scope}/${profile.name} · ${profile.model}`);
  }
  const cells = (accounts.cells ?? []) as { profile: string }[];
  if (host.pfPresent === true) {
    const workflows = await Promise.all(cells.map((cell) => read(`/api/polyforge/workflows?profile=${encodeURIComponent(cell.profile)}&cwd=${encodeURIComponent(cwd ?? "")}`)));
    workflows.forEach((answer, index) => {
      if (answer.capable === false || answer.reason) throw new Error("Cannot verify workspace model references");
      const union = workflowUnion(answer as unknown as PolyforgeWorkflowsRead);
      if (union.failures.length) throw new Error("Cannot verify workspace model references");
      for (const { project, owner } of union.projects) for (const row of buildWorkflowRows(owner.workflows, owner.declared, project)) {
        const candidates = [...(row.declaredModels ?? []), ...row.effective.flatMap((chain) => chain.models)];
        for (const candidate of new Set(candidates)) if (matches(candidate)) impact.push(`Cell ${cells[index].profile} · ${project}/${row.id} · ${candidate}`);
      }
    });
  }
  return [...new Set(impact)];
}
