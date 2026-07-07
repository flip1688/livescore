package thscore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// disableLimiters replaces every configured rate limiter with an
// effectively-unlimited one so tests don't block on the real cadences.
func disableLimiters(c *Client) {
	for path := range c.limiters {
		c.limiters[path] = rate.NewLimiter(rate.Inf, 1)
	}
}

func TestGet_SendsAPIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("api_key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"message":"","data":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	disableLimiters(c)

	if _, err := c.FetchCountries(context.Background()); err != nil {
		t.Fatalf("FetchCountries: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("api_key = %q, want %q", gotKey, "test-key")
	}
}

func TestFetch_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":429,"message":"rate limited","data":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	disableLimiters(c)

	_, err := c.FetchCountries(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error %q missing code/message", err)
	}
}

func TestFetchLivescores_ParsesMatch(t *testing.T) {
	// livescores.aspx sends homeId as a number, awayId shown as string here to
	// cover schedule/basic.aspx's encoding too — FlexString accepts both.
	body := `{"code":0,"message":"","data":[{"matchId":123,"leagueId":1,"leagueName":"Test League",` +
		`"matchTime":"08-07-2026 20:00:00","status":1,"homeId":55,"homeName":"Home FC",` +
		`"awayId":"77","awayName":"Away FC","homeScore":1,"awayScore":0}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	disableLimiters(c)

	matches, err := c.FetchLivescores(context.Background())
	if err != nil {
		t.Fatalf("FetchLivescores: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	m := matches[0]
	if m.MatchID != 123 {
		t.Errorf("MatchID = %d, want 123", m.MatchID)
	}
	if m.HomeID != "55" || m.AwayID != "77" {
		t.Errorf("HomeID/AwayID = %q/%q, want %q/%q (string ids)", m.HomeID, m.AwayID, "55", "77")
	}
	if m.HomeName != "Home FC" || m.AwayName != "Away FC" {
		t.Errorf("HomeName/AwayName = %q/%q", m.HomeName, m.AwayName)
	}
	if m.HomeScore != 1 || m.AwayScore != 0 {
		t.Errorf("HomeScore/AwayScore = %d/%d, want 1/0", m.HomeScore, m.AwayScore)
	}
}

func TestFetchLiveChanges_ParsesTimeAny(t *testing.T) {
	// matchId=1 sends matchTime as a unix number, matchId=2 as a unix string,
	// matchId=3 as the GMT+7 datetime string seen in live payloads — all three
	// encode the same instant and must parse identically via ParseTimeAny.
	body := `{"code":0,"message":"","data":[` +
		`{"matchId":1,"matchTime":1751980800,"status":1},` +
		`{"matchId":2,"matchTime":"1751980800","status":1},` +
		`{"matchId":3,"matchTime":"08-07-2025 20:20:00","status":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-key")
	disableLimiters(c)

	changes, err := c.FetchLiveChanges(context.Background())
	if err != nil {
		t.Fatalf("FetchLiveChanges: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("len(changes) = %d, want 3", len(changes))
	}
	// 1751980800 == 08-07-2025 20:20:00 GMT+7.
	want := time.Unix(1751980800, 0).UTC()
	for _, ch := range changes {
		got, ok := ParseTimeAny(ch.MatchTime)
		if !ok {
			t.Fatalf("ParseTimeAny(%#v) returned ok=false", ch.MatchTime)
		}
		if !got.Equal(want) {
			t.Fatalf("matchId=%d: got %v, want %v", ch.MatchID, got, want)
		}
	}
}
