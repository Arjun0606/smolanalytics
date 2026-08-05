package mcp

import (
	"sort"
	"strings"
	"testing"
)

// registeredTools is every tool the server actually advertises.
func registeredTools(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, m := range toolList {
		if n, _ := m["name"].(string); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) < 20 {
		t.Fatalf("only %d tools found — the registry shape changed and this guard is now blind", len(names))
	}
	return names
}

// The readOnly guard protects the PUBLIC DEMO, whose /mcp is deliberately unauthenticated. Its
// allowlist was hand-written and had silently drifted from the tools that exist: it named tools
// that were never registered (so those entries protected nothing) and omitted real mutating
// ones (so strangers could call them). The comment above it argued an explicit list was safer
// than inferring from a prefix — but nothing checked the list against reality, so it rotted in
// exactly the way it was written to prevent.
//
// This is the check that was missing: every name in mutatingTools must be a real tool.
func TestMutatingToolsAllExist(t *testing.T) {
	have := map[string]bool{}
	for _, n := range registeredTools(t) {
		have[n] = true
	}
	var ghosts []string
	for n := range mutatingTools {
		if !have[n] {
			ghosts = append(ghosts, n)
		}
	}
	sort.Strings(ghosts)
	if len(ghosts) > 0 {
		t.Errorf("mutatingTools names %d tool(s) that do not exist, so they guard nothing: %s",
			len(ghosts), strings.Join(ghosts, ", "))
	}
}

// ...and the other direction, which is the one that actually lets a stranger write: every
// registered tool must be CLASSIFIED. A new mutating tool that nobody adds to the list is
// open on the demo the moment it ships, and no test would have said so.
func TestEveryToolIsClassifiedReadOrWrite(t *testing.T) {
	var unclassified []string
	for _, n := range registeredTools(t) {
		if mutatingTools[n] || readOnlyTools[n] {
			continue
		}
		unclassified = append(unclassified, n)
	}
	if len(unclassified) > 0 {
		t.Errorf("%d tool(s) are in neither mutatingTools nor readOnlyTools: %s\n"+
			"every tool must be classified — an unclassified one defaults to callable on the "+
			"public demo, which is the vandalism surface this guard exists to close",
			len(unclassified), strings.Join(unclassified, ", "))
	}
}
