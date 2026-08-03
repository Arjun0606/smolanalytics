package main

import (
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Go's collector sizes the heap against how much is live, not against how much the machine
// has. With the default GOGC it lets the heap roughly double before collecting, which is the
// right trade on a laptop and the wrong one in a 256MB container: the kernel OOM-kills the
// process before the collector ever decides it is worth running.
//
// Measured on this codebase, 2M events hold ~1.1GB live while one dashboard render pushes the
// heap to ~4GB, nearly all of it short-lived garbage that is reclaimed immediately after. The
// container does not survive long enough to see it reclaimed.
//
// A soft memory limit changes what happens at the edge. As the heap approaches it the
// collector runs harder and more often, spending CPU to stay under the cap. The dashboard gets
// slower. It does NOT die, and it does not lose data. That is the whole trade: a slow page is
// a complaint, an OOM-killed container is a customer watching their analytics disappear with
// no error message anywhere, which is the failure mode that ends a trial.
//
// It is a SOFT limit by design. Go will exceed it rather than livelock in permanent GC, so a
// genuinely too-small box still fails — just visibly and slowly rather than instantly.
func init() {
	// An explicit GOMEMLIMIT means the operator has already decided. Go applies it itself; we
	// must not compute a second answer over the top of theirs.
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	avail, source := containerMemoryLimit()
	if avail <= 0 {
		return // not containerized, or the limit is unset/unbounded: leave Go's default alone
	}
	// Headroom for everything the Go heap does not count: goroutine stacks, the binary itself,
	// and the allocator returning pages to the OS lazily. Going right up to the cgroup limit
	// reintroduces the OOM this exists to prevent.
	limit := avail / 100 * 80
	const floor = 64 << 20 // below this the reserve matters more than the ratio
	if limit < floor {
		if avail <= floor {
			return // too small to divide up safely; Go's default is no worse than a bad guess
		}
		limit = floor
	}
	debug.SetMemoryLimit(limit)
	log.Printf("memory limit %d MB (80%% of %d MB %s) — GC works harder near the cap instead of the container being killed",
		limit>>20, avail>>20, source)
}

// containerMemoryLimit reports the cgroup memory ceiling in bytes, or 0 when there is none.
// Both cgroup versions are read because Fly.io, Docker and Kubernetes do not agree on which
// one is mounted, and guessing wrong silently disables the limit.
func containerMemoryLimit() (int64, string) {
	if n := parseCgroupLimit("/sys/fs/cgroup/memory.max"); n > 0 {
		return n, "cgroup v2"
	}
	if n := parseCgroupLimit("/sys/fs/cgroup/memory/memory.limit_in_bytes"); n > 0 {
		return n, "cgroup v1"
	}
	return 0, ""
}

// parseCgroupLimit reads one cgroup memory file, returning 0 for anything that is not a real
// ceiling: a missing file, unparseable contents, or either spelling of "unbounded". cgroup v2
// writes the literal string "max"; v1 writes a sentinel just under max-int64, so that one is
// rejected by magnitude rather than by name.
func parseCgroupLimit(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= (1<<62) {
		return 0
	}
	return n
}
