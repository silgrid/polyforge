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

export function buildModelRows(_models: readonly ModelViewInput[], _defaultModel: Pick<ModelViewInput, "harness" | "provider" | "id"> | null): ModelViewRow[] {
  return [];
}
