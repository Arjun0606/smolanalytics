package survey

import (
	"testing"

	"github.com/Arjun0606/smolanalytics/internal/event"
)

func shown(id, u string) event.Event {
	return event.Event{Name: ShownEvent, DistinctID: u, Properties: map[string]any{PropSurvey: id}}
}
func resp(id, u string, ans any) event.Event {
	return event.Event{Name: ResponseEvent, DistinctID: u, Properties: map[string]any{PropSurvey: id, PropAnswer: ans}}
}

func TestNPSResults(t *testing.T) {
	evs := []event.Event{
		shown("s1", "a"), shown("s1", "b"), shown("s1", "c"), shown("s1", "d"),
		resp("s1", "a", float64(10)), // promoter
		resp("s1", "b", float64(9)),  // promoter
		resp("s1", "c", float64(7)),  // passive
		resp("s1", "d", float64(3)),  // detractor
		shown("other", "x"),          // different survey → excluded
	}
	r := Results(evs, "s1", "nps", 0)
	if r.Shown != 4 || r.Responses != 4 {
		t.Fatalf("shown=%d responses=%d, want 4/4", r.Shown, r.Responses)
	}
	if r.RatePct != 100 {
		t.Fatalf("rate = %v, want 100", r.RatePct)
	}
	if r.NPS == nil || *r.NPS != 25 { // %prom(50) - %det(25) = 25
		t.Fatalf("nps = %v, want 25", r.NPS)
	}
}

func TestRatingChoiceText(t *testing.T) {
	rr := Results([]event.Event{shown("r", "a"), resp("r", "a", float64(4)), resp("r", "b", float64(2))}, "r", "rating", 0)
	if rr.Average == nil || *rr.Average != 3 {
		t.Fatalf("rating avg = %v, want 3", rr.Average)
	}
	cr := Results([]event.Event{resp("c", "a", "blue"), resp("c", "b", "blue"), resp("c", "c", "red")}, "c", "choice", 0)
	if len(cr.Breakdown) != 2 || cr.Breakdown[0].Label != "blue" || cr.Breakdown[0].N != 2 {
		t.Fatalf("choice breakdown wrong: %+v", cr.Breakdown)
	}
	tr := Results([]event.Event{resp("t", "a", "love it"), resp("t", "b", "meh")}, "t", "text", 0)
	if len(tr.Recent) != 2 || tr.Recent[0] != "meh" {
		t.Fatalf("text recent wrong (most recent first): %+v", tr.Recent)
	}
}

func TestStoreValidation(t *testing.T) {
	s, _ := Open("")
	if _, err := s.Save(Survey{Name: "x", Type: "bogus", Question: "q"}); err == nil {
		t.Fatal("bad type should error")
	}
	sv, err := s.Save(Survey{Name: "NPS", Type: "nps", Question: "How likely?", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if sv.ID == "" {
		t.Fatal("save should assign an id")
	}
	if _, ok := s.Get(sv.ID); !ok {
		t.Fatal("should find saved survey")
	}
}
