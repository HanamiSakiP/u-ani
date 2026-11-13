package httpserver

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

type kazumiSource struct {
	Rule     string `json:"rule"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	Error    string `json:"error,omitempty"`
	HasMatch bool   `json:"has_match"`
}

// handleKazumiSources searches every installed rule in parallel. This mirrors
// Kazumi's "开始观看" flow: after opening a subject, show all installed source
// rules that can find it.
func handleKazumiSources(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keyword string `json:"keyword"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	keyword := strings.TrimSpace(body.Keyword)
	if keyword == "" {
		writeError(w, http.StatusBadRequest, "keyword is required")
		return
	}
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	rules, err := repo.ListInstalled()
	if err != nil {
		writeKazumiError(w, err)
		return
	}

	type result struct {
		rule  kazumi.InstalledRule
		items []kazumi.SearchItem
		err   error
	}
	results := make([]result, 0, len(rules))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, installed := range rules {
		installed := installed
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rule, err := repo.LoadRule(installed.Name)
			if err != nil {
				mu.Lock()
				results = append(results, result{rule: installed, err: err})
				mu.Unlock()
				return
			}
			items, err := kazumi.Search(rule, keyword)
			mu.Lock()
			results = append(results, result{rule: installed, items: items, err: err})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		return strings.ToLower(results[i].rule.Name) < strings.ToLower(results[j].rule.Name)
	})

	normalizedKeyword := normalizeSourceName(keyword)
	out := make([]kazumiSource, 0, len(results))
	for _, item := range results {
		if item.err != nil {
			out = append(out, kazumiSource{Rule: item.rule.Name, Error: item.err.Error()})
			continue
		}
		for index, search := range item.items {
			if index >= 5 {
				break
			}
			name := search.Name
			out = append(out, kazumiSource{
				Rule: item.rule.Name, Name: name, Source: search.Src,
				HasMatch: sourceNameMatches(normalizedKeyword, name),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

func normalizeSourceName(value string) string {
	var builder strings.Builder
	for _, ch := range strings.ToLower(value) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || (ch >= 0x4e00 && ch <= 0x9fff) {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func sourceNameMatches(keyword, name string) bool {
	normalized := normalizeSourceName(name)
	if keyword == "" || normalized == "" {
		return false
	}
	return strings.Contains(normalized, keyword) || strings.Contains(keyword, normalized)
}
