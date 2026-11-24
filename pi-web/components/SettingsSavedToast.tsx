"use client";

// 「已保存 · 撤销」（polyforge#259 T4 AC1）：开关与单选立即写之后的那条 toast。
// 到期自己消失，所以撤销按钮只在那一段时间里存在；presentational，词都由调用方给。

export interface SettingsSavedToastProps {
  texts: { saved: string; undo: string };
  /** 这条 toast 什么时候过期（毫秒时间戳）；已过期就什么都不渲染。 */
  expiresAt: number;
  onUndo: () => void;
  /** 到期时告诉调用方（让它丢掉那条撤销记录）。 */
  onExpire?: () => void;
}

export function SettingsSavedToast(_props: SettingsSavedToastProps) {
  return null;
}
