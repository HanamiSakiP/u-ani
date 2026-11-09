package webui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedAssets(t *testing.T) {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "assets/app.css", "assets/app.js"} {
		if info, err := fs.Stat(sub, name); err != nil || info.IsDir() {
			t.Fatalf("embedded asset %s missing: %v", name, err)
		}
	}
}

func TestHandlerServesAssetsAndSPAFallback(t *testing.T) {
	server := httptest.NewServer(Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), "--accent") {
		t.Fatal("css content was not served")
	}

	resp, err = http.Get(server.URL + "/kazumi/deep/link")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<div class=\"app-shell\">") {
		t.Fatalf("SPA fallback failed: %d", resp.StatusCode)
	}
}
