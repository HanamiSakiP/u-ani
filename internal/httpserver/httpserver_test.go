package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

const testToken = "test-admin-token"

func setupHTTPTest(t *testing.T) string {
	t.Helper()
	oldConfig, oldConn := config.Cfg, db.Conn
	if db.Conn != nil {
		_ = db.Conn.Close()
		db.Conn = nil
	}
	dir := t.TempDir()
	config.Cfg = config.Config{
		DataSource:       config.SourceBangumiMoe,
		DownloadDelegate: "noop",
		SavePath:         filepath.Join(dir, "bangumi"),
		DBPath:           filepath.Join(dir, "bangumi.db"),
		ScriptPath:       filepath.Join(dir, "scripts"),
		TmpPath:          filepath.Join(dir, "tmp"),
		LogPath:          filepath.Join(dir, "log"),
		ToolsPath:        filepath.Join(dir, "tools"),
		FrontStaticPath:  filepath.Join(dir, "front_static"),
		MaxPath:          3,
		PathFormatter:    "{name}/S{season:02d}/E{episode:02d}.{suffix}",
		SavePathMap:      map[string]string{},
		HTTP:             config.HTTPConfig{AdminToken: testToken},
	}
	for _, p := range []string{config.Cfg.ScriptPath, config.Cfg.SavePath, config.Cfg.FrontStaticPath} {
		if err := ensureDir(p); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := db.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		db.Conn = nil
		config.Cfg = oldConfig
		db.Conn = oldConn
	})
	return dir
}

