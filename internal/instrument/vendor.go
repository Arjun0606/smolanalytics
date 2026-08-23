package instrument

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WRITE INTO THE ANALYTICS THEY ALREADY HAVE.
//
// Every call this package emitted was `smolanalytics.track(...)`, which quietly made instrumentation a
// migration: to let us write your tracking you first had to adopt our SDK. That is the opposite of the
// promise on the site, where we say keep PostHog, GA4 or Plausible exactly where they are.
//
// The job worth owning is not collection, it is INSTRUMENTATION: deciding what to track, writing the
// calls, keeping the names consistent, noticing when one breaks and putting it back. Nobody wants that
// job and every team has it. Doing it in the customer's own SDK means we maintain their tracking
// without touching their data or their bill.
//
// Detection reads the repo rather than asking. A codebase already tells you what it uses, and a
// question at setup time is a step where people leave.

// Vendor is an analytics SDK we can write calls for.
type Vendor struct {
	// ID is stable and lower case: posthog, mixpanel, amplitude, ga4, segment, smolanalytics.
	ID string
	// Name is how a human refers to it, for the PR body and the report.
	Name string
	// detect matches the SDK being LOADED or CALLED, not merely mentioned in a comment or a doc.
	detect *regexp.Regexp
	// track matches one tracking call by this SDK and captures the event name in submatch 1.
	// Narrower than detect on purpose: detect answers "do they use this", track answers "is THIS
	// LINE a tracked event, and which one".
	track *regexp.Regexp
	// call renders one tracking call in this SDK's own idiom.
	call func(event string, props []string) string
	// serverCall renders the call for a backend language, keyed by the Framework.Language value.
	// Absent where we would be guessing; the caller must say so rather than emit ours.
	serverCall map[string]func(event string, props []string) string
	// install is what a person has to do before the calls above will run. Empty when the SDK is
	// already initialised in their app, which is the whole point of detecting it.
	install string
}

