package handler

import (
	"net/http"

	"github.com/flip1688/livescore/internal/service"
)

// Boxing endpoints, serving the /boxing page.
//
// Like every other read path these answer from our own storage, never from
// the vendor: the stream URLs upstream returns carry a token derived from
// our API key, so a request that reached it from a browser would hand that
// key out.
func (h *Handler) boxingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/boxing/schedule", h.listBoxingSchedule)
	mux.HandleFunc("GET /v1/boxing/programs", h.listBoxingPrograms)
}

// listBoxingSchedule serves one day's broadcasts. ?date=YYYY-MM-DD selects
// the display day (04:00 ICT cutoff); omitted means the current one.
func (h *Handler) listBoxingSchedule(w http.ResponseWriter, r *http.Request) {
	rows, err := h.boxing.Schedule(r.Context(), r.URL.Query().Get("date"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) listBoxingPrograms(w http.ResponseWriter, r *http.Request) {
	rows, err := h.boxing.Programs(r.Context())
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// WithBoxing enables the boxing endpoints.
func WithBoxing(cat *service.BoxingCatalog) Option {
	return func(h *Handler) { h.boxing = cat }
}
