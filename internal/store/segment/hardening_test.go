package segment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/store/blob"
)

func openTemp(t *testing.T, sealAt int) (*Store, string, blob.Blob) {
	t.Helper()
	dir := t.TempDir()
	b, err := blob.NewLocal(filepath.Join(dir, "cold"))
	if err != nil {
		t.Fatal(err)
	}
	hot := filepath.Join(dir, "hot.log")
	s, err := Open(hot, b, sealAt)
	if err != nil {
		t.Fatal(err)
	}
	return s, hot, b
}

// A crash mid-append leaves a torn record at the hot-log tail. Reopen must keep
// every complete prior event and drop only the torn tail — never refuse to open,
// never lose the good prefix.
func TestTornHotLogTailRecovers(t *testing.T) {
	s, hot, b := openTemp(t, 1000) // high sealAt: everything stays hot
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		if err := s.Ingest(ev(fmt.Sprintf("e%d", i), "u1", base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	// simulate the crash: abandon the store WITHOUT Close (Close would seal the hot
	// block — a real crash doesn't get that courtesy), then tear the final record
	fi, err := os.Stat(hot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(hot, fi.Size()-7); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(hot, b, 1000)
	if err != nil {
		t.Fatalf("a torn tail must not prevent opening: %v", err)
	}
	evs, _ := s2.Range(time.Time{}, time.Time{})
	if len(evs) != 19 {
		t.Fatalf("want the 19 complete events to survive (only the torn one dropped), got %d", len(evs))
	}
}

// The manifest round-trips through the versioned envelope, and a legacy bare-array
// manifest (v0, written by older binaries) still loads — no migration step.
func TestManifestEnvelopeAndLegacy(t *testing.T) {
	s, _, b := openTemp(t, 2)
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	_ = s.Ingest(ev("a", "u1", base), ev("b", "u2", base.Add(time.Minute))) // seals

	raw, err := b.Get("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var env manifestEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.V != 1 || len(env.Segments) != 1 {
		t.Fatalf("manifest should be a v1 envelope with 1 segment: %s (%v)", raw, err)
	}

	// rewrite it as a legacy v0 bare array and reopen — must load identically
	legacy, _ := json.Marshal(env.Segments)
	if err := b.Put("manifest.json", legacy); err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	s2, err := Open(filepath.Join(dir2, "hot.log"), b, 2)
	if err != nil {
		t.Fatalf("legacy bare-array manifest must load: %v", err)
	}
	evs, _ := s2.Range(time.Time{}, time.Time{})
	if len(evs) != 2 {
		t.Fatalf("legacy manifest lost data: got %d events", len(evs))
	}

	// a FUTURE version must fail loudly, not misread silently
	future, _ := json.Marshal(map[string]any{"v": 99, "segments": []any{}})
	_ = b.Put("manifest.json", future)
	if _, err := Open(filepath.Join(t.TempDir(), "hot.log"), b, 2); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("v99 manifest must demand an upgrade, got %v", err)
	}
}

// Verify holds on a healthy store, flags corruption, and Scrub removes orphans
// without touching referenced segments.
func TestVerifyAndScrub(t *testing.T) {
	s, _, b := openTemp(t, 2)
	base := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	_ = s.Ingest(ev("a", "u1", base), ev("b", "u2", base.Add(time.Minute))) // segment 1
	_ = s.Ingest(ev("c", "u1", base.Add(2*time.Minute)))                    // stays hot

	r := s.Verify()
	if len(r.Problems) != 0 || r.Segments != 1 || r.Events != 2 || r.HotEvents != 1 {
		t.Fatalf("healthy store: %+v", r)
	}

	// plant an orphan (unreferenced blob) and corrupt nothing → scrub removes it
	_ = b.Put("seg/9999999999.sms", []byte("orphaned junk"))
	r2, deleted := s.Scrub()
	if len(r2.Orphans) != 1 || deleted != 1 || len(r2.Problems) != 0 {
		t.Fatalf("scrub should remove exactly the orphan: %+v deleted=%d", r2, deleted)
	}
	if evs, _ := s.Range(time.Time{}, time.Time{}); len(evs) != 3 {
		t.Fatal("scrub must never touch referenced data")
	}

	// now corrupt the real segment → Verify must flag it
	keys, _ := b.List("seg/")
	if err := b.Put(keys[0], []byte("garbage")); err != nil {
		t.Fatal(err)
	}
	r3 := s.Verify()
	if len(r3.Problems) == 0 {
		t.Fatal("corrupt segment must be reported")
	}
}

// Format-compatibility: fixtures written by past binaries must decode identically,
// forever. When the format (or manifest version) changes, regenerate a NEW fixture
// dir alongside the old ones — never replace them.
func TestFormatCompatibility(t *testing.T) {
	fixtures, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("no testdata fixtures yet")
	}
	for _, fx := range fixtures {
		if !fx.IsDir() {
			continue
		}
		t.Run(fx.Name(), func(t *testing.T) {
			root := filepath.Join("testdata", fx.Name())
			b, err := blob.NewLocal(filepath.Join(root, "cold"))
			if err != nil {
				t.Fatal(err)
			}
			// open against a COPY of the hot log so the fixture stays pristine
			hotSrc, err := os.ReadFile(filepath.Join(root, "hot.log"))
			if err != nil {
				t.Fatal(err)
			}
			hotCopy := filepath.Join(t.TempDir(), "hot.log")
			if err := os.WriteFile(hotCopy, hotSrc, 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(hotCopy, b, 100000)
			if err != nil {
				t.Fatalf("fixture from an older binary failed to open: %v", err)
			}
			evs, err := s.Range(time.Time{}, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			goldenRaw, err := os.ReadFile(filepath.Join(root, "golden.json"))
			if err != nil {
				t.Fatal(err)
			}
			var golden []map[string]any
			if err := json.Unmarshal(goldenRaw, &golden); err != nil {
				t.Fatal(err)
			}
			if len(evs) != len(golden) {
				t.Fatalf("fixture decodes %d events, golden expects %d", len(evs), len(golden))
			}
			for i, g := range golden {
				if evs[i].ID != g["id"] || evs[i].Name != g["name"] || evs[i].DistinctID != g["distinct_id"] {
					t.Fatalf("event %d drifted: got %+v want %+v", i, evs[i], g)
				}
			}
		})
	}
}
