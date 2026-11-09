package httpx

import (
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

var (
	once   sync.Once
	client *http.Client
)

// NewTransport builds an HTTP transport honoring the configured proxy. The
// caller owns the returned transport and may adjust TLS settings for ECH.
func NewTransport() *http.Transport {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if config.Cfg.Proxy != "" {
		if u, err := url.Parse(config.Cfg.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return transport
}

// Client returns a shared HTTP client honoring the configured proxy.
func Client() *http.Client {
	once.Do(func() {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Timeout: 120 * time.Second, Transport: NewTransport(), Jar: jar}
	})
	return client
}
