package ws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// tokenSkew is how far past its stated expiry a WS access token is still
// accepted, to absorb clock drift between whatever issued the token and
// this server.
const tokenSkew = 30 * time.Second

// minNonceHexLen is the minimum accepted length of the token's nonce
// component (see verifyToken).
const minNonceHexLen = 16

var (
	errTokenMalformed = errors.New("ws: malformed token")
	errTokenBadSig    = errors.New("ws: bad token signature")
	errTokenExpired   = errors.New("ws: token expired")
)

// verifyToken checks a WS access token of the form "<exp>.<nonce>.<sig>"
// against secret:
//   - exp:   decimal unix-seconds timestamp
//   - nonce: 16+ hex characters
//   - sig:   lowercase-hex HMAC-SHA256("<exp>.<nonce>") keyed by secret
//
// now is the reference time used for the expiry check; a token is still
// accepted up to tokenSkew after its exp to absorb clock drift. Callers
// must not invoke this with an empty secret expecting "always valid" —
// that passthrough behavior belongs in the caller (an empty
// WS_TOKEN_SECRET disables token checking entirely, before this function
// is ever reached).
func verifyToken(secret, token string, now time.Time) error {
	signedPart, sig, ok := cutLast(token, ".")
	if !ok || sig == "" {
		return errTokenMalformed
	}
	expPart, nonce, ok := strings.Cut(signedPart, ".")
	if !ok || nonce == "" {
		return errTokenMalformed
	}

	exp, err := strconv.ParseInt(expPart, 10, 64)
	if err != nil {
		return errTokenMalformed
	}
	if len(nonce) < minNonceHexLen || !isHex(nonce) {
		return errTokenMalformed
	}
	if !isHex(sig) {
		return errTokenMalformed
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPart))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return errTokenBadSig
	}

	if now.After(time.Unix(exp, 0).Add(tokenSkew)) {
		return errTokenExpired
	}
	return nil
}

// cutLast splits s at the last occurrence of sep, mirroring strings.Cut but
// anchored to the end. Used to pull the sig off "<exp>.<nonce>.<sig>"
// without assuming exp/nonce never contain sep (they're validated as pure
// hex/decimal afterwards anyway, but this keeps parsing unambiguous).
func cutLast(s, sep string) (before, after string, ok bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// isHex reports whether s is non-empty and every byte is a hex digit
// (either case).
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
