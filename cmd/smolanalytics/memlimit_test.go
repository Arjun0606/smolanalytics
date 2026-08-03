package main

import (
	"os"
	"path/filepath"
	"testing"
)

// containerMemoryLimit decides whether the process gets a memory cap at all. Reading the wrong
// file, or misreading "unbounded", silently leaves the default in place — and the symptom is a
// container OOM-killed in production weeks later with nothing in the logs to explain it.
func TestContainerMemoryLimitParsing(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool // should this be treated as a real limit?
	}{
		{"v2 real limit", "268435456\n", true},
		{"v2 unbounded", "max\n", false},
		{"v1 unbounded sentinel", "9223372036854771712\n", false},
		{"garbage", "not-a-number\n", false},
		{"zero", "0\n", false},
		{"negative", "-1\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "memory.max")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got := parseCgroupLimit(p)
			if (got > 0) != tc.want {
				t.Errorf("parseCgroupLimit(%q) = %d, want limit=%v", tc.content, got, tc.want)
			}
		})
	}
}

// On a developer machine there is no cgroup file, and the binary must not invent a limit.
func TestNoCgroupMeansNoLimit(t *testing.T) {
	if n := parseCgroupLimit(filepath.Join(t.TempDir(), "does-not-exist")); n != 0 {
		t.Errorf("missing cgroup file produced limit %d, want 0", n)
	}
}
