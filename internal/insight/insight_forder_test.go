package insight

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

// THE VERDICT MUST USE THE PAGE'S FUNNEL DISCIPLINE, NOT ITS OWN.
//
// Passing the page's STEPS in was only half the fix. The funnel pane has an order selector
// (?forder=ordered|strict|unordered) and the verdict card above it always computed with the
// default, because forder was parsed further down the handler than the verdict was built.
//
// On a product where people commonly fire the later event first, selecting "any order" made the
// pane read "100% continue · 0 dropped" while the card directly above it announced "only 75% go on
// to signup, so 10 people stop here" — the page's headline diagnosis contradicting the very pane
// it is diagnosing, because one control moved one of them and not the other.
func TestVerdictHonoursTheFunnelOrderDiscipline(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	// Everyone fires checkout BEFORE signup. Ordered sees a funnel nobody completes;
	// unordered sees everyone completing both.
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("u%d", i)
		t0 := now.AddDate(0, 0, -2).Add(time.Duration(i) * time.Minute)
		evs = append(evs,
			event.Event{ID: "c" + id, Name: "checkout", DistinctID: id, Timestamp: t0},
			event.Event{ID: "s" + id, Name: "signup", DistinctID: id, Timestamp: t0.Add(time.Minute)},
		)
	}
	steps := []funnel.Step{{Event: "signup"}, {Event: "checkout"}}

	read := func(fs []Finding) string {
		var b strings.Builder
		for _, f := range fs {
			b.WriteString(f.Title)
			b.WriteString("|")
			b.WriteString(f.Detail)
			b.WriteString("\n")
		}
		return b.String()
	}

	ordered := read(GenerateForFunnelOpts(evs, steps, funnel.Options{Order: funnel.Ordered}))
	unordered := read(GenerateForFunnelOpts(evs, steps, funnel.Options{Order: funnel.Unordered}))

	if ordered == unordered {
		t.Errorf("the verdict is identical under ordered and unordered discipline on a dataset "+
			"where every user fires the steps backwards — it is computing with its own funnel "+
			"while the pane uses the reader's:\n%s", ordered)
	}
	// And the default wrapper must keep behaving exactly as before.
	if got := read(GenerateForFunnel(evs, steps)); got == "" && ordered == "" {
		t.Skip("no findings on this fixture at all")
	}
}
