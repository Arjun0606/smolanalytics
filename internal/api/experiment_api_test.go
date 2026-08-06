package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// expServer seeds `days` of traffic at `perDay` visitors with a `convPct` conversion rate, so the
// power calculation has a real baseline to work from rather than a number a test invented.
func expServer(t *testing.T, perDay, days, convPct int) *httptest.Server {
	t.Helper()
	st := memory.New()
	s := New(st)
	fs, err := flag.Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.SetFlags(fs)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < days; d++ {
		for i := 0; i < perDay; i++ {
			u := fmt.Sprintf("d%d_u%d", d, i)
			ts := now.AddDate(0, 0, -d).Add(time.Duration(i) * time.Second)
			batch = append(batch, map[string]any{
				"name": "$pageview", "distinct_id": u, "timestamp": ts.Format(time.RFC3339)})
			if i*100 < perDay*convPct {
				batch = append(batch, map[string]any{
					"name": "signup", "distinct_id": u, "timestamp": ts.Add(time.Minute).Format(time.RFC3339)})
			}
		}
	}
	// Sent in chunks, and the status is CHECKED.
	//
	// The first version posted the whole thing at once and ignored the response. 4,000/day over 30
	// days is 120,000 events, the ingest batch cap is 10,000, so every large fixture 413'd and the
	// store stayed empty — and the tests then failed with "unknown event signup" pointing at the
	// endpoint rather than at the seed. A test helper that discards an error produces failures that
	// accuse the wrong code.
	const chunk = 5000
	for i := 0; i < len(batch); i += chunk {
		end := i + chunk
		if end > len(batch) {
			end = len(batch)
		}
		body, _ := json.Marshal(batch[i:end])
		resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		code := resp.StatusCode
		resp.Body.Close()
		if code >= 300 {
			t.Fatalf("seeding failed with %d at event %d of %d — the fixture never landed", code, i, len(batch))
		}
	}
	return srv
}

func postExp(t *testing.T, srv *httptest.Server, in map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(in)
	resp, err := srv.Client().Post(srv.URL+"/v1/experiments", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// THE FEATURE IS THE REFUSAL.
//
// At 40 visitors a day, detecting a 5% relative lift takes years. Every other tool will happily
// start that experiment and hand over a p-value at the end. Telling someone not to run it is the
// single most valuable thing this endpoint does, and it is the part a solo developer cannot work
// out for themselves.
func TestItRefusesAnExperimentTheTrafficCannotFinish(t *testing.T) {
	srv := expServer(t, 40, 30, 5) // 40/day, 5% conversion
	got := postExp(t, srv, map[string]any{"key": "pricing_v2", "goal": "signup"})

	v, _ := got["verdict"].(string)
	if v == "" {
		t.Fatal("no verdict — a sample size with no advice is arithmetic, not an answer")
	}
	if !strings.Contains(v, "do not run this") {
		t.Errorf("at 40 visitors a day this test cannot finish, and the verdict was: %q", v)
	}
	// It must say the cost in a unit a human feels. "n=47000" is not a duration.
	if !strings.Contains(v, "month") {
		t.Errorf("the refusal must state the cost in months, got %q", v)
	}
	// And it must say what to do instead, or it is only discouragement.
	if !strings.Contains(v, "instead") {
		t.Errorf("a refusal with no alternative leaves the user stuck: %q", v)
	}
}

// Plenty of traffic: run it, and say how long.
func TestItEncouragesAnExperimentTheTrafficCanFinish(t *testing.T) {
	srv := expServer(t, 900, 30, 25) // busy, high baseline
	got := postExp(t, srv, map[string]any{"key": "banner", "goal": "signup"})
	v, _ := got["verdict"].(string)
	if strings.Contains(v, "do not run") {
		t.Errorf("at 900/day and a 25%% baseline this is very runnable, got %q", v)
	}
	if !strings.Contains(v, "days") {
		t.Errorf("the verdict must state a duration, got %q", v)
	}
}

// NOTHING is created until asked. Someone should see the cost before committing, because the
// honest answer is often "do not run this" — and a tool that creates first has already decided.
func TestPlanningCreatesNothing(t *testing.T) {
	srv := expServer(t, 300, 30, 15)
	got := postExp(t, srv, map[string]any{"key": "pricing_v2", "goal": "signup"})
	if got["created"] != false {
		t.Fatalf("planning created something: %v", got["created"])
	}
	var flags map[string]any
	mustJSON(t, getBody(t, srv, "/v1/flags"), &flags)
	if n := len(flags["flags"].([]any)); n != 0 {
		t.Fatalf("a flag was created by a plan-only request (%d flags exist)", n)
	}
}

// The two questions are enough. Everything a PM would be asked for is derived.
func TestTwoQuestionsProduceAFullyDesignedExperiment(t *testing.T) {
	srv := expServer(t, 700, 30, 20)
	got := postExp(t, srv, map[string]any{"key": "pricing_v2", "goal": "signup", "start": true})
	if got["created"] != true {
		t.Fatalf("start:true did not create: %v", got)
	}
	f := got["flag"].(map[string]any)
	exp := f["experiment"].(map[string]any)

	if exp["control"] != "control" {
		t.Errorf("control must be DECLARED, not inferred — inferring alphabetically inverts the sign "+
			"of every lift on arms like {control, a_new}. got %v", exp["control"])
	}
	if exp["mode"] != "sequential" {
		t.Errorf("mode must default to sequential: a solo dev WILL check daily, and peeking at a "+
			"fixed-horizon test runs ~43%% false positives. got %v", exp["mode"])
	}
	if f["measured"] != true {
		t.Error("an experiment that does not log exposures has nothing to analyse")
	}
	// The guardrail nobody remembers to ask for.
	gs, _ := exp["guardrails"].([]any)
	if len(gs) == 0 {
		t.Fatal("no guardrail — a conversion win that doubled the error rate would read as a win")
	}
	if g := gs[0].(map[string]any); g["event"] != "$exception" {
		t.Errorf("the default guardrail should watch errors, got %v", g["event"])
	}
	if exp["locked"] != true {
		t.Error("the plan must lock on start — a plan editable after seeing data is not a plan")
	}
	if n, _ := exp["n_planned"].(float64); n <= 0 {
		t.Error("n_planned must come from the real baseline, not be left empty")
	}
}

// A goal nobody sends is a typo, and the experiment would measure nothing forever.
func TestAnUnknownGoalIsRefusedUpFront(t *testing.T) {
	srv := expServer(t, 300, 30, 15)
	b, _ := json.Marshal(map[string]any{"key": "x", "goal": "singup"}) // typo
	resp, err := srv.Client().Post(srv.URL+"/v1/experiments", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("a goal event nobody sends was accepted with %d — the experiment would measure "+
			"nothing, forever, and look healthy doing it", resp.StatusCode)
	}
}

// Both questions are required, and the error says what they mean rather than naming fields.
func TestItAsksForBothQuestionsInPlainWords(t *testing.T) {
	srv := expServer(t, 300, 30, 15)
	b, _ := json.Marshal(map[string]any{"key": "x"})
	resp, _ := srv.Client().Post(srv.URL+"/v1/experiments", "application/json", strings.NewReader(string(b)))
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "counts as a win") {
		t.Errorf("the error should explain what a goal IS, not just name the field: %q", msg)
	}
}
