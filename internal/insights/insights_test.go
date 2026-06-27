package insights

import (
	"path/filepath"
	"testing"
)

func TestSaveListDeletePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "insights.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.Save(Insight{Name: "Signup funnel", Type: "funnel", Params: map[string]string{"steps": "signup,checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if in.ID == "" || in.Created.IsZero() {
		t.Fatalf("save should assign id + created: %+v", in)
	}
	if len(s.List()) != 1 {
		t.Fatalf("want 1 saved, got %d", len(s.List()))
	}

	// reload from disk — should persist
	s2, _ := Open(path)
	if len(s2.List()) != 1 || s2.List()[0].Name != "Signup funnel" {
		t.Fatalf("reload lost the insight: %+v", s2.List())
	}

	if err := s2.Delete(in.ID); err != nil {
		t.Fatal(err)
	}
	s3, _ := Open(path)
	if len(s3.List()) != 0 {
		t.Fatalf("delete didn't persist: %+v", s3.List())
	}
}

func TestSaveValidates(t *testing.T) {
	s, _ := Open("")
	if _, err := s.Save(Insight{Type: "funnel"}); err == nil {
		t.Fatal("empty name should error")
	}
	if _, err := s.Save(Insight{Name: "x", Type: "bogus"}); err == nil {
		t.Fatal("bad type should error")
	}
}
