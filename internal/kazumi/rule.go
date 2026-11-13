package kazumi

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

// Rule is an installed Kazumi rule. It supports the common XPath and API
// schemas used by the official KazumiRules repository.
type Rule struct {
	API              string            `json:"api"`
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	MultiSources     bool              `json:"muliSources"`
	UsePost          bool              `json:"usePost"`
	UseNativePlayer  bool              `json:"useNativePlayer"`
	AdBlocker        bool              `json:"adBlocker"`
	UserAgent        string            `json:"userAgent"`
	BaseURL          string            `json:"baseURL"`
	SearchURL        string            `json:"searchURL"`
	SearchList       string            `json:"searchList"`
	SearchName       string            `json:"searchName"`
	SearchResult     string            `json:"searchResult"`
	ChapterRoads     string            `json:"chapterRoads"`
	ChapterResult    string            `json:"chapterResult"`
	Referer          string            `json:"referer"`
	SearchMode       string            `json:"searchMode"`
	ChapterMode      string            `json:"chapterMode"`
	SearchAPIConfig  APISearchConfig   `json:"searchApiConfig"`
	ChapterAPIConfig APIChapterConfig  `json:"chapterApiConfig"`
	AntiCrawler      AntiCrawlerConfig `json:"antiCrawlerConfig"`
}

// SearchItem is one result returned by a rule search.
type SearchItem struct {
	Name string `json:"name"`
	Src  string `json:"src"`
}

// Road is one playback line containing episode URLs.
type Road struct {
	Name  string   `json:"name"`
	URLs  []string `json:"urls"`
	Names []string `json:"names"`
}

// APIRequestConfig is a templated HTTP request used by API rules.
type APIRequestConfig struct {
	Method   string         `json:"method"`
	URL      string         `json:"url"`
	Headers  map[string]any `json:"headers"`
	Query    map[string]any `json:"query"`
	BodyType string         `json:"bodyType"`
	Body     any            `json:"body"`
}

// APISearchConfig describes an API search rule.
type APISearchConfig struct {
	Request        APIRequestConfig `json:"request"`
	ListPath       string           `json:"listPath"`
	NamePath       string           `json:"namePath"`
	SourcePath     string           `json:"sourcePath"`
	SourceTemplate string           `json:"sourceTemplate"`
}

// APIEpisodePageConfig builds the final playback page URL.
type APIEpisodePageConfig struct {
	URL   string         `json:"url"`
	Query map[string]any `json:"query"`
}

// APIChapterConfig describes an API chapter rule.
type APIChapterConfig struct {
	Request          APIRequestConfig      `json:"request"`
	Format           string                `json:"format"`
	RoadsPath        string                `json:"roadsPath"`
	RoadNamePath     string                `json:"roadNamePath"`
	EpisodesPath     string                `json:"episodesPath"`
	EpisodeNamePath  string                `json:"episodeNamePath"`
	EpisodeURLPath   string                `json:"episodeUrlPath"`
	RoadNamesPath    string                `json:"roadNamesPath"`
	RoadEpisodesPath string                `json:"roadEpisodesPath"`
	RoadSeparator    string                `json:"roadSeparator"`
	EpisodeSeparator string                `json:"episodeSeparator"`
	FieldSeparator   string                `json:"fieldSeparator"`
	Variables        map[string]string     `json:"variables"`
	EpisodePage      *APIEpisodePageConfig `json:"episodePage"`
}

// AntiCrawlerConfig is the captcha challenge metadata from a rule.
type AntiCrawlerConfig struct {
	Enabled            bool   `json:"enabled"`
	CaptchaType        int    `json:"captchaType"`
	CaptchaImage       string `json:"captchaImage"`
	CaptchaInput       string `json:"captchaInput"`
	CaptchaButton      string `json:"captchaButton"`
	CaptchaDetectType  int    `json:"captchaDetectType"`
	CaptchaDetectValue string `json:"captchaDetectValue"`
	CaptchaScript      string `json:"captchaScript"`
}

// ErrCaptchaRequired means the source returned an anti-crawler challenge.
var ErrCaptchaRequired = fmt.Errorf("source requires a captcha challenge; complete it in a browser and try again")

const defaultRuleUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

var mediaURLPattern = regexp.MustCompile(`(?i)(https?:)?//[^\s"'<>\\]+?\.(?:m3u8|mp4|flv)(?:\?[^\s"'<>\\]*)?`)