func ensureDir(path string) error {
	if path == "" {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

func seedBangumi(t *testing.T, displayName string, offset int) {
	t.Helper()
	if err := db.Conn.SaveBangumi(model.WebsiteBangumi{
		ID:        "b1",
		Name:      "Test Anime",
		UpdateDay: "Mon",
		Cover:     "https://example.com/cover.jpg",
		SubtitleGroup: []model.SubtitleGroup{
			{ID: "s1", Name: "Sub1"},
			{ID: "s2", Name: "Sub2"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveFollowed(&db.Followed{
		BangumiName:   "Test Anime",
		Episodes:      map[int]bool{1: true, 2: true},
		Status:        db.FollowedStatusFollowed,
		UpdatedTime:   time.Now().Unix(),
		Subtitle:      []string{"s1"},
		Season:        2,
		EpisodeOffset: offset,
		DisplayName:   displayName,
	}); err != nil {
		t.Fatal(err)
	}
}

func request(t *testing.T, server *httptest.Server, method, path string, body any, token string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func requestRaw(t *testing.T, server *httptest.Server, method, path string, body []byte, contentType, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func makeTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestAPIIndexCalendarAndPlayer(t *testing.T) {
	dir := setupHTTPTest(t)
	seedBangumi(t, "Test Anime", 10)

	for ep := 1; ep <= 2; ep++ {
		episodeDir := filepath.Join(dir, "bangumi", "Test Anime", strconv.Itoa(ep))
		if err := os.MkdirAll(episodeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(episodeDir, fmt.Sprintf("ep%d.mkv", ep)), []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/index/index", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", resp.StatusCode)
	}
	index := decodeJSON(t, resp)
	if index["version"] != config.Version {
		t.Errorf("version = %v", index["version"])
	}
	data, _ := index["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("index data length = %d", len(data))
	}
	item := data[0].(map[string]any)
	if item["id"] != "b1" || item["bangumi_name"] != "Test Anime" || item["episode"] != float64(2) {
		t.Errorf("unexpected index item: %v", item)
	}

	resp = request(t, server, http.MethodGet, "/api/calendar", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar status = %d", resp.StatusCode)
	}
	calendar := decodeJSON(t, resp)
	mon, _ := calendar["mon"].([]any)
	if len(mon) != 1 {
		t.Fatalf("calendar mon length = %d", len(mon))
	}
	calItem := mon[0].(map[string]any)
	if calItem["name"] != "Test Anime" || calItem["episode"] != float64(2) || calItem["status"] != float64(1) {
		t.Errorf("unexpected calendar item: %v", calItem)
	}

	resp = request(t, server, http.MethodGet, "/api/player/b1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("player status = %d", resp.StatusCode)
	}
	player := decodeJSON(t, resp)
	playerData := player["data"].(map[string]any)
	playerMap := playerData["player"].(map[string]any)
	if len(playerMap) != 2 {
		t.Errorf("player entries = %d", len(playerMap))
	}
	ep1 := playerMap["1"].(map[string]any)
	if ep1["path"] != "/Test Anime/1/ep1.mkv" {
		t.Errorf("player path = %v", ep1["path"])
	}
}

func TestAPIPlayerFormatted(t *testing.T) {
	dir := setupHTTPTest(t)
	seedBangumi(t, "Display Name", 10)
	config.Cfg.EnablePathFormatter = true

	file := filepath.Join(dir, "bangumi", "Display Name", "S02", "E12.mkv")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodGet, "/api/player/b1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("player status = %d", resp.StatusCode)
	}
	data := decodeJSON(t, resp)["data"].(map[string]any)
	playerMap := data["player"].(map[string]any)
	if _, ok := playerMap["2"]; !ok {
		t.Errorf("expected episode 2 in formatted player, got %v", playerMap)
	}
	if playerMap["2"].(map[string]any)["path"] != "/Display Name/S02/E12.mkv" {
		t.Errorf("unexpected formatted path: %v", playerMap["2"])
	}
}

func TestPlayerCommandLine(t *testing.T) {
	parts := splitCommandLine(`"C:\Program Files\mpv\mpv.exe" --fullscreen {path}`)
	if len(parts) != 3 || parts[0] != `C:\Program Files\mpv\mpv.exe` || parts[1] != "--fullscreen" || parts[2] != "{path}" {
		t.Fatalf("splitCommandLine() = %#v", parts)
	}
	args := withPlayerPath(parts[1:], `C:\Videos\EP1.mkv`)
	if len(args) != 2 || args[1] != `C:\Videos\EP1.mkv` {
		t.Fatalf("withPlayerPath() = %#v", args)
	}
	args = withPlayerPath([]string{"--fullscreen"}, "/tmp/EP1.mkv")
	if len(args) != 2 || args[1] != "/tmp/EP1.mkv" {
		t.Fatalf("withPlayerPath(append) = %#v", args)
	}
}

func TestResolveLocalMedia(t *testing.T) {
	dir := setupHTTPTest(t)
	video := filepath.Join(dir, "bangumi", "Show", "EP1.mkv")
	if err := os.MkdirAll(filepath.Dir(video), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveLocalMedia("/bangumi/Show/EP1.mkv")
	if err != nil {
		t.Fatalf("resolveLocalMedia() error = %v", err)
	}
	if got != video {
		t.Fatalf("resolveLocalMedia() = %q, want %q", got, video)
	}
	if _, err := resolveLocalMedia("/bangumi/../secret.mkv"); err == nil {
		t.Fatal("path traversal should be rejected")
	}
	if _, err := resolveLocalMedia("https://example.com/video.mp4"); err == nil {
		t.Fatal("remote URLs should be rejected")
	}
	if _, err := resolveLocalMedia("/bangumi/Show/missing.mkv"); err == nil {
		t.Fatal("missing files should be rejected")
	}
}

func TestAdminAddBgmTVSubject(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumi(model.WebsiteBangumi{
		ID:        "b1",
		Name:      "Test Anime",
		UpdateDay: "Mon",
	}); err != nil {
		t.Fatal(err)
	}
	bgmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/253" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   253,
			"name": "Test Anime",
		})
	}))
	defer bgmServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = bgmServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodPost, "/api/admin/add", map[string]any{"bgm_id": 253}, testToken)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("add bgm.tv status = %d: %s", resp.StatusCode, body)
	}
	body := decodeJSON(t, resp)
	if body["bangumi_name"] != "Test Anime" {
		t.Fatalf("add response bangumi_name = %v", body["bangumi_name"])
	}
	if _, err := db.Conn.GetFollowed("Test Anime"); err != nil {
		t.Fatalf("bgm.tv subject was not followed: %v", err)
	}
}

func TestAdminUpgradeRequiresAuth(t *testing.T) {
	setupHTTPTest(t)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/upgrade", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized upgrade status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodPost, "/api/admin/upgrade", map[string]any{"version": "../bad"}, testToken)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("invalid upgrade target status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if !strings.Contains(fmt.Sprint(body["detail"]), "invalid target") {
		t.Fatalf("unexpected error: %v", body)
	}
}

func TestAdminDeleteClearsCollection(t *testing.T) {
	setupHTTPTest(t)
	oldToken := config.Cfg.Bangumi.AccessToken
	config.Cfg.Bangumi.AccessToken = ""
	t.Cleanup(func() { config.Cfg.Bangumi.AccessToken = oldToken })
	seedBangumi(t, "Test Anime", 0)
	if err := db.Conn.SaveBangumiLink("Test Anime", 101); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveBangumiCollection(101, 3); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodPost, "/api/admin/delete", map[string]any{"bangumi": "Test Anime"}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if id, _ := body["subject_id"].(float64); id != 101 {
		t.Fatalf("delete subject_id = %v", body["subject_id"])
	}
	if value, err := db.Conn.GetBangumiCollection(101); err != nil || value != 0 {
		t.Fatalf("cached collection status = %d, %v; want tombstone 0", value, err)
	}
}

func TestEnrichSubjectPayloadClearsStaleCollection(t *testing.T) {
	setupHTTPTest(t)
	oldToken := config.Cfg.Bangumi.AccessToken
	config.Cfg.Bangumi.AccessToken = ""
	t.Cleanup(func() { config.Cfg.Bangumi.AccessToken = oldToken })
	seedBangumi(t, "Test Anime", 0)
	if err := db.Conn.SaveBangumiLink("Test Anime", 101); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveBangumiCollection(101, 3); err != nil {
		t.Fatal(err)
	}
	followed, err := db.Conn.GetFollowed("Test Anime")
	if err != nil {
		t.Fatal(err)
	}
	followed.Status = db.FollowedStatusDeleted
	if err := db.Conn.SaveFollowed(followed); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"collection_type": 3,
		"subscription":    map[string]any{"bangumi_name": "Test Anime"},
	}
	got := enrichSubjectPayload(101, payload)
	if _, ok := got["collection_type"]; ok {
		t.Fatalf("stale collection_type was not removed: %v", got["collection_type"])
	}
	if _, ok := got["subscription"]; ok {
		t.Fatalf("stale subscription was not removed: %v", got["subscription"])
	}
	if value, err := db.Conn.GetBangumiCollection(101); err != nil || value != 0 {
		t.Fatalf("collection cache = %d, %v; want tombstone 0", value, err)
	}
}

