package main

// `smolanalytics plan check --source=posthog` — the instrumentation drift gate for
// teams whose events already live in PostHog. The plan file in the repo stays the
// single source of truth; the check reads the user's EXISTING PostHog project over
// its public query API and renders the exact same report with the exact same exit
// code as the server-backed check. No smolanalytics server, no migration, nothing
// written to the PostHog project — the query API is read-only.
//
// Two HogQL queries, always, regardless of plan size:
//  1. count + last-seen per planned event (one GROUP BY over the declared names)
//  2. one countIf column per planned (event, property) pair — "how many of these
//     events carried this property" in a single row
//
// One deliberate difference from the server-backed check: unplanned events are not
// reported. PostHog is only asked about the events the plan declares; enumerating
// a whole project's event namespace is their dashboard's job, not this gate's.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func runPlanCheckPostHog(file, phHost, phKey, phProject string, windowHours int, asJSON bool, out io.Writer) error {
	if err := validatePostHogFlags(phKey, phProject); err != nil {
		return err
	}
	pf, err := readPlanFile(file)
	if err != nil {
		return err
	}
	if err := validatePlanFile(pf); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	text, err := posthogHealth(pf, phHost, phKey, phProject, windowHours)
	if err != nil {
		return err
	}
	return renderAndGate(text, asJSON,
		"• unplanned events not checked — the posthog source verifies planned events only", out)
}

// validatePostHogFlags fails fast, before any file or network access, with the
// exact flag to fix and where its value comes from.
func validatePostHogFlags(phKey, phProject string) error {
	if phKey == "" {
		return fmt.Errorf("--source=posthog needs --ph-key — a PostHog personal API key with the query:read scope (PostHog → Settings → Personal API keys)")
	}
	if phProject == "" {
		return fmt.Errorf("--source=posthog needs --ph-project — the project id (PostHog → Settings → Project)")
	}
	return nil
}

// posthogHealth asks the PostHog project about the plan's events and returns a
// payload in the exact shape instrumentation_health serves, so renderAndGate
// treats both sources identically.
func posthogHealth(pf planFile, phHost, phKey, phProject string, windowHours int) (string, error) {
	names := make([]string, len(pf.Events))
	for i, e := range pf.Events {
		names[i] = hogqlString(e.Name)
	}
	where := "event IN (" + strings.Join(names, ", ") + ")"
	if windowHours > 0 {
		where += fmt.Sprintf(" AND timestamp > now() - INTERVAL %d HOUR", windowHours)
	}

	// query 1: total + last-seen per planned event.
	counts := map[string]int{}
	lastSeen := map[string]string{}
	rows, err := posthogQuery(phHost, phKey, phProject,
		"SELECT event, count(), max(timestamp) FROM events WHERE "+where+" GROUP BY event")
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		name, _ := row[0].(string)
		if n, ok := row[1].(float64); ok {
			counts[name] = int(n)
		}
		if ts, ok := row[2].(string); ok {
			lastSeen[name] = normalizeTimestamp(ts)
		}
	}

	// query 2: one countIf column per planned (event, property) pair — a single
	// request no matter how many properties the plan declares.
	type pair struct{ event, prop string }
	var cols []pair
	var exprs []string
	for _, e := range pf.Events {
		for _, p := range e.Properties {
			cols = append(cols, pair{e.Name, p})
			exprs = append(exprs, fmt.Sprintf("countIf(event = %s AND isNotNull(properties.%s))",
				hogqlString(e.Name), hogqlIdent(p)))
		}
	}
	present := map[pair]bool{}
	if len(exprs) > 0 {
		rows, err := posthogQuery(phHost, phKey, phProject,
			"SELECT "+strings.Join(exprs, ", ")+" FROM events WHERE "+where)
		if err != nil {
			return "", err
		}
		if len(rows) > 0 {
			for i, c := range cols {
				if i < len(rows[0]) {
					if n, ok := rows[0][i].(float64); ok && n > 0 {
						present[c] = true
					}
				}
			}
		}
	}

	// same rows, same status strings, same healthy rule as instrumentation_health.
	healthy := true
	report := make([]map[string]any, 0, len(pf.Events))
	for _, pe := range pf.Events {
		row := map[string]any{"event": pe.Name}
		if counts[pe.Name] == 0 {
			row["status"] = "MISSING — never seen"
			healthy = false
		} else {
			row["status"] = "flowing"
			row["count"] = counts[pe.Name]
			row["last_seen"] = lastSeen[pe.Name]
			var missing []string
			for _, prop := range pe.Properties {
				if !present[pair{pe.Name, prop}] {
					missing = append(missing, prop)
				}
			}
			if len(missing) > 0 {
				row["missing_properties"] = missing
				healthy = false
			}
		}
		report = append(report, row)
	}
	b, err := json.Marshal(map[string]any{
		"healthy":          healthy,
		"source":           "posthog",
		"planned":          report,
		"unplanned_events": []string{},
		"note":             "unplanned events are not checked — the posthog source verifies planned events only",
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// posthogQuery runs one HogQL query against PostHog's public query API and
// returns the result rows. Failure modes map to the flag to fix.
func posthogQuery(host, key, project, query string) ([][]any, error) {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"kind": "HogQLQuery", "query": query},
	})
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(host, "/") + "/api/projects/" + url.PathEscape(project) + "/query"
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %v — check --ph-host and your network", u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("PostHog returned %s — check --ph-key (needs a personal API key with query:read): %s", resp.Status, trimBody(raw))
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("PostHog returned 404 for project %q — check --ph-project, and --ph-host if the project lives on the EU cloud: %s", project, trimBody(raw))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("%s returned %s: %s", u, resp.Status, trimBody(raw))
	}
	var res struct {
		Results [][]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("unreadable PostHog response from %s — is that a PostHog host? (%s)", u, trimBody(raw))
	}
	return res.Results, nil
}

// normalizeTimestamp renders PostHog/ClickHouse timestamps the way the server
// renders last_seen (RFC3339 UTC); unrecognized shapes pass through untouched.
func normalizeTimestamp(s string) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}

// hogqlString renders s as a single-quoted HogQL string literal.
func hogqlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// hogqlIdent renders s as a backtick-quoted HogQL identifier — property keys are
// whatever humans typed into track() calls.
func hogqlIdent(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "`", "\\`")
	return "`" + s + "`"
}
