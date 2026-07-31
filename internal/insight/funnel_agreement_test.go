package insight

import (
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

// The dashboard picks its funnel one way (top events by volume, ordered by journey) and this
// package used to pick its own another way (first-touch coverage). One page could therefore
// show a funnel pane reading "$pageview → $engagement → $click" while its own verdict card
// said "overall $pageview→$deadclick conversion is 7%". Both were internally honest, and the
// page still contradicted itself about what "the funnel" was.
//
// These pin the contract: when the caller supplies the page's steps, every finding must
// describe THOSE steps and no others.

func fev(name, user string, t time.Time) event.Event {
	return event.Event{Name: name, DistinctID: user, Timestamp: t}
}

// a dataset where an auto-detected journey would plausibly end somewhere other than the
// funnel the caller asks for: $deadclick is present and correlated, but is not a page step.
func funnelFixture() []event.Event {
	base := time.Now().UTC().Add(-48 * time.Hour)
	var evs []event.Event
	for i := 0; i < 40; i++ {
		u := string(rune('a'+i%26)) + string(rune('0'+i/26))
		evs = append(evs, fev("$pageview", u, base.Add(time.Duration(i)*time.Minute)))
		if i%2 == 0 {
			evs = append(evs, fev("$engagement", u, base.Add(time.Duration(i)*time.Minute+time.Second)))
		}
		if i%4 == 0 {
			evs = append(evs, fev("$click", u, base.Add(time.Duration(i)*time.Minute+2*time.Second)))
		}
		if i%5 == 0 {
			evs = append(evs, fev("$deadclick", u, base.Add(time.Duration(i)*time.Minute+3*time.Second)))
		}
	}
	return evs
}

func TestGenerateForFunnelDescribesTheSuppliedSteps(t *testing.T) {
	evs := funnelFixture()
	steps := []funnel.Step{{Event: "$pageview"}, {Event: "$engagement"}, {Event: "$click"}}

	var dropoff string
	for _, f := range GenerateForFunnel(evs, steps) {
		if strings.HasPrefix(f.Title, "Biggest drop-off") {
			dropoff = f.Title + " " + f.Detail
		}
	}
	if dropoff == "" {
		t.Fatalf("expected a drop-off finding over the supplied funnel; got none")
	}
	// The endpoints are now rendered in human terms, so assert on those rather than the raw
	// names — the finding must still describe the SUPPLIED funnel and no other.
	if !strings.Contains(dropoff, HumanEventIng("$pageview")) || !strings.Contains(dropoff, HumanEventIng("$click")) {
		t.Errorf("finding should name the supplied funnel's ends, got: %s", dropoff)
	}
	// and must NOT describe a step the caller did not ask for
	if strings.Contains(dropoff, HumanEventIng("$deadclick")) || strings.Contains(dropoff, "$deadclick") {
		t.Errorf("finding describes $deadclick, which is not in the supplied funnel: %s", dropoff)
	}
}

func TestGenerateStillAutoDetectsWithoutSuppliedSteps(t *testing.T) {
	evs := funnelFixture()
	// Generate is what the CLI, the brief and MCP use: no page context, so it must still
	// produce findings on its own rather than going quiet now that the parameter exists.
	if got := Generate(evs); len(got) == 0 {
		t.Fatal("Generate produced no findings; auto-detection regressed")
	}
}

func TestASingleStepFallsBackToDetection(t *testing.T) {
	evs := funnelFixture()
	// fewer than 2 steps is not a funnel, so it must not be honoured as one
	one := GenerateForFunnel(evs, []funnel.Step{{Event: "$pageview"}})
	auto := Generate(evs)
	if len(one) != len(auto) {
		t.Errorf("a 1-step funnel should fall back to detection: got %d findings, auto gives %d", len(one), len(auto))
	}
}
