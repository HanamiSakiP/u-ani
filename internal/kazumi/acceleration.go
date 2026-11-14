// Package kazumi implements the Kazumi rule repository and the network
// acceleration switches used by the official Kazumi client.
package kazumi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

const (
	// OfficialRepository is the default KazumiRules raw repository.
	OfficialRepository = "https://raw.githubusercontent.com/Predidit/KazumiRules/main/"
	// APILevel is the latest Kazumi rule API level supported by the official
	// client. Rules are stored as-is; this is exposed for clients.
	APILevel = 9

	// nextBangumiMirror is the public next.bgm.tv-compatible mirror. It serves
	// p1 endpoints (calendar, subject overview, comments) and is the default
	// source for those routes.
	nextBangumiMirror   = "https://next.bangumi.lat"
	bangumiMirrorDomain = "https://api.kazumi.fyi"
	// bangumiAuthMirrorDomain is the public v0/legacy API proxy used by
	// Kazumi for authenticated requests. It also supports full-text search
	// without an application signature.
	bangumiAuthMirrorDomain = "https://api.bgmapi.com"
	ghProxyPrefix           = "https://gh-proxy.org/"
	ghFastPrefix            = "https://ghfast.top/"
	gitCodePrefix           = "https://raw.gitcode.com/gh_mirrors/ka/KazumiRules/raw/main/"
	imageMirrorURL          = "https://wsrv.nl/"
)

const (
	BangumiAccelerationDirect = "direct"
	BangumiAccelerationECH    = "ech"
	BangumiAccelerationMirror = "mirror"
)

const (
	RulesMirrorGHProxy = "ghProxy"
	RulesMirrorGHFast  = "ghfast"
	RulesMirrorGitCode = "gitcode"
	RulesMirrorDirect  = "direct"
)

const (
	ImageAccelerationDirect = "direct"
	ImageAccelerationECH    = "ech"
	ImageAccelerationMirror = "mirror"
)

// ModeOption mirrors the labels shown in Kazumi's network settings.
type ModeOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Settings is the effective Kazumi configuration.
type Settings struct {
	RuleRepository      string   `json:"rule_repository"`
	BangumiAcceleration string   `json:"bangumi_acceleration"`
	BangumiMirror       string   `json:"bangumi_mirror"`
	BangumiMirrors      []string `json:"bangumi_mirrors"`
	RulesMirror         string   `json:"rules_mirror"`
	ImageAcceleration   string   `json:"image_acceleration"`
}

// BangumiModes returns the official 番剧条目加速 choices.
func BangumiModes() []ModeOption {
	return []ModeOption{
		{Value: BangumiAccelerationDirect, Label: "直连", Description: "访问官方接口"},
		{Value: BangumiAccelerationECH, Label: "ECH", Description: "启用加密握手"},
		{Value: BangumiAccelerationMirror, Label: "镜像", Description: "镜像接口（默认）"},
	}
}

// RulesModes returns the official 规则仓库加速 choices.
func RulesModes() []ModeOption {
	return []ModeOption{
		{Value: RulesMirrorGHProxy, Label: "gh-proxy", Description: "默认镜像"},
		{Value: RulesMirrorGHFast, Label: "ghfast", Description: "备用镜像"},
		{Value: RulesMirrorGitCode, Label: "GitCode", Description: "国内镜像"},
		{Value: RulesMirrorDirect, Label: "直连", Description: "访问 GitHub"},
	}
}

// ImageModes returns the official 图片加速 choices.
func ImageModes() []ModeOption {
	return []ModeOption{
		{Value: ImageAccelerationDirect, Label: "直连", Description: "加载原始图片"},
		{Value: ImageAccelerationECH, Label: "ECH", Description: "加密握手（推荐）"},
		{Value: ImageAccelerationMirror, Label: "镜像", Description: "使用图片镜像"},
	}
}