func TestAdminFilterSeenAndAuth(t *testing.T) {
	setupHTTPTest(t)
	seedBangumi(t, "Test Anime", 10)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodPost, "/api/admin/auth", map[string]any{}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("unauthorized status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodPost, "/api/admin/auth", map[string]any{}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("auth status = %d", resp.StatusCode)
	}
	decodeJSON(t, resp)

	escaped := url.PathEscape("Test Anime")
	resp = request(t, server, http.MethodGet, "/api/admin/filter/"+escaped, nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get filter status = %d", resp.StatusCode)
	}
	filter := decodeJSON(t, resp)
	if filter["regex"] != "" || filter["season"] != float64(2) {
		t.Errorf("unexpected filter: %v", filter)
	}

	resp = request(t, server, http.MethodPatch, "/api/admin/filter/"+escaped, map[string]any{
		"selected_subtitle": []string{"Sub2"},
		"include":           []string{"1080"},
		"exclude":           []string{"x265"},
		"regex":             ".*1080.*",
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch filter status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	followed, err := db.Conn.GetFollowed("Test Anime")
	if err != nil {
		t.Fatal(err)
	}
	if len(followed.Subtitle) != 1 || followed.Subtitle[0] != "s2" || followed.Regex != ".*1080.*" {
		t.Errorf("filter not saved: %+v", followed)
	}

	resp = request(t, server, http.MethodPost, "/api/admin/seen_forget", map[string]any{
		"bangumi": "Test Anime",
		"episode": 1,
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seen_forget status = %d", resp.StatusCode)
	}
	forgot := decodeJSON(t, resp)
	if forgot["episode"] != float64(1) {
		t.Errorf("unexpected seen_forget response: %v", forgot)
	}

	resp = request(t, server, http.MethodPost, "/api/admin/seen_mark", map[string]any{
		"bangumi":  "Test Anime",
		"episodes": []int{1, 3},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seen_mark status = %d", resp.StatusCode)
	}
	marked := decodeJSON(t, resp)
	seen, _ := marked["seen"].([]any)
	if len(seen) != 3 {
		t.Errorf("seen = %v", seen)
	}
}

func TestCalendarICSAndPlaceholder(t *testing.T) {
	setupHTTPTest(t)
	seedBangumi(t, "Test Anime", 0)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/resource/calendar.ics", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ics status = %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(data), "BEGIN:VCALENDAR") || !strings.Contains(string(data), "Test Anime") {
		t.Errorf("unexpected ics: %s", data)
	}

	resp = request(t, server, http.MethodGet, "/", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("web ui status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "uani") || !strings.Contains(string(body), "app.js") {
		t.Errorf("unexpected built-in web ui: %s", body)
	}
}

func TestStaticFrontendServing(t *testing.T) {
	dir := setupHTTPTest(t)
	config.Cfg.HTTP.ServeStaticFiles = true
	if err := os.MkdirAll(filepath.Join(dir, "front_static", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "front_static", "index.html"), []byte("<html>uani</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "front_static", "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/", nil, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "uani") {
		t.Errorf("index not served: %s", body)
	}

	resp = request(t, server, http.MethodGet, "/some/spa/route", nil, "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "uani") {
		t.Errorf("SPA fallback not served: %s", body)
	}

	resp = request(t, server, http.MethodGet, "/assets/app.js", nil, "")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "console.log(1)" {
		t.Errorf("asset not served: %s", body)
	}
}

func TestMCPInitializeToolsAndAuth(t *testing.T) {
	setupHTTPTest(t)
	seedBangumi(t, "Test Anime", 0)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-06-18"},
	}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthorized MCP status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"protocolVersion": "2025-06-18"},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("missing Mcp-Session-Id header")
	}
	init := decodeJSON(t, resp)
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "uani" {
		t.Errorf("serverInfo = %v", serverInfo)
	}

	resp = request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}, testToken)
	toolsResult := decodeJSON(t, resp)["result"].(map[string]any)
	tools := toolsResult["tools"].([]any)
	if len(tools) != 32 {
		t.Errorf("tool count = %d", len(tools))
	}

	resp = request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list",
			"arguments": map[string]any{},
		},
	}, testToken)
	call := decodeJSON(t, resp)
	callResult := call["result"].(map[string]any)
	if callResult["isError"] != false {
		t.Errorf("isError = %v", callResult["isError"])
	}
	content := callResult["content"].([]any)[0].(map[string]any)
	var listed []map[string]any
	if err := json.Unmarshal([]byte(content["text"].(string)), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0]["name"] != "Test Anime" || listed[0]["status"] != "STATUS_FOLLOWED" {
		t.Errorf("unexpected list result: %v", listed)
	}

	resp = request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "cal",
			"arguments": map[string]any{"force_update": false},
		},
	}, testToken)
	calCall := decodeJSON(t, resp)
	calResult := calCall["result"].(map[string]any)
	calContent := calResult["content"].([]any)[0].(map[string]any)
	var calendar map[string][]map[string]any
	if err := json.Unmarshal([]byte(calContent["text"].(string)), &calendar); err != nil {
		t.Fatal(err)
	}
	calDay := calendar["mon"]
	if len(calDay) != 1 || calDay[0]["id"] != "b1" || calDay[0]["status"] != "STATUS_FOLLOWED" {
		t.Errorf("unexpected cal result: %v", calendar)
	}

	resp = request(t, server, http.MethodPost, "/mcp/messages", map[string]any{}, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("messages without session status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func setupKazumiTestRepo(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rules/index.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "AGE", "version": "1.6", "author": "tester", "useNativePlayer": true, "lastUpdate": 1700000000000},
		})
	})
	mux.HandleFunc("/rules/AGE.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api": "9", "type": "anime", "name": "AGE", "version": "1.6",
			"baseURL": "https://example.com/", "useNativePlayer": true,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func setupKazumiHTTPTest(t *testing.T) string {
	t.Helper()
	dir := setupHTTPTest(t)
	oldHome, oldConfigPath := config.UANIHome, config.ConfigFilePath
	config.UANIHome = filepath.Join(dir, "uani")
	config.ConfigFilePath = filepath.Join(dir, "config.toml")
	t.Cleanup(func() {
		config.UANIHome = oldHome
		config.ConfigFilePath = oldConfigPath
	})
	return dir
}

func TestAdminKazumiRepositoryAndSettings(t *testing.T) {
	setupKazumiHTTPTest(t)
	repoServer := setupKazumiTestRepo(t)
	config.Cfg.Kazumi = config.KazumiConfig{
		RuleRepository:      repoServer.URL + "/rules",
		BangumiAcceleration: "direct",
		RulesMirror:         "direct",
		ImageAcceleration:   "direct",
	}
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/kazumi/catalog", nil, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated catalog status = %d", resp.StatusCode)
	}
	resp = request(t, server, http.MethodGet, "/api/admin/kazumi/catalog", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("catalog status = %d", resp.StatusCode)
	}
	catalog := decodeJSON(t, resp)
	if len(catalog["rules"].([]any)) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}

	resp = request(t, server, http.MethodPost, "/api/admin/kazumi/rules", map[string]any{
		"names": []string{"AGE"},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("install status = %d", resp.StatusCode)
	}
	install := decodeJSON(t, resp)
	if len(install["rules"].([]any)) != 1 {
		t.Fatalf("install = %#v", install)
	}

	resp = request(t, server, http.MethodGet, "/api/admin/kazumi/rules", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	listed := decodeJSON(t, resp)
	if len(listed["rules"].([]any)) != 1 {
		t.Fatalf("list = %#v", listed)
	}

	resp = request(t, server, http.MethodPatch, "/api/admin/kazumi/settings", map[string]any{
		"image_acceleration":   "mirror",
		"bangumi_acceleration": "ech",
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings patch status = %d", resp.StatusCode)
	}
	settings := decodeJSON(t, resp)
	if settings["image_acceleration"] != "mirror" || settings["bangumi_acceleration"] != "ech" {
		t.Fatalf("settings = %#v", settings)
	}
	if config.Cfg.Kazumi.ImageAcceleration != "mirror" || config.Cfg.Kazumi.BangumiAcceleration != "ech" {
		t.Fatalf("config not saved: %#v", config.Cfg.Kazumi)
	}

	resp = request(t, server, http.MethodPatch, "/api/admin/kazumi/settings", map[string]any{
		"rules_mirror": "nope",
	}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid settings status = %d", resp.StatusCode)
	}

	resp = request(t, server, http.MethodDelete, "/api/admin/kazumi/rules/AGE", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status = %d", resp.StatusCode)
	}
}

func TestMCPKazumiNetworkTools(t *testing.T) {
	setupKazumiHTTPTest(t)
	config.Cfg.Kazumi = config.KazumiConfig{
		BangumiAcceleration: "mirror",
		RulesMirror:         "ghProxy",
		ImageAcceleration:   "ech",
	}
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "kazumi_get_network",
			"arguments": map[string]any{},
		},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP get network status = %d", resp.StatusCode)
	}
	call := decodeJSON(t, resp)
	result := call["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("MCP get network = %#v", call)
	}
	content := result["content"].([]any)[0].(map[string]any)
	var settings map[string]any
	if err := json.Unmarshal([]byte(content["text"].(string)), &settings); err != nil {
		t.Fatal(err)
	}
	if settings["rules_mirror"] != "ghProxy" {
		t.Fatalf("settings = %#v", settings)
	}

	resp = request(t, server, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "kazumi_set_network",
			"arguments": map[string]any{
				"rules_mirror":       "ghfast",
				"image_acceleration": "direct",
			},
		},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP set network status = %d", resp.StatusCode)
	}
	if config.Cfg.Kazumi.RulesMirror != "ghfast" || config.Cfg.Kazumi.ImageAcceleration != "direct" {
		t.Fatalf("MCP settings not saved: %#v", config.Cfg.Kazumi)
	}
}

