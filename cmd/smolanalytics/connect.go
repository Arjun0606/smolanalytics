package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// connect wires this binary into the MCP config of the coding assistants you already use,
// so "ask your analytics in your editor" is one command instead of hand-editing JSON. It
// detects installed assistants and merges a "smolanalytics" stdio server into each config
// (preserving anything already there), and never clobbers a config it can't parse. Run it
// once, restart the editor, ask away.
//
//	smolanalytics connect            # every installed assistant
//	smolanalytics connect cursor     # just one (claude | cursor | windsurf | vscode | cline | claude-code)

// mcpClient is an assistant we can auto-configure by merging into its JSON config.
type mcpClient struct {
	name  string // display name
	short string // arg alias
	path  string // config file ("" if unknown on this OS)
	key   string // top-level object key: "mcpServers" (most) or "servers" (VS Code)
}

func connect(args []string) {
	host, key := parseHostKey(args)
	// The first bare arg is the editor — but a flag's VALUE is also bare. Scanning naively
	// meant `connect --host https://x.fly.dev --key sa_k` read the URL as the editor name,
	// matched no assistant, and silently configured nothing. That is the documented cloud
	// command, so for cloud users this subcommand did nothing at all and said so nowhere.
	target := parseConnectTarget(args)

	bin, err := os.Executable()
	if err != nil {
		fmt.Println("couldn't find this binary's path:", err)
		return
	}
	bin, _ = filepath.Abs(bin)
	data, _ := filepath.Abs(dataPath())

	// cloud mode (--host + --key): wire a local stdio proxy so the agent can instrument
	// the local repo while data queries hit the cloud instance. Otherwise point `mcp` at
	// the local data file (self-host).
	cloud := host != "" && key != ""
	var entry map[string]any
	if cloud {
		host = strings.TrimRight(host, "/")
		entry = map[string]any{"command": bin, "args": []string{"mcp", "--host", host, "--key", key}}
	} else {
		entry = map[string]any{"command": bin, "args": []string{"mcp"}, "env": map[string]string{"SMOLANALYTICS_DB": data}}
	}

	explicit := target != "" && target != "all"
	wrote, failed := 0, 0
	var skipped []mcpClient

	for _, c := range mcpClients() {
		if explicit && !strings.EqualFold(target, c.short) && !strings.EqualFold(target, c.name) {
			continue
		}
		if c.path == "" {
			continue
		}
		// on auto-detect, only touch assistants that are actually installed (config dir
		// exists). SAY when we skip one: silently passing over a client the user is sitting
		// in front of is why this felt broken — you ran connect, your assistant was not
		// mentioned at all, and there was nothing to act on.
		if !explicit && !dirExists(filepath.Dir(c.path)) {
			skipped = append(skipped, c)
			continue
		}
		if err := mergeMCPConfig(c.path, c.key, entry); err != nil {
			fmt.Printf("  ✗ %s: %v\n", c.name, err)
			failed++
			continue
		}
		fmt.Printf("  ✓ %s\n      %s\n", c.name, c.path)
		wrote++
	}

	// Claude Code configures via its own CLI (the documented path), not a file we edit.
	if (!explicit || strings.EqualFold(target, "claude-code")) && addClaudeCode(bin, data, host, key) {
		fmt.Println("  ✓ Claude Code  (added via `claude mcp add`)")
		wrote++
	}

	// Report what we did NOT touch, and exactly how to force it. A config dir only appears
	// once the app has run at least once, so "not installed" here often means "installed but
	// never opened", which is a state the user can fix in two seconds if we say so.
	if len(skipped) > 0 {
		fmt.Println()
		fmt.Println("Not configured (no config folder yet, so the app looks uninstalled):")
		for _, c := range skipped {
			fmt.Printf("  – %-16s expected %s\n", c.name, c.path)
		}
		fmt.Println()
		fmt.Println("If one of those IS installed, open it once so it creates its config, or force it:")
		fmt.Printf("  smolanalytics connect --target %s\n", skipped[0].short)
	}

	if wrote == 0 {
		fmt.Println()
		fmt.Println("Nothing was configured automatically. Add this by hand")
		fmt.Println("(most use the \"mcpServers\" key; VS Code uses \"servers\"):")
		fmt.Println()
		b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"smolanalytics": entry}}, "  ", "  ")
		fmt.Println("  " + string(b))
		return
	}
	fmt.Println()
	if failed > 0 {
		fmt.Printf("%d assistant(s) could not be written. The JSON above can be pasted by hand.\n", failed)
	}
	fmt.Println("Done. Now FULLY restart the assistant, not just its window:")
	fmt.Println("  Claude Desktop  quit with Cmd-Q (closing the window is not enough), then reopen")
	fmt.Println("  Cursor/Windsurf reload the window, or restart the app")
	fmt.Println()
	fmt.Println("Then ask: \"what's my biggest funnel drop-off this week?\"")
}