// ParseRule decodes and normalizes a rule JSON document.
func ParseRule(raw []byte) (*Rule, error) {
	var rule Rule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return nil, err
	}
	if rule.Name == "" {
		return nil, fmt.Errorf("rule has no name")
	}
	rule.SearchMode = ruleMode(rule.SearchMode)
	rule.ChapterMode = ruleMode(rule.ChapterMode)
	return &rule, nil
}

func ruleMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "api") {
		return "api"
	}
	return "xpath"
}

// Search runs a rule search for keyword.
func Search(rule *Rule, keyword string) ([]SearchItem, error) {
	if rule == nil {
		return nil, fmt.Errorf("rule is nil")
	}
	if rule.SearchMode == "api" {
		return searchAPI(rule, keyword)
	}
	return searchXPath(rule, keyword)
}

// Chapters fetches the episode list from a show detail page.
func Chapters(rule *Rule, source string) ([]Road, error) {
	if rule == nil {
		return nil, fmt.Errorf("rule is nil")
	}
	var (
		roads []Road
		err   error
	)
	if rule.ChapterMode == "api" {
		roads, err = chaptersAPI(rule, source)
	} else {
		roads, err = chaptersXPath(rule, source)
	}
	if err != nil {
		return nil, err
	}
	return filterMinorRoads(roads), nil
}

func filterMinorRoads(roads []Road) []Road {
	if len(roads) <= 1 {
		return roads
	}
	// Dedupe identical routes while preserving order.
	unique := make([]Road, 0, len(roads))
	seen := map[string]bool{}
	for _, road := range roads {
		key := strings.Join(road.URLs, "\x00")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, road)
	}
	roads = unique
	if len(roads) <= 1 {
		return roads
	}
	maxCount := 0
	for _, road := range roads {
		if len(road.URLs) > maxCount {
			maxCount = len(road.URLs)
		}
	}
	if maxCount <= 1 {
		return roads
	}
	kept := make([]Road, 0, len(roads))
	for _, road := range roads {
		count := len(road.URLs)
		// Drop clearly minor/duplicate routes (e.g. trailers or broken split lines)
		// when a much fuller route exists.
		minor := false
		switch {
		case maxCount >= 6 && count*3 < maxCount:
			minor = true
		case maxCount >= 4 && count == 1:
			minor = true
		case maxCount >= 8 && count <= 2:
			minor = true
		case len(roads) == 2 && maxCount >= 4 && count <= maxCount/2:
			// Common case: two routes where one source is obviously incomplete.
			minor = true
		}
		if !minor {
			kept = append(kept, road)
		}
	}
	if len(kept) == 0 {
		return roads
	}
	return kept
}

// IsMediaURL reports whether a URL already points at a media file.
func IsMediaURL(raw string) bool {
	lower := strings.ToLower(raw)
	for _, ext := range []string{".m3u8", ".mp4", ".flv", ".mkv", ".webm"} {
		if strings.Contains(lower, ext) {
			return true
		}
	}
	return false
}

// ResolveMediaURL looks for a direct media URL in an episode page. It is a
// best-effort fallback; yt-dlp/ffmpeg can also consume the page URL directly.
func ResolveMediaURL(rule *Rule, pageURL string) (string, error) {
	input, media, err := ResolvePlaybackURL(rule, pageURL)
	if err != nil || !media {
		return "", err
	}
	return input, nil
}

// ResolvePlaybackURL returns the best URL to hand to a downloader. It prefers a
// direct media URL; when no media URL is exposed, it may return a resolved
// player/iframe URL instead (media=false). This covers common MacCMS players
// such as LMM without needing a headless browser.
func ResolvePlaybackURL(rule *Rule, pageURL string) (string, bool, error) {
	input, media, _, err := ResolvePlayback(rule, pageURL)
	return input, media, err
}

// ResolvePlayback returns the playback URL, whether it is a direct media URL,
// and an optional referer override ("-" means the downloader must not send a
// Referer header, which some CDNs require).
func ResolvePlayback(rule *Rule, pageURL string) (string, bool, string, error) {
	return resolvePlaybackURL(rule, pageURL, 0, map[string]bool{})
}

