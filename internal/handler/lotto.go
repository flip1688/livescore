package handler

import (
	"crypto/hmac"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/flip1688/livescore/internal/lotto"
	"github.com/flip1688/livescore/internal/service"
)

// maxWebhookBody caps what we read from an upstream POST. A result payload
// is well under 4KB; the cap stops an unbounded read from an endpoint that,
// by design, anyone on the internet can reach.
const maxWebhookBody = 1 << 20 // 1 MiB

// LottoRoutes registers the lottery endpoints on an existing mux.
//
// The webhook is intentionally NOT behind the CORS allowlist or any other
// browser-facing middleware: it is a server-to-server POST from upstream,
// authenticated by a shared secret, and no browser should ever call it.
func (h *Handler) lottoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/lotto/markets", h.listLottoMarkets)
	mux.HandleFunc("GET /v1/lotto/latest", h.listLottoLatest)
	mux.HandleFunc("GET /v1/lotto/day/{day}", h.listLottoByDay)
	mux.HandleFunc("GET /v1/lotto/markets/{slug}", h.getLottoMarket)
	mux.HandleFunc("GET /v1/lotto/markets/{slug}/history", h.listLottoHistory)
	mux.HandleFunc("POST /v1/lotto/webhook", h.lottoWebhook)
}

// listLottoMarkets serves the market list for the category menus.
// ?category= filters; ?all=1 includes markets upstream no longer feeds
// (normally hidden — see model.LottoMarket.Active).
func (h *Handler) listLottoMarkets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	markets, err := h.lotto.Markets(r.Context(), q.Get("category"), q.Get("all") != "1")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": markets})
}

// listLottoLatest serves one latest draw per market, for a category landing
// page. ?category= filters.
func (h *Handler) listLottoLatest(w http.ResponseWriter, r *http.Request) {
	draws, err := h.lotto.LatestPerMarket(r.Context(), r.URL.Query().Get("category"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": draws})
}

// listLottoByDay serves every draw on one Bangkok calendar day
// ("2026-11-16"), for the archive page.
func (h *Handler) listLottoByDay(w http.ResponseWriter, r *http.Request) {
	draws, err := h.lotto.DrawsByDay(r.Context(), r.PathValue("day"), r.URL.Query().Get("category"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": draws})
}

func (h *Handler) getLottoMarket(w http.ResponseWriter, r *http.Request) {
	m, err := h.lotto.Market(r.Context(), r.PathValue("slug"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": m})
}

// listLottoHistory serves a market's past draws, newest first. ?limit=
// defaults to 50.
func (h *Handler) listLottoHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			h.writeError(w, r, service.ErrBadInput)
			return
		}
		limit = n
	}
	draws, err := h.lotto.History(r.Context(), r.PathValue("slug"), limit)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": draws})
}

// lottoWebhook receives upstream's result.published / result.updated POSTs.
//
// Auth is the X-Webhook-Secret header compared in constant time. Upstream
// sends the secret verbatim rather than signing the body, so this is a
// bearer check, not a signature — which is why the endpoint also refuses to
// run at all when no secret is configured: an unauthenticated writer into
// our results collection is worse than a webhook that does not work.
func (h *Handler) lottoWebhook(w http.ResponseWriter, r *http.Request) {
	if h.lottoWebhookSecret == "" || h.lottoSync == nil {
		http.Error(w, "webhook not configured", http.StatusNotFound)
		return
	}
	got := r.Header.Get("X-Webhook-Secret")
	if subtle.ConstantTimeEq(int32(len(got)), int32(len(h.lottoWebhookSecret))) != 1 ||
		!hmac.Equal([]byte(got), []byte(h.lottoWebhookSecret)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var ev lotto.WebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		h.log.Warn("lotto webhook: undecodable body", "err", err)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if ev.Event != lotto.EventResultPublished && ev.Event != lotto.EventResultUpdated {
		// Unknown event types are acknowledged, not rejected: upstream has
		// no retry, and a 4xx on a type we simply do not handle would just
		// show up as a delivery failure on their side.
		h.writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	if err := h.lottoSync.ApplyWebhook(r.Context(), ev); err != nil {
		h.log.Error("lotto webhook: apply failed", "event", ev.Event, "slug", ev.Data.Slug, "err", err)
		http.Error(w, "apply failed", http.StatusInternalServerError)
		return
	}
	h.log.Info("lotto webhook applied", "event", ev.Event, "slug", ev.Data.Slug)
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
