package instrument

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func find(r CoverageReport, file string, kind string) (Action, bool) {
	for _, a := range r.Actions {
		if a.File == file && a.Kind == kind {
			return a, true
		}
	}
	return Action{}, false
}

// The headline capability: naming a thing the product does that nothing measures. An untracked
// checkout is invisible in event data — it looks exactly like a checkout nobody performed.
func TestFindsAnUntrackedAction(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/pay.ts": `export async function pay() {
  const session = await stripe.checkout.sessions.create({ mode: "payment" });
  return session.url;
}`,
	})
	r := Report(repo)
	a, ok := find(r, "src/pay.ts", "payment")
	if !ok {
		t.Fatalf("missed an untracked checkout: %+v", r.Actions)
	}
	if a.Covered {
		t.Error("checkout has no track() anywhere near it but was reported as covered")
	}
	if a.Suggest == "" {
		t.Error("should suggest an event name so two people instrumenting this agree")
	}
	if r.Uncovered != 1 || r.Percent != 0 {
		t.Errorf("summary wrong: %d uncovered, %d%%", r.Uncovered, r.Percent)
	}
	if !strings.Contains(r.Headline, "no tracking") {
		t.Errorf("headline should state the finding, got %q", r.Headline)
	}
}

// Tracking is nearly always a few lines from the action, not on the same line. Requiring an
// exact-line match would report every correctly-instrumented codebase as untracked.
func TestTrackingNearbyCounlsAsCovered(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/pay.ts": `export async function pay() {
  const session = await stripe.checkout.sessions.create({ mode: "payment" });
  console.log("ok");
  smolanalytics.track("checkout", { plan: "pro" });
  return session.url;
}`,
	})
	r := Report(repo)
	a, ok := find(r, "src/pay.ts", "payment")
	if !ok {
		t.Fatal("action not found at all")
	}
	if !a.Covered {
		t.Error("a track() call three lines away should count as covering this action")
	}
	if a.CoveredBy != "checkout" {
		t.Errorf("should name the covering event, got %q", a.CoveredBy)
	}
	if r.Percent != 100 {
		t.Errorf("coverage %d%%, want 100", r.Percent)
	}
}

// Too loose and one tracked function covers a whole file, which reports a codebase as measured
// when it is not. The window has to end somewhere.
func TestTrackingFarAwayDoesNotCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("export async function pay() {\n")
	b.WriteString("  const s = await stripe.checkout.sessions.create({});\n")
	for i := 0; i < 40; i++ {
		b.WriteString("  // filler\n")
	}
	b.WriteString(`  smolanalytics.track("something_else");` + "\n}\n")
	repo := writeRepo(t, map[string]string{"src/pay.ts": b.String()})

	a, ok := find(Report(repo), "src/pay.ts", "payment")
	if !ok {
		t.Fatal("action not found")
	}
	if a.Covered {
		t.Error("a track() call 40 lines away is not measuring this action")
	}
}

// Noise is what kills a coverage report. It gets dismissed once and never opened again, so the
// patterns must not fire on ordinary code.
func TestDoesNotFlagOrdinaryCode(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/ui.tsx": `export function Button({ onClick }) {
  return <button onClick={onClick}>Click</button>;
}
const x = items.map(i => i.id);
// signup happens elsewhere
function formatDate(d) { return d.toISOString(); }`,
	})
	r := Report(repo)
	for _, a := range r.Actions {
		if a.Kind != "form submit" { // a <form> would be legitimate; nothing here has one
			t.Errorf("flagged ordinary code as a user-facing action: %+v", a)
		}
	}
}

// Tests, stories and mocks are not the product. Flagging them buries the real findings.
func TestSkipsNonAppFiles(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/pay.test.ts":    `stripe.checkout.sessions.create({})`,
		"src/pay.stories.ts": `stripe.checkout.sessions.create({})`,
		"src/__mocks__/s.ts": `stripe.checkout.sessions.create({})`,
		"src/real.ts":        `stripe.checkout.sessions.create({})`,
	})
	r := Report(repo)
	for _, a := range r.Actions {
		if a.File != "src/real.ts" {
			t.Errorf("flagged a non-app file: %s", a.File)
		}
	}
	if len(r.Actions) != 1 {
		t.Errorf("got %d actions, want 1 (only the real file)", len(r.Actions))
	}
}

// The uncovered list is what someone acts on, so it must come first and be stable across runs.
func TestUncoveredFirstAndDeterministic(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/a.ts": "export function signUp() {\n  auth.signup(email);\n  smolanalytics.track(\"signup\");\n}",
		"src/b.ts": "export function pay() {\n  stripe.checkout.sessions.create({});\n}",
		"src/c.ts": "export function invite() {\n  sendInvite(email);\n}",
	})
	first := Report(repo)
	if first.Actions[0].Covered {
		t.Error("uncovered actions must come first — that is the list someone acts on")
	}
	for i := 0; i < 10; i++ {
		r := Report(repo)
		if len(r.Actions) != len(first.Actions) {
			t.Fatal("action count is not stable across runs")
		}
		for j := range r.Actions {
			if r.Actions[j] != first.Actions[j] {
				t.Fatalf("run %d differs at %d: %+v vs %+v", i, j, r.Actions[j], first.Actions[j])
			}
		}
	}
}

// A repository with no recognisable actions must say so rather than reporting 100% coverage,
// which would read as "fully instrumented" when nothing was even examined.
func TestEmptyRepoDoesNotClaimFullCoverage(t *testing.T) {
	r := Report(writeRepo(t, map[string]string{"README.md": "hello"}))
	if r.Percent != 0 || len(r.Actions) != 0 {
		t.Errorf("expected an empty report, got %d%% over %d actions", r.Percent, len(r.Actions))
	}
	if !strings.Contains(r.Headline, "No recognisable") {
		t.Errorf("should say nothing was found, got %q", r.Headline)
	}
}

func TestDetectsTheMainActionKinds(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/auth.ts":        "export function a() {\n  await auth.signUp(email);\n}",
		"src/login.ts":       "export function b() {\n  await signInWithPassword(email);\n}",
		"src/form.tsx":       "export function C() {\n  return <form onSubmit={go} />;\n}",
		"app/api/x/route.ts": "export async function POST(req) {\n  return Response.json({});\n}",
		"src/invite.ts":      "export function d() {\n  sendInvite(email);\n}",
	})
	r := Report(repo)
	want := []string{"signup", "login", "form submit", "api mutation", "invite"}
	for _, kind := range want {
		found := false
		for _, a := range r.Actions {
			if a.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("did not detect a %q action; got %+v", kind, r.Actions)
		}
	}
}

// A JSX form spans several lines and matches on both `<form` and `onSubmit=`. Counting it twice
// reports the same submission as two untracked actions and makes a codebase look worse
// instrumented than it is.
func TestOneFormCountsOnce(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/Ask.tsx": `export function Ask() {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
      }}
    />
  );
}`,
	})
	r := Report(repo)
	n := 0
	for _, a := range r.Actions {
		if a.Kind == "form submit" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("one form produced %d actions, want 1: %+v", n, r.Actions)
	}
}

// Two genuinely separate forms in one file are two actions, so the dedupe must not swallow them.
func TestTwoSeparateFormsCountTwice(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/Two.tsx": `export function A() {
  return <form action={one} />;
}

