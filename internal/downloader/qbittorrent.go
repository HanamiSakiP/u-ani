package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

type QBittorrent struct {
	baseURL  string
	username string
	password string
	mu       sync.Mutex
	loggedIn bool
}

func newQBittorrent() *QBittorrent {
	cfg := config.Cfg.QBittorrent
	return &QBittorrent{
		baseURL:  normalizeBaseURL(cfg.RPCHost, cfg.RPCPort, "http"),
		username: cfg.RPCUsername,
		password: cfg.RPCPassword,
	}
}

func (q *QBittorrent) CheckConfig() {}

func (q *QBittorrent) login() error {
	if q.loggedIn {
		return nil
	}
	form := url.Values{
		"username": {q.username},
		"password": {q.password},
	}
	req, err := http.NewRequest("POST", q.baseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", q.baseURL)
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("qbittorrent login failed: HTTP %d", resp.StatusCode)
	}
	text := strings.TrimSpace(string(body))
	if text != "Ok." && text != "" {
		return fmt.Errorf("qbittorrent login failed: %s", text)
	}
	q.loggedIn = true
	return nil
}

func (q *QBittorrent) request(method, path string, params url.Values, form url.Values, out any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.login(); err != nil {
		return err
	}
	do := func() (*http.Response, error) {
		u := q.baseURL + path
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, u, body)
		if err != nil {
			return nil, err
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Referer", q.baseURL)
		return httpx.Client().Do(req)
	}
	resp, err := do()
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		q.loggedIn = false
		if err := q.login(); err != nil {
			return err
		}
		resp, err = do()
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("qbittorrent HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type qbTorrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	SavePath string  `json:"save_path"`
	Progress float64 `json:"progress"`
	AddedOn  int64   `json:"added_on"`
}

func (q *QBittorrent) AddDownload(rawURL, savePath string) (string, error) {
	cfg := config.Cfg.QBittorrent
	form := url.Values{
		"urls":     {rawURL},
		"savepath": {savePath},
		"paused":   {"false"},
		"autoTMM":  {"false"},
	}
	if cfg.Category != "" {
		form.Set("category", cfg.Category)
	}
	if len(cfg.Tags) > 0 {
		form.Set("tags", strings.Join(cfg.Tags, ","))
	}
	if err := q.request("POST", "/api/v2/torrents/add", nil, form, nil); err != nil {
		return "", err
	}

	var infos []qbTorrent
	if err := q.request("GET", "/api/v2/torrents/info", url.Values{"sort": {"added_on"}}, nil, &infos); err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", nil
	}
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].AddedOn < infos[j].AddedOn })
	for _, t := range infos {
		if t.SavePath == savePath {
			return t.Hash, nil
		}
	}
	return infos[len(infos)-1].Hash, nil
}

func (q *QBittorrent) getTorrent(id string) (*qbTorrent, error) {
	var infos []qbTorrent
	if err := q.request("GET", "/api/v2/torrents/info", url.Values{"hashes": {id}}, nil, &infos); err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, nil
	}
	return &infos[0], nil
}

func (q *QBittorrent) GetStatus(id string) (Status, error) {
	t, err := q.getTorrent(id)
	if err != nil {
		return Error, err
	}
	if t == nil {
		return NotFound, nil
	}
	state := strings.ToLower(t.State)
	switch state {
	case "uploading", "stalledup", "pausedup", "queuedup", "checkingup", "forcedup", "stoppedup":
		return Done, nil
	case "downloading", "metadl", "stalleddl", "checkingdl", "forceddl", "allocating", "queueddl", "forcedmetadl", "queuedforchecking", "checkingresumedata", "moving":
		return Downloading, nil
	case "pauseddl", "stoppeddl":
		return NotDownloading, nil
	case "error", "missingfiles":
		return Error, nil
	default:
		return Error, nil
	}
}

func (q *QBittorrent) GetFiles(id string) ([]string, error) {
	t, err := q.getTorrent(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	var files []struct {
		Name string `json:"name"`
	}
	if err := q.request("GET", "/api/v2/torrents/files", url.Values{"hash": {id}}, nil, &files); err != nil {
		return nil, err
	}
	dir := strings.TrimRight(t.SavePath, "/")
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, dir+"/"+f.Name)
	}
	return out, nil
}

func (q *QBittorrent) RemoveDownload(id string) error {
	form := url.Values{"hashes": {id}, "deleteFiles": {"false"}}
	return q.request("POST", "/api/v2/torrents/delete", nil, form, nil)
}
