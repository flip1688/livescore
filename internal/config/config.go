// Package config loads service configuration from environment variables,
// seeded from a local .env file when one exists.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// HTTP
	Port int

	// MongoDB Atlas
	MongoURI string
	MongoDB  string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// thscore upstream
	ThscoreBaseURL string
	ThscoreAPIKey  string
	// ThscoreRPS caps outbound calls to thscore (requests per second).
	ThscoreRPS float64

	// Cache TTLs
	DictionaryTTL time.Duration // leagues/teams
	LiveTTL       time.Duration // in-play matches

	// MatchRetentionDays is how long match-scoped documents (matches,
	// match_events, match_stats) live before MongoDB's TTL monitor
	// auto-deletes them. <= 0 disables TTL entirely (indexes are dropped if
	// present). Does not apply to standings/leagues/teams/countries.
	MatchRetentionDays int

	// AnalysisRetentionDays is the (shorter) TTL for match_analysis — the
	// pre-match H2H blob is the largest doc (~16KB) and worthless once the
	// match has passed, so it gets its own retention separate from
	// MatchRetentionDays. <= 0 disables that TTL index.
	AnalysisRetentionDays int

	// WSAllowedOrigins restricts WebSocket upgrades (comma-separated,
	// e.g. "https://example.com"). Empty = allow all (dev only).
	WSAllowedOrigins []string

	// CORSAllowedOrigins restricts which browser origins get CORS headers on
	// the REST API (comma-separated, e.g. "https://example.com"). Empty = no
	// CORS headers at all (same-origin/server-to-server only). "*" allows any
	// origin.
	CORSAllowedOrigins []string

	// Cloudflare R2 (S3-compatible) — mirrors team/league logos so we never
	// hotlink thscore's CDN. All five empty = mirroring disabled (dev mode,
	// source URLs passed through as-is).
	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Bucket          string
	R2PublicBaseURL   string

	// LogoDNSServer ("host:port") routes logo-CDN DNS lookups through a
	// specific resolver — some ISPs (notably Thai ones) poison titan007.com
	// at DNS level. Empty = system resolver.
	LogoDNSServer string
}

func Load() (*Config, error) {
	LoadDotEnv(".env")
	cfg := &Config{
		Port:           envInt("PORT", 8080),
		MongoURI:       os.Getenv("MONGO_URI"),
		MongoDB:        envStr("MONGO_DB", "livescore"),
		RedisAddr:      envStr("REDIS_ADDR", "localhost:6379"),
		RedisPassword:  os.Getenv("REDIS_PASSWORD"),
		RedisDB:        envInt("REDIS_DB", 0),
		ThscoreBaseURL: os.Getenv("THSCORE_BASE_URL"),
		ThscoreAPIKey:  os.Getenv("THSCORE_API_KEY"),
		ThscoreRPS:     envFloat("THSCORE_RPS", 1),
		DictionaryTTL:  envDuration("DICTIONARY_TTL", 6*time.Hour),
		LiveTTL:        envDuration("LIVE_TTL", 10*time.Second),

		MatchRetentionDays:    envInt("MATCH_RETENTION_DAYS", 30),
		AnalysisRetentionDays: envInt("ANALYSIS_RETENTION_DAYS", 7),

		R2AccountID:       os.Getenv("R2_ACCOUNT_ID"),
		R2AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2Bucket:          os.Getenv("R2_BUCKET"),
		R2PublicBaseURL:   os.Getenv("R2_PUBLIC_BASE_URL"),

		LogoDNSServer: os.Getenv("LOGO_DNS_SERVER"),
	}
	cfg.WSAllowedOrigins = envOriginList("WS_ALLOWED_ORIGINS")
	cfg.CORSAllowedOrigins = envOriginList("CORS_ALLOWED_ORIGINS")
	if cfg.MongoURI == "" {
		return nil, fmt.Errorf("MONGO_URI is required")
	}
	if err := cfg.validateR2(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateR2 enforces all-or-nothing: R2 mirroring is either fully
// configured or fully disabled, never half-set.
func (cfg *Config) validateR2() error {
	vars := map[string]string{
		"R2_ACCOUNT_ID":        cfg.R2AccountID,
		"R2_ACCESS_KEY_ID":     cfg.R2AccessKeyID,
		"R2_SECRET_ACCESS_KEY": cfg.R2SecretAccessKey,
		"R2_BUCKET":            cfg.R2Bucket,
		"R2_PUBLIC_BASE_URL":   cfg.R2PublicBaseURL,
	}
	var set, missing []string
	for name, v := range vars {
		if v != "" {
			set = append(set, name)
		} else {
			missing = append(missing, name)
		}
	}
	if len(set) > 0 && len(missing) > 0 {
		return fmt.Errorf("R2 config incomplete: set %v but missing %v (set all five R2_* vars or none)", set, missing)
	}
	return nil
}

// LoadDotEnv seeds os environment variables from a KEY=VALUE file so every
// binary works with a plain `.env` next to it — no `export` dance needed.
// Real environment variables always win over file values; a missing file is
// simply ignored. Lines starting with # and blank lines are skipped, and
// optional surrounding quotes on values are stripped. Exported so standalone
// tools that don't need the full Config (e.g. cmd/thscore-smoke) can seed
// their env the same way.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		os.Setenv(key, value)
	}
}

// envOriginList parses a comma-separated origin allowlist env var (used by
// both WS_ALLOWED_ORIGINS and CORS_ALLOWED_ORIGINS), trimming whitespace and
// dropping empty entries. Returns nil when the var is unset/empty.
func envOriginList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(v, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