func resolvePlaybackURL(rule *Rule, pageURL string, depth int, visited map[string]bool) (string, bool, string, error) {
	if depth > 2 || visited[pageURL] {
		return "", false, "", nil
	}
	visited[pageURL] = true
	headers := ruleHeaders(rule)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	body, err := fetchRulePage(ctx, rule, http.MethodGet, pageURL, nil, headers)
	cancel()
	if err != nil {
		return "", false, "", err
	}
	text := decodeEscapedURLs(string(body))
	if match := mediaURLPattern.FindString(text); match != "" {
		return normalizeURL(pageURL, match), true, "", nil
	}
	if media, referer := resolveVXDevPage(rule, pageURL, body); media != "" {
		return media, true, referer, nil
	}

	playerURL := macPlayerURL(rule, pageURL, body)
	if playerURL != "" && IsMediaURL(playerURL) {
		return playerURL, true, "", nil
	}

	root, _ := htmlquery.Parse(bytes.NewReader(body))
	var candidates []string
	if root != nil {
		for _, node := range queryAll(root, "//iframe|//script") {
			value := htmlquery.SelectAttr(node, "src")
			if value == "" {
				value = htmlquery.SelectAttr(node, "data-src")
			}
			if value != "" {
				candidates = append(candidates, normalizeURL(pageURL, value))
			}
		}
	}
	if playerURL != "" {
		candidates = append([]string{playerURL}, candidates...)
	}
	bestPlayer := playerURL
	for _, candidate := range candidates {
		if visited[candidate] {
			continue
		}
		childCtx, childCancel := context.WithTimeout(context.Background(), 20*time.Second)
		child, err := fetchRulePage(childCtx, rule, http.MethodGet, candidate, nil, headersWithReferer(rule, pageURL))
		childCancel()
		if err != nil {
			continue
		}
		if match := mediaURLPattern.FindString(decodeEscapedURLs(string(child))); match != "" {
			return normalizeURL(candidate, match), true, "", nil
		}
		if media, referer := resolveVXDevPage(rule, candidate, child); media != "" {
			return media, true, referer, nil
		}
		if depth < 2 && !strings.HasSuffix(strings.ToLower(uriPath(candidate)), ".js") {
			if nested, media, referer, _ := resolvePlaybackURL(rule, candidate, depth+1, visited); nested != "" {
				if media {
					return nested, true, referer, nil
				}
				if bestPlayer == "" {
					bestPlayer = nested
				}
			}
		}
	}
	if bestPlayer != "" {
		return bestPlayer, false, "", nil
	}
	return "", false, "", nil
}

func uriPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return parsed.Path
}

var (
	vxDevKey = []byte{0x26, 0x5f, 0x4d, 0x50, 0x26, 0x47, 0x7a, 0x46, 0x53, 0x75, 0x38, 0x4d, 0x5f, 0x6a, 0x6b, 0x70}
	vxDevIV  = []byte{0x34, 0x77, 0x63, 0x79, 0x49, 0x67, 0x68, 0x2b, 0x23, 0x67, 0x4b, 0x44, 0x76, 0x28, 0x52, 0x43}
)

