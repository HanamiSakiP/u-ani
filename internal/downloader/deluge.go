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

type Deluge struct {
	url      string
	password string
	mu       sync.Mutex
	id       int
	loggedIn bool
}

func newDeluge() *Deluge {
	return &Deluge{url: config.Cfg.Deluge.RPCURL, password: config.Cfg.Deluge.RPCPassword}
}

func (d *Deluge) CheckConfig() {}

type delugeResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (d *Deluge) rawCall(method string, params []any) (json.RawMessage, error) {
	d.id++
	payload, err := json.Marshal(map[string]any{"method": method, "params": params, "id": d.id})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", d.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deluge HTTP %d", resp.StatusCode)
	}
	var parsed delugeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("deluge error, reason: %s", parsed.Error.Message)
	}
	return parsed.Result, nil
}

func (d *Deluge) login() error {
	if d.loggedIn {
		return nil
	}
	result, err := d.rawCall("auth.login", []any{d.password})
	if err != nil {
		return err
	}
	var ok bool
	_ = json.Unmarshal(result, &ok)
	if !ok {
		return fmt.Errorf("deluge authentication failed")
	}
	d.loggedIn = true
	return nil
}

func (d *Deluge) call(method string, params []any) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.login(); err != nil {
		return nil, err
	}
	return d.rawCall(method, params)
}

func (d *Deluge) AddDownload(rawURL, savePath string) (string, error) {
	options := map[string]any{
		"add_paused":        false,
		"move_completed":    false,
		"download_location": savePath,
	}
	method := "core.add_torrent_url"
	if strings.HasPrefix(rawURL, "magnet:") {
		method = "core.add_torrent_magnet"
	}
	result, err := d.call(method, []any{rawURL, options})
	if err != nil {
		return "", err
	}
	var id string
	_ = json.Unmarshal(result, &id)
	return id, nil
}

func (d *Deluge) GetStatus(id string) (Status, error) {
	result, err := d.call("web.get_torrent_status", []any{id, []string{"state", "progress"}})
	if err != nil {
		return Error, err
	}
	var status map[string]any
	_ = json.Unmarshal(result, &status)
	if status == nil {
		return NotFound, nil
	}
	state, _ := status["state"].(string)
	if state == "" {
		return NotFound, nil
	}
	if state == "Paused" {
		progress, _ := status["progress"].(float64)
		if progress == 100 {
			return Done, nil
		}
		return NotDownloading, nil
	}
	switch state {
	case "Error":
		return Error, nil
	case "Downloading", "Checking":
		return Downloading, nil
	case "Seeding":
		return Done, nil
	default:
		return Error, nil
	}
}

func (d *Deluge) GetFiles(id string) ([]string, error) {
	result, err := d.call("web.get_torrent_status", []any{id, []string{"save_path", "files"}})
	if err != nil {
		return nil, err
	}
	var status struct {
		SavePath string `json:"save_path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	_ = json.Unmarshal(result, &status)
	out := make([]string, 0, len(status.Files))
	for _, f := range status.Files {
		out = append(out, status.SavePath+"/"+f.Path)
	}
	return out, nil
}

func (d *Deluge) RemoveDownload(id string) error {
	_, err := d.call("core.remove_torrent", []any{id, false})
	return err
}