// Ordered by how specific the signature is. Segment last: analytics.track() is generic enough that a
// wrapper around another SDK often matches it, so anything more distinctive wins first.
var vendors = []Vendor{
	{
		ID: "posthog", Name: "PostHog",
		detect: regexp.MustCompile(`(?i)(posthog-js|posthog-node|\bposthog\.init\s*\(|\bposthog\??\.capture\s*\(|from ["']posthog)`),
		track:  regexp.MustCompile("\\bposthog\\??\\.capture\\(\\s*[\"'`]([^\"'`]+)"),
		call: func(e string, p []string) string {
			if len(p) == 0 {
				return `posthog.capture("` + e + `");`
			}
			return `posthog.capture("` + e + `", { ` + placeholders(p) + ` });`
		},
		serverCall: map[string]func(string, []string) string{
			"python": func(e string, p []string) string {
				return `posthog.capture(distinct_id=user_id, event="` + e + `"` + pyKwargProps(p) + `)`
			},
			"ruby": func(e string, p []string) string {
				return `posthog.capture(distinct_id: current_user.id, event: "` + e + `"` + rubyKwargProps(p) + `)`
			},
			"go": func(e string, p []string) string {
				return `client.Enqueue(posthog.Capture{DistinctId: userID, Event: "` + e + `"` + goPropsField(p) + `})`
			},
		},
	},
	{
		ID: "mixpanel", Name: "Mixpanel",
		detect: regexp.MustCompile(`(?i)(mixpanel-browser|\bmixpanel\.(init|track)\s*\(|from ["']mixpanel)`),
		track:  regexp.MustCompile("\\bmixpanel\\??\\.track\\(\\s*[\"'`]([^\"'`]+)"),
		call: func(e string, p []string) string {
			if len(p) == 0 {
				return `mixpanel.track("` + e + `");`
			}
			return `mixpanel.track("` + e + `", { ` + placeholders(p) + ` });`
		},
		serverCall: map[string]func(string, []string) string{
			"python": func(e string, p []string) string {
				return `mp.track(user_id, "` + e + `", {` + pyDictProps(p) + `})`
			},
			"ruby": func(e string, p []string) string {
				return `tracker.track(current_user.id, "` + e + `", {` + rubyProps(p) + `})`
			},
		},
	},
	{
		ID: "amplitude", Name: "Amplitude",
		detect: regexp.MustCompile(`(?i)(@amplitude/|\bamplitude\.(init|track|logEvent)\s*\()`),
		track:  regexp.MustCompile("\\bamplitude\\??\\.(?:track|logEvent)\\(\\s*[\"'`]([^\"'`]+)"),
		call: func(e string, p []string) string {
			if len(p) == 0 {
				return `amplitude.track("` + e + `");`
			}
			return `amplitude.track("` + e + `", { ` + placeholders(p) + ` });`
		},
		serverCall: map[string]func(string, []string) string{
			"python": func(e string, p []string) string {
				return `amplitude.track(BaseEvent(event_type="` + e + `", user_id=user_id, event_properties={` + pyDictProps(p) + `}))`
			},
		},
	},
	{
		ID: "ga4", Name: "Google Analytics 4",
		detect: regexp.MustCompile(`(\bgtag\s*\(|googletagmanager\.com/gtag|\bG-[A-Z0-9]{8,}\b)`),
		track:  regexp.MustCompile("\\bgtag\\(\\s*[\"']event[\"']\\s*,\\s*[\"'`]([^\"'`]+)"),
		call: func(e string, p []string) string {
			// GA4's call takes the literal "event" first and the name second, and its names are
			// snake_case by convention — getting either wrong produces a call that runs and
			// records nothing, which is the worst kind of wrong.
			if len(p) == 0 {
				return `gtag("event", "` + e + `");`
			}
			return `gtag("event", "` + e + `", { ` + placeholders(p) + ` });`
		},
		// No serverCall: GA4 has no server SDK, only the Measurement Protocol, which needs an API
		// secret we have not been given. Writing a guess here would be a call that silently drops.
	},
	{
		ID: "segment", Name: "Segment",
		detect: regexp.MustCompile(`(?i)(@segment/analytics|\banalytics\.track\s*\(|segment\.com/analytics)`),
		track:  regexp.MustCompile("\\banalytics\\??\\.track\\(\\s*[\"'`]([^\"'`]+)"),
		call: func(e string, p []string) string {
			if len(p) == 0 {
				return `analytics.track("` + e + `");`
			}
			return `analytics.track("` + e + `", { ` + placeholders(p) + ` });`
		},
		serverCall: map[string]func(string, []string) string{
			"python": func(e string, p []string) string {
				return `analytics.track(user_id, "` + e + `", {` + pyDictProps(p) + `})`
			},
			"ruby": func(e string, p []string) string {
				return `Analytics.track(user_id: current_user.id, event: "` + e + `", properties: {` + rubyProps(p) + `})`
			},
		},
	},
}

// Smolanalytics is the fallback, used when the repo has no analytics SDK at all. It is the only
// vendor with a non-empty install: everyone else is already running.
var Smolanalytics = Vendor{
	ID: "smolanalytics", Name: "smolanalytics",
	track:   regexp.MustCompile("\\bsmolanalytics\\??\\.track\\(\\s*[\"'`]([^\"'`]+)"),
	install: "add the snippet below to your root layout",
	call: func(e string, p []string) string {
		if len(p) == 0 {
			return `smolanalytics.track("` + e + `");`
		}
		return `smolanalytics.track("` + e + `", { ` + placeholders(p) + ` });`
	},
}

// Call renders one tracking call in this vendor's idiom, for the browser.
func (v Vendor) Call(event string, props []string) string { return v.call(event, props) }

// ServerCall renders the call for a backend language. The second return is false when we have no
// confident idiom for that pair, and the caller must then say so rather than fall back to ours:
// writing smolanalytics.track() into a PostHog codebase is how instrumentation becomes a migration.
func (v Vendor) ServerCall(lang, event string, props []string) (string, bool) {
	f, ok := v.serverCall[lang]
	if !ok {
		return "", false
	}
	return f(event, props), true
}

// IsOurs reports whether this is our own SDK rather than one the customer already had.
func (v Vendor) IsOurs() bool { return v.ID == Smolanalytics.ID }

