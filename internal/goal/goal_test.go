package goal

import (
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func TestResolveGoalWithAttribution(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	mk := func(id, name, ref, utm string, off time.Duration, props map[string]any) event.Event {
		p := map[string]any{}
		for k, v := range props {
			p[k] = v
		}
		if ref != "" {
			p["referrer"] = ref
		}
		if utm != "" {
			p["utm_source"] = utm
		}
		return event.Event{ID: id + name, Name: name, DistinctID: id, Timestamp: now.Add(off), Properties: p}
	}
	evs := []event.Event{
		// u1: arrived from HN, signed up
		mk("u1", "$pageview", "https://news.ycombinator.com/item", "", -2*time.Hour, map[string]any{"path": "/"}),
		mk("u1", "signup", "", "", -1*time.Hour, nil),
		// u2: arrived via twitter utm, hit /thanks
		mk("u2", "$pageview", "https://t.co/x", "twitter", -3*time.Hour, map[string]any{"path": "/"}),
		mk("u2", "$pageview", "", "", -2*time.Hour, map[string]any{"path": "/thanks"}),
		// u3: direct, no conversion
		mk("u3", "$pageview", "", "", -1*time.Hour, map[string]any{"path": "/pricing"}),
		// u4: converted but OUTSIDE the window — must not count
		mk("u4", "signup", "", "", -40*24*time.Hour, nil),
	}

	r := Resolve(evs, Definition{Name: "Signed up", Kind: "event", Value: "signup"}, 30, now)
	if r.Conversions != 1 || r.Visitors != 3 || r.ConversionPct != 33 {
		t.Fatalf("event goal: %+v", r)
	}
	if len(r.ByReferrer) != 1 || r.ByReferrer[0].Value != "news.ycombinator.com" {
		t.Fatalf("attribution should credit HN: %+v", r.ByReferrer)
	}

	r = Resolve(evs, Definition{Name: "Thanks page", Kind: "path", Value: "/thanks*"}, 30, now)
	if r.Conversions != 1 {
		t.Fatalf("path goal should match /thanks: %+v", r)
	}
	if len(r.ByUTMSource) != 1 || r.ByUTMSource[0].Value != "twitter" {
		t.Fatalf("utm attribution: %+v", r.ByUTMSource)
	}
}

func TestGoalStoreValidation(t *testing.T) {
	s, _ := Open("")
	if _, err := s.Save(Definition{Name: "x", Kind: "regex", Value: "y"}); err == nil {
		t.Fatal("bad kind must be rejected")
	}
	if _, err := s.Save(Definition{Name: "", Kind: "event", Value: "y"}); err == nil {
		t.Fatal("empty name must be rejected")
	}
	d, err := s.Save(Definition{Name: "ok", Kind: "event", Value: "signup"})
	if err != nil || d.ID == "" {
		t.Fatalf("valid goal: %v", err)
	}
	if err := s.Delete(d.ID); err != nil {
		t.Fatal(err)
	}
}
