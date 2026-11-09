package controllers

import (
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/db"
)

func TestCacheHasRemoteImagesIgnoresStaffAndComments(t *testing.T) {
	remoteStaff := `[{"cover":"https://lain.bgm.tv/pic/crt/l/staff.jpg"}]`
	cache := &db.BangumiCache{
		Staff:    remoteStaff,
		Comments: `{"items":[{"user":{"cover":"https://lain.bgm.tv/pic/user/l/user.jpg"}}]}`,
	}
	if cacheHasRemoteImages(cache) {
		t.Fatal("staff and comment images should not keep the cache stale")
	}

	cache.Overview = `{"cover":"https://lain.bgm.tv/pic/cover/l/subject.jpg"}`
	if !cacheHasRemoteImages(cache) {
		t.Fatal("remote cover should mark the cache as stale")
	}
}

func TestCacheHasRemoteImagesChecksCharactersAndRelations(t *testing.T) {
	for name, raw := range map[string]string{
		"characters": `[{"cover":"https://lain.bgm.tv/pic/crt/l/character.jpg"}]`,
		"relations":  `[{"cover":"https://lain.bgm.tv/pic/cover/l/related.jpg"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			cache := &db.BangumiCache{}
			if name == "characters" {
				cache.Characters = raw
			} else {
				cache.Relations = raw
			}
			if !cacheHasRemoteImages(cache) {
				t.Fatalf("remote %s image should mark the cache as stale", name)
			}
		})
	}
}
