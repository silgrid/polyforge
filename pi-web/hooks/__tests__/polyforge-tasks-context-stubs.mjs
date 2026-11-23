// The i18n and MarkdownBody stand-ins of the polyforge#230 Tasks-context
// behaviour suite (components/PolyforgeTasks.context.test.mjs). A unit's own
// fixtures live under a recognized test path (the PolyforgePanel progress
// precedent, hooks/__tests__/), so its test commit carries them alone.
//
// `t` renders real wording for the few keys the suite reads — the one
// interpolated key 分析 <slug> included — so an assertion sees the sentence a
// user reads, never a bare key; any other key falls back to itself.
const MESSAGES = {
  "polyforge.tasks.analysisOf": "分析 {slug}",
  "polyforge.context": "上下文",
  "polyforge.tasks.state.running": "运行中",
  "polyforge.tasks.state.completed": "已完成",
  "polyforge.tasks.tailRunning": "生成中…",
  "polyforge.tasks.tailEmpty": "暂无报告",
  "polyforge.tasks.status": "状态",
  "polyforge.tasks.project": "项目",
  "polyforge.tasks.candidates": "候选",
  "polyforge.tasks.created": "创建",
  "polyforge.tasks.started": "开始",
  "polyforge.tasks.ended": "结束",
  "polyforge.tasks.prompt": "提示",
  "polyforge.tasks.report": "报告",
  "polyforge.tasks.back": "返回",
  "polyforge.tasks.empty": "暂无任务",
  "polyforge.tasks.emptyHint": "还没有一次性任务。",
  "polyforge.tasks.running": "正在运行",
  "polyforge.tasks.all": "全部",
  "polyforge.tasks.expand": "展开",
  "polyforge.tasks.collapse": "收起",
  "polyforge.loading": "加载中",
};

const t = (key, params) => (MESSAGES[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? ""));
const i18n = { t, locale: "zh-CN" };

export function useI18n() {
  return i18n;
}

export function MarkdownBody() {
  return null;
}
