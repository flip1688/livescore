// Package thscore is the only place in the codebase that talks to the
// thscore API (see docs/thscore-api.md).
//
// Every endpoint has its own rate limiter because thscore's hard limits vary
// wildly per endpoint (1 call/day for dictionaries down to 1 call/second for
// live deltas). The limiter intervals below follow the *recommended* rates
// from the docs, staying well inside the hard limits.
//
// The auth mechanism is not published in the public docs — it comes with the
// paid plan credentials. Wire it into authorize() once known.
package thscore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// Endpoint paths, verbatim from docs/thscore-api.md.
const (
	pathLeagueBasic    = "/football_th/league/basic.aspx"
	pathLeague         = "/football_th/league.aspx"
	pathTeam           = "/football_th/team.aspx"
	pathCountry        = "/football_th/country.aspx"
	pathScheduleBasic  = "/football_th/schedule/basic.aspx"
	pathScheduleModify = "/football_th/schedule/modify.aspx"
	pathLivescores     = "/football_th/livescores.aspx"
	pathLiveChanges    = "/football_th/livescores/changes.aspx"
	pathEvents         = "/football_th/events.aspx"
	pathStanding       = "/football_th/standing/league.aspx"
	pathAnalysis       = "/football_th/analysis.aspx"
	pathStats          = "/football_th/stats.aspx"
	pathCorner         = "/football_th/corner.aspx"
	pathLineups        = "/football_th/lineups.aspx"
	pathStandingCup    = "/football_th/standing/cup.aspx"
)

type Client struct {
	baseURL  string
	apiKey   string
	http     *http.Client
	limiters map[string]*rate.Limiter
}

func New(baseURL, apiKey string) *Client {
	every := func(d time.Duration) *rate.Limiter {
		return rate.NewLimiter(rate.Every(d), 1)
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 15 * time.Second},
		limiters: map[string]*rate.Limiter{
			// dictionaries — hard limit 30m–1h/call; we sync these on a
			// daily cron so a 30m floor here is a safety net, not the pacer
			pathLeagueBasic:    every(1 * time.Hour),
			pathLeague:         every(30 * time.Minute),
			pathTeam:           every(30 * time.Minute),
			pathCountry:        every(30 * time.Minute),
			pathStanding:       every(1 * time.Minute), // hard 5s, recommended 1/day
			pathAnalysis:       every(1 * time.Minute), // hard 1s, recommended 6h; per-match
			pathScheduleBasic:  every(60 * time.Second),
			pathScheduleModify: every(60 * time.Second),
			// live path — recommended cadences
			pathLivescores:  every(1 * time.Minute),
			pathLiveChanges: every(5 * time.Second),
			pathEvents:      every(1 * time.Minute),
			pathStats:       every(1 * time.Minute), // hard 10s (recommended)
			// no Fetch* methods yet — limiters reserved for when we add them
			pathCorner:      every(1 * time.Minute),
			pathLineups:     every(1 * time.Minute),
			pathStandingCup: every(1 * time.Minute),
		},
	}
}

