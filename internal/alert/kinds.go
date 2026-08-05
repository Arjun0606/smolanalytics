package alert

// Alert shapes.
//
// There was one: a raw event count against a number you typed in. That shape has a problem nobody
// names — it asks you to already know your own numbers. "Alert me if signups drop below 40" needs
// you to know that 40 is low, and to keep knowing it as the product grows, or the alert silently
// stops meaning anything. The fixed threshold is why most alerts are either never set up or muted
// within a month.
//
// The other three ask nothing of you. A relative alert knows what normal was because it looks at
// the prior window. An anomaly alert uses the detector that has been sitting in internal/insight
// this whole time, wired to nothing — the dashboard could tell you "signups dropped 40% in the
// last 24h" and there was no way to be TOLD that without opening the page. And a ratio alert
// watches a derived metric, which is where most real health lives: not "how many errors" but
// "what share of sessions hit one".
const (
	// KindCount is the original: events in a window against a fixed threshold. Default when a
	// stored alert has no kind, so every alert that already exists keeps behaving identically.
	KindCount = "count"
	// KindRelative compares the window against the SAME-LENGTH window before it. "Tell me if
	// signups fall 30%" needs no knowledge of what signups usually are.
	KindRelative = "relative"
	// KindAnomaly defers entirely to the anomaly detector: no event, no threshold, no direction.
	// "Tell me when something is unusual."
	KindAnomaly = "anomaly"
	// KindRatio watches one event over another — the share, not the count.
	KindRatio = "ratio"
)

// Kind returns the alert's shape, defaulting to the original.
//
// A stored alert written before kinds existed has an empty string here, and it MUST mean count.
// Treating it as anything else would silently change what a live alert watches, which is the one
// thing an alert must never do.
func (a Alert) Kind() string {
	if a.KindName == "" {
		return KindCount
	}
	return a.KindName
}

// Describe renders the rule in words. Alerts are read months after they are written, usually by
// someone deciding whether a page at 3am was worth it, and "gt 40" is not a rule anyone can judge.
func (a Alert) Describe() string {
	w := a.WindowHours
	if w <= 0 {
		w = 24
	}
	switch a.Kind() {
	case KindRelative:
		dir := "falls"
		if a.Op == "gt" {
			dir = "rises"
		}
		return a.Event + " " + dir + " more than " + trimNum(a.Threshold) + "% against the previous " +
			hours(w) + ", comparing like for like rather than against a number typed in months ago"
	case KindAnomaly:
		return "anything moves unusually far from its own trailing baseline — no event and no " +
			"threshold to keep up to date"
	case KindRatio:
		verb := "above"
		if a.Op == "lt" {
			verb = "below"
		}
		return a.Event + " per " + a.Against + " is " + verb + " " + trimNum(a.Threshold) +
			"% over " + hours(w)
	default:
		verb := "above"
		if a.Op == "lt" {
			verb = "below"
		}
		return a.Event + " count is " + verb + " " + trimNum(a.Threshold) + " over " + hours(w)
	}
}

func hours(h int) string {
	if h == 24 {
		return "24 hours"
	}
	if h == 1 {
		return "hour"
	}
	return trimNum(float64(h)) + " hours"
}

// trimNum prints a float without a trailing .0, because "drops more than 30.000000%" reads as
// generated rather than written.
func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return itoa(int(f))
	}
	s := ""
	whole := int(f)
	frac := int((f-float64(whole))*100 + 0.5)
	s = itoa(whole) + "." + itoa(frac)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