func TestAdminBangumiSyncEndpoints(t *testing.T) {
	dir := setupHTTPTest(t)
	seedBangumi(t, "", 0)

	oldConfigPath := config.ConfigFilePath
	config.ConfigFilePath = filepath.Join(dir, "config.toml")
	defer func() { config.ConfigFilePath = oldConfigPath }()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": "tester"})
		case "/v0/users/tester/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"data": []map[string]any{{
					"subject_id": 777,
					"type":       3,
					"ep_status":  0,
					"subject": map[string]any{
						"id":      777,
						"name":    "Sync Test",
						"name_cn": "同步测试",
					},
				}},
			})
		case "/calendar":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodPost, "/api/admin/bangumi/token", map[string]any{"access_token": "secret"}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d", resp.StatusCode)
	}
	tokenBody := decodeJSON(t, resp)
	if tokenBody["username"] != "tester" {
		t.Fatalf("unexpected token response: %v", tokenBody)
	}

	resp = request(t, server, http.MethodPatch, "/api/admin/bangumi/sync", map[string]any{"sync_enabled": true, "sync_types": []string{"watching"}}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	patchBody := decodeJSON(t, resp)
	if patchBody["sync_enabled"] != true || patchBody["token_configured"] != true {
		t.Fatalf("unexpected patch response: %v", patchBody)
	}

	resp = request(t, server, http.MethodPost, "/api/admin/bangumi/sync", map[string]any{"dry_run": true}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d", resp.StatusCode)
	}
	syncBody := decodeJSON(t, resp)
	summary, _ := syncBody["summary"].(map[string]any)
	if summary["fetched"] != float64(1) || summary["dry_run"] != true {
		t.Fatalf("unexpected sync response: %v", syncBody)
	}

	resp = request(t, server, http.MethodDelete, "/api/admin/bangumi/token", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	logoutBody := decodeJSON(t, resp)
	if logoutBody["token_configured"] != false {
		t.Fatalf("unexpected logout response: %v", logoutBody)
	}
}

func TestKazumiTimelineRecommendAndSubject(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumi(model.WebsiteBangumi{ID: "bgmtv:101", Name: "测试番剧", UpdateDay: "Mon"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveFollowed(&db.Followed{BangumiName: "测试番剧", Episodes: map[int]bool{1: true}, Status: db.FollowedStatusFollowed}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveManualBangumi(&db.ManualBangumi{BangumiName: "测试番剧", Provider: "bgmtv", ProviderID: "101"}); err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/calendar":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"1": []map[string]any{{
					"watchers": 42,
					"subject": map[string]any{
						"id": 101, "name": "Test", "nameCN": "测试番剧",
						"images":   map[string]string{"large": "https://example.com/1.jpg"},
						"rating":   map[string]any{"score": 7.5, "rank": 100, "total": 50},
						"metaTags": []string{"日本", "TV"},
					},
				}},
			})
		case "/recommend":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": 102, "name": "Rec", "nameCN": "推荐番剧", "rating": map[string]any{"score": 8.1}}},
			})
		case "/v0/subjects/101":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 101, "name": "Test", "name_cn": "测试番剧",
				"summary": "简介", "rating": map[string]any{"score": 7.5, "rank": 100, "total": 50},
				"tags": []map[string]any{{"name": "TV", "count": 10}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	timelineMu.Lock()
	timelineCache = nil
	timelineMu.Unlock()
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/kazumi/timeline", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d", resp.StatusCode)
	}
	timeline := decodeJSON(t, resp)
	days, _ := timeline["days"].([]any)
	if len(days) != 7 {
		t.Fatalf("timeline days = %d", len(days))
	}
	first := days[0].(map[string]any)
	items := first["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name_cn"] != "测试番剧" {
		t.Fatalf("unexpected timeline item: %v", items)
	}

	resp = request(t, server, http.MethodGet, "/api/kazumi/recommend?tag=日本", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recommend status = %d", resp.StatusCode)
	}
	recommend := decodeJSON(t, resp)
	subjects := recommend["subjects"].([]any)
	if len(subjects) != 1 || subjects[0].(map[string]any)["name_cn"] != "推荐番剧" {
		t.Fatalf("unexpected recommendations: %v", subjects)
	}

	resp = request(t, server, http.MethodGet, "/api/kazumi/subject/101", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subject status = %d", resp.StatusCode)
	}
	subject := decodeJSON(t, resp)["subject"].(map[string]any)
	if subject["summary"] != "简介" || subject["score"] != 7.5 {
		t.Fatalf("unexpected subject detail: %v", subject)
	}
	subscription, _ := subject["subscription"].(map[string]any)
	if subscription["bangumi_name"] != "测试番剧" || subscription["episode"] != float64(1) {
		t.Fatalf("subject subscription = %v", subject["subscription"])
	}
}

