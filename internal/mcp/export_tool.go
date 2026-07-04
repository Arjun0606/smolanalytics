package mcp

// The export-link tool — "give me my data" as one sentence. Mints a one-time
// download URL for the full raw export instead of streaming rows through the
// conversation (a dataset does not belong in a chat transcript).

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/exportlink"
)

func (s *Server) SetExportLinks(st *exportlink.Store) { s.exports = st }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_export_link",
			"description": "Mint a ONE-TIME download link for the full raw event export — every stored event, no lock-in. format: jsonl (default; this instance's own re-importable shape) or csv. Returns the URL path ONCE (/export/<token>); prepend the instance's base URL and hand it to the user. The link expires in 1 hour and dies after its first download — mint a fresh one if it's needed again. Use this instead of paging raw events through the conversation.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format": map[string]any{"type": "string", "enum": []string{"jsonl", "csv"}, "description": "Export format (default jsonl — re-importable via import_events)"},
				},
			},
		},
	)
}

func (s *Server) callExportLink(name string, args json.RawMessage) (bool, string, error) {
	if name != "create_export_link" {
		return false, "", nil
	}
	if s.exports == nil {
		return true, "", fmt.Errorf(noStore, "export-link")
	}
	var p struct {
		Format string `json:"format"`
	}
	if err := unmarshalArgs(args, &p); err != nil {
		return true, "", err
	}
	l, token, err := s.exports.Create(p.Format, time.Now().UTC())
	if err != nil {
		return true, "", err
	}
	return true, jsonStr(map[string]any{
		"created": map[string]any{"id": l.ID, "format": l.Format, "expires": l.Expires},
		"path":    "/export/" + token,
		"note":    "shown ONCE (stored hashed). Full URL = the instance's base URL + this path. Single-use: the link dies after the first download and expires at the time shown — mint another if needed.",
	}), nil
}