// resolveVXDevPage handles the "vxdev" MacCMS player used by LMM and several
// mirror sites. Its player page posts a signed token to vxdev.php; the token
// is AES-128-CBC encrypted with the same key/IV as the official player script.
func resolveVXDevPage(rule *Rule, pageURL string, body []byte) (string, string) {
	text := string(body)
	if !strings.Contains(text, "vxdev.php") {
		return "", ""
	}
	vid := jsStringVar(text, "vid")
	timestamp := jsStringVar(text, "t")
	token := jsStringVar(text, "token")
	act := jsStringVar(text, "act")
	play := jsStringVar(text, "play")
	if vid == "" || timestamp == "" || token == "" {
		return "", ""
	}
	signature, err := decryptVXDevToken(token)
	if err != nil {
		return "", ""
	}
	form := url.Values{
		"vid":   {vid},
		"t":     {timestamp},
		"token": {signature},
		"act":   {act},
		"play":  {play},
	}
	apiURL := normalizeURL(pageURL, "vxdev.php")
	headers := ruleHeaders(rule)
	headers["Referer"] = pageURL
	headers["X-Requested-With"] = "XMLHttpRequest"
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := fetchRulePage(ctx, rule, http.MethodPost, apiURL, []byte(form.Encode()), headers)
	if err != nil {
		return "", ""
	}
	var payload struct {
		Code    int    `json:"code"`
		URL     string `json:"url"`
		Ext     string `json:"ext"`
		Referer string `json:"referer"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	if payload.URL == "" {
		return "", ""
	}
	ref := payload.Referer
	if strings.EqualFold(ref, "never") {
		ref = "-"
	}
	return normalizeURL(apiURL, payload.URL), ref
}

func jsStringVar(text, name string) string {
	pattern := regexp.MustCompile(`var\s+` + regexp.QuoteMeta(name) + `\s*=\s*["']([^"']+)["']`)
	if match := pattern.FindStringSubmatch(text); len(match) == 2 {
		return match[1]
	}
	return ""
}

func decryptVXDevToken(value string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
		if err != nil {
			return "", err
		}
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid vxdev token length")
	}
	block, err := aes.NewCipher(vxDevKey)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, vxDevIV).CryptBlocks(out, data)
	if padding := int(out[len(out)-1]); padding > 0 && padding <= aes.BlockSize && padding <= len(out) {
		out = out[:len(out)-padding]
	}
	return string(out), nil
}

var (
	macPlayerPattern     = regexp.MustCompile(`(?s)player_aaaa\s*=\s*(\{[^<]*?\})\s*;?\s*</script>`)
	playerParseURLRegexp = regexp.MustCompile(`['"]((?:https?:)?//[^'"]+)['"]\s*\+\s*MacPlayer\.Parse\s*\+\s*MacPlayer\.PlayUrl\s*\+\s*['"]([^'"]*)['"]\s*\+\s*window\.location\.href`)
	playerURLRegexp      = regexp.MustCompile(`['"]((?:https?:)?//[^'"]+)['"]\s*\+\s*MacPlayer\.PlayUrl\s*\+\s*['"]([^'"]*)['"]`)
	playerAnyURLRegexp   = regexp.MustCompile(`['"]((?:https?:)?//[^'"]+)['"]`)
	unicodeEscapeRegexp  = regexp.MustCompile(`%u([0-9A-Fa-f]{4})`)
)

func macPlayerURL(rule *Rule, pageURL string, body []byte) string {
	match := macPlayerPattern.FindSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	var config struct {
		URL     string `json:"url"`
		URLNext string `json:"url_next"`
		From    string `json:"from"`
		Encrypt any    `json:"encrypt"`
	}
	if err := json.Unmarshal(match[1], &config); err != nil {
		return ""
	}
	playURL := decodeMacPlayerValue(config.URL, fmt.Sprint(config.Encrypt))
	if playURL == "" {
		return ""
	}
	if IsMediaURL(playURL) {
		return normalizeURL(pageURL, playURL)
	}
	// Some rules (e.g. 7sefun) decode to another full playback page rather than
	// a token. Follow that page directly instead of the site's ad-injected
	// player.js wrapper.
	if parsed, err := url.Parse(playURL); err == nil && (parsed.IsAbs() || strings.HasPrefix(playURL, "/")) {
		return normalizeURL(pageURL, playURL)
	}
	from := strings.TrimSpace(config.From)
	if from == "" || from == "parse" || from == "link" {
		return ""
	}
	jsURL := normalizeURL(pageURL, "/static/player/"+from+".js")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script, err := fetchRulePage(ctx, rule, http.MethodGet, jsURL, nil, headersWithReferer(rule, pageURL))
	if err != nil {
		return ""
	}
	source := string(script)
	if match := playerParseURLRegexp.FindStringSubmatch(source); len(match) == 3 {
		return normalizeURL(pageURL, match[1]+playURL+match[2]+pageURL)
	}
	if match := playerURLRegexp.FindStringSubmatch(source); len(match) == 3 {
		return normalizeURL(pageURL, match[1]+playURL+match[2])
	}
	if strings.Contains(source, "MacPlayer.PlayUrl") {
		if match := playerAnyURLRegexp.FindStringSubmatch(source); len(match) == 2 {
			return normalizeURL(pageURL, match[1]+playURL)
		}
	}
	return ""
}

func decodeMacPlayerValue(value, encrypt string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch strings.TrimSpace(encrypt) {
	case "1":
		return jsUnescape(value)
	case "2":
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			if decoded, err = base64.RawStdEncoding.DecodeString(value); err != nil {
				return ""
			}
		}
		return jsUnescape(string(decoded))
	default:
		return jsUnescape(value)
	}
}

func jsUnescape(value string) string {
	value = unicodeEscapeRegexp.ReplaceAllStringFunc(value, func(match string) string {
		code, err := strconv.ParseInt(match[2:], 16, 32)
		if err != nil {
			return match
		}
		return string(rune(code))
	})
	if decoded, err := url.QueryUnescape(value); err == nil {
		return decoded
	}
	return value
}

func searchXPath(rule *Rule, keyword string) ([]SearchItem, error) {
	rawURL := strings.ReplaceAll(rule.SearchURL, "@keyword", escapeQuery(keyword))
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("rule %s has no searchURL", rule.Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := ruleHeaders(rule)
	method := http.MethodGet
	var body []byte
	if rule.UsePost {
		parsed, err := url.Parse(rawURL)
		if err == nil {
			values := parsed.Query()
			parsed.RawQuery = ""
			rawURL = parsed.String()
			body = []byte(values.Encode())
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
		method = http.MethodPost
	}
	raw, err := fetchRulePage(ctx, rule, method, rawURL, body, headers)
	if err != nil {
		return nil, err
	}
	root, err := htmlquery.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse search page: %w", err)
	}
	if detectsCaptcha(rule, string(raw), root) {
		return nil, ErrCaptchaRequired
	}
	nodes := queryAll(root, rule.SearchList)
	items := make([]SearchItem, 0, len(nodes))
	for _, node := range nodes {
		name := nodeText(scopedQuery(node, rule.SearchName))
		src := nodeHref(scopedQuery(node, rule.SearchResult))
		if name == "" || src == "" {
			continue
		}
		items = append(items, SearchItem{Name: name, Src: normalizeURL(rule.BaseURL, src)})
	}
	return items, nil
}

func chaptersXPath(rule *Rule, source string) ([]Road, error) {
	rawURL := normalizeURL(rule.BaseURL, source)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := fetchRulePage(ctx, rule, http.MethodGet, rawURL, nil, ruleHeaders(rule))
	if err != nil {
		return nil, err
	}
	root, err := htmlquery.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse chapter page: %w", err)
	}
	if detectsCaptcha(rule, string(raw), root) {
		return nil, ErrCaptchaRequired
	}
	roadNodes := queryAll(root, rule.ChapterRoads)
	roads := make([]Road, 0, len(roadNodes))
	for _, roadNode := range roadNodes {
		episodeNodes := scopedQueryAll(roadNode, rule.ChapterResult)
		road := Road{Name: fmt.Sprintf("播放线路%d", len(roads)+1)}
		for index, node := range episodeNodes {
			href := nodeHref(node)
			if href == "" {
				continue
			}
			name := strings.Join(strings.Fields(nodeText(node)), "")
			if name == "" {
				name = fmt.Sprintf("第%d集", index+1)
			}
			road.URLs = append(road.URLs, normalizeURL(rawURL, href))
			road.Names = append(road.Names, name)
		}
		if len(road.URLs) > 0 {
			roads = append(roads, road)
		}
	}
	return roads, nil
}

func searchAPI(rule *Rule, keyword string) ([]SearchItem, error) {
	config := rule.SearchAPIConfig
	variables := map[string]any{"keyword": keyword}
	request, err := buildAPIRequest(config.Request, variables)
	if err != nil {
		return nil, err
	}
	body, err := executeRuleRequest(rule, request)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	nodes, err := jsonPathValues(document, config.ListPath)
	if err != nil {
		return nil, err
	}
	items := make([]SearchItem, 0, len(nodes))
	for _, node := range nodes {
		name := jsonValueString(firstJSONPath(node, config.NamePath))
		src := jsonValueString(firstJSONPath(node, config.SourcePath))
		if name == "" || src == "" {
			continue
		}
		if config.SourceTemplate != "" {
			src = strings.ReplaceAll(config.SourceTemplate, "@source", url.QueryEscape(src))
		}
		items = append(items, SearchItem{Name: name, Src: src})
	}
	return items, nil
}

func chaptersAPI(rule *Rule, source string) ([]Road, error) {
	config := rule.ChapterAPIConfig
	variables := map[string]any{"source": source}
	request, err := buildAPIRequest(config.Request, variables)
	if err != nil {
		return nil, err
	}
	body, err := executeRuleRequest(rule, request)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode chapter response: %w", err)
	}
	for key, path := range config.Variables {
		value := firstJSONPath(document, path)
		if value != nil {
			variables[key] = value
		}
	}
	if config.Format == "delimited" {
		return chaptersDelimited(rule, document, config, variables)
	}
	var roadNodes []any
	if strings.TrimSpace(config.RoadsPath) == "" {
		roadNodes = []any{document}
	} else {
		roadNodes, err = jsonPathValues(document, config.RoadsPath)
		if err != nil {
			return nil, err
		}
	}
	roads := make([]Road, 0, len(roadNodes))
	for roadIndex, roadNode := range roadNodes {
		road := Road{Name: fmt.Sprintf("播放线路%d", roadIndex+1)}
		if strings.TrimSpace(config.RoadsPath) != "" {
			if name := jsonValueString(firstJSONPath(roadNode, config.RoadNamePath)); name != "" {
				road.Name = name
			}
		}
		episodeNodes, err := jsonPathValues(roadNode, config.EpisodesPath)
		if err != nil {
			continue
		}
		for episodeIndex, episodeNode := range episodeNodes {
			name := jsonValueString(firstJSONPath(episodeNode, config.EpisodeNamePath))
			if name == "" {
				name = fmt.Sprintf("第%d集", episodeIndex+1)
			}
			rawURL := ""
			if strings.TrimSpace(config.EpisodeURLPath) != "" {
				rawURL = jsonValueString(firstJSONPath(episodeNode, config.EpisodeURLPath))
			}
			pageURL, err := resolveEpisodePage(rule, config, variables, rawURL, roadIndex, episodeIndex)
			if err != nil || pageURL == "" {
				continue
			}
			road.URLs = append(road.URLs, pageURL)
			road.Names = append(road.Names, name)
		}
		if len(road.URLs) > 0 {
			roads = append(roads, road)
		}
	}
	return roads, nil
}

func chaptersDelimited(rule *Rule, document any, config APIChapterConfig, variables map[string]any) ([]Road, error) {
	namesValue := jsonValueString(firstJSONPath(document, config.RoadNamesPath))
	episodesValue := jsonValueString(firstJSONPath(document, config.RoadEpisodesPath))
	if episodesValue == "" {
		return nil, nil
	}
	roadSeparator := config.RoadSeparator
	if roadSeparator == "" {
		roadSeparator = "$$$"
	}
	episodeSeparator := config.EpisodeSeparator
	if episodeSeparator == "" {
		episodeSeparator = "#"
	}
	fieldSeparator := config.FieldSeparator
	if fieldSeparator == "" {
		fieldSeparator = "$"
	}
	roadNames := strings.Split(namesValue, roadSeparator)
	groups := strings.Split(episodesValue, roadSeparator)
	roads := make([]Road, 0, len(groups))
	for roadIndex, group := range groups {
		road := Road{Name: fmt.Sprintf("播放线路%d", roadIndex+1)}
		if roadIndex < len(roadNames) && strings.TrimSpace(roadNames[roadIndex]) != "" {
			road.Name = strings.TrimSpace(roadNames[roadIndex])
		}
		for episodeIndex, entry := range strings.Split(group, episodeSeparator) {
			entry = strings.TrimSpace(entry)
			index := strings.Index(entry, fieldSeparator)
			if index < 0 {
				continue
			}
			name := strings.TrimSpace(entry[:index])
			rawURL := strings.TrimSpace(entry[index+len(fieldSeparator):])
			pageURL, err := resolveEpisodePage(rule, config, variables, rawURL, roadIndex, episodeIndex)
			if err != nil || pageURL == "" {
				continue
			}
			if name == "" {
				name = fmt.Sprintf("第%d集", episodeIndex+1)
			}
			road.URLs = append(road.URLs, pageURL)
			road.Names = append(road.Names, name)
		}
		if len(road.URLs) > 0 {
			roads = append(roads, road)
		}
	}
	return roads, nil
}

func resolveEpisodePage(rule *Rule, config APIChapterConfig, variables map[string]any, rawURL string, roadIndex, episodeIndex int) (string, error) {
	page := config.EpisodePage
	if page == nil {
		return normalizeURL(rule.BaseURL, rawURL), nil
	}
	values := map[string]any{
		"source":        variables["source"],
		"episodeUrl":    rawURL,
		"roadIndex":     roadIndex,
		"roadNumber":    roadIndex + 1,
		"episodeIndex":  episodeIndex,
		"episodeNumber": episodeIndex + 1,
	}
	for key, value := range variables {
		if _, exists := values[key]; !exists {
			values[key] = value
		}
	}
	rendered := renderTemplate(page.URL, values, true)
	parsed, err := url.Parse(rendered)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, value := range page.Query {
		query.Set(renderTemplate(key, values, false), fmt.Sprint(renderValue(value, values)))
	}
	parsed.RawQuery = query.Encode()
	return normalizeURL(rule.BaseURL, parsed.String()), nil
}

type preparedRequest struct {
	method  string
	url     string
	headers map[string]string
	body    []byte
}

func buildAPIRequest(config APIRequestConfig, variables map[string]any) (preparedRequest, error) {
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return preparedRequest{}, fmt.Errorf("unsupported API method %s", method)
	}
	rawURL := renderTemplate(config.URL, variables, true)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return preparedRequest{}, fmt.Errorf("invalid rule API URL %q", rawURL)
	}
	query := parsed.Query()
	for key, value := range config.Query {
		query.Set(renderTemplate(key, variables, false), fmt.Sprint(renderValue(value, variables)))
	}
	parsed.RawQuery = query.Encode()
	headers := map[string]string{}
	for key, value := range config.Headers {
		headers[renderTemplate(key, variables, false)] = fmt.Sprint(renderValue(value, variables))
	}
	var body []byte
	bodyType := strings.ToLower(strings.TrimSpace(config.BodyType))
	if method == http.MethodPost && bodyType != "" && bodyType != "none" {
		switch bodyType {
		case "form":
			values := url.Values{}
			if object, ok := config.Body.(map[string]any); ok {
				for key, value := range object {
					values.Set(key, fmt.Sprint(renderValue(value, variables)))
				}
			}
			body = []byte(values.Encode())
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		case "json":
			rendered := renderValue(config.Body, variables)
			body, err = json.Marshal(rendered)
			if err != nil {
				return preparedRequest{}, err
			}
			headers["Content-Type"] = "application/json"
		}
	}
	return preparedRequest{method: method, url: parsed.String(), headers: headers, body: body}, nil
}

func executeRuleRequest(rule *Rule, request preparedRequest) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := ruleHeaders(rule)
	for key, value := range request.headers {
		headers[key] = value
	}
	return fetchRulePage(ctx, rule, request.method, request.url, request.body, headers)
}

func fetchRulePage(ctx context.Context, rule *Rule, method, rawURL string, body []byte, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		resp, err := httpx.Client().Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
			continue
		}
		if resp.StatusCode >= 400 {
			preview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return data, nil
	}
	return nil, lastErr
}

func headersWithReferer(rule *Rule, referer string) map[string]string {
	headers := ruleHeaders(rule)
	if headers["Referer"] == "" && referer != "" {
		headers["Referer"] = referer
	}
	return headers
}

func ruleHeaders(rule *Rule) map[string]string {
	ua := defaultRuleUserAgent
	if rule != nil && strings.TrimSpace(rule.UserAgent) != "" {
		ua = strings.TrimSpace(rule.UserAgent)
	}
	headers := map[string]string{
		"User-Agent": ua,
		"Accept":     "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8",
	}
	if rule != nil && strings.TrimSpace(rule.Referer) != "" {
		headers["Referer"] = rule.Referer
	}
	return headers
}

func detectsCaptcha(rule *Rule, raw string, root *html.Node) bool {
	config := rule.AntiCrawler
	if !config.Enabled {
		return false
	}
	value := strings.TrimSpace(config.CaptchaDetectValue)
	if value != "" {
		switch config.CaptchaDetectType {
		case 2:
			return strings.Contains(raw, value)
		case 3:
			re, err := regexp.Compile(value)
			return err == nil && re.MatchString(raw)
		default:
			return queryOne(root, value) != nil
		}
	}
	return queryOne(root, config.CaptchaImage) != nil ||
		queryOne(root, config.CaptchaButton) != nil
}

func queryAll(root *html.Node, expression string) []*html.Node {
	if root == nil || strings.TrimSpace(expression) == "" {
		return nil
	}
	nodes, _ := htmlquery.QueryAll(root, expression)
	return nodes
}

func queryOne(root *html.Node, expression string) *html.Node {
	if root == nil || strings.TrimSpace(expression) == "" {
		return nil
	}
	node, _ := htmlquery.Query(root, expression)
	return node
}

func scopedQuery(node *html.Node, expression string) *html.Node {
	nodes := scopedQueryAll(node, expression)
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

func scopedQueryAll(node *html.Node, expression string) []*html.Node {
	expression = strings.TrimSpace(expression)
	if expression == "" || node == nil {
		return nil
	}
	if expression == "." {
		return []*html.Node{node}
	}
	if strings.HasPrefix(expression, "//") || strings.HasPrefix(expression, "/") {
		expression = "." + expression
	}
	return queryAll(node, expression)
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == html.TextNode {
		return strings.TrimSpace(node.Data)
	}
	return strings.TrimSpace(htmlquery.InnerText(node))
}

func nodeHref(node *html.Node) string {
	if node == nil {
		return ""
	}
	if href := strings.TrimSpace(htmlquery.SelectAttr(node, "href")); href != "" {
		return href
	}
	return nodeText(node)
}

func normalizeURL(base, reference string) string {
	reference = strings.TrimSpace(strings.ReplaceAll(reference, "&amp;", "&"))
	if reference == "" {
		return ""
	}
	parsed, err := url.Parse(reference)
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

func escapeQuery(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func decodeEscapedURLs(value string) string {
	value = strings.ReplaceAll(value, `\/`, "/")
	value = strings.ReplaceAll(value, `\u0026`, "&")
	value = strings.ReplaceAll(value, "&amp;", "&")
	return value
}

func renderTemplate(template string, variables map[string]any, encode bool) string {
	re := regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_]*)`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		name := strings.TrimPrefix(match, "@")
		value, ok := variables[name]
		if !ok {
			return match
		}
		text := fmt.Sprint(value)
		if encode {
			return url.QueryEscape(text)
		}
		return text
	})
}

