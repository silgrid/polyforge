package stepcall

// language.go: the language of the daemon's step agents,
// `[daemon] agent_language`. One fact, one place: the vocabulary and its
// default (the spawn journal's record of a dispatch's language, so the
// capsule package holds them), the hand-off body's headings and every
// label the daemon stamps on artifacts/handoff.md in each language, live
// here.

import "github.com/silgrid/polyforge/internal/common/capsule"

// The agent languages `[daemon] agent_language` accepts.
const (
	LangEN   = capsule.LangEN
	LangZhCN = capsule.LangZhCN
	// DefaultLanguage is the language an absent key means.
	DefaultLanguage = capsule.DefaultLanguage
)

// IsLanguage reports whether v is one of the accepted agent languages
// (exact spelling).
func IsLanguage(v string) bool { return capsule.IsLanguage(v) }

// bodySections are the body template's seven headings per language, in
// the template's order (design #483 §6; workflows/_shared/handoff.md).
var bodySections = map[string][]string{
	LangZhCN: {"结论", "这一步要回答什么", "做了什么", "关键决定和理由", "验证", "发现的问题和风险", "给下游与未决"},
	LangEN:   {"Conclusion", "The question this step answers", "What was done", "Decisions and why", "Verification", "Problems and risks found", "For downstream, and what is open"},
}

// reentrySections are the heading a body carries, per language, when its
// step handed off before (a re-run answers the previous round point by
// point).
var reentrySections = map[string]string{
	LangZhCN: "回应上一轮",
	LangEN:   "Reply to the previous round",
}

// BodySectionsOf answers the seven body headings of lang, in order (a
// fresh slice); an absent or unknown language answers DefaultLanguage's.
func BodySectionsOf(lang string) []string {
	s, ok := bodySections[lang]
	if !ok {
		s = bodySections[DefaultLanguage]
	}
	return append([]string{}, s...)
}

// ReentrySectionOf answers the re-entry heading of lang; an absent or
// unknown language answers DefaultLanguage's.
func ReentrySectionOf(lang string) string {
	if s, ok := reentrySections[lang]; ok {
		return s
	}
	return reentrySections[DefaultLanguage]
}

// Labels are every label the daemon stamps on artifacts/handoff.md in one
// language: the opening, a section's header and the event sections. A
// field with verbs is a format string; one starting with a separator
// carries its language's punctuation.
type Labels struct {
	// The opening: its title, note, table, acceptance and lane step order.
	Doc, OpeningNote, Item, Value, WorkItem, Project, Lane, Goal  string
	Acceptance, NoAcceptance, StepOrder                           string
	GateRewindsTo, Waivable, LastStep, Skipped, NoteList, NoteSep string
	MaxRewinds                                                    string
	// A section's header (RenderSection) and its repos' diff stat.
	SectionTitle, StampNote                                                string
	Step, Round, Dispatch, Started, Ended, Duration, Model, Status, Reason string
	Evidence, RepoCommits, NoRepo, StartUnknown, RepoStat                  string
	StatStartUnknown, StatNoCommits, StatUnavailable, StatNoChanges        string
	RoundEvents, OwnerMessages, None, PerOwner, Body                       string
	// The event sections (回退 / Rewind, 收尾 / Close).
	Event, Rewind, Close, EventNote, Trigger, TriggerText, TriggerPerOwner string
	EventReason, ReasonSeeBody, ResultEnded, ResultCanceled, Redo, Rewinds string
	OwnerRewinds, PerOwnerUncounted, HowToRead, ResultCompleted            string
}

