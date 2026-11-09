package season

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var cnNumMap = map[string]int{
	"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9,
	"十": 10, "十一": 11, "十二": 12, "十三": 13, "十四": 14, "十五": 15,
}

func cnToInt(s string) int {
	if v, ok := cnNumMap[s]; ok {
		return v
	}
	if strings.Count(s, "十") == 1 {
		parts := strings.Split(s, "十")
		if len(parts) == 2 && len([]rune(parts[0])) <= 1 && len([]rune(parts[1])) <= 1 {
			tens := 1
			if parts[0] != "" {
				tens = cnNumMap[parts[0]]
			}
			ones := 0
			if parts[1] != "" {
				ones = cnNumMap[parts[1]]
			}
			return tens*10 + ones
		}
	}
	return 1
}

type seasonPattern struct {
	re        *regexp.Regexp
	converter bool
}

var patterns = []seasonPattern{
	{regexp.MustCompile(`第\s*(\d+)\s*季`), false},
	{regexp.MustCompile(`第\s*([一二三四五六七八九十]+)\s*季`), true},
	{regexp.MustCompile(`[Ss]eason\s*(\d+)`), false},
	{regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])S(\d+)(?:[^a-zA-Z\d]|$)`), false},
	{regexp.MustCompile(`(\d+)(?:st|nd|rd|th)\s*[Ss]eason`), false},
	{regexp.MustCompile(`Part\s*(\d+)`), false},
}

// ParseSeason extracts a season number from a bangumi name. It returns 1 when
// no season marker is present.
func ParseSeason(name string) int {
	for _, p := range patterns {
		m := p.re.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if p.converter {
			return cnToInt(m[1])
		}
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 1
}

var trailingREs = []*regexp.Regexp{
	regexp.MustCompile(`第\s*\d+\s*季\s*$`),
	regexp.MustCompile(`第\s*[一二三四五六七八九十]+\s*季\s*$`),
	regexp.MustCompile(`[Ss]eason\s*\d+\s*$`),
	regexp.MustCompile(`\d+(?:st|nd|rd|th)\s*[Ss]eason\s*$`),
	regexp.MustCompile(`Part\s*\d+\s*$`),
}

var standaloneS = regexp.MustCompile(`(?i)[Ss](\d+)\s*$`)

func isSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("._-:：/／|｜", r)
}

// StripSeasonSuffix removes a trailing season marker from a name.
func StripSeasonSuffix(name string) string {
	for _, re := range trailingREs {
		loc := re.FindStringIndex(name)
		if loc == nil {
			continue
		}
		start := loc[0]
		for start > 0 {
			r := []rune(name[:start])
			if len(r) == 0 || !isSeparator(r[len(r)-1]) {
				break
			}
			start -= len(string(r[len(r)-1]))
		}
		return strings.TrimSpace(name[:start])
	}

	// "S2" needs look-behind in Python; emulate the boundary check by hand.
	if loc := standaloneS.FindStringSubmatchIndex(name); loc != nil {
		start := loc[0]
		before := []rune(name[:start])
		if len(before) == 0 || !isASCIILetter(before[len(before)-1]) {
			for start > 0 {
				r := []rune(name[:start])
				if len(r) == 0 || !isSeparator(r[len(r)-1]) {
					break
				}
				start -= len(string(r[len(r)-1]))
			}
			return strings.TrimSpace(name[:start])
		}
	}
	return name
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
