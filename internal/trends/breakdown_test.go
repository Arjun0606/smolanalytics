package trends

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func evp(user, name, source string, day int) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: base.AddDate(0, 0, day),
		Properties: map[string]any{"source": source}}
}

func TestComputeBreakdown(t *testing.T) {
	evs := []event.Event{
		evp("a", "signup", "google", 0), evp("b", "signup", "google", 0),
		evp("c", "signup", "twitter", 1),
		{DistinctID: "d", Name: "signup", Timestamp: base}, // no source -> (none)
		evp("e", "other", "google", 0),                     // wrong event, ignored
	}
	series := ComputeBreakdown(evs, "signup", "source", time.Time{}, time.Time{}, false)
	if len(series) != 3 {
		t.Fatalf("want 3 series, got %d", len(series))
	}
	// sorted by total desc: google(2) first
	if series[0].Value != "google" || series[0].Total != 2 {
		t.Fatalf("top series = %s/%d, want google/2", series[0].Value, series[0].Total)
	}
	var none bool
	for _, s := range series {
		if s.Value == "(none)" && s.Total == 1 {
			none = true
		}
	}
	if !none {
		t.Fatalf("missing (none) series for the property-less event")
	}
}
