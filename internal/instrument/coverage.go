package instrument

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Coverage answers the question no events-only tool can even ask: what does this product DO that
// nothing is measuring?
//
// Every analytics tool can describe the events it receives. None can name the ones that were
// never written, because absence is invisible in event data — a checkout nobody instrumented
// looks exactly like a checkout nobody performed. Answering needs the source, which is why this
// works from the editor and cannot be a hosted feature for anyone.
//
// It is deliberately conservative. A coverage report that flags every onClick in a codebase is
// noise, gets dismissed once, and is never opened again — so this only reports actions that are
// almost always worth a tracked event: form submissions, auth flows, payment, and mutating API
// route handlers.

// Action is one user-facing thing the code does.
type Action struct {
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
	// Suggest is the event name to use if this gets instrumented. Named here rather than left to
	// the caller so two people instrumenting the same codebase produce the same event names.
	Suggest string `json:"suggested_event,omitempty"`
	Covered bool   `json:"covered"`
	// CoveredBy is the track() call that covers it, when one is nearby.
	CoveredBy string `json:"covered_by,omitempty"`
}

// CoverageReport is the whole picture: what the product does, and how much of it is measured.
type CoverageReport struct {
	Actions   []Action `json:"actions"`
	Covered   int      `json:"covered"`
	Uncovered int      `json:"uncovered"`
	Percent   int      `json:"coverage_pct"`
	Headline  string   `json:"headline"`
}

// nearbyLines is how far from an action a track() call may sit and still count as covering it.
// Tracking is nearly always in the same handler as the action, and a handler is rarely longer
// than this. Too tight and every real call reads as a miss; too loose and one tracked function
// covers the whole file.
const nearbyLines = 12

type actionPattern struct {
	kind    string
	suggest string
	re      *regexp.Regexp
}

// The patterns require CALL SYNTAX, not a mention of a word.
//
// The first version matched anywhere the word appeared and produced 497 "untracked actions" on a
// real 550-file codebase — 10% covered — where the findings were `import { signOut }`, the line
// `export default function SignOutButton()`, the JSX text "sign out", and `redirect("/login")`
// repeated five times in one file. None of those is a user action. A coverage report like that is
// dismissed once and never opened again, which is worse than not shipping it, so every pattern
// below has to match something being INVOKED.
var actionPatterns = []actionPattern{
	{"payment", "checkout", regexp.MustCompile(`(?i)(stripe\.\w+\.\w*create\s*\(|checkout\.sessions?\.create\s*\(|\bcreateCheckoutSession\s*\(|\bcreateSubscription\s*\(|\.subscriptions?\.create\s*\(|\bpaymentIntents?\.create\s*\(|\.charges?\.create\s*\()`)},
	{"signup", "signup", regexp.MustCompile(`(?i)(\b(auth\.)?sign[_-]?up(\.\w+)?\s*\(|\bsignUpWith\w*\s*\(|\bcreateUserWith\w*\s*\(|\bcreateUser\s*\(|\bregisterUser\s*\(|\bregisterAccount\s*\(|\.users\.create\s*\()`)},
	{"login", "login", regexp.MustCompile(`(?i)(\b(auth\.)?sign[_-]?in(\.\w+)?\s*\(|\bsignInWith\w*\s*\(|\blog[_-]?in\s*\(|\bauthenticate\s*\()`)},
	{"logout", "logout", regexp.MustCompile(`(?i)(\b(auth\.)?sign[_-]?out\s*\(|\blog[_-]?out\s*\()`)},
	{"form submit", "form_submitted", regexp.MustCompile(`(?i)(onsubmit\s*=|\bhandleSubmit\s*\(|<form\b)`)},
	{"invite", "invite_sent", regexp.MustCompile(`(?i)(send[_-]?invite\w*\s*\(|\binviteUser\s*\(|\binviteMember\s*\(|createInvit\w*\s*\()`)},
	{"upload", "file_uploaded", regexp.MustCompile(`(?i)(\buploadFile\s*\(|\bhandleUpload\s*\(|\.upload\s*\()`)},
	{"share", "shared", regexp.MustCompile(`(?i)(\bcreateShareLink\s*\(|\bhandleShare\s*\(|\bshareLink\s*\()`)},
	{"delete account", "account_deleted", regexp.MustCompile(`(?i)(\bdeleteAccount\s*\(|\bdeleteUser\s*\(|\bcloseAccount\s*\()`)},
	{"api mutation", "api_called", regexp.MustCompile(`^\s*export\s+(async\s+)?function\s+(POST|PUT|PATCH|DELETE)\s*\(`)},
	{"api mutation", "api_called", regexp.MustCompile(`(?i)\b(router|app)\.(post|put|patch|delete)\s*\(`)},
}

// notAnAction rejects lines that mention an action without performing one. Each of these was a
// real false positive from running the first version against a live codebase.
var notAnAction = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(import|export)\s`),                                                               // import { signOut } from …
	regexp.MustCompile(`^\s*(//|/\*|\*|#)`),                                                                   // comments
	regexp.MustCompile(`^\s*(type|interface|declare)\s`),                                                      // type declarations
	regexp.MustCompile(`\bfrom\s+["'` + "`" + `]`),                                                            // … from "@/lib/auth-client"
	regexp.MustCompile(`^\s*(function|const|let|var)\s+\w+\s*[=:(]\s*(async\s*)?\(?\s*\)?\s*(=>)?\s*\{?\s*$`), // a declaration, not a call
}

