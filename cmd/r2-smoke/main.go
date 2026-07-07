// Command r2-smoke verifies the R2 connection end-to-end: it uploads a tiny
// test object, checks Exists, fetches it back through the public URL, and
// reports each step. Reads the same R2_* env vars as the app.
//
//	Usage: R2_ACCOUNT_ID=... R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=... \
//	       R2_BUCKET=... R2_PUBLIC_BASE_URL=... go run ./cmd/r2-smoke
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/flip1688/livescore/internal/storage"
)

func main() {
	cfg := storage.Config{
		AccountID:       os.Getenv("R2_ACCOUNT_ID"),
		AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Bucket:          os.Getenv("R2_BUCKET"),
		PublicBaseURL:   os.Getenv("R2_PUBLIC_BASE_URL"),
	}
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" || cfg.PublicBaseURL == "" {
		fmt.Fprintln(os.Stderr, "all five R2_* env vars must be set")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r2, err := storage.New(ctx, cfg)
	if err != nil {
		fail("init client", err)
	}

	const key = "smoke/hello.txt"
	body := []byte("livescore r2 smoke test")

	if err := r2.Put(ctx, key, "text/plain", body); err != nil {
		fail("put", err)
	}
	fmt.Println("put:    OK", key)

	ok, err := r2.Exists(ctx, key)
	if err != nil {
		fail("exists", err)
	}
	if !ok {
		fail("exists", fmt.Errorf("object not found right after put"))
	}
	fmt.Println("exists: OK")

	url := r2.PublicURL(key)
	resp, err := http.Get(url)
	if err != nil {
		fail("public fetch", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		fail("public fetch", fmt.Errorf("status %d, body %q (want %q) — check public access / R2_PUBLIC_BASE_URL", resp.StatusCode, got, body))
	}
	fmt.Println("public: OK", url)
	fmt.Println("R2 connection verified.")
}

func fail(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: FAILED: %v\n", step, err)
	os.Exit(1)
}