func TestImageProxyRejectsNonBangumiURLs(t *testing.T) {
	setupHTTPTest(t)
	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodGet, "/api/image?url=https%3A%2F%2Fexample.com%2Fa.jpg", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("image proxy status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestKazumiSubjectDetailSectionsAndCache(t *testing.T) {
	setupHTTPTest(t)
	var characterHits int
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/subjects/101":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 101, "name": "Test", "name_cn": "测试", "summary": "简介",
				"info":   "{{Infobox animanga/TVAnime\n|话数= 12\n|放送星期= 星期六\n|导演= 测试导演\n}}",
				"rating": map[string]any{"score": 7.5, "rank": 1, "total": 10},
			})
		case "/v0/subjects/101/characters":
			characterHits++
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 1, "name": "角色A", "relation": "主角",
				"images": map[string]string{"large": "https://lain.bgm.tv/c.jpg"},
				"actors": []map[string]any{{"id": 2, "name": "声优A", "images": map[string]string{"large": "https://lain.bgm.tv/a.jpg"}}},
			}})
		case "/v0/subjects/101/persons":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 3, "name": "制作人", "relation": "导演", "career": []string{"producer"}}})
		case "/v0/subjects/101/subjects":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 102, "name": "Related", "name_cn": "关联", "relation": "续集", "images": map[string]string{"large": "https://lain.bgm.tv/r.jpg"}}})
		case "/comments/101":
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 1, "list": []map[string]any{{
				"user": map[string]any{"id": 4, "username": "u", "nickname": "用户", "avatar": map[string]string{"large": "https://lain.bgm.tv/u.jpg"}},
				"rate": 8, "comment": "好看", "updatedAt": 1700000000,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/101", nil, "")
	subject := decodeJSON(t, resp)["subject"].(map[string]any)
	fields, _ := subject["info_fields"].([]any)
	if len(fields) != 3 {
		t.Fatalf("info_fields = %v", subject["info_fields"])
	}

	for path, key := range map[string]string{
		"/api/kazumi/subject/101/characters": "items",
		"/api/kazumi/subject/101/staff":      "items",
		"/api/kazumi/subject/101/relations":  "items",
		"/api/kazumi/subject/101/comments":   "items",
	} {
		resp = request(t, server, http.MethodGet, path, nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
		body := decodeJSON(t, resp)
		items, _ := body[key].([]any)
		if len(items) != 1 {
			t.Fatalf("%s items = %v", path, body[key])
		}
	}

	// Characters are cached for 24h; a second request must not hit the API.
	resp = request(t, server, http.MethodGet, "/api/kazumi/subject/101/characters", nil, "")
	decodeJSON(t, resp)
	if characterHits != 1 {
		t.Fatalf("character endpoint hit %d times, want 1", characterHits)
	}

	resp = request(t, server, http.MethodGet, "/api/kazumi/lookup?subject_id=101", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lookup status = %d", resp.StatusCode)
	}
	lookup := decodeJSON(t, resp)
	if fmt.Sprint(lookup["subject_id"]) != "101" {
		t.Fatalf("lookup subject_id = %v", lookup["subject_id"])
	}
}

func TestBangumiCollectionSwitch(t *testing.T) {
	setupHTTPTest(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v0/users/-/collections/101" {
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	oldBangumi := config.Cfg.Bangumi
	bgmtv.APIBase = apiServer.URL
	config.Cfg.Bangumi = config.BangumiConfig{AccessToken: "token", Username: "tester"}
	defer func() {
		bgmtv.APIBase = oldBase
		config.Cfg.Bangumi = oldBangumi
	}()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodPost, "/api/admin/bangumi/collection", map[string]any{"subject_id": 101, "type": 4}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set collection status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodGet, "/api/admin/bangumi/collection/101", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get collection status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["type"] != float64(4) || body["cached"] != true {
		t.Fatalf("collection response = %v", body)
	}
}

func TestMCPSettingsToggle(t *testing.T) {
	dir := setupHTTPTest(t)
	oldConfigPath := config.ConfigFilePath
	config.ConfigFilePath = filepath.Join(dir, "config.toml")
	t.Cleanup(func() { config.ConfigFilePath = oldConfigPath })

	server := httptest.NewServer(HandlerWithOptions(Options{Web: true, MCP: true, MCPToggle: true}))
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/http/settings", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("http settings status = %d", resp.StatusCode)
	}
	settings := decodeJSON(t, resp)
	if settings["mcp_enabled"] != true || settings["mcp_toggle"] != true {
		t.Fatalf("unexpected initial MCP settings: %v", settings)
	}

	resp = request(t, server, http.MethodPatch, "/api/admin/http/settings", map[string]any{"mcp_enabled": false}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable MCP status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodGet, "/mcp", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled /mcp status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodPatch, "/api/admin/http/settings", map[string]any{"mcp_enabled": true}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable MCP status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, server, http.MethodGet, "/mcp", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("enabled /mcp status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandlerOptionsDisableWebAndMCP(t *testing.T) {
	apiOnly := httptest.NewServer(HandlerWithOptions(Options{Web: false, MCP: false}))
	defer apiOnly.Close()
	for _, path := range []string{"/", "/mcp"} {
		resp, err := apiOnly.Client().Get(apiOnly.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}

	noMCP := httptest.NewServer(HandlerWithOptions(Options{Web: true, MCP: false}))
	defer noMCP.Close()
	resp, err := noMCP.Client().Get(noMCP.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/mcp status = %d, want 404", resp.StatusCode)
	}
	resp, err = noMCP.Client().Get(noMCP.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ status = %d, want 200", resp.StatusCode)
	}
}

func TestKazumiRecommendCategoriesFromDiskCache(t *testing.T) {
	setupHTTPTest(t)
	recommendMu.Lock()
	recommendCache = map[string]recommendEntry{}
	recommendMu.Unlock()
	key := fmt.Sprintf("%s|%s|%d|%d", "", "日常", 24, 0)
	items := []map[string]any{{"id": 201, "name": "Daily", "name_cn": "日常番"}}
	saveRecommendDiskCache(key, items)

	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodGet, "/api/kazumi/recommend/all", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	categories, _ := body["categories"].(map[string]any)
	list, _ := categories["日常"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name_cn"] != "日常番" {
		t.Fatalf("categories = %#v", categories)
	}
}

func TestNormalizeCommentsPayloadLegacyShapes(t *testing.T) {
	payloads := []any{
		[]any{map[string]any{"content": "好看", "createdAt": float64(1700000000)}},
		[]map[string]any{{"content": "好看", "updated": float64(1700000000)}},
		map[string]any{"list": []any{map[string]any{"content": "好看", "created_at": float64(1700000000)}}},
	}
	for _, payload := range payloads {
		normalized, ok := normalizeCommentsPayload(payload).(map[string]any)
		if !ok {
			t.Fatalf("normalized = %#v", normalized)
		}
		items, _ := normalized["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items = %#v", normalized["items"])
		}
		item := items[0].(map[string]any)
		if item["comment"] != "好看" {
			t.Fatalf("comment = %#v", item)
		}
		if number, _ := item["updated_at"].(float64); number != 1700000000 {
			t.Fatalf("updated_at = %#v", item["updated_at"])
		}
	}
}

func TestKazumiCommentsRefetchesEmptyCache(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Comments:  `{"items":[],"total":0}`,
	}); err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comments/101" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 1, "list": []map[string]any{{
			"user": map[string]any{"id": 4, "username": "u", "nickname": "用户", "avatar": map[string]string{"large": "https://lain.bgm.tv/u.jpg"}},
			"rate": 8, "comment": "好看", "updatedAt": 1700000000,
		}}})
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()
	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/101/comments", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	item := items[0].(map[string]any)
	if item["comment"] != "好看" {
		t.Fatalf("comment = %#v", item)
	}
}

