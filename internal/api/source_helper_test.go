package api

import (
	"os"
	"testing"
)

// readFileForTest reads a source file so a test can assert a WIRING property — that a surface
// references a thing at all. Used where no rendered assertion can help, because a page missing a
// section looks identical to a page whose section had nothing to say.
func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
