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

// The ask response's intent field powers reports-as-answers in the UI: the chart under an
// answer is chosen by the ENGINE's classification, never re-derived client-side.
func TestAskIntentExposed(t *testing.T) {
	for q, want := range map[string]string{
		"where do people drop off?": "funnel",
		"how is retention?":         "retention",
		"total revenue":             "measure",
		"how many signups?":         "signups",
	} {
		if got := string(classifyAsk(q)); got != want {
			t.Errorf("classifyAsk(%q) = %q, want %q", q, got, want)
		}
	}
}

// Geo questions must land on the geo intent (never signups/channels), natural
// time phrases must parse, and a refused window must return NO intent so no UI
// renders a chart under a refusal.
func TestAskGeoAndWindows(t *testing.T) {
	if got := string(classifyAsk("from how many countries did i get viewership in the past week")); got != "geo" {
		t.Errorf("countries question classified as %q, want geo", got)
	}
	if got := string(classifyAsk("where are my visitors from")); got != "channels" {
		t.Errorf("visitors-from stays channels, got %q", got)
	}
	now := time.Now().UTC()
	if _, unsup := parseWindow("how many signups in the past week", now); unsup != "" {
		t.Errorf("'past week' should parse, got unsupported=%q", unsup)
	}
	if w, _ := parseWindow("visitors in the past month", now); !w.scoped() {
		t.Error("'past month' should scope to a rolling 30-day window")
	}
	if _, unsup := parseWindow("signups this quarter", now); unsup == "" {
		t.Error("'quarter' must still be named unsupported")
	}
}
