package httpserver

import (
	"net/http"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

type httpSettingsRequest struct {
	MCPEnabled *bool `json:"mcp_enabled"`
}

func adminHTTPSettings(w http.ResponseWriter, r *http.Request) {
	state := serverStateFrom(r)
	if state == nil {
		writeError(w, http.StatusInternalServerError, "server state unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mcp_enabled": state.mcpEnabled.Load(),
		"mcp_toggle":  state.mcpToggle,
		"disable_mcp": config.Cfg.HTTP.DisableMCP,
	})
}

func adminPatchHTTPSettings(w http.ResponseWriter, r *http.Request) {
	var body httpSettingsRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.MCPEnabled == nil {
		writeError(w, http.StatusBadRequest, "mcp_enabled is required")
		return
	}
	state := serverStateFrom(r)
	if state == nil {
		writeError(w, http.StatusInternalServerError, "server state unavailable")
		return
	}
	if !state.mcpToggle {
		writeError(w, http.StatusConflict, "MCP is locked by startup options")
		return
	}
	enabled := *body.MCPEnabled
	previous := state.mcpEnabled.Swap(enabled)
	config.Cfg.HTTP.DisableMCP = !enabled
	if err := config.Cfg.Save(); err != nil {
		state.mcpEnabled.Store(previous)
		config.Cfg.HTTP.DisableMCP = !previous
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mcp_enabled": enabled,
		"mcp_toggle":  true,
		"disable_mcp": !enabled,
	})
}
