package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Version is the BGmi-compatible version.
const Version = "1.0.0"

type Source string

const (
	SourceMikan      Source = "mikan_project"
	SourceBangumiMoe Source = "bangumi_moe"
	SourceDmhy       Source = "dmhy"
)

var SupportWebsite = []struct {
	View string
	ID   string
}{
	{"萌番组 https://bangumi.moe/", "bangumi_moe"},
	{"蜜柑计划 https://mikanani.me/", "mikan_project"},
	{"动漫花园 http://share.dmhy.org/", "dmhy"},
}

var (
	UANIHome       string
	ConfigFilePath string
	IsWindows      = os.PathSeparator == '\\'
	Cfg            Config
)

type Aria2Config struct {
	RPCURL   string `toml:"rpc_url" json:"rpc_url"`
	RPCToken string `toml:"rpc_token" json:"rpc_token"`
}

type TransmissionConfig struct {
	RPCHost     string   `toml:"rpc_host" json:"rpc_host"`
	RPCPort     int      `toml:"rpc_port" json:"rpc_port"`
	RPCUsername string   `toml:"rpc_username" json:"rpc_username"`
	RPCPassword string   `toml:"rpc_password" json:"rpc_password"`
	RPCPath     string   `toml:"rpc_path" json:"rpc_path"`
	Labels      []string `toml:"labels" json:"labels"`
}

type QBittorrentConfig struct {
	RPCHost     string   `toml:"rpc_host" json:"rpc_host"`
	RPCPort     int      `toml:"rpc_port" json:"rpc_port"`
	RPCUsername string   `toml:"rpc_username" json:"rpc_username"`
	RPCPassword string   `toml:"rpc_password" json:"rpc_password"`
	Category    string   `toml:"category" json:"category"`
	Tags        []string `toml:"tags" json:"tags"`
}

type DelugeConfig struct {
	RPCURL      string `toml:"rpc_url" json:"rpc_url"`
	RPCPassword string `toml:"rpc_password" json:"rpc_password"`
}

type HTTPConfig struct {
	AdminToken       string `toml:"admin_token" json:"admin_token"`
	DanmakuAPIURL    string `toml:"danmaku_api_url" json:"danmaku_api_url"`
	ServeStaticFiles bool   `toml:"serve_static_files" json:"serve_static_files"`
	DisableWeb       bool   `toml:"disable_web" json:"disable_web"`
	DisableMCP       bool   `toml:"disable_mcp" json:"disable_mcp"`
}

// KazumiConfig contains the Kazumi rule repository and the same acceleration
// switches used by the official Kazumi client.
type KazumiConfig struct {
	RuleRepository      string   `toml:"rule_repository" json:"rule_repository"`
	BangumiAcceleration string   `toml:"bangumi_acceleration" json:"bangumi_acceleration"`
	BangumiMirror       string   `toml:"bangumi_mirror" json:"bangumi_mirror"`
	BangumiMirrors      []string `toml:"bangumi_mirrors" json:"bangumi_mirrors"`
	RulesMirror         string   `toml:"rules_mirror" json:"rules_mirror"`
	ImageAcceleration   string   `toml:"image_acceleration" json:"image_acceleration"`
}

// BangumiConfig contains the Bangumi (bgm.tv) account used by the
// subscription sync feature. SyncTypes selects which collection buckets are
// imported, for example ["watching"] or ["watching", "wish"].
type BangumiConfig struct {
	AccessToken      string   `toml:"access_token" json:"access_token"`
	Username         string   `toml:"username" json:"username"`
	SyncTypes        []string `toml:"sync_types" json:"sync_types"`
	SyncTypesVersion int      `toml:"sync_types_version" json:"sync_types_version"`
	SyncEnabled      bool     `toml:"sync_enabled" json:"sync_enabled"`
	LastSync         int64    `toml:"last_sync" json:"last_sync"`
}

// WebDAVConfig stores an optional WebDAV endpoint used to sync collection
// data between uani instances.
type WebDAVConfig struct {
	URL              string `toml:"url" json:"url"`
	Username         string `toml:"username" json:"username"`
	Password         string `toml:"password" json:"password"`
	Enabled          bool   `toml:"enabled" json:"enabled"`
	SyncCollectibles bool   `toml:"sync_collectibles" json:"sync_collectibles"`
	SyncDirection    string `toml:"sync_direction" json:"sync_direction"`
	LastSync         int64  `toml:"last_sync" json:"last_sync"`
}