export function B() {
  const x = 1;
  const y = 2;
  return <form action={two} />;
}`,
	})
	n := 0
	for _, a := range Report(repo).Actions {
		if a.Kind == "form submit" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("two separate forms produced %d actions, want 2", n)
	}
}

// The mentions that made the first version unusable, pinned so they cannot come back.
func TestMentionsAreNotActions(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/x.tsx": `import { signOut } from "@/lib/auth-client";
import SignOutButton from "./SignOutButton";
type SignUpProps = { onSignUp: () => void };
// signup happens in auth.ts
export default function SignOutButton() {
  if (!session) redirect("/login");
  return <span>{busy ? "signing out…" : "sign out"}</span>;
}`,
	})
	r := Report(repo)
	if len(r.Actions) != 0 {
		t.Errorf("mentions were reported as actions: %+v", r.Actions)
	}
}

// Running the scanner against this very repository proposed instrumenting its own test
// fixtures, because Go names test files foo_test.go and none of the dotted suffixes caught it.
// The tracking calls in a fixture are examples, not a product.
func TestSkipsGoTestFiles(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"internal/x/thing_test.go": "func TestX(t *testing.T) {\n  auth.signUp(email)\n}",
		"internal/x/thing.go":      "func Real() {\n  auth.signUp(email)\n}",
	})
	for _, a := range Report(repo).Actions {
		if a.File != "internal/x/thing.go" {
			t.Errorf("flagged a Go test file: %s", a.File)
		}
	}
}

// Modern auth clients namespace the call — signIn.social(...), signUp.email(...). Requiring a
// bare signIn( found nothing at all in a real better-auth codebase.
func TestMatchesNamespacedAuthCalls(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/AuthForm.tsx": `export function F() {
  const a = await signIn.magicLink({ email });
  const b = await signUp.email({ name, email });
  const c = await signIn.social({ provider: "github" });
  return null;
}`,
	})
	kinds := map[string]int{}
	for _, a := range Report(repo).Actions {
		kinds[a.Kind]++
	}
	if kinds["login"] < 2 || kinds["signup"] < 1 {
		t.Errorf("namespaced auth calls not detected: %v", kinds)
	}
}

// A pattern must not match the TAIL of a longer identifier. instanceLogin() is our own
// server-to-server call, not a user signing in, and it was proposed three times before this.
func TestDoesNotMatchInsideLongerIdentifiers(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"src/x.ts": `export async function go() {
  const cookie = await instanceLogin(p);
  const other = await fetchUserLogin(p);
  return cookie;
}`,
	})
	for _, a := range Report(repo).Actions {
		t.Errorf("matched the tail of a longer identifier: %+v", a)
	}
}
