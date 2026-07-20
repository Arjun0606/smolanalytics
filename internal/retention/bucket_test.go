package retention

import (
	"reflect"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

// ev fires "open" for a user on a given day, anchored exactly on the UTC day boundary so
// week buckets (7-day blocks) fall where the test expects: day 0-6 = week 0, 7-13 = week 1.
func evDay(user string, day int) event.Event {
	return event.Event{Name: "open", DistinctID: user, Timestamp: time.Unix(int64(day)*86400, 0).UTC()}
}

func TestRetention_WeeklyBucket(t *testing.T) {
	evs := []event.Event{
		evDay("A", 0), evDay("A", 7), // week 0 and week 1
		evDay("B", 0),                 // week 0 only
		evDay("C", 0), evDay("C", 14), // week 0 and week 2
	}
	r := ComputeBucketed(evs, 3, "open", "week", false)
	if r.Bucket != "week" {
		t.Fatalf("Bucket = %q, want week", r.Bucket)
	}
	if len(r.Cohorts) != 1 || r.Cohorts[0].Size != 3 {
		t.Fatalf("want one week-0 cohort of 3, got %+v", r.Cohorts)
	}
	// classic: active EXACTLY in week n. wk0=all, wk1=A, wk2=C.
	if got := r.Cohorts[0].Returned; !reflect.DeepEqual(got, []int{3, 1, 1, 0}) {
		t.Errorf("classic weekly Returned = %v, want [3 1 1 0]", got)
	}
}

func TestRetention_RollingMode(t *testing.T) {
	evs := []event.Event{
		evDay("A", 0), evDay("A", 7), // last active week 1
		evDay("B", 0),                 // last active week 0
		evDay("C", 0), evDay("C", 14), // last active week 2
	}
	r := ComputeBucketed(evs, 3, "open", "week", true)
	if !r.Rolling {
		t.Fatal("Rolling flag not set")
	}
	// rolling: retained at period n if active on n OR LATER.
	// wk0: all 3. wk1: A(last=1)+C(last=2)=2. wk2: C=1.
	if got := r.Cohorts[0].Returned; !reflect.DeepEqual(got, []int{3, 2, 1, 0}) {
		t.Errorf("rolling weekly Returned = %v, want [3 2 1 0]", got)
	}
}

func TestRetention_DailyStillDefault(t *testing.T) {
	// Compute (the old signature) must behave exactly as before: daily bucket.
	r := Compute([]event.Event{evDay("A", 0), evDay("A", 1)}, 2, "open")
	if r.Bucket != "day" {
		t.Errorf("Compute default bucket = %q, want day", r.Bucket)
	}
	if got := r.Cohorts[0].Returned; !reflect.DeepEqual(got, []int{1, 1, 0}) {
		t.Errorf("daily Returned = %v, want [1 1 0]", got)
	}
}
