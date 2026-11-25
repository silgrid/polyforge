// 原始 id → 显示名（polyforge#259 T4 AC2）：设置页里每一处「以原始 id 作主文
// 字」的地方都改以显示名为主、原始 id 作次行等宽灰字。这里只放 id 到 i18n key
// 的映射，页面负责渲染；映射是纯函数，所以单测不依赖 React 或 i18n provider。

/** 一个原始 id 的显示名。 */
export interface DisplayName {
  /** 显示名的 i18n key；为 null 时没有翻译，调用方直接用 `id`。 */
  labelKey: string | null;
  /** 显示名消息的参数（「Cell {name}」之类）。 */
  params?: Record<string, string>;
  /** 原始 id，永远原样保留，作次行。 */
  id: string;
}

/**
 * claude 自己的审批模式（`lib/harness/concepts.ts` 的 CLAUDE_APPROVAL_MODES）。
 * 一个模式一个 key：两个模式共用一个显示名会让三选一的选择变歧义。
 */
const CLAUDE_MODE_LABEL_KEYS: Readonly<Record<string, string>> = {
  default: "settings.permissionModeDefault",
  acceptEdits: "settings.permissionModeAcceptEdits",
  plan: "settings.permissionModePlan",
  auto: "settings.permissionModeAuto",
  dontAsk: "settings.permissionModeDontAsk",
  bypassPermissions: "settings.permissionModeBypass",
};

/** claude 审批模式的显示名（`acceptEdits`、`plan` …）；未知模式保留 id 本身。 */
export function claudeApprovalModeName(mode: string): DisplayName {
  return { labelKey: CLAUDE_MODE_LABEL_KEYS[mode] ?? null, id: mode };
}

/** `denyInsteadOfPrompting` 开关的显示名。 */
export const DENY_INSTEAD_OF_PROMPTING: DisplayName = {
  labelKey: "settings.permissionDenyInstead",
  id: "denyInsteadOfPrompting",
};

/**
 * 账号页凭据写入目标的显示名：`interactive` 是交互会话，其余是各 Cell 的
 * profile（「Cell default」「Cell public」）。profile 本身是名字，仍原样出现在
 * 显示名里，次行保留完整 id。
 */
export function accountTargetName(target: string): DisplayName {
  if (target === "interactive") return { labelKey: "settings.accountTargetInteractive", id: target };
  return { labelKey: "settings.accountTargetCell", params: { name: target }, id: target };
}

/** 一个插件源去掉协议与末尾分隔符后的名字段。 */
function sourceTail(source: string): string {
  const trimmed = source.trim().replace(/[/\\]+$/, "");
  const withoutProtocol = trimmed.replace(/^(?:npm|git):/i, "");
  // A scoped npm name ("@scope/pkg") names the package whole, not its tail.
  if (withoutProtocol.startsWith("@")) return withoutProtocol;
  const segments = withoutProtocol.split(/[/\\]/).filter(Boolean);
  return segments.at(-1) ?? source;
}

/** 插件侧栏行的主文字：包名，缺包名时用源的末段，绝不是整条安装路径。 */
export function pluginRowName(pkg: { packageName?: string | null; source: string }): string {
  const packageName = pkg.packageName?.trim();
  if (packageName) return packageName;
  return sourceTail(pkg.source);
}