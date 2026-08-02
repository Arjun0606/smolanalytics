package fixbrief

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/funnel"
)

func TestTmpPromptLen(t *testing.T) {
	now := time.Now().UTC()
	var evs []event.Event
	paths := []string{"/", "/pricing", "/docs/getting-started", "/blog/why-analytics", "/signup"}
	for i := 0; i < 600; i++ {
		uid := fmt.Sprintf("u%d", i)
		ts := now.Add(-time.Duration(i%6*24) * time.Hour).Add(-2 * time.Hour)
		evs = append(evs, event.Event{Name: "$pageview", UserID: uid, Timestamp: ts, Props: map[string]any{"path": paths[i%len(paths)]}})
		evs = append(evs, event.Event{Name: "signup", UserID: uid, Timestamp: ts.Add(time.Minute)})
		if i%3 == 0 {
			evs = append(evs, event.Event{Name: "activate", UserID: uid, Timestamp: ts.Add(2 * time.Minute)})
		}
		if i%9 == 0 {
			evs = append(evs, event.Event{Name: "checkout", UserID: uid, Timestamp: ts.Add(3 * time.Minute)})
		}
	}
	steps := []funnel.Step{{Event: "signup"}, {Event: "activate"}, {Event: "checkout"}}
	bs := ComputeAll(evs, steps, now)
	if len(bs) == 0 {
		t.Fatal("no briefs")
	}
	for _, b := range bs {
		enc := url.QueryEscape(b.Prompt)
		t.Logf("kind=%s title=%q raw=%d encoded=%d askRaw=%d", b.Finding.Kind, b.Finding.Title, len(b.Prompt), len(enc), len(b.Ask))
	}
	t.Logf("---- PROMPT ----\n%s", bs[0].Prompt)
	t.Logf("---- ASK ----\n%s", bs[0].Ask)
}
