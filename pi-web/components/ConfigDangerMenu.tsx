"use client";

// 详情 header 的「⋯」菜单 + 二次确认（polyforge#259 T4 AC1）：只有不可逆的
// 删除 / 移除进菜单（工单计划审第 5 条），而且写入只能在确认态发生——「移除」
// 一次点击只是提问，第二次点击才执行。纯展示：阶段由调用方持有，所以两种
// 状态都能在 SSR 里断言。

import { useState } from "react";
import { ConfigButton } from "./SettingsUi";

/** 菜单的三个阶段：收起、列出动作、等确认。 */
export type ConfigDangerStage = "closed" | "menu" | "confirm";

export interface ConfigDangerMenuTexts {
  /** 「⋯」的无障碍名字。 */
  menu: string;
  /** 菜单里那一项（删除 / 移除）。 */
  action: string;
  /** 确认行的问题。 */
  confirmText: string;
  confirm: string;
  cancel: string;
}

export interface ConfigDangerMenuProps {
  texts: ConfigDangerMenuTexts;
  /** The open stage; a caller that keeps it (a row that resets on change) passes
   *  both this and `onStageChange`, every other caller lets the menu keep it. */
  stage?: ConfigDangerStage;
  onStageChange?: (stage: ConfigDangerStage) => void;
  /** 用户在确认行按了确认——这是唯一会写入的点击。 */
  onConfirm: () => void;
  busy?: boolean;
  /** The action's own availability; the trigger waits and is named by its note. */
  disabled?: boolean;
  /** The id of the visible text saying why the action is unavailable. */
  describedBy?: string;
}

/** 只有确认态可以写入：菜单项那一次点击只把阶段推到 confirm。 */
export function dangerConfirmAllowed(stage: ConfigDangerStage): boolean {
  return stage === "confirm";
}

export function ConfigDangerMenu({ texts, stage: controlledStage, onStageChange, onConfirm, busy = false, disabled = false, describedBy }: ConfigDangerMenuProps) {
  const [ownStage, setOwnStage] = useState<ConfigDangerStage>("closed");
  const stage = controlledStage ?? ownStage;
  const setStage = onStageChange ?? setOwnStage;
  if (stage === "closed") {
    return (
      <button
        type="button"
        className="config-danger-menu-trigger"
        aria-label={texts.menu}
        aria-describedby={describedBy}
        title={texts.menu}
        disabled={disabled || busy}
        onClick={() => setStage("menu")}
      >
        ⋯
      </button>
    );
  }
  if (stage === "menu") {
    return (
      <div className="config-danger-menu" role="group" aria-label={texts.menu}>
        <button
          type="button"
          className="config-danger-menu-item"
          disabled={disabled || busy}
          onClick={() => setStage("confirm")}
        >
          {texts.action}
        </button>
      </div>
    );
  }
  return (
    <div className="config-danger-confirm" role="group" aria-label={texts.confirmText}>
      <span className="config-danger-confirm-text">{texts.confirmText}</span>
      <ConfigButton
        size="small"
        variant="danger"
        disabled={busy || !dangerConfirmAllowed(stage)}
        onClick={onConfirm}
      >
        {texts.confirm}
      </ConfigButton>
      <ConfigButton size="small" disabled={busy} onClick={() => setStage("closed")}>
        {texts.cancel}
      </ConfigButton>
    </div>
  );
}
