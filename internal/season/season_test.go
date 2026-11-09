package season

import "testing"

func TestParseSeason(t *testing.T) {
	cases := []struct {
		name   string
		season int
	}{
		{"进击的巨人 第二季", 2},
		{"某某某 第十二季", 12},
		{"某某某 第三季", 3},
		{"王者天下 第二十一季", 21},
		{"银河英雄传说 第三十二季", 32},
		{"进击的巨人 第2季", 2},
		{"名侦探柯南 第15季", 15},
		{"Attack on Titan Season 3", 3},
		{"Attack on Titan season2", 2},
		{"Title S02", 2},
		{"Title S3", 3},
		{"Attack on Titan 2nd Season", 2},
		{"Title 3rd season", 3},
		{"名侦探柯南", 1},
		{"进击的巨人", 1},
		{"", 1},
		{"某某某 Part 2", 2},
		{"PSYCHO-PASS", 1},
	}
	for _, tc := range cases {
		if got := ParseSeason(tc.name); got != tc.season {
			t.Errorf("ParseSeason(%q) = %d, want %d", tc.name, got, tc.season)
		}
	}
}

func TestStripSeasonSuffix(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"相反的你和我 第二季", "相反的你和我"},
		{"进击的巨人 第2季", "进击的巨人"},
		{"Attack on Titan Season 3", "Attack on Titan"},
		{"Title S02", "Title"},
		{"PSYCHO-PASS", "PSYCHO-PASS"},
		{"名侦探柯南", "名侦探柯南"},
	}
	for _, tc := range cases {
		if got := StripSeasonSuffix(tc.name); got != tc.want {
			t.Errorf("StripSeasonSuffix(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
