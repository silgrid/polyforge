export interface ModelViewInput {
  harness: string;
  provider: string;
  id: string;
  name: string;
  chatAvailable: boolean;
  reasoning?: string;
}

export interface ModelViewRow {
  provider: string;
  id: string;
  name: string;
  harnesses: string[];
  chatAvailable: boolean;
  reasoning: string[];
  isDefault: boolean;
}

export function buildModelRows(models: readonly ModelViewInput[], defaultModel: Pick<ModelViewInput, "harness" | "provider" | "id"> | null): ModelViewRow[] {
  const groups = new Map<string, ModelViewRow>();
  for (const model of models) {
    const key = `${model.provider}\u0000${model.id}`;
    const row = groups.get(key);
    if (row) {
      row.isDefault ||= defaultModel?.harness === model.harness
        && defaultModel.provider === model.provider && defaultModel.id === model.id;
      if (!row.harnesses.includes(model.harness)) row.harnesses.push(model.harness);
      if (model.chatAvailable) row.chatAvailable = true;
      if (model.reasoning && !row.reasoning.includes(model.reasoning)) row.reasoning.push(model.reasoning);
      continue;
    }
    groups.set(key, {
      provider: model.provider,
      id: model.id,
      name: model.name,
      harnesses: [model.harness],
      chatAvailable: model.chatAvailable,
      reasoning: model.reasoning ? [model.reasoning] : [],
      isDefault: defaultModel?.harness === model.harness
        && defaultModel.provider === model.provider
        && defaultModel.id === model.id,
    });
  }
  return [...groups.values()].sort((a, b) => a.provider.localeCompare(b.provider) || a.id.localeCompare(b.id));
}
