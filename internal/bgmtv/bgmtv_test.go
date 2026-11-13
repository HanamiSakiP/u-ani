package bgmtv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSubjectID(t *testing.T) {
	cases := map[string]int{
		"253":                             253,
		"https://bgm.tv/subject/253":      253,
		"https://bgm.tv/subject/253/":     253,
		"http://bangumi.tv/subject/12345": 12345,
		"bgm.tv/subject/42?foo=bar":       42,
	}
	for input, want := range cases {
		got, err := ParseSubjectID(input)
		if err != nil {
			t.Fatalf("ParseSubjectID(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseSubjectID(%q) = %d, want %d", input, got, want)
		}
	}
	for _, input := range []string{"", "abc", "-1", "https://bgm.tv/subject/x"} {
		if _, err := ParseSubjectID(input); err == nil {
			t.Fatalf("ParseSubjectID(%q) unexpectedly succeeded", input)
		}
	}
}

func TestSubjectSearchNames(t *testing.T) {
	subject := Subject{
		Name:   "涼宮ハルヒの憂鬱",
		NameCN: "凉宫春日的忧郁",
		Infobox: []Infobox{
			{Key: "别名", Value: json.RawMessage(`["Haruhi", {"v": "凉宫春日的憂鬱"}]`)},
			{Key: "英文名", Value: json.RawMessage(`"The Melancholy of Haruhi Suzumiya"`)},
			{Key: "导演", Value: json.RawMessage(`"Tatsuya Ishihara"`)},
		},
	}
	names := subject.SearchNames()
	want := []string{
		"涼宮ハルヒの憂鬱",
		"凉宫春日的忧郁",
		"Haruhi",
		"凉宫春日的憂鬱",
		"The Melancholy of Haruhi Suzumiya",
	}
	if len(names) != len(want) {
		t.Fatalf("SearchNames() = %#v, want %#v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("SearchNames()[%d] = %q, want %q", i, names[i], name)
		}
	}
}

