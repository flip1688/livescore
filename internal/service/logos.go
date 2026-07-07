package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/flip1688/livescore/internal/storage"
)

// maxLogoBytes caps the download so a misbehaving/hostile source URL can't
// exhaust memory; every logo we've seen is well under this.
const maxLogoBytes = 2 << 20 // 2 MiB

// logoWorkers bounds concurrent downloads during a dictionary sync so we
// don't hammer thscore's (flaky) CDN or our own outbound bandwidth.
const logoWorkers = 8

var allowedLogoExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// LogoMirror downloads team/league logos from thscore once and re-serves
// them from our own object storage — thscore's docs forbid hotlinking, and
// their CDN is a Chinese one that's known to be flaky (docs/widgets-repo-analysis.md).
type LogoMirror struct {
	store  storage.ObjectStore
	http   *http.Client
	log    *slog.Logger
	prefix string
}

// NewLogoMirror builds a mirror over store. dnsServer ("host:port", e.g.
// "1.1.1.1:53") makes logo downloads resolve hostnames through that server
// instead of the system resolver — needed on networks whose ISP DNS poisons
// thscore's logo CDN domains (Thai ISPs block titan007.com at DNS level and
// return unroutable addresses). Empty = system resolver.
func NewLogoMirror(store storage.ObjectStore, dnsServer string, log *slog.Logger) *LogoMirror {
	client := &http.Client{Timeout: 15 * time.Second}
	if dnsServer != "" {
		dialer := &net.Dialer{
			Timeout: 10 * time.Second,
			Resolver: &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					d := net.Dialer{Timeout: 5 * time.Second}
					return d.DialContext(ctx, network, dnsServer)
				},
			},
		}
		client.Transport = &http.Transport{DialContext: dialer.DialContext}
	}
	return &LogoMirror{
		store:  store,
		http:   client,
		log:    log,
		prefix: "logos",
	}
}

// MirrorURL ensures sourceURL is mirrored under our bucket and returns the
// public URL to use instead. The key is derived from the source URL's hash,
// so a daily re-sync is idempotent (unchanged source → same key, no
// re-download) while a changed source URL naturally lands on a new key.
func (l *LogoMirror) MirrorURL(ctx context.Context, kind, id, sourceURL string) (string, error) {
	if !usableLogoSource(sourceURL) {
		return "", nil
	}

	key := l.objectKey(kind, id, sourceURL)

	exists, err := l.store.Exists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("check existing logo: %w", err)
	}
	if exists {
		return l.store.PublicURL(key), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", fmt.Errorf("build logo request: %w", err)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("download logo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download logo: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogoBytes+1))
	if err != nil {
		return "", fmt.Errorf("read logo body: %w", err)
	}
	if len(body) > maxLogoBytes {
		return "", fmt.Errorf("logo body exceeds %d bytes", maxLogoBytes)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(logoExt(sourceURL))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := l.store.Put(ctx, key, contentType, body); err != nil {
		return "", fmt.Errorf("upload logo: %w", err)
	}
	return l.store.PublicURL(key), nil
}

// objectKey derives the deterministic bucket key for a source URL: hash of
// the source is part of the key, so an unchanged source always maps to the
// same object and a changed source lands on a new one.
func (l *LogoMirror) objectKey(kind, id, sourceURL string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	hash := hex.EncodeToString(sum[:])[:8]
	return fmt.Sprintf("%s/%s/%s-%s%s", l.prefix, kind, id, hash, logoExt(sourceURL))
}

// ExpectedURL returns the public URL sourceURL mirrors to, without any I/O.
// A stored logo_url that differs — empty, source changed, or public base URL
// changed — means the doc needs (re-)mirroring.
func (l *LogoMirror) ExpectedURL(kind, id, sourceURL string) string {
	if !usableLogoSource(sourceURL) {
		return ""
	}
	return l.store.PublicURL(l.objectKey(kind, id, sourceURL))
}

// usableLogoSource reports whether sourceURL actually names an image file.
// thscore sometimes emits placeholder URLs with an empty filename
// (".../images/?win007=sell") for entries that have no logo — those 403
// forever, so they are treated the same as an empty source. Keeping
// MirrorURL and ExpectedURL consistent here matters: both must agree that a
// junk source means "no logo", or the doc would stay pending on every run.
func usableLogoSource(sourceURL string) bool {
	if sourceURL == "" {
		return false
	}
	u, err := url.Parse(sourceURL)
	if err != nil {
		return false
	}
	return u.Path != "" && !strings.HasSuffix(u.Path, "/")
}

// MirrorAll mirrors every id→sourceURL pair concurrently (bounded pool) and
// never fails the batch: a per-item error is logged and yields "" for that
// id, so one flaky logo never blocks the rest of the dictionary sync.
func (l *LogoMirror) MirrorAll(ctx context.Context, kind string, srcByID map[string]string) map[string]string {
	out := make(map[string]string, len(srcByID))
	var mu sync.Mutex

	type job struct{ id, src string }
	jobs := make(chan job)
	var wg sync.WaitGroup

	for range logoWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				u, err := l.MirrorURL(ctx, kind, j.id, j.src)
				if err != nil {
					l.log.Warn("mirror logo", "kind", kind, "id", j.id, "err", err)
					u = ""
				}
				mu.Lock()
				out[j.id] = u
				mu.Unlock()
			}
		}()
	}

feed:
	for id, src := range srcByID {
		select {
		case jobs <- job{id, src}:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

// logoExt returns a safe, allowlisted file extension for key naming. Sources
// with an unknown/missing extension fall back to .png.
func logoExt(sourceURL string) string {
	if u, err := url.Parse(sourceURL); err == nil {
		ext := strings.ToLower(path.Ext(u.Path))
		if allowedLogoExt[ext] {
			return ext
		}
	}
	return ".png"
}
