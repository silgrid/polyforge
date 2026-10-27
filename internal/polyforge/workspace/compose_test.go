package workspace

// compose_test.go: the precedence chain (project step id > bundle step >
// project default > bundle default), table-driven. Fixture model ids are
// placeholders.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

func bundleWith(defaults []string, steps []workflow.FrontmatterStep) *workflow.Frontmatter {
	fm := &workflow.Frontmatter{Steps: steps}
	if defaults != nil {
		fm.Defaults = &workflow.Defaults{ModelCandidates: defaults}
	}
	return fm
}

func models(project string, entry map[string][]string) *Config {
	return &Config{Models: map[string]map[string][]string{project: entry}}
}

var testBundle = func() *workflow.Frontmatter {
	return bundleWith([]string{"prov/weak-a"}, []workflow.FrontmatterStep{
		{ID: "grill"},
		{ID: "fix", ModelCandidates: []string{"prov/flash"}},
		{ID: "release"},
		{ID: "verification"},
	})
}

// A project step-id list wholly replaces that step's resolution (a
// bundle step-level list included); other steps keep bundle resolution.
func TestComposeProjectStepListReplacesThatStep(t *testing.T) {
	got := ComposeCandidates(testBundle(), models("p", map[string][]string{"fix": {"y/c1", "y/c2"}, "release": {"y/r"}}), "p", nil)
	want := map[string][]string{
		"grill":        {"prov/weak-a"},
		"fix":          {"y/c1", "y/c2"},
		"release":      {"y/r"},
		"verification": {"prov/weak-a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compose = %v, want %v", got, want)
	}
}

