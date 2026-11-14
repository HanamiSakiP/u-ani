// Package webdav implements the small WebDAV subset needed for sync snapshots.
package webdav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

// Client is a minimal WebDAV client with basic authentication.
type Client struct {
	base     string
	username string
	password string
}

func New(url, username, password string) (*Client, error) {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" {
		return nil, fmt.Errorf("WebDAV URL is not configured")
	}
	return &Client{base: url, username: username, password: password}, nil
}

func Current() (*Client, error) {
	return New(config.Cfg.WebDAV.URL, config.Cfg.WebDAV.Username, config.Cfg.WebDAV.Password)
}

func (c *Client) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	req.Header.Set("User-Agent", "uani/1.0.1 (webdav sync)")
	return httpx.Client().Do(req)
}

// Get returns the file content and whether it exists.
func (c *Client) Get(path string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("WebDAV GET %s: HTTP %d", path, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return data, true, err
}

// Put writes a file, creating the sync directory when needed.
func (c *Client) Put(path string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.mkdir(ctx, "/kazumiSync"); err != nil {
		return err
	}
	resp, err := c.request(ctx, http.MethodPut, path, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("WebDAV PUT %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

func (c *Client) mkdir(ctx context.Context, path string) error {
	resp, err := c.request(ctx, "MKCOL", path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("WebDAV MKCOL %s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}
