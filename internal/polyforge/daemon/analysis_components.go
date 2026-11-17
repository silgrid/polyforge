package daemon

// analysis_components.go (polyforge#89, AC3): the component division the
// pre-filing analysis runs on. This commit lands the timeout's
// declaration only, with a zero-value section, so the unit's test is red
// on its assertions, never on a missing symbol.

import "time"

// analysisComponentsTimeout bounds one components.py run: the section
// is built at dispatch time, inside the analysis dispatch walk's lock,
// so a stuck tool must never hold it.
var analysisComponentsTimeout = 10 * time.Second
