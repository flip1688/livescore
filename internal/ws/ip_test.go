package ws

import (
	"net/http"
	"testing"
)

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name       string
		cfConnIP   string
		xff        string
		remoteAddr string
		want       string
	}{
		{
			name:       "CF-Connecting-IP takes priority over everything",
			cfConnIP:   "203.0.113.9",
			xff:        "198.51.100.1, 10.0.0.1",
			remoteAddr: "10.0.0.2:12345",
			want:       "203.0.113.9",
		},
		{
			name:       "CF-Connecting-IP with surrounding whitespace is trimmed",
			cfConnIP:   "  203.0.113.9  ",
			remoteAddr: "10.0.0.2:12345",
			want:       "203.0.113.9",
		},
		{
			name:       "falls back to first hop of X-Forwarded-For",
			xff:        "198.51.100.1, 10.0.0.1",
			remoteAddr: "10.0.0.2:12345",
			want:       "198.51.100.1",
		},
		{
			name:       "X-Forwarded-For single value",
			xff:        "198.51.100.1",
			remoteAddr: "10.0.0.2:12345",
			want:       "198.51.100.1",
		},
		{
			name:       "falls back to RemoteAddr host when no headers present",
			remoteAddr: "10.0.0.2:12345",
			want:       "10.0.0.2",
		},
		{
			name:       "RemoteAddr without a port is used as-is",
			remoteAddr: "10.0.0.2",
			want:       "10.0.0.2",
		},
		{
			name:       "IPv6 RemoteAddr host is extracted",
			remoteAddr: "[::1]:54321",
			want:       "::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}, RemoteAddr: tt.remoteAddr}
			if tt.cfConnIP != "" {
				r.Header.Set("CF-Connecting-IP", tt.cfConnIP)
			}
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := extractClientIP(r); got != tt.want {
				t.Errorf("extractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
