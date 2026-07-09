// Package handler exposes the read API over HTTP (stdlib mux, Go 1.22+ patterns).
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/flip1688/livescore/internal/service"
	"github.com/flip1688/livescore/internal/thscore"
)

type Handler struct {
	catalog            *service.Catalog
	log                *slog.Logger
	corsAllowedOrigins []string
}

// corsAllowedOrigins restricts which browser origins get CORS headers
// (comma-separated allowlist, "*" = any origin); empty disables CORS
// entirely. See internal/config for how it's parsed from env.
func New(catalog *service.Catalog, log *slog.Logger, corsAllowedOrigins []string) *Handler {
	return &Handler{catalog: catalog, log: log, corsAllowedOrigins: corsAllowedOrigins}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /v1/leagues", h.listLeagues)
	mux.HandleFunc("GET /v1/leagues/{leagueID}/teams", h.listTeams)
	mux.HandleFunc("GET /v1/leagues/{leagueID}/standings", h.getStandings)
	mux.HandleFunc("GET /v1/matches", h.listMatches)
	mux.HandleFunc("GET /v1/matches/{matchID}", h.getMatch)
	mux.HandleFunc("GET /v1/matches/{matchID}/events", h.listMatchEvents)
	mux.HandleFunc("GET /v1/matches/{matchID}/stats", h.listMatchStats)
	mux.HandleFunc("GET /v1/matches/{matchID}/analysis", h.getMatchAnalysis)
	return corsMiddleware(h.corsAllowedOrigins, mux)
}

// getStandings returns one league's table (all 6 standing views).
func (h *Handler) getStandings(w http.ResponseWriter, r *http.Request) {
	st, err := h.catalog.Standings(r.Context(), r.PathValue("leagueID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": st})
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listLeagues(w http.ResponseWriter, r *http.Request) {
	leagues, err := h.catalog.Leagues(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": leagues})
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.catalog.Teams(r.Context(), r.PathValue("leagueID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": teams})
}

// listMatches returns the daily match list for ?date=YYYY-MM-DD
// (default: today, Thai time). Sorted by kickoff; league fields are embedded
// so the frontend can group per league.
func (h *Handler) listMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := h.catalog.MatchesByDate(r.Context(), r.URL.Query().Get("date"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": matches})
}

// getMatch returns one match's current state (Redis live state, falling
// back to MongoDB — same source as the `match:<id>` WS snapshot).
func (h *Handler) getMatch(w http.ResponseWriter, r *http.Request) {
	m, err := h.catalog.Match(r.Context(), r.PathValue("matchID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": m})
}

// listMatchEvents returns the event timeline for one match. The JSON shape
// matches what the WS `events` message publishes — no remapping.
func (h *Handler) listMatchEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.catalog.MatchEvents(r.Context(), r.PathValue("matchID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if events == nil {
		events = []thscore.EventItem{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

// listMatchStats returns the technical stats for one match. Same JSON shape
// as the WS `stats` message.
func (h *Handler) listMatchStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.catalog.MatchStats(r.Context(), r.PathValue("matchID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if stats == nil {
		stats = []thscore.StatItem{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": stats})
}

// getMatchAnalysis returns the pre-fetched H2H/form/odds analysis blob for
// one match, embedded as-is (thscore's undocumented, opaque payload — see
// model.MatchAnalysis) rather than re-encoded as a JSON string.
func (h *Handler) getMatchAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, err := h.catalog.MatchAnalysis(r.Context(), r.PathValue("matchID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": analysis})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log.Error("write response", "err", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrBadInput):
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		h.log.Error("request failed", "path", r.URL.Path, "err", err)
		h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}
