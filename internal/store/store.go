// Package store is the data layer. The Store interface keeps the analytics engine
// (funnels, retention, trends) independent of the backend: an in-memory store for
// tests and the CLI demo, a DuckDB store for real columnar speed in production —
// same interface, so the engine never changes.
package store

import (
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// Store ingests events and serves them back for a time range. Ingestion is
// idempotent on Event.ID so a retried request never double-counts.
type Store interface {
	// Ingest records events. Events with an ID already seen are ignored.
	Ingest(events ...event.Event) error
	// Range returns every event with Timestamp in [from, to). Either bound may be
	// zero to mean unbounded. Events are returned for the engine to compute over.
	Range(from, to time.Time) ([]event.Event, error)
	// Names returns the distinct event names seen (for auto-building funnels/UI).
	Names() ([]string, error)
}