func TestFetchSubjectAndCalendar(t *testing.T) {
	var sawUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUserAgent = r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/v0/subjects/253":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      253,
				"url":     "https://bgm.tv/subject/253",
				"name":    "涼宮ハルヒの憂鬱",
				"name_cn": "凉宫春日的忧郁",
				"images":  map[string]string{"large": "https://example.com/cover.jpg"},
			})
		case "/calendar":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"weekday": map[string]any{"en": "Sun", "cn": "星期日", "ja": "日曜日", "id": 7},
				"items":   []map[string]any{{"id": 253, "name": "涼宮ハルヒの憂鬱"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBase := APIBase
	APIBase = server.URL
	defer func() { APIBase = oldBase }()

	subject, err := FetchSubject(253)
	if err != nil {
		t.Fatal(err)
	}
	if subject.NameCN != "凉宫春日的忧郁" || subject.CoverURL() != "https://example.com/cover.jpg" {
		t.Fatalf("unexpected subject: %#v", subject)
	}
	if sawUserAgent == "" {
		t.Fatal("bgm.tv request did not set a User-Agent")
	}
	if day := WeekdayForSubject(253); day != "Sun" {
		t.Fatalf("WeekdayForSubject() = %q, want Sun", day)
	}
}

func TestParseNextCalendar(t *testing.T) {
	raw := json.RawMessage(`{
		"1": [{"subject": {"id": 1, "name": "Monday", "nameCN": "周一", "images": {"large": "https://example.com/1.jpg"}}}],
		"7": [{"subject": {"id": 7, "name": "Sunday", "nameCN": "周日"}}]
	}`)
	days, err := parseNextCalendar(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 7 {
		t.Fatalf("len(days) = %d", len(days))
	}
	if days[0].Weekday.En != "Mon" || len(days[0].Items) != 1 || days[0].Items[0].ID != 1 {
		t.Fatalf("Monday = %#v", days[0])
	}
	if days[0].Items[0].NameCN != "周一" || days[0].Items[0].Images.Large != "https://example.com/1.jpg" {
		t.Fatalf("Monday item = %#v", days[0].Items[0])
	}
	if days[6].Weekday.En != "Sun" || days[6].Items[0].ID != 7 {
		t.Fatalf("Sunday = %#v", days[6])
	}
}

func TestOfficialAPISelection(t *testing.T) {
	old := APIBase
	defer func() { APIBase = old }()

	APIBase = "https://api.bgm.tv"
	if got := subjectAPIURL(253); got != "https://next.bgm.tv/p1/subjects/253" {
		t.Errorf("subjectAPIURL = %q", got)
	}
	if got := calendarAPIURL(); got != "https://next.bgm.tv/p1/calendar" {
		t.Errorf("calendarAPIURL = %q", got)
	}

	APIBase = "http://127.0.0.1:8080"
	if got := subjectAPIURL(253); got != "http://127.0.0.1:8080/v0/subjects/253" {
		t.Errorf("custom subjectAPIURL = %q", got)
	}
	if got := calendarAPIURL(); got != "http://127.0.0.1:8080/calendar" {
		t.Errorf("custom calendarAPIURL = %q", got)
	}
}

func TestSubjectSearchNamesFromP1Info(t *testing.T) {
	subject := Subject{
		Name:   "カウボーイビバップ",
		NameCN: "星际牛仔",
		Info: `{{Infobox animanga/TVAnime
|中文名= 星际牛仔
|别名={
[Cowboy Bebop]
[宇宙牛仔]
[赏金猎人]
}
|英文名= Cowboy Bebop
|话数= 26
}}`,
	}
	names := subject.SearchNames()
	for _, want := range []string{"カウボーイビバップ", "星际牛仔", "Cowboy Bebop", "宇宙牛仔", "赏金猎人"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("SearchNames() = %v, missing %q", names, want)
		}
	}
	if len(names) != 5 {
		t.Errorf("SearchNames() = %v, want 5 unique names", names)
	}
}

func TestParseCollectionTypes(t *testing.T) {
	ids, err := ParseCollectionTypes([]string{"在看", "watched,3", "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 {
		t.Fatalf("ParseCollectionTypes() = %v, want all 5", ids)
	}
	normalized, err := NormalizeCollectionTypes([]string{"watching", "想看"})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 || normalized[0] != "wish" || normalized[1] != "watching" {
		t.Fatalf("NormalizeCollectionTypes() = %v", normalized)
	}
	if _, err := ParseCollectionTypes([]string{"invalid"}); err == nil {
		t.Fatal("expected invalid collection type to fail")
	}
}

func TestBangumiAccountAndCollections(t *testing.T) {
	var sawAuth string
	var postedType float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/v0/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "username": "tester"})
		case r.URL.Path == "/v0/users/tester/collections":
			if got := r.URL.Query().Get("subject_type"); got != "2" {
				t.Errorf("subject_type = %q, want 2", got)
			}
			limit := r.URL.Query().Get("limit")
			offset := r.URL.Query().Get("offset")
			if limit != "2" {
				t.Errorf("limit = %q, want 2", limit)
			}
			data := []map[string]any{
				{
					"subject_id": 101,
					"type":       3,
					"ep_status":  4,
					"updated_at": "2025-01-01T00:00:00Z",
					"subject": map[string]any{
						"id":      101,
						"name":    "Subject 101",
						"name_cn": "条目 101",
					},
				},
				{
					"subject_id": 102,
					"type":       2,
					"subject": map[string]any{
						"id":      102,
						"name":    "Subject 102",
						"name_cn": "条目 102",
					},
				},
			}
			if offset == "2" {
				data = []map[string]any{{
					"subject_id": 103,
					"type":       3,
					"subject": map[string]any{
						"id":      103,
						"name":    "Subject 103",
						"name_cn": "条目 103",
					},
				}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":   data,
				"total":  3,
				"limit":  2,
				"offset": 0,
			})
		case r.URL.Path == "/v0/users/-/collections/101" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			postedType, _ = body["type"].(float64)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBase := APIBase
	APIBase = server.URL
	defer func() { APIBase = oldBase }()

	user, err := GetCurrentUser("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "tester" {
		t.Fatalf("username = %q", user.Username)
	}
	if sawAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", sawAuth)
	}

	collections, err := GetCollections("tester", "secret-token", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(collections) != 3 {
		t.Fatalf("len(collections) = %d, want 3", len(collections))
	}
	if collections[0].SubjectID != 101 || collections[0].Subject.NameCN != "条目 101" || collections[0].EpStatus != 4 {
		t.Fatalf("first collection = %#v", collections[0])
	}
	if collections[1].SubjectID != 102 || collections[1].Type != 2 {
		t.Fatalf("second collection = %#v", collections[1])
	}
	if collections[2].SubjectID != 103 {
		t.Fatalf("third collection = %#v", collections[2])
	}
	if collections[0].Subject.URL == "" {
		t.Fatal("collection subject URL was not filled in")
	}

	if err := UpdateCollection(101, "secret-token", CollectionWatching); err != nil {
		t.Fatal(err)
	}
	if postedType != 3 {
		t.Fatalf("posted collection type = %v, want 3", postedType)
	}
}

