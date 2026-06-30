package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// connect wires this binary into the MCP config of the editors you already use (Claude
// Desktop, Cursor), so "ask your analytics in your editor" is one command instead of
// hand-editing JSON. It detects installed editors, merges a "smolanalytics" stdio server
// into each config (preserving any servers already there), and never clobbers a config it
// can't parse. Run it once; restart the editor; ask away.
func connect(target string) {
	bin, err := os.Executable()
	if err != nil {
		fmt.Println("couldn't find this binary's path:", err)
		return
	}
	bin, _ = filepath.Abs(bin)
	data, _ := filepath.Abs(dataPath())
	entry := map[string]any{
		"command": bin,
		"args":    []string{"mcp"},
		"env":     map[string]string{"SMOLANALYTICS_DB": data},
	}

	type tgt struct{ name, path string }
	var targets []tgt
	switch target {
	case "claude":
		targets = []tgt{{"Claude Desktop", claudeConfigPath()}}
	case "cursor":
		targets = []tgt{{"Cursor", cursorConfigPath()}}
	case "", "all":
		targets = []tgt{{"Claude Desktop", claudeConfigPath()}, {"Cursor", cursorConfigPath()}}
	default:
		fmt.Println("usage: smolanalytics connect [claude|cursor]")
		return
	}

	wrote := 0
	for _, t := range targets {
		if t.path == "" {
			continue
		}
		// only touch an editor that's actually installed (its config dir exists), unless
		// the user named it explicitly.
		if target == "" && !dirExists(filepath.Dir(t.path)) {
			continue
		}
		if err := mergeMCPConfig(t.path, entry); err != nil {
			fmt.Printf("  %s: %v\n", t.name, err)
			continue
		}
		fmt.Printf("  ✓ %s — added the 'smolanalytics' MCP server\n      %s\n", t.name, t.path)
		wrote++
	}

	if wrote == 0 {
		fmt.Println("No installed editor config found. Add this to your MCP config by hand:")
		fmt.Println()
		b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"smolanalytics": entry}}, "  ", "  ")
		fmt.Println("  " + string(b))
		return
	}
	fmt.Println()
	fmt.Println("Done. Restart the editor, then ask: \"what's my biggest funnel drop-off this week?\"")
}

// mergeMCPConfig adds (or replaces) the smolanalytics server in an editor's MCP config,
// keeping every other server intact. Refuses to overwrite a config it can't parse.
func mergeMCPConfig(path string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("existing config isn't valid JSON, leaving it untouched: %s", path)
		}
	}
	servers, ok := cfg["mcpServers"].(map[string]any)
	if !ok || servers == nil {
		servers = map[string]any{}
	}
	servers["smolanalytics"] = entry
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Claude", "claude_desktop_config.json")
		}
		return ""
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func cursorConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor", "mcp.json")
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
