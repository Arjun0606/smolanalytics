package mcp

// Share-link tools — mint and revoke read-only traffic pages from the editor:
// "give me a link I can send my investor" is one sentence.

import (
	"encoding/json"
	"fmt"

	"github.com/Arjun0606/smolanalytics/internal/share"
)

func (s *Server) SetShares(st *share.Store) { s.shares = st }

func init() {
	toolList = append(toolList,
		map[string]any{
			"name":        "create_share_link",
			"description": "Mint a revocable READ-ONLY share link to this instance's web traffic overview (visitors, live, top pages, referrers — no actions, no settings, no raw events). Returns the URL path ONCE (/share/<token>); prepend the instance's base URL. Name it after who it's for.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "description": "Who it's for, e.g. 'investor update'"}}, "required": []string{"name"}},
		},
		map[string]any{
			"name":        "list_share_links",
			"description": "List share links (names + ids; tokens are hashed and can never be shown again).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "revoke_share_link",
			"description": "Revoke a share link by id — the URL stops working immediately.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}},
		},
	)
}

func (s *Server) callShares(name string, args json.RawMessage) (bool, string, error) {
	switch name {
	case "create_share_link":
		if s.shares == nil {
			return true, "", fmt.Errorf(noStore, "share-link")
		}
		var p struct{ Name string }
		if err := unmarshalArgs(args, &p); err != nil {
			return true, "", err
		}
		l, token, err := s.shares.Create(p.Name)
		if err != nil {
			return true, "", err
		}
		return true, jsonStr(map[string]any{
			"created": map[string]any{"id": l.ID, "name": l.Name},
			"path":    "/share/" + token,
			"note":    "shown ONCE (stored hashed). Full URL = the instance's base URL + this path. Revoke any time with revoke_share_link.",
		}), nil
	case "list_share_links":
		if s.shares == nil {
			return true, "", fmt.Errorf(noStore, "share-link")
		}
		list := s.shares.List()
		out := make([]map[string]any, 0, len(list))
		for _, l := range list {
			out = append(out, map[string]any{"id": l.ID, "name": l.Name, "created": l.Created})
		}
		return true, jsonStr(map[string]any{"share_links": out}), nil
	case "revoke_share_link":
		return s.deleteByID(args, "share-link", func(id string) error { return s.shares.Delete(id) }, s.shares == nil)
	}
	return false, "", nil
}
