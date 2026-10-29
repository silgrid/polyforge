package observe

// view.go: the monitor's owner-facing projections for the status and
// watch faces (activity, live progress).

import "time"

// Activity is one work item's owner-facing liveness reading (the ops
// status face): when its current dispatch began and when its harness last
// did REAL work (UI chatter never counts — the F15 ruling carried into
// the translator). Zero values = no dispatch in flight (between steps,
// waiting on a human, or not yet started).
type Activity struct {
	Step          string
	SpawnN        int64
	StepStartedAt time.Time
	LastWorkAt    time.Time // zero = no work-class signal yet this dispatch
	InDispatch    bool
	// Candidate is the in-flight dispatch's model candidate,
	// `<harness>:<model>[:<effort>]` (polyforge#176: the ops status
	// face shows the effort it runs at); "" = the harness default model.
	Candidate string
	// WaitCalls / WaitSeconds are the current step's wait calls (bash
	// calls that only `sleep N`, N > 3s) and their summed seconds, over
	// every dispatch of the step this daemon watched: counted and
	// surfaced, never judged.
	WaitCalls   int
	WaitSeconds int64
}

// Activities answers the activity of every monitored work item:
// canonical work-item id -> Activity (the ops status face renders the
// step age and the last-work-signal age; the pilot's 2.7-hour wedge had
// NO owner-visible surface while it ran).
func (o *Observe) Activities() map[string]Activity {
	o.mu.Lock()
	monitors := make([]*Monitor, 0, len(o.monitors))
	for _, m := range o.monitors {
		monitors = append(monitors, m)
	}
	o.mu.Unlock()
	out := map[string]Activity{}
	for _, m := range monitors {
		out[m.wi.WorkItem] = m.activity()
	}
	return out
}

// LiveProgress is one work item's live progress inside its current
// dispatch (the watch face): the command the harness is running right now
// and how long it has run, the files it changed lately, and how many tools
// the dispatch has used. The zero value (InDispatch false) means no
// dispatch is in flight (between steps, waiting on a human).
type LiveProgress struct {
	InDispatch bool
	Step       string
	// Command is the newest running tool's command line (the bash command,
	// or the tool name plus its key argument); "" when no tool is running.
	Command       string
	CommandAgeSec int64
	// RecentFiles are the last few paths edit/write tools touched, oldest
	// first (at most five).
	RecentFiles []string
	ToolCalls   int
	// WaitCalls / WaitSeconds: the step's wait calls, as on Activity.
	WaitCalls   int
	WaitSeconds int64
}

// Progress answers the live progress of one work item's current dispatch
// by its canonical id; ok=false when the work item is not monitored here.
func (o *Observe) Progress(wi string) (LiveProgress, bool) {
	m := o.lookup(wi)
	if m == nil {
		return LiveProgress{}, false
	}
	return m.liveProgress(), true
}
