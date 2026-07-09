package thscore

import (
	"strconv"
	"time"
)

// RepairInvalidEscapes fixes thscore payloads that aren't valid JSON because
// a string contains an illegal escape sequence — analysis.aspx renders team
// names like "Women's" as `Women\s` (observed 2026-07-10, match 2970031).
// The repair drops the backslash of any escape that isn't legal JSON and
// keeps the following character; legal escapes and everything outside
// strings pass through untouched, so well-formed payloads round-trip
// byte-identical.
func RepairInvalidEscapes(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case !inStr:
			if c == '"' {
				inStr = true
			}
			out = append(out, c)
		case c == '"':
			inStr = false
			out = append(out, c)
		case c == '\\' && i+1 < len(b):
			switch n := b[i+1]; n {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				out = append(out, c, n)
			default:
				out = append(out, n) // illegal escape: drop the backslash
			}
			i++
		case c == '\\':
			// trailing backslash at EOF: drop it
		default:
			out = append(out, c)
		}
	}
	return out
}

// BangkokZone is the fixed GMT+7 offset thscore uses for every timestamp
// field documented as "Bangkok time" (see docs/thscore-api.md). It is a
// fixed offset, not a location, so it carries no DST rules — thscore's times
// don't need any.
var BangkokZone = time.FixedZone("ICT", 7*60*60)

// matchTimeLayout is the wire format for GMT+7 time strings across the
// schedule/livescores/events endpoints, e.g. "02-01-2006 15:04:05".
const matchTimeLayout = "02-01-2006 15:04:05"

// ParseMatchTime parses a thscore "dd-MM-yyyy HH:mm:ss" timestamp, which is
// always GMT+7 per docs. The returned time.Time carries the correct instant;
// callers must not re-interpret or re-convert the zone.
func ParseMatchTime(s string) (time.Time, error) {
	return time.ParseInLocation(matchTimeLayout, s, BangkokZone)
}

// ParseTimeAny converts livescores/changes' matchTime/startTime into an
// absolute instant. Docs describe a Unix timestamp encoded as JSON number or
// string, but live payloads (2026-07-08) send "dd-MM-yyyy HH:mm:ss" GMT+7
// strings — so v may be int64/int/float64 (unix seconds), a numeric string,
// or a GMT+7 datetime string. Returns false when v is absent, zero/negative,
// or unparseable.
func ParseTimeAny(v any) (time.Time, bool) {
	switch value := v.(type) {
	case int64:
		if value <= 0 {
			return time.Time{}, false
		}
		return time.Unix(value, 0).UTC(), true
	case int:
		if value <= 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(value), 0).UTC(), true
	case float64:
		if value <= 0 {
			return time.Time{}, false
		}
		return time.Unix(int64(value), 0).UTC(), true
	case string:
		if value == "" {
			return time.Time{}, false
		}
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			if n <= 0 {
				return time.Time{}, false
			}
			return time.Unix(n, 0).UTC(), true
		}
		if t, err := ParseMatchTime(value); err == nil {
			return t, true
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}
