// Package lotto is the only place in the codebase that talks to the
// lotto2news API — the same containment rule internal/thscore follows: no
// user request may reach upstream, because the API key is per-account and
// the plan's quota (Starter: 60 req/min, 5,000 req/day) is shared by every
// visitor the moment it leaks onto the read path.
//
// Upstream contract, confirmed against the live API on 19 Nov 2026 rather
// than taken from its documentation, which disagrees with it:
//
//   - GET /api/v1/lotteries              -> data is an array of markets
//   - GET /api/v1/result                 -> data is an array, but only of the
//     handful of markets published in the last few minutes, NOT all markets
//     as documented. Unused here for that reason.
//   - GET /api/v1/result/{slug}          -> data is a market with .latest,
//     which is null for gov-th even while its history carries draws.
//   - GET /api/v1/result/{slug}/history  -> data.results is the array;
//     data itself is the market. limit is capped at 100 server-side, and
//     `to` is exclusive.
//
// The published webhook sample is stale in three fields at once (slug "gov",
// category "thai", tier "main" — none of which exist in the live feed), so
// treat this file, not the vendor docs, as the contract.
package lotto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

const (
	pathLotteries = "/api/v1/lotteries"
	pathResult    = "/api/v1/result"
)

// MaxHistoryLimit is upstream's server-side cap: ?limit=500 returns 100.
const MaxHistoryLimit = 100

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	limiter *rate.Limiter
}

// New builds a client paced below the plan's per-minute quota. rps <= 0
// falls back to the Starter plan's 60 req/min with headroom.
func New(baseURL, apiKey string, rps float64) *Client {
	if rps <= 0 {
		rps = 0.9 // ~54 req/min, inside Starter's 60
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 20 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(rps), 1),
	}
}

// APIError is an upstream-level failure: a response carrying
// {"success": false, "error": {"code", "message"}}. Branch on Code — the
// messages are Thai prose and change without notice.
type APIError struct {
	Path    string
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("lotto: %s: api error (%s): %s", e.Path, e.Code, e.Message)
}

// Error codes seen from upstream.
const (
	CodeMissingAPIKey = "missing_api_key"
	CodeInvalidAPIKey = "invalid_api_key"
	// CodeRateLimited: "เกินขีดจำกัด 60 requests/นาที (รวมทุก key)". Note
	// "รวมทุก key" — the quota is per account, not per key, so issuing a
	// second key buys nothing and any other process on the same account
	// (a one-shot backfill run next to the live service, say) spends from
	// the same budget.
	CodeRateLimited = "rate_limit_exceeded"
)

// rateLimitRetries is how many times fetch re-tries a rate-limited call
// before giving up. Upstream's window is a minute, so the backoff below
// (15s, 30s, 60s) spans one full window.
const rateLimitRetries = 3

// IsRateLimited reports whether err is upstream's quota rejection.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == CodeRateLimited
}

type envelope[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("lotto: bad url: %w", err)
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// Header auth, not a query param: keeps the key out of upstream's access
	// logs and out of anything that echoes a URL.
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lotto: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && len(body) == 0 {
		return nil, fmt.Errorf("lotto: %s: status %d", path, resp.StatusCode)
	}
	return body, nil
}

// fetch performs a rate-limited GET and decodes the envelope's data field.
//
// A quota rejection is retried rather than returned: the limit is shared
// across the whole account, so a sweep of 162 markets can be pushed over the
// line by something entirely outside this process. Without the retry, one
// such collision aborts every market after it.
func fetch[T any](ctx context.Context, c *Client, path string, params url.Values) (T, error) {
	var zero T
	backoff := 15 * time.Second
	for attempt := 0; ; attempt++ {
		data, err := fetchOnce[T](ctx, c, path, params)
		if err == nil || !IsRateLimited(err) || attempt >= rateLimitRetries {
			return data, err
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func fetchOnce[T any](ctx context.Context, c *Client, path string, params url.Values) (T, error) {
	var zero T
	body, err := c.get(ctx, path, params)
	if err != nil {
		return zero, err
	}
	var env envelope[T]
	if err := json.Unmarshal(body, &env); err != nil {
		return zero, fmt.Errorf("lotto: %s: decode: %w", path, err)
	}
	if !env.Success {
		e := &APIError{Path: path}
		if env.Error != nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		return zero, e
	}
	return env.Data, nil
}

// FetchMarkets returns every market upstream lists — including ones it no
// longer feeds, which it does not flag (see model.MarkActive).
func (c *Client) FetchMarkets(ctx context.Context) ([]Market, error) {
	return fetch[[]Market](ctx, c, pathLotteries, nil)
}

// FetchHistory returns up to limit past draws for one market, newest first.
// limit above MaxHistoryLimit is clamped by upstream anyway; clamp here so
// callers page deliberately rather than silently getting 100.
func (c *Client) FetchHistory(ctx context.Context, slug string, limit int) (History, error) {
	if limit <= 0 || limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}
	params := url.Values{"limit": {strconv.Itoa(limit)}}
	return fetch[History](ctx, c, pathResult+"/"+url.PathEscape(slug)+"/history", params)
}

// FetchHistoryRange returns draws in [from, to) — upstream's `to` is
// exclusive, verified against hanoi-normal: from=2026-10-01&to=2026-10-07
// returns 01..06. Dates are "YYYY-MM-DD".
func (c *Client) FetchHistoryRange(ctx context.Context, slug, from, to string) (History, error) {
	params := url.Values{
		"from":  {from},
		"to":    {to},
		"limit": {strconv.Itoa(MaxHistoryLimit)},
	}
	return fetch[History](ctx, c, pathResult+"/"+url.PathEscape(slug)+"/history", params)
}
