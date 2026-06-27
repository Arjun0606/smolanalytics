package cohort

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/query"
)

func ev(user, name, source string) event.Event {
	return event.Event{DistinctID: user, Name: name, Timestamp: time.Now().UTC(),
		Properties: map[string]any{"source": source}}
}

func TestResolveAnyAll(t *testing.T) {
	evs := []event.Event{
		ev("a", "signup", "google"), ev("a", "checkout", "google"),
		ev("b", "signup", "twitter"),
		ev("c", "checkout", "google"),
	}
	// any: did signup OR checkout -> a,b,c
	any := Resolve(evs, Definition{Match: "any", Events: []string{"signup", "checkout"}})
	if len(any) != 3 {
		t.Fatalf("any = %d, want 3", len(any))
	}
	// all: did signup AND checkout -> only a
	all := Resolve(evs, Definition{Match: "all", Events: []string{"signup", "checkout"}})
	if len(all) != 1 || !all["a"] {
		t.Fatalf("all = %v, want {a}", all)
	}
	// with filter source=google: checkout from google -> a,c
	g := Resolve(evs, Definition{Match: "any", Events: []string{"checkout"},
		Filters: []query.Filter{{Property: "source", Op: query.Eq, Value: "google"}}})
	if len(g) != 2 || !g["a"] || !g["c"] {
		t.Fatalf("google checkout = %v, want {a,c}", g)
	}
}

func TestStorePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cohorts.json")
	s, _ := Open(path)
	d, err := s.Save(Definition{Name: "Customers", Events: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || d.Match != "any" {
		t.Fatalf("save should set id + default match: %+v", d)
	}
	s2, _ := Open(path)
	if len(s2.List()) != 1 {
		t.Fatalf("reload lost cohort")
	}
	if _, ok := s2.Get(d.ID); !ok {
		t.Fatalf("get by id failed")
	}
}
