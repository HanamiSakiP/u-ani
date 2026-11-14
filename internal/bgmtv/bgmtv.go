// Package bgmtv is a small client for the public bgm.tv (Bangumi) API.
package bgmtv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

// APIBase can be overridden in tests.
var APIBase = "https://api.bgm.tv"

// publicNextMirror is a public next.bgm.tv mirror used as a last-resort
// fallback. In many networks api.bgm.tv / next.bgm.tv are unreachable, and
// the signed Kazumi mirror rejects public comment requests, so without this
// fallback the 吐槽 tab can never load.
var publicNextMirror = "https://next.bangumi.lat"

const userAgent = "uani/1.0.1 (https://github.com/HanamiSakiP/u-ani)"

var (
	subjectIDPattern   = regexp.MustCompile(`(?i)(?:bgm\.tv|bangumi\.tv)/subject/(\d+)`)
	wikiBracketPattern = regexp.MustCompile(`\[([^\[\]]+)\]`)
)

// Images contains the cover URLs returned by bgm.tv.
type Images struct {
	Large  string `json:"large"`
	Common string `json:"common"`
	Medium string `json:"medium"`
	Small  string `json:"small"`
	Grid   string `json:"grid"`
}

// UnmarshalJSON accepts both the standard image object and the older/string
// shapes used by some mirrors so search results always keep their covers.
func (images *Images) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		images.Large = normalizeImageHost(value)
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	pick := func(keys ...string) string {
		for _, key := range keys {
			value, ok := raw[key]
			if !ok {
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err == nil && text != "" {
				return text
			}
		}
		return ""
	}
	images.Large = normalizeImageHost(pick("large", "Large", "original", "image", "cover", "url"))
	images.Common = normalizeImageHost(pick("common", "Common"))
	images.Medium = normalizeImageHost(pick("medium", "Medium"))
	images.Small = normalizeImageHost(pick("small", "Small"))
	images.Grid = normalizeImageHost(pick("grid", "Grid", "thumbnail"))
	return nil
}

// normalizeImageHost rewrites cover URLs served by the next API mirror to the
// official Bangumi image host so the existing image proxy and acceleration
// settings keep working.
func normalizeImageHost(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"https://lain.bangumi.lat/", "http://lain.bangumi.lat/"} {
		if strings.HasPrefix(raw, prefix) {
			return "https://lain.bgm.tv/" + strings.TrimPrefix(raw, prefix)
		}
	}
	return raw
}

// Infobox is one infobox entry. Its value can be a string, an object or a
// list of either, so it is decoded lazily.
type Infobox struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// Rating is the Bangumi rating summary.
type Rating struct {
	Score float64 `json:"score"`
	Rank  int     `json:"rank"`
	Total int     `json:"total"`
}

// Tag is one Bangumi subject tag.
type Tag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Platform describes the broadcast platform returned by the next API.
type Platform struct {
	Type   string `json:"type"`
	TypeCN string `json:"typeCN"`
	Alias  string `json:"alias"`
}

// Person is a staff member or voice actor.
type Person struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Type     int      `json:"type"`
	Images   Images   `json:"images"`
	Career   []string `json:"career"`
	Relation string   `json:"relation"`
	Eps      string   `json:"eps"`
}

// Character is a subject character and its voice actors.
type Character struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Relation string   `json:"relation"`
	Type     int      `json:"type"`
	Images   Images   `json:"images"`
	Actors   []Person `json:"actors"`
}

// Relation is a related subject.
type Relation struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Relation string `json:"relation"`
	Type     int    `json:"type"`
	Images   Images `json:"images"`
}

// CommentUser is the author of a subject comment.
type CommentUser struct {
	ID       int             `json:"id"`
	Username string          `json:"username"`
	Nickname string          `json:"nickname"`
	Avatar   json.RawMessage `json:"avatar"`
}

// Comment is one subject comment ("吐槽").
type Comment struct {
	User      CommentUser `json:"user"`
	Rate      int         `json:"rate"`
	Comment   string      `json:"comment"`
	Content   string      `json:"content"`
	UpdatedAt int64       `json:"updatedAt"`
	Updated   int64       `json:"updated"`
	CreatedAt int64       `json:"createdAt"`
}

// CommentPage is a page of subject comments.
type CommentPage struct {
	List  []Comment `json:"list"`
	Total int       `json:"total"`
}

// InfoField is one parsed infobox key/value pair.
type InfoField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Subject is a bgm.tv subject (anime, book, game, ...).
type Subject struct {
	ID          int       `json:"id"`
	URL         string    `json:"url"`
	Type        int       `json:"type"`
	Name        string    `json:"name"`
	NameCN      string    `json:"name_cn"`
	NameCNCamel string    `json:"nameCN"`
	Date        string    `json:"date"`
	Eps         int       `json:"eps"`
	Images      Images    `json:"images"`
	Infobox     []Infobox `json:"infobox"`
	Summary     string    `json:"summary"`
	Rating      Rating    `json:"rating"`
	Tags        []Tag     `json:"tags"`
	MetaTags    []string  `json:"metaTags"`
	Platform    Platform  `json:"platform"`
	// Info is the raw infobox wikitext returned by the next/p1 API used by
	// Kazumi. It is parsed for aliases when Infobox is absent.
	Info string `json:"info"`
}