func TestCardCoverPrefersSmallerThumbnail(t *testing.T) {
	images := bgmtv.Images{Large: "large", Common: "common", Medium: "medium", Small: "small"}
	if got := cardCover(images); got != "common" {
		t.Fatalf("cardCover = %q, want common", got)
	}
	if got := cardCover(bgmtv.Images{Large: "large"}); got != "large" {
		t.Fatalf("cardCover fallback = %q, want large", got)
	}
}

func TestCacheExportImportEndpoints(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Overview:  `{"name":"导出测试"}`,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/cache/export", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", resp.StatusCode)
	}
	exported := decodeJSON(t, resp)
	subjects, _ := exported["subjects"].([]any)
	if len(subjects) != 1 {
		t.Fatalf("subjects = %#v", exported["subjects"])
	}

	resp = request(t, server, http.MethodPost, "/api/admin/cache/import", map[string]any{
		"version": 1,
		"subjects": []any{map[string]any{
			"subject_id": 202,
			"overview":   `{"name":"导入测试"}`,
		}},
	}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d", resp.StatusCode)
	}
	imported := decodeJSON(t, resp)
	result, _ := imported["result"].(map[string]any)
	if fmt.Sprint(result["imported"]) != "1" {
		t.Fatalf("import result = %#v", imported)
	}
	cache, err := db.Conn.GetBangumiCache(202)
	if err != nil || cache.Overview == "" {
		t.Fatalf("cache = %#v, err = %v", cache, err)
	}

	// The Kazumi-prefixed alias must route to the same handlers.
	resp = request(t, server, http.MethodGet, "/api/admin/kazumi/cache/export", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("kazumi export status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestKazumiCommentsUnavailableWithoutCache(t *testing.T) {
	setupHTTPTest(t)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/999/comments", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["unavailable"] != true {
		t.Fatalf("body = %#v", body)
	}
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("items = %#v", body["items"])
	}
	if message, _ := body["message"].(string); message == "" {
		t.Fatalf("message = %#v", body["message"])
	}
}