var labels = map[string]Labels{
	LangZhCN: {
		Doc:           "交接文档",
		OpeningNote:   "本文件只追加：开头和事件段由 daemon 写入；每段的头部由 daemon 盖章，正文由该步经 `pf step handoff` 写入",
		Item:          "项",
		Value:         "值",
		WorkItem:      "工单",
		Project:       "项目",
		Lane:          "车道",
		Goal:          "目标",
		Acceptance:    "验收清单（工单原文）",
		NoAcceptance:  "（工单正文没有验收段：`## Acceptance` / `## 验收`）",
		StepOrder:     "车道步骤顺序",
		GateRewindsTo: "，fail 回退到 `%s`",
		Waivable:      "，可放行",
		LastStep:      "最后一步",
		Skipped:       "已跳过",
		NoteList:      "（%s）",
		NoteSep:       "；",
		MaxRewinds:    "回退次数上限（max_rewinds）：%d",

		SectionTitle:     "%s · 第 %d 轮 · 派发 %d · %s",
		StampNote:        "以下头部由 daemon 盖章（调用台账 seq %d），step 不写",
		Step:             "步骤",
		Round:            "轮次",
		Dispatch:         "派发",
		Started:          "开始",
		Ended:            "结束",
		Duration:         "耗时",
		Model:            "模型",
		Status:           "状态",
		Reason:           "原因",
		Evidence:         "证据",
		RepoCommits:      "各仓提交",
		NoRepo:           "（无任务仓）",
		StartUnknown:     "（起点未知）",
		RepoStat:         "（%s）",
		StatStartUnknown: "起点未知",
		StatNoCommits:    "无新提交",
		StatUnavailable:  "改动统计不可得",
		StatNoChanges:    "无改动",
		RoundEvents:      "本轮事件",
		OwnerMessages:    "本步本轮收到的 owner 消息",
		None:             "（无）",
		PerOwner:         "`--per-owner` 引用的 owner 消息",
		Body:             "正文（step 撰写）",

		Event:             "事件",
		Rewind:            "回退",
		Close:             "收尾",
		EventNote:         "daemon 写入",
		Trigger:           "触发：%s",
		TriggerText:       "`%s` 第 %d 轮交接 `%s`（调用台账 call %d）",
		TriggerPerOwner:   "，依据 owner 消息 `%s`（原文见该段头部）",
		EventReason:       "原因：%s",
		ReasonSeeBody:     "见该步该段正文",
		ResultEnded:       "结果：工单结束，状态 `%s`",
		ResultCanceled:    "结果：owner 取消，工单以 canceled 结束",
		Redo:              "要重做的步骤（按顺序）：%s",
		Rewinds:           "回退次数：%d/%d",
		OwnerRewinds:      "（另有 owner 的回退 %d 次，不计次数）",
		PerOwnerUncounted: "；本次依据 owner 的话，不计次数",
		HowToRead:         "读法：被点名的步骤逐条回应上一轮（正文加「回应上一轮」）；它们之前的段落仍然有效、可以参考",
		ResultCompleted:   "结果：车道的每一步都已 done，daemon 收尾（wrap）工单",
	},
	LangEN: {
		Doc:           "Hand-off document",
		OpeningNote:   "append-only: the daemon writes the opening and the events and stamps each section's header; each step writes its body through `pf step handoff`",
		Item:          "Item",
		Value:         "Value",
		WorkItem:      "Work item",
		Project:       "Project",
		Lane:          "Lane",
		Goal:          "Goal",
		Acceptance:    "Acceptance (verbatim from the work item)",
		NoAcceptance:  "(the work item has no `## Acceptance` section)",
		StepOrder:     "Lane step order",
		GateRewindsTo: ", fail rewinds to `%s`",
		Waivable:      ", waivable",
		LastStep:      "last step",
		Skipped:       "skipped",
		NoteList:      " (%s)",
		NoteSep:       "; ",
		MaxRewinds:    "Rewind budget (max_rewinds): %d",

		SectionTitle:     "%s · round %d · dispatch %d · %s",
		StampNote:        "the header below is the daemon's stamp (call ledger seq %d); the step does not write it",
		Step:             "Step",
		Round:            "Round",
		Dispatch:         "Dispatch",
		Started:          "Started",
		Ended:            "Ended",
		Duration:         "Duration",
		Model:            "Model",
		Status:           "Status",
		Reason:           "Reason",
		Evidence:         "Evidence",
		RepoCommits:      "Task repo commits",
		NoRepo:           "(no task repo)",
		StartUnknown:     "(start unknown)",
		RepoStat:         " (%s)",
		StatStartUnknown: "start unknown",
		StatNoCommits:    "no new commits",
		StatUnavailable:  "diff stat unavailable",
		StatNoChanges:    "no changes",
		RoundEvents:      "Events of this round",
		OwnerMessages:    "Owner messages to this step this round",
		None:             "(none)",
		PerOwner:         "The owner message `--per-owner` cites",
		Body:             "Body (written by the step)",

		Event:             "Event",
		Rewind:            "Rewind",
		Close:             "Close",
		EventNote:         "written by the daemon",
		Trigger:           "Trigger: %s",
		TriggerText:       "`%s` round %d handed off `%s` (call ledger call %d)",
		TriggerPerOwner:   ", resting on the owner message `%s` (quoted verbatim in that section's header)",
		EventReason:       "Reason: %s",
		ReasonSeeBody:     "see that step's section body",
		ResultEnded:       "Result: the work item ended, status `%s`",
		ResultCanceled:    "Result: the owner cancelled; the work item ended canceled",
		Redo:              "Steps to redo (in order): %s",
		Rewinds:           "Rewinds: %d/%d",
		OwnerRewinds:      " (plus %d owner rewinds, not counted)",
		PerOwnerUncounted: "; this one rests on the owner's word and is not counted",
		HowToRead:         "How to read: each named step answers the previous round point by point (its body adds the re-entry heading); the sections before it still stand as reference",
		ResultCompleted:   "Result: every step of the lane is done; the daemon wraps the work item",
	},
}

// LabelsOf answers the stamp labels of lang; an absent or unknown
// language answers DefaultLanguage's.
func LabelsOf(lang string) Labels {
	if l, ok := labels[lang]; ok {
		return l
	}
	return labels[DefaultLanguage]
}
