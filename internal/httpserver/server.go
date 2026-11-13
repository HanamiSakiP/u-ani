// Package httpserver implements the uani HTTP API, static frontend, calendar
// feed, and MCP endpoints. It mirrors the upstream bgmi_http service.
package httpserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
	"github.com/HanamiSakiP/u-ani/internal/webui"
)

// Options controls which optional HTTP services are exposed.
type Options struct {
	Web bool
	MCP bool
}

// DefaultOptions enables the embedded Web UI and MCP endpoints.
func DefaultOptions() Options {
	return Options{Web: true, MCP: true}
}

// Handler builds the default HTTP handler tree (Web UI and MCP enabled).
func Handler() http.Handler {
	return HandlerWithOptions(DefaultOptions())
}

// HandlerWithOptions builds the HTTP handler tree with optional Web/MCP
// services. The JSON API, media files and calendar feed are always available.
func HandlerWithOptions(options Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", apiRouter)
	if options.MCP {
		mux.HandleFunc("/mcp", mcpRouter)
		mux.HandleFunc("/mcp/", mcpRouter)
	} else {
		mux.HandleFunc("/mcp", http.NotFound)
		mux.HandleFunc("/mcp/", http.NotFound)
	}
	mux.HandleFunc("/resource/calendar.ics", calendarICS)
	mux.Handle("/covers/", http.StripPrefix("/covers/", staticHandler(utils.CoverCacheDir(), false)))
	// Backward compatibility for cached payloads generated before the cover
	// cache moved to cache_path/covers.
	mux.Handle("/bangumi/.cover/", http.StripPrefix("/bangumi/.cover/", staticHandler(utils.CoverCacheDir(), false)))
	mux.Handle("/bangumi/", http.StripPrefix("/bangumi/", staticHandler(config.Cfg.SavePath, false)))
	mux.Handle("/kazumi-files/", http.StripPrefix("/kazumi-files/", staticHandler(controllers.KazumiFilesDir(), false)))

	if !options.Web {
		mux.HandleFunc("/", http.NotFound)
		return WithMiddleware(mux)
	}

	// An installed legacy frontend wins when explicitly enabled. Otherwise the
	// bundled web UI is always available, so `uani http` works out of the box.
	if config.Cfg.HTTP.ServeStaticFiles {
		if _, err := os.Stat(filepath.Join(config.Cfg.FrontStaticPath, "index.html")); err == nil {
			mux.Handle("/", spaHandler(config.Cfg.FrontStaticPath))
			return WithMiddleware(mux)
		}
	}
	mux.Handle("/", webui.Handler())
	return WithMiddleware(mux)
}

// Run starts the HTTP server and blocks until it exits.
func Run(address string, port int, options Options) error {
	if db.Conn == nil {
		if _, err := db.New(); err != nil {
			return err
		}
	}
	addr := net.JoinHostPort(address, strconv.Itoa(port))
	if err := ensurePortFree(addr, port); err != nil {
		return err
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           HandlerWithOptions(options),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go controllers.DownloadCovers(true)
	services := []string{"api"}
	if options.Web {
		services = append(services, "web")
	}
	if options.MCP {
		services = append(services, "mcp")
	}
	fmt.Printf("uani HTTP Server listening on %s (%s)\n", addr, strings.Join(services, "+"))
	server.Handler = HandlerWithOptions(options)
	return server.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}

func decodeBody(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	return dec.Decode(out)
}
