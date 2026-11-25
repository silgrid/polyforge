"use client";

// 「已保存 · 撤销」（polyforge#259 T4 AC1）：开关与单选立即写之后的那条 toast。
// 到期自己消失，所以撤销按钮只在那一段时间里存在。纯展示加一个到期计时器：词
// 都由调用方给，它不发请求也不判断写没写成。

import { useEffect } from "react";

export interface SettingsSavedToastProps {
  texts: { saved: string; undo: string };
  /** 撤销还能用多久（毫秒）；0 或更小就什么都不渲染。 */
  remainingMs: number;
  onUndo: () => void;
  /** 到期时告诉调用方（让它丢掉那条撤销记录）。 */
  onExpire?: () => void;
}

export function SettingsSavedToast({ texts, remainingMs, onUndo, onExpire }: SettingsSavedToastProps) {
  useEffect(() => {
    if (remainingMs <= 0) return;
    const timer = setTimeout(() => onExpire?.(), remainingMs);
    return () => clearTimeout(timer);
  }, [remainingMs, onExpire]);
  // Spent, the toast is gone, undo button included: the one window the ticket
  // gives a toggle's undo. `remainingMs` is the caller's write-time fact, never
  // a clock read during render.
  if (remainingMs <= 0) return null;
  return (
    <div role="status" className="settings-saved-toast">
      <span className="settings-saved-toast-text">{texts.saved}</span>
      <button type="button" className="settings-saved-toast-undo" onClick={onUndo}>{texts.undo}</button>
    </div>
  );
}
