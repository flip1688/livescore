package lotto

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Payloads below are trimmed copies of real responses captured on
// 19 Nov 2026 — including the shapes that differ from the vendor docs.
const historyBody = `{"success":true,"data":{
 "slug":"gov-th","name":"หวยรัฐบาลไทย","shortName":"รัฐบาล",
 "category":"gov","tier":"normal","country":"TH",
 "drawSchedule":"ทุกวันที่ 1 และ 16 เวลา 16:00",
 "count":2,
 "results":[
  {"drawId":14910,"drawDate":"2026-11-16T09:00:00.000Z","round":null,
   "status":"published","isCanceled":false,"publishedAt":"2026-11-16T09:00:08.302Z",
   "prizes":[{"type":"first","label":"รางวัลที่ 1","number":"645654"},
             {"type":"front3","label":"เลขหน้า 3 ตัว","number":"938"},
             {"type":"front3","label":"เลขหน้า 3 ตัว","number":"101"},
             {"type":"last3","label":"เลขท้าย 3 ตัว","number":"260"},
             {"type":"last3","label":"เลขท้าย 3 ตัว","number":"775"},
             {"type":"last2","label":"เลขท้าย 2 ตัว","number":"23"}]},
  {"drawId":557,"drawDate":"2026-07-01T09:00:00.000Z","round":null,
   "status":"published","isCanceled":false,"publishedAt":"2026-07-21T15:48:29.767Z",
   "prizes":[{"type":"first","label":"รางวัลที่ 1","number":"751495"}]}
 ]}}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "test-key", 1000) // no pacing in tests
	return c
}

func TestFetchHistoryReadsResultsNotData(t *testing.T) {
	// The array lives at data.results. Decoding data itself as a list is the
	// easy mistake and yields an empty, successful-looking response.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("api key sent as %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "100" {
			t.Errorf("limit = %q, want 100", got)
		}
		w.Write([]byte(historyBody))
	})

	hist, err := c.FetchHistory(context.Background(), "gov-th", 0)
	if err != nil {
		t.Fatal(err)
	}
	if hist.Slug != "gov-th" {
		t.Errorf("market slug = %q", hist.Slug)
	}
	if len(hist.Results) != 2 {
		t.Fatalf("got %d draws, want 2", len(hist.Results))
	}
	if hist.Results[0].DrawID != 14910 {
		t.Errorf("first drawId = %d", hist.Results[0].DrawID)
	}
}

func TestFetchHistoryClampsLimit(t *testing.T) {
	// Upstream silently caps at 100; clamping here keeps callers honest
	// about how much they actually asked for.
	var gotLimit string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		w.Write([]byte(historyBody))
	})
	if _, err := c.FetchHistory(context.Background(), "gov-th", 500); err != nil {
		t.Fatal(err)
	}
	if gotLimit != "100" {
		t.Errorf("limit = %q, want it clamped to 100", gotLimit)
	}
}

func TestFetchSurfacesAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"error":{"code":"invalid_api_key","message":"API key ไม่ถูกต้องหรือถูกเพิกถอน"}}`))
	})
	_, err := c.FetchMarkets(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an *APIError, got %v", err)
	}
	if apiErr.Code != CodeInvalidAPIKey {
		t.Errorf("code = %q, want %q", apiErr.Code, CodeInvalidAPIKey)
	}
}

func TestDrawToModel(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(historyBody))
	})
	hist, err := c.FetchHistory(context.Background(), "gov-th", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

	full := hist.Results[0].ToModel(hist.Market, now)
	if full.ID != "14910" {
		t.Errorf("ID = %q, want the drawId as a string", full.ID)
	}
	// 09:00Z is 16:00 in Bangkok — same calendar day, but the conversion has
	// to happen for draws published late in the UTC evening.
	if full.DrawDay != "2026-11-16" {
		t.Errorf("DrawDay = %q, want 2026-11-16", full.DrawDay)
	}
	if !full.Complete {
		t.Error("a draw with every prize row should be complete")
	}

	partial := hist.Results[1].ToModel(hist.Market, now)
	if partial.Complete {
		t.Error("a draw carrying only รางวัลที่ 1 should not be complete")
	}
}

func TestDrawDayCrossesMidnightInBangkok(t *testing.T) {
	// 18:30Z on the 18th is 01:30 on the 19th in Bangkok. Lottery draws have
	// no 04:00 cutover, so the Bangkok calendar day is the answer.
	d := Draw{
		DrawID:   1,
		DrawDate: time.Date(2026, 11, 18, 18, 30, 0, 0, time.UTC),
		Prizes:   []Prize{{Type: "3up", Number: "384"}, {Type: "2down", Number: "30"}},
	}
	m := Market{Slug: "hanoi-vip", Category: "hanoi"}
	got := d.ToModel(m, time.Now())
	if got.DrawDay != "2026-11-19" {
		t.Fatalf("DrawDay = %q, want 2026-11-19", got.DrawDay)
	}
}
