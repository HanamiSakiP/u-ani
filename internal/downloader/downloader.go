// Package downloader contains the supported RPC/WebAPI clients.
package downloader

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

// Status mirrors the upstream bgmi download status values.
type Status int

const (
	NotDownloading Status = iota
	Downloading
	Done
	Error
	NotFound
)

func (s Status) String() string {
	switch s {
	case NotDownloading:
		return "not_downloading"
	case Downloading:
		return "downloading"
	case Done:
		return "done"
	case Error:
		return "error"
	case NotFound:
		return "not_found"
	default:
		return "unknown"
	}
}

// Service is a wrapped downloader RPC client.
type Service interface {
	AddDownload(url, savePath string) (string, error)
	GetStatus(id string) (Status, error)
	GetFiles(id string) ([]string, error)
	RemoveDownload(id string) error
	CheckConfig()
}

var defaultTrackers = []string{
	"http://t.nyaatracker.com/announce",
	"http://tracker.kamigami.org:2710/announce",
	"http://share.camoe.cn:8080/announce",
	"http://opentracker.acgnx.se/announce",
	"http://anidex.moe:6969/announce",
	"http://t.acg.rip:6699/announce",
	"https://tr.bangumi.moe:9696/announce",
	"udp://tr.bangumi.moe:6969/announce",
	"http://open.acgtracker.com:1096/announce",
	"udp://tracker.opentrackr.org:1337/announce",
}

// AddTracker injects the default announce list into magnet links.
func AddTracker(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "magnet" {
		return raw
	}
	q := u.Query()
	set := map[string]bool{}
	for _, tr := range defaultTrackers {
		set[tr] = true
	}
	for _, tr := range q["tr"] {
		set[tr] = true
	}
	trackers := make([]string, 0, len(set))
	for tr := range set {
		trackers = append(trackers, tr)
	}
	sort.Strings(trackers)
	q["tr"] = trackers
	u.RawQuery = q.Encode()
	return u.String()
}

// New creates the configured downloader.
func New(delegate string) (Service, error) {
	switch delegate {
	case "aria2-rpc":
		return newAria2(), nil
	case "transmission-rpc":
		return newTransmission(), nil
	case "qbittorrent-webapi":
		return newQBittorrent(), nil
	case "deluge-rpc":
		return newDeluge(), nil
	case "noop":
		return &Noop{}, nil
	default:
		return nil, fmt.Errorf("can't load download delegate %s", delegate)
	}
}

func normalizeBaseURL(host string, port int, scheme string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/")
	}
	if scheme == "" {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

var _ = config.Cfg