func TestFetchRecommended(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recommend" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("tag") != "喜剧" {
			t.Errorf("tag = %q", r.URL.Query().Get("tag"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": 101, "name": "Test", "nameCN": "测试", "rating": map[string]any{"score": 7.8, "rank": 12, "total": 100}},
				{"subject": map[string]any{"id": 102, "name": "Wrapped", "name_cn": "包装", "rating": map[string]any{"score": 6.5}}},
			},
		})
	}))
	defer server.Close()
	oldBase := APIBase
	APIBase = server.URL
	defer func() { APIBase = oldBase }()

	subjects, err := FetchRecommended(12, 0, "喜剧")
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 2 {
		t.Fatalf("len(subjects) = %d", len(subjects))
	}
	if subjects[0].NameCN != "测试" || subjects[0].Rating.Score != 7.8 || subjects[0].URL == "" {
		t.Fatalf("first subject = %#v", subjects[0])
	}
	if subjects[1].NameCN != "包装" || subjects[1].Rating.Score != 6.5 {
		t.Fatalf("second subject = %#v", subjects[1])
	}
}

func TestParseSubjectInfoKeepsFieldsAfterEmptyValues(t *testing.T) {
	info := `{{Infobox animanga/TVAnime
|中文名= 测试
|别名={
[Test Alias]
[另一个别名]
}
|话数= 12
|放送开始= 2026年10月3日
|放送星期= 星期六
|官方网站= https://example.com/
|在线播放平台= 
|播放结束= 
|导演= 测试导演
|脚本= 测试脚本
|动画制作= 测试制作
}}`
	fields := ParseSubjectInfo(info)
	values := map[string]string{}
	for _, field := range fields {
		values[field.Key] = field.Value
	}
	if values["话数"] != "12" || values["导演"] != "测试导演" || values["动画制作"] != "测试制作" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if values["别名"] != "Test Alias / 另一个别名" {
		t.Fatalf("alias = %q", values["别名"])
	}
	if _, ok := values["在线播放平台"]; ok {
		t.Fatalf("empty field should be skipped: %#v", fields)
	}
}

func TestSearchSubjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search/subject/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": 1,
			"list": []map[string]any{{
				"id": 101, "name": "Test", "name_cn": "测试", "air_date": "2026-10-01",
				"images": map[string]string{"large": "https://lain.bgm.tv/x.jpg"},
				"rating": map[string]any{"score": 7.2},
			}},
		})
	}))
	defer server.Close()
	oldBase := APIBase
	APIBase = server.URL
	defer func() { APIBase = oldBase }()
	subjects, err := SearchSubjects("测试", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0].NameCN != "测试" || subjects[0].Date != "2026-10-01" {
		t.Fatalf("subjects = %#v", subjects)
	}
}

func TestDecodeCommentPageShapes(t *testing.T) {
	cases := map[string]string{
		"list":     `{"list":[{"comment":"好看","updatedAt":1700000000}],"total":1}`,
		"data":     `{"data":[{"comment":"好看"}],"total":1}`,
		"bare":     `[{"comment":"好看","updated":1700000000}]`,
		"result":   `{"result":{"comments":[{"content":"好看","updated_at":1700000000}]}}`,
		"envelope": `{"code":200,"result":{"list":[{"comment":"好看"}]}}`,
	}
	for name, raw := range cases {
		page, err := decodeCommentPage([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(page.List) != 1 || page.List[0].Comment != "好看" {
			t.Fatalf("%s: page = %#v", name, page)
		}
	}
	if _, err := decodeCommentPage([]byte(`{"code":500,"error":"boom"}`)); err == nil {
		t.Fatal("expected error for rejected response")
	}
}

func TestImagesUnmarshalShapes(t *testing.T) {
	cases := map[string]string{
		"object": `{"large":"https://lain.bgm.tv/l.jpg","medium":"https://lain.bgm.tv/m.jpg"}`,
		"string": `"https://lain.bgm.tv/s.jpg"`,
		"alias":  `{"image":"https://lain.bgm.tv/i.jpg"}`,
	}
	for name, raw := range cases {
		var images Images
		if err := json.Unmarshal([]byte(raw), &images); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if images.Large == "" {
			t.Fatalf("%s: images = %#v", name, images)
		}
	}
}

func TestSearchSubjectsPrefersResultsWithCovers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/subject/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"list": []map[string]any{{"id": 1, "name": "No Cover"}},
			})
		case r.URL.Path == "/v0/search/subjects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": 2, "name": "With Cover",
					"images": map[string]string{"large": "https://lain.bgm.tv/c.jpg"},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldBase := APIBase
	APIBase = server.URL
	defer func() { APIBase = oldBase }()
	subjects, err := SearchSubjects("test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 || subjects[0].CoverURL() == "" {
		t.Fatalf("subjects = %#v", subjects)
	}
}
