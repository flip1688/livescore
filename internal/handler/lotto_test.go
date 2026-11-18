package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flip1688/livescore/internal/service"
)

// testHandler wires the lotto routes with stand-ins. A non-nil catalog is
// what switches the routes on, and a non-nil syncer is what makes the
// webhook reachable; neither is actually called by these tests, which stop
// at auth and decoding, so both are built over nil dependencies.
func testHandler(secret string) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := service.NewLottoCatalog(nil, log)
	syncer := service.NewLottoSyncer(nil, nil, log, 0, 0)
	return New(nil, log, nil, WithLotto(cat, syncer, secret)).Routes()
}

const webhookBody = `{"event":"result.published","occurredAt":"2026-11-18T17:30:50.580Z",
 "data":{"slug":"hanoi-normal","name":"ฮานอยปกติ","category":"hanoi","tier":"normal","country":"VN",
  "latest":{"drawId":15253,"drawDate":"2026-11-18T11:30:00.000Z","status":"published",
   "isCanceled":false,"publishedAt":"2026-11-18T11:30:50.580Z",
   "prizes":[{"type":"3up","label":"3 ตัวบน","number":"384"},
             {"type":"2down","label":"2 ตัวล่าง","number":"30"}]}}}`

func postWebhook(t *testing.T, h http.Handler, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/lotto/webhook", strings.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Webhook-Secret", secret)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWebhookRejectsWrongSecret(t *testing.T) {
	h := testHandler("right-secret")
	for _, tc := range []struct{ name, sent string }{
		{"no header", ""},
		{"wrong value", "wrong-secret"},
		{"right prefix wrong length", "right-secret-plus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postWebhook(t, h, tc.sent, webhookBody)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	// An unauthenticated writer into the results collection is worse than a
	// webhook that does not work, so no secret means no endpoint at all —
	// not an endpoint that accepts anything.
	h := testHandler("")
	rec := postWebhook(t, h, "anything", webhookBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWebhookIgnoresUnknownEvent(t *testing.T) {
	// Upstream does not retry, so an event type we do not handle is
	// acknowledged rather than failed — a 4xx would only show up on their
	// side as a broken endpoint.
	h := testHandler("s3cret")
	body := strings.Replace(webhookBody, "result.published", "result.archived", 1)
	rec := postWebhook(t, h, "s3cret", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ignored") {
		t.Errorf("body = %s, want it to report the event as ignored", rec.Body.String())
	}
}

func TestWebhookRejectsBadJSON(t *testing.T) {
	h := testHandler("s3cret")
	rec := postWebhook(t, h, "s3cret", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookRejectsGET(t *testing.T) {
	h := testHandler("s3cret")
	req := httptest.NewRequest(http.MethodGet, "/v1/lotto/webhook", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("GET should not reach the webhook handler")
	}
}

func TestLottoRoutesAbsentWhenNotConfigured(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(nil, log, nil).Routes()
	req := httptest.NewRequest(http.MethodGet, "/v1/lotto/markets", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when lotto is disabled", rec.Code)
	}
}
