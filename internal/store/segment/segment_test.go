package segment

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

func mk(id, name, did string, ts time.Time, props map[string]any) event.Event {
	return event.Event{ID: id, Name: name, DistinctID: did, Timestamp: ts, Properties: props}
}

func openStore(t *testing.T, sealAt int) (*Store, string, blob.Blob) {
	t.Helper()
	dir := t.TempDir()
	b, err := blob.NewLocal(filepath.Join(dir, "cold"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "hot.data"), b, sealAt)
	if err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(dir, "hot.data"), b
}

func TestCodecRoundTrip(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	in := []event.Event{
		mk("e1", "signup", "u1", base, map[string]any{"plan": "pro", "n": float64(3)}),
		mk("e2", "checkout", "u2", base.Add(time.Hour), nil),
		mk("e3", "signup", "u1", base.Add(2*time.Hour), map[string]any{"src": "hn"}),
	}
	data, min, max, names, err := encodeSegment(in)
	if err != nil {
		t.Fatal(err)
	}
	if !min.Equal(base) || !max.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("min/max wrong: %v %v", min, max)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 distinct names, got %v", names)
	}
	out, err := decodeSegment(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch %d != %d", len(out), len(in))
	}
	for i := range in {
		if out[i].ID != in[i].ID || out[i].Name != in[i].Name || out[i].DistinctID != in[i].DistinctID || !out[i].Timestamp.Equal(in[i].Timestamp) {
			t.Fatalf("event %d mismatch: %+v != %+v", i, out[i], in[i])
		}
	}
	if out[0].Properties["plan"] != "pro" || out[2].Properties["src"] != "hn" {
		t.Fatalf("properties not preserved: %+v", out[0].Properties)
	}
}

func TestSealScanCountAndReopen(t *testing.T) {
	s, hotPath, b := openStore(t, 100) // seal every 100 events
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	const N = 1000
	for i := 0; i < N; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := s.Ingest(mk("e"+itoa(i), "pageview", "u"+itoa(i%10), ts, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if c := s.Count(); c != N {
		t.Fatalf("count after ingest = %d, want %d", c, N)
	}
	// most events are in sealed cold segments now (only the tail is hot)
	if len(s.manifest) < 9 {
		t.Fatalf("expected ~9 sealed segments, got %d", len(s.manifest))
	}

	// time-range scan returns only in-range, reading just overlapping segments
	from := base.Add(200 * time.Minute)
	to := base.Add(300 * time.Minute)
	got := 0
	if err := s.Scan(from, to, func(e event.Event) error { got++; return nil }); err != nil {
		t.Fatal(err)
	}
	if got != 100 { // events 200..299
		t.Fatalf("range scan got %d, want 100", got)
	}

	if err := s.Close(); err != nil { // flushes the hot tail to a segment
		t.Fatal(err)
	}
	// reopen: every event durable across hot WAL + cold segments, no loss, no dup
	s2, err := Open(hotPath, b, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c := s2.Count(); c != N {
		t.Fatalf("count after reopen = %d, want %d", c, N)
	}
	all, _ := s2.Range(time.Time{}, time.Time{})
	if len(all) != N {
		t.Fatalf("range after reopen = %d, want %d", len(all), N)
	}
}

func TestPruneDropsOldSegments(t *testing.T) {
	s, _, _ := openStore(t, 100)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 500; i++ {
		ts := base.AddDate(0, 0, i) // one event per day
		_ = s.Ingest(mk("e"+itoa(i), "pageview", "u1", ts, nil))
	}
	_ = s.Flush()
	before := s.Count()
	// prune everything older than day 300
	cut := base.AddDate(0, 0, 300)
	removed, err := s.Prune(cut)
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 || s.Count() != before-removed {
		t.Fatalf("prune accounting off: removed=%d before=%d now=%d", removed, before, s.Count())
	}
	// nothing before the cutoff should remain
	old := 0
	_ = s.Scan(time.Time{}, cut, func(e event.Event) error { old++; return nil })
	if old != 0 {
		t.Fatalf("events before cutoff still present: %d", old)
	}
}

// TestCrashWindowDedup reproduces a crash *after* a segment's manifest was persisted but
// *before* the hot WAL was cleared, and asserts Open reconciles it (no double-count).
func TestCrashWindowDedup(t *testing.T) {
	s, hotPath, b := openStore(t, 1_000_000) // high sealAt so nothing auto-seals
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	evs := make([]event.Event, 50)
	for i := range evs {
		evs[i] = mk("e"+itoa(i), "pageview", "u1", base.Add(time.Duration(i)*time.Minute), nil)
		_ = s.Ingest(evs[i])
	}
	// Manually seal WITHOUT clearing hot — exactly the crash window.
	data, min, max, names, err := encodeSegment(evs)
	if err != nil {
		t.Fatal(err)
	}
	key := "seg/0000000000.sms"
	if err := b.Put(key, data); err != nil {
		t.Fatal(err)
	}
	s.manifest = append(s.manifest, segMeta{Key: key, Count: len(evs), MinTS: min, MaxTS: max, Names: names})
	if err := s.persistManifestLocked(); err != nil {
		t.Fatal(err)
	}
	_ = s.hot.Close() // simulate process death with hot WAL still holding the events

	// Reopen: recovery must notice the hot events are all in the last segment and clear them.
	s2, err := Open(hotPath, b, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if c := s2.Count(); c != len(evs) {
		t.Fatalf("crash-window double-count: got %d, want %d", c, len(evs))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
