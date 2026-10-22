package test

// w256_docs_translation_acceptance_test.go — polyforge#256 e2e
// acceptance scenarios for the in-place English translation of the six
// live Chinese design docs and the new docs/zh.md routing page
// (artifacts/spec.md, "## Requirements" / "## Acceptance criteria"):
//
//   - the Han-character bar: after removing every operative-literal
//     allowlist occurrence (the spec's "## The operative-literal
//     allowlist", Families 1–3), no \p{Han} rune remains in any of the
//     six docs — full-width punctuation is not barred (spec decision 2),
//     and a Chinese original retained beside the translation is a Han
//     rune outside the allowlist and fails the same bar;
//   - the README↔zh.md drift bar: README's Documentation list opens with
//     the zh.md pointer (`中文导读 (Chinese orientation): docs/zh.md`),
//     and every docs link the list carries — including docs/zh.md
//     itself, carried by zh.md's own pointer paragraph (spec decision 8,
//     no self-exemption) — also appears in docs/zh.md, so a newly added
//     doc page cannot leave the routing page silently stale.
//
// Authored against the spec alone, BEFORE the translation is delivered
// (the e2e-author step runs pre-code): both bars are red on the
// pre-implementation tree — the six docs are full of non-allowlisted Han
// (08's `<!-- 权威 -->` title marker, the ledger's 2026-10-12 banner,
// every narrative line), and README's Documentation list opens with the
// concepts pointer while docs/zh.md does not exist — and they turn green
// only on the delivered translation.
//
// Assertions are on spec-pinned tokens and shapes — never on wording;
// the prose-level halves of the criteria (the untouched set
// byte-identical, §-marker and heading-number survival, the frozen
// glossary, gloss quality, discrepancy reporting, dangling pointers
// kept, the routing page restating no fact) are judged by review against
// the same spec (artifacts/e2e-scenarios.md). The docs-only nature of
// this work item shows in the scenario shape: the project's real
// interfaces here are the doc files themselves and the repo's Go test
// bar — no daemon, no aihub, no harness is spawned (the w254 precedent).
//
// Interpretation note for `code` (spec decision 4 meets the Han bar):
// the ledger's dangling `references/agent-contract.md` §十一 mention
// keeps its pointer — the sentence around it is translated, the target
// is neither retargeted nor fixed — and the section-number word renders
// as `§11` because `十一` is not in the allowlist; the Han bar itself
// forces that number word to English.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// w256Docs is the translated set: the six live Chinese design docs
// under docs/design/polyforge-vnext/ (the ledger included — it is the
// live ledger that keeps receiving status writebacks, grill answer 1,
// not archive/ history).
var w256Docs = []string{
	"01-workflow-skills.md",
	"06-memory-search.md",
	"08-capsule-base-migration.md",
	"v2-converge.md",
	"wi-kind-contract.md",
	"v0-lessons-ledger.md",
}

// w256Allowlist is the spec's exhaustive operative-literal allowlist
// ("## The operative-literal allowlist", Families 1–3): the Chinese
// strings that code, a prompt or a test matches byte-for-byte, so the
// translated docs keep them verbatim (each with a parenthesized English
// gloss). Every other Han rune in the six docs is translated away.
var w256Allowlist = []string{
	// Family 1 — the wrap-memory prompt contract (06 §8): pinned
	// byte-for-byte by workflows/_shared/memory-wrap.md,
	// skills/polyforge/verbs/remember.md, spawn_test.go and
	// stepmem_test.go. The full-width colon of 建议新增条目： is part
	// of the literal.
	"做了什么", "关键决定", "遗留", "建议新增条目：",
	// Family 2 — the failure-text classifier patterns (v2-converge
	// §102/§103/§106/§119), matched byte-for-byte by the ordered
	// classification regexes in internal/polyforge/driver/
	// supervision.go. Machine-class / local row (hostPatterns):
	"权限不够", "权限被拒绝", "拒绝访问",
	// Allowance/auth row (textClasses row 1, FailExitModel):
	"余额不足", "欠费", "额度", "配额", "用尽", "用完", "耗尽", "不足",
	"超出", "超限", "超过", "账户", "停用", "禁用", "冻结", "鉴权失败",
	"认证失败", "无效", "密钥", "令牌", "无权限", "没有权限", "权限不足",
	"模型", "接口", "服务",
	// Rate-limit row (textClasses row 2, FailRateLimited):
	"并发", "过高", "过大", "过多", "超限", "超出", "超过", "请求",
	"过于", "太", "频繁", "请求频率", "请求过多", "并发超额", "限流",
	"速率限制", "繁忙", "负载过高",
	// Family 3 — the summarize.language default (runtime state.go
	// DefaultSummarizeLanguage): verbatim in backticks with a gloss.
	// No occurrence in the six docs today; defensive.
	"`中文`",
}

// w256HanRe matches one Han rune (\p{Han}). Full-width CJK punctuation
// is not a Han rune and is not barred (spec decision 2).
var w256HanRe = regexp.MustCompile(`\p{Han}`)

// w256Root answers the repo root (this package's cwd is the repo's
// test/ dir).
func w256Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// w256Read answers a repo-relative file's body, failing on the named
// path.
func w256Read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w256Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#256: read %s: %v", rel, err)
	}
	return string(raw)
}

