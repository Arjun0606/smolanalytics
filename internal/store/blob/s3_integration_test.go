package blob

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// mockS3 is a tiny in-memory S3-compatible server (ignores auth) — enough to verify the
// S3 client's HTTP behavior, key encoding, and ListObjectsV2 XML round-trip end to end.
func mockS3(t *testing.T) (*S3, func()) {
	t.Helper()
	var mu sync.Mutex
	objs := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// list: GET /{bucket}?list-type=2&prefix=...
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			prefix := r.URL.Query().Get("prefix")
			var keys []string
			for k := range objs {
				if strings.HasPrefix(k, prefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><ListBucketResult>`)
			for _, k := range keys {
				fmt.Fprintf(&b, "<Contents><Key>%s</Key></Contents>", k)
			}
			b.WriteString(`<IsTruncated>false</IsTruncated></ListBucketResult>`)
			w.Write([]byte(b.String()))
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/test/")
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			objs[key] = body
			w.WriteHeader(200)
		case http.MethodGet:
			b, ok := objs[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		case http.MethodDelete:
			delete(objs, key)
			w.WriteHeader(204)
		}
	}))
	s, err := NewS3(srv.URL, "us-east-1", "test", "ak", "sk", "tenant1")
	if err != nil {
		t.Fatal(err)
	}
	return s, srv.Close
}

func TestS3RoundTrip(t *testing.T) {
	s, done := mockS3(t)
	defer done()

	if err := s.Put("seg/0000000001.sms", []byte("hello columnar")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("seg/0000000001.sms")
	if err != nil || string(got) != "hello columnar" {
		t.Fatalf("get = %q, %v", got, err)
	}
	// absent object → os.ErrNotExist (matches Local, so segment.Open treats it the same)
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("expected error for missing object")
	}
	if err := s.Put("manifest.json", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	keys, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	// prefix "tenant1/" is stripped back off
	want := map[string]bool{"manifest.json": true, "seg/0000000001.sms": true}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("unexpected key %q in %v", k, keys)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("list = %v, want 2 keys", keys)
	}
	if err := s.Delete("manifest.json"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.List(""); len(keys) != 1 {
		t.Fatalf("after delete, list = %v", keys)
	}
}
