package kazumi

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

const echConfigURL = "https://dns.alidns.com/resolve?name=crypto.cloudflare.com&type=HTTPS"

var echHosts = map[string]bool{
	"api.bgm.tv":   true,
	"next.bgm.tv":  true,
	"lain.bgm.tv":  true,
	"gh-proxy.org": true,
}

// The official Kazumi ECH transport pins Cloudflare edge addresses so a
// polluted DNS answer cannot prevent the encrypted handshake.
var echAddresses = []string{"172.67.134.140", "104.21.6.61", "172.67.73.67"}

var (
	echMu          sync.Mutex
	echClient      *http.Client
	echUnavailable bool
	echUnsupported sync.Map
)

// ShouldUseECH reports whether a host is covered by Kazumi's ECH transport.
func ShouldUseECH(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && echHosts[u.Host]
}

// Get performs a GET request with optional ECH. ECH is best-effort: if the
// target does not negotiate ECH, the request is retried with normal TLS.
func Get(ctx context.Context, rawURL string, headers map[string]string, useECH bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if useECH && ShouldUseECH(rawURL) {
		if client := sharedECHClient(); client != nil {
			host := request.URL.Host
			if _, unsupported := echUnsupported.Load(host); !unsupported {
				response, err := client.Do(request)
				if err == nil {
					return response, nil
				}
				// Many hosts do not yet publish an ECH config. Falling back to
				// normal TLS keeps ECH a preference instead of a hard failure.
				echUnsupported.Store(host, true)
			}
		}
	}
	retry, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		retry.Header.Set(key, value)
	}
	return httpx.Client().Do(retry)
}

func sharedECHClient() *http.Client {
	echMu.Lock()
	defer echMu.Unlock()
	if echClient != nil || echUnavailable {
		return echClient
	}
	config, err := fetchECHConfig()
	if err != nil || len(config) == 0 {
		echUnavailable = true
		return nil
	}
	transport := httpx.NewTransport()
	transport.DialContext = echDialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.TLSClientConfig = &tls.Config{
		MinVersion:                     tls.VersionTLS13,
		EncryptedClientHelloConfigList: config,
	}
	echClient = &http.Client{Timeout: 120 * time.Second, Transport: transport}
	return echClient
}

func echDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !echHosts[host] {
		return (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for _, ip := range echAddresses {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		dialer := &net.Dialer{Timeout: remaining, KeepAlive: 30 * time.Second}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no ECH address available for %s", host)
}

func fetchECHConfig() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, echConfigURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ECH DNS HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Answer []struct {
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	for _, answer := range payload.Answer {
		if value := echValue(answer.Data); value != "" {
			if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
				return decoded, nil
			}
			if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
				return decoded, nil
			}
		}
	}
	return nil, fmt.Errorf("ECH config not found in DNS HTTPS record")
}

func echValue(record string) string {
	const marker = `ech="`
	if index := strings.Index(record, marker); index >= 0 {
		start := index + len(marker)
		if end := strings.Index(record[start:], `"`); end >= 0 {
			return record[start : start+end]
		}
	}
	for _, field := range strings.Fields(record) {
		if strings.HasPrefix(field, "ech=") {
			return strings.Trim(strings.TrimPrefix(field, "ech="), `"`)
		}
	}
	return ""
}
