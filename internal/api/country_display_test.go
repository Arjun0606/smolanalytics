package api

import (
	"strings"
	"testing"
)

// A country row is two letters. "MD 0%" sitting under "US 3%" is a row most people cannot
// read, and the conversion-by-country card was rendering exactly that — no flag, no name.

func TestFlagOfBuildsRegionalIndicators(t *testing.T) {
	for code, want := range map[string]string{
		"US": "\U0001F1FA\U0001F1F8",
		"IN": "\U0001F1EE\U0001F1F3",
		"MD": "\U0001F1F2\U0001F1E9",
		"BR": "\U0001F1E7\U0001F1F7",
	} {
		if got := flagOf(code); got != want {
			t.Errorf("flagOf(%q) = %q, want %q", code, got, want)
		}
	}
}

// The arithmetic is only meaningful for A-Z. Anything else maps to some arbitrary codepoint
// pair and renders as two unrelated glyphs — a WRONG flag beside a real number, which is worse
// than no flag, because it is confidently incorrect rather than merely missing.
func TestFlagOfRefusesAnythingThatIsNotTwoLetters(t *testing.T) {
	for _, bad := range []string{"", "U", "USA", "us", "Us", "u1", "12", "Z9", "--", " U"} {
		if got := flagOf(bad); got != "" {
			t.Errorf("flagOf(%q) = %q, want empty — that is a garbage glyph pair, not a flag", bad, got)
		}
	}
}

func TestUnresolvedGeoGetsNoFlagAndNoCode(t *testing.T) {
	// MaxMind's markers for anonymous proxy / unresolved. They are not countries.
	rows := []segConv{{Value: "ZZ", Users: 4}, {Value: "XX", Users: 2}, {Value: "", Users: 1}}
	decorateCountrySegs(rows)
	for _, r := range rows {
		if r.Value != "Unknown" {
			t.Errorf("unresolved geo rendered as %q, want %q", r.Value, "Unknown")
		}
		if r.Flag != "" || r.Code != "" {
			t.Errorf("unresolved geo got flag %q / code %q — it should carry neither", r.Flag, r.Code)
		}
	}
}

func TestRealCountriesKeepTheirCodeSeparateFromTheLabel(t *testing.T) {
	rows := []segConv{{Value: "US", Users: 73, Conv: 3}, {Value: "IN", Users: 13, Conv: 38}}
	decorateCountrySegs(rows)
	for _, r := range rows {
		// Value stays the bare code. The flag used to be glued onto it, which made the string
		// the reader sees and the property value the same mangled thing — so the first feature
		// that needed the real code (this one) had to unpick an emoji from a filter value.
		if len(r.Value) != 2 {
			t.Errorf("Value is %q; it must stay the bare code, with the flag in its own field", r.Value)
		}
		if r.Code != r.Value {
			t.Errorf("Code %q does not match Value %q", r.Code, r.Value)
		}
		if r.Flag == "" || !strings.ContainsRune(r.Flag, 0x1F1E6+rune(r.Value[0])-'A') {
			t.Errorf("%s got flag %q, which is not its regional-indicator pair", r.Value, r.Flag)
		}
	}
}

// The full name is presentation only, so it is expanded in the browser rather than shipped as a
// table in the binary. What has to be true in the template is that the browser is GIVEN the code
// to expand, on both cards that show countries, and that the flag is hidden from assistive tech
// (the name is the label; a flag emoji read aloud is noise).
func TestCountryCardsHandTheBrowserSomethingToExpand(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if n := strings.Count(tpl, `data-cc="{{.Code}}"`); n < 2 {
		t.Errorf("only %d country card(s) emit data-cc; both the countries card and conversion-by-country need it", n)
	}
	if !strings.Contains(tpl, `<span class="cflag" aria-hidden="true">{{.Flag}}</span>`) {
		t.Error("the flag is not aria-hidden — a screen reader will read the emoji instead of the country")
	}
	for _, want := range []string{
		"function saCountryNames(",
		"Intl.DisplayNames",
		"navigator.languages",  // the reader's language, not ours
		"if(!full||full===cc)", // a tooltip repeating the thing you hover is worse than none
	} {
		if !strings.Contains(tpl, want) {
			t.Errorf("browser-side country naming lost %q", want)
		}
	}
}

// "1 users" was rendering under every single-user country in the screenshot that prompted this.
func TestUserCountsArePluralisedHonestly(t *testing.T) {
	tpl := dashboardTemplateSource(t)
	if strings.Contains(tpl, "{{.Users}} users<") {
		t.Error(`the segment row still hardcodes "users", so a one-user country reads "1 users"`)
	}
	if !strings.Contains(tpl, "{{.Users}} user{{if ne .Users 1}}s{{end}}") {
		t.Error("the segment row lost its pluralisation")
	}
}
