package httpserver

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// gzipResponseWriter defers the compression decision until the first write so
// it can inspect the Content-Type chosen by the handler. Media responses
// (video/audio/image) are passed through untouched.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	decided     bool
	compress    bool
	wroteHeader bool
}

func (w *gzipResponseWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	ct := strings.ToLower(w.Header().Get("Content-Type"))
	switch {
	case strings.HasPrefix(ct, "text/"),
		strings.Contains(ct, "json"),
		strings.Contains(ct, "javascript"),
		strings.Contains(ct, "xml"),
		strings.Contains(ct, "svg"):
		w.compress = true
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length")
	}
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.decide()
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.decide()
		w.wroteHeader = true
	}
	if w.compress {
		if w.gz == nil {
			w.gz = gzip.NewWriter(w.ResponseWriter)
		}
		return w.gz.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *gzipResponseWriter) Close() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

// gzipMiddleware compresses text-ish responses when the client supports gzip.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/kazumi-files/"),
			strings.HasPrefix(r.URL.Path, "/api/image"),
			strings.HasPrefix(r.URL.Path, "/mcp"):
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}

// cacheMiddleware sets browser cache headers for static assets.
func cacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case p == "/" || p == "/index.html":
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(p, "/api/"):
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// WithMiddleware wraps the base handler with compression and cache policy.
func WithMiddleware(next http.Handler) http.Handler {
	return cacheMiddleware(gzipMiddleware(next))
}
