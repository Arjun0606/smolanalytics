package exportlink

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedeemIsSingleUse(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "links.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, token, err := s.Create("csv", now)
	if err != nil {
		t.Fatal(err)
	}
	if l.Format != "csv" || !l.Expires.Equal(now.Add(TTL)) {
		t.Fatalf("link = %+v, want csv expiring in 1h", l)
	}
	if strings.Contains(l.Hash, token) {
		t.Fatal("raw token must never be stored")
	}

	format, ok := s.Redeem(token, now.Add(time.Minute))
	if !ok || format != "csv" {
		t.Fatalf("first redeem = (%q, %v), want (csv, true)", format, ok)
	}
	if _, ok := s.Redeem(token, now.Add(time.Minute)); ok {
		t.Fatal("second redeem must fail — links are single-use")
	}

	// the burn survives a restart: a reopened store must also refuse the token
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Redeem(token, now.Add(time.Minute)); ok {
		t.Fatal("redeem after reopen must fail — the burn is persisted")
	}
}

func TestRedeemExpiry(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Create("jsonl", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Redeem(token, now.Add(TTL+time.Second)); ok {
		t.Fatal("expired link must not redeem")
	}
	// and it stays dead — expiry burns, it doesn't just postpone
	if _, ok := s.Redeem(token, now); ok {
		t.Fatal("an expired-then-retried token must stay dead")
	}
}

func TestCreateValidatesFormat(t *testing.T) {
	s, _ := Open("")
	now := time.Now().UTC()
	if _, _, err := s.Create("xml", now); err == nil || !strings.Contains(err.Error(), `"csv" or "jsonl"`) {
		t.Fatalf("err = %v, want the format menu", err)
	}
	l, _, err := s.Create("", now) // empty defaults to jsonl, the re-importable shape
	if err != nil || l.Format != "jsonl" {
		t.Fatalf("default format = %+v, %v", l, err)
	}
}

func TestRedeemRejectsGarbage(t *testing.T) {
	s, _ := Open("")
	now := time.Now().UTC()
	if _, _, err := s.Create("csv", now); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{"", "short", strings.Repeat("0", 32), strings.Repeat("0", 64)} {
		if _, ok := s.Redeem(tok, now); ok {
			t.Fatalf("token %q must not redeem", tok)
		}
	}
}

func TestCreatePrunesExpired(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	s, _ := Open("")
	if _, _, err := s.Create("csv", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("csv", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.items)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("items = %d, want 1 — expired links must be pruned on create", n)
	}
}
