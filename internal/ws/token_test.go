package ws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

const testSecret = "test-secret"

// sign builds a valid "<exp>.<nonce>.<sig>" token for exp/nonce under
// secret, mirroring what the frontend is expected to produce.
func sign(secret string, exp int64, nonce string) string {
	signed := strconv.FormatInt(exp, 10) + "." + nonce
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	return signed + "." + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const nonce = "0123456789abcdef" // 16 hex chars, minimum length

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:  "valid, not yet expired",
			token: sign(testSecret, now.Add(time.Minute).Unix(), nonce),
		},
		{
			name:  "valid, exactly at exp",
			token: sign(testSecret, now.Unix(), nonce),
		},
		{
			name:  "valid, longer nonce",
			token: sign(testSecret, now.Add(time.Minute).Unix(), nonce+"00112233"),
		},
		{
			name:    "expired well past skew",
			token:   sign(testSecret, now.Add(-time.Hour).Unix(), nonce),
			wantErr: true,
		},
		{
			name:    "garbage token, no dots",
			token:   "not-a-token",
			wantErr: true,
		},
		{
			name:    "garbage token, empty string",
			token:   "",
			wantErr: true,
		},
		{
			name:    "malformed exp, not a number",
			token:   "notanumber." + nonce + "." + "aa",
			wantErr: true,
		},
		{
			name:    "malformed nonce, too short",
			token:   sign(testSecret, now.Add(time.Minute).Unix(), "abc123"),
			wantErr: true,
		},
		{
			name:    "malformed nonce, non-hex",
			token:   strconv.FormatInt(now.Add(time.Minute).Unix(), 10) + ".zzzzzzzzzzzzzzzz." + "aa",
			wantErr: true,
		},
		{
			name: "bad signature, tampered last byte of a valid token",
			token: func() string {
				valid := sign(testSecret, now.Add(time.Minute).Unix(), nonce)
				// Flip the last hex char of the sig so it no longer matches.
				flipped := "0"
				if valid[len(valid)-1] == '0' {
					flipped = "1"
				}
				return valid[:len(valid)-1] + flipped
			}(),
			wantErr: true,
		},
		{
			name:    "bad signature, wrong secret",
			token:   sign("wrong-secret", now.Add(time.Minute).Unix(), nonce),
			wantErr: true,
		},
		{
			name:  "skew boundary: exactly 30s past exp is still valid",
			token: sign(testSecret, now.Add(-tokenSkew).Unix(), nonce),
		},
		{
			name:    "skew boundary: 1s past the 30s grace is expired",
			token:   sign(testSecret, now.Add(-tokenSkew-time.Second).Unix(), nonce),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyToken(testSecret, tt.token, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("verifyToken(%q) err = %v, wantErr %v", tt.token, err, tt.wantErr)
			}
		})
	}
}

// TestVerifyToken_EmptySecretIsNotSpecialCased documents that verifyToken
// itself has no "empty secret" passthrough — an empty secret is just
// another HMAC key. The actual passthrough (skip the check entirely) lives
// one layer up in Handler, which never calls verifyToken when
// cfg.TokenSecret == "". Exercised end-to-end in
// TestHandlerEmptySecretAllowsAnyRequest.
func TestVerifyToken_EmptySecretIsNotSpecialCased(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token := sign("", now.Add(time.Minute).Unix(), "0123456789abcdef")

	if err := verifyToken("", token, now); err != nil {
		t.Fatalf("verifyToken with empty secret on both sides should still validate its own signature: %v", err)
	}
	if err := verifyToken("not-empty", token, now); err == nil {
		t.Fatalf("token signed with empty secret must not verify against a different secret")
	}
}
