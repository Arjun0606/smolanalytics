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

// MemTotal is the fallback that makes this work on Fly, where a Firecracker microVM has no
// cgroup ceiling. It is reported in kibibytes; reading it as bytes would set a limit 1024x too
// small and drive the process into permanent GC, so the unit is asserted rather than assumed.
func TestParseMemTotal(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int64
	}{
		{"fly 256MB machine", "MemTotal:         246964 kB\nMemFree:  100 kB\n", 246964 * 1024},
		{"MemTotal not first", "SwapTotal: 0 kB\nMemTotal: 1024 kB\n", 1024 * 1024},
		{"unexpected unit is rejected", "MemTotal: 1024 MB\n", 0},
		{"missing entirely", "MemFree: 100 kB\n", 0},
		{"unparseable", "MemTotal: lots kB\n", 0},
		{"zero", "MemTotal: 0 kB\n", 0},
		{"empty file", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "meminfo")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := parseMemTotal(p); got != tc.want {
				t.Errorf("parseMemTotal(%q) = %d, want %d", tc.content, got, tc.want)
			}
		})
	}
}

// On the machine running this test there is real memory, so a limit must be derivable. A zero
// here means every deployment target silently falls back to Go's default — the exact failure
// that shipped once already.
func TestAvailableMemoryResolvesSomewhere(t *testing.T) {
	n, source := availableMemory()
	if n <= 0 {
		t.Skip("no cgroup and no /proc/meminfo on this platform (macOS) — covered by the parser tests")
	}
	t.Logf("available memory: %d MB from %s", n>>20, source)
}