type Config struct {
	DataSource       Source `toml:"data_source" json:"data_source"`
	DownloadDelegate string `toml:"download_delegate" json:"download_delegate"`

	TmpPath string `toml:"tmp_path" json:"tmp_path"`
	LogPath string `toml:"log_path" json:"log_path"`
	Proxy   string `toml:"proxy" json:"proxy"`

	SavePath        string `toml:"save_path" json:"save_path"`
	CachePath       string `toml:"cache_path" json:"cache_path"`
	FrontStaticPath string `toml:"front_static_path" json:"front_static_path"`
	DBPath          string `toml:"db_path" json:"db_path"`
	ScriptPath      string `toml:"script_path" json:"script_path"`
	ToolsPath       string `toml:"tools_path" json:"tools_path"`

	MaxPath int `toml:"max_path" json:"max_path"`

	BangumiMoeURL string `toml:"bangumi_moe_url" json:"bangumi_moe_url"`
	ShareDmhyURL  string `toml:"share_dmhy_url" json:"share_dmhy_url"`

	MikanUsername string `toml:"mikan_username" json:"mikan_username"`
	MikanPassword string `toml:"mikan_password" json:"mikan_password"`

	HTTP    HTTPConfig    `toml:"http" json:"http"`
	Kazumi  KazumiConfig  `toml:"kazumi" json:"kazumi"`
	Bangumi BangumiConfig `toml:"bangumi" json:"bangumi"`
	WebDAV  WebDAVConfig  `toml:"webdav" json:"webdav"`
	Lang    string        `toml:"lang" json:"lang"`

	Aria2        Aria2Config        `toml:"aria2" json:"aria2"`
	Transmission TransmissionConfig `toml:"transmission" json:"transmission"`
	QBittorrent  QBittorrentConfig  `toml:"qbittorrent" json:"qbittorrent"`
	Deluge       DelugeConfig       `toml:"deluge" json:"deluge"`

	EnableGlobalIncludeKeywords bool     `toml:"enable_global_include_keywords" json:"enable_global_include_keywords"`
	GlobalIncludeKeywords       []string `toml:"global_include_keywords" json:"global_include_keywords"`

	EnableGlobalFilters bool     `toml:"enable_global_filters" json:"enable_global_filters"`
	GlobalFilters       []string `toml:"global_filters" json:"global_filters"`

	SavePathMap map[string]string `toml:"save_path_map" json:"save_path_map"`

	EnablePathFormatter bool   `toml:"enable_path_formatter" json:"enable_path_formatter"`
	PathFormatter       string `toml:"path_formatter" json:"path_formatter"`
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	// Accept the legacy BGMI_ prefix so existing environments keep working.
	if strings.HasPrefix(key, "UANI_") {
		if v := os.Getenv("BGMI_" + strings.TrimPrefix(key, "UANI_")); v != "" {
			return v
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := env(key, ""); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envList(key string, fallback []string) []string {
	v := env(key, "")
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func getUANIHome() string {
	if h := os.Getenv("UANI_PATH"); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	// Legacy compatibility: BGMI_PATH used to select the data directory.
	if h := os.Getenv("BGMI_PATH"); h != "" {
		if abs, err := filepath.Abs(h); err == nil {
			return abs
		}
		return h
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".uani")
	}
	return filepath.Join(os.TempDir(), ".uani")
}

func defaultConfig() Config {
	home := UANIHome
	token := env("UANI_HTTP_ADMIN_TOKEN", "")
	if token == "" {
		buf := make([]byte, 16)
		_, _ = rand.Read(buf)
		token = base64.RawURLEncoding.EncodeToString(buf)
	}
	source := Source(env("UANI_DATA_SOURCE", string(SourceBangumiMoe)))
	return Config{
		DataSource:       source,
		DownloadDelegate: env("UANI_DOWNLOAD_DELEGATE", "aria2-rpc"),
		TmpPath:          env("UANI_TMP_PATH", filepath.Join(home, "tmp")),
		LogPath:          env("UANI_LOG_PATH", filepath.Join(home, "log")),
		Proxy:            env("UANI_PROXY", ""),
		SavePath:         env("UANI_SAVE_PATH", filepath.Join(home, "bangumi")),
		CachePath:        env("UANI_CACHE_PATH", filepath.Join(home, "cache")),
		FrontStaticPath:  env("UANI_FRONT_STATIC_PATH", filepath.Join(home, "front_static")),
		DBPath:           env("UANI_DB_PATH", filepath.Join(home, "bangumi.db")),
		ScriptPath:       env("UANI_SCRIPT_PATH", filepath.Join(home, "scripts")),
		ToolsPath:        env("UANI_TOOLS_PATH", filepath.Join(home, "tools")),
		MaxPath:          3,
		BangumiMoeURL:    env("UANI_BANGUMI_MOE_URL", "https://bangumi.moe"),
		ShareDmhyURL:     env("UANI_SHARE_DMHY_URL", "https://share.dmhy.org"),
		MikanUsername:    env("UANI_MIKAN_USERNAME", ""),
		MikanPassword:    env("UANI_MIKAN_PASSWORD", ""),
		HTTP: HTTPConfig{
			AdminToken:       token,
			DanmakuAPIURL:    env("UANI_HTTP_DANMAKU_API_URL", ""),
			ServeStaticFiles: envBool("UANI_HTTP_SERVE_STATIC_FILES", false),
			DisableWeb:       envBool("UANI_HTTP_DISABLE_WEB", false),
			DisableMCP:       envBool("UANI_HTTP_DISABLE_MCP", false),
		},
		Kazumi: KazumiConfig{
			RuleRepository:      env("UANI_KAZUMI_RULE_REPOSITORY", ""),
			BangumiAcceleration: env("UANI_KAZUMI_BANGUMI_ACCELERATION", "mirror"),
			BangumiMirror:       env("UANI_KAZUMI_BANGUMI_MIRROR", ""),
			BangumiMirrors:      envList("UANI_KAZUMI_BANGUMI_MIRRORS", nil),
			RulesMirror:         env("UANI_KAZUMI_RULES_MIRROR", "ghProxy"),
			ImageAcceleration:   env("UANI_KAZUMI_IMAGE_ACCELERATION", "ech"),
		},
		Bangumi: BangumiConfig{
			AccessToken: env("UANI_BANGUMI_ACCESS_TOKEN", ""),
			Username:    env("UANI_BANGUMI_USERNAME", ""),
			SyncTypes:   envList("UANI_BANGUMI_SYNC_TYPES", []string{"wish", "watched", "watching", "on_hold", "dropped"}),
			SyncEnabled: envBool("UANI_BANGUMI_SYNC_ENABLED", false),
		},
		WebDAV: WebDAVConfig{
			URL:              env("UANI_WEBDAV_URL", ""),
			Username:         env("UANI_WEBDAV_USERNAME", ""),
			Password:         env("UANI_WEBDAV_PASSWORD", ""),
			Enabled:          envBool("UANI_WEBDAV_ENABLED", false),
			SyncCollectibles: envBool("UANI_WEBDAV_SYNC_COLLECTIBLES", true),
			SyncDirection:    env("UANI_WEBDAV_SYNC_DIRECTION", "download"),
		},
		Lang: env("UANI_LANG", "zh_cn"),
		Aria2: Aria2Config{
			RPCURL:   env("UANI_ARIA2_RPC_URL", "http://127.0.0.1:6800/rpc"),
			RPCToken: env("UANI_ARIA2_RPC_TOKEN", "token:"),
		},
		Transmission: TransmissionConfig{
			RPCHost:     env("UANI_TRANSMISSION_RPC_HOST", "127.0.0.1"),
			RPCPort:     envInt("UANI_TRANSMISSION_RPC_PORT", 9091),
			RPCUsername: env("UANI_TRANSMISSION_RPC_USERNAME", "your_username"),
			RPCPassword: env("UANI_TRANSMISSION_RPC_PASSWORD", "your_password"),
			RPCPath:     env("UANI_TRANSMISSION_RPC_PATH", "/transmission/rpc"),
			Labels:      []string{"uani"},
		},
		QBittorrent: QBittorrentConfig{
			RPCHost:     env("UANI_QBITTORRENT_RPC_HOST", "127.0.0.1"),
			RPCPort:     envInt("UANI_QBITTORRENT_RPC_PORT", 8080),
			RPCUsername: env("UANI_QBITTORRENT_RPC_USERNAME", "admin"),
			RPCPassword: env("UANI_QBITTORRENT_RPC_PASSWORD", "adminadmin"),
			Category:    env("UANI_QBITTORRENT_RPC_CATEGORY", ""),
			Tags:        []string{"uani"},
		},
		Deluge: DelugeConfig{
			RPCURL:      env("UANI_DELUGE_RPC_URL", "http://127.0.0.1:8112/json"),
			RPCPassword: env("UANI_DELUGE_RPC_PASSWORD", "deluge"),
		},
		EnableGlobalIncludeKeywords: false,
		GlobalIncludeKeywords:       []string{"1080"},
		EnableGlobalFilters:         true,
		GlobalFilters:               []string{"Leopard-Raws", "hevc", "x265", "c-a Raws", "U3-Web"},
		SavePathMap:                 map[string]string{},
		EnablePathFormatter:         false,
		PathFormatter:               "{name}/S{season:02d}/S{season:02d}E{episode:02d}.{suffix}",
	}
}

// Load reads config from disk (or environment defaults). It also creates the
// data directory and the default config file when they do not exist yet.
func Load() error {
	UANIHome = getUANIHome()
	ConfigFilePath = filepath.Join(UANIHome, "config.toml")
	Cfg = defaultConfig()

	if err := os.MkdirAll(UANIHome, 0o755); err != nil {
		return err
	}

	data, err := os.ReadFile(ConfigFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		migrateBangumiSyncTypes()
		if err := Validate(&Cfg); err != nil {
			return err
		}
		if err := Cfg.Save(); err != nil {
			return err
		}
		return EnsureDirs()
	}
	if err := toml.Unmarshal(data, &Cfg); err != nil {
		fmt.Println("配置文件错误，请手动编辑配置文件后重试")
		fmt.Println("配置文件位置：", ConfigFilePath)
		fmt.Println(err)
		os.Exit(1)
	}
	if err := Validate(&Cfg); err != nil {
		fmt.Println("配置文件错误，请手动编辑配置文件后重试")
		fmt.Println("配置文件位置：", ConfigFilePath)
		fmt.Println(err)
		os.Exit(1)
	}
	if migrateBangumiSyncTypes() {
		if err := Cfg.Save(); err != nil {
			return err
		}
	}
	changed := migrateWebDAVDirection()
	if migrateBangumiMirrors() {
		changed = true
	}
	if Cfg.CachePath == "" {
		Cfg.CachePath = filepath.Join(UANIHome, "cache")
		changed = true
	}
	if changed {
		if err := Cfg.Save(); err != nil {
			return err
		}
	}
	if err := EnsureDirs(); err != nil {
		return err
	}
	migrateLegacyCoverDir()
	return nil
}

// migrateLegacyCoverDir moves covers from the historical save_path/.cover
// location into the new cache_path/covers directory.
func migrateLegacyCoverDir() {
	if Cfg.CachePath == "" || Cfg.SavePath == "" {
		return
	}
	old := filepath.Join(Cfg.SavePath, ".cover")
	if _, err := os.Stat(old); err != nil {
		return
	}
	newDir := filepath.Join(Cfg.CachePath, "covers")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return
	}
	entries, err := os.ReadDir(old)
	if err != nil {
		return
	}
	for _, entry := range entries {
		src := filepath.Join(old, entry.Name())
		dst := filepath.Join(newDir, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.Rename(src, dst)
	}
	_ = os.Remove(old)
}

func migrateWebDAVDirection() bool {
	if strings.EqualFold(strings.TrimSpace(Cfg.WebDAV.SyncDirection), "bi") || strings.TrimSpace(Cfg.WebDAV.SyncDirection) == "" {
		Cfg.WebDAV.SyncDirection = "download"
		return true
	}
	return false
}

func migrateBangumiMirrors() bool {
	changed := false
	if len(Cfg.Kazumi.BangumiMirrors) == 0 && strings.TrimSpace(Cfg.Kazumi.BangumiMirror) != "" {
		Cfg.Kazumi.BangumiMirrors = []string{strings.TrimRight(strings.TrimSpace(Cfg.Kazumi.BangumiMirror), "/")}
		changed = true
	}
	// Upgrade the historical built-in mirror list to use next.bangumi.lat as
	// the default p1 source. Only touch the exact legacy defaults so custom
	// lists keep their order.
	const (
		legacyDetail = "https://api.kazumi.fyi"
		legacyAuth   = "https://api.bgmapi.com"
		nextMirror   = "https://next.bangumi.lat"
	)
	mirrors := Cfg.Kazumi.BangumiMirrors
	legacyDefault := (len(mirrors) == 2 && mirrors[0] == legacyDetail && mirrors[1] == legacyAuth) ||
		(len(mirrors) == 1 && mirrors[0] == legacyDetail)
	if legacyDefault {
		Cfg.Kazumi.BangumiMirrors = append([]string{nextMirror}, mirrors...)
		active := strings.TrimRight(strings.TrimSpace(Cfg.Kazumi.BangumiMirror), "/")
		if active == "" || active == legacyDetail {
			Cfg.Kazumi.BangumiMirror = nextMirror
		}
		changed = true
	}
	return changed
}

func migrateBangumiSyncTypes() bool {
	if Cfg.Bangumi.SyncTypesVersion >= 2 {
		return false
	}
	// Older uani versions wrote the single default "watching". Expand that
	// legacy default once so Bangumi sync matches Kazumi's all-collection sync.
	if len(Cfg.Bangumi.SyncTypes) == 1 && Cfg.Bangumi.SyncTypes[0] == "watching" {
		Cfg.Bangumi.SyncTypes = []string{"wish", "watched", "watching", "on_hold", "dropped"}
	}
	Cfg.Bangumi.SyncTypesVersion = 2
	return true
}

// EnsureDirs creates the data directory and all directories referenced by the
// current configuration. It is safe to call repeatedly.
func EnsureDirs() error {
	paths := []string{
		UANIHome,
		Cfg.SavePath,
		Cfg.CachePath,
		Cfg.TmpPath,
		Cfg.LogPath,
		Cfg.ScriptPath,
		Cfg.ToolsPath,
		Cfg.FrontStaticPath,
		filepath.Join(UANIHome, "kazumi", "rules"),
		filepath.Join(Cfg.SavePath, "kazumi"),
		filepath.Join(Cfg.CachePath, "covers"),
		filepath.Join(Cfg.CachePath, "kazumi"),
	}
	if Cfg.DBPath != "" {
		paths = append(paths, filepath.Dir(Cfg.DBPath))
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func Validate(c *Config) error {
	switch c.DataSource {
	case SourceMikan, SourceBangumiMoe, SourceDmhy:
	default:
		return fmt.Errorf("data source should be one of mikan_project, bangumi_moe, dmhy")
	}
	if c.MaxPath < 0 {
		return fmt.Errorf("max_path should not be negative")
	}
	switch strings.ToLower(strings.TrimSpace(c.WebDAV.SyncDirection)) {
	case "", "bi", "upload", "download":
	default:
		return fmt.Errorf("webdav.sync_direction should be upload or download")
	}
	if value := strings.TrimSpace(c.WebDAV.URL); value != "" {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("webdav.url must be an HTTP or HTTPS URL")
		}
	}
	if !validAcceleration(c.Kazumi.BangumiAcceleration, "direct", "ech", "mirror") {
		return fmt.Errorf("kazumi.bangumi_acceleration should be one of direct, ech, mirror")
	}
	if !validRulesMirror(c.Kazumi.RulesMirror) {
		return fmt.Errorf("kazumi.rules_mirror should be one of ghProxy, ghfast, gitcode, direct")
	}
	if !validAcceleration(c.Kazumi.ImageAcceleration, "direct", "ech", "mirror") {
		return fmt.Errorf("kazumi.image_acceleration should be one of direct, ech, mirror")
	}
	mirrors := append([]string{}, c.Kazumi.BangumiMirrors...)
	if value := strings.TrimSpace(c.Kazumi.BangumiMirror); value != "" {
		mirrors = append(mirrors, value)
	}
	for _, mirror := range mirrors {
		u, err := url.Parse(strings.TrimSpace(mirror))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("kazumi.bangumi_mirror must be an HTTP or HTTPS URL")
		}
	}
	if repo := strings.TrimSpace(c.Kazumi.RuleRepository); repo != "" {
		u, err := url.Parse(repo)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("kazumi.rule_repository must be an HTTP or HTTPS URL")
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("kazumi.rule_repository must not contain credentials, query parameters, or fragments")
		}
		if strings.EqualFold(u.Host, "github.com") {
			return fmt.Errorf("kazumi.rule_repository must use a raw file URL, not a GitHub repository web page")
		}
		path := strings.TrimRight(u.EscapedPath(), "/")
		if path != "" && !strings.HasSuffix(strings.ToLower(path), "/index.json") {
			last := path[strings.LastIndex(path, "/")+1:]
			if strings.Contains(last, ".") {
				return fmt.Errorf("kazumi.rule_repository must be a directory or end with index.json")
			}
		}
	}
	for _, value := range c.Bangumi.SyncTypes {
		if !validCollectionType(value) {
			return fmt.Errorf("bangumi.sync_types should contain only wish, watched, watching, on_hold, dropped or all")
		}
	}
	return nil
}

func validCollectionType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all", "wish", "watched", "watching", "on_hold", "onhold", "dropped",
		"想看", "看过", "在看", "搁置", "抛弃", "全部",
		"plan_to_watch", "plantowatch", "collect", "do", "hold", "drop",
		"1", "2", "3", "4", "5":
		return true
	default:
		return false
	}
}

