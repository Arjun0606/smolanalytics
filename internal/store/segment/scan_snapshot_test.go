package segment

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

// hookBlob fires a callback on the first Get of a segment key. That is the deterministic
// stand-in for "a mutation landed while a scan was between its manifest snapshot and its
// reads" — the window every bug in this file lives in. Firing it from inside Get is exactly
// the interleaving, without a sleep or a goroutine race to lose.
type hookBlob struct {
	blob.Blob
	fired bool
	on    func()
}

func (h *hookBlob) Get(key string) ([]byte, error) {
	if h.on != nil && !h.fired && strings.HasPrefix(key, "seg/") {
		h.fired = true
		h.on()
	}
	return h.Blob.Get(key)
}

func openHooked(t *testing.T, sealAt int) (*Store, *hookBlob) {
	t.Helper()
	dir := t.TempDir()
	local, err := blob.NewLocal(filepath.Join(dir, "cold"))
	if err != nil {
		t.Fatal(err)
	}
	hb := &hookBlob{Blob: local}
	s, err := Open(filepath.Join(dir, "hot.data"), hb, sealAt)
	if err != nil {
		t.Fatal(err)
	}
	return s, hb
}

// A seal that lands mid-scan used to erase the whole hot block from the answer: Scan
// snapshotted the manifest, dropped the lock, and only read the hot WAL at the end — by
// which time sealLocked had written those events out as a segment the snapshot doesn't
// contain and cleared the WAL. Measured 1 event returned where Count() said 6, error nil,
// and an identical query a moment later said 6. At the default sealAt that window opens
// every 50k events, i.e. under exactly the load where every surface is polling, and two
// surfaces disagreeing is the one thing this product may not do.
func TestScanIsAtomicAgainstASealLandingMidScan(t *testing.T) {
	s, hb := openHooked(t, 1_000_000) // never auto-seals; the test seals on purpose
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	if err := s.Ingest(mk("cold-0", "ev", "u1", base, nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil { // one sealed segment
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := s.Ingest(mk("hot-"+string(rune('a'+i)), "ev", "u1", base.Add(time.Duration(i+1)*time.Minute), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if c := s.Count(); c != 6 {
		t.Fatalf("setup: count=%d want 6", c)
	}

	// seal the hot block while the scan is decoding segment 0
	hb.on = func() {
		if err := s.Flush(); err != nil {
			t.Errorf("mid-scan seal: %v", err)
		}
	}

	var got []string
	if err := s.Scan(time.Time{}, time.Time{}, func(e event.Event) error {
		got = append(got, e.ID)
		return nil
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("scan returned %d of 6 events (%v) — a seal in the snapshot window dropped the hot block", len(got), got)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id %s returned %d times — the snapshot and the hot read overlapped", id, n)
		}
	}
}

// Prune persists the shrunken manifest and then deleted the now-unreferenced blobs inline,
// while an in-flight Scan was still iterating its pre-prune snapshot. The scan's next Get
// returned "open .../cold/seg/0000000001.sms: no such file or directory" — a 500 with a
// storage path in the body, for every dashboard/API/MCP query running during the 6-hourly
// retention prune, after fn had already been handed part of the answer.
func TestScanSurvivesAPruneDeletingBlobsMidScan(t *testing.T) {
	s, hb := openHooked(t, 1_000_000)
	now := time.Now().UTC()
	old := now.Add(-90 * 24 * time.Hour)

	if err := s.Ingest(mk("keep-0", "ev", "u1", now.Add(-time.Hour), nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(mk("old-0", "ev", "u2", old, nil), mk("old-1", "ev", "u2", old.Add(time.Minute), nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil { // two sealed segments, one of them prunable
		t.Fatal(err)
	}

	hb.on = func() {
		if _, err := s.Prune(now.Add(-30 * 24 * time.Hour)); err != nil {
			t.Errorf("mid-scan prune: %v", err)
		}
	}

	n := 0
	if err := s.Scan(time.Time{}, time.Time{}, func(event.Event) error { n++; return nil }); err != nil {
		t.Fatalf("a retention prune must not break an in-flight query: %v", err)
	}
	if n != 3 {
		t.Fatalf("scan returned %d events, want the 3 its snapshot promised", n)
	}

	// The deferred blob is still garbage, not a leak: the next sweep with no scan
	// registered removes it, so retention still reclaims disk.
	if _, deleted := s.Scrub(); deleted == 0 {
		t.Fatal("the blob held back for the in-flight scan was never swept afterwards")
	}
	if r := s.Verify(); len(r.Orphans) != 0 || len(r.Problems) != 0 {
		t.Fatalf("after the sweep: orphans=%v problems=%v", r.Orphans, r.Problems)
	}
}

// Same window, the GDPR path: DeleteUser rewrites affected segments under fresh keys and
// then retires the old blobs. An erasure request must not turn every concurrent query into
// a 500 either.
func TestScanSurvivesDeleteUserMidScan(t *testing.T) {
	s, hb := openHooked(t, 1_000_000)
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	if err := s.Ingest(mk("a-0", "ev", "ua", base, nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(mk("b-0", "ev", "ub", base.Add(time.Hour), nil), mk("a-1", "ev", "ua", base.Add(2*time.Hour), nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	hb.on = func() {
		if _, err := s.DeleteUser("ub"); err != nil {
			t.Errorf("mid-scan erasure: %v", err)
		}
	}

	n := 0
	if err := s.Scan(time.Time{}, time.Time{}, func(event.Event) error { n++; return nil }); err != nil {
		t.Fatalf("a GDPR erasure must not break an in-flight query: %v", err)
	}
	if n != 3 {
		t.Fatalf("scan returned %d events, want the 3 its snapshot promised", n)
	}
	// the erasure itself still took effect for every query that starts after it
	after, err := s.Range(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after {
		if e.DistinctID == "ub" {
			t.Fatalf("erased user still readable: %+v", e)
		}
	}
}