// CalendarItem is an entry in the bgm.tv weekly calendar.
type CalendarItem struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	NameCN   string   `json:"name_cn"`
	Images   Images   `json:"images"`
	Info     string   `json:"info"`
	Rating   Rating   `json:"rating"`
	MetaTags []string `json:"metaTags"`
	Platform Platform `json:"platform"`
	Watchers int      `json:"watchers"`
}

// CalendarDay is one weekday of the bgm.tv calendar.
type CalendarDay struct {
	Weekday struct {
		En string `json:"en"`
		Cn string `json:"cn"`
		Ja string `json:"ja"`
		ID int    `json:"id"`
	} `json:"weekday"`
	Items []CalendarItem `json:"items"`
}

// ParseSubjectID accepts a numeric subject id or a bgm.tv / bangumi.tv URL.
func ParseSubjectID(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty bgm.tv subject")
	}
	if match := subjectIDPattern.FindStringSubmatch(value); len(match) == 2 {
		id, err := strconv.Atoi(match[1])
		if err != nil || id <= 0 {
			return 0, fmt.Errorf("invalid bgm.tv subject id %q", match[1])
		}
		return id, nil
	}
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid bgm.tv subject %q; use a subject id or https://bgm.tv/subject/<id>", value)
	}
	return id, nil
}

// FetchSubject fetches a subject by id from the bgm.tv API.
func FetchSubject(id int) (*Subject, error) {
	var subject Subject
	if err := getJSON(subjectAPIURL(id), &subject); err != nil {
		return nil, err
	}
	if subject.ID == 0 {
		subject.ID = id
	}
	normalizeSubject(&subject)
	if subject.Name == "" && subject.NameCN == "" {
		return nil, fmt.Errorf("bgm.tv subject %d has no title", id)
	}
	return &subject, nil
}

// FetchCalendar fetches the current weekly calendar from bgm.tv. The official
// next API (used by Kazumi) returns an object keyed by weekday number, while
// the legacy API returns an array.
func FetchCalendar() ([]CalendarDay, error) {
	var raw json.RawMessage
	if err := getJSON(calendarAPIURL(), &raw); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var days []CalendarDay
		if err := json.Unmarshal(raw, &days); err != nil {
			return nil, err
		}
		return days, nil
	}
	return parseNextCalendar(raw)
}

func subjectAPIURL(id int) string {
	if isPublicAPIBase() {
		return fmt.Sprintf("https://next.bgm.tv/p1/subjects/%d", id)
	}
	return fmt.Sprintf("%s/v0/subjects/%d", strings.TrimRight(APIBase, "/"), id)
}

func calendarAPIURL() string {
	if isPublicAPIBase() {
		return "https://next.bgm.tv/p1/calendar"
	}
	return strings.TrimRight(APIBase, "/") + "/calendar"
}

func isPublicAPIBase() bool {
	u, err := url.Parse(APIBase)
	if err != nil {
		return false
	}
	return u.Host == "api.bgm.tv" || u.Host == "next.bgm.tv"
}