func renderValue(value any, variables map[string]any) any {
	switch v := value.(type) {
	case string:
		return renderTemplate(v, variables, false)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = renderValue(item, variables)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = renderValue(item, variables)
		}
		return out
	default:
		return value
	}
}

func jsonValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// jsonPathValues evaluates the restricted JSONPath subset used by Kazumi:
// $, .key, [index], [*], ['key'], ["key"].
func jsonPathValues(document any, expression string) ([]any, error) {
	tokens, err := parseJSONPath(expression)
	if err != nil {
		return nil, err
	}
	current := []any{document}
	for _, token := range tokens {
		next := []any{}
		for _, node := range current {
			switch token.kind {
			case "key":
				if object, ok := node.(map[string]any); ok {
					if value, exists := object[token.key]; exists {
						next = append(next, value)
					}
				}
			case "index":
				if list, ok := node.([]any); ok && token.index >= 0 && token.index < len(list) {
					next = append(next, list[token.index])
				}
			case "wildcard":
				switch value := node.(type) {
				case []any:
					next = append(next, value...)
				case map[string]any:
					for _, item := range value {
						next = append(next, item)
					}
				}
			}
		}
		current = next
	}
	return current, nil
}

func firstJSONPath(document any, expression string) any {
	values, err := jsonPathValues(document, expression)
	if err != nil || len(values) == 0 {
		return nil
	}
	return values[0]
}