// VendorInfo is the JSON-safe view of a Vendor, for the MCP result and the PR body. It exists so
// the surfaces that consume a proposal can NAME the SDK they are writing into: a pull request that
// changes analytics code without saying which analytics it touched is one nobody merges.
type VendorInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Install is empty when their SDK is already running, which is the usual case and the reason
	// there is nothing to set up.
	Install string `json:"install,omitempty"`
	// Detected is false only for the smolanalytics fallback, where we found no analytics at all.
	Detected bool `json:"detected"`
}

// Info renders the vendor for JSON consumers.
func (v Vendor) Info() VendorInfo {
	return VendorInfo{ID: v.ID, Name: v.Name, Install: v.install, Detected: !v.IsOurs()}
}

// anyTrack matches a tracking call by ANY known SDK, ours included, plus our HTTP ingest path.
//
// This is the "is this action measured?" test, and a false NEGATIVE here is the expensive
// direction: it tells someone with working PostHog tracking that 0% of their product is measured,
// and then proposes a second SDK's call beside every existing one — double-counting their
// conversions. Generous on purpose.
var anyTrack = func() *regexp.Regexp {
	parts := []string{`/v1/events`}
	for _, v := range append(append([]Vendor{}, vendors...), Smolanalytics) {
		parts = append(parts, v.track.String())
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}()

// TracksSomething reports whether the line contains any SDK's tracking call.
func TracksSomething(line string) bool { return anyTrack.MatchString(line) }

// EventNameOn returns the event name tracked on this line, in whichever SDK wrote it.
//
// Per-vendor patterns rather than one union with a shared capture group, because the name is not
// in the same argument position in every SDK: gtag("event", "signup") puts the literal "event"
// where PostHog puts the name, so a positional guess would record every GA4 event as "event".
func EventNameOn(line string) (string, bool) {
	for _, v := range append(append([]Vendor{}, vendors...), Smolanalytics) {
		if m := v.track.FindStringSubmatch(line); m != nil {
			return m[1], true
		}
	}
	return "", false
}

// The property renderers below exist per language because the shape differs and a copy-paste-ready
// snippet is the whole promise: a Python call with JS object literals in it is not paste-ready.

func pyKwargProps(props []string) string {
	if len(props) == 0 {
		return ""
	}
	return `, properties={` + pyDictProps(props) + `}`
}

func pyDictProps(props []string) string {
	parts := make([]string, len(props))
	for i, p := range props {
		parts[i] = `"` + p + `": None`
	}
	return strings.Join(parts, ", ")
}

func rubyKwargProps(props []string) string {
	if len(props) == 0 {
		return ""
	}
	return `, properties: {` + rubyProps(props) + `}`
}

func goPropsField(props []string) string {
	if len(props) == 0 {
		return ""
	}
	out := ", Properties: posthog.NewProperties()"
	for _, p := range props {
		out += `.Set("` + p + `", ` + p + `)`
	}
	return out
}

func placeholders(props []string) string {
	pairs := make([]string, len(props))
	for i, p := range props {
		pairs[i] = p + ": " + "/* " + p + " */"
	}
	return strings.Join(pairs, ", ")
}

// DetectVendor reports which analytics SDK the repository already uses.
//
// Package manifests are read first and weighted above source, because a dependency is a decision while
// a source match can be a leftover: a file that still calls mixpanel.track after the team moved to
// PostHog would otherwise win. When nothing is found we return smolanalytics, and the caller should say
// so plainly rather than silently installing us.
func DetectVendor(root string) Vendor {
	hits := map[string]int{}

	// manifests first, weighted
	for _, name := range []string{"package.json", "requirements.txt", "Gemfile", "go.mod"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		for _, v := range vendors {
			if v.detect.Match(b) {
				hits[v.ID] += 10
			}
		}
	}

	// then source, one point each, bounded so a big repo does not cost a minute
	scanned := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || scanned > 600 {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		scanned++
		for _, v := range vendors {
			if v.detect.Match(b) {
				hits[v.ID]++
			}
		}
		return nil
	})

	best, bestN := Smolanalytics, 0
	for _, v := range vendors {
		if hits[v.ID] > bestN {
			best, bestN = v, hits[v.ID]
		}
	}
	return best
}
