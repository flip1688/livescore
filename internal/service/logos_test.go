package service

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeStore is an in-memory storage.ObjectStore for tests.
type fakeStore struct {
	mu   sync.Mutex
	objs map[string][]byte
	cts  map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objs: map[string][]byte{}, cts: map[string]string{}}
}

func (f *fakeStore) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objs[key]
	return ok, nil
}

func (f *fakeStore) Put(_ context.Context, key, contentType string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objs[key] = bytes.Clone(body)
	f.cts[key] = contentType
	return nil
}

func (f *fakeStore) PublicURL(key string) string {
	return "https://cdn.test/" + key
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMirrorURL_FreshDownload(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("logo-bytes"))
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())

	url, err := lm.MirrorURL(context.Background(), "teams", "42", srv.URL+"/logo.png")
	if err != nil {
		t.Fatalf("MirrorURL: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if want := "https://cdn.test/logos/teams/42-"; len(url) < len(want) || url[:len(want)] != want {
		t.Errorf("url = %s, want prefix %s", url, want)
	}
	if got := url[len(url)-4:]; got != ".png" {
		t.Errorf("url ext = %s, want .png", got)
	}
	if len(store.objs) != 1 {
		t.Fatalf("stored objects = %d, want 1", len(store.objs))
	}
}

func TestMirrorURL_Idempotent(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("logo-bytes"))
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())
	ctx := context.Background()
	src := srv.URL + "/logo.png"

	url1, err := lm.MirrorURL(ctx, "teams", "42", src)
	if err != nil {
		t.Fatalf("MirrorURL #1: %v", err)
	}
	url2, err := lm.MirrorURL(ctx, "teams", "42", src)
	if err != nil {
		t.Fatalf("MirrorURL #2: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (second call should hit Exists, not re-download)", requests)
	}
	if url1 != url2 {
		t.Errorf("url1 = %s, url2 = %s, want equal", url1, url2)
	}
}

func TestMirrorURL_SourceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())

	_, err := lm.MirrorURL(context.Background(), "teams", "1", srv.URL+"/missing.png")
	if err == nil {
		t.Fatal("MirrorURL: want error on 404 source, got nil")
	}
}

func TestMirrorAll_PartialFailureDoesNotFailBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad.png" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("logo-bytes"))
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())

	src := map[string]string{
		"1": srv.URL + "/good1.png",
		"2": srv.URL + "/bad.png",
		"3": srv.URL + "/good2.png",
	}
	out := lm.MirrorAll(context.Background(), "teams", src)
	if len(out) != 3 {
		t.Fatalf("out len = %d, want 3", len(out))
	}
	if out["2"] != "" {
		t.Errorf("out[2] = %s, want empty (source 404)", out["2"])
	}
	if out["1"] == "" || out["3"] == "" {
		t.Errorf("out[1]/out[3] should be populated: %v", out)
	}
}

func TestMirrorURL_OversizedBodyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte("a"), 1<<20) // 1 MiB
		for i := 0; i < 3; i++ {                  // 3 MiB > 2 MiB cap
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())

	_, err := lm.MirrorURL(context.Background(), "teams", "1", srv.URL+"/big.png")
	if err == nil {
		t.Fatal("MirrorURL: want error on oversized body, got nil")
	}
	if len(store.objs) != 0 {
		t.Errorf("stored objects = %d, want 0 (rejected before Put)", len(store.objs))
	}
}

func TestExpectedURL_MatchesMirrorURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("logo-bytes"))
	}))
	defer srv.Close()

	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())
	src := srv.URL + "/logo.png"

	// The pending check (stored logo_url != ExpectedURL) only converges if
	// ExpectedURL predicts exactly what MirrorURL stamps.
	mirrored, err := lm.MirrorURL(context.Background(), "teams", "42", src)
	if err != nil {
		t.Fatalf("MirrorURL: %v", err)
	}
	if expected := lm.ExpectedURL("teams", "42", src); expected != mirrored {
		t.Errorf("ExpectedURL = %s, MirrorURL = %s, want equal", expected, mirrored)
	}
	if lm.ExpectedURL("teams", "42", "") != "" {
		t.Error("ExpectedURL with empty source should be empty")
	}
	// A changed source must produce a different URL so the doc reads as pending.
	if lm.ExpectedURL("teams", "42", src+"?v=2") == mirrored {
		t.Error("ExpectedURL should change when the source URL changes")
	}
}

func TestMirrorURL_UnusableSource(t *testing.T) {
	store := newFakeStore()
	lm := NewLogoMirror(store, "", discardLogger())

	for _, src := range []string{
		"", // no logo at all
		"http://zq.titan007.com/Image/league_match/images/?win007=sell", // thscore placeholder: empty filename
		"http://zq.titan007.com/Image/team/images/",
	} {
		url, err := lm.MirrorURL(context.Background(), "teams", "1", src)
		if err != nil {
			t.Fatalf("MirrorURL(%q): %v", src, err)
		}
		if url != "" {
			t.Errorf("MirrorURL(%q) = %s, want empty", src, url)
		}
		// ExpectedURL must agree, or the doc would stay pending forever.
		if exp := lm.ExpectedURL("teams", "1", src); exp != "" {
			t.Errorf("ExpectedURL(%q) = %s, want empty", src, exp)
		}
	}
	if len(store.objs) != 0 {
		t.Errorf("stored objects = %d, want 0", len(store.objs))
	}
}
