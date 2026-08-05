package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func sdkSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("sdk.js")
	if err != nil {
		t.Fatalf("read sdk.js: %v", err)
	}
	return string(b)
}

// elemDesc is documented "Metadata only, never input values" and then read `el.value`. An
// <input> has no innerText, so the fallback fired on every text field: what the user typed —
// email, name, card number, search query — left the browser on click. A control's own label is
// metadata; its value is the user's data.
func TestClickCaptureNeverReadsInputValues(t *testing.T) {
	src := sdkSource(t)
	// the fallback chain that caused it, in any spacing
	if regexp.MustCompile(`innerText\s*\|\|\s*el\.value`).MatchString(src) {
		t.Error("elemDesc falls back to el.value again — every click on a text input ships its contents")
	}
	for _, bad := range []string{"el.value ||", "|| el.value", "d.text = el.value"} {
		if strings.Contains(src, bad) {
			t.Errorf("sdk.js reads an element's value into a captured property (%q)", bad)
		}
	}
}

// The data-* loop was documented as "opt-in element identity" and was opt-OUT: it shipped every
// data-* attribute of the clicked element and four ancestors. data-user-id, data-email,
// data-price and data-account all live on exactly the elements people click.
func TestClickCaptureOnlyReadsItsOwnDataNamespace(t *testing.T) {
	src := sdkSource(t)
	i := strings.Index(src, "function elemDesc")
	if i < 0 {
		t.Fatal("elemDesc not found")
	}
	body := src[i : i+2400]
	if !strings.Contains(body, `dk.indexOf("sa") !== 0`) {
		t.Error("elemDesc no longer restricts itself to data-sa-* — it is exfiltrating the host " +
			"app's own data attributes from every clicked element")
	}
}