// CurrentSettings returns the normalized effective settings.
func CurrentSettings() Settings {
	repo, err := NormalizeRuleRepository(config.Cfg.Kazumi.RuleRepository)
	if err != nil || repo == "" {
		repo = OfficialRepository
	}
	bangumi, err := NormalizeBangumiAcceleration(config.Cfg.Kazumi.BangumiAcceleration)
	if err != nil {
		bangumi = BangumiAccelerationMirror
	}
	rules, err := NormalizeRulesMirror(config.Cfg.Kazumi.RulesMirror)
	if err != nil {
		rules = RulesMirrorGHProxy
	}
	image, err := NormalizeImageAcceleration(config.Cfg.Kazumi.ImageAcceleration)
	if err != nil {
		image = ImageAccelerationECH
	}
	return Settings{
		RuleRepository:      repo,
		BangumiAcceleration: bangumi,
		BangumiMirror:       ActiveBangumiMirror(),
		BangumiMirrors:      BangumiMirrors(),
		RulesMirror:         rules,
		ImageAcceleration:   image,
	}
}

// SaveSettings validates and persists Kazumi settings.
func SaveSettings(settings Settings) error {
	repo, err := NormalizeRuleRepository(settings.RuleRepository)
	if err != nil {
		return err
	}
	bangumi, err := NormalizeBangumiAcceleration(settings.BangumiAcceleration)
	if err != nil {
		return err
	}
	rules, err := NormalizeRulesMirror(settings.RulesMirror)
	if err != nil {
		return err
	}
	image, err := NormalizeImageAcceleration(settings.ImageAcceleration)
	if err != nil {
		return err
	}
	mirrors, err := NormalizeBangumiMirrors(settings.BangumiMirrors)
	if err != nil {
		return err
	}
	mirror, err := NormalizeBangumiMirror(settings.BangumiMirror)
	if err != nil {
		return err
	}
	if !mirrorInList(mirrors, mirror) {
		mirror = ""
		if len(mirrors) > 0 {
			mirror = mirrors[0]
		}
	}
	if len(mirrors) == 0 && mirror != "" {
		mirrors = []string{mirror}
	}
	previous := config.Cfg.Kazumi
	config.Cfg.Kazumi = config.KazumiConfig{
		RuleRepository:      repo,
		BangumiAcceleration: bangumi,
		BangumiMirror:       mirror,
		BangumiMirrors:      mirrors,
		RulesMirror:         rules,
		ImageAcceleration:   image,
	}
	if err := config.Cfg.Save(); err != nil {
		config.Cfg.Kazumi = previous
		return err
	}
	return nil
}

// RulesDir returns the directory used to store installed rule JSON files.
func RulesDir() string {
	return filepath.Join(config.UANIHome, "kazumi", "rules")
}

// NormalizeBangumiAcceleration validates a 番剧条目加速 value.
func NormalizeBangumiAcceleration(value string) (string, error) {
	return normalizeMode(value, BangumiAccelerationMirror, BangumiAccelerationDirect, BangumiAccelerationECH, BangumiAccelerationMirror)
}

// NormalizeImageAcceleration validates a 图片加速 value.
func NormalizeImageAcceleration(value string) (string, error) {
	return normalizeMode(value, ImageAccelerationECH, ImageAccelerationDirect, ImageAccelerationECH, ImageAccelerationMirror)
}

func normalizeMode(value, fallback string, allowed ...string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return fallback, nil
	}
	switch normalized {
	case "镜像", "mirror":
		normalized = "mirror"
	case "直连", "direct":
		normalized = "direct"
	case "加密握手", "ech":
		normalized = "ech"
	}
	for _, item := range allowed {
		if normalized == item {
			return normalized, nil
		}
	}
	return "", fmt.Errorf("unknown acceleration mode %q (use one of %s)", value, strings.Join(allowed, ", "))
}

// NormalizeRulesMirror validates a 规则仓库加速 value and returns the
// official enum spelling.
func NormalizeRulesMirror(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ghproxy", "gh-proxy", "gh_proxy", "默认镜像":
		return RulesMirrorGHProxy, nil
	case "ghfast", "gh-fast", "备用镜像":
		return RulesMirrorGHFast, nil
	case "gitcode", "国内镜像":
		return RulesMirrorGitCode, nil
	case "direct", "直连":
		return RulesMirrorDirect, nil
	default:
		return "", fmt.Errorf("unknown rule repository mirror %q (use one of ghProxy, ghfast, gitcode, direct)", value)
	}
}