type jsonPathToken struct {
	kind  string
	key   string
	index int
}

func parseJSONPath(expression string) ([]jsonPathToken, error) {
	expression = strings.TrimSpace(expression)
	if !strings.HasPrefix(expression, "$") {
		return nil, fmt.Errorf("invalid JSONPath %q", expression)
	}
	var tokens []jsonPathToken
	for index := 1; index < len(expression); {
		switch expression[index] {
		case '.':
			index++
			start := index
			for index < len(expression) && (isJSONNameChar(expression[index])) {
				index++
			}
			if index == start {
				return nil, fmt.Errorf("invalid JSONPath %q", expression)
			}
			tokens = append(tokens, jsonPathToken{kind: "key", key: expression[start:index]})
		case '[':
			end := strings.IndexByte(expression[index:], ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid JSONPath %q", expression)
			}
			end += index
			content := strings.TrimSpace(expression[index+1 : end])
			switch {
			case content == "*":
				tokens = append(tokens, jsonPathToken{kind: "wildcard"})
			case isDigits(content):
				value, _ := strconv.Atoi(content)
				tokens = append(tokens, jsonPathToken{kind: "index", index: value})
			case len(content) >= 2 && ((content[0] == '\'' && content[len(content)-1] == '\'') || (content[0] == '"' && content[len(content)-1] == '"')):
				tokens = append(tokens, jsonPathToken{kind: "key", key: content[1 : len(content)-1]})
			default:
				return nil, fmt.Errorf("unsupported JSONPath segment [%s]", content)
			}
			index = end + 1
		default:
			return nil, fmt.Errorf("invalid JSONPath %q", expression)
		}
	}
	return tokens, nil
}

func isJSONNameChar(ch byte) bool {
	return ch == '_' || ch == '-' || ch == '$' ||
		(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
