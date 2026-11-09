package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/download"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/postprocessor"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type sseSession struct {
	messages chan []byte
}

var (
	sseMu       sync.Mutex
	sseSessions = map[string]*sseSession{}
)

func mcpRouter(w http.ResponseWriter, r *http.Request) {
	if !mcpAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/mcp", "/mcp/":
		handleStreamableMCP(w, r)
	case "/mcp/sse", "/mcp/sse/":
		handleMCPSSE(w, r)
	case "/mcp/messages", "/mcp/messages/":
		handleMCPMessages(w, r)
	default:
		http.NotFound(w, r)
	}
}

func mcpAuthorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	return strings.TrimPrefix(header, "Bearer ") == config.Cfg.HTTP.AdminToken
}

func handleStreamableMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// handled below
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	responses, batch, sessionID := processRPCBody(body)
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var payload any = responses[0]
	if batch {
		payload = responses
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

func processRPCBody(body []byte) ([]rpcResponse, bool, string) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, false, ""
	}
	sessionID := ""
	if strings.HasPrefix(trimmed, "[") {
		var requests []rpcRequest
		if err := json.Unmarshal(body, &requests); err != nil {
			return []rpcResponse{{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}}}, true, ""
		}
		var responses []rpcResponse
		for i := range requests {
			response, newSession := processRPC(&requests[i])
			if newSession != "" {
				sessionID = newSession
			}
			if response != nil {
				responses = append(responses, *response)
			}
		}
		return responses, true, sessionID
	}

	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return []rpcResponse{{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}}}, false, ""
	}
	response, newSession := processRPC(&request)
	if response == nil {
		return nil, false, newSession
	}
	return []rpcResponse{*response}, false, newSession
}

func processRPC(request *rpcRequest) (*rpcResponse, string) {
	if request.ID == nil {
		// JSON-RPC notification: no response is sent.
		return nil, ""
	}
	sessionID := ""
	switch request.Method {
	case "initialize":
		sessionID = newSessionID()
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result: map[string]any{
				"protocolVersion": negotiatedProtocolVersion(request.Params),
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{"name": "uani", "version": config.Version},
				"instructions": "uani is a CLI tool for subscribing to and downloading bangumi (anime). " +
					"Use these tools to manage subscriptions, search for anime, check schedules, configure filters, trigger downloads, manage Kazumi rules and acceleration, search/download episodes through Kazumi rules, and sync Bangumi collections.",
			},
		}, sessionID
	case "ping":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}}, sessionID
	case "tools/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": mcpTools()}}, sessionID
	case "tools/call":
		result, err := handleToolCall(request.Params)
		if err != nil {
			return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcError{Code: -32602, Message: err.Error()}}, sessionID
		}
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result}, sessionID
	case "resources/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"resources": []any{}}}, sessionID
	case "resources/templates/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"resourceTemplates": []any{}}}, sessionID
	case "prompts/list":
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"prompts": []any{}}}, sessionID
	default:
		return &rpcResponse{JSONRPC: "2.0", ID: request.ID, Error: &rpcError{Code: -32601, Message: "Method not found"}}, sessionID
	}
}

func negotiatedProtocolVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 && json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		return p.ProtocolVersion
	}
	return mcpProtocolVersion
}

func newSessionID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func handleMCPSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sessionID := newSessionID()
	session := &sseSession{messages: make(chan []byte, 16)}
	sseMu.Lock()
	sseSessions[sessionID] = session
	sseMu.Unlock()
	defer func() {
		sseMu.Lock()
		delete(sseSessions, sessionID)
		sseMu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: endpoint\ndata: /mcp/messages/?session_id=%s\n\n", sessionID)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		case message := <-session.messages:
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", message)
			flusher.Flush()
		}
	}
}

func handleMCPMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = r.Header.Get("Mcp-Session-Id")
	}
	sseMu.Lock()
	session := sseSessions[sessionID]
	sseMu.Unlock()
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	responses, _, _ := processRPCBody(body)
	for i := range responses {
		encoded, err := json.Marshal(responses[i])
		if err != nil {
			continue
		}
		select {
		case session.messages <- encoded:
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func tool(name, description string, properties map[string]any, required ...string) mcpToolDef {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return mcpToolDef{Name: name, Description: description, InputSchema: schema}
}

func mcpTools() []mcpToolDef {
	stringProp := map[string]any{"type": "string"}
	intProp := map[string]any{"type": "integer"}
	boolProp := map[string]any{"type": "boolean"}
	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	intArray := map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	nullableInt := map[string]any{"type": []string{"integer", "null"}}
	nullableString := map[string]any{"type": []string{"string", "null"}}

	return []mcpToolDef{
		tool("cal", "Get the current-season bangumi calendar grouped by weekday.", map[string]any{
			"force_update": boolProp,
		}),
		tool("list", "List all currently followed bangumi subscriptions.", map[string]any{}),
		tool("add", "Subscribe to a bangumi by name, or add a bgm.tv subscription using bgm_id/bgm_url.", map[string]any{
			"name":           stringProp,
			"bgm_id":         stringProp,
			"bgm_url":        stringProp,
			"episode":        nullableInt,
			"season":         nullableInt,
			"episode_offset": nullableInt,
			"display_name":   nullableString,
		}),
		tool("delete", "Unsubscribe from a bangumi.", map[string]any{
			"name": stringProp,
		}, "name"),
		tool("search", "Search for bangumi episodes by keyword.", map[string]any{
			"keyword":     stringProp,
			"count":       intProp,
			"regex":       nullableString,
			"min_episode": nullableInt,
			"max_episode": nullableInt,
		}, "keyword"),
		tool("seen", "Get downloaded episode records for a followed bangumi.", map[string]any{
			"name": stringProp,
		}, "name"),
		tool("seen_forget", "Remove episodes from download records (triggers re-download on next update).", map[string]any{
			"name":     stringProp,
			"episode":  nullableInt,
			"episodes": intArray,
		}, "name"),
		tool("seen_mark", "Add episodes to download records (marks them as seen).", map[string]any{
			"name":     stringProp,
			"episode":  nullableInt,
			"episodes": intArray,
		}, "name"),
		tool("update", "Trigger bangumi update: check for new episodes and download them.", map[string]any{
			"names": stringArray,
		}),
		tool("download", "Manually download a specific episode by providing a torrent/magnet URL.", map[string]any{
			"name":         stringProp,
			"title":        stringProp,
			"episode":      intProp,
			"download_url": stringProp,
		}, "name", "title", "episode", "download_url"),
		tool("get_filter", "Get the download filter settings for a bangumi.", map[string]any{
			"name": stringProp,
		}, "name"),
		tool("set_filter", "Set download filter for a bangumi.", map[string]any{
			"name":     stringProp,
			"subtitle": stringArray,
			"include":  stringArray,
			"exclude":  stringArray,
			"regex":    nullableString,
		}, "name"),
		tool("postprocess", "Process completed downloads: move files to formatted paths and remove torrents.", map[string]any{}),
		tool("download_status", "Get download progress for all active tasks.", map[string]any{
			"limit": intProp,
		}),
		tool("set_status", "Set the follow lifecycle status of a bangumi.", map[string]any{
			"name":   stringProp,
			"status": map[string]any{"type": "string", "enum": []string{"STATUS_FOLLOWED", "STATUS_UPDATED_TODAY"}},
		}, "name", "status"),
		tool("kazumi_catalog", "Browse the configured Kazumi rule repository.", map[string]any{
			"keyword": stringProp,
		}),
		tool("kazumi_list", "List installed Kazumi rules.", map[string]any{}),
		tool("kazumi_install", "Install selected Kazumi rules from the repository.", map[string]any{
			"names": stringArray,
			"name":  stringProp,
			"all":   boolProp,
		}),
		tool("kazumi_update", "Update installed Kazumi rules (all when names is omitted).", map[string]any{
			"names": stringArray,
			"name":  stringProp,
		}),
		tool("kazumi_remove", "Remove installed Kazumi rules.", map[string]any{
			"names": stringArray,
			"name":  stringProp,
		}),
		tool("kazumi_get_network", "Get Kazumi rule repository and network acceleration settings.", map[string]any{}),
		tool("kazumi_set_network", "Update Kazumi rule repository and network acceleration settings.", map[string]any{
			"rule_repository":      stringProp,
			"bangumi_acceleration": stringProp,
			"rules_mirror":         stringProp,
			"image_acceleration":   stringProp,
		}),
		tool("kazumi_search", "Search a bangumi through an installed Kazumi rule.", map[string]any{
			"rule":    stringProp,
			"keyword": stringProp,
		}, "rule", "keyword"),
		tool("kazumi_chapters", "List episode roads for a Kazumi search result.", map[string]any{
			"rule":   stringProp,
			"source": stringProp,
		}, "rule", "source"),
		tool("kazumi_download", "Download an episode through a Kazumi rule using yt-dlp/ffmpeg.", map[string]any{
			"rule":    stringProp,
			"name":    stringProp,
			"episode": intProp,
			"title":   stringProp,
			"source":  stringProp,
		}, "rule", "name", "episode", "source"),
		tool("kazumi_download_status", "List Kazumi streaming download tasks.", map[string]any{}),
		tool("kazumi_files", "List downloaded Kazumi media files.", map[string]any{}),
		tool("bangumi_sync_status", "Get the Bangumi account and 追番 subscription sync settings.", map[string]any{}),
		tool("bangumi_sync", "Import the configured Bangumi 追番 collections as uani subscriptions.", map[string]any{
			"types":   stringArray,
			"episode": nullableInt,
			"latest":  boolProp,
			"dry_run": boolProp,
			"limit":   intProp,
		}),
		tool("bangumi_set_token", "Validate and save a Bangumi Access Token, optionally enabling sync.", map[string]any{
			"access_token": stringProp,
			"enabled":      boolProp,
			"sync_types":   stringArray,
		}, "access_token"),
		tool("bangumi_configure_sync", "Update Bangumi sync settings without changing the token.", map[string]any{
			"enabled":    boolProp,
			"sync_types": stringArray,
		}),
		tool("bangumi_logout", "Remove the saved Bangumi Access Token and disable sync.", map[string]any{}),
	}
}

func handleToolCall(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	value, err := callTool(params.Name, params.Arguments)
	if err != nil {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		}, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(encoded)}},
		"isError": false,
	}, nil
}

