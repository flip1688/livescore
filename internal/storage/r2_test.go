package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestR2 starts an httptest server acting as the S3/R2 endpoint and
// returns an R2 client pointed at it plus a hook to inspect requests.
func newTestR2(t *testing.T, handler http.HandlerFunc) *R2 {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	r2, err := New(context.Background(), Config{
		AccountID:       "acct",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Bucket:          "mybucket",
		PublicBaseURL:   "https://cdn.example.com/",
		Endpoint:        srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r2
}

func TestExists(t *testing.T) {
	t.Run("200 -> true", func(t *testing.T) {
		r2 := newTestR2(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("method = %s, want HEAD", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		})
		ok, err := r2.Exists(context.Background(), "logos/teams/1-abcd1234.png")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !ok {
			t.Error("Exists = false, want true")
		}
	})

	t.Run("404 -> false", func(t *testing.T) {
		r2 := newTestR2(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		ok, err := r2.Exists(context.Background(), "logos/teams/missing.png")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if ok {
			t.Error("Exists = true, want false")
		}
	})
}

func TestPut(t *testing.T) {
	var gotPath, gotContentType, gotBody string
	r2 := newTestR2(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	err := r2.Put(context.Background(), "logos/teams/1-abcd1234.png", "image/png", []byte("fake-png-bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if want := "/mybucket/logos/teams/1-abcd1234.png"; gotPath != want {
		t.Errorf("path = %s, want %s", gotPath, want)
	}
	if gotContentType != "image/png" {
		t.Errorf("content-type = %s, want image/png", gotContentType)
	}
	if gotBody != "fake-png-bytes" {
		t.Errorf("body = %s, want fake-png-bytes", gotBody)
	}
}

func TestPublicURL(t *testing.T) {
	cases := []struct {
		base string
		key  string
		want string
	}{
		{"https://cdn.example.com", "logos/teams/1-abcd1234.png", "https://cdn.example.com/logos/teams/1-abcd1234.png"},
		{"https://cdn.example.com/", "logos/teams/1-abcd1234.png", "https://cdn.example.com/logos/teams/1-abcd1234.png"},
	}
	for _, c := range cases {
		r2 := &R2{base: c.base}
		if got := r2.PublicURL(c.key); got != c.want {
			t.Errorf("PublicURL(%q) with base %q = %s, want %s", c.key, c.base, got, c.want)
		}
	}
}
