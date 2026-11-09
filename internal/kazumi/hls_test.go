package kazumi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDownloadHLSSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/master.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=900\nmedia.m3u8\n")
		case "/media.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nseg0.ts\n#EXTINF:1,\nseg1.ts\n#EXT-X-ENDLIST\n")
		case "/seg0.ts":
			fmt.Fprint(w, "AAA")
		case "/seg1.ts":
			fmt.Fprint(w, "BBB")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := DownloadHLS(context.Background(), server.URL+"/master.m3u8", t.TempDir()+"/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "AAABBB" || !strings.HasSuffix(out, ".ts") {
		t.Fatalf("unexpected HLS output %s: %q", out, data)
	}
}

func TestDownloadHLSAES128(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	plain := []byte("encrypted segment data")
	block, _ := aes.NewCipher(key)
	padded := append([]byte{}, plain...)
	pad := aes.BlockSize - len(padded)%aes.BlockSize
	padded = append(padded, []byte(strings.Repeat(string(rune(pad)), pad))...)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/media.m3u8":
			fmt.Fprintf(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"/key.bin\",IV=0x%s\n#EXTINF:1,\nseg.ts\n#EXT-X-ENDLIST\n", hex.EncodeToString(iv))
		case "/key.bin":
			_, _ = w.Write(key)
		case "/seg.ts":
			_, _ = w.Write(encrypted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := DownloadHLS(context.Background(), server.URL+"/media.m3u8", t.TempDir()+"/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(plain) {
		t.Fatalf("decrypted data = %q", data)
	}
}
