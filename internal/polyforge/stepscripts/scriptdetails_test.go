package stepscripts

// scriptdetails_test.go: the result files of the workflow scripts that took
// over pf-facts and pf-integrate (workflows/_shared/scripts/facts.py and
// integrate.py,) as these black-box tests read them — their
// command names and kinds, and their details' shapes.

// The scripts' result file command names (each script's file name: their
// readers check them).
const (
	NameFacts     = "facts.py"
	NameIntegrate = "integrate.py"
)

// facts.py's kinds.
const (
	FactsPlanConformance = "plan-conformance"
	FactsBuild           = "build"
	FactsCarries         = "carries"
	FactsMicroBounds     = "micro-bounds"
)

// factsUnitDecl is one unit of artifacts/build/units.json.
type factsUnitDecl struct {
	ID         string        `json:"id"`
	TestCommit string        `json:"test_commit"`
	ImplCommit string        `json:"impl_commit"`
	Run        *factsUnitRun `json:"run,omitempty"`
	Marker     string        `json:"marker,omitempty"`
}

// factsUnitRun is a unit's run: the repo, the cwd inside it and the argv.
type factsUnitRun struct {
	Repo string   `json:"repo"`
	Cwd  string   `json:"cwd,omitempty"`
	Argv []string `json:"argv"`
}

// planConformanceDetails is `facts.py plan-conformance`'s details.
type planConformanceDetails struct {
	Plan                 string          `json:"plan"`
	UnitsFile            string          `json:"units_file,omitempty"`
	Unavailable          string          `json:"unavailable,omitempty"`
	Obligations          []declaredWrite `json:"declared_writes"`
	Undeclared           []string        `json:"undeclared"`
	UnchangedObligations []string        `json:"unchanged_obligations"`
	UnchangedWrites      []string        `json:"unchanged_writes"`
	Released             []string        `json:"released"`
}

type declaredWrite struct {
	Path        string `json:"path"`
	Obligations []struct {
		Path string `json:"path"`
		Note string `json:"note,omitempty"`
	} `json:"obligations,omitempty"`
	NoneReason string `json:"none_reason,omitempty"`
}

// buildDetails is `facts.py build`'s details.
type buildDetails struct {
	UnitsFile string `json:"units_file"`
	Replay    *struct {
		UnitTimeout string `json:"unit_timeout"`
		Deadline    string `json:"deadline"`
	} `json:"replay,omitempty"`
	Units        []buildUnit    `json:"units"`
	TestWeakened []weakenedFact `json:"test_weakened"`
}

type buildUnit struct {
	ID          string        `json:"id"`
	Repo        string        `json:"repo,omitempty"`
	TestCommit  string        `json:"test_commit,omitempty"`
	ImplCommit  string        `json:"impl_commit,omitempty"`
	TestFiles   []string      `json:"test_files,omitempty"`
	Why         string        `json:"unresolved,omitempty"`
	Commits     string        `json:"commits,omitempty"`
	Run         *factsUnitRun `json:"run,omitempty"`
	Replay      *buildReplay  `json:"replay,omitempty"`
	ReplayError string        `json:"replay_error,omitempty"`
}

