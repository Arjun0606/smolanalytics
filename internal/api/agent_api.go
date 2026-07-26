package api

// Agent-observability HTTP surface: tool-call health, error taxonomy, and conversation health,
// each computed over the same filtered/dev-env-scoped event stream every other /v1 report uses
// (s.filtered) with the same time grammar (parseTrendWindow). The compute lives in
// internal/agent so this handler, the MCP tools, and the dashboard all serialize ONE result —
// agreement_test pins /v1/agent/* == the MCP tools byte-for-byte.

import (
	"net/http"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/agent"
)

// errNoLabel is the exact wording the agent_labels MCP tool uses for a missing label (see
// mcp.errNoLabel), so the same mistake gets the same answer on both surfaces.
const errNoLabel = "label is required, e.g. intent or sentiment: a label name your model wrote with label_conversation. nothing labeled yet? call sample_conversations, then label_conversation."

// GET /v1/agent/tools — per-tool call/error/latency health, with the client split.
func (s *Server) apiAgentTools(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent.ComputeToolHealth(evs, from, to))
}

// GET /v1/agent/errors?tool=search — breakdown of failing tool calls by error_type,
// optionally scoped to one tool. An unseen tool returns an honest empty taxonomy.
func (s *Server) apiAgentErrors(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent.ComputeErrorTaxonomy(evs, r.URL.Query().Get("tool"), from, to))
}

// GET /v1/agent/conversations — conversation-level health (turns, re-ask, abandon, TTFT),
// with resolution_rate null unless the app sends a `resolved` bool (the honesty covenant).
func (s *Server) apiAgentConversations(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent.ComputeConversations(evs, from, to))
}

// GET /v1/agent/labels?label=intent — conversations per value of a model-written label. The
// VALUES are inferences the user's own model wrote via the label_conversation MCP tool (we never
// run a model); the COUNTS over them are computed here like every other number. The response
// carries labeled_by and the labeled/unlabeled split so an inference is never read as a
// measurement, and an unlabeled instance gets an honest empty result explaining how to label.
func (s *Server) apiAgentLabels(w http.ResponseWriter, r *http.Request) {
	evs, err := s.filtered(r)
	if err != nil {
		writeQueryErr(w, err)
		return
	}
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		writeErr(w, http.StatusBadRequest, errNoLabel)
		return
	}
	from, to, werr := parseTrendWindow(r)
	if werr != nil {
		writeErr(w, http.StatusBadRequest, werr.Error())
		return
	}
	writeJSON(w, http.StatusOK, agent.ComputeLabelBreakdown(evs, label, from, to))
}
