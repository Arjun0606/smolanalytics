// Package event defines the core analytics event — the single unit everything
// (funnels, retention, trends) is computed from. One event = one thing a user did.
package event

import "time"

// Event is a single user action. DistinctID ties events to one user/visitor;
// Name is the event type (e.g. "signup", "checkout"); Properties carry context
// (plan, source, value...). Timestamp is when it happened (event time, not
// ingest time) so late-arriving events still land in the right place.
type Event struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	DistinctID string         `json:"distinct_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Properties map[string]any `json:"properties,omitempty"`
}
