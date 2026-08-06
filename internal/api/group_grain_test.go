package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// b2bServer seeds a shape where user grain and account grain MUST disagree: many seats per
// account, and only one seat per account converting. If a test here passes on data where the two
// grains happen to be equal, it is not testing anything.
func b2bServer(t *testing.T) *httptest.Server {
	t.Helper()
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	t.Cleanup(srv.Close)
	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < 30; i++ {
		u, co := fmt.Sprintf("u%d", i), fmt.Sprintf("acct%d", i%3) // 30 people, 3 accounts
		ts := now.Add(-time.Duration(i%5) * 24 * time.Hour)
		batch = append(batch, map[string]any{
			"name": "signup", "distinct_id": u, "timestamp": ts.Format(time.RFC3339),
			// plan travels too, so a filtered request in the tests below is a REAL filter — a
			// filter that matches nothing empties the page and would let the link assertion pass
			// or fail for reasons that have nothing to do with the link.
			"properties": map[string]any{"company": co, "plan": map[bool]string{true: "pro", false: "free"}[i%2 == 0]},
		})
		if i%10 == 0 { // exactly one person per account converts
			batch = append(batch, map[string]any{
				"name": "checkout", "distinct_id": u, "timestamp": ts.Add(time.Hour).Format(time.RFC3339),
			})
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return srv
}

func getBody(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustJSON(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

// The number the whole feature exists for. 3 of 30 people convert (10%); 3 of 3 accounts convert
// (100%). If these ever come out equal the re-keying has stopped working and every account report
// is silently a user report.
func TestAccountGrainAndUserGrainGiveDifferentAnswers(t *testing.T) {
	srv := b2bServer(t)
	var user, acct struct {
		Steps []struct {
			Event string `json:"event"`
			Count int    `json:"count"`
		} `json:"steps"`
		Unit  string `json:"unit"`
		Grain *struct {
			Groups  int `json:"groups"`
			Dropped int `json:"events_dropped"`
		} `json:"group_grain"`
	}
	mustJSON(t, getBody(t, srv, "/v1/funnel?steps=signup,checkout"), &user)
	mustJSON(t, getBody(t, srv, "/v1/funnel?steps=signup,checkout&group_by=company"), &acct)

	if user.Steps[0].Count != 30 || user.Steps[1].Count != 3 {
		t.Fatalf("user grain: want 30 → 3, got %d → %d", user.Steps[0].Count, user.Steps[1].Count)
	}
	if acct.Steps[0].Count != 3 || acct.Steps[1].Count != 3 {
		t.Fatalf("account grain: want 3 → 3 (every account converts), got %d → %d",
			acct.Steps[0].Count, acct.Steps[1].Count)
	}
	// The unit must be STATED. A response that counts accounts and does not say so is the whole
	// failure mode: 3 read as three people is a catastrophic misreading of a healthy product.
	if acct.Unit != "accounts" {
		t.Fatalf("account-grain response must declare its unit, got %q", acct.Unit)
	}
	if acct.Grain == nil || acct.Grain.Groups != 3 {
		t.Fatalf("the grain must travel with the report, got %+v", acct.Grain)
	}
	if user.Unit != "" || user.Grain != nil {
		t.Fatal("a plain funnel must be byte-identical to what it always was — no envelope when nobody asked for one")
	}
}

// A property nothing carries is an error, never an empty funnel. "0 of 0 accounts converted" is
// the most alarming thing this product could show for a reason that is entirely our fault.
func TestAnUninstrumentedGroupPropertyIsAnErrorNotAnEmptyReport(t *testing.T) {
	srv := b2bServer(t)
	resp, err := srv.Client().Get(srv.URL + "/v1/funnel?steps=signup,checkout&group_by=comapny")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("a typo'd group property must 400, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if !strings.Contains(body, "comapny") {
		t.Fatalf("the error must name the property that failed, got %q", body)
	}
	// It has to say what to DO. "no event carries comapny" alone leaves someone stuck.
	if !strings.Contains(body, "identify") && !strings.Contains(body, "signup") {
		t.Fatalf("the error must say how to instrument it, got %q", body)
	}
}

// The dashboard must never print "users" over a count of accounts. This is the one bug that makes
// the feature worse than not having it: every number is right and every label is wrong.
func TestTheDashboardNamesTheUnitItActuallyCounted(t *testing.T) {
	srv := b2bServer(t)
	plain := getBody(t, srv, "/")
	if !strings.Contains(plain, "counts people who reached it") {
		t.Fatal("the ordinary funnel should still say it counts people")
	}
	acct := getBody(t, srv, "/?grain=company")
	if strings.Contains(acct, "counts people who reached it") {
		t.Fatal("the funnel is counting accounts and still says users")
	}
	if !strings.Contains(acct, "counts <b>accounts</b>") {
		t.Fatal("the account-grain funnel must say it counts accounts")
	}
	if !strings.Contains(acct, "returning accounts") {
		t.Fatal("retention at account grain must not be headed 'returning users'")
	}
	// The way back must keep the question. A bare ?grain= would reset window and filters, so the
	// reader would see several numbers move and credit them all to the grain switch.
	back := getBody(t, srv, "/?days=7&f=plan:eq:pro&grain=company")
	if !strings.Contains(back, "days=7") || !strings.Contains(back, "plan") {
		t.Fatal("the 'count people instead' link dropped the window or the filters")
	}
}

// A grain nobody instrumented falls back to user grain — and must SAY it fell back, or the page
// shows user numbers under an accounts heading.
func TestAFailedGrainRequestFallsBackVisibly(t *testing.T) {
	srv := b2bServer(t)
	body := getBody(t, srv, "/?grain=nonesuch")
	if strings.Contains(body, "returning accounts") {
		t.Fatal("nothing carries the property, so the page must not claim to be counting accounts")
	}
}
