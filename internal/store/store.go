// Package store is the data layer. The Store interface keeps the analytics engine
// (funnels, retention, trends) independent of the backend: an in-memory store for
// tests and the CLI demo, the durable file log for a single box, and the tiered
// segment store (columnar segments on object storage) for scale — same interface, so
// the engine never changes. See gtm/SCALE.md for the architecture.
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
	// Scan streams every event in [from, to) through fn, in storage order. Unlike
	// Range it never materializes the full set, so a columnar/object-store backend
	// keeps memory bounded regardless of total volume. fn returning an error stops the
	// scan and is returned. This is the query path the scale tiers are built around.
	Scan(from, to time.Time, fn func(event.Event) error) error
	// Names returns the distinct event names seen (for auto-building funnels/UI).
	Names() ([]string, error)
	// Clear deletes all events (the settings "danger zone" reset).
	Clear() error
	// Prune deletes events with Timestamp before the cutoff (retention policy),
	// returning how many were removed.
	Prune(before time.Time) (int, error)
	// DeleteUser erases every event belonging to a distinct_id (GDPR right to
	// erasure), returning how many events were removed. Irreversible.
	DeleteUser(distinctID string) (int, error)
}