// NormalizeRuleRepository mirrors Kazumi's RuleRepositoryConfig rules. An
// empty value selects the official KazumiRules repository.
func NormalizeRuleRepository(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return OfficialRepository, nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("rule repository must be a valid HTTP or HTTPS URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("rule repository must not contain a username or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("rule repository must not contain query parameters or fragments")
	}
	if strings.EqualFold(u.Host, "github.com") {
		return "", fmt.Errorf("use a raw file URL, not a GitHub repository web page")
	}

	// Preserve the inner slashes of proxy-prefixed repositories such as
	// https://gh-proxy.org/https://raw.githubusercontent.com/... .
	rawPath := u.EscapedPath()
	if rawPath == "" {
		rawPath = "/"
	}
	for strings.HasSuffix(rawPath, "/") {
		rawPath = strings.TrimSuffix(rawPath, "/")
	}
	lowerPath := strings.ToLower(rawPath)
	if strings.HasSuffix(lowerPath, "/index.json") {
		rawPath = rawPath[:len(rawPath)-len("/index.json")]
	} else if !strings.HasSuffix(u.EscapedPath(), "/") {
		last := rawPath[strings.LastIndex(rawPath, "/")+1:]
		if strings.Contains(last, ".") {
			return "", fmt.Errorf("rule repository must be a directory or end with index.json")
		}
	}
	out := u.Scheme + "://" + u.Host
	if rawPath == "" {
		return out + "/", nil
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	return out + rawPath + "/", nil
}

// CatalogURL returns the repository index URL.
func CatalogURL(repository string) (string, error) {
	repo, err := NormalizeRuleRepository(repository)
	if err != nil {
		return "", err
	}
	return repo + "index.json", nil
}

// RuleURL returns the URL of one rule in a repository.
func RuleURL(repository, name string) (string, error) {
	if err := validateRuleName(name); err != nil {
		return "", err
	}
	repo, err := NormalizeRuleRepository(repository)
	if err != nil {
		return "", err
	}
	return repo + url.PathEscape(name) + ".json", nil
}

// ApplyRulesMirror rewrites repository URLs exactly like Kazumi's
// RulesMirrorInterceptor. Mirrors only apply to the official repository.
func ApplyRulesMirror(rawURL, mode string) string {
	normalized, err := NormalizeRulesMirror(mode)
	if err != nil {
		normalized = RulesMirrorGHProxy
	}
	if !strings.HasPrefix(rawURL, OfficialRepository) {
		return rawURL
	}
	switch normalized {
	case RulesMirrorGHProxy:
		return ghProxyPrefix + rawURL
	case RulesMirrorGHFast:
		return ghFastPrefix + rawURL
	case RulesMirrorGitCode:
		return gitCodePrefix + strings.TrimPrefix(rawURL, OfficialRepository)
	default:
		return rawURL
	}
}

// ApplyBangumiURL rewrites bgm.tv public API URLs for the 番剧条目加速 mode.
func ApplyBangumiURL(rawURL, mode string) string {
	normalized, err := NormalizeBangumiAcceleration(mode)
	if err != nil {
		normalized = BangumiAccelerationMirror
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if !isBangumiAPIHost(u.Host) {
		return rawURL
	}
	switch normalized {
	case BangumiAccelerationMirror:
		target := bangumiMirrorBaseForPath(u.Path) + u.EscapedPath()
		if u.RawQuery != "" {
			target += "?" + u.RawQuery
		}
		return target
	case BangumiAccelerationECH:
		if u.Scheme == "http" {
			u.Scheme = "https"
			return u.String()
		}
	}
	return rawURL
}

// ImageURLForRuntime returns the image URL to fetch under the current image
// acceleration mode. This is used by cover downloads.
func ImageURLForRuntime(rawURL string) string {
	mode, _ := NormalizeImageAcceleration(config.Cfg.Kazumi.ImageAcceleration)
	return ApplyImageURL(rawURL, mode)
}

// ApplyImageURL rewrites Bangumi image URLs like Kazumi's image service.
func ApplyImageURL(rawURL, mode string) string {
	normalized, err := NormalizeImageAcceleration(mode)
	if err != nil {
		normalized = ImageAccelerationECH
	}
	u, err := url.Parse(rawURL)
	if err != nil || !isBangumiImage(u) {
		return rawURL
	}
	switch normalized {
	case ImageAccelerationMirror:
		source := u.Host + u.EscapedPath()
		if u.RawQuery != "" {
			source += "?" + u.RawQuery
		}
		target := imageMirrorURL + "?url=" + url.QueryEscape(source)
		if strings.HasSuffix(strings.ToLower(u.Path), ".gif") {
			target += "&n=-1"
		}
		return target
	case ImageAccelerationECH:
		if u.Scheme == "http" {
			u.Scheme = "https"
			return u.String()
		}
	}
	return rawURL
}

// IsBangumiImage reports whether a URL is one of the images Kazumi
// accelerates.
func IsBangumiImage(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && isBangumiImage(u)
}

func isBangumiImage(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if u.Host == "lain.bgm.tv" || u.Host == "lain.bangumi.lat" {
		return true
	}
	if u.Host != "api.bgm.tv" {
		return false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 4 || segments[0] != "v0" || segments[3] != "image" {
		return false
	}
	switch segments[1] {
	case "subjects", "characters", "persons":
	default:
		return false
	}
	id, err := strconv.Atoi(segments[2])
	return err == nil && id > 0
}

// NormalizeBangumiMirror validates an optional custom Bangumi API mirror.
func NormalizeBangumiMirror(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("Bangumi mirror must be a valid HTTP or HTTPS URL")
	}
	return strings.TrimRight(value, "/"), nil
}

// NormalizeBangumiMirrors validates a list of custom Bangumi mirrors.
func NormalizeBangumiMirrors(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		normalized, err := NormalizeBangumiMirror(value)
		if err != nil {
			return nil, err
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out, nil
}

// BangumiMirrors returns the configured mirrors, or the built-in defaults.
// next.bangumi.lat comes first so p1 endpoints use it by default.
func BangumiMirrors() []string {
	mirrors, err := NormalizeBangumiMirrors(config.Cfg.Kazumi.BangumiMirrors)
	if err == nil && len(mirrors) > 0 {
		return mirrors
	}
	if mirror, err := NormalizeBangumiMirror(config.Cfg.Kazumi.BangumiMirror); err == nil && mirror != "" {
		return []string{mirror}
	}
	return []string{nextBangumiMirror, bangumiMirrorDomain, bangumiAuthMirrorDomain}
}

// ActiveBangumiMirror returns the mirror selected in settings. The selected
// mirror is tried first by every request; the remaining list is a fallback.
func ActiveBangumiMirror() string {
	mirrors := BangumiMirrors()
	active, err := NormalizeBangumiMirror(config.Cfg.Kazumi.BangumiMirror)
	if err == nil && mirrorInList(mirrors, active) {
		return active
	}
	if len(mirrors) > 0 {
		return mirrors[0]
	}
	return bangumiMirrorDomain
}

func mirrorInList(mirrors []string, mirror string) bool {
	if mirror == "" {
		return false
	}
	for _, item := range mirrors {
		if item == mirror {
			return true
		}
	}
	return false
}

// BangumiSearchMirrors returns search-capable mirrors, active first. The
// public v0 proxy is always included because the default mirror requires an
// app signature for /v0/search/subjects.
func BangumiSearchMirrors() []string {
	mirrors := BangumiMirrors()
	active := ActiveBangumiMirror()
	out := make([]string, 0, len(mirrors)+1)
	seen := map[string]bool{}
	for _, mirror := range append([]string{active}, mirrors...) {
		// next.bangumi.lat only exposes p1 endpoints; v0/legacy search
		// requests would return its HTML frontend.
		if mirror == "" || mirror == nextBangumiMirror || seen[mirror] {
			continue
		}
		seen[mirror] = true
		out = append(out, mirror)
	}
	if !seen[bangumiAuthMirrorDomain] {
		out = append(out, bangumiAuthMirrorDomain)
	}
	return out
}

func bangumiMirrorBase() string {
	return ActiveBangumiMirror()
}

// bangumiMirrorSupportsPath reports whether a mirror can serve a path. The
// next API mirror only implements p1 routes; the auth proxy avoids p1 and
// Kazumi-specific routes.
func bangumiMirrorSupportsPath(mirror, path string) bool {
	switch mirror {
	case nextBangumiMirror:
		return strings.HasPrefix(path, "/p1/")
	case bangumiAuthMirrorDomain:
		return !strings.HasPrefix(path, "/p1/") && !strings.HasPrefix(path, "/kazumi/")
	default:
		return true
	}
}

func bangumiMirrorBaseForPath(path string) string {
	active := ActiveBangumiMirror()
	if bangumiMirrorSupportsPath(active, path) {
		return active
	}
	for _, mirror := range BangumiMirrors() {
		if bangumiMirrorSupportsPath(mirror, path) {
			return mirror
		}
	}
	return bangumiMirrorDomain
}

func isBangumiAPIHost(host string) bool {
	return host == "api.bgm.tv" || host == "next.bgm.tv"
}

func validateRuleName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("rule name must not be empty")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid rule name %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid rule name %q", name)
	}
	for _, ch := range name {
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("invalid rule name %q", name)
		}
	}
	return nil
}

