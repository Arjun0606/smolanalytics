package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

func actionServer(t *testing.T) *Server {
	t.Helper()
	st := memory.New()
	for _, name := range []string{"signup", "checkout"} {
		if err := st.Ingest(event.Event{ID: name, Name: name, DistinctID: "u1", Timestamp: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st)
	ins, _ := insights.Open("")
	coh, _ := cohort.Open("")
	wh, _ := webhook.Open("")
	al, _ := alert.Open("")
	s.SetInsights(ins)
	s.SetCohorts(coh)
	s.SetWebhooks(wh)
	s.SetAlerts(al)
	return s
}

func callAct(t *testing.T, s *Server, tool, args string) (string, error) {
	t.Helper()
	return s.callTool(tool, json.RawMessage(args))
}

func TestActionAlertLifecycle(t *testing.T) {
	s := actionServer(t)

	// unknown event must be rejected with a self-correcting error, never created
	if _, err := callAct(t, s, "create_alert", `{"name":"x","event":"sinup","op":"lt","threshold":10,"window_hours":24}`); err == nil || !strings.Contains(err.Error(), "signup") {
		t.Fatalf("typo'd event should error listing real events, got %v", err)
	}

	out, err := callAct(t, s, "create_alert", `{"name":"signup drop","event":"signup","op":"lt","threshold":10,"window_hours":24}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no webhook is configured") {
		t.Fatalf("alert with no webhook must warn it has nowhere to fire: %s", out)
	}

	list, err := callAct(t, s, "list_alerts", `{}`)
	if err != nil || !strings.Contains(list, "signup drop") {
		t.Fatalf("list_alerts should show the alert: %s (%v)", list, err)
	}
	var parsed struct {
		Alerts []alert.Alert `json:"alerts"`
	}
	_ = json.Unmarshal([]byte(list), &parsed)
	if len(parsed.Alerts) != 1 {
		t.Fatalf("want 1 alert, got %d", len(parsed.Alerts))
	}
	if _, err := callAct(t, s, "delete_alert", `{"id":"`+parsed.Alerts[0].ID+`"}`); err != nil {
		t.Fatal(err)
	}
	list, _ = callAct(t, s, "list_alerts", `{}`)
	if strings.Contains(list, "signup drop") {
		t.Fatalf("alert should be gone: %s", list)
	}
}

func TestActionCohortAndReport(t *testing.T) {
	s := actionServer(t)

	out, err := callAct(t, s, "create_cohort", `{"name":"Paying","events":["checkout"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"current_members":1`) {
		t.Fatalf("cohort should resolve u1 as a member: %s", out)
	}

	if _, err := callAct(t, s, "save_report", `{"name":"Signup trend","type":"trend","params":{"event":"signup"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := callAct(t, s, "save_report", `{"name":"bad","type":"pie","params":{}}`); err == nil {
		t.Fatal("unknown report type must be rejected")
	}
	list, _ := callAct(t, s, "list_saved_reports", `{}`)
	if !strings.Contains(list, "Signup trend") {
		t.Fatalf("saved report missing from list: %s", list)
	}
}

// create_sequence_cohort defines an ordered behavioral cohort from the editor and resolves it
// through the same engine as everything else — only the user who did the events IN ORDER counts.
func TestActionSequenceCohort(t *testing.T) {
	st := memory.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := func(user, name string, d time.Duration) {
		if err := st.Ingest(event.Event{ID: user + name, Name: name, DistinctID: user, Timestamp: base.Add(d)}); err != nil {
			t.Fatal(err)
		}
	}
	seed("u1", "signup", 0)           // u1: signup then checkout → matches signup→checkout
	seed("u1", "checkout", time.Hour) //
	seed("u2", "checkout", 0)         // u2: checkout then signup (reverse) → must NOT match
	seed("u2", "signup", time.Hour)   //
	s := New(st)
	coh, _ := cohort.Open("")
	s.SetCohorts(coh)

	out, err := callAct(t, s, "create_sequence_cohort", `{"name":"Activated","steps":[{"event":"signup"},{"event":"checkout"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"current_members":1`) {
		t.Fatalf("only u1 (signup then checkout, in order) should match: %s", out)
	}

	if _, err := callAct(t, s, "create_sequence_cohort", `{"name":"x","steps":[]}`); err == nil {
		t.Fatal("empty steps must be rejected")
	}
	// an unknown step event self-corrects, like create_cohort does
	if _, err := callAct(t, s, "create_sequence_cohort", `{"name":"x","steps":[{"event":"sinup"},{"event":"checkout"}]}`); err == nil || !strings.Contains(err.Error(), "signup") {
		t.Fatalf("unknown step event should error listing real events, got %v", err)
	}
}

func TestActionWebhookSecretShownOnceAndRedacted(t *testing.T) {
	s := actionServer(t)
	out, err := callAct(t, s, "add_webhook", `{"name":"slack","url":"https://hooks.example.com/x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"secret"`) {
		t.Fatalf("creation must return the signing secret once: %s", out)
	}
	list, _ := callAct(t, s, "list_webhooks", `{}`)
	if strings.Contains(list, `"secret"`) {
		t.Fatalf("list must redact secrets: %s", list)
	}
}

func TestActionsWithoutStoresExplain(t *testing.T) {
	st := memory.New()
	s := New(st) // no stores attached — bare mode
	_, err := callAct(t, s, "create_alert", `{"name":"x","event":"y","op":"lt","threshold":1,"window_hours":24}`)
	if err == nil || !strings.Contains(err.Error(), "smolanalytics serve") {
		t.Fatalf("bare mode must explain how to enable actions, got %v", err)
	}
}
