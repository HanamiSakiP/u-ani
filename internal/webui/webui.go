// Package webui embeds the built-in uani web frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var assets embed.FS

// Handler serves the embedded single-page frontend with SPA fallback.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	index, _ := fs.ReadFile(sub, "index.html")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." || clean == "" || clean == "index.html" {
			serveIndex(w, index)
			return
		}
		if info, err := fs.Stat(sub, clean); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		serveIndex(w, index)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
