package modelhealth

// alerts.go: the edge-triggered episode state. The tracker owns the map;
// Build only answers the current warnings and the restart seed.

// evaluateLocked applies the current alerts against the armed episode map.
// It arms an episode before returning it for emission (so observing our own
// model_unhealthy append is a no-op), and disarms episodes that are no
// longer unhealthy (the re-arm the spec requires).
func evaluateLocked(alerted map[string]bool, alerts []Alert) []Alert {
	current := make(map[string]bool, len(alerts))
	var emit []Alert
	for _, a := range alerts {
		k := alertKey(a.Model, a.Effort)
		current[k] = true
		if !alerted[k] {
			alerted[k] = true
			emit = append(emit, a)
		}
	}
	for k := range alerted {
		if !current[k] {
			delete(alerted, k)
		}
	}
	return emit
}
