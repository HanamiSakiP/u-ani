package model

import "testing"

func TestRemoveDuplicatedEpisode(t *testing.T) {
	input := []Episode{
		{Name: "1", Title: "1", Download: "1", Episode: 1},
		{Name: "1", Title: "1", Download: "1", Episode: 1},
		{Name: "2", Title: "2", Download: "2", Episode: 2},
		{Name: "2", Title: "2", Download: "2", Episode: 2},
		{Name: "3", Title: "3", Download: "3", Episode: 3},
		{Name: "5", Title: "5", Download: "5", Episode: 5},
	}
	got := RemoveDuplicatedEpisode(input)
	if len(got) != 4 {
		t.Fatalf("RemoveDuplicatedEpisode returned %d items, want 4", len(got))
	}
	seen := map[int]bool{}
	for _, e := range got {
		seen[e.Episode] = true
	}
	for _, want := range []int{1, 2, 3, 5} {
		if !seen[want] {
			t.Errorf("missing episode %d", want)
		}
	}
}

func TestContainsAnyWords(t *testing.T) {
	if !(Episode{Title: "a b c"}).ContainsAnyWords([]string{"a"}) {
		t.Error("expected lowercase keyword to match")
	}
	if !(Episode{Title: "A B c"}).ContainsAnyWords([]string{"a", "b"}) {
		t.Error("expected case-insensitive match")
	}
	if (Episode{Title: "a b c"}).ContainsAnyWords([]string{"d", "ab"}) {
		t.Error("unexpected match")
	}
}
