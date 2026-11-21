// carries: internal/polyforge/daemon/analysis_prompt.md, internal/contracttest/testdata/analysis_estimate_lines.tsv

package test

// pf213_estimate_line_contract_test.go (polyforge#213, AC2): the analysis
// prompt's estimate line and the parser the C1 retest tool (polyforge#209)
// will use are one format, pinned by one sample set. The regex below is
// written from the format string by hand — a full-width colon after 估算,
// full-width 、 between the fields, integer counts, the repository first,
// then 新增, 修改 and 单元 in that order — and the sample file's verdicts
// and expected N,M,K values are the ticket's hand-derived table, never an
// output of any implementation.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pf213EstimateLine is the format the analysis prompt's estimate line must
// use. The line is matched after trimming its surrounding whitespace; the
// line itself is written without a list marker and without backticks.
var pf213EstimateLine = regexp.MustCompile(`^估算：(\S+) 新增 (\d+) 个文件、修改 (\d+) 个文件、单元 (\d+) 个$`)

// TestEstimateLineSamplesTSV runs the shared sample lines through the
// parser the prompt's format string describes and checks every row's
// verdict and captured values against the hand-derived table.
func TestEstimateLineSamplesTSV(t *testing.T) {
	const rel = "internal/contracttest/testdata/analysis_estimate_lines.tsv"
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s must exist — the shared sample set and polyforge#209's only sample source: %v", rel, err)
	}
	okRows, rejectRows := 0, 0
	for i, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("sample line %d must carry 3 tab-separated columns (line, ok|reject, N,M,K or empty), got %d: %q", i+1, len(cols), line)
		}
		text, verdict, want := cols[0], cols[1], cols[2]
		m := pf213EstimateLine.FindStringSubmatch(strings.TrimSpace(text))
		switch verdict {
		case "ok":
			okRows++
			if m == nil {
				t.Errorf("sample line %d %q must be accepted by the estimate-line format, got reject", i+1, text)
				continue
			}
			if got := strings.Join(m[2:], ","); got != want {
				t.Errorf("sample line %d %q: captured N,M,K=%s, want %s", i+1, text, got, want)
			}
		case "reject":
			rejectRows++
			if m != nil {
				t.Errorf("sample line %d %q must be rejected by the estimate-line format, got match %q", i+1, text, m)
			}
			if want != "" {
				t.Errorf("sample line %d is a reject row, its third column must be empty, got %q", i+1, want)
			}
		default:
			t.Fatalf("sample line %d: verdict %q is neither ok nor reject", i+1, verdict)
		}
	}
	if okRows == 0 || rejectRows == 0 {
		t.Fatalf("the shared sample set must hold at least one ok row and one reject row, got ok=%d reject=%d", okRows, rejectRows)
	}
}

// TestEstimateLineTemplateMatchesParser pins the prompt side to the same
// format: the prompt carries exactly one backtick fragment holding the
// estimate-line format, and filling its placeholders must match the
// parser above.
func TestEstimateLineTemplateMatchesParser(t *testing.T) {
	prompt := w68Raw(t, filepath.Join("internal", "polyforge", "daemon", "analysis_prompt.md"))
	var fragments []string
	for rest := prompt; ; {
		i := strings.Index(rest, "`")
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		j := strings.Index(rest, "`")
		if j < 0 {
			break
		}
		fragments = append(fragments, rest[:j])
		rest = rest[j+1:]
	}
	var found []string
	for _, f := range fragments {
		if strings.Contains(f, "估算：") {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the prompt must carry exactly one backtick fragment containing 估算： — the format the analysis writes and the sample file parses; found %d: %q", len(found), found)
	}
	format := found[0]
	for _, repl := range []struct{ old, new string }{
		{"<仓库>", "repo"},
		{"新增 N 个文件", "新增 1 个文件"},
		{"修改 M 个文件", "修改 1 个文件"},
		{"单元 K 个", "单元 1 个"},
	} {
		format = strings.ReplaceAll(format, repl.old, repl.new)
	}
	m := pf213EstimateLine.FindStringSubmatch(format)
	if m == nil {
		t.Fatalf("the prompt's estimate-line format %q, with its placeholders filled, must match the shared parser %q", format, pf213EstimateLine.String())
	}
	if got := strings.Join(m[1:], ","); got != "repo,1,1,1" {
		t.Fatalf("the prompt's format filled with placeholders captured %s, want repo,1,1,1", got)
	}
}
