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
	target := ""
	for _, a := range args { // the first bare (non-flag) arg is the editor
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}

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
	wrote := 0

	for _, c := range mcpClients() {
		if explicit && !strings.EqualFold(target, c.short) && !strings.EqualFold(target, c.name) {
			continue
		}
		if c.path == "" {
			continue
		}
		// on auto-detect, only touch assistants that are actually installed (config dir exists)
		if !explicit && !dirExists(filepath.Dir(c.path)) {
			continue
		}
		if err := mergeMCPConfig(c.path, c.key, entry); err != nil {
			fmt.Printf("  %s: %v\n", c.name, err)
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

	if wrote == 0 {
		fmt.Println("No installed assistant config found. Add this to your MCP config by hand")
		fmt.Println("(most use the \"mcpServers\" key; VS Code uses \"servers\"):")
		fmt.Println()
		b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"smolanalytics": entry}}, "  ", "  ")
		fmt.Println("  " + string(b))
		return
	}
	fmt.Println()
	fmt.Println("Done. Restart the editor, then ask: \"what's my biggest funnel drop-off this week?\"")
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