type buildReplay struct {
	Exit       int    `json:"exit_code"`
	Red        bool   `json:"red"`
	TimedOut   bool   `json:"timed_out"`
	NotStarted bool   `json:"not_started,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Log        string `json:"log,omitempty"`
	Image      string `json:"image,omitempty"`
	Container  string `json:"container,omitempty"`
	Host       bool   `json:"host,omitempty"`
	Worktree   string `json:"worktree"`
	Cwd        string `json:"cwd"`
	Line       string `json:"line"`
	OutputTail string `json:"output_tail,omitempty"`
}

type weakenedFact struct {
	Unit   string `json:"unit"`
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Hunk   string `json:"hunk,omitempty"`
}

// carriesDetails is `facts.py carries`'s details.
type carriesDetails struct {
	UnitsFile string `json:"units_file,omitempty"`
	Repos     []struct {
		Name     string   `json:"name"`
		UnitRepo bool     `json:"unit_repo"`
		Problems []string `json:"problems"`
	} `json:"repos"`
	Problems []string `json:"problems"`
}

// microBoundsDetails is `facts.py micro-bounds`'s details.
type microBoundsDetails struct {
	Plan     string `json:"plan"`
	InBounds bool   `json:"in_bounds"`
	Reason   string `json:"reason,omitempty"`
	Repos    []struct {
		Name          string   `json:"name"`
		Base          string   `json:"base"`
		ChangedPaths  []string `json:"changed_paths"`
		DeclaredPaths []string `json:"declared_paths"`
		Commands      []struct {
			Repo    string `json:"repo"`
			Command string `json:"command"`
		} `json:"commands"`
	} `json:"repos"`
}

// collectDetails is `integrate.py collect`'s details (batch.json).
type collectDetails struct {
	Batch    string                 `json:"batch"`
	Heads    map[string]string      `json:"heads"`
	Repos    map[string]*pinnedHead `json:"repos"`
	Gated    []string               `json:"gated"`
	Items    []collectItem          `json:"items"`
	Skipped  []skippedItem          `json:"skipped"`
	Holds    []holdDetail           `json:"holds"`
	RedHeads map[string]string      `json:"red_heads"`
	Refused  string                 `json:"refused,omitempty"`
	Empty    string                 `json:"empty,omitempty"`
	TakenAt  string                 `json:"taken_at"`
}

type pinnedHead struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Green  string `json:"green,omitempty"`
	Skip   string `json:"skip,omitempty"`
}

// landingRecord is the landing record merge.py --landing-out writes (the
// `integration` wrap attr the merge step registers): its state, one entry
// per merge (a repo merged more than once has more than one), the merge
// commits by repo, oldest first, and the owner's waivers on the delivery
// record — and nothing else.
type landingRecord struct {
	State        string              `json:"state"`
	Repos        []landingRepo       `json:"repos"`
	MergeCommits map[string][]string `json:"merge_commits,omitempty"`
	Waived       []landingWaiver     `json:"waived,omitempty"`
}

// landingRepo is one merge of the landing record: its repo, its PR, the
// head it merged, the light-verified head (absent: none verified), the
// PR's merge commit, and whether the owner accepted a merge made outside
// the merge step as the landing.
type landingRepo struct {
	Repo              string `json:"repo"`
	PR                string `json:"pr"`
	Head              string `json:"head"`
	LightVerifiedHead string `json:"light_verified_head,omitempty"`
	MergeCommit       string `json:"merge_commit,omitempty"`
	Accepted          bool   `json:"accepted,omitempty"`
}

// landingWaiver is one owner waiver on the landing record: the gate whose
// FAIL verdict was waived and the owner's answer verbatim.
type landingWaiver struct {
	Gate   string `json:"gate"`
	Ruling string `json:"ruling"`
}

type collectItem struct {
	ID           string              `json:"id"`
	Slug         string              `json:"slug,omitempty"`
	Repos        []landingRepo       `json:"repos"`
	MergeCommits map[string][]string `json:"merge_commits"`
	Waived       bool                `json:"waived,omitempty"`
	RedRounds    int                 `json:"red_rounds,omitempty"`
	FixTickets   []string            `json:"fix_tickets,omitempty"`
}

type skippedItem struct {
	WI   string `json:"wi"`
	Name string `json:"name,omitempty"`
	Why  string `json:"why"`
}

type holdDetail struct {
	WI           string              `json:"wi"`
	Name         string              `json:"name,omitempty"`
	Stuck        bool                `json:"stuck"`
	Reason       string              `json:"reason"`
	RedRounds    int                 `json:"red_rounds"`
	FixTickets   []string            `json:"fix_tickets"`
	PRs          []string            `json:"prs,omitempty"`
	MergeCommits map[string][]string `json:"merge_commits,omitempty"`
	LandedFixes  []struct {
		Ref          string              `json:"ref"`
		MergeCommits map[string][]string `json:"merge_commits"`
	} `json:"landed_fixes,omitempty"`
}

// closeDetails is `integrate.py close`'s details.
type closeDetails struct {
	Moves []struct {
		WI   string         `json:"wi"`
		Name string         `json:"name,omitempty"`
		Set  map[string]any `json:"set"`
	} `json:"moves"`
	IntegrationBatch map[string]any `json:"integration_batch"`
}