func parseNextCalendar(raw json.RawMessage) ([]CalendarDay, error) {
	var payload map[string][]struct {
		Subject struct {
			ID       int      `json:"id"`
			Name     string   `json:"name"`
			NameCN   string   `json:"nameCN"`
			Images   Images   `json:"images"`
			Info     string   `json:"info"`
			Rating   Rating   `json:"rating"`
			MetaTags []string `json:"metaTags"`
			Platform Platform `json:"platform"`
		} `json:"subject"`
		Watchers int `json:"watchers"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	weekdays := []struct {
		en, cn, ja string
		id         int
	}{
		{"Mon", "星期一", "月曜日", 1},
		{"Tue", "星期二", "火曜日", 2},
		{"Wed", "星期三", "水曜日", 3},
		{"Thu", "星期四", "木曜日", 4},
		{"Fri", "星期五", "金曜日", 5},
		{"Sat", "星期六", "土曜日", 6},
		{"Sun", "星期日", "日曜日", 7},
	}
	days := make([]CalendarDay, 0, 7)
	for index, weekday := range weekdays {
		day := CalendarDay{}
		day.Weekday.En = weekday.en
		day.Weekday.Cn = weekday.cn
		day.Weekday.Ja = weekday.ja
		day.Weekday.ID = weekday.id
		key := strconv.Itoa(index + 1)
		for _, item := range payload[key] {
			day.Items = append(day.Items, CalendarItem{
				ID:       item.Subject.ID,
				Name:     item.Subject.Name,
				NameCN:   item.Subject.NameCN,
				Images:   item.Subject.Images,
				Info:     item.Subject.Info,
				Rating:   item.Subject.Rating,
				MetaTags: item.Subject.MetaTags,
				Platform: item.Subject.Platform,
				Watchers: item.Watchers,
			})
		}
		days = append(days, day)
	}
	return days, nil
}

// User is the Bangumi account associated with an Access Token.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   Images `json:"avatar"`
}

// Collection is one subject in a user's Bangumi collection list. Type uses the
// official values: 1 wish, 2 watched, 3 watching, 4 on hold, 5 dropped.
type Collection struct {
	SubjectID   int     `json:"subject_id"`
	SubjectType int     `json:"subject_type"`
	Type        int     `json:"type"`
	EpStatus    int     `json:"ep_status"`
	VolStatus   int     `json:"vol_status"`
	UpdatedAt   string  `json:"updated_at"`
	Subject     Subject `json:"subject"`
}

// Collection type values used by the Bangumi collection API.
const (
	CollectionWish     = 1
	CollectionWatched  = 2
	CollectionWatching = 3
	CollectionOnHold   = 4
	CollectionDropped  = 5
)

var collectionTypeAliases = map[string]int{
	"1":             CollectionWish,
	"wish":          CollectionWish,
	"plan_to_watch": CollectionWish,
	"plantowatch":   CollectionWish,
	"想看":            CollectionWish,
	"2":             CollectionWatched,
	"watched":       CollectionWatched,
	"collect":       CollectionWatched,
	"看过":            CollectionWatched,
	"3":             CollectionWatching,
	"watching":      CollectionWatching,
	"do":            CollectionWatching,
	"在看":            CollectionWatching,
	"4":             CollectionOnHold,
	"on_hold":       CollectionOnHold,
	"onhold":        CollectionOnHold,
	"hold":          CollectionOnHold,
	"搁置":            CollectionOnHold,
	"5":             CollectionDropped,
	"dropped":       CollectionDropped,
	"drop":          CollectionDropped,
	"抛弃":            CollectionDropped,
}

// NormalizeCollectionTypes validates collection type names and returns the
// canonical spelling used by the sync configuration, e.g. ["watching"].
func NormalizeCollectionTypes(values []string) ([]string, error) {
	ids, err := ParseCollectionTypes(values)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, CollectionTypeName(id))
	}
	return out, nil
}

// ParseCollectionTypes converts user supplied collection type names ("在看",
// "watching", "3", ...) into the numeric Bangumi values. "all" selects every
// collection bucket.
func ParseCollectionTypes(values []string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	add := func(id int) {
		if id < CollectionWish || id > CollectionDropped || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			key := strings.ToLower(strings.TrimSpace(part))
			if key == "" {
				continue
			}
			if key == "all" || key == "全部" || key == "*" {
				for id := CollectionWish; id <= CollectionDropped; id++ {
					add(id)
				}
				continue
			}
			id, ok := collectionTypeAliases[key]
			if !ok {
				return nil, fmt.Errorf("unknown Bangumi collection type %q (use wish, watched, watching, on_hold, dropped or all)", part)
			}
			add(id)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	sort.Ints(out)
	return out, nil
}

// CollectionTypeName returns the canonical name of a Bangumi collection type.
func CollectionTypeName(value int) string {
	switch value {
	case CollectionWish:
		return "wish"
	case CollectionWatched:
		return "watched"
	case CollectionWatching:
		return "watching"
	case CollectionOnHold:
		return "on_hold"
	case CollectionDropped:
		return "dropped"
	default:
		return fmt.Sprintf("unknown_%d", value)
	}
}

// GetCurrentUser resolves the account behind an Access Token.
func GetCurrentUser(token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("Bangumi Access Token should not be empty")
	}
	var user User
	if err := getJSONAuth(authAPIBase()+"/v0/me", token, &user); err != nil {
		if strings.Contains(err.Error(), "HTTP 401") {
			return nil, fmt.Errorf("Bangumi Access Token is invalid or expired: %w", err)
		}
		return nil, err
	}
	if strings.TrimSpace(user.Username) == "" {
		return nil, fmt.Errorf("Bangumi user information is incomplete")
	}
	return &user, nil
}

// GetCollections fetches every collection of the given subject type for a
// user. subjectType uses Bangumi's subject type (2 = anime); pass 0 for all.
func GetCollections(username, token string, subjectType, pageLimit int) ([]Collection, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("Bangumi username should not be empty")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Bangumi Access Token should not be empty")
	}
	if pageLimit <= 0 || pageLimit > 50 {
		pageLimit = 50
	}
	base := authAPIBase() + "/v0/users/" + url.PathEscape(username) + "/collections"
	var all []Collection
	seen := map[int]bool{}
	for offset := 0; ; {
		query := url.Values{}
		if subjectType > 0 {
			query.Set("subject_type", strconv.Itoa(subjectType))
		}
		query.Set("limit", strconv.Itoa(pageLimit))
		query.Set("offset", strconv.Itoa(offset))
		var page struct {
			Data   []Collection `json:"data"`
			Total  int          `json:"total"`
			Limit  int          `json:"limit"`
			Offset int          `json:"offset"`
		}
		if err := getJSONAuth(base+"?"+query.Encode(), token, &page); err != nil {
			return nil, err
		}
		if len(page.Data) == 0 {
			break
		}
		for _, item := range page.Data {
			item.normalize()
			if item.SubjectID == 0 || seen[item.SubjectID] {
				continue
			}
			seen[item.SubjectID] = true
			all = append(all, item)
		}
		offset += len(page.Data)
		effectiveLimit := pageLimit
		if page.Limit > 0 {
			effectiveLimit = page.Limit
		}
		if page.Total > 0 && offset >= page.Total {
			break
		}
		if len(page.Data) < effectiveLimit {
			break
		}
	}
	return all, nil
}

// UpdateCollection changes the collection type of one subject. This mirrors
// Kazumi's Bangumi write path and is used by optional progress/status sync.
func UpdateCollection(subjectID int, token string, collectionType int) error {
	if subjectID <= 0 {
		return fmt.Errorf("invalid Bangumi subject id %d", subjectID)
	}
	if collectionType < CollectionWish || collectionType > CollectionDropped {
		return fmt.Errorf("invalid Bangumi collection type %d", collectionType)
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("Bangumi Access Token should not be empty")
	}
	target := fmt.Sprintf("%s/v0/users/-/collections/%d", authAPIBase(), subjectID)
	return postJSONAuth(target, token, map[string]any{"type": collectionType})
}

// DeleteCollection removes a subject from the user's Bangumi collection.
func DeleteCollection(subjectID int, token string) error {
	if subjectID <= 0 {
		return fmt.Errorf("invalid Bangumi subject id %d", subjectID)
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("Bangumi Access Token should not be empty")
	}
	target := fmt.Sprintf("%s/v0/users/-/collections/%d", authAPIBase(), subjectID)
	err := doJSON(http.MethodDelete, target, nil, token, nil)
	// A subject that is not in the collection is already in the desired state.
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

func (c *Collection) normalize() {
	if c.Subject.ID == 0 {
		c.Subject.ID = c.SubjectID
	}
	if c.SubjectID == 0 {
		c.SubjectID = c.Subject.ID
	}
	if c.Subject.URL == "" && c.Subject.ID > 0 {
		c.Subject.URL = fmt.Sprintf("https://bgm.tv/subject/%d", c.Subject.ID)
	}
}

// authAPIBase selects the endpoint used for authenticated requests. The
// official Kazumi client uses api.bgmapi.com when the Bangumi mirror is
// enabled, and api.bgm.tv for direct/ECH access.
func authAPIBase() string {
	if !isPublicAPIBase() {
		return strings.TrimRight(APIBase, "/")
	}
	mode, _ := kazumi.NormalizeBangumiAcceleration(config.Cfg.Kazumi.BangumiAcceleration)
	if mode == kazumi.BangumiAccelerationMirror {
		return "https://api.bgmapi.com"
	}
	return "https://api.bgm.tv"
}

// WeekdayMap returns subject id -> English weekday ("Mon" ... "Sun") from one
// calendar request. It is used to avoid downloading the calendar once per
// synchronized subject.
func WeekdayMap() map[int]string {
	out := map[int]string{}
	days, err := FetchCalendar()
	if err != nil {
		return out
	}
	for _, day := range days {
		weekday := normalizeWeekday(day.Weekday.En)
		if weekday == "" {
			continue
		}
		for _, item := range day.Items {
			out[item.ID] = weekday
		}
	}
	return out
}

// FetchRecommended returns popular anime (Kazumi recommendation page).
func FetchRecommended(limit, offset int, tag string) ([]Subject, error) {
	if limit <= 0 {
		limit = 24
	}
	if limit > 50 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var raw json.RawMessage
	if err := getJSON(recommendAPIURL(limit, offset, tag), &raw); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	var items []json.RawMessage
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
	} else {
		var envelope struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		items = envelope.Data
	}
	out := make([]Subject, 0, len(items))
	for _, item := range items {
		var wrapper struct {
			Subject Subject `json:"subject"`
		}
		if err := json.Unmarshal(item, &wrapper); err == nil && wrapper.Subject.ID != 0 {
			normalizeSubject(&wrapper.Subject)
			out = append(out, wrapper.Subject)
			continue
		}
		var subject Subject
		if err := json.Unmarshal(item, &subject); err == nil && subject.ID != 0 {
			normalizeSubject(&subject)
			out = append(out, subject)
		}
	}
	return out, nil
}

func recommendAPIURL(limit, offset int, tag string) string {
	query := url.Values{}
	if !isPublicAPIBase() {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("offset", strconv.Itoa(offset))
		if tag != "" {
			query.Set("tag", tag)
		}
		return strings.TrimRight(APIBase, "/") + "/recommend?" + query.Encode()
	}
	mode, _ := kazumi.NormalizeBangumiAcceleration(config.Cfg.Kazumi.BangumiAcceleration)
	if mode == kazumi.BangumiAccelerationMirror {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("offset", strconv.Itoa(offset))
		if tag != "" {
			query.Set("tag", tag)
		}
		return "https://api.kazumi.fyi/kazumi/v1/popular/subjects?" + query.Encode()
	}
	query.Set("type", "2")
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	return "https://next.bgm.tv/p1/trending/subjects?" + query.Encode()
}

func normalizeSubject(subject *Subject) {
	if subject == nil {
		return
	}
	if subject.NameCN == "" && subject.NameCNCamel != "" {
		subject.NameCN = subject.NameCNCamel
	}
	if subject.URL == "" && subject.ID > 0 {
		subject.URL = fmt.Sprintf("https://bgm.tv/subject/%d", subject.ID)
	}
	if subject.Type == 0 {
		subject.Type = 2
	}
}

// FetchCharacters returns the characters of a subject.
func FetchCharacters(id int) ([]Character, error) {
	var out []Character
	if err := getJSON(v0SubjectURL(id, "/characters"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FetchStaff returns the production staff of a subject.
func FetchStaff(id int) ([]Person, error) {
	var out []Person
	if err := getJSON(v0SubjectURL(id, "/persons"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FetchRelations returns related subjects.
func FetchRelations(id int) ([]Relation, error) {
	var out []Relation
	if err := getJSON(v0SubjectURL(id, "/subjects"), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FetchComments returns the first page of subject comments. The official
// next API and configured mirrors are queried concurrently so one slow or
// incompatible endpoint cannot block the others.
func FetchComments(id, offset, limit int) (*CommentPage, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	candidates := []string{}
	add := func(rawURL string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		candidates = append(candidates, rawURL)
	}
	if isPublicAPIBase() {
		active := strings.TrimRight(kazumi.ActiveBangumiMirror(), "/")
		if active != "" {
			add(fmt.Sprintf("%s/p1/subjects/%d/comments?limit=%d&offset=%d", active, id, limit, offset))
			add(fmt.Sprintf("%s/comments/%d?limit=%d&offset=%d", active, id, limit, offset))
		}
		add(fmt.Sprintf("https://next.bgm.tv/p1/subjects/%d/comments?limit=%d&offset=%d", id, limit, offset))
		add(fmt.Sprintf("https://api.bgm.tv/comments/%d?limit=%d&offset=%d", id, limit, offset))
		for _, mirror := range kazumi.BangumiMirrors() {
			mirror = strings.TrimRight(mirror, "/")
			add(fmt.Sprintf("%s/p1/subjects/%d/comments?limit=%d&offset=%d", mirror, id, limit, offset))
			add(fmt.Sprintf("%s/comments/%d?limit=%d&offset=%d", mirror, id, limit, offset))
		}
		for _, mirror := range kazumi.BangumiSearchMirrors() {
			mirror = strings.TrimRight(mirror, "/")
			add(fmt.Sprintf("%s/p1/subjects/%d/comments?limit=%d&offset=%d", mirror, id, limit, offset))
			add(fmt.Sprintf("%s/comments/%d?limit=%d&offset=%d", mirror, id, limit, offset))
		}
		add(fmt.Sprintf("%s/p1/subjects/%d/comments?limit=%d&offset=%d", strings.TrimRight(publicNextMirror, "/"), id, limit, offset))
	} else {
		add(commentsAPIURL(id, offset, limit))
		add(fmt.Sprintf("%s/p1/subjects/%d/comments?limit=%d&offset=%d", strings.TrimRight(APIBase, "/"), id, limit, offset))
		add(fmt.Sprintf("%s/comments/%d?limit=%d&offset=%d", strings.TrimRight(APIBase, "/"), id, limit, offset))
	}
	seen := map[string]bool{}
	urls := make([]string, 0, len(candidates))
	for _, rawURL := range candidates {
		if seen[rawURL] {
			continue
		}
		seen[rawURL] = true
		urls = append(urls, rawURL)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("Bangumi comments endpoint is not configured")
	}
	type result struct {
		page *CommentPage
		err  error
	}
	results := make(chan result, len(urls))
	for _, rawURL := range urls {
		rawURL := rawURL
		go func() {
			timeout := 5 * time.Second
			if strings.Contains(rawURL, "next.bgm.tv") || strings.Contains(rawURL, "api.bgm.tv") {
				timeout = 6500 * time.Millisecond
			}
			var raw json.RawMessage
			if err := getJSONDirectTimeout(rawURL, &raw, timeout); err != nil {
				results <- result{err: err}
				return
			}
			page, err := decodeCommentPage(raw)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{page: page}
		}()
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var lastErr error
	var emptyPage *CommentPage
	for range urls {
		select {
		case item := <-results:
			if item.err != nil {
				lastErr = item.err
				continue
			}
			if item.page == nil {
				continue
			}
			// Some mirrors answer unsupported comment endpoints with an
			// empty 200 response. Keep waiting for a candidate that actually
			// has comments and only fall back to the empty page at the end.
			if len(item.page.List) > 0 {
				return item.page, nil
			}
			if emptyPage == nil {
				emptyPage = item.page
			}
		case <-timer.C:
			if emptyPage != nil {
				return emptyPage, nil
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("Bangumi comments request timed out")
			}
			return nil, lastErr
		}
	}
	if emptyPage != nil {
		return emptyPage, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Bangumi comments returned no data")
	}
	return nil, lastErr
}

// decodeCommentPage accepts the shapes returned by the official next API and
// the various mirrors. Some endpoints wrap the list in an envelope, while
// older APIs return a bare array.
func decodeCommentPage(raw json.RawMessage) (*CommentPage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("comments endpoint returned no comment data")
	}
	var list []Comment
	if err := json.Unmarshal(raw, &list); err == nil {
		normalizeComments(list)
		return &CommentPage{List: list, Total: len(list)}, nil
	}
	var payload struct {
		List     []Comment `json:"list"`
		Data     []Comment `json:"data"`
		Comments []Comment `json:"comments"`
		Result   *struct {
			List     []Comment `json:"list"`
			Data     []Comment `json:"data"`
			Comments []Comment `json:"comments"`
			Total    *int      `json:"total"`
		} `json:"result"`
		Total *int   `json:"total"`
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.Code >= 400 || payload.Error != "" {
		msg := strings.TrimSpace(payload.Error)
		if msg == "" {
			msg = "request rejected"
		}
		return nil, fmt.Errorf("comments endpoint rejected the request: %s", msg)
	}
	list = firstCommentList(payload.List, payload.Data, payload.Comments)
	total := payload.Total
	if payload.Result != nil {
		if len(list) == 0 {
			list = firstCommentList(payload.Result.List, payload.Result.Data, payload.Result.Comments)
		}
		if total == nil {
			total = payload.Result.Total
		}
	}
	// At least one recognized field must be present; otherwise this is an
	// unrelated JSON document (for example an HTML error rendered by a mirror).
	if payload.List == nil && payload.Data == nil && payload.Comments == nil && payload.Total == nil && payload.Result == nil {
		return nil, fmt.Errorf("comments endpoint returned no comment data")
	}
	normalizeComments(list)
	out := &CommentPage{List: list}
	if total != nil {
		out.Total = *total
	}
	return out, nil
}

func firstCommentList(lists ...[]Comment) []Comment {
	for _, list := range lists {
		if len(list) > 0 {
			return list
		}
	}
	for _, list := range lists {
		if list != nil {
			return list
		}
	}
	return nil
}

func normalizeComments(list []Comment) {
	for i := range list {
		if list[i].Comment == "" {
			list[i].Comment = list[i].Content
		}
		if list[i].UpdatedAt == 0 {
			if list[i].Updated != 0 {
				list[i].UpdatedAt = list[i].Updated
			} else {
				list[i].UpdatedAt = list[i].CreatedAt
			}
		}
	}
}

// GetCollection returns the current user's collection type for one subject.
func GetCollection(username string, subjectID int, token string) (int, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, fmt.Errorf("Bangumi username is not configured")
	}
	if subjectID <= 0 {
		return 0, fmt.Errorf("invalid Bangumi subject id %d", subjectID)
	}
	target := fmt.Sprintf("%s/v0/users/%s/collections/%d", authAPIBase(), url.PathEscape(username), subjectID)
	var payload struct {
		Type int `json:"type"`
	}
	if err := getJSONAuth(target, token, &payload); err != nil {
		return 0, err
	}
	return payload.Type, nil
}

func commentsAPIURL(id, offset, limit int) string {
	if !isPublicAPIBase() {
		return fmt.Sprintf("%s/comments/%d?limit=%d&offset=%d", strings.TrimRight(APIBase, "/"), id, limit, offset)
	}
	return fmt.Sprintf("https://next.bgm.tv/p1/subjects/%d/comments?limit=%d&offset=%d", id, limit, offset)
}

func getJSONDirect(rawURL string, out any) error {
	return getJSONDirectTimeout(rawURL, out, 4500*time.Millisecond)
}

func getJSONDirectTimeout(rawURL string, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("bgm.tv API HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func v0SubjectURL(id int, suffix string) string {
	base := strings.TrimRight(APIBase, "/")
	if isPublicAPIBase() {
		base = "https://api.bgm.tv"
	}
	return fmt.Sprintf("%s/v0/subjects/%d%s", base, id, suffix)
}

// ParseSubjectInfo converts the p1 infobox wikitext into display fields.
func ParseSubjectInfo(info string) []InfoField {
	var out []InfoField
	var blockKey string
	var blockValues []string
	flush := func() {
		if blockKey != "" && len(blockValues) > 0 {
			out = append(out, InfoField{Key: blockKey, Value: strings.Join(blockValues, " / ")})
		}
		blockKey = ""
		blockValues = nil
	}
	for _, line := range strings.Split(info, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if blockKey != "" {
			if strings.HasPrefix(trimmed, "}") {
				flush()
				continue
			}
			if values := bracketValues(trimmed); len(values) > 0 {
				blockValues = append(blockValues, values...)
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "|"))
		index := strings.Index(body, "=")
		if index <= 0 {
			continue
		}
		key := strings.TrimSpace(body[:index])
		value := strings.TrimSpace(body[index+1:])
		if key == "" || key == "中文名" {
			continue
		}
		if strings.HasPrefix(value, "{") {
			inner := strings.TrimPrefix(value, "{")
			if end := strings.LastIndex(inner, "}"); end >= 0 {
				out = append(out, InfoField{Key: key, Value: strings.Join(bracketValues(inner[:end]), " / ")})
			} else {
				blockKey = key
				blockValues = bracketValues(inner)
			}
			continue
		}
		// An empty field (e.g. "|播放结束=") is common in Bangumi infoboxes.
		// It must not start a multi-line block, otherwise every following field
		// would be swallowed.
		if value == "" {
			continue
		}
		out = append(out, InfoField{Key: key, Value: strings.Join(bracketValues(value), " / ")})
	}
	flush()
	return out
}

// WeekdayForSubject returns the English weekday ("Mon" ... "Sun") for a
// subject if it appears in the bgm.tv calendar.
func WeekdayForSubject(subjectID int) string {
	days, err := FetchCalendar()
	if err != nil {
		return ""
	}
	for _, day := range days {
		for _, item := range day.Items {
			if item.ID == subjectID {
				if wd := normalizeWeekday(day.Weekday.En); wd != "" {
					return wd
				}
			}
		}
	}
	return ""
}

// PreferredName returns the Chinese title when available for zh locales and
// the original title otherwise.
func (s *Subject) PreferredName(lang string) string {
	if strings.HasPrefix(strings.ToLower(lang), "zh") && strings.TrimSpace(s.NameCN) != "" {
		return strings.TrimSpace(s.NameCN)
	}
	if strings.TrimSpace(s.Name) != "" {
		return strings.TrimSpace(s.Name)
	}
	return strings.TrimSpace(s.NameCN)
}

// CoverURL returns the best available cover image URL.
func (s *Subject) CoverURL() string {
	for _, candidate := range []string{s.Images.Large, s.Images.Common, s.Images.Medium, s.Images.Small, s.Images.Grid} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// SearchNames returns the title, Chinese title, and known aliases used to
// match torrents from the configured data source.
func (s *Subject) SearchNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	add(s.Name)
	add(s.NameCN)
	for _, box := range s.Infobox {
		if !isAliasKey(box.Key) {
			continue
		}
		for _, value := range infoboxValues(box.Value) {
			add(value)
		}
	}
	if strings.TrimSpace(s.Info) != "" {
		for _, value := range infoAliases(s.Info) {
			add(value)
		}
	}
	return out
}

// infoAliases extracts the title/alias fields from the next/p1 API's raw
// infobox wikitext. For example:
//
//	|别名={
//	[Cowboy Bebop]
//	[宇宙牛仔]
//	}
func infoAliases(info string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(info, "\n") {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			if strings.HasPrefix(trimmed, "}") {
				inBlock = false
				continue
			}
			if trimmed != "" {
				out = append(out, bracketValues(trimmed)...)
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "|"))
		index := strings.Index(body, "=")
		if index <= 0 {
			continue
		}
		key := strings.TrimSpace(body[:index])
		value := strings.TrimSpace(body[index+1:])
		if !isAliasKey(key) {
			continue
		}
		if strings.HasPrefix(value, "{") {
			inner := strings.TrimPrefix(value, "{")
			if end := strings.LastIndex(inner, "}"); end >= 0 {
				out = append(out, bracketValues(inner[:end])...)
				continue
			}
			inBlock = true
			for _, item := range bracketValues(inner) {
				out = append(out, item)
			}
			continue
		}
		if value == "" {
			inBlock = true
			continue
		}
		out = append(out, bracketValues(value)...)
	}
	return out
}

func bracketValues(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "{" || value == "}" {
		return nil
	}
	matches := wikiBracketPattern.FindAllStringSubmatch(value, -1)
	if len(matches) > 0 {
		out := make([]string, 0, len(matches))
		for _, match := range matches {
			if len(match) > 1 {
				out = append(out, strings.TrimSpace(match[1]))
			}
		}
		return out
	}
	value = strings.Trim(value, "{}[] ")
	if value == "" {
		return nil
	}
	return []string{value}
}

func isAliasKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{"别名", "别称", "英文名", "日文名", "罗马字", "中文名"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func infoboxValues(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return splitAlias(text)
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err == nil {
		var out []string
		for _, item := range items {
			switch value := item.(type) {
			case string:
				out = append(out, splitAlias(value)...)
			case map[string]any:
				for _, key := range []string{"v", "value", "name"} {
					if text, ok := value[key].(string); ok && text != "" {
						out = append(out, splitAlias(text)...)
						break
					}
				}
			}
		}
		return out
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, key := range []string{"v", "value", "name"} {
			if text, ok := object[key].(string); ok && text != "" {
				return splitAlias(text)
			}
		}
	}
	return nil
}

func splitAlias(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func normalizeWeekday(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mon", "monday", "星期一":
		return "Mon"
	case "tue", "tuesday", "星期二":
		return "Tue"
	case "wed", "wednesday", "星期三":
		return "Wed"
	case "thu", "thursday", "星期四":
		return "Thu"
	case "fri", "friday", "星期五":
		return "Fri"
	case "sat", "saturday", "星期六":
		return "Sat"
	case "sun", "sunday", "星期日", "星期天":
		return "Sun"
	default:
		return ""
	}
}

func getJSON(url string, out any) error {
	return doJSON(http.MethodGet, url, nil, "", out)
}

func getJSONAuth(url, token string, out any) error {
	return doJSON(http.MethodGet, url, nil, token, out)
}

func postJSONAuth(url, token string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return doJSON(http.MethodPost, url, data, token, nil)
}

func doJSON(method, url string, body []byte, token string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	mode, _ := kazumi.NormalizeBangumiAcceleration(config.Cfg.Kazumi.BangumiAcceleration)
	target := kazumi.ApplyBangumiURL(url, mode)
	headers := map[string]string{
		"User-Agent": userAgent,
		"Accept":     "application/json",
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if body != nil {
		headers["Content-Type"] = "application/json"
	}

	var resp *http.Response
	var err error
	if method == http.MethodGet {
		resp, err = kazumi.Get(ctx, target, headers, mode == kazumi.BangumiAccelerationECH)
	} else {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			return err
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err = httpx.Client().Do(req)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("bgm.tv API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode bgm.tv response: %w", err)
	}
	return nil
}

// SearchAPIBase can be overridden by tests.
var SearchAPIBase = "https://api.bgm.tv"

// SearchSubjects searches Bangumi subjects through all available public
// endpoints concurrently. Custom mirrors are included, which allows users to
// point at a mirror that supports search without Kazumi's app signature.
func SearchSubjects(keyword string, limit int) ([]Subject, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []Subject{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 24
	}
	public := isPublicAPIBase()
	base := strings.TrimRight(SearchAPIBase, "/")
	if !public {
		base = strings.TrimRight(APIBase, "/")
	}
	type searchFunc func() ([]Subject, error)
	var jobs []searchFunc
	seenJob := map[string]bool{}
	addJob := func(key string, fn searchFunc) {
		if seenJob[key] {
			return
		}
		seenJob[key] = true
		jobs = append(jobs, fn)
	}
	if public {
		for _, mirror := range kazumi.BangumiSearchMirrors() {
			mirror := strings.TrimRight(mirror, "/")
			if mirror == "" {
				continue
			}
			addJob("v0:"+mirror, func() ([]Subject, error) { return searchSubjectsV0(mirror, keyword, limit) })
			addJob("legacy:"+mirror, func() ([]Subject, error) { return searchSubjectsLegacy(mirror, keyword, limit) })
		}
	} else {
		addJob("v0:"+base, func() ([]Subject, error) { return searchSubjectsV0(base, keyword, limit) })
		addJob("legacy:"+base, func() ([]Subject, error) { return searchSubjectsLegacy(base, keyword, limit) })
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("Bangumi search endpoint is not configured")
	}
	type result struct {
		subjects []Subject
		err      error
	}
	results := make(chan result, len(jobs))
	for _, job := range jobs {
		job := job
		go func() {
			subjects, err := job()
			results <- result{subjects: subjects, err: err}
		}()
	}
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	var lastErr error
	var fallback []Subject
	// Once any mirror returns usable results, don't keep the user waiting for
	// slow mirrors just to find a cover. A short grace period lets a fast
	// cover-bearing response win without turning every coverless result into
	// a multi-second wait.
	var coverGrace *time.Timer
	var coverGraceC <-chan time.Time
	defer func() {
		if coverGrace != nil {
			coverGrace.Stop()
		}
	}()
	for range jobs {
		select {
		case item := <-results:
			if item.err == nil && len(item.subjects) > 0 {
				// A fast mirror may return names without covers. Wait only a
				// moment for a source that includes images so search result
				// cards render correctly.
				if subjectsHaveCovers(item.subjects) {
					return item.subjects, nil
				}
				if fallback == nil {
					fallback = item.subjects
					coverGrace = time.NewTimer(400 * time.Millisecond)
					coverGraceC = coverGrace.C
				}
				continue
			}
			if item.err != nil {
				lastErr = item.err
			}
		case <-coverGraceC:
			return fallback, nil
		case <-timer.C:
			if fallback != nil {
				return fallback, nil
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("Bangumi search timed out")
			}
			return nil, lastErr
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("Bangumi search returned no results")
	}
	return nil, lastErr
}

func subjectsHaveCovers(subjects []Subject) bool {
	for i := range subjects {
		if subjects[i].CoverURL() != "" {
			return true
		}
	}
	return false
}

func searchSubjectsLegacy(base, keyword string, limit int) ([]Subject, error) {
	target := fmt.Sprintf("%s/search/subject/%s?type=2&responseGroup=large&max_results=%d", strings.TrimRight(base, "/"), url.PathEscape(keyword), limit)
	var payload struct {
		List []struct {
			ID      int    `json:"id"`
			URL     string `json:"url"`
			Type    int    `json:"type"`
			Name    string `json:"name"`
			NameCN  string `json:"name_cn"`
			AirDate string `json:"air_date"`
			Images  Images `json:"images"`
			Summary string `json:"summary"`
			Rating  Rating `json:"rating"`
		} `json:"list"`
	}
	if err := getJSONDirect(target, &payload); err != nil {
		return nil, err
	}
	out := make([]Subject, 0, len(payload.List))
	for _, item := range payload.List {
		out = append(out, Subject{
			ID: item.ID, URL: item.URL, Type: item.Type, Name: item.Name,
			NameCN: item.NameCN, Date: item.AirDate, Images: item.Images,
			Summary: item.Summary, Rating: item.Rating,
		})
	}
	return out, nil
}

func searchSubjectsV0(base, keyword string, limit int) ([]Subject, error) {
	target := fmt.Sprintf("%s/v0/search/subjects?limit=%d&offset=0", strings.TrimRight(base, "/"), limit)
	body := map[string]any{
		"keyword": keyword,
		"sort":    "match",
		"filter":  map[string]any{"type": []int{2}},
	}
	var payload struct {
		Data []Subject `json:"data"`
	}
	if err := postJSONDirect(target, body, &payload); err != nil {
		return nil, err
	}
	for i := range payload.Data {
		normalizeSubject(&payload.Data[i])
	}
	return payload.Data, nil
}

func postJSONDirect(rawURL string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("bgm.tv API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
