package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageToolNamesUntrackedActions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One tracked action and one untracked, so both halves of the report are exercised.
	if err := os.WriteFile(filepath.Join(dir, "src", "a.ts"),
		[]byte("export function pay() {\n  stripe.checkout.sessions.create({});\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "b.ts"),
		[]byte("export function up() {\n  auth.signUp(email);\n  smolanalytics.track(\"signup\");\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newServer(t)
	args, _ := json.Marshal(map[string]any{"repo_path": dir})
	out, err := s.callTool("instrumentation_coverage", args)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if m["uncovered"].(float64) != 1 || m["covered"].(float64) != 1 {
		t.Errorf("got %v uncovered / %v covered, want 1/1", m["uncovered"], m["covered"])
	}
	// The uncovered one must come first — that is the list someone acts on.
	acts := m["actions"].([]any)
	if acts[0].(map[string]any)["covered"] == true {
		t.Error("covered action sorted before the uncovered one")
	}
	if !strings.Contains(m["next"].(string), "propose_instrumentation") {
		t.Errorf("should point at the next step, got %v", m["next"])
	}
}

func TestCoverageToolIsListedAndExplainsWhyItIsUnique(t *testing.T) {
	s := newServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	b, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []struct{ Name, Description string }
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, tl := range got.Tools {
		if tl.Name != "instrumentation_coverage" {
			continue
		}
		for _, must := range []string{"invisible in the data", "repo", "propose_instrumentation"} {
			if !strings.Contains(tl.Description, must) {
				t.Errorf("description missing %q", must)
			}
		}
		return
	}
	t.Fatal("instrumentation_coverage is not in tools/list")
}
