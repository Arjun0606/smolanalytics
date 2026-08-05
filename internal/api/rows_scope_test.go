package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// The rows behind a number must be the rows behind THAT number.
//
// /v1/rows honoured the window but ignored ?date=, so clicking one bar of a chart returned every
// row in the whole window — and then proved it. Internally consistent, about a different figure
// than the one clicked. A proof that verifies the wrong number is worse than no proof at all,
// because it is confidently checkable and still wrong.
func TestRowsScopeToTheBarThatWasClicked(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	day := func(d int) time.Time { return now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(9 * time.Hour) }
	var batch []map[string]any
	add := func(user string, at time.Time) {
		batch = append(batch, map[string]any{
			"name": "signup", "distinct_id": user, "timestamp": at.Format(time.RFC3339),
		})
	}
	for i := 0; i < 5; i++ { // 5 on day-2
		add(fmt.Sprintf("a%d", i), day(2))
	}
	for i := 0; i < 11; i++ { // 11 on day-1
		add(fmt.Sprintf("b%d", i), day(1))
	}
	body, _ := json.Marshal(batch)
	res, err := http.Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil || res.StatusCode != http.StatusAccepted {
		t.Fatalf("seed: %v %v", err, res.StatusCode)
	}
	res.Body.Close()

	get := func(path string) map[string]any {
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	total := func(m map[string]any) int { n, _ := m["total"].(float64); return int(n) }
	proved := func(m map[string]any) bool {
		p, _ := m["proof"].(map[string]any)
		ok, _ := p["matches"].(bool)
		return ok
	}

	whole := get("/v1/rows?event=signup&days=7")
	if got := total(whole); got != 16 {
		t.Errorf("window total = %d, want 16", got)
	}

	oneDay := get("/v1/rows?event=signup&days=7&date=" + day(1).Format("2006-01-02"))
	if got := total(oneDay); got != 11 {
		t.Errorf("one bar returned %d rows, want the 11 on that day — the microscope is showing "+
			"the whole window for a click on a single bucket", got)
	}
	if !proved(oneDay) {
		t.Error("the single-day rows do not reproduce their own figure")
	}
	// the question has to name the day, or a result pasted elsewhere claims more than it proved
	if q, _ := oneDay["question"].(string); !strings.Contains(q, day(1).Format("2006-01-02")) {
		t.Errorf("question %q does not say which day it is about", q)
	}
	// and the two must genuinely differ, or this test proves nothing
	if total(whole) == total(oneDay) {
		t.Fatal("fixture is wrong: the day and the window have the same count")
	}
}
