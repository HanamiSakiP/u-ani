package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
)

func TestKazumiFilesServesEncodedChinesePath(t *testing.T) {
	setupHTTPTest(t)
	path := filepath.Join(controllers.KazumiFilesDir(), "测试番剧", "1.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(Handler())
	defer server.Close()

	mediaURL := server.URL + "/kazumi-files/%E6%B5%8B%E8%AF%95%E7%95%AA%E5%89%A7/1.mp4"
	resp, err := http.Get(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "0123456789" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("Content-Type = %q", got)
	}

	req, err := http.NewRequest(http.MethodGet, mediaURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-4")
	rangeResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, _ := io.ReadAll(rangeResp.Body)
	rangeResp.Body.Close()
	if rangeResp.StatusCode != http.StatusPartialContent || string(rangeBody) != "234" {
		t.Fatalf("range status=%d body=%q", rangeResp.StatusCode, rangeBody)
	}
}
