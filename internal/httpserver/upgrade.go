package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/selfupdate"
)

type upgradeRequest struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
}

type sourceSyncRequest struct {
	Version string `json:"version"`
}

func adminUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, selfupdate.Check(ctx))
}

func adminSyncSource(w http.ResponseWriter, r *http.Request) {
	var body sourceSyncRequest
	if err := decodeBody(r, &body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	result, err := selfupdate.SyncSource(ctx, body.Version, selfupdate.SourceDir())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func adminUpgrade(w http.ResponseWriter, r *http.Request) {
	var body upgradeRequest
	if err := decodeBody(r, &body); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "remote"
	}
	if mode != "remote" && mode != "local" {
		writeError(w, http.StatusBadRequest, `mode must be "remote" or "local"`)
		return
	}
	// Keep installing if the browser disconnects, but bound the total runtime.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()

	sourceDir := selfupdate.SourceDir()
	sourceResult := selfupdate.SourceResult{}
	sourceError := ""
	var result selfupdate.Result
	var err error
	switch mode {
	case "local":
		version := strings.TrimSpace(body.Version)
		if version == "" {
			version = "latest"
		}
		sourceResult, err = selfupdate.SyncSource(ctx, version, sourceDir)
		if err != nil {
			if !selfupdate.SourceReady(sourceDir) {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("could not synchronize local source: %v", err))
				return
			}
			sourceError = err.Error()
		}
		result, err = selfupdate.InstallLocal(ctx, sourceDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		result, err = selfupdate.Install(ctx, body.Version)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sourceResult, err = selfupdate.SyncSource(ctx, result.Version, sourceDir)
		if err != nil {
			sourceError = err.Error()
		}
	}

	// Run migrations with the newly installed binary, which may know about
	// schema changes that the currently running process does not.
	migrationError := ""
	if result.Path != "" {
		migrateCtx, migrateCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		cmd := exec.CommandContext(migrateCtx, result.Path, "upgrade")
		cmd.Env = append(os.Environ(), "UANI_UPGRADE_DB_ONLY=1")
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Run(); err != nil {
			migrationError = fmt.Sprintf("%v: %s", err, output.String())
		}
		migrateCancel()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current":          result.Current,
		"version":          result.Version,
		"mode":             result.Mode,
		"path":             result.Path,
		"replaced":         result.Replaced,
		"output":           result.Output,
		"source_path":      sourceDir,
		"source_version":   sourceResult.Version,
		"source_error":     sourceError,
		"migration_error":  migrationError,
		"restart_required": true,
	})
}
