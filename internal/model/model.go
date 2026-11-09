package model

import "strings"

// Episode is a torrent or direct download entry.
type Episode struct {
	Title         string `json:"title"`
	Download      string `json:"download"`
	Episode       int    `json:"episode"`
	Time          int64  `json:"time"`
	SubtitleGroup string `json:"subtitle_group,omitempty"`
	Name          string `json:"name,omitempty"`
}

func (e Episode) ContainsAnyWords(keywords []string) bool {
	title := strings.ToLower(e.Title)
	for _, k := range keywords {
		if strings.Contains(title, k) {
			return true
		}
	}
	return false
}

// RemoveDuplicatedEpisode keeps the first entry for each episode number,
// matching BGmi's behavior of preserving the newest/best first result.
func RemoveDuplicatedEpisode(items []Episode) []Episode {
	seen := map[int]bool{}
	out := make([]Episode, 0, len(items))
	for _, e := range items {
		if seen[e.Episode] {
			continue
		}
		seen[e.Episode] = true
		out = append(out, e)
	}
	return out
}

type SubtitleGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type WebsiteBangumi struct {
	ID            string          `json:"id"`
	UpdateDay     string          `json:"update_day"`
	Name          string          `json:"name"`
	Status        int             `json:"status"`
	SubtitleGroup []SubtitleGroup `json:"subtitle_group"`
	Cover         string          `json:"cover"`
	Episodes      []Episode       `json:"episodes"`
}
