package main

// The cloud-aware MCP: `smolanalytics mcp --host=<cloud> --key=<key>` runs a LOCAL stdio
// MCP server that serves the instrumentation tools by reading the LOCAL repo, and forwards
// every other request to the user's CLOUD instance over its HTTP MCP. This is how a cloud
// customer gets the agent-instruments-your-app USP: the agent runs this in the project dir,
// so propose_instrumentation can read the code, while all data queries hit their real cloud
// instance. `smolanalytics connect --host --key` wires exactly this.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/instrument"
)

// localInstrumentTools need the local repo, so the proxy serves them itself instead of
// forwarding to the cloud (whose filesystem is a Fly container, not the user's project).
var localInstrumentTools = map[string]bool{
	"propose_instrumentation":     true,
	"suggest_instrumentation_fix": true,
	"verify_instrumentation":      true,
}

// parseHostKey pulls --host and --key out of args (supports --flag=v and --flag v),
// shared by the `mcp` and `connect` commands so their cloud flags parse identically.
func parseHostKey(args []string) (host, key string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--host" && i+1 < len(args):
			host, i = args[i+1], i+1
		case strings.HasPrefix(a, "--host="):
			host = strings.TrimPrefix(a, "--host=")
		case a == "--key" && i+1 < len(args):
			key, i = args[i+1], i+1
		case strings.HasPrefix(a, "--key="):
			key = strings.TrimPrefix(a, "--key=")
		}
	}
	return
}

func runMCPProxy(host, key string) {
	host = strings.TrimRight(host, "/")
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := os.Stdout
	for in.Scan() {
		line := in.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if resp := proxyDispatch(host, key, line); resp != nil {
			_, _ = out.Write(append(resp, '\n'))
		}
	}
}

func proxyDispatch(host, key string, line []byte) []byte {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return nil
	}
	if req.Method == "tools/call" {
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if localInstrumentTools[p.Name] {
			text, isErr := runProxyInstrument(host, key, p.Name, p.Arguments)
			return mcpToolResult(req.ID, text, isErr)
		}
	}
	// everything else (initialize, tools/list, data tools, prompts) → forward verbatim
	resp, err := proxyRaw(host, key, line)
	if err != nil {
		if isNotification(req.ID) {
			return nil
		}
		return mcpRPCError(req.ID, "cloud proxy failed: "+err.Error())
	}
	if len(bytes.TrimSpace(resp)) == 0 { // 202 for notifications — nothing to write
		return nil
	}
	return resp
}

func runProxyInstrument(host, key, name string, args json.RawMessage) (string, bool) {
	var a struct {
		RepoPath string `json:"repo_path"`
		Event    string `json:"event"`
		Host     string `json:"host"`
		Key      string `json:"key"`
	}
	_ = json.Unmarshal(args, &a)
	root := "."
	if strings.TrimSpace(a.RepoPath) != "" {
		root = a.RepoPath
	}
	switch name {
	case "propose_instrumentation":
		// the snippet's host+key default to the cloud instance this proxy is bound to
		h, k := a.Host, a.Key
		if h == "" {
			h = host
		}
		if k == "" {
			k = key
		}
		b, _ := json.MarshalIndent(instrument.ProposeResult(root, h, k), "", "  ")
		return string(b), false
	case "suggest_instrumentation_fix":
		if strings.TrimSpace(a.Event) == "" {
			return "event is required — the planned event that isn't arriving", true
		}
		b, _ := json.MarshalIndent(instrument.SuggestFixResult(root, a.Event), "", "  ")
		return string(b), false
	case "verify_instrumentation":
		return proxyVerify(host, key, root)
	}
	return "unknown tool", true
}

// proxyVerify combines the cloud's traffic-based health (which events are firing) with a
// LOCAL code scan (which have a track() call), so a cloud user gets the full
// FIRING / WIRED / MISSING table the self-host path gets.
func proxyVerify(host, key, root string) (string, bool) {
	text, err := mcpCall(host, key, "instrumentation_health", map[string]any{})
	if err != nil {
		return err.Error(), true // the tool's own message (e.g. "no plan declared") is actionable
	}
	var h struct {
		Planned []struct {
			Event  string `json:"event"`
			Status string `json:"status"`
		} `json:"planned"`
	}
	if err := json.Unmarshal([]byte(text), &h); err != nil {
		return "unreadable health payload: " + err.Error(), true
	}
	firing := map[string]bool{}
	names := make([]string, 0, len(h.Planned))
	for _, p := range h.Planned {
		names = append(names, p.Event)
		if p.Status == "flowing" {
			firing[p.Event] = true
		}
	}
	if len(names) == 0 {
		return "no tracking plan declared yet — apply the instrumentation, set_tracking_plan, then verify", true
	}
	wired := instrument.Wired(root, names)
	type row struct {
		Event  string `json:"event"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	var rows []row
	f, w, m := 0, 0, 0
	for _, n := range names {
		switch {
		case firing[n]:
			rows = append(rows, row{n, "FIRING", "✓ arriving in traffic"})
			f++
		case wired[n].Name != "":
			rows = append(rows, row{n, "WIRED", fmt.Sprintf("track() at %s:%d, no traffic yet — run the flow", wired[n].File, wired[n].Line)})
			w++
		default:
			rows = append(rows, row{n, "MISSING", "no track() call and no traffic — call suggest_instrumentation_fix"})
			m++
		}
	}
	out := map[string]any{
		"summary": fmt.Sprintf("%d firing, %d wired-not-yet-fired, %d missing (of %d planned)", f, w, m, len(names)),
		"events":  rows,
		"note":    "FIRING = proven end to end. WIRED = code is there, exercise the flow. MISSING = not wired.",
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b), false
}

// proxyRaw forwards a raw JSON-RPC request to the cloud instance's HTTP MCP and returns
// its response body verbatim, preserving ids and shape.
func proxyRaw(host, key string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, host+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func mcpToolResult(id json.RawMessage, text string, isErr bool) []byte {
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isErr {
		result["isError"] = true
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "result": result})
	return b
}

func mcpRPCError(id json.RawMessage, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "error": map[string]any{"code": -32000, "message": msg}})
	return b
}

func isNotification(id json.RawMessage) bool {
	return len(bytes.TrimSpace(id)) == 0 || string(bytes.TrimSpace(id)) == "null"
}

func rawOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}
