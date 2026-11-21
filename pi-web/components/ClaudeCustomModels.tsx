"use client";

import { useI18n } from "@/hooks/useI18n";

/**
 * The models page's Claude section (85a3-3): the editor of claude's own
 * native modelPicker custom names, and the home of the retired
 * self-stored customModels' one-time migration. Zero-value skeleton: the
 * signature exists so the suite's red is its assertions.
 */
export function ClaudeCustomModels({ onRefresh }: { onRefresh?: () => void }) {
  const { t } = useI18n();
  return <div data-claude-custom-models><h4>{t("models.claudeCustomHeading")}</h4></div>;
}
