package handler

import (
	"net/http"

	"github.com/flip1688/livescore/internal/service"
)

// Basketball endpoints, serving the /basketball page. Read from our own
// storage like every other route — see boxing.go for why that matters with
// this vendor.
func (h *Handler) basketballRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/basketball/games", h.listBasketballGames)
}

// listBasketballGames serves one display day. ?date=YYYY-MM-DD selects the
// day (04:00 ICT cutoff); omitted means the current one.
func (h *Handler) listBasketballGames(w http.ResponseWriter, r *http.Request) {
	day, err := h.basketball.Games(r.Context(), r.URL.Query().Get("date"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": day})
}

// WithBasketball enables the basketball endpoints.
func WithBasketball(cat *service.BasketballCatalog) Option {
	return func(h *Handler) { h.basketball = cat }
}