func TestKazumiCommentsFallsBackToCacheOnFailure(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Comments:  `{"items":[{"comment":"缓存吐槽"}],"total":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/101/comments?refresh=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["comment"] != "缓存吐槽" {
		t.Fatalf("items = %#v", body["items"])
	}
}

func TestKazumiCommentsForcedRefreshBypassesEmptyLocalCache(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{SubjectID: 303, Comments: `{"items":[],"total":0}`}); err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"list":  []map[string]any{{"comment": "重新加载的吐槽"}},
			"total": 1,
		})
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	// The retry button sends local=1&refresh=1; refresh must bypass the empty
	// local cache instead of returning an empty page again.
	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/303/comments?local=1&refresh=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["comment"] != "重新加载的吐槽" {
		t.Fatalf("items = %#v", body["items"])
	}
}

func TestKazumiCommentsForcedRefreshDoesNotFallBackToEmptyCache(t *testing.T) {
	setupHTTPTest(t)
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{SubjectID: 304, Comments: `{"items":[],"total":0}`}); err != nil {
		t.Fatal(err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer apiServer.Close()
	oldBase := bgmtv.APIBase
	bgmtv.APIBase = apiServer.URL
	defer func() { bgmtv.APIBase = oldBase }()

	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/kazumi/subject/304/comments?refresh=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["unavailable"] != true {
		t.Fatalf("body = %#v", body)
	}
}

func TestDownloadAndBrowseExportImportEndpoints(t *testing.T) {
	setupHTTPTest(t)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/kazumi/downloads/export", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download export status = %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "zip") {
		t.Fatalf("download export content type = %q", contentType)
	}
	exported, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported))); err != nil {
		t.Fatalf("download export is not a zip: %v", err)
	}

	downloadZip := makeTestZip(t, map[string]string{
		"manifest.json":    `{"version":1,"tasks":[{"name":"测试番剧","episode":1,"path":"测试番剧/1.mp4","status":2}]}`,
		"media/测试番剧/1.mp4": "video-data",
	})
	resp = requestRaw(t, server, http.MethodPost, "/api/admin/kazumi/downloads/import", downloadZip, "application/zip", testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download import status = %d", resp.StatusCode)
	}
	imported := decodeJSON(t, resp)
	result, _ := imported["result"].(map[string]any)
	if fmt.Sprint(result["imported"]) != "1" || fmt.Sprint(result["media"]) != "1" {
		t.Fatalf("download import result = %#v", imported)
	}
	media, err := os.ReadFile(filepath.Join(config.Cfg.SavePath, "kazumi", "测试番剧", "1.mp4"))
	if err != nil || string(media) != "video-data" {
		t.Fatalf("imported media = %q, err = %v", media, err)
	}

	resp = request(t, server, http.MethodGet, "/api/admin/kazumi/browse/export", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browse export status = %d", resp.StatusCode)
	}
	browseZip, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(browseZip), int64(len(browseZip))); err != nil {
		t.Fatalf("browse export is not a zip: %v", err)
	}

	importZip := makeTestZip(t, map[string]string{
		"manifest.json": `{"version":1,"timeline":[],"recommend":{"recommend_test.json":[]}}`,
	})
	resp = requestRaw(t, server, http.MethodPost, "/api/admin/kazumi/browse/import", importZip, "application/zip", testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("browse import status = %d", resp.StatusCode)
	}
	browse := decodeJSON(t, resp)
	browseResult, _ := browse["result"].(map[string]any)
	if browseResult["timeline"] != true || fmt.Sprint(browseResult["recommend"]) != "1" {
		t.Fatalf("browse import result = %#v", browse)
	}
}

func TestFollowBundleExportImportEndpoints(t *testing.T) {
	setupHTTPTest(t)
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp := request(t, server, http.MethodGet, "/api/admin/bangumi/export/bundle", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("follow bundle export status = %d", resp.StatusCode)
	}
	exported, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported))); err != nil {
		t.Fatalf("follow bundle export is not a zip: %v", err)
	}

	bundle := makeTestZip(t, map[string]string{
		"manifest.json": `{"version":1,"items":[]}`,
	})
	resp = requestRaw(t, server, http.MethodPost, "/api/admin/bangumi/import/bundle", bundle, "application/zip", testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("follow bundle import status = %d", resp.StatusCode)
	}
	imported := decodeJSON(t, resp)
	result, _ := imported["result"].(map[string]any)
	if fmt.Sprint(result["imported"]) != "0" {
		t.Fatalf("follow bundle import result = %#v", imported)
	}
}
