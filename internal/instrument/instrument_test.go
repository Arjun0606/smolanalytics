package instrument

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTree materializes a fake repo under a temp dir for the scan/detect tests.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectFrameworkNextApp(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"dependencies":{"next":"15.0.0","react":"18"}}`,
		"tsconfig.json":  `{}`,
		"app/layout.tsx": "export default function L(){return null}",
	})
	fw := DetectFramework(root)
	if fw.Name != "next-app" {
		t.Fatalf("name = %q, want next-app", fw.Name)
	}
	if fw.Language != "typescript" {
		t.Errorf("language = %q, want typescript", fw.Language)
	}
	if fw.SnippetFile != "app/layout.tsx" {
		t.Errorf("snippet file = %q, want app/layout.tsx", fw.SnippetFile)
	}
}

func TestDetectFrameworkOthers(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"vite-react", map[string]string{"package.json": `{"dependencies":{"react":"18","vite":"5"}}`, "index.html": "<html></html>"}, "react"},
		{"go", map[string]string{"go.mod": "module x\n"}, "go"},
		{"rails", map[string]string{"Gemfile": "gem 'rails'\n"}, "rails"},
		{"django", map[string]string{"manage.py": "x", "requirements.txt": "django\n"}, "django"},
		{"static", map[string]string{"index.html": "<html></html>"}, "static"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectFramework(writeTree(t, c.files)).Name; got != c.want {
				t.Errorf("framework = %q, want %q", got, c.want)
			}
		})
	}
}

func TestScanFindsCallSitesWithExactSnippet(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":   `{"dependencies":{"next":"15","typescript":"5"}}`,
		"app/layout.tsx": "export default function L(){return null}",
		"app/api/auth/route.ts": `
export async function POST(req: Request) {
  const user = await supabase.auth.signUp({ email, password });
  return Response.json(user);
}`,
		"app/checkout/action.ts": `
export async function pay() {
  const session = await stripe.checkout.sessions.create({ mode: "subscription" });
  return session.url;
}`,
		// a line that already tracks must NOT be re-proposed
		"app/already.ts": `smolanalytics.track("login", { method: "google" });`,
	})
	p := Propose(root, "https://inst.fly.dev", "sa_testkey")

	if p.Framework.Name != "next-app" {
		t.Fatalf("framework = %q", p.Framework.Name)
	}
	if want := `<script src="https://inst.fly.dev/sdk.js"></script>`; !contains(p.Snippet.Code, want) {
		t.Errorf("snippet missing resolved host: %s", p.Snippet.Code)
	}
	if !contains(p.Snippet.Code, "sa_testkey") {
		t.Errorf("snippet missing resolved key: %s", p.Snippet.Code)
	}

	byEvent := map[string]CallSite{}
	for _, s := range p.Events {
		byEvent[s.Event] = s
	}
	// signup + checkout must be found; login was already tracked so must be skipped
	if s, ok := byEvent["signup"]; !ok {
		t.Errorf("signup not found; events=%v", p.Events)
	} else {
		if !contains(s.File, "route.ts") || s.Line == 0 {
			t.Errorf("signup site wrong: %+v", s)
		}
		if !contains(s.Snippet, `smolanalytics.track("signup"`) {
			t.Errorf("signup snippet wrong: %q", s.Snippet)
		}
	}
	if _, ok := byEvent["checkout"]; !ok {
		t.Errorf("checkout not found; events=%v", p.Events)
	}
	if s, ok := byEvent["login"]; ok {
		t.Errorf("login should be skipped (already tracked), got %+v", s)
	}

	// the plan the CLI/agent declares mirrors the found events, deduped
	names := map[string]bool{}
	for _, e := range p.PlanEvents() {
		names[e["name"].(string)] = true
	}
	if !names["signup"] || !names["checkout"] {
		t.Errorf("plan events missing signup/checkout: %v", p.PlanEvents())
	}
}

func TestScanSkipsNodeModules(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":              `{"dependencies":{"react":"18"}}`,
		"node_modules/pkg/index.js": `stripe.checkout.sessions.create({})`, // must be ignored
		"src/main.jsx":              `console.log("hi")`,
	})
	for _, s := range Propose(root, "h", "k").Events {
		if contains(s.File, "node_modules") {
			t.Fatalf("scanned node_modules: %+v", s)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestFindAllTrackedMatchesOptionalChaining pins the regression caught by end-to-end
// testing on a real repo (2026-07-14): agents commonly write track() calls with
// optional chaining or a window cast, and the scanner must see all of them or
// verify_instrumentation falsely reports correctly-wired events as MISSING.
func TestFindAllTrackedMatchesOptionalChaining(t *testing.T) {
	dir := t.TempDir()
	src := `
export function f() {
  smolanalytics.track("plain", { a: 1 });
  smolanalytics?.track("optchain", { b: 2 });
  (window as any).smolanalytics?.track("windowcast", { c: 3 });
  window.smolanalytics.track("windowdot");
}`
	if err := os.WriteFile(filepath.Join(dir, "app.tsx"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindAllTracked(dir)
	for _, want := range []string{"plain", "optchain", "windowcast", "windowdot"} {
		if _, ok := got[want]; !ok {
			t.Errorf("FindAllTracked missed %q — scanner blind to a real track() form; got %v", want, keysOf(got))
		}
	}
}

func keysOf(m map[string]TrackedEvent) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
