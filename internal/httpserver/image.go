package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

// handleImage proxies Bangumi images so the browser can use the configured
// image acceleration mode (including ECH), which a browser cannot negotiate by
// itself. Only Bangumi image hosts are allowed to avoid an open proxy.
func handleImage(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if !kazumi.IsBangumiImage(raw) {
		writeError(w, http.StatusBadRequest, "only Bangumi image URLs are supported")
		return
	}
	mode, _ := kazumi.NormalizeImageAcceleration(config.Cfg.Kazumi.ImageAcceleration)
	targets := uniqueStrings([]string{
		kazumi.ApplyImageURL(raw, mode),
		kazumi.ApplyImageURL(raw, kazumi.ImageAccelerationMirror),
		kazumi.ApplyImageURL(raw, kazumi.ImageAccelerationECH),
		raw,
	})
	if len(targets) == 0 {
		writeError(w, http.StatusBadGateway, "failed to load image")
		return
	}

	// Start with the configured mode, then hedge with the remaining fallbacks
	// every 700ms. This keeps the preferred route when it is fast while
	// avoiding a long stall when a direct connection is blocked.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	type imageResult struct {
		data        []byte
		contentType string
		err         error
	}
	results := make(chan imageResult, len(targets))
	headers := map[string]string{
		"User-Agent":    "uani/1.0.0 (image proxy)",
		"Accept":        "image/avif,image/webp,image/apng,image/*,*/*;q=0.8",
		"Cache-Control": "max-age=604800",
		"Referer":       "https://bgm.tv/",
	}
	next := 0
	started := 0
	startNext := func() {
		if next >= len(targets) {
			return
		}
		target := targets[next]
		useECH := mode == kazumi.ImageAccelerationECH && target == raw
		next++
		started++
		go func() {
			reqCtx, reqCancel := context.WithTimeout(ctx, 8*time.Second)
			defer reqCancel()
			resp, err := kazumi.Get(reqCtx, target, headers, useECH)
			if err != nil {
				results <- imageResult{err: err}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				results <- imageResult{err: fmt.Errorf("image HTTP %d", resp.StatusCode)}
				return
			}
			contentType := resp.Header.Get("Content-Type")
			if contentType == "" {
				contentType = "image/jpeg"
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
			if err != nil {
				results <- imageResult{err: err}
				return
			}
			results <- imageResult{data: data, contentType: contentType}
		}()
	}
	startNext()
	hedge := time.NewTimer(700 * time.Millisecond)
	defer hedge.Stop()
	received := 0
	for {
		if received >= started && next >= len(targets) {
			break
		}
		select {
		case result := <-results:
			received++
			if result.err == nil {
				cancel()
				w.Header().Set("Content-Type", result.contentType)
				w.Header().Set("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400, immutable")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(result.data)
				return
			}
			// A failed preferred route should immediately start the next one
			// instead of waiting for the hedge timer.
			startNext()
		case <-hedge.C:
			startNext()
			hedge.Reset(700 * time.Millisecond)
		case <-ctx.Done():
			return
		}
	}
	writeError(w, http.StatusBadGateway, "failed to load image")
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
