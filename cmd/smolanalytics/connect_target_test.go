package main

import "testing"

// The editor-name scan used to take the first argument that did not start with "-". A flag's
// VALUE is also bare, so `connect --host https://x.fly.dev --key sa_k` read the URL as the
// editor name, matched no assistant, wrote nothing and said nothing. That is the documented
// cloud command, so for every cloud user this subcommand was a silent no-op.
//
// parseConnectTarget is the extracted scan; these pin that a flag value is never mistaken
// for an assistant.

func TestConnectTargetIgnoresFlagValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"cloud command has no explicit editor", []string{"--host", "https://x.fly.dev", "--key", "sa_k"}, ""},
		{"host value is not an editor", []string{"--host", "https://smolyt.fly.dev"}, ""},
		{"key value is not an editor", []string{"--key", "sa_abc123"}, ""},
		{"explicit editor still wins", []string{"cursor", "--host", "https://x.fly.dev"}, "cursor"},
		{"editor after flags is still found", []string{"--host", "https://x.fly.dev", "--key", "k", "windsurf"}, "windsurf"},
		{"equals form does not swallow the next arg", []string{"--host=https://x.fly.dev", "claude"}, "claude"},
		{"no args at all", []string{}, ""},
		{"bare editor only", []string{"vscode"}, "vscode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseConnectTarget(c.args); got != c.want {
				t.Errorf("parseConnectTarget(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

func TestFlagTakesValue(t *testing.T) {
	for _, f := range []string{"--host", "--key", "-host", "--target", "--data"} {
		if !flagTakesValue(f) {
			t.Errorf("%s should consume the next argument", f)
		}
	}
	for _, f := range []string{"--all", "--help", "-v"} {
		if flagTakesValue(f) {
			t.Errorf("%s should NOT consume the next argument", f)
		}
	}
}