func callTool(name string, args map[string]any) (any, error) {
	switch name {
	case "cal":
		return toolCal(argBool(args, "force_update"))
	case "list":
		return toolList()
	case "add":
		return toolAdd(args)
	case "delete":
		return toolDelete(args)
	case "search":
		return toolSearch(args)
	case "seen":
		return toolSeen(args)
	case "seen_forget":
		return toolSeenChange(args, true)
	case "seen_mark":
		return toolSeenChange(args, false)
	case "update":
		return toolUpdate(args)
	case "download":
		return toolDownload(args)
	case "get_filter":
		return toolGetFilter(args)
	case "set_filter":
		return toolSetFilter(args)
	case "postprocess":
		return toolPostprocess()
	case "download_status":
		return toolDownloadStatus(args)
	case "set_status":
		return toolSetStatus(args)
	case "kazumi_catalog":
		return toolKazumiCatalog(args)
	case "kazumi_list":
		return toolKazumiList()
	case "kazumi_install":
		return toolKazumiInstall(args)
	case "kazumi_update":
		return toolKazumiUpdate(args)
	case "kazumi_remove":
		return toolKazumiRemove(args)
	case "kazumi_get_network":
		return toolKazumiGetNetwork()
	case "kazumi_set_network":
		return toolKazumiSetNetwork(args)
	case "kazumi_search":
		return toolKazumiSearch(args)
	case "kazumi_chapters":
		return toolKazumiChapters(args)
	case "kazumi_download":
		return toolKazumiDownload(args)
	case "kazumi_download_status":
		return toolKazumiDownloadStatus()
	case "kazumi_files":
		return toolKazumiFiles()
	case "bangumi_sync_status":
		return toolBangumiSyncStatus()
	case "bangumi_sync":
		return toolBangumiSync(args)
	case "bangumi_set_token":
		return toolBangumiSetToken(args)
	case "bangumi_configure_sync":
		return toolBangumiConfigureSync(args)
	case "bangumi_logout":
		return toolBangumiLogout()
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

func toolCal(force bool) (any, error) {
	weekly, err := controllers.Cal(force, nil)
	if err != nil {
		return nil, err
	}
	out := map[string][]map[string]any{}
	for day, items := range weekly {
		for _, item := range items {
			subs := make([]map[string]string, 0, len(item.SubtitleGroup))
			for _, sub := range item.SubtitleGroup {
				subs = append(subs, map[string]string{"id": sub.ID, "name": sub.Name})
			}
			entry := map[string]any{
				"name":           item.Name,
				"subtitle_group": subs,
				"update_day":     item.UpdateDay,
				"cover":          strings.TrimLeft(item.Cover, "/"),
				"status":         followedStatusName(item.Status),
				"episode":        nullableInt(item.Episode),
			}
			if item.ID != "" {
				entry["id"] = item.ID
			}
			if item.IsScript {
				entry["bangumi_name"] = item.Name
				entry["updated_time"] = item.UpdatedTime
			}
			out[day] = append(out[day], entry)
		}
	}
	return out, nil
}

func toolList() (any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	list, err := db.Conn.AllFollowedBangumi(db.BangumiStatusUpdating)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		info := map[string]any{
			"name":       item.Followed.BangumiName,
			"episode":    item.Followed.Episode(),
			"status":     followedStatusName(&item.Followed.Status),
			"updated_at": formatTimestamp(item.Followed.UpdatedTime),
			"update_day": item.Bangumi.UpdateDay,
			"season":     item.Followed.Season,
		}
		if item.Followed.EpisodeOffset != 0 {
			info["episode_offset"] = item.Followed.EpisodeOffset
		}
		if item.Followed.DisplayName != "" {
			info["display_name"] = item.Followed.DisplayName
		}
		out = append(out, info)
	}
	return out, nil
}

func toolAdd(args map[string]any) (any, error) {
	episode := 0
	var episodePtr *int = &episode
	if value, present := args["episode"]; present {
		if value == nil {
			episodePtr = nil
		} else {
			n := intValue(value)
			episodePtr = &n
		}
	}
	season := nullableIntArg(args, "season")
	offset := nullableIntArg(args, "episode_offset")
	display := nullableStringArg(args, "display_name")

	bgm, _ := argString(args, "bgm_url")
	if bgm == "" {
		bgm = argStringOrNumber(args, "bgm_id")
	}
	if bgm != "" {
		return resultMap(controllers.AddFromBgmTV(bgm, episodePtr, season, offset, display)), nil
	}

	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name or bgm_id/bgm_url")
	}
	return resultMap(controllers.Add(name, episodePtr, season, offset, display)), nil
}