// isDeclaredNotCalled keeps `export async function POST(` — a route handler IS the action — while
// rejecting other declarations. Route handlers are the one case where declaring is doing.
func skipAsNonAction(line string, kind string) bool {
	if kind == "api mutation" {
		// Only the import/comment guards apply; declaring a POST handler is the action itself.
		return notAnAction[0].MatchString(line) && !strings.Contains(line, "function") ||
			notAnAction[1].MatchString(line)
	}
	for _, re := range notAnAction {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// trackNearby matches any tracking call, including the optional-chaining forms agents generate.
var trackNearby = regexp.MustCompile(`smolanalytics\??\.track\(|/v1/events`)

// Report walks the repository and returns every user-facing action it can identify, marked as
// covered or not.
func Report(root string) CoverageReport {
	var actions []Action
	seen := map[string]bool{} // one action per file:line, even if several patterns match

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if nonAppFile(rel) { // tests, stories and mocks are not the product
			return nil
		}
		info, e := d.Info()
		if e != nil || info.Size() > 512*1024 {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		lines := strings.Split(string(b), "\n")

		for i, raw := range lines {
			line := strings.TrimSpace(raw)
			if line == "" || len(line) > 400 || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "*") {
				continue
			}
			// A line that IS the tracking call is not an untracked action.
			if trackNearby.MatchString(line) {
				continue
			}
			for _, ap := range actionPatterns {
				if !ap.re.MatchString(line) {
					continue
				}
				if skipAsNonAction(line, ap.kind) {
					continue
				}
				key := fmt.Sprintf("%s:%d", rel, i+1)
				if seen[key] {
					continue
				}
				// One form is one action. A JSX form usually spans several lines and matches on
				// both `<form` and `onSubmit=`, which would report the same submission twice and
				// make the count look worse than the codebase is.
				if n := len(actions); n > 0 {
					last := actions[n-1]
					if last.File == rel && last.Kind == ap.kind && i+1-last.Line <= 3 {
						seen[key] = true
						continue
					}
				}
				seen[key] = true
				covered, by := coveredNear(lines, i)
				actions = append(actions, Action{
					Kind: ap.kind, File: rel, Line: i + 1,
					Snippet: truncate(line, 160), Suggest: ap.suggest,
					Covered: covered, CoveredBy: by,
				})
				break
			}
		}
		return nil
	})

	// Deterministic order: uncovered first (that is the list someone acts on), then by location.
	sort.SliceStable(actions, func(a, b int) bool {
		if actions[a].Covered != actions[b].Covered {
			return !actions[a].Covered
		}
		if actions[a].File != actions[b].File {
			return actions[a].File < actions[b].File
		}
		return actions[a].Line < actions[b].Line
	})

	rep := CoverageReport{Actions: actions}
	for _, a := range actions {
		if a.Covered {
			rep.Covered++
		} else {
			rep.Uncovered++
		}
	}
	if n := len(actions); n > 0 {
		rep.Percent = int(float64(rep.Covered)/float64(n)*100 + 0.5)
	}
	rep.Headline = coverageHeadline(rep)
	return rep
}

// coveredNear reports whether a tracking call sits close enough to this action to be measuring
// it, and returns the call that does.
func coveredNear(lines []string, idx int) (bool, string) {
	lo := idx - nearbyLines
	if lo < 0 {
		lo = 0
	}
	hi := idx + nearbyLines
	if hi > len(lines)-1 {
		hi = len(lines) - 1
	}
	for j := lo; j <= hi; j++ {
		if trackNearby.MatchString(lines[j]) {
			if m := trackCallRe.FindStringSubmatch(lines[j]); len(m) > 1 {
				return true, m[1]
			}
			return true, strings.TrimSpace(truncate(lines[j], 80))
		}
	}
	return false, ""
}

func coverageHeadline(r CoverageReport) string {
	switch {
	case len(r.Actions) == 0:
		return "No recognisable user-facing actions found. Either this is not an application repository, or repo_path is pointing at the wrong directory."
	case r.Uncovered == 0:
		return fmt.Sprintf("All %d user-facing actions found in this code have tracking nearby.", len(r.Actions))
	default:
		kinds := map[string]int{}
		for _, a := range r.Actions {
			if !a.Covered {
				kinds[a.Kind]++
			}
		}
		names := make([]string, 0, len(kinds))
		for k := range kinds {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, k := range names {
			parts = append(parts, fmt.Sprintf("%d %s", kinds[k], k))
		}
		return fmt.Sprintf("%d of %d user-facing actions have no tracking near them (%d%% covered): %s. These are things the product does that no report can currently show, because an action nobody instrumented looks identical to one nobody performed.",
			r.Uncovered, len(r.Actions), r.Percent, strings.Join(parts, ", "))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
