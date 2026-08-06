package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// A PROOF LINK THAT DISAGREES WITH THE NUMBER IT PROVES IS WORSE THAN NO PROOF LINK.
//
// The whole claim behind this product is that every report is a pure function of the raw event
// log, so any figure can be opened and recomputed. "show the rows" is where that claim is cashed.
// If the rows recompute to something other than the tile above them, the feature does not merely
// fail — it demonstrates, to the one user curious enough to check, that the numbers cannot be
// trusted. It spends exactly the trust it was built to earn.
//
// Two real defects this pins, both live before it existed:
//
//  1. The 6h/12h presets force rangeDays to 1 (day-based helpers need a positive day count), and
//     the proof query hardcoded days=N — so a six-hour tile opened a FULL DAY of rows.
//  2. The People tile counts distinct people; /v1/rows counts events by default. Proving it
//     without unique=1 would have opened 2,336 pageview rows under a figure of 1,753 people.
//
// This renders the real dashboard, reads the tiles and their proof queries out of the HTML, and
// asks the endpoint the question the link actually asks.
func TestEveryProofLinkRecomputesItsOwnTile(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	// Hourly traffic: the windows under test differ by parts of a day, so daily-granularity
	// fixtures pass against the very bugs this is for.
	now := time.Now().UTC()
	var batch []map[string]any
	for d := 0; d < 20; d++ {
		for h := 0; h < 24; h++ {
			ts := now.AddDate(0, 0, -d).Truncate(24 * time.Hour).Add(time.Duration(h) * time.Hour)
			if ts.After(now) {
				continue
			}
			u := fmt.Sprintf("d%dh%d", d, h)
			batch = append(batch, map[string]any{
				"name": "$pageview", "distinct_id": u, "timestamp": ts.Format(time.RFC3339),
				"properties": map[string]any{"path": "/"}})
			if h%2 == 0 {
				batch = append(batch, map[string]any{
					"name": "signup", "distinct_id": u,
					"timestamp": ts.Add(time.Minute).Format(time.RFC3339)})
			}
		}
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	code := resp.StatusCode
	resp.Body.Close()
	if code >= 300 {
		t.Fatalf("seeding failed with %d — the fixture never landed", code)
	}

	// Each tile: the rendered value, then the proof query that sits inside it. Split on the tile
	// boundary rather than matching a balanced block — a non-greedy `.*?</div>` stops at the first
	// inner close and never reaches the proof link, which is how the first version of this test
	// reported "no tile carries a proof" about a page full of them.
	valRe := regexp.MustCompile(`(?s)<div class="kv(?:\s[^"]*)?">([^<]+)<`)
	proofRe := regexp.MustCompile(`data-proof="([^"]+)"`)

	for _, window := range []string{"", "?days=7", "?days=1", "?hours=6", "?hours=12"} {
		html := getBody(t, srv, "/"+window)
		blocks := strings.Split(html, `<div class="kpi">`)[1:]
		if len(blocks) == 0 {
			t.Fatalf("%s: no KPI tiles rendered at all", window)
		}
		checked := 0
		for _, blk := range blocks {
			b := []string{"", blk}
			pm := proofRe.FindStringSubmatch(b[1])
			if pm == nil {
				continue // tiles with no rows to show (ratios) are honestly unproven
			}
			vm := valRe.FindStringSubmatch(b[1])
			if vm == nil {
				t.Errorf("%s: a tile has a proof link but no readable value", window)
				continue
			}
			shown, cerr := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(vm[1]), ",", ""))
			if cerr != nil {
				continue // non-numeric tile (a duration, a percentage)
			}

			q := strings.ReplaceAll(pm[1], "&amp;", "&")
			var got struct {
				Total int `json:"total"`
				Users int `json:"users"`
				Proof struct {
					Claimed    int  `json:"claimed"`
					Recomputed int  `json:"recomputed"`
					Matches    bool `json:"matches"`
				} `json:"proof"`
			}
			mustJSON(t, getBody(t, srv, "/v1/rows?"+q+"&limit=1"), &got)

			// The figure the link stands behind: people when it asks for people, events otherwise.
			proven := got.Total
			if strings.Contains(q, "unique=1") {
				proven = got.Users
			}
			if proven != shown {
				t.Errorf("%s: tile shows %d but its own proof link (%s) recomputes %d — the link "+
					"contradicts the number it proves", window, shown, q, proven)
			}
			// And the endpoint must agree with itself: claimed == recomputed.
			if !got.Proof.Matches || got.Proof.Claimed != got.Proof.Recomputed {
				t.Errorf("%s: /v1/rows?%s claimed %d but recomputed %d (matches=%v)",
					window, q, got.Proof.Claimed, got.Proof.Recomputed, got.Proof.Matches)
			}
			checked++
		}
		if checked == 0 {
			t.Errorf("%s: not one tile carried a proof link — the moat is invisible again", window)
		}
	}
}

// The headline number specifically. It is the first figure anyone reads, and it was the one that
// could not be checked while the tile beside it could.
func TestTheHeadlinePeopleTileIsProvable(t *testing.T) {
	st := memory.New()
	srv := httptest.NewServer(New(st).Handler())
	defer srv.Close()

	now := time.Now().UTC()
	var batch []map[string]any
	for i := 0; i < 60; i++ {
		batch = append(batch, map[string]any{
			"name": "$pageview", "distinct_id": fmt.Sprintf("u%d", i%25),
			"timestamp":  now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
			"properties": map[string]any{"path": "/"}})
	}
	body, _ := json.Marshal(batch)
	resp, err := srv.Client().Post(srv.URL+"/v1/events", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	html := getBody(t, srv, "/?days=7")
	i := strings.Index(html, "People · ")
	if i < 0 {
		t.Fatal("no People tile on the page")
	}
	// The proof link belongs to THIS tile, so look only at the markup that follows it before the
	// next tile begins.
	seg := html[i:]
	if j := strings.Index(seg[10:], `class="kpi`); j > 0 {
		seg = seg[:j+10]
	}
	if !strings.Contains(seg, "data-proof=") {
		t.Fatal("the headline People number has no way to be checked — the one figure everyone " +
			"reads is the one they cannot verify")
	}
	if !strings.Contains(seg, "unique=1") {
		t.Error("the People proof must ask for people (unique=1); counting events under a people " +
			"figure is a recomputation of a different question")
	}
}
