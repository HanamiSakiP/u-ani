package episode

import "testing"

var episodeCases = []struct {
	title   string
	episode int
}{
	{
		"[YMDR][哥布林殺手][Goblin Slayer][2018][01][1080p][AVC][JAP][BIG5][MP4-AAC][繁中]",
		1,
	},
	{
		"I.Became.a.Legend.after.My.10.Year.Long.Last.Stand.S01E01." +
			"When.I.Returned.Home.Id.Become.a.Hero.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG.mkv",
		1,
	},
	{"弱弱老师.Yowayowa.Sensei.S01..1080p.UNCENSORED.ADN.WEB-DL [2026]", 0},
	{"【安達與島村】【第二話】【1080P】【繁體中文】【AVC】", 2},
	{"のんのんびより のんすとっぷ 第02话 BIG5 720p MP4", 2},
	{"OVA 噬血狂袭 Strike the Blood IV [E01][720P][GB][BDrip]", 1},
	{"Kumo Desu ga, Nanika - 01 v2 [1080p][繁体]", 1},
	{"Re Zero Isekai Seikatsu S02 - 17 [Baha][1080p][AVC AAC]", 17},
	// Range titles have no single episode number.
	{"[从零开始的异世界生活 第二季_Re Zero S2][34-35][繁体][720P][MP4]", 0},
	{"Strike The Blood IV][OVA][05-06][1080P][GB][MP4]", 0},
	{"[Legend of the Galactic Heroes 银河英雄传说][全110话+外传+剧场版][MKV][外挂繁中]", 0},
	{"不知道什么片 全二十话", 0},
	{"不知道什么片 全20话", 0},
	{
		"[Lilith-Raws] 如果究极进化的完全沉浸 RPG 比现实还更像垃圾游戏的话 / Full Dive - 02 [Baha][WEB-DL][1080p][AVC AAC][CHT][MP4]",
		2,
	},
	{
		"[Lilith-Raws] 86 - Eighty Six - 01 [Baha][WEB-DL][1080p][AVC AAC][CHT][MP4]",
		1,
	},
	{
		"六四位元字幕组★躲在超市后门抽烟的两人 Super no Ura de Yani Suu Futari" +
			"★07(abema先行版)★1920x1080★AVC AAC MP4★繁体中文",
		7,
	},
	{
		"[7³ACG] 躲在超市后门抽烟的两人/Super no Ura de Yani Suu Futari S01 | " +
			"01-12 [简繁字幕] WEBRip 1080p AV1 AAC 2.0",
		0,
	},
	{"[银色子弹字幕组][名侦探柯南][第1068集 圆谷光彦的侦探笔记][简日双语MP4][1080P]", 1068},
	{"[银色子弹字幕组][名侦探柯南][第1071集 工藤优作的推理秀（前篇）][简日双语MP4][1080P]", 1071},
}

func TestParse(t *testing.T) {
	for _, tc := range episodeCases {
		if got := Parse(tc.title); got != tc.episode {
			t.Errorf("Parse(%q) = %d, want %d", tc.title, got, tc.episode)
		}
	}
}