func validAcceleration(value string, allowed ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return true
	}
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func validRulesMirror(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ghproxy", "gh-proxy", "gh_proxy", "ghfast", "gh-fast", "gitcode", "direct":
		return true
	default:
		return false
	}
}

func Marshal(c Config) ([]byte, error) {
	return toml.Marshal(c)
}

// Save writes the current configuration back to disk.
func (c Config) Save() error {
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ConfigFilePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ConfigFilePath, data, 0o644)
}

func WriteDefaultConfig() error {
	if _, err := os.Stat(ConfigFilePath); err == nil {
		return nil
	}
	return Cfg.Save()
}

// ConfigPrint returns the TOML representation of the current config.
func ConfigPrint() (string, error) {
	data, err := toml.Marshal(Cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetValue updates a config value addressed by its TOML key path.
func SetValue(keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("no config key given")
	}
	if keys[0] == "source" {
		return fmt.Errorf("you can't change source with this command, use `uani source ...`")
	}

	// Modify the in-memory config via reflection. Roll back when validation or
	// persistence fails so a rejected value does not linger until restart.
	previous := Cfg
	if err := setReflect(reflect.ValueOf(&Cfg).Elem(), keys, value); err != nil {
		Cfg = previous
		return err
	}
	if err := Validate(&Cfg); err != nil {
		Cfg = previous
		return fmt.Errorf("config is not valid after change, won't write to config file: %w", err)
	}
	if err := Cfg.Save(); err != nil {
		Cfg = previous
		return err
	}
	return nil
}

func setReflect(v reflect.Value, keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("missing key")
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	key := keys[0]

	if v.Kind() == reflect.Map {
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		if len(keys) > 1 {
			return fmt.Errorf("value of key '%s' is not object, can't update its attribute", key)
		}
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported map value type")
		}
		v.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value))
		return nil
	}

	if v.Kind() != reflect.Struct {
		return fmt.Errorf("value of key '%s' is not object, can't update its attribute", key)
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		tag := strings.Split(field.Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if tag != key {
			continue
		}
		fv := v.Field(i)
		if len(keys) == 1 {
			switch fv.Kind() {
			case reflect.String:
				fv.SetString(value)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid integer value %q for %s", value, key)
				}
				fv.SetInt(n)
			case reflect.Bool:
				b, err := strconv.ParseBool(value)
				if err != nil {
					return fmt.Errorf("invalid boolean value %q for %s", value, key)
				}
				fv.SetBool(b)
			case reflect.Slice:
				// Only simple comma-separated values are supported here.
				parts := []string{}
				for _, s := range strings.Split(value, ",") {
					if s = strings.TrimSpace(s); s != "" {
						parts = append(parts, s)
					}
				}
				slice := reflect.MakeSlice(fv.Type(), len(parts), len(parts))
				for j, s := range parts {
					slice.Index(j).SetString(s)
				}
				fv.Set(slice)
			default:
				return fmt.Errorf("unsupported config value type for %s", key)
			}
			return nil
		}
		return setReflect(fv, keys[1:], value)
	}
	return fmt.Errorf("config key %q not found", key)
}

// GetValue returns the value at the given TOML key path.
func GetValue(keys []string) (any, error) {
	data, err := os.ReadFile(ConfigFilePath)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var cur any = doc
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return map[string]any{}, nil
		}
		v, ok := m[key]
		if !ok {
			return map[string]any{}, nil
		}
		cur = v
	}
	return cur, nil
}