// mcpClients lists every assistant we can auto-configure, with OS-specific config paths.
func mcpClients() []mcpClient {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var cfg string // per-OS user-config base
	switch runtime.GOOS {
	case "darwin":
		cfg = filepath.Join(home, "Library", "Application Support")
	case "windows":
		cfg = os.Getenv("APPDATA")
	default:
		cfg = filepath.Join(home, ".config")
	}
	return []mcpClient{
		{"Claude Desktop", "claude", filepath.Join(cfg, "Claude", "claude_desktop_config.json"), "mcpServers"},
		{"Cursor", "cursor", filepath.Join(home, ".cursor", "mcp.json"), "mcpServers"},
		{"Windsurf", "windsurf", filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), "mcpServers"},
		{"VS Code", "vscode", filepath.Join(cfg, "Code", "User", "mcp.json"), "servers"},
		{"Cline", "cline", filepath.Join(cfg, "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), "mcpServers"},
	}
}

// mergeMCPConfig adds (or replaces) the smolanalytics server under `key` in an assistant's
// config, keeping every other server intact. Refuses to overwrite a config it can't parse.
func mergeMCPConfig(path, key string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("existing config isn't valid JSON, leaving it untouched: %s", path)
		}
	}
	servers, ok := cfg[key].(map[string]any)
	if !ok || servers == nil {
		servers = map[string]any{}
	}
	servers["smolanalytics"] = entry
	cfg[key] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// addClaudeCode registers the server through the `claude` CLI (Claude Code's own way).
// Returns true if it succeeded. Best-effort: no CLI, no problem.
func addClaudeCode(bin, data, host, key string) bool {
	if _, err := exec.LookPath("claude"); err != nil {
		return false
	}
	// remove any prior entry so re-running connect is idempotent, then add.
	_ = exec.Command("claude", "mcp", "remove", "smolanalytics", "-s", "user").Run()
	var cmd *exec.Cmd
	if host != "" && key != "" { // cloud proxy
		cmd = exec.Command("claude", "mcp", "add", "smolanalytics", "-s", "user",
			"--", bin, "mcp", "--host", strings.TrimRight(host, "/"), "--key", key)
	} else {
		cmd = exec.Command("claude", "mcp", "add", "smolanalytics", "-s", "user",
			"-e", "SMOLANALYTICS_DB="+data, "--", bin, "mcp")
	}
	return cmd.Run() == nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// flagTakesValue reports whether a connect flag consumes the following argument, so the
// editor-name scan does not mistake a flag's value for an assistant name.
func flagTakesValue(flag string) bool {
	switch strings.TrimLeft(flag, "-") {
	case "host", "key", "target", "data":
		return true
	}
	return false
}

// parseConnectTarget picks the editor name out of the args, skipping any flag values.
//
// It used to be an inline loop taking the first argument without a leading "-". A flag's
// value is also bare, so `connect --host https://x.fly.dev --key sa_k` read the URL as the
// editor, matched nothing, and configured nothing while printing no error.
func parseConnectTarget(args []string) string {
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			// --flag=value carries its own value; --flag consumes the next argument
			if !strings.Contains(a, "=") && flagTakesValue(a) {
				skipNext = true
			}
			continue
		}
		return a
	}
	return ""
}
