package controllers

import (
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/db"
)

func TestCacheHasRemoteImagesChecksCoverOnly(t *testing.T) {
	cache := &db.BangumiCache{
		Staff:     `[{"cover":"https://lain.bgm.tv/pic/crt/l/staff.jpg"}]`,
		Comments:  `{"items":[{"user":{"cover":"https://lain.bgm.tv/pic/user/l/user.jpg"}}]}`,
		Characters: `[{"cover":"https://lain.bgm.tv/pic/crt/l/character.jpg"}]`,
		Relations:  `[{"cover":"https://lain.bgm.tv/pic/cover/l/related.jpg"}]`,
	}
	if cacheHasRemoteImages(cache) {
		t.Fatal("non-cover images should not require Kazumi image caching")
	}

	cache = &db.BangumiCache{Overview: `{"cover":"https://lain.bgm.tv/pic/cover/l/subject.jpg"}`}
	if !cacheHasRemoteImages(cache) {
		t.Fatal("remote cover should mark the cache as stale")
	}
}
