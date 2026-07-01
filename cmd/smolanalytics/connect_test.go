package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mergeMCPConfig must add our server under the right key, preserve any existing servers
// and unrelated settings, and support both the "mcpServers" (most) and "servers" (VS Code)
// schemas.
func TestMergeMCPConfigPreservesAndKeys(t *testing.T) {
	dir := t.TempDir()
	entry := map[string]any{"command": "/bin/smol", "args": []string{"mcp"}}

	// existing mcpServers config with another server + an unrelated key
	p := filepath.Join(dir, "mcp.json")
	os.WriteFile(p, []byte(`{"mcpServers":{"other":{"command":"x"}},"keep":true}`), 0o600)
	if err := mergeMCPConfig(p, "mcpServers", entry); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &got)
	servers := got["mcpServers"].(map[string]any)
	if servers["other"] == nil {
		t.Error("dropped the existing 'other' server")
	}
	if servers["smolanalytics"] == nil {
		t.Error("didn't add smolanalytics")
	}
	if got["keep"] != true {
		t.Error("dropped the unrelated 'keep' setting")
	}

	// VS Code uses a different top-level key
	vp := filepath.Join(dir, "vscode.json")
	if err := mergeMCPConfig(vp, "servers", entry); err != nil {
		t.Fatal(err)
	}
	var vgot map[string]any
	b, _ = os.ReadFile(vp)
	json.Unmarshal(b, &vgot)
	if vgot["servers"] == nil || vgot["mcpServers"] != nil {
		t.Error("VS Code config should use the 'servers' key, not 'mcpServers'")
	}
}

// refuse to clobber a config that isn't valid JSON (don't destroy a user's hand-edits).
func TestMergeMCPConfigRefusesCorrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(p, []byte(`{not json`), 0o600)
	if err := mergeMCPConfig(p, "mcpServers", map[string]any{"command": "x"}); err == nil {
		t.Fatal("should refuse to overwrite an unparseable config")
	}
}
