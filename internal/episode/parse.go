// Package episode implements the same episode-number heuristics used by
// anime-episode-parser 0.0.15, plus BGmi's extra fallbacks.
package episode

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	episodeWithBrackets = regexp.MustCompile(`[【\[]E?(\d+)\s?(?:END)?[】\]]`)
	episodeZH           = regexp.MustCompile(`第?\s?(\d{1,4})\s?[話话集]`)
	episodeAllZH        = regexp.MustCompile(`第([^第]*?)[話话集]`)
	episodeOnlyNum      = regexp.MustCompile(`^([\d]{2,})$`)
	episodeWithEP       = regexp.MustCompile(`EP([\d]{2,})`)
	episodeWithSeason   = regexp.MustCompile(`\bS\d{1,2}E(\d{1,4})\b`)
	episodeSeasonWords  = regexp.MustCompile(`\bSeason\s+\d{1,2}\s+Episode\s+(\d{1,4})\b`)
	episodeSeasonX      = regexp.MustCompile(`\b\d{1,2}x(\d{1,4})\b`)

	episodeRange     = regexp.MustCompile(`[^sS]([\d]{2,})\s?[-~]\s?([\d]{2,})`)
	episodeRange2    = regexp.MustCompile(`\[(\d+)-(\d+)]`)
	episodeRangeZH   = regexp.MustCompile(`[第]([\d]{2,})\s?[-~]\s?([\d]{2,})\s?[話话集]`)
	episodeRangeAll1 = regexp.MustCompile(`[全]([\d-]*?)[話话集]`)
	episodeRangeAll2 = regexp.MustCompile(`第?(\d*)\s?[-~]\s(\d*)[話话集]`)

	episodeOVAOAD  = regexp.MustCompile(`([\d]{2,})\s?\((?:OVA|OAD)\)]`)
	episodeWithVer = regexp.MustCompile(`[【\[\s](\d+)\s? *v\d(?:END)?[】\]\s]`)

	releaseYear = regexp.MustCompile(`[\[【(（]\s*(?:19|20)\d{2}\s*[\]】)）]`)

	separatorEpisode = regexp.MustCompile(`[★☆](?:第\s*)?(0*[1-9]\d{0,2})`)
)

var cnNum = map[rune]int{
	'〇': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	'零': 0, '壹': 1, '贰': 2, '叁': 3, '肆': 4, '伍': 5, '陆': 6, '柒': 7, '捌': 8, '玖': 9,
	'貮': 2, '两': 2,
}

var cnUnit = map[rune]int{
	'十': 10, '拾': 10, '百': 100, '佰': 100, '千': 1000, '仟': 1000, '万': 10000, '萬': 10000,
}

func chineseToArabic(cn string) (int, bool) {
	unit := 0
	var digits []int
	runes := []rune(cn)
	for i := len(runes) - 1; i >= 0; i-- {
		ch := runes[i]
		if u, ok := cnUnit[ch]; ok {
			unit = u
			if unit == 10000 || unit == 100000000 {
				digits = append(digits, unit)
				unit = 1
			}
			continue
		}
		d, ok := cnNum[ch]
		if !ok {
			return 0, false
		}
		if unit != 0 {
			d *= unit
			unit = 0
		}
		digits = append(digits, d)
	}
	if unit == 10 {
		digits = append(digits, 10)
	}
	val, tmp := 0, 0
	for i := len(digits) - 1; i >= 0; i-- {
		x := digits[i]
		if x == 10000 || x == 100000000 {
			val += tmp * x
			tmp = 0
		} else {
			tmp += x
		}
	}
	val += tmp
	return val, true
}

func getRealEpisode(values []string) int {
	min := 0
	for i, s := range values {
		n, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		if i == 0 || n < min {
			min = n
		}
	}
	return min
}

// parseRaw returns (start, count). count is 0 when unknown.
func parseRaw(title string) (int, int, bool) {
	if m := episodeRangeAll1.FindStringSubmatch(title); m != nil && m[1] != "" {
		// A complete collection ("全12话") has no single episode number.
		return 0, 0, false
	}
	if m := episodeRangeAll2.FindStringSubmatch(title); m != nil && m[1] != "" && m[2] != "" {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a < b {
			return a, b - a + 1, true
		}
	}
	if m := episodeRange.FindStringSubmatch(title); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a < b {
			return a, b - a + 1, true
		}
	}
	if m := episodeRange2.FindStringSubmatch(title); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a < b {
			return a, b - a + 1, true
		}
	}
	if m := episodeRangeZH.FindStringSubmatch(title); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a < b {
			return a, b - a, true
		}
	}
	if m := episodeZH.FindStringSubmatch(title); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, 1, true
		}
	}
	if m := episodeAllZH.FindStringSubmatch(title); m != nil && m[1] != "" {
		if n, ok := chineseToArabic(m[1]); ok {
			return n, 1, true
		}
	}
	if m := episodeWithVer.FindStringSubmatch(title); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, 1, true
		}
	}
	if m := episodeWithBrackets.FindAllStringSubmatch(title, -1); len(m) > 0 {
		vals := make([]string, 0, len(m))
		for _, x := range m {
			vals = append(vals, x[1])
		}
		return getRealEpisode(vals), 1, true
	}
	if m := episodeWithEP.FindAllStringSubmatch(title, -1); len(m) > 0 {
		vals := make([]string, 0, len(m))
		for _, x := range m {
			vals = append(vals, x[1])
		}
		return getRealEpisode(vals), 1, true
	}
	for _, re := range []*regexp.Regexp{episodeWithSeason, episodeSeasonWords, episodeSeasonX} {
		if m := re.FindAllStringSubmatch(title, -1); len(m) > 0 {
			vals := make([]string, 0, len(m))
			for _, x := range m {
				vals = append(vals, x[1])
			}
			return getRealEpisode(vals), 1, true
		}
	}

	rest := []int{}
	spare := 0
	normalized := strings.NewReplacer("★", " ", "[", " ", "【", ",").Replace(title)
	for _, token := range strings.Split(normalized, " ") {
		for _, re := range []*regexp.Regexp{
			episodeZH, episodeAllZH, episodeWithBrackets, episodeOnlyNum, episodeWithEP,
			episodeRange, episodeRange2, episodeRangeAll1, episodeRangeAll2, episodeOVAOAD,
			episodeWithVer, episodeWithSeason, episodeSeasonWords, episodeSeasonX,
		} {
			m := re.FindStringSubmatch(token)
			if m == nil || len(m) < 2 || m[1] == "" {
				continue
			}
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if n > 1000 {
				spare = n
			} else {
				rest = append(rest, n)
			}
		}
	}
	if len(rest) > 0 {
		min := rest[0]
		for _, n := range rest[1:] {
			if n < min {
				min = n
			}
		}
		return min, 1, true
	}
	if spare > 0 {
		return spare, 1, true
	}
	return 0, 0, false
}

// Parse returns the episode number found in an anime release title.
func Parse(title string) int {
	title = releaseYear.ReplaceAllString(title, "")
	start, count, ok := parseRaw(title)
	if ok && count == 1 {
		return start
	}
	// BGmi fallback for titles like "★07(先行版)".
	for _, loc := range separatorEpisode.FindAllStringSubmatchIndex(title, -1) {
		if len(loc) < 4 {
			continue
		}
		raw := title[loc[2]:loc[3]]
		n, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		end := loc[1]
		if end < len(title) {
			next := title[end]
			if !strings.ContainsRune(" \t(（)）]】★☆_.", rune(next)) {
				continue
			}
		}
		return n
	}
	return 0
}