// get performs a rate-limited GET and returns the raw body. It blocks until
// the endpoint's limiter grants a slot or ctx is canceled.
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if lim, ok := c.limiters[path]; ok {
		if err := lim.Wait(ctx); err != nil {
			return nil, err
		}
	}

	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("thscore: bad url: %w", err)
	}
	if params == nil {
		params = url.Values{}
	}
	c.authorize(params)
	if q := u.RawQuery; q != "" {
		// paths like events.aspx?cmd=shot carry a query of their own
		u.RawQuery = q + "&" + params.Encode()
	} else {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("thscore: %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("thscore: %s: status %d: %s", path, resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

// authorize attaches credentials: thscore takes an api_key query param on
// every request (confirmed from the ChangPuakk/widgets reference client).
func (c *Client) authorize(params url.Values) {
	if c.apiKey != "" {
		params.Set("api_key", c.apiKey)
	}
}

// envelope is thscore's response wrapper: {"code": int, "message": string,
// "data": ...}. Endpoints that don't use the wrapper simply omit code/message,
// which decode to the zero value (0, "") — indistinguishable from an
// explicit success, which is the correct behavior per docs.
type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// fetch performs a rate-limited GET against path and decodes the envelope's
// data field into T. A non-zero envelope code is an error — thscore returns
// HTTP 200 with code != 0 on rate limit, so this is the only reliable
// failure signal for those cases.
func fetch[T any](ctx context.Context, c *Client, path string, params url.Values) (T, error) {
	var zero T
	body, err := c.get(ctx, path, params)
	if err != nil {
		return zero, err
	}
	var env envelope[T]
	if err := json.Unmarshal(body, &env); err != nil {
		return zero, fmt.Errorf("thscore: %s: decode: %w", path, err)
	}
	if env.Code != 0 {
		return zero, fmt.Errorf("thscore: %s: api error (code %d): %s", path, env.Code, env.Message)
	}
	return env.Data, nil
}

// --- Dictionary ---

// FetchLeagues pulls the full league profile dictionary.
// modifiedWithinDays > 0 limits to recently-changed leagues (incremental sync).
func (c *Client) FetchLeagues(ctx context.Context, modifiedWithinDays int) ([]LeagueProfile, error) {
	p := url.Values{}
	if modifiedWithinDays > 0 {
		p.Set("day", fmt.Sprint(modifiedWithinDays))
	}
	return fetch[[]LeagueProfile](ctx, c, pathLeague, p)
}

// FetchTeams pulls the team profile dictionary. page is 1–5, 0 for all;
// modifiedWithinDays > 0 for incremental sync.
func (c *Client) FetchTeams(ctx context.Context, page, modifiedWithinDays int) ([]TeamProfile, error) {
	p := url.Values{}
	if page > 0 {
		p.Set("page", fmt.Sprint(page))
	}
	if modifiedWithinDays > 0 {
		p.Set("day", fmt.Sprint(modifiedWithinDays))
	}
	return fetch[[]TeamProfile](ctx, c, pathTeam, p)
}

// FetchCountries pulls the country list (has Thai names in countryTh).
func (c *Client) FetchCountries(ctx context.Context) ([]Country, error) {
	return fetch[[]Country](ctx, c, pathCountry, nil)
}

// --- Schedule ---

// FetchScheduleByDate pulls fixtures/results for a date (yyyy-MM-dd, GMT+7).
// Note: date, leagueId and matchId are mutually exclusive upstream.
func (c *Client) FetchScheduleByDate(ctx context.Context, date string) ([]LivescoreMatch, error) {
	return fetch[[]LivescoreMatch](ctx, c, pathScheduleBasic, url.Values{"date": {date}})
}

// FetchScheduleByLeague pulls a league's fixtures; season "" = current.
func (c *Client) FetchScheduleByLeague(ctx context.Context, leagueID, season string) ([]LivescoreMatch, error) {
	p := url.Values{"leagueId": {leagueID}}
	if season != "" {
		p.Set("season", season)
	}
	return fetch[[]LivescoreMatch](ctx, c, pathScheduleBasic, p)
}

// FetchScheduleByMatchIDs pulls specific matches (max 50 ids per call) — used
// to re-fetch matches flagged by the modification feed.
func (c *Client) FetchScheduleByMatchIDs(ctx context.Context, matchIDs []string) ([]LivescoreMatch, error) {
	return fetch[[]LivescoreMatch](ctx, c, pathScheduleBasic, url.Values{"matchId": {strings.Join(matchIDs, ",")}})
}

// FetchScheduleModifications pulls deletions/reschedules from the past 12h.
func (c *Client) FetchScheduleModifications(ctx context.Context) ([]ScheduleModification, error) {
	return fetch[[]ScheduleModification](ctx, c, pathScheduleModify, nil)
}

// --- Live ---

// FetchLivescores pulls the full snapshot of today's matches (today = GMT+0).
func (c *Client) FetchLivescores(ctx context.Context) ([]LivescoreMatch, error) {
	return fetch[[]LivescoreMatch](ctx, c, pathLivescores, nil)
}

// FetchLiveChanges pulls only matches that changed in the last 20 seconds —
// the primary high-frequency poll during live windows.
func (c *Client) FetchLiveChanges(ctx context.Context) ([]LivescoreChange, error) {
	return fetch[[]LivescoreChange](ctx, c, pathLiveChanges, nil)
}

// FetchRecentEvents pulls match events updated in the last 3 minutes.
func (c *Client) FetchRecentEvents(ctx context.Context) ([]EventsMatch, error) {
	return fetch[[]EventsMatch](ctx, c, pathEvents, url.Values{"cmd": {"new"}})
}

// --- Stats ---

// FetchStanding pulls the league table. subLeagueID "" for the default stage.
// The response shape is complex (six standing views + color zones) and not
// needed yet, so it is returned raw.
func (c *Client) FetchStanding(ctx context.Context, leagueID, subLeagueID string) ([]byte, error) {
	p := url.Values{"leagueId": {leagueID}}
	if subLeagueID != "" {
		p.Set("subLeagueId", subLeagueID)
	}
	return c.get(ctx, pathStanding, p)
}

// FetchAnalysis pulls H2H/form/goal stats for one match (upstream caches
// 24h). The response shape is complex and not needed yet, so it is returned
// raw.
func (c *Client) FetchAnalysis(ctx context.Context, matchID string) ([]byte, error) {
	return c.get(ctx, pathAnalysis, url.Values{"matchId": {matchID}})
}

// FetchLiveStats pulls technical match stats (possession/shots/...). date is
// yyyy-MM-dd (GMT+0 day boundary); empty means "today" per the API default.
func (c *Client) FetchLiveStats(ctx context.Context, date string) ([]StatsMatch, error) {
	p := url.Values{}
	if date != "" {
		p.Set("date", date)
	}
	return fetch[[]StatsMatch](ctx, c, pathStats, p)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