func toolDelete(args map[string]any) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	return resultMap(controllers.Delete(name, false, true)), nil
}

func toolSearch(args map[string]any) (any, error) {
	keyword, ok := argString(args, "keyword")
	if !ok {
		return nil, fmt.Errorf("missing required argument: keyword")
	}
	count := 3
	if value, present := args["count"]; present && value != nil {
		count = intValue(value)
	}
	regex := nullableStringArg(args, "regex")
	minEpisode := nullableIntArg(args, "min_episode")
	maxEpisode := nullableIntArg(args, "max_episode")
	result := controllers.Search(keyword, count, regex, false, minEpisode, maxEpisode, false, nil)
	return resultMap(result), nil
}

func toolSeen(args map[string]any) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	return resultMap(controllers.Seen(name)), nil
}

func toolSeenChange(args map[string]any, forget bool) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	var episodes []int
	if raw, present := args["episodes"]; present && raw != nil {
		episodes = intSliceValue(raw)
	} else if raw, present := args["episode"]; present && raw != nil {
		episodes = []int{intValue(raw)}
	}
	var result controllers.Result
	if forget {
		result = controllers.SeenForgetBatch(name, episodes)
	} else {
		result = controllers.SeenMarkBatch(name, episodes)
	}
	out := resultMap(result)
	if result.Status == "success" && len(result.Episodes) > 0 {
		out["episodes"] = result.Episodes
	}
	return out, nil
}

func toolUpdate(args map[string]any) (any, error) {
	names := []string{}
	if raw, present := args["names"]; present && raw != nil {
		names = stringSliceValue(raw)
	}
	if err := controllers.Update(names, true, false); err != nil {
		return nil, err
	}
	if config.Cfg.EnablePathFormatter {
		postprocessor.ProcessCompletedDownloads()
	}
	target := "all subscriptions"
	if len(names) > 0 {
		target = strings.Join(names, ", ")
	}
	return map[string]any{"status": "success", "message": "Update completed for: " + target}, nil
}

func toolDownload(args map[string]any) (any, error) {
	name, _ := argString(args, "name")
	title, _ := argString(args, "title")
	url, _ := argString(args, "download_url")
	episode := 0
	if raw, present := args["episode"]; present && raw != nil {
		episode = intValue(raw)
	}
	ok := download.DownloadEpisode(model.Episode{Name: name, Title: title, Episode: episode, Download: url})
	if !ok {
		return map[string]any{"status": "error", "message": "Failed to download: " + title}, nil
	}
	return map[string]any{"status": "success", "message": "Download queued: " + title}, nil
}

func toolGetFilter(args map[string]any) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	return resultMap(controllers.Filter(name, nil, nil, nil, nil)), nil
}

func toolSetFilter(args map[string]any) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	subtitle := joinedStringListArg(args, "subtitle")
	include := joinedStringListArg(args, "include")
	exclude := joinedStringListArg(args, "exclude")
	regex := nullableStringArg(args, "regex")
	return resultMap(controllers.Filter(name, subtitle, include, exclude, regex)), nil
}

func toolPostprocess() (any, error) {
	if !config.Cfg.EnablePathFormatter {
		return map[string]any{"status": "skipped", "message": "path formatter is disabled"}, nil
	}
	postprocessor.ProcessCompletedDownloads()
	return map[string]any{"status": "success", "message": "Post-processing completed"}, nil
}

