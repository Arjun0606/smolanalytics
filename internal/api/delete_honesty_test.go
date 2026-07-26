package api

// The HTTP DELETE endpoints share the MCP tools' stores, so they shared the same
// defect: 200 {"deleted": "<whatever was in the path>"} whether or not a row
// existed. The response is what a script (or the dashboard) reports to a human, so
// it must never imply a removal that did not happen. A miss stays 200 (deleting the
// same id twice is a legitimate retry), it just says deleted:false.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/defined"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/survey"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

func deleteHonestyServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	_ = st.Ingest(event.Event{ID: "1", Name: "signup", DistinctID: "u1", Timestamp: time.Now().UTC()})
	dir := t.TempDir()
	at := func(n string) string { return filepath.Join(dir, n) }
	s := New(st)

	ins, _ := insights.Open(at("insights.json"))
	coh, _ := cohort.Open(at("cohorts.json"))
	wh, _ := webhook.Open(at("webhooks.json"))
	al, _ := alert.Open(at("alerts.json"))
	gl, _ := goal.Open(at("goals.json"))
	sh, _ := share.Open(at("shares.json"))
	dp, _ := deploys.Open(at("deploys.json"))
	fl, _ := flag.Open(at("flags.json"))
	sv, _ := survey.Open(at("surveys.json"))
	df, _ := defined.Open(at("defined.json"))
	set, err := settings.Open(at("settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	s.SetInsights(ins)
	s.SetCohorts(coh)
	s.SetWebhooks(wh)
	s.SetAlerts(al)
	s.SetGoals(gl)
	s.SetShares(sh)
	s.SetDeploys(dp)
	s.SetFlags(fl)
	s.SetSurveys(sv)
	s.SetDefined(df)
	s.SetSettings(set)
	return s
}

// DELETE of an id that matches nothing: 200 (idempotent) but deleted:false, never a
// payload that reads like a confirmation.
func TestDeleteEndpointsDoNotClaimPhantomRemovals(t *testing.T) {
	s := deleteHonestyServer(t)
	h := s.Handler()

	for _, tc := range []struct{ path, verb string }{
		{"/v1/insights/nope123", "deleted"},
		{"/v1/cohorts/nope123", "deleted"},
		{"/v1/defined/totally_made_up_xyz", "deleted"},
		{"/v1/webhooks/nope123", "deleted"},
		{"/v1/alerts/nope123", "deleted"},
		{"/v1/goals/nope123", "deleted"},
		{"/v1/deploys/nope123", "deleted"},
		{"/v1/flags/never_existed_xyz", "deleted"},
		{"/v1/surveys/nope123", "deleted"},
		{"/v1/shares/nope123", "revoked"},
		{"/v1/settings/keys/nope123", "revoked"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("DELETE", tc.path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("DELETE %s: got %d, want 200 so retries stay idempotent (%s)", tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("DELETE %s: body isn't JSON: %s", tc.path, w.Body.String())
		}
		done, ok := body[tc.verb].(bool)
		if !ok {
			t.Fatalf("DELETE %s: %q must be a boolean, got: %s", tc.path, tc.verb, w.Body.String())
		}
		if done {
			t.Fatalf("DELETE %s claimed it removed something that never existed: %s", tc.path, w.Body.String())
		}
		if note, _ := body["note"].(string); note == "" {
			t.Fatalf("DELETE %s: a no-op needs a note saying nothing was removed: %s", tc.path, w.Body.String())
		}
	}
}

// The mirror image at the HTTP layer: a real row reports deleted:true.
func TestDeleteEndpointReportsTrueForRealRow(t *testing.T) {
	s := deleteHonestyServer(t)
	h := s.Handler()

	g, err := s.goals.Save(goal.Definition{Name: "Signed up", Kind: "event", Value: "signup"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/v1/goals/"+g.ID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if done, _ := body["deleted"].(bool); !done {
		t.Fatalf("deleting a real goal must report deleted:true: %s", w.Body.String())
	}
	if _, hasNote := body["note"]; hasNote {
		t.Fatalf("a real deletion needs no hedging note: %s", w.Body.String())
	}
	if len(s.goals.List()) != 0 {
		t.Fatalf("goal survived a deleted:true: %+v", s.goals.List())
	}
}
