package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/event"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

// explain_change answers the question every events-only tool leaves open. These pin that it
// answers it honestly: candidates presented as candidates, and a graceful result when there is
// no repo, no git, or nothing to explain.

// gitRepoFiring builds a real git repository whose committed code fires `eventName`, so the tool
// is exercised against actual git output rather than a stub. Real git is the point: parsing its
// output is where this would break in the field.
func gitRepoFiring(t *testing.T, eventName string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Commits are dated relative to now so they land inside the window around the change the
	// fixture below creates. Dating them "now" would put them outside it, which is correct
	// behaviour from the tool and a broken fixture.
	runAt := func(daysAgo int, args ...string) {
		t.Helper()
		when := time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run := func(args ...string) { t.Helper(); runAt(0, args...) }
	run("init", "-q")
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(src, "checkout.ts")
	body := "export function pay() {\n  smolanalytics.track(\"" + eventName + "\");\n}\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	runAt(20, "commit", "-q", "-m", "add checkout tracking") // long before the change
	// The suspect: a commit touching the same file on the day the metric fell.
	if err := os.WriteFile(f, []byte(body+"// tweak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	runAt(8, "commit", "-q", "-m", "refactor checkout handler")
	return dir
}

// serverWithDrop builds an instance where `name` runs steady then collapses, which is the shape
// someone actually asks about.
func serverWithDrop(t *testing.T, name string, dropToZero bool) *Server {
	t.Helper()
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	id := 0
	add := func(day int, n int) {
		for i := 0; i < n; i++ {
			id++
			evs = append(evs, event.Event{
				ID: fmt.Sprint(id), Name: name, DistinctID: fmt.Sprintf("u%d", i),
				Timestamp: now.AddDate(0, 0, -day).Add(-time.Hour),
			})
		}
	}
	for d := 20; d > 8; d-- {
		add(d, 40)
	}
	after := 0
	if !dropToZero {
		after = 5
	}
	for d := 8; d >= 1; d-- {
		add(d, after)
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}
	return New(st)
}

func explain(t *testing.T, s *Server, name, repo string) map[string]any {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"event": name, "repo_path": repo, "days": 21})
	out, err := s.callTool("explain_change", args)
	if err != nil {
		t.Fatalf("explain_change: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return m
}

func TestExplainChangeFindsTheCommitsThatTouchedTheCallSite(t *testing.T) {
	repo := gitRepoFiring(t, "checkout")
	s := serverWithDrop(t, "checkout", false)

	m := explain(t, s, "checkout", repo)
	change, _ := m["change"].(map[string]any)
	if change["found"] != true {
		t.Fatalf("a 40/day to 5/day fall should be detected: %v", m["verdict"])
	}
	if change["direction"] != "drop" {
		t.Errorf("direction %v, want drop", change["direction"])
	}
	// It must know where the event is fired from — that is what scopes the git search and is the
	// whole reason this beats listing every commit in the week.
	fired, _ := m["fired_from"].(map[string]any)
	if fired == nil || !strings.Contains(fired["file"].(string), "checkout.ts") {
		t.Errorf("should have located the call site, got %v", m["fired_from"])
	}
	commits, _ := m["commits"].([]any)
	if len(commits) == 0 {
		t.Fatalf("expected commits touching the call site, got none (git_note: %v)", m["git_note"])
	}
	first, _ := commits[0].(map[string]any)
	for _, k := range []string{"sha", "when", "author", "subject"} {
		if first[k] == nil || first[k] == "" {
			t.Errorf("commit is missing %s: %v", k, first)
		}
	}
	// Honesty: a commit near a metric move is evidence, not proof, and the verdict must say so.
	v := m["verdict"].(string)
	if !strings.Contains(v, "CANDIDATES") {
		t.Errorf("verdict must present commits as candidates rather than causes, got %q", v)
	}
}

// An event falling to exactly zero is the highest-signal case and must not be reported as just
// another percentage drop — it is nearly always broken tracking or a failed deploy.
func TestEventStoppingIsReportedAsStopped(t *testing.T) {
	repo := gitRepoFiring(t, "checkout")
	s := serverWithDrop(t, "checkout", true)

	m := explain(t, s, "checkout", repo)
	v := m["verdict"].(string)
	if !strings.Contains(v, "STOPPED") {
		t.Errorf("an event going to zero should be called out as stopped, got %q", v)
	}
	if !strings.Contains(v, "broken tracking") {
		t.Errorf("verdict should name the likely cause, got %q", v)
	}
}

// A steady metric must produce "nothing changed", not a manufactured explanation. A tool that
// always finds a culprit is worse than one that finds none.
func TestSteadyMetricGetsNoExplanation(t *testing.T) {
	st := memory.New()
	now := time.Now().UTC()
	var evs []event.Event
	id := 0
	for d := 20; d >= 1; d-- {
		for i := 0; i < 30; i++ {
			id++
			evs = append(evs, event.Event{ID: fmt.Sprint(id), Name: "steady",
				DistinctID: fmt.Sprintf("u%d", i), Timestamp: now.AddDate(0, 0, -d)})
		}
	}
	if err := st.Ingest(evs...); err != nil {
		t.Fatal(err)
	}
	s := New(st)

	m := explain(t, s, "steady", t.TempDir())
	change, _ := m["change"].(map[string]any)
	if change["found"] == true {
		t.Errorf("invented a change in a flat series: %v", m["verdict"])
	}
	if m["commits"] != nil {
		t.Error("no change means no commits should be blamed")
	}
}

// Not a git repository must degrade to a useful answer with an explanation, never an error. The
// change detection is still worth having without the repo half.
func TestNoRepoStillReportsTheChange(t *testing.T) {
	s := serverWithDrop(t, "checkout", false)
	m := explain(t, s, "checkout", t.TempDir()) // empty dir, no git history

	change, _ := m["change"].(map[string]any)
	if change["found"] != true {
		t.Fatal("the change should still be detected without a repo")
	}
	note, _ := m["git_note"].(string)
	if note == "" {
		t.Error("should explain why no commits were listed")
	}
	if !strings.Contains(m["verdict"].(string), strings.Split(note, ".")[0]) {
		t.Errorf("the verdict should carry the explanation, got %q", m["verdict"])
	}
}

// A typo must not be silently analysed into a confident "nothing changed".
func TestUnknownEventErrors(t *testing.T) {
	s := serverWithDrop(t, "checkout", false)
	args, _ := json.Marshal(map[string]any{"event": "chekout", "repo_path": t.TempDir()})
	if _, err := s.callTool("explain_change", args); err == nil {
		t.Error("a misspelled event name should error rather than report no change")
	}
}

func TestExplainChangeIsListedWithItsCaveats(t *testing.T) {
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
		if tl.Name != "explain_change" {
			continue
		}
		for _, must := range []string{"CANDIDATES", "repo", "STOPPED"} {
			if !strings.Contains(tl.Description, must) {
				t.Errorf("description missing %q — the model needs the caveat as much as the capability", must)
			}
		}
		return
	}
	t.Fatal("explain_change is not in tools/list")
}