// w256Strip removes every allowlisted literal occurrence from body —
// what remains must carry no Han rune. Longest-first: the entries are
// sorted by rune length, descending, before the ReplaceAll pass, so a
// shorter entry (不足) cannot destroy a longer allowlisted literal
// (权限不足) before it is matched as a whole (owner ruling
// w256-review-b1-b5-b6, B1).
func w256Strip(body string) string {
	ordered := append([]string(nil), w256Allowlist...)
	sort.Slice(ordered, func(i, j int) bool {
		return len([]rune(ordered[i])) > len([]rune(ordered[j]))
	})
	for _, lit := range ordered {
		body = strings.ReplaceAll(body, lit, "")
	}
	return body
}

// TestW256HanBarOutsideAllowlist: the six docs are English in place —
// after removing every allowlisted literal occurrence (Families 1–3),
// no Han rune remains in any of the six docs. A Chinese original
// retained beside the translation, an untranslated placeholder,
// runbook-fence shell comment, quoted ruling, banner or ledger-prose
// line is a surviving Han rune and fails this bar. Red on the
// pre-translation tree (the docs are full of non-allowlisted Han), green
// only on the delivered translation.
func TestW256HanBarOutsideAllowlist(t *testing.T) {
	const perDoc = 3 // cap the evidence lines per doc
	var offenders []string
	for _, name := range w256Docs {
		rel := filepath.Join("docs", "design", "polyforge-vnext", name)
		body := w256Read(t, rel)
		found := 0
		for i, line := range strings.Split(body, "\n") {
			if !w256HanRe.MatchString(w256Strip(line)) {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			if found++; found == perDoc {
				break
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("polyforge#256: the six docs are English in place — after stripping the operative-literal allowlist (Families 1–3) no Han rune may remain in any of them (full-width punctuation is not barred) — but non-allowlisted Han survives (first lines per doc):\n%s", strings.Join(offenders, "\n"))
	}
}

// w256DocLinkRe matches a Markdown link target into the docs set.
var w256DocLinkRe = regexp.MustCompile(`\]\((docs/[^)\s]+)\)`)

// TestW256ReadmeZhRoutingDriftBar: README's Documentation list opens
// with the zh.md pointer, and every docs link the list carries —
// including docs/zh.md itself, no self-exemption — also appears in
// docs/zh.md. Red on the pre-change tree (the list opens with the
// concepts pointer and docs/zh.md does not exist), green only on the
// delivered routing page.
func TestW256ReadmeZhRoutingDriftBar(t *testing.T) {
	// the Documentation section: from its '## ' heading to the next
	// heading, every Markdown link target into docs/ collected in
	// document order.
	var targets []string
	firstLine := ""
	inSection := false
	for _, line := range strings.Split(w256Read(t, "README.md"), "\n") {
		if strings.HasPrefix(line, "##") {
			if inSection {
				break // the next section heading ends the list
			}
			inSection = strings.HasPrefix(line, "## Documentation")
			continue
		}
		if !inSection {
			continue
		}
		// every docs link on the line, not just the first — a README line
		// may carry several (line 17 carries both quickstart links);
		// owner ruling w256-review-b1-b5-b6, B5. The first-line logic
		// is unchanged: the line carrying the first target.
		for _, m := range w256DocLinkRe.FindAllStringSubmatch(line, -1) {
			if len(targets) == 0 {
				firstLine = strings.TrimSpace(line)
			}
			targets = append(targets, m[1])
		}
	}
	if len(targets) == 0 {
		t.Fatalf("polyforge#256: README.md must carry a Documentation list of docs links — the routing page's source of truth — but the section carries no docs link")
	}
	// the list opens with the zh.md pointer, the spec-pinned line
	// `中文导读 (Chinese orientation): docs/zh.md`.
	if targets[0] != "docs/zh.md" {
		t.Fatalf("polyforge#256: README.md's Documentation list must open with the zh.md pointer (`中文导读 (Chinese orientation): docs/zh.md`) — it opens with %q instead:\n%s", targets[0], firstLine)
	}
	if !strings.Contains(firstLine, "中文导读") || !strings.Contains(firstLine, "Chinese orientation") {
		t.Fatalf("polyforge#256: README.md's Documentation list's first line is the Chinese orientation pointer `中文导读 (Chinese orientation): docs/zh.md` — the opening line does not carry the pinned wording:\n%s", firstLine)
	}
	// every routed link also appears in docs/zh.md — entry for entry,
	// the routing page's own path carried by its pointer paragraph.
	zhRaw, err := os.ReadFile(filepath.Join(w256Root(t), "docs", "zh.md"))
	if err != nil {
		t.Fatalf("polyforge#256: docs/zh.md is the deliverable — the Simplified Chinese routing page — and the repo must carry it: %v", err)
	}
	zh := string(zhRaw)
	if strings.TrimSpace(zh) == "" {
		t.Fatalf("polyforge#256: docs/zh.md is empty: the routing page is a document, not a placeholder")
	}
	var missing []string
	for _, target := range targets {
		if !strings.Contains(zh, target) {
			missing = append(missing, target)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("polyforge#256: every docs link in README's Documentation list must also appear in docs/zh.md (the routing page ties README entry for entry; docs/zh.md itself is carried by its own pointer paragraph, no self-exemption) — the routing page never routes: %s", strings.Join(missing, ", "))
	}
}
