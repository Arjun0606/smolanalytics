package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func sqlCall(t *testing.T, s *Server, query string) map[string]any {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"query": query})
	out, err := s.callTool("run_sql", args)
	if err != nil {
		t.Fatalf("run_sql %q: %v", query, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("run_sql returned invalid JSON: %v\n%s", err, out)
	}
	return m
}

func TestRunSQLThroughTheToolSurface(t *testing.T) {
	s := newServer(t)
	m := sqlCall(t, s, "SELECT name, count(*) AS n FROM events GROUP BY name ORDER BY n DESC, name ASC")
	rows, _ := m["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %v", len(rows), m["rows"])
	}
	// Rows come back as objects so a model cannot misalign a column the way it can with a
	// positional array.
	first, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("rows should be objects keyed by column, got %T", rows[0])
	}
	if first["name"] != "signup" || first["n"].(float64) != 3 {
		t.Errorf("top row is %v, want signup with 3", first)
	}
}

func TestRunSQLMatchesTheDedicatedTools(t *testing.T) {
	s := newServer(t)
	// The SQL surface must agree with the purpose-built reports on the same question, or one of
	// them is lying and a user has no way to tell which.
	m := sqlCall(t, s, "SELECT count(distinct distinct_id) AS users FROM events")
	rows := m["rows"].([]any)
	got := rows[0].(map[string]any)["users"].(float64)

	overview, err := s.callTool("overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ov map[string]any
	if err := json.Unmarshal([]byte(overview), &ov); err != nil {
		t.Fatal(err)
	}
	want, ok := ov["total_users"].(float64)
	if !ok {
		t.Skipf("overview has no total_users field to compare against: %v", ov)
	}
	if got != want {
		t.Errorf("run_sql counted %v distinct users, overview reports %v — two tools disagree about the same number", got, want)
	}
}

// A parse failure must hand back the grammar. A model that guessed the dialect wrong can then
// fix it in the same turn instead of guessing again against something it cannot see.
func TestRunSQLErrorsCarryTheGrammar(t *testing.T) {
	s := newServer(t)
	args, _ := json.Marshal(map[string]any{"query": "SELECT * FROM events JOIN users ON x"})
	_, err := s.callTool("run_sql", args)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "FROM events") || !strings.Contains(err.Error(), "Not supported") {
		t.Errorf("error should include the grammar, got: %v", err)
	}
}

func TestRunSQLRefusesWritesThroughTheTool(t *testing.T) {
	s := newServer(t)
	for _, q := range []string{
		"DELETE FROM events",
		"DROP TABLE events",
		"UPDATE events SET name='x'",
		"SELECT name FROM events; DELETE FROM events",
	} {
		args, _ := json.Marshal(map[string]any{"query": q})
		if _, err := s.callTool("run_sql", args); err == nil {
			t.Errorf("run_sql accepted a write: %q", q)
		}
	}
}

// An empty result must say why rather than returning a bare empty list, which a model reads as
// "there is no data" when the real cause is usually a mistyped property key.
func TestRunSQLEmptyResultExplainsItself(t *testing.T) {
	s := newServer(t)
	m := sqlCall(t, s, "SELECT name FROM events WHERE name = 'nonexistent'")
	if n := m["row_count"].(float64); n != 0 {
		t.Fatalf("expected 0 rows, got %v", n)
	}
	note, _ := m["note"].(string)
	if !strings.Contains(note, "case-sensitive") {
		t.Errorf("empty result should hint at the usual cause, got note %q", note)
	}
}

func TestRunSQLIsListedWithItsGrammar(t *testing.T) {
	s := newServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	b, _ := json.Marshal(resp.Result)
	var got struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, tl := range got.Tools {
		if tl.Name != "run_sql" {
			continue
		}
		// The dialect is a subset, so the description has to state the surface AND what is
		// missing — otherwise the model writes valid ANSI SQL and gets rejected.
		for _, must := range []string{"SELECT", "prop.", "GROUP BY", "Not supported", "count(distinct"} {
			if !strings.Contains(tl.Description, must) {
				t.Errorf("run_sql description is missing %q — a model cannot write correct queries without it", must)
			}
		}
		return
	}
	t.Fatal("run_sql is not in tools/list — a tool the model cannot see does not exist")
}

// The public demo serves everything read-only. run_sql cannot write by construction, so it must
// stay available there rather than being swept up as a mutating tool.
func TestRunSQLWorksOnTheReadOnlyDemo(t *testing.T) {
	s := newServer(t)
	s.readOnly = true
	if _, err := s.callTool("run_sql", mustJSON(map[string]any{"query": "SELECT count(*) AS n FROM events"})); err != nil {
		t.Errorf("run_sql should work on the read-only demo, got: %v", err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
