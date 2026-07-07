// Package handler exposes the read API over HTTP (stdlib mux, Go 1.22+ patterns).
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/flip1688/livescore/internal/service"
)

type Handler struct {
	catalog *service.Catalog
	log     *slog.Logger
}

func New(catalog *service.Catalog, log *slog.Logger) *Handler {
	return &Handler{catalog: catalog, log: log}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /v1/leagues", h.listLeagues)
	mux.HandleFunc("GET /v1/leagues/{leagueID}/teams", h.listTeams)
	mux.HandleFunc("GET /v1/matches", h.listMatches)
	return mux
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

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log.Error("write response", "err", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrBadInput) {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.log.Error("request failed", "path", r.URL.Path, "err", err)
	h.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}
