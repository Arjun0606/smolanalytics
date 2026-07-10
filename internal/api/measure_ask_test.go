package api

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func checkoutWithAmount(amount float64) event.Event {
	return event.Event{
		Name:       "checkout",
		DistinctID: "u",
		Timestamp:  time.Now().UTC().Add(-time.Hour),
		Properties: map[string]any{"amount": amount},
	}
}

// The ask bar must answer the money questions from real values — and REFUSE to invent one
// when the property was never sent. Both halves are the product's trust promise.
func TestAskMeasure_ComputesRevenueAndAOV(t *testing.T) {
	now := time.Now().UTC()
	evs := []event.Event{checkoutWithAmount(20), checkoutWithAmount(30), checkoutWithAmount(40)} // sum 90, avg 30

	if a := answer("total revenue", evs, now); !strings.Contains(a, "90") {
		t.Errorf("'total revenue' should sum amounts to 90, got: %q", a)
	}
	if a := answer("average order value", evs, now); !strings.Contains(a, "30") {
		t.Errorf("'average order value' should be 30, got: %q", a)
	}
	if a := answer("what's the max amount", evs, now); !strings.Contains(a, "40") {
		t.Errorf("'max amount' should be 40, got: %q", a)
	}
}

func TestAskMeasure_RefusesToInventWhenNotTracked(t *testing.T) {
	now := time.Now().UTC()
	// signups only, no numeric property anywhere
	evs := []event.Event{{Name: "signup", DistinctID: "u", Timestamp: now.Add(-time.Hour), Properties: map[string]any{"path": "/"}}}

	a := answer("total revenue", evs, now)
	if !strings.Contains(a, "numeric") || !strings.Contains(a, "track(") {
		t.Errorf("with no numeric property, revenue must be an honest refusal that guides sending one, got: %q", a)
	}
	// it must NOT fabricate a revenue figure
	if strings.Contains(a, "Total amount") {
		t.Errorf("must not report a computed total when nothing was tracked, got: %q", a)
	}
}

func TestAskMeasure_ReceiptPresent(t *testing.T) {
	now := time.Now().UTC()
	if cb := computedBy("total revenue", now); !strings.Contains(cb, "numeric-aggregation") {
		t.Errorf("measure answer should carry a numeric-aggregation receipt, got: %q", cb)
	}
}
