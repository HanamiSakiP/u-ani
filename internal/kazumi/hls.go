package kazumi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

// DownloadHLS downloads an HLS playlist (including AES-128 encrypted
// segments) to outputBase. It returns the final file path. The extension is
// .mp4 for fMP4 playlists and .ts otherwise.
func DownloadHLS(ctx context.Context, playlistURL, outputBase string, headers map[string]string) (string, error) {
	playlist, err := loadHLSPlaylist(ctx, playlistURL, headers)
	if err != nil {
		return "", err
	}
	ext := ".ts"
	if playlist.hasMap || playlist.fmp4 {
		ext = ".mp4"
	}
	output := outputBase + ext
	file, err := os.Create(output)
	if err != nil {
		return "", err
	}
	defer file.Close()

	if playlist.mapURL != "" {
		data, err := fetchHLSBytes(ctx, playlist.mapURL, headers)
		if err != nil {
			return "", err
		}
		if _, err := file.Write(data); err != nil {
			return "", err
		}
	}

	keyCache := map[string][]byte{}
	for index, segment := range playlist.segments {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := fetchHLSBytes(ctx, segment, headers)
		if err != nil {
			return "", fmt.Errorf("segment %d: %w", index+1, err)
		}
		if playlist.key != nil {
			key := keyCache[playlist.key.uri]
			if key == nil {
				key, err = fetchHLSBytes(ctx, playlist.key.uri, headers)
				if err != nil {
					return "", fmt.Errorf("fetch HLS key: %w", err)
				}
				keyCache[playlist.key.uri] = key
			}
			iv := playlist.key.iv
			if len(iv) == 0 {
				iv = make([]byte, 16)
				binary.BigEndian.PutUint64(iv[8:], uint64(index))
			}
			data, err = decryptHLS(data, key, iv)
			if err != nil {
				return "", fmt.Errorf("decrypt segment %d: %w", index+1, err)
			}
		}
		if _, err := file.Write(data); err != nil {
			return "", err
		}
	}
	return output, nil
}

type hlsKey struct {
	uri string
	iv  []byte
}

type hlsPlaylist struct {
	segments []string
	key      *hlsKey
	mapURL   string
	hasMap   bool
	fmp4     bool
}

func loadHLSPlaylist(ctx context.Context, rawURL string, headers map[string]string) (*hlsPlaylist, error) {
	data, err := fetchHLSBytes(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if strings.Contains(text, "#EXT-X-STREAM-INF") {
		variant, err := pickHLSVariant(rawURL, text)
		if err != nil {
			return nil, err
		}
		data, err = fetchHLSBytes(ctx, variant, headers)
		if err != nil {
			return nil, err
		}
	}
	return parseHLSMedia(rawURL, string(data))
}

func pickHLSVariant(baseURL, text string) (string, error) {
	lines := strings.Split(text, "\n")
	bestBandwidth := -1
	bestURL := ""
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		bandwidth := parseHLSBandwidth(line)
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || strings.HasPrefix(next, "#") {
				continue
			}
			if bandwidth > bestBandwidth {
				bestBandwidth = bandwidth
				bestURL = resolveHLSURL(baseURL, next)
			}
			break
		}
	}
	if bestURL == "" {
		return "", fmt.Errorf("HLS master playlist has no variants")
	}
	return bestURL, nil
}

func parseHLSBandwidth(line string) int {
	index := strings.Index(line, "BANDWIDTH=")
	if index < 0 {
		return 0
	}
	rest := line[index+len("BANDWIDTH="):]
	end := strings.IndexAny(rest, ",:")
	if end >= 0 {
		rest = rest[:end]
	}
	value, _ := strconv.Atoi(strings.TrimSpace(rest))
	return value
}

func parseHLSMedia(baseURL, text string) (*hlsPlaylist, error) {
	playlist := &hlsPlaylist{}
	sequence := 0
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:") {
			sequence, _ = strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-KEY:") {
			attrs := parseHLSAttrs(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			if strings.EqualFold(attrs["METHOD"], "AES-128") && attrs["URI"] != "" {
				iv := []byte(nil)
				if rawIV := strings.TrimPrefix(attrs["IV"], "0x"); rawIV != "" {
					if decoded, err := hex.DecodeString(rawIV); err == nil && len(decoded) == 16 {
						iv = decoded
					}
				}
				playlist.key = &hlsKey{uri: resolveHLSURL(baseURL, attrs["URI"]), iv: iv}
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			attrs := parseHLSAttrs(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			if attrs["URI"] != "" {
				playlist.hasMap = true
				playlist.mapURL = resolveHLSURL(baseURL, attrs["URI"])
				if !strings.Contains(strings.ToLower(attrs["URI"]), ".ts") {
					playlist.fmp4 = true
				}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		playlist.segments = append(playlist.segments, resolveHLSURL(baseURL, line))
	}
	if len(playlist.segments) == 0 {
		return nil, fmt.Errorf("HLS playlist has no segments")
	}
	_ = sequence
	return playlist, nil
}

func parseHLSAttrs(value string) map[string]string {
	out := map[string]string{}
	for _, part := range splitHLSAttrs(value) {
		index := strings.Index(part, "=")
		if index <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:index])
		item := strings.TrimSpace(part[index+1:])
		item = strings.Trim(item, `"`)
		out[key] = item
	}
	return out
}

func splitHLSAttrs(value string) []string {
	var out []string
	var current strings.Builder
	quoted := false
	for _, ch := range value {
		switch ch {
		case '"':
			quoted = !quoted
			current.WriteRune(ch)
		case ',':
			if quoted {
				current.WriteRune(ch)
			} else {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func fetchHLSBytes(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultRuleUserAgent)
	}
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func decryptHLS(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted segment is not block aligned")
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)
	if padding := int(out[len(out)-1]); padding > 0 && padding <= aes.BlockSize && padding <= len(out) {
		out = out[:len(out)-padding]
	}
	return out, nil
}

func resolveHLSURL(base, reference string) string {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return reference
	}
	if parsed.IsAbs() {
		return parsed.String()
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return reference
	}
	return baseURL.ResolveReference(parsed).String()
}

// HLSOutputPath returns the preferred output path for an HLS download.
func HLSOutputPath(dir string, episode int) string {
	return filepath.Join(dir, strconv.Itoa(episode))
}

// HLSContentType reports whether a response looks like an HLS playlist.
func HLSContentType(contentType string, body []byte) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "mpegurl") ||
		bytes.Contains(body[:min(len(body), 64)], []byte("#EXTM3U"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