// The project default governs every step lacking both a step-id list
// and a step-level list, beating the bundle's defaults; a bundle step's
// own list beats the project default (polyforge#164 H10); a step-id list
// beats both.
func TestComposeProjectDefaultAndStepPrecedence(t *testing.T) {
	got := ComposeCandidates(testBundle(), models("p", map[string][]string{"default": {"y/d"}, "verification": {"y/g"}}), "p", nil)
	want := map[string][]string{
		"grill":        {"y/d"},
		"fix":          {"prov/flash"},
		"release":      {"y/d"},
		"verification": {"y/g"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compose = %v, want %v", got, want)
	}
}

// A declared-empty list is present and governs: no fall-through.
func TestComposePresentEmptyGoverns(t *testing.T) {
	got := ComposeCandidates(testBundle(), models("p", map[string][]string{"default": {"y/d"}, "fix": {}}), "p", nil)
	if l, ok := got["fix"]; !ok || l == nil || len(l) != 0 {
		t.Fatalf("fix = %#v, want present-empty", got["fix"])
	}
	if !reflect.DeepEqual(got["grill"], []string{"y/d"}) {
		t.Fatalf("grill = %v", got["grill"])
	}
	got = ComposeCandidates(testBundle(), models("p", map[string][]string{"default": {}}), "p", nil)
	for id, l := range got {
		if id == "fix" {
			if !reflect.DeepEqual(l, []string{"prov/flash"}) {
				t.Fatalf("fix = %v, want its own step list (a step list outranks the project default)", l)
			}
			continue
		}
		if len(l) != 0 {
			t.Fatalf("%s = %v, want empty (default [] governs)", id, l)
		}
	}
	if len(got) != 4 {
		t.Fatalf("present-empty entries must exist for every step: %v", got)
	}
}

// A project the yaml does not list, a nil config, and a nil bundle.
func TestComposeUnlistedNilAndBundleOnly(t *testing.T) {
	bundleOnly := map[string][]string{
		"grill":        {"prov/weak-a"},
		"fix":          {"prov/flash"},
		"release":      {"prov/weak-a"},
		"verification": {"prov/weak-a"},
	}
	cfg := models("p", map[string][]string{"default": {"y/d"}})
	if got := ComposeCandidates(testBundle(), cfg, "other", nil); !reflect.DeepEqual(got, bundleOnly) {
		t.Fatalf("unlisted project = %v, want bundle-only %v", got, bundleOnly)
	}
	if got := ComposeCandidates(testBundle(), nil, "p", nil); !reflect.DeepEqual(got, bundleOnly) {
		t.Fatalf("nil config = %v, want bundle-only", got)
	}
	if got := ComposeCandidates(nil, cfg, "p", nil); got != nil {
		t.Fatalf("nil bundle = %v, want nil", got)
	}
	if got := ComposeCandidates(bundleWith(nil, []workflow.FrontmatterStep{{ID: "a"}}), nil, "p", nil); got != nil {
		t.Fatalf("no list anywhere = %v, want nil", got)
	}
}

// Two projects in one yaml compose independently.
func TestComposeOtherProjectUnaffected(t *testing.T) {
	cfg := &Config{Models: map[string]map[string][]string{
		"a": {"fix": {"y/a"}},
		"b": {"fix": {"y/b"}},
	}}
	if got := ComposeCandidates(testBundle(), cfg, "a", nil); !reflect.DeepEqual(got["fix"], []string{"y/a"}) {
		t.Fatalf("a fix = %v", got["fix"])
	}
	if got := ComposeCandidates(testBundle(), cfg, "b", nil); !reflect.DeepEqual(got["fix"], []string{"y/b"}) {
		t.Fatalf("b fix = %v", got["fix"])
	}
}

func TestComposeManualBeatsEveryOtherLevel(t *testing.T) {
	manual := []string{"man/a", "man/b"}
	cfg := models("p", map[string][]string{"default": {"y/d"}, "fix": {"y/c"}})
	for _, c := range []*Config{cfg, nil} {
		got := ComposeCandidates(testBundle(), c, "p", manual)
		for _, step := range []string{"grill", "fix", "release"} {
			if !reflect.DeepEqual(got[step], manual) {
				t.Fatalf("step %s = %v, want the manual list over yaml step/default and bundle", step, got[step])
			}
		}
	}
	// a present-empty yaml level does not beat the manual list either
	if got := ComposeCandidates(testBundle(), models("p", map[string][]string{"default": {}}), "p", manual); !reflect.DeepEqual(got["grill"], manual) {
		t.Fatalf("manual must beat a present-empty yaml level, got %v", got["grill"])
	}
	// no manual list: unchanged
	if got := ComposeCandidates(testBundle(), cfg, "p", nil); !reflect.DeepEqual(got["fix"], []string{"y/c"}) {
		t.Fatalf("without a manual list the yaml chain governs, got %v", got["fix"])
	}
}

func TestNormalizeModels(t *testing.T) {
	if got, err := NormalizeModels([]string{" pi:a/x ", "claude: claude-opus-5-5"}); err != nil || !reflect.DeepEqual(got, []string{"pi:a/x", "claude:claude-opus-5-5"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	// polyforge#176: the same model at two efforts is two candidates
	if got, err := NormalizeModels([]string{"pi:a/x:low", "pi:a/x : high", "pi:a/x"}); err != nil || !reflect.DeepEqual(got, []string{"pi:a/x:low", "pi:a/x:high", "pi:a/x"}) {
		t.Fatalf("effort chain got %v, %v", got, err)
	}
	for name, c := range map[string]struct {
		in   []string
		want string
	}{
		"empty list":     {nil, "models list is empty"},
		"empty id":       {[]string{"pi:a", ""}, "empty candidate"},
		"blank id":       {[]string{"pi:a", "  "}, "empty candidate"},
		"duplicate":      {[]string{"pi:a", "pi:b", " pi: a"}, `repeats "pi:a"`},
		"no prefix":      {[]string{"example-gateway/model-a"}, `"example-gateway/model-a" needs a harness prefix (pi:… or claude:…)`},
		"unknown prefix": {[]string{"codex:gpt-6"}, `unknown harness prefix "codex"`},
		"empty model":    {[]string{"claude:"}, "names no model"},
		"claude effort":  {[]string{"pi:a:off", "claude:m:off"}, `effort "off" is not accepted by claude`},
		"effort dup":     {[]string{"pi:a:high", "pi: a : high"}, `repeats "pi:a:high"`},
	} {
		if _, err := NormalizeModels(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err %v, want %q", name, err, c.want)
		}
	}
}

// ResolveSteps names each step's level; the harness of each candidate is
// its prefix (polyforge#175), a list-less step runs on pi.
func TestResolveStepsHarnessPerCandidate(t *testing.T) {
	fm := bundleWith([]string{"pi:g/glm"}, []workflow.FrontmatterStep{{ID: "fix"}, {ID: "review"}, {ID: "spec", ModelCandidates: []string{}}})
	cfg := models("p", map[string][]string{"review": {"claude:claude-opus-5-5", "pi:g/glm"}})
	rs := ResolveSteps("bug-fix", fm, cfg, "p", nil)
	if len(rs) != 3 {
		t.Fatalf("resolutions %v", rs)
	}
	hs := func(r StepResolution) []string {
		t.Helper()
		h, err := r.Harnesses()
		if err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
		return h
	}
	fix, rev, spec := rs[0], rs[1], rs[2]
	if fix.ModelsFrom != "bundle bug-fix defaults.model_candidates" || !reflect.DeepEqual(hs(fix), []string{"pi"}) {
		t.Fatalf("fix %+v", fix)
	}
	if rev.ModelsFrom != "steps.p.review.models" || !reflect.DeepEqual(hs(rev), []string{"claude", "pi"}) {
		t.Fatalf("review %+v", rev)
	}
	if !reflect.DeepEqual(hs(spec), []string{"pi"}) {
		t.Fatalf("an empty list runs on pi's default model: %+v", spec)
	}
	rs = ResolveSteps("bug-fix", fm, cfg, "p", []string{"claude:claude-sonnet-5"})
	if rs[0].ModelsFrom != "pf submit --models" || !reflect.DeepEqual(hs(rs[0]), []string{"claude"}) {
		t.Fatalf("manual %+v", rs[0])
	}
	// an unparseable candidate (unreachable past the loaders) is an error,
	// never guessed onto pi
	bad := StepResolution{Project: "p", Bundle: "b", Step: "s", Models: []string{"pi:a", "x/y"}, ModelsFrom: "test"}
	if _, err := bad.Harnesses(); err == nil || !strings.Contains(err.Error(), `p/b/s: candidate 1 (test): "x/y" needs a harness prefix`) {
		t.Fatalf("unparseable candidate: %v", err)
	}
}

// The summarize pick (polyforge#224, polyforge#242): the root
// summarize.models key, one list for a claim of ANY project — present
// answers the parsed list, a declared-empty list answers present-empty,
// an absent key (or no yaml at all) answers nil (the lane is silently
// off). Never a precedence level: the pick ignores the step chain and
// the per-project entries entirely.
func TestSummarizeModelsPick(t *testing.T) {
	present := models("p", map[string][]string{"fix": {"y/c"}})
	present.Summarize = []string{"pi:s1", "claude:s2"}
	if got := SummarizeModels(present); !reflect.DeepEqual(got, []string{"pi:s1", "claude:s2"}) {
		t.Fatalf("present key: %v", got)
	}
	if got := SummarizeModels(nil); got != nil {
		t.Fatalf("no yaml at all: %v", got)
	}
	// a declared-empty list is present and non-nil (present-empty
	// governs; an empty candidate list dispatches nothing — the lane off)
	empty := &Config{Summarize: []string{}}
	if got := SummarizeModels(empty); got == nil || len(got) != 0 {
		t.Fatalf("declared-empty key: %#v, want present-empty", got)
	}
	// a yaml without the key answers nil even beside step lists
	absent := models("p", map[string][]string{"fix": {"y/c"}, DefaultKey: {"y/d"}})
	if got := SummarizeModels(absent); got != nil {
		t.Fatalf("absent key: %v", got)
	}
}

// The summarize language pick (polyforge#243): the declared root
// summarize.language, or 中文 when the key or the whole yaml is absent.
func TestSummarizeLanguagePick(t *testing.T) {
	if got := SummarizeLanguage(&Config{Summarize: []string{"pi:s"}, SummarizeLanguage: "English"}); got != "English" {
		t.Fatalf("declared language: %q", got)
	}
	if got := SummarizeLanguage(&Config{Summarize: []string{"pi:s"}}); got != "中文" {
		t.Fatalf("absent language: %q, want 中文", got)
	}
	if got := SummarizeLanguage(nil); got != "中文" {
		t.Fatalf("no yaml at all: %q, want 中文", got)
	}
}

// The summarize interval pick (polyforge#250): the declared root
// summarize.interval_sec, or 60 when the key or the whole yaml is absent.
func TestSummarizeIntervalSecPick(t *testing.T) {
	if got := SummarizeIntervalSec(&Config{Summarize: []string{"pi:s"}, SummarizeIntervalSec: 10}); got != 10 {
		t.Fatalf("declared interval: %d", got)
	}
	if got := SummarizeIntervalSec(&Config{Summarize: []string{"pi:s"}}); got != 60 {
		t.Fatalf("absent interval: %d, want 60", got)
	}
	if got := SummarizeIntervalSec(nil); got != 60 {
		t.Fatalf("no yaml at all: %d, want 60", got)
	}
}
