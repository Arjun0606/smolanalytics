package api

// GET /v1/investigate — the flagship, finally reachable by a machine over HTTP.
//
// Until this file the investigation had exactly three doors: the MCP tool, a server-rendered
// dashboard page, and the share page's HTML. None of them is callable by a program that is not
// an MCP client, which meant the one output this product is named for could not cross a process
// boundary. The cloud control plane — which polls /v1/notable and /v1/brief on a schedule and is
// the thing that would ACT on a finding — literally could not read an investigate.Finding.
//
// That gap is what made the newest finding kind useless: tracking_broke is provable without a
// model and mechanically fixable, and there was no way to hand it to the half of the system that
// holds repository access. This route is that hand-off, and its JSON body is a frozen contract.

import (
	"net/http"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/desk"
	"github.com/Arjun0606/smolanalytics/internal/investigate"
)

// deskSources gathers this instance's optional state for an investigation. One place, so the
// route, the dashboard and the share page cannot be wired with different halves of it.
func (s *Server) deskSources() desk.Sources {
	return desk.Sources{
		Flags:   flagsFor(s),
		Deploys: deploysFor(s),
		Acted:   actedLookup(s),
		Planned: desk.Planned(s.trackplan),
	}
}

// apiInvestigate serves the whole desk as JSON.
//
// AUTH IS SESSION-OR-KEY, exactly like notable and apiBrief, and for the same reason those two
// are: the control plane arrives with a Bearer key and no cookie. Session-only here would 401
// every cloud poll and silently kill the entire feature — the failure would look like "the
// robot never finds anything" rather than like an auth error, which is the worst kind.
func (s *Server) apiInvestigate(w http.ResponseWriter, r *http.Request) {
	if s.readsGated() && !s.validSession(r) && !s.keyAuthed(r) {
		writeErr(w, http.StatusUnauthorized, "login or a valid key required")
		return
	}
	// Same default and same clamp as /v1/brief and the MCP investigate tool. posIntParam so
	// ?days=abc is a 400 rather than a silently different window than the caller asked for.
	days, derr := posIntParam(r, "days", 30, 90)
	if derr != nil {
		writeQueryErr(w, derr)
		return
	}
	evs, err := s.store.Range(time.Time{}, time.Time{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Deliberately NOT pre-scoped here. investigate.Run applies production scope itself, and the
	// MCP tool hands it the raw range — so passing a pre-filtered slice from this door only, to
	// be filtered again downstream, is precisely the kind of asymmetry the agreement test exists
	// to catch. One door must not narrow what the other does not.
	writeJSON(w, http.StatusOK, desk.Doc(desk.Build(evs, s.deskSources(), investigate.Opts{
		Now:  time.Now().UTC(),
		Days: days,
	})))
}
