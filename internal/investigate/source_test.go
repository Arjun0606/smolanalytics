package investigate

import (
	"os"
	"testing"
)

// readSource lets a test assert on the code itself. Used where the property is "this calls the
// shared implementation rather than its own" — provable no other way, and the defect it guards
// against (two surfaces, one question, two answers) has recurred repeatedly in this codebase.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
