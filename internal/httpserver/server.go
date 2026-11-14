// Package httpserver implements the uani HTTP API, static frontend, calendar
// feed, and MCP endpoints. It mirrors the upstream bgmi_http service.
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
	"github.com/HanamiSakiP/u-ani/internal/webui"
)

// Options controls which optional HTTP services are exposed.
type Options struct {
	Web       bool
	MCP       bool
	MCPToggle bool
}

// DefaultOptions enables the embedded Web UI and MCP endpoints.
func DefaultOptions() Options {
	return Options{Web: true, MCP: true, MCPToggle: true}
}

// serverState holds per-handler runtime switches. Keeping it out of the
// global config avoids races between concurrent handler trees (for example in
// tests) while still allowing the Web UI to toggle MCP at runtime.
type serverState struct {
	mcpEnabled atomic.Bool
	mcpToggle  bool
}

type serverStateKey struct{}

func withServerState(next http.Handler, state *serverState) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), serverStateKey{}, state)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func serverStateFrom(r *http.Request) *serverState {
	state, _ := r.Context().Value(serverStateKey{}).(*serverState)
	return state
}

// Handler builds the default HTTP handler tree (Web UI and MCP enabled).
func Handler() http.Handler {
	return HandlerWithOptions(DefaultOptions())
}

// HandlerWithOptions builds the HTTP handler tree with optional Web/MCP
// services. The JSON API, media files and calendar feed are always available.
func HandlerWithOptions(options Options) http.Handler {
	mux := http.NewServeMux()
	state := &serverState{mcpToggle: options.MCPToggle}
	state.mcpEnabled.Store(options.MCP)
	mux.HandleFunc("/api/", apiRouter)
	if options.MCP || options.MCPToggle {
		mcpHandler := func(w http.ResponseWriter, r *http.Request) {
			if !state.mcpEnabled.Load() {
				http.NotFound(w, r)
				return
			}
			mcpRouter(w, r)
		}
		mux.HandleFunc("/mcp", mcpHandler)
		mux.HandleFunc("/mcp/", mcpHandler)
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
	} else if config.Cfg.HTTP.ServeStaticFiles {
		// An installed legacy frontend wins when explicitly enabled. Otherwise
		// the bundled web UI is always available, so `uani http` works out of
		// the box.
		if _, err := os.Stat(filepath.Join(config.Cfg.FrontStaticPath, "index.html")); err == nil {
			mux.Handle("/", spaHandler(config.Cfg.FrontStaticPath))
		} else {
			mux.Handle("/", webui.Handler())
		}
	} else {
		mux.Handle("/", webui.Handler())
	}
	return WithMiddleware(withServerState(mux, state))
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