func toolDownloadStatus(args map[string]any) (any, error) {
	limit := 20
	if raw, present := args["limit"]; present && raw != nil {
		limit = intValue(raw)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	if err := ensureDB(); err != nil {
		return nil, err
	}
	status := db.DownloadStatusDownloading
	downloads, err := db.Conn.GetDownloads(&status)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(downloads, func(i, j int) bool { return downloads[i].ID > downloads[j].ID })
	driver, _ := download.GetDriver()
	out := make([]map[string]any, 0, len(downloads))
	for _, dl := range downloads {
		if len(out) >= limit {
			break
		}
		info := map[string]any{
			"name":    dl.BangumiName,
			"title":   dl.Title,
			"episode": dl.Episode,
			"task_id": nil,
		}
		if dl.TaskID != nil {
			info["task_id"] = *dl.TaskID
		}
		if dl.TaskID == nil || *dl.TaskID == "" {
			info["status"] = "no_task_id"
		} else if driver == nil {
			info["status"] = "unknown"
		} else if st, err := driver.GetStatus(*dl.TaskID); err != nil {
			info["status"] = "unknown"
		} else {
			info["status"] = st.String()
		}
		out = append(out, info)
	}
	return out, nil
}

func toolSetStatus(args map[string]any) (any, error) {
	name, ok := argString(args, "name")
	if !ok || name == "" {
		return nil, fmt.Errorf("missing required argument: name")
	}
	statusName, ok := argString(args, "status")
	if !ok {
		return nil, fmt.Errorf("missing required argument: status")
	}
	var status int
	switch statusName {
	case "STATUS_FOLLOWED":
		status = db.FollowedStatusFollowed
	case "STATUS_UPDATED_TODAY":
		status = db.FollowedStatusUpdated
	default:
		return map[string]any{
			"status":  "error",
			"message": fmt.Sprintf("Invalid follow status %q. Use one of: STATUS_FOLLOWED, STATUS_UPDATED_TODAY", statusName),
		}, nil
	}
	if err := ensureDB(); err != nil {
		return nil, err
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		return map[string]any{"status": "error", "message": fmt.Sprintf("Bangumi %s is not followed", name)}, nil
	}
	followed.Status = status
	if status == db.FollowedStatusUpdated {
		followed.UpdatedTime = time.Now().Unix()
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return nil, err
	}
	followStatus := followedStatusName(&followed.Status)
	return map[string]any{
		"status":        "success",
		"message":       fmt.Sprintf("Set %s follow status to %s", name, followStatus),
		"follow_status": followStatus,
	}, nil
}

func resultMap(result controllers.Result) map[string]any {
	out := map[string]any{
		"status":  result.Status,
		"message": result.Message,
	}
	for key, value := range result.Data {
		out[key] = value
	}
	if result.Options != nil {
		out["options"] = result.Options
	}
	return out
}

func followedStatusName(status *int) string {
	if status == nil {
		return "STATUS_NOT_FOLLOWED"
	}
	switch *status {
	case db.FollowedStatusDeleted:
		return "STATUS_DELETED"
	case db.FollowedStatusFollowed:
		return "STATUS_FOLLOWED"
	case db.FollowedStatusUpdated:
		return "STATUS_UPDATED_TODAY"
	case db.FollowedStatusEnd:
		return "STATUS_END_UNUSED"
	default:
		return fmt.Sprintf("STATUS_UNKNOWN_%d", *status)
	}
}

func formatTimestamp(timestamp int64) any {
	if timestamp == 0 {
		return nil
	}
	return time.Unix(timestamp, 0).Format(time.RFC3339)
}

func argString(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func argStringOrNumber(args map[string]any, key string) string {
	if text, ok := argString(args, key); ok {
		return text
	}
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case float32:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func argBool(args map[string]any, key string) bool {
	value, ok := args[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, _ := strconv.ParseBool(v)
		return parsed
	default:
		return false
	}
}

func nullableIntArg(args map[string]any, key string) *int {
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	n := intValue(value)
	return &n
}

func nullableStringArg(args map[string]any, key string) *string {
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		text = fmt.Sprint(value)
	}
	return &text
}

func joinedStringListArg(args map[string]any, key string) *string {
	value, ok := args[key]
	if !ok || value == nil {
		return nil
	}
	joined := strings.Join(stringSliceValue(value), ",")
	return &joined
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

func stringSliceValue(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if values, ok := value.([]string); ok {
			return values
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func intSliceValue(value any) []int {
	items, ok := value.([]any)
	if !ok {
		if values, ok := value.([]int); ok {
			return values
		}
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		out = append(out, intValue(item))
	}
	return out
}
