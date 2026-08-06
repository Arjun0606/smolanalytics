package mcp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/instrument"
	"github.com/Arjun0606/smolanalytics/internal/whatchanged"
)

// explain_change is the question every other product-analytics tool leaves unanswered.
//
// They can all tell you a number moved. None can tell you what moved it, because answering needs
// the repository and a SaaS vendor cannot have it. Here the MCP server runs in the editor beside
// the code, so the event can be traced to the call site that fires it, and the call site to the
// commits that touched it around the day the number changed.
//
// It reports CANDIDATES and says so. Correlating a commit with a metric change is evidence, not
// proof, and a tool that says "this commit caused it" will eventually be confidently wrong in
// front of someone who trusted it.
func (s *Server) toolExplainChange(args json.RawMessage) (string, error) {
	var a struct {
		Event    string `json:"event"`
		RepoPath string `json:"repo_path"`
		Days     int    `json:"days"`
	}
	if err := unmarshalArgs(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Event) == "" {
		return "", fmt.Errorf("event is required — the name of the metric that moved")
	}
	if a.RepoPath == "" {
		a.RepoPath = "."
	}
	if a.Days <= 0 {
		a.Days = 30
	}

	evs, err := s.all()
	if err != nil {
		return "", err
	}
	// Production scope, like every other tool in this package. This one read the raw store — the
	// single exception across the whole MCP surface — so asking "why did signup change" from an
	// editor could name a different day, or a different size of move, than /v1/explain answered
	// about the same event on the same instance. Two answers to one question, split by which
	// doorway you came through.
	evs = applyDefaultScope(evs)
	if err := s.checkEvents(a.Event); err != nil {
		return "", err
	}

	now := time.Now().UTC()
	// Trimmed, or the first day this event was ever sent reads as a 100% jump — see
	// TrimLeadingSilence. That fires for everyone in their first month, which is when they are
	// least able to tell a real finding from an artefact of the window.
	series := whatchanged.TrimLeadingSilence(whatchanged.Series(evs, a.Event, a.Days, now))
	change := whatchanged.Detect(series)

	out := map[string]any{
		"event":  a.Event,
		"window": fmt.Sprintf("%d days", a.Days),
		"series": series,
		"change": change,
	}

	if !change.Found {
		out["verdict"] = change.Verdict
		return jsonText(out)
	}

	// Where in the code this event is fired from. Without this the git search would have to look
	// at every commit in the window, which returns the whole week's work and explains nothing.
	tracked := instrument.FindAllTracked(a.RepoPath)
	site, hasSite := tracked[a.Event]
	if hasSite {
		out["fired_from"] = map[string]any{"file": site.File, "line": site.Line}
	}

	from, to, ok := whatchanged.Window(change.Day, 3)
	var commits []whatchanged.Commit
	var gitNote string
	if ok {
		commits, gitNote = gitLog(a.RepoPath, from, to, site.File, hasSite)
	}
	commits = whatchanged.Rank(commits, change.Day)
	if len(commits) > 8 {
		commits = commits[:8]
	}
	out["commits"] = commits
	if gitNote != "" {
		out["git_note"] = gitNote
	}

	// Deploy markers already recorded on this instance, near the same day. An independent second
	// signal: a deploy that lines up with the change is worth more than a commit that merely
	// touched the file.
	if s.deploys != nil {
		var near []map[string]any
		for _, d := range s.deploys.List() {
			day := d.At.UTC().Format("2006-01-02")
			if day >= from && day <= to {
				near = append(near, map[string]any{"at": d.At.UTC().Format(time.RFC3339), "sha": d.SHA, "message": d.Message, "source": d.Source})
			}
		}
		if len(near) > 0 {
			out["deploys_near_the_change"] = near
		}
	}

	out["verdict"] = verdictFor(a.Event, change, commits, hasSite, site.File, gitNote)
	return jsonText(out)
}

func verdictFor(name string, c whatchanged.Change, commits []whatchanged.Commit, hasSite bool, file, gitNote string) string {
	var b strings.Builder
	b.WriteString(strings.Replace(c.Verdict, "this event", name, 1))
	if !hasSite {
		b.WriteString(fmt.Sprintf(" No track(\"%s\") call was found in the repo, so the commits below are everything that changed in the window rather than only the code that fires this event. If the name is right, the call site may have been deleted — event_source will confirm.", name))
	} else {
		b.WriteString(fmt.Sprintf(" It is fired from %s.", file))
	}
	switch {
	case gitNote != "":
		b.WriteString(" " + gitNote)
	case len(commits) == 0:
		b.WriteString(" Nothing in the repo changed around that day, so this is more likely a change in traffic, a dependency, or something upstream than a change you shipped.")
	default:
		b.WriteString(fmt.Sprintf(" %d commit(s) touched that code within three days of the change, closest first. These are CANDIDATES: a commit landing near a metric move is evidence, not proof.", len(commits)))
	}
	return b.String()
}

// gitLog returns commits touching the file that fires the event, within the window. Everything
// here degrades to an explanation rather than an error: git may be absent, the directory may not
// be a repository, or the history may be shallow, and none of those should turn a useful answer
// into a failure.
func gitLog(repo, from, to, file string, scoped bool) ([]whatchanged.Commit, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "git is not installed here, so the commits that could explain this were not searched."
	}
	args := []string{"-C", repo, "log",
		"--since=" + from, "--until=" + to,
		"--date=iso-strict",
		"--pretty=format:%H%x1f%ad%x1f%an%x1f%s",
		"--no-merges", "-n", "40",
	}
	if scoped && file != "" {
		args = append(args, "--", file)
	}
	outB, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, "could not read git history here (not a repository, or no commits in range), so no commits are listed."
	}
	var out []whatchanged.Commit
	for _, line := range strings.Split(strings.TrimSpace(string(outB)), "\n") {
		if line == "" {
			continue
		}
		p := strings.Split(line, "\x1f")
		if len(p) < 4 {
			continue
		}
		sha := p[0]
		if len(sha) > 8 {
			sha = sha[:8]
		}
		out = append(out, whatchanged.Commit{SHA: sha, When: p[1], Author: p[2], Subject: p[3]})
	}
	return out, ""
}

var explainChangeToolDef = map[string]any{
	"name": "explain_change",
	"description": "Find WHEN an event's volume changed and WHICH commits could explain it. " +
		"This is the follow-up to any 'X is down' or 'X spiked' observation: it locates the day the series stepped to a new level, " +
		"finds the file that fires that event, and lists the commits that touched that file within three days either side, closest first. " +
		"Also surfaces any recorded deploys near the same day. " +
		"An event that falls to exactly zero is reported as STOPPED rather than as a percentage drop, because that is almost always broken tracking or a failed deploy rather than a product change. " +
		"Commits are CANDIDATES, not causes — say so when reporting them. " +
		"Needs the repo on this machine, which is why this works from the editor and not from a hosted dashboard.",
	"inputSchema": obj(map[string]any{
		"event":     map[string]any{"type": "string", "description": "The event whose volume moved, e.g. 'signup'"},
		"repo_path": map[string]any{"type": "string", "description": "Path to the repo root on this machine (default: current directory)"},
		"days":      map[string]any{"type": "integer", "description": "How much history to search for the change, default 30"},
	}, []string{"event"}),
}
