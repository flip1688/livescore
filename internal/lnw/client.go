// Package lnw is the only place in the codebase that talks to the
// lnwthailive.online API — the same containment rule internal/thscore and
// internal/lotto follow.
//
// This feed is being evaluated as an alternative to thscore. Nothing here
// writes to thscore's collections; see internal/store/lnw.go.
//
// # The contract, confirmed against the live API on 19 Nov 2026
//
// Vendor documentation (/api-service/demo, /llms.txt, /llms-full.txt)
// disagrees with the live API on response shape, on pricing and on rate
// limits, so treat this file as the contract and not those pages.
//
// The decisive fact: this service is an aggregator of at least four
// unrelated upstreams glued behind one host, and they share no id space.
//
//	/api/public/matches    ids "rb_4344928"   logos logos1.tcpwe138stya.ru
//	/api/public/results    ids 19893334 (int) logos cdn.sportmonks.com
//	/api/public/standings  NO id at all       logos 365scores (base64-wrapped)
//	/api/analyse/*         ids 342830 (int)   its own league/team/season ids
//
// A match therefore cannot be joined across endpoints by key, only guessed
// at by team name and kick-off time. That is the single largest difference
// from thscore, where everything hangs off one match id, and it is why
// model.LnwMatchLink exists.
//
// # Traps
//
//   - /api/public/matches carries no absolute date. Each row has only
//     day:"today"|"tomorrow" and time:"20:00". The date must be resolved
//     against the clock at fetch time; a row stored raw and converted later
//     is wrong. Rows are Asia/Bangkok wall-clock.
//   - /api/public/matches has no history whatsoever: it returns today and
//     tomorrow only. Past days come from /api/public/results?date=, which is
//     a different id space again.
//   - Stream URLs carry short-lived tokens. The vendor's own docs say not to
//     cache them beyond 15 seconds, so they are never persisted — only the
//     channel list is. See Match.Channels.
//   - /api/public/standings has no league id; leagueUrl ("/standings#epl")
//     is the only stable key upstream offers.
//   - Envelopes are not uniform. matches/standings put rows in "data",
//     results in "leagues", analyse in "data.matches". There is no generic
//     envelope type for that reason.
//
// # Auth
//
// Header x-api-key. The public playground token (field "initialCsrf") is a
// signed, expiring session token, not an API key, and cannot drive a sync.
package lnw

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	pathMatches   = "/api/public/matches"
	pathResults   = "/api/public/results"
	pathStandings = "/api/public/standings"
	pathAnalyse   = "/api/analyse"
	pathVerify    = "/api/portal/verify"
)

// Client is a rate-limited reader for the lnwthailive API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	limiter *rate.Limiter
}

// New builds a client paced below the plan's per-minute quota. rps <= 0
// falls back deliberately low: the vendor quotes 10/min (demo page), 60/min
// (demo page, Starter) and 1,000/min (llms.txt, Starter) for the same tier,
// so until a real plan is confirmed in writing, assume the smallest.
func New(baseURL, apiKey string, rps float64) *Client {
	if rps <= 0 {
		rps = 0.13 // ~8 req/min, inside even the 10/min demo quota
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 25 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
	}
}

// APIError is an upstream-level rejection. Branch on Status and Code; the
// messages come in both Thai and English and change without notice.
type APIError struct {
	Path    string
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("lnw: %s: status %d (code %d): %s", e.Path, e.Status, e.Code, e.Message)
}

// errorBody covers both rejection shapes seen from upstream:
// {"error":"...","code":401} on the public endpoints and
// {"ok":false,"error":"..."} on /api/portal/verify.
type errorBody struct {
	OK    *bool  `json:"ok"`
	Error string `json:"error"`
	Code  int    `json:"code"`
}

// get performs a rate-limited GET and returns the raw body, having already
// rejected anything that carries an upstream error envelope.
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("lnw: bad url: %w", err)
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// Header auth, not ?api_key=: keeps the key out of upstream's access
	// logs and out of anything that echoes a URL.
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lnw: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// An error body can arrive with status 200, so inspect it either way
	// rather than branching on the status code alone.
	var eb errorBody
	if json.Unmarshal(body, &eb) == nil && eb.Error != "" {
		return nil, &APIError{Path: path, Status: resp.StatusCode, Code: eb.Code, Message: eb.Error}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Path: path, Status: resp.StatusCode, Message: string(trim(body, 200))}
	}
	return body, nil
}

func trim(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

func decode[T any](ctx context.Context, c *Client, path string, params url.Values) (T, error) {
	var out T
	body, err := c.get(ctx, path, params)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("lnw: %s: decode: %w", path, err)
	}
	return out, nil
}

// FetchMatches returns today's and tomorrow's fixtures for one sport.
// sport "" means football, upstream's default. The rows carry no absolute
// date — see Day on the returned rows and lnw.ResolveKickoff.
func (c *Client) FetchMatches(ctx context.Context, sport string) (MatchesResponse, error) {
	var params url.Values
	if sport != "" && sport != SportFootball {
		params = url.Values{"sport": {sport}}
	}
	return decode[MatchesResponse](ctx, c, pathMatches, params)
}

// FetchResults returns finished and in-play results for one day.
// day is "YYYY-MM-DD", or "inplay" for live only, or "" for today.
func (c *Client) FetchResults(ctx context.Context, day string) (ResultsResponse, error) {
	var params url.Values
	if day != "" {
		params = url.Values{"date": {day}}
	}
	return decode[ResultsResponse](ctx, c, pathResults, params)
}

// FetchStandings returns every league table upstream publishes. It may come
// back flagged stale while upstream refreshes in the background; the flags
// are preserved rather than hidden (see StandingsResponse.IsStale).
func (c *Client) FetchStandings(ctx context.Context) (StandingsResponse, error) {
	return decode[StandingsResponse](ctx, c, pathStandings, nil)
}

// FetchAnalyseList returns one page of the pre-match analysis index.
// typ is "today", "tomorrow" or "all".
func (c *Client) FetchAnalyseList(ctx context.Context, typ string, page, pageSize int) (AnalyseListResponse, error) {
	params := url.Values{"type": {typ}}
	if page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		params.Set("page_size", strconv.Itoa(pageSize))
	}
	return decode[AnalyseListResponse](ctx, c, pathAnalyse, params)
}

// VerifyKey reports what the configured key is actually allowed to read.
// This is the first call to make against any new key: the vendor's plan
// descriptions disagree with each other, and this endpoint answers from the
// account itself (tier, expiry, rate limit, per-sport permissions).
func (c *Client) VerifyKey(ctx context.Context) (KeyInfo, error) {
	return decode[KeyInfo](ctx, c, pathVerify, nil)
}
