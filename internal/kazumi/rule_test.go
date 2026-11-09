package kazumi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestXPathRuleSearchAndChapters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			if r.URL.Query().Get("q") != "葬送的芙莉莲" {
				t.Errorf("keyword = %q", r.URL.Query().Get("q"))
			}
			fmt.Fprint(w, `<html><body>
				<div class="item"><h2>葬送的芙莉莲</h2><a href="/show/1">详情</a></div>
				<div class="item"><h2>其他番剧</h2><a href="/show/2">详情</a></div>
			</body></html>`)
		case "/show/1":
			fmt.Fprint(w, `<html><body>
				<div class="road"><a href="/play/1/1">第01集</a><a href="/play/1/2">第02集</a></div>
				<div class="road"><a href="/play/2/1">第01集</a></div>
			</body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rule := &Rule{
		Name:          "test",
		BaseURL:       server.URL + "/",
		SearchURL:     server.URL + "/search?q=@keyword",
		SearchList:    "//div[@class='item']",
		SearchName:    "//h2",
		SearchResult:  "//a",
		ChapterRoads:  "//div[@class='road']",
		ChapterResult: "//a",
	}
	items, err := Search(rule, "葬送的芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "葬送的芙莉莲" || items[0].Src != server.URL+"/show/1" {
		t.Fatalf("unexpected search results: %#v", items)
	}
	roads, err := Chapters(rule, items[0].Src)
	if err != nil {
		t.Fatal(err)
	}
	if len(roads) != 2 || len(roads[0].URLs) != 2 {
		t.Fatalf("unexpected roads: %#v", roads)
	}
	if roads[0].URLs[1] != server.URL+"/play/1/2" || roads[0].Names[0] != "第01集" {
		t.Fatalf("unexpected first road: %#v", roads[0])
	}
}

func TestAPIRuleSearchAndChapters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"list": []map[string]any{{"id": 7, "name": "葬送的芙莉莲"}},
			})
		case "/api/detail":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"roads": []map[string]any{{
					"name": "线路A",
					"eps":  []map[string]any{{"name": "第01集", "url": "/play/7/1"}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var raw = []byte(fmt.Sprintf(`{
		"api": "9", "name": "api-test", "baseURL": %q,
		"searchMode": "api", "chapterMode": "api",
		"searchApiConfig": {
			"request": {"method": "GET", "url": %q, "query": {"wd": "@keyword"}},
			"listPath": "$.list[*]", "namePath": "$.name", "sourcePath": "$.id",
			"sourceTemplate": %q
		},
		"chapterApiConfig": {
			"request": {"method": "GET", "url": %q},
			"roadsPath": "$.roads[*]", "roadNamePath": "$.name",
			"episodesPath": "$.eps[*]", "episodeNamePath": "$.name", "episodeUrlPath": "$.url"
		}
	}`, server.URL+"/", server.URL+"/api/search", server.URL+"/api/detail?source=@source", server.URL+"/api/detail"))
	rule, err := ParseRule(raw)
	if err != nil {
		t.Fatal(err)
	}
	items, err := Search(rule, "芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Src != server.URL+"/api/detail?source=7" {
		t.Fatalf("unexpected API search: %#v", items)
	}
	roads, err := Chapters(rule, "7")
	if err != nil {
		t.Fatal(err)
	}
	if len(roads) != 1 || roads[0].Name != "线路A" || roads[0].URLs[0] != server.URL+"/play/7/1" {
		t.Fatalf("unexpected API chapters: %#v", roads)
	}
}

func TestResolveMediaURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/play/1":
			fmt.Fprint(w, `<html><body><script>var url = "https:\/\/example.com\/video\/1.m3u8";</script></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	rule := &Rule{Name: "test", BaseURL: server.URL}
	media, err := ResolveMediaURL(rule, server.URL+"/play/1")
	if err != nil {
		t.Fatal(err)
	}
	if media != "https://example.com/video/1.m3u8" {
		t.Fatalf("media url = %q", media)
	}
}

func TestJSONPathSubset(t *testing.T) {
	var document any
	if err := json.Unmarshal([]byte(`{"data":{"roads":[{"name":"A","eps":[{"url":"u1"}]}]}}`), &document); err != nil {
		t.Fatal(err)
	}
	roads, err := jsonPathValues(document, "$.data.roads[*]")
	if err != nil || len(roads) != 1 {
		t.Fatalf("roads = %#v, err = %v", roads, err)
	}
	urls, err := jsonPathValues(roads[0], "$.eps[*].url")
	if err != nil || len(urls) != 1 || urls[0] != "u1" {
		t.Fatalf("urls = %#v, err = %v", urls, err)
	}
	if !strings.Contains(fmt.Sprint(firstJSONPath(roads[0], "$.name")), "A") {
		t.Fatalf("name = %v", firstJSONPath(roads[0], "$.name"))
	}
}

func TestResolveMacCMSDirectURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/play/1" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<html><body><script>var player_aaaa={"encrypt":"1","url":"https%3A%2F%2Fcdn.example.com%2Fvideo.m3u8","from":"vxdev"};</script></body></html>`)
	}))
	defer server.Close()
	input, media, err := ResolvePlaybackURL(&Rule{Name: "mac", BaseURL: server.URL}, server.URL+"/play/1")
	if err != nil {
		t.Fatal(err)
	}
	if !media || input != "https://cdn.example.com/video.m3u8" {
		t.Fatalf("input=%q media=%v", input, media)
	}
}

func TestResolveMacCMSPlayerPage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/play/1":
			fmt.Fprint(w, `<html><body><script>var player_aaaa={"encrypt":"0","url":"TOKEN123","from":"vxdev"};</script></body></html>`)
		case "/static/player/vxdev.js":
			fmt.Fprintf(w, `document.getElementById('playerCnt').src = '%s/yunbox/?type=vxdev&vid=' + MacPlayer.Parse + MacPlayer.PlayUrl + '&referer='+ window.location.href;`, server.URL)
		case "/yunbox/":
			fmt.Fprint(w, `<html><body><script>var url = "https://cdn.example.com/video.m3u8";</script></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	input, media, err := ResolvePlaybackURL(&Rule{Name: "mac", BaseURL: server.URL}, server.URL+"/play/1")
	if err != nil {
		t.Fatal(err)
	}
	if !media || input != "https://cdn.example.com/video.m3u8" {
		t.Fatalf("input=%q media=%v", input, media)
	}
}

func TestDecryptVXDevToken(t *testing.T) {
	signature, err := decryptVXDevToken("1UYATgG0npRMfoEYMioXIs/arD4lP1JfCHlBS5ZiOn3MJzcnKxLy0EqieHEvvU1m")
	if err != nil {
		t.Fatal(err)
	}
	if signature != "0bf296ec014ebd374073e9aa54b97a9f" {
		t.Fatalf("signature = %q", signature)
	}
}

func TestResolveVXDevPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/yunbox/vxdev.php":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			_ = r.ParseForm()
			if r.Form.Get("token") != "0bf296ec014ebd374073e9aa54b97a9f" {
				t.Errorf("token = %q", r.Form.Get("token"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "ext": "mp4", "referer": "never", "url": "https://cdn.example.com/video.mp4",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	page := fmt.Sprintf(`<html><script>
var vid = "abc";
var t = "123";
var token = "1UYATgG0npRMfoEYMioXIs/arD4lP1JfCHlBS5ZiOn3MJzcnKxLy0EqieHEvvU1m";
var act = "0";
var play = "1";
$.post("vxdev.php",{});
</script></html>`)
	media, referer := resolveVXDevPage(&Rule{Name: "vx", BaseURL: server.URL}, server.URL+"/yunbox/", []byte(page))
	if media != "https://cdn.example.com/video.mp4" || referer != "-" {
		t.Fatalf("media = %q referer = %q", media, referer)
	}
}

func TestMacCMSPageURLBeatsAdPlayer(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/play/7":
			real := server.URL + "/real"
			encoded := url.QueryEscape(real)
			base64URL := base64.StdEncoding.EncodeToString([]byte(encoded))
			fmt.Fprintf(w, `<html><script>var player_aaaa={"encrypt":2,"url":%q,"from":"lmm"};</script></html>`, base64URL)
		case "/real":
			fmt.Fprint(w, `<html><script>var url="https://cdn.example.com/real.m3u8";</script></html>`)
		case "/static/player/lmm.js":
			fmt.Fprint(w, `MacPlayer.Html = '<iframe src="https://ads.example.com/ad.mp4"></iframe>';`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	input, media, _, err := ResolvePlayback(&Rule{Name: "test", BaseURL: server.URL}, server.URL+"/play/7")
	if err != nil {
		t.Fatal(err)
	}
	if !media || input != "https://cdn.example.com/real.m3u8" {
		t.Fatalf("input=%q media=%v", input, media)
	}
}
