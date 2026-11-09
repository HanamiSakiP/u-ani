package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

type Transmission struct {
	url        string
	username   string
	password   string
	sessionID  string
	mu         sync.Mutex
	rpcVersion int
	versionOK  bool
	reqID      int
}

func newTransmission() *Transmission {
	cfg := config.Cfg.Transmission
	base := normalizeBaseURL(cfg.RPCHost, cfg.RPCPort, "http")
	if cfg.RPCPath == "" {
		cfg.RPCPath = "/transmission/rpc"
	}
	return &Transmission{
		url:      base + cfg.RPCPath,
		username: cfg.RPCUsername,
		password: cfg.RPCPassword,
	}
}

func (t *Transmission) CheckConfig() {}

type transmissionRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
	ID        int    `json:"id"`
}

func (t *Transmission) doRequest(method string, args any, out any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reqID++
	body, err := json.Marshal(transmissionRequest{Method: method, Arguments: args, ID: t.reqID})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest("POST", t.url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if t.sessionID != "" {
			req.Header.Set("X-Transmission-Session-Id", t.sessionID)
		}
		if t.username != "" {
			req.SetBasicAuth(t.username, t.password)
		}
		resp, err := httpx.Client().Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusConflict {
			if sid := resp.Header.Get("X-Transmission-Session-Id"); sid != "" {
				t.sessionID = sid
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 {
			return fmt.Errorf("transmission rpc HTTP %d", resp.StatusCode)
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return fmt.Errorf("transmission rpc session handshake failed")
}

func (t *Transmission) ensureVersion() {
	if t.versionOK {
		return
	}
	var result struct {
		Arguments struct {
			RPCVersion int `json:"rpc-version"`
		} `json:"arguments"`
	}
	if err := t.doRequest("session-get", nil, &result); err == nil {
		t.rpcVersion = result.Arguments.RPCVersion
		t.versionOK = true
	}
}

type transmissionTorrent struct {
	HashString  string  `json:"hashString"`
	Error       int     `json:"error"`
	Status      int     `json:"status"`
	Progress    float64 `json:"progress"`
	DownloadDir string  `json:"downloadDir"`
	Files       []struct {
		Name string `json:"name"`
	} `json:"files"`
}

type transmissionTorrentResult struct {
	Arguments struct {
		Torrents []transmissionTorrent `json:"torrents"`
	} `json:"arguments"`
	Result string `json:"result"`
}

type transmissionAddResult struct {
	Arguments struct {
		TorrentAdded struct {
			HashString string `json:"hashString"`
		} `json:"torrent-added"`
		TorrentDuplicate struct {
			HashString string `json:"hashString"`
		} `json:"torrent-duplicate"`
	} `json:"arguments"`
	Result string `json:"result"`
}

func (t *Transmission) AddDownload(rawURL, savePath string) (string, error) {
	t.ensureVersion()
	args := map[string]any{"download-dir": savePath, "paused": false}
	if t.rpcVersion >= 16 {
		args["labels"] = config.Cfg.Transmission.Labels
	}
	var result transmissionAddResult
	if err := t.doRequest("torrent-add", args, &result); err != nil {
		return "", err
	}
	if result.Arguments.TorrentAdded.HashString != "" {
		return result.Arguments.TorrentAdded.HashString, nil
	}
	if result.Arguments.TorrentDuplicate.HashString != "" {
		return result.Arguments.TorrentDuplicate.HashString, nil
	}
	return "", nil
}

func (t *Transmission) getTorrent(id string) (*transmissionTorrent, error) {
	args := map[string]any{
		"ids":    []string{id},
		"fields": []string{"hashString", "error", "status", "progress", "downloadDir", "files"},
	}
	var result transmissionTorrentResult
	if err := t.doRequest("torrent-get", args, &result); err != nil {
		return nil, err
	}
	if len(result.Arguments.Torrents) == 0 {
		return nil, nil
	}
	return &result.Arguments.Torrents[0], nil
}

func (t *Transmission) GetStatus(id string) (Status, error) {
	torrent, err := t.getTorrent(id)
	if err != nil {
		return Error, err
	}
	if torrent == nil || torrent.Error != 0 {
		return NotFound, nil
	}
	if torrent.Status == 0 {
		if torrent.Progress >= 1 {
			return Done, nil
		}
		return NotDownloading, nil
	}
	switch torrent.Status {
	case 1, 2, 3, 4:
		return Downloading, nil
	case 5, 6:
		return Done, nil
	default:
		return Error, nil
	}
}

func (t *Transmission) GetFiles(id string) ([]string, error) {
	torrent, err := t.getTorrent(id)
	if err != nil {
		return nil, err
	}
	if torrent == nil {
		return nil, nil
	}
	dir := strings.TrimRight(torrent.DownloadDir, "/")
	out := make([]string, 0, len(torrent.Files))
	for _, f := range torrent.Files {
		out = append(out, dir+"/"+f.Name)
	}
	return out, nil
}

func (t *Transmission) RemoveDownload(id string) error {
	return t.doRequest("torrent-remove", map[string]any{"ids": []string{id}}, nil)
}
