package api

// The public share page: GET /share/{token} renders a read-only web overview for
// anyone holding a valid link — no login, no session, no actions. The token is
// verified against hashes; everything else on the server stays gated.

import (
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/query"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/web"
)

func (s *Server) SetShares(st *share.Store) { s.shares = st; s.mcp.SetShares(st) }

// createShare mints a share link from the dashboard/settings. The raw token is
// returned ONCE (only its hash is stored), as a ready-to-send /share/<token> path —
// the client prepends its own origin. Session-gated by authMW like every /v1 write.
func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "share links unavailable")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	_ = json.Unmarshal(body, &req)
	l, token, err := s.shares.Create(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.rec("share.created", l.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":      l.ID,
		"name":    l.Name,
		"created": l.Created,
		"path":    "/share/" + token,
		"note":    "shown once — the token is stored hashed and cannot be recovered; revoke in settings → share links",
	})
}

// deleteShare revokes a link by id — the URL stops working immediately.
func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) {
	if s.shares == nil {
		writeErr(w, http.StatusServiceUnavailable, "share links unavailable")
		return
	}
	if err := s.shares.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rec("share.revoked", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"revoked": r.PathValue("id")})
}

var shareTmpl = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Project}} · traffic</title>
<style>
:root{--bg:#0A0A0A;--surface:#161616;--surface2:#1C1C1C;--line:#262626;--fg:#FAFAFA;--mut:#8E8E8E;--mut2:#6A6A6A;--accent:#F5A623;
--mono:ui-monospace,"JetBrains Mono",Menlo,monospace;--sans:Inter,-apple-system,"Segoe UI",sans-serif}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font-family:var(--sans);font-size:14px;line-height:1.5}
.wrap{max-width:860px;margin:0 auto;padding:28px 24px}
.head{display:flex;align-items:baseline;justify-content:space-between;margin-bottom:22px}
.logo{font-weight:800;letter-spacing:-.02em;font-size:16px}.logo b{color:var(--accent)}
.proj{color:var(--mut);font-family:var(--mono);font-size:12px}
.stats{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-bottom:14px}
.stat{background:var(--surface);border:1px solid var(--line);padding:16px}
.stat .l{font-family:var(--mono);font-size:11px;text-transform:uppercase;letter-spacing:.08em;color:var(--mut)}
.stat .v{font-family:var(--mono);font-size:26px;font-weight:700;margin-top:4px}.stat .v.acc{color:var(--accent)}
.cols{display:grid;grid-template-columns:1fr 1fr;gap:12px}
.card{background:var(--surface);border:1px solid var(--line);padding:16px}
.card h2{font-size:12px;font-family:var(--mono);text-transform:uppercase;letter-spacing:.1em;color:var(--mut);margin:0 0 12px}
.row{position:relative;height:30px;margin-bottom:4px;background:var(--surface2);border:1px solid var(--line);overflow:hidden}
.bar{position:absolute;inset:0 auto 0 0;background:rgba(245,166,35,.16);border-right:2px solid rgba(245,166,35,.5)}
.meta{position:absolute;inset:0;display:flex;align-items:center;justify-content:space-between;padding:0 10px;font-size:12px}
.meta .n{font-family:var(--mono);color:var(--mut)}
.foot{margin-top:22px;color:var(--mut2);font-family:var(--mono);font-size:11px;text-align:center}
.foot a{color:var(--accent);text-decoration:none}
@media(max-width:700px){.cols,.stats{grid-template-columns:1fr}}
</style></head><body><div class="wrap">
<div class="head"><div class="logo">smol<b>analytics</b></div><div class="proj">{{.Project}} · last {{.Days}} days · read-only</div></div>
<div class="stats">
  <div class="stat"><div class="l">Live now</div><div class="v acc">{{.W.LiveNow}}</div></div>
  <div class="stat"><div class="l">Visitors</div><div class="v">{{.W.Visitors}}</div></div>
  <div class="stat"><div class="l">Pageviews</div><div class="v">{{.W.Pageviews}}</div></div>
</div>
<div class="cols">
  <div class="card"><h2>top pages</h2>
    {{range .Pages}}<div class="row"><div class="bar" style="width:{{.BarPct}}%"></div><div class="meta"><span>{{.Value}}</span><span class="n">{{.Count}}</span></div></div>{{else}}<div class="proj">no pageviews yet</div>{{end}}
  </div>
  <div class="card"><h2>referrers</h2>
    {{range .Refs}}<div class="row"><div class="bar" style="width:{{.BarPct}}%"></div><div class="meta"><span>{{.Value}}</span><span class="n">{{.Count}}</span></div></div>{{else}}<div class="proj">no referrers yet</div>{{end}}
  </div>
</div>
<div class="foot">shared via <a href="https://github.com/Arjun0606/smolanalytics">smolanalytics</a> — open-source analytics in one binary</div>
</div></body></html>`))

type shareRow struct {
	Value  string
	Count  int
	BarPct int
}

func shareRows(rows []web.Row, n int) []shareRow {
	top := 0
	if len(rows) > 0 {
		top = rows[0].Count
	}
	if len(rows) > n {
		rows = rows[:n]
	}
	out := make([]shareRow, 0, len(rows))
	for _, r := range rows {
		sr := shareRow{Value: r.Value, Count: r.Count, BarPct: 100}
		if top > 0 {
			sr.BarPct = int(float64(r.Count) / float64(top) * 100)
		}
		out = append(out, sr)
	}
	return out
}

func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if s.shares == nil || !s.shares.Verify(token) {
		// same body for missing store / bad token — no oracle for probing
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>not found</title><style>html{background:#0A0A0A;color:#8E8E8E;font-family:ui-monospace,Menlo,monospace}body{display:flex;min-height:100vh;align-items:center;justify-content:center}</style><div>this share link doesn't exist or was revoked</div>`))
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		// public route — never echo the internal error to an unauthenticated visitor
		serverError(w, "sharePage store.Range", err)
		return
	}
	evs = query.Apply(evs, nil) // production scope, same as every surface
	wv := web.Compute(evs, 30, time.Time{})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = shareTmpl.Execute(w, map[string]any{
		"Project": s.projectName(),
		"Days":    30,
		"W":       wv,
		"Pages":   shareRows(wv.TopPages, 8),
		"Refs":    shareRows(wv.Referrers, 8),
	})
}
