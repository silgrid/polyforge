package daemon

// analysis_step_tiers.go (polyforge#81 u4, AC4): the pre-submit
// analysis's capability-tier material. The daemon attaches the Host
// [step_tiers] catalog (each tier's ordered candidates) and the ticket's
// own lane step table to the analysis prompt, so the analysis can
// recommend one catalog tier per step with a reason. It is a prompt
// section only: the analysis writes recommendations as text, nothing
// here writes an override or an attrs key. An empty catalog is one
// fallback line (the analysis still runs, never blocked); a lane this
// machine cannot read is named as such — no tier or step is invented.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// analysisStepTiersSection answers the prompt section that carries the
// Host catalog and the ticket's lane step table, or one fallback line
// when neither can be attached. It never fails and never blocks the
// dispatch.
func (d *Daemon) analysisStepTiersSection(item *api.WorkItem) string {
	var b strings.Builder
	b.WriteString("步骤能力档目录与车道 step 表：\n\n")
	catalog := d.rt.StepTiersCatalog()
	if len(catalog) == 0 {
		b.WriteString("Host 未配置 [step_tiers]（目录为空）——无法按档推荐；请在「步骤能力档建议」里为每个 step 写「不设」并说明原因，不要编造档名。\n")
	} else {
		b.WriteString("Host [step_tiers] 目录（每个档一组按优先级排列的 `<harness>:<model>[:<effort>]` 候选）：\n\n")
		tiers := make([]string, 0, len(catalog))
		for tier := range catalog {
			tiers = append(tiers, tier)
		}
		sort.Strings(tiers)
		for _, tier := range tiers {
			fmt.Fprintf(&b, "- %s: %s\n", tier, strings.Join(catalog[tier], ", "))
		}
		b.WriteString("\n`default` 是约定的主力档；在本票的单票覆盖里选它等于「不设」（沿用项目现有链），目录里的 `default` 候选不会被展开。\n")
	}

	bundle := ""
	if item.BundleName != nil {
		bundle = *item.BundleName
	}
	if bundle == "" {
		b.WriteString("\n本票没有声明车道（bundle）——无法列出 step 表；请在「步骤能力档建议」里写明无法推荐档名。\n")
		return b.String()
	}
	lane := d.laneOf(item.ProjectName, bundle)
	if lane == nil || len(lane.Steps) == 0 {
		fmt.Fprintf(&b, "\n本车道 %s 在本机不可读——无法列出 step 表，请在「步骤能力档建议」里写明无法推荐档名（不要编造 step 或档名）。\n", bundle)
		return b.String()
	}
	fmt.Fprintf(&b, "\n本车道 %s（项目 %s）的 step 表：\n\n| step | model_tier | model_candidates |\n|---|---|---|\n", bundle, item.ProjectName)
	for _, s := range lane.Steps {
		cands := ""
		if len(s.ModelCandidates) > 0 {
			cands = strings.Join(s.ModelCandidates, ", ")
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", s.ID, s.ModelTier, cands)
	}
	b.WriteString("\n按每个 step 的判断/机械程度在「步骤能力档建议」里给出建议档名与理由；档名只取上面目录里的名字。\n")
	return b.String()
}
