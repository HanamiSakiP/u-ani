package httpserver

import (
	"context"
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
		raw,
	})
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		useECH := mode == kazumi.ImageAccelerationECH && target == raw
		resp, err := kazumi.Get(ctx, target, map[string]string{
			"User-Agent": "uani/1.0.0 (image proxy)",
			"Accept":     "image/avif,image/webp,image/apng,image/*,*/*;q=0.8",
		}, useECH)
		if err != nil {
			cancel()
			continue
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			cancel()
			continue
		}
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "image/jpeg"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		cancel()
		return
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
