package kazumi

import (
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

func TestNormalizeRuleRepository(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"", OfficialRepository, false},
		{OfficialRepository, OfficialRepository, false},
		{strings.TrimRight(OfficialRepository, "/"), OfficialRepository, false},
		{OfficialRepository + "index.json", OfficialRepository, false},
		{"https://example.com/rules", "https://example.com/rules/", false},
		{"https://example.com/rules/", "https://example.com/rules/", false},
		{"https://example.com", "https://example.com/", false},
		{"https://example.com/index.json", "https://example.com/", false},
		{"http://127.0.0.1:8080/rules", "http://127.0.0.1:8080/rules/", false},
		{"https://github.com/Predidit/KazumiRules", "", true},
		{"https://example.com/rules?x=1", "", true},
		{"ftp://example.com/rules", "", true},
		{"https://example.com/file.json", "", true},
	}
	for _, test := range tests {
		got, err := NormalizeRuleRepository(test.input)
		if test.err {
			if err == nil {
				t.Errorf("NormalizeRuleRepository(%q) expected error, got %q", test.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeRuleRepository(%q): %v", test.input, err)
			continue
		}
		if got != test.want {
			t.Errorf("NormalizeRuleRepository(%q) = %q, want %q", test.input, got, test.want)
		}
		if again, err := NormalizeRuleRepository(got); err != nil || again != got {
			t.Errorf("NormalizeRuleRepository(%q) is not idempotent: %q, %v", got, again, err)
		}
	}
}

func TestAccelerationModeNormalization(t *testing.T) {
	for _, value := range []string{"", "direct", "ech", "mirror", "直连", "加密握手", "镜像"} {
		if _, err := NormalizeBangumiAcceleration(value); err != nil {
			t.Errorf("NormalizeBangumiAcceleration(%q): %v", value, err)
		}
		if _, err := NormalizeImageAcceleration(value); err != nil {
			t.Errorf("NormalizeImageAcceleration(%q): %v", value, err)
		}
	}
	if got, _ := NormalizeBangumiAcceleration(""); got != BangumiAccelerationMirror {
		t.Errorf("default bangumi mode = %q", got)
	}
	if got, _ := NormalizeImageAcceleration(""); got != ImageAccelerationECH {
		t.Errorf("default image mode = %q", got)
	}
	for _, value := range []string{"", "ghProxy", "gh-proxy", "GHFAST", "gitcode", "direct"} {
		if _, err := NormalizeRulesMirror(value); err != nil {
			t.Errorf("NormalizeRulesMirror(%q): %v", value, err)
		}
	}
	if _, err := NormalizeImageAcceleration("proxy"); err == nil {
		t.Error("expected invalid acceleration mode to fail")
	}
}

func TestApplyRulesMirror(t *testing.T) {
	official := OfficialRepository + "index.json"
	tests := []struct {
		mode string
		want string
	}{
		{RulesMirrorDirect, official},
		{RulesMirrorGHProxy, ghProxyPrefix + official},
		{RulesMirrorGHFast, ghFastPrefix + official},
		{RulesMirrorGitCode, gitCodePrefix + "index.json"},
	}
	for _, test := range tests {
		if got := ApplyRulesMirror(official, test.mode); got != test.want {
			t.Errorf("ApplyRulesMirror(%q) = %q, want %q", test.mode, got, test.want)
		}
	}
	custom := "https://example.com/rules/index.json"
	if got := ApplyRulesMirror(custom, RulesMirrorGHProxy); got != custom {
		t.Errorf("custom repository should not be mirrored, got %q", got)
	}
}

func TestApplyBangumiURL(t *testing.T) {
	raw := "https://api.bgm.tv/v0/subjects/253?foo=bar"
	if got := ApplyBangumiURL(raw, BangumiAccelerationMirror); got != "https://api.kazumi.fyi/v0/subjects/253?foo=bar" {
		t.Errorf("mirror URL = %q", got)
	}
	if got := ApplyBangumiURL("http://next.bgm.tv/p1/calendar", BangumiAccelerationECH); got != "https://next.bgm.tv/p1/calendar" {
		t.Errorf("ECH URL = %q", got)
	}
	if got := ApplyBangumiURL(raw, BangumiAccelerationDirect); got != raw {
		t.Errorf("direct URL = %q", got)
	}
	custom := "http://127.0.0.1:8080/v0/subjects/1"
	if got := ApplyBangumiURL(custom, BangumiAccelerationMirror); got != custom {
		t.Errorf("custom host should not be mirrored, got %q", got)
	}
}

func TestApplyImageURL(t *testing.T) {
	raw := "https://lain.bgm.tv/pic/cover/l/ab/cd/123.jpg"
	want := "https://wsrv.nl/?url=lain.bgm.tv%2Fpic%2Fcover%2Fl%2Fab%2Fcd%2F123.jpg"
	if got := ApplyImageURL(raw, ImageAccelerationMirror); got != want {
		t.Errorf("mirror image = %q, want %q", got, want)
	}
	gif := "https://lain.bgm.tv/pic/cover/l/ab/cd/123.gif"
	if got := ApplyImageURL(gif, ImageAccelerationMirror); !strings.HasSuffix(got, "&n=-1") {
		t.Errorf("gif mirror = %q, want n=-1", got)
	}
	apiImage := "https://api.bgm.tv/v0/subjects/253/image?x=1"
	if got := ApplyImageURL(apiImage, ImageAccelerationMirror); !strings.HasPrefix(got, "https://wsrv.nl/?url=api.bgm.tv%2Fv0%2Fsubjects%2F253%2Fimage%3Fx%3D1") {
		t.Errorf("api image mirror = %q", got)
	}
	if got := ApplyImageURL("http://lain.bgm.tv/pic/cover/l/x.jpg", ImageAccelerationECH); got != "https://lain.bgm.tv/pic/cover/l/x.jpg" {
		t.Errorf("ECH image = %q", got)
	}
	other := "https://example.com/cover.jpg"
	if got := ApplyImageURL(other, ImageAccelerationMirror); got != other {
		t.Errorf("non-bangumi image = %q", got)
	}
	if !IsBangumiImage(raw) || !IsBangumiImage(apiImage) {
		t.Error("expected Bangumi images to be detected")
	}
	if IsBangumiImage(other) {
		t.Error("did not expect example.com to be a Bangumi image")
	}
}

func TestNextMirrorIsDefaultForP1(t *testing.T) {
	old := config.Cfg.Kazumi
	defer func() { config.Cfg.Kazumi = old }()
	config.Cfg.Kazumi = config.KazumiConfig{}

	mirrors := BangumiMirrors()
	if len(mirrors) == 0 || mirrors[0] != nextBangumiMirror {
		t.Fatalf("default mirrors = %#v, want next.bangumi.lat first", mirrors)
	}
	if got := ActiveBangumiMirror(); got != nextBangumiMirror {
		t.Fatalf("active mirror = %q, want %q", got, nextBangumiMirror)
	}
	if got := ApplyBangumiURL("https://next.bgm.tv/p1/calendar", BangumiAccelerationMirror); got != nextBangumiMirror+"/p1/calendar" {
		t.Fatalf("p1 URL = %q", got)
	}
	if got := ApplyBangumiURL("https://api.bgm.tv/v0/subjects/253", BangumiAccelerationMirror); got != bangumiMirrorDomain+"/v0/subjects/253" {
		t.Fatalf("v0 URL = %q, want detail mirror fallback", got)
	}
	if got := ApplyBangumiURL("https://api.bgm.tv/kazumi/v1/popular/subjects", BangumiAccelerationMirror); got != bangumiMirrorDomain+"/kazumi/v1/popular/subjects" {
		t.Fatalf("kazumi URL = %q, want detail mirror fallback", got)
	}
	for _, mirror := range BangumiSearchMirrors() {
		if mirror == nextBangumiMirror {
			t.Fatalf("search mirrors must skip next.bangumi.lat: %#v", BangumiSearchMirrors())
		}
	}
}