// TestBangumiMirror checks whether a mirror can serve core Bangumi APIs.
// Comments are checked as a best-effort signal because many mirrors lack
// p1 comments support even when calendar/search/subject endpoints work.
func TestBangumiMirror(value string) (int, time.Duration, error) {
	base, err := NormalizeBangumiMirror(value)
	if err != nil {
		return 0, 0, err
	}
	if base == "" {
		base = bangumiMirrorDomain
	}
	start := time.Now()
	lastStatus := 0
	var lastErr error
	coreOK := false
	for _, path := range []string{
		"/p1/calendar",
		"/search/subject/test?type=2&responseGroup=small&max_results=1",
		"/v0/subjects/253",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			cancel()
			return 0, time.Since(start), err
		}
		req.Header.Set("User-Agent", "uani/1.0.0 (mirror test)")
		req.Header.Set("Accept", "application/json")
		resp, err := httpx.Client().Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		lastStatus = resp.StatusCode
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			lastErr = fmt.Errorf("镜像返回了非 JSON 数据")
			continue
		}
		if code, ok := payload["code"].(float64); ok && code >= 400 {
			lastErr = fmt.Errorf("镜像返回错误：%v", payload["error"])
			continue
		}
		if message, ok := payload["error"].(string); ok && strings.TrimSpace(message) != "" {
			lastErr = fmt.Errorf("镜像返回错误：%s", message)
			continue
		}
		switch {
		case strings.HasPrefix(path, "/v0/subjects/"):
			if _, ok := payload["id"]; !ok {
				lastErr = fmt.Errorf("镜像缺少 Bangumi 条目数据")
				continue
			}
		case strings.HasPrefix(path, "/search/subject/"):
			if _, ok := payload["list"]; !ok {
				lastErr = fmt.Errorf("镜像缺少 Bangumi 搜索结果")
				continue
			}
		}
		coreOK = true
		lastErr = nil
		break
	}
	if !coreOK {
		return lastStatus, time.Since(start), lastErr
	}
	// Comments are not required for a mirror to be usable, but if it explicitly
	// rejects the comments endpoint we surface that as a warning-capable error.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/p1/subjects/253/comments?limit=1&offset=0", nil)
	if err == nil {
		req.Header.Set("User-Agent", "uani/1.0.0 (mirror test)")
		req.Header.Set("Accept", "application/json")
		if resp, err := httpx.Client().Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode < 400 {
				var payload map[string]any
				if json.Unmarshal(body, &payload) == nil {
					if msg, ok := payload["error"].(string); ok && strings.TrimSpace(msg) != "" {
						cancel()
						return lastStatus, time.Since(start), fmt.Errorf("镜像可用，但吐槽接口不可用：%s", msg)
					}
				}
			}
		}
	}
	cancel()
	return lastStatus, time.Since(start), nil
}
