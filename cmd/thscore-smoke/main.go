// Command thscore-smoke fires one real request per typed thscore endpoint and
// validates the live payload against our structs: it reports envelope errors,
// payload fields our structs don't know, struct fields the payload never sent,
// and strict-decode (type) mismatches. Raw payloads are saved to -out for
// inspection. One call per endpoint stays within every documented rate limit.
//
// Usage: THSCORE_BASE_URL=... THSCORE_API_KEY=... go run ./cmd/thscore-smoke -out /tmp/payloads
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/flip1688/livescore/internal/config"
	"github.com/flip1688/livescore/internal/thscore"
)

var (
	baseURL     string
	apiKey      string
	outDir      string
	only        map[string]bool
	cupLeagueID string
)

func main() {
	config.LoadDotEnv(".env") // so THSCORE_BASE_URL/THSCORE_API_KEY don't need exporting
	baseURL = os.Getenv("THSCORE_BASE_URL")
	apiKey = os.Getenv("THSCORE_API_KEY")

	flag.StringVar(&outDir, "out", "smoke-out", "directory for raw payload dumps")
	flag.StringVar(&cupLeagueID, "cupLeagueId", "75", "leagueId for the cup-standing case (default: 75, World Cup 2026)")
	onlyFlag := flag.String("only", "", "comma-separated endpoint names to test (default: all); respect the per-endpoint rate limits when re-running")
	flag.Parse()
	if *onlyFlag != "" {
		only = map[string]bool{}
		for _, n := range strings.Split(*onlyFlag, ",") {
			only[strings.TrimSpace(n)] = true
		}
	}
	if baseURL == "" || apiKey == "" {
		fmt.Fprintln(os.Stderr, "THSCORE_BASE_URL and THSCORE_API_KEY must be set")
		os.Exit(2)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	today := time.Now().In(time.FixedZone("GMT+7", 7*3600)).Format("2006-01-02")

	run[thscore.LeagueProfile]("league", "/football_th/league.aspx", nil)
	run[thscore.TeamProfile]("team", "/football_th/team.aspx", url.Values{"page": {"1"}})
	run[thscore.Country]("country", "/football_th/country.aspx", nil)
	run[thscore.LivescoreMatch]("schedule", "/football_th/schedule/basic.aspx", url.Values{"date": {today}})
	run[thscore.ScheduleModification]("modify", "/football_th/schedule/modify.aspx", nil)
	run[thscore.LivescoreMatch]("livescores", "/football_th/livescores.aspx", nil)
	run[thscore.LivescoreChange]("changes", "/football_th/livescores/changes.aspx", nil)
	run[thscore.EventsMatch]("events", "/football_th/events.aspx", url.Values{"cmd": {"new"}})
	run[thscore.StatsMatch]("stats", "/football_th/stats.aspx", nil)

	// standing/league.aspx is keyed by leagueId (hard limit 5s/call — only
	// ever called once here), so discover one from today's schedule first.
	if only == nil || only["standing"] {
		leagueID := discoverLeagueID(today)
		if leagueID == "" {
			fmt.Println("== standing: skipped (could not discover a leagueId from today's schedule)")
		} else {
			runObj[thscore.StandingResponse]("standing", "/football_th/standing/league.aspx", url.Values{"leagueId": {leagueID}})
		}
	}

	// standing/cup.aspx is keyed by leagueId too (assumed same 5s hard limit
	// as standing/league.aspx — undocumented). Unlike standing/league.aspx,
	// it uses the standard {"code","message","data"} envelope with "data" an
	// array holding one element, so it fits the generic `run` array-of-T
	// validator directly. Group-stage cup competitions (e.g. the World Cup)
	// aren't reliably discoverable from today's schedule, so the league id
	// is a flag defaulting to 75 (World Cup 2026, confirmed to have live
	// group-stage data 2026-07-09).
	if only == nil || only["cup-standing"] {
		run[thscore.CupStandingResponse]("cup-standing", "/football_th/standing/cup.aspx", url.Values{"leagueId": {cupLeagueID}})
	}

	// analysis.aspx is keyed by matchId (hard limit 1s/call, recommended 6h/
	// call per match) and has no typed struct — its field schema isn't
	// documented, so the payload is stored/served as an opaque JSON blob
	// (see internal/thscore.FetchAnalysis). Just confirm the envelope shape.
	if only == nil || only["analysis"] {
		runAnalysis(discoverMatchID(today))
	}
}

// discoverMatchID quietly fetches today's schedule and returns the first
// matchId found, to feed the "analysis" smoke case. Errors degrade to ""
// (the case is then skipped) rather than aborting the whole smoke run.
func discoverMatchID(date string) string {
	body, err := get("/football_th/schedule/basic.aspx", url.Values{"date": {date}})
	if err != nil {
		return ""
	}
	var env struct {
		Data []struct {
			MatchID json.Number `json:"matchId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 {
		return ""
	}
	return env.Data[0].MatchID.String()
}

// runAnalysis fetches analysis.aspx for one matchId and reports only the
// envelope shape (code/message/data present? "data" an object? which top-
// level keys?) — there is no typed struct for this endpoint to validate
// against, by design (see internal/thscore.FetchAnalysis).
func runAnalysis(matchID string) {
	name, path := "analysis", "/football_th/analysis.aspx"
	fmt.Printf("== %s (%s)\n", name, path)
	if matchID == "" {
		fmt.Println("   skipped (could not discover a matchId from today's schedule)")
		return
	}
	body, err := get(path, url.Values{"matchId": {matchID}})
	if err != nil {
		fmt.Printf("   FETCH ERROR: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".json"), body, 0o644); err != nil {
		fmt.Printf("   write dump: %v\n", err)
	}

	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		fmt.Printf("   ENVELOPE DECODE ERROR: %v (body: %.120s)\n", err, body)
		return
	}
	if env.Code != 0 {
		fmt.Printf("   API ERROR: code=%d message=%q\n", env.Code, env.Message)
		return
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		fmt.Println("   data: empty/null")
		return
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(env.Data, &inner); err != nil {
		fmt.Printf("   DATA NOT AN OBJECT: %v (data: %.120s)\n", err, env.Data)
		return
	}
	keys := make([]string, 0, len(inner))
	for k := range inner {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("   OK: envelope has 'data' object, keys: %s\n", strings.Join(keys, ", "))
}

// discoverLeagueID quietly fetches today's schedule and returns the first
// leagueId found, to feed the per-league "standing" smoke case. Errors
// degrade to "" (the standing case is then skipped) rather than aborting the
// whole smoke run.
func discoverLeagueID(date string) string {
	body, err := get("/football_th/schedule/basic.aspx", url.Values{"date": {date}})
	if err != nil {
		return ""
	}
	var env struct {
		Data []struct {
			LeagueID int `json:"leagueId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data) == 0 {
		return ""
	}
	return fmt.Sprint(env.Data[0].LeagueID)
}

// run fetches one endpoint and prints a validation report for element type T.
func run[T any](name, path string, params url.Values) {
	if only != nil && !only[name] {
		return
	}
	fmt.Printf("== %s (%s)\n", name, path)
	body, err := get(path, params)
	if err != nil {
		fmt.Printf("   FETCH ERROR: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".json"), body, 0o644); err != nil {
		fmt.Printf("   write dump: %v\n", err)
	}

	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		fmt.Printf("   ENVELOPE DECODE ERROR: %v (body: %.120s)\n", err, body)
		return
	}
	if env.Code != 0 {
		fmt.Printf("   API ERROR: code=%d message=%q\n", env.Code, env.Message)
		return
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		fmt.Println("   data: empty/null")
		return
	}

	var elems []json.RawMessage
	if err := json.Unmarshal(env.Data, &elems); err != nil {
		fmt.Printf("   DATA NOT AN ARRAY: %v (data: %.120s)\n", err, env.Data)
		return
	}

	known := jsonTags(reflect.TypeFor[T]())
	seen := map[string]bool{}
	strictErrs := map[string]int{}
	for _, raw := range elems {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			strictErrs["element not an object: "+err.Error()]++
			continue
		}
		for k := range m {
			seen[k] = true
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var v T
		if err := dec.Decode(&v); err != nil {
			strictErrs[err.Error()]++
		}
	}

	var extra, missing []string
	for k := range seen {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	for k := range known {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)

	fmt.Printf("   items: %d\n", len(elems))
	if len(extra) > 0 {
		fmt.Printf("   payload fields NOT in struct: %s\n", strings.Join(extra, ", "))
	}
	if len(missing) > 0 {
		fmt.Printf("   struct fields never in payload: %s\n", strings.Join(missing, ", "))
	}
	if len(strictErrs) > 0 {
		keys := make([]string, 0, len(strictErrs))
		for k := range strictErrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i == 8 {
				fmt.Printf("   ... %d more distinct strict-decode errors\n", len(keys)-8)
				break
			}
			fmt.Printf("   strict-decode (%dx): %s\n", strictErrs[k], k)
		}
	}
	if len(extra) == 0 && len(strictErrs) == 0 {
		fmt.Println("   OK: struct covers payload")
	}
}

// runObj validates a single-object response for element type T — unlike run,
// it does not expect the {"code","message","data"} envelope (standing/league
// returns the object directly) and Data is a single object, not an array.
func runObj[T any](name, path string, params url.Values) {
	if only != nil && !only[name] {
		return
	}
	fmt.Printf("== %s (%s)\n", name, path)
	body, err := get(path, params)
	if err != nil {
		fmt.Printf("   FETCH ERROR: %v\n", err)
		return
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".json"), body, 0o644); err != nil {
		fmt.Printf("   write dump: %v\n", err)
	}

	// The endpoint may still surface a rate-limit error as a top-level
	// {"code","message"} pair (see thscore.Client.FetchLeagueStanding).
	var probe struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &probe); err == nil && probe.Code != 0 {
		fmt.Printf("   API ERROR: code=%d message=%q\n", probe.Code, probe.Message)
		return
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		fmt.Printf("   DATA NOT AN OBJECT: %v (body: %.200s)\n", err, body)
		return
	}
	known := jsonTags(reflect.TypeFor[T]())
	var extra, missing []string
	for k := range m {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	for k := range known {
		if _, ok := m[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var v T
	strictErr := ""
	if err := dec.Decode(&v); err != nil {
		strictErr = err.Error()
	}

	if len(extra) > 0 {
		fmt.Printf("   payload fields NOT in struct: %s\n", strings.Join(extra, ", "))
	}
	if len(missing) > 0 {
		fmt.Printf("   struct fields never in payload: %s\n", strings.Join(missing, ", "))
	}
	if strictErr != "" {
		fmt.Printf("   strict-decode error: %s\n", strictErr)
	}
	if len(extra) == 0 && strictErr == "" {
		fmt.Println("   OK: struct covers payload")
	}
}

func get(path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", apiKey)
	u := baseURL + path + "?" + params.Encode()
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %.200s", resp.StatusCode, body)
	}
	time.Sleep(500 * time.Millisecond) // politeness gap between endpoints
	return body, nil
}

// jsonTags returns the set of top-level json field names of a struct type.
func jsonTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}
