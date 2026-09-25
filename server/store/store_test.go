package store

// 数据层单元测试：建表迁移、各表 CRUD / 筛选 / 聚合口径、设置项预置。

import (
	"errors"
	"path/filepath"
	"testing"
)

// newTestStore 在临时目录创建测试用 Store（用完自动清理）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWrongWordsCRUDAndFilter(t *testing.T) {
	s := newTestStore(t)

	w1 := &WrongWord{Word: "went", ErrorType: "时态混用",
		OriginalSentence: "I have went.", CorrectedSentence: "I have gone."}
	if err := s.InsertWrongWord(w1); err != nil {
		t.Fatalf("写入错词失败: %v", err)
	}
	if w1.ID == 0 || w1.CreatedAt == "" {
		t.Fatal("写入后应回填 ID 与 CreatedAt")
	}
	w2 := &WrongWord{Word: "children", ErrorType: "名词复数"}
	if err := s.InsertWrongWord(w2); err != nil {
		t.Fatalf("写入错词失败: %v", err)
	}

	// 类型筛选（AND 条件之一）
	got, err := s.ListWrongWords(WrongWordFilter{Type: "时态混用"})
	if err != nil || len(got) != 1 || got[0].Word != "went" {
		t.Fatalf("类型筛选结果不符合预期: %+v err=%v", got, err)
	}
	// 单词模糊搜索
	got, err = s.ListWrongWords(WrongWordFilter{Query: "chil"})
	if err != nil || len(got) != 1 || got[0].Word != "children" {
		t.Fatalf("搜索结果不符合预期: %+v err=%v", got, err)
	}
	// 全量倒序（后写入的 children 在前）
	got, _ = s.ListWrongWords(WrongWordFilter{})
	if len(got) != 2 || got[0].Word != "children" {
		t.Fatalf("倒序结果不符合预期: %+v", got)
	}

	if err := s.DeleteWrongWord(w1.ID); err != nil {
		t.Fatalf("删除错词失败: %v", err)
	}
	if err := s.DeleteWrongWord(w1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除应返回 ErrNotFound, got %v", err)
	}
}

func TestSentenceCRUD(t *testing.T) {
	s := newTestStore(t)

	x := &Sentence{English: "I received your letter yesterday.", Chinese: "我昨天收到了你的信。"}
	if err := s.InsertSentence(x); err != nil {
		t.Fatalf("写入例句失败: %v", err)
	}
	if x.Source != "manual" {
		t.Fatalf("默认来源应为 manual, got %q", x.Source)
	}
	got, err := s.ListSentences("received")
	if err != nil || len(got) != 1 {
		t.Fatalf("搜索例句不符合预期: %+v err=%v", got, err)
	}
	if err := s.DeleteSentence(x.ID); err != nil {
		t.Fatalf("删除例句失败: %v", err)
	}
	got, _ = s.ListSentences("")
	if len(got) != 0 {
		t.Fatalf("删除后应为空列表: %+v", got)
	}
}

func TestDiscriminationReuse(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.FindDiscriminationByWord("receive"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("无记录时应返回 ErrNotFound, got %v", err)
	}
	d1 := &Discrimination{Word: "receive", SynonymsJSON: `["accept","get"]`, ResultJSON: `{"word":"receive"}`}
	if err := s.InsertDiscrimination(d1); err != nil {
		t.Fatalf("写入辨析失败: %v", err)
	}
	// 同词写入第二条，复用查询应命中最新一条
	d2 := &Discrimination{Word: "receive", ResultJSON: `{"word":"receive","v":2}`}
	if err := s.InsertDiscrimination(d2); err != nil {
		t.Fatalf("写入辨析失败: %v", err)
	}
	got, err := s.FindDiscriminationByWord("receive")
	if err != nil || got.ID != d2.ID {
		t.Fatalf("应命中最新记录: got %+v err=%v", got, err)
	}
	n, err := s.DeleteAllDiscriminations()
	if err != nil || n != 2 {
		t.Fatalf("清空辨析历史不符合预期: n=%d err=%v", n, err)
	}
}

func TestCheckHistoryAndTrends(t *testing.T) {
	s := newTestStore(t)

	mk := func(sentence string, errs int) {
		t.Helper()
		if err := s.InsertCheckHistory(&CheckHistory{Sentence: sentence, ErrorCount: errs}); err != nil {
			t.Fatalf("写入检查历史失败: %v", err)
		}
	}
	mk("Perfect sentence.", 0)
	mk("I have went.", 2)
	mk("Another good one.", 0)

	hist, err := s.ListCheckHistory(10)
	if err != nil || len(hist) != 3 {
		t.Fatalf("检查历史列表不符合预期: %+v err=%v", hist, err)
	}

	points, err := s.ListTrendPoints("2000-01-01", "")
	if err != nil || len(points) != 1 {
		t.Fatalf("趋势点不符合预期: %+v err=%v", points, err)
	}
	// 3 句中 2 句 0 错误 → 66.7%
	if points[0].Accuracy != 66.7 {
		t.Fatalf("正确率应为 66.7, got %v", points[0].Accuracy)
	}

	if n, err := s.DeleteAllCheckHistory(); err != nil || n != 3 {
		t.Fatalf("清空检查历史不符合预期: n=%d err=%v", n, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// 预置项：辨析复用开关默认开；api_key 预置存在（空值）
	if v, err := s.GetSetting("reuse_discrimination_history"); err != nil || v != "1" {
		t.Fatalf("辨析复用开关默认应为 1, got %q err=%v", v, err)
	}
	if _, err := s.GetSetting("api_key"); err != nil {
		t.Fatalf("api_key 应预置存在: %v", err)
	}
	// 不存在的键返回空串而非报错
	if v, err := s.GetSetting("no_such_key"); err != nil || v != "" {
		t.Fatalf("不存在的键应返回空串, got %q err=%v", v, err)
	}

	if err := s.SetSetting("api_key", "sk-test"); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	if v, _ := s.GetSetting("api_key"); v != "sk-test" {
		t.Fatalf("设置回写失败: %q", v)
	}
	all, err := s.GetAllSettings()
	if err != nil || all["api_key"] != "sk-test" {
		t.Fatalf("全量读取设置失败: %v err=%v", all, err)
	}
}

// boolPtr 测试辅助：构造 *bool 筛选参数。
func boolPtr(b bool) *bool { return &b }

// TestCompInfoSeedAndLookup 成分说明 seed 预置与精确查询。
func TestCompInfoSeedAndLookup(t *testing.T) {
	s := newTestStore(t)

	// 预置角色可查且四段内容齐全
	c, err := s.GetCompInfo("主语")
	if err != nil || c.Role != "主语" || c.What == "" || c.Why == "" || c.How == "" || c.Demo == "" {
		t.Fatalf("预置成分说明应可查且四段齐全: %+v err=%v", c, err)
	}
	// 从句细分角色同样预置
	if _, err := s.GetCompInfo("让步状语从句"); err != nil {
		t.Fatalf("从句细分角色应预置: %v", err)
	}
	// 未收录角色 → ErrNotFound
	if _, err := s.GetCompInfo("不存在成分"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未收录角色应返回 ErrNotFound, got %v", err)
	}
	// 前后空格应被裁剪后再匹配
	if _, err := s.GetCompInfo(" 主语 "); err != nil {
		t.Fatalf("角色名应容忍首尾空格: %v", err)
	}
}

// TestFavoriteToggle 收藏标记：置位/取消/过滤/幂等/不存在。
func TestFavoriteToggle(t *testing.T) {
	s := newTestStore(t)

	d := &Discrimination{Word: "receive", ResultJSON: `{"word":"receive"}`}
	if err := s.InsertDiscrimination(d); err != nil {
		t.Fatalf("写入辨析失败: %v", err)
	}

	// 默认未收藏；无过滤返回全部
	got, err := s.ListDiscriminations("", "", "", nil)
	if err != nil || len(got) != 1 || got[0].Favorite {
		t.Fatalf("默认应未收藏: %+v err=%v", got, err)
	}

	// 收藏 → favorite=true 过滤命中、false 过滤排除
	if err := s.SetDiscriminationFavorite(d.ID, true); err != nil {
		t.Fatalf("收藏失败: %v", err)
	}
	if got, _ = s.ListDiscriminations("", "", "", boolPtr(true)); len(got) != 1 || !got[0].Favorite {
		t.Fatalf("收藏后 favorite=1 过滤应命中: %+v", got)
	}
	if got, _ = s.ListDiscriminations("", "", "", boolPtr(false)); len(got) != 0 {
		t.Fatalf("收藏后 favorite=0 过滤应为空: %+v", got)
	}

	// 幂等：重复收藏不报错；不存在的 id → ErrNotFound；取消收藏记录保留
	if err := s.SetDiscriminationFavorite(d.ID, true); err != nil {
		t.Fatalf("重复收藏应幂等: %v", err)
	}
	if err := s.SetDiscriminationFavorite(999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的 id 应返回 ErrNotFound, got %v", err)
	}
	if err := s.SetDiscriminationFavorite(d.ID, false); err != nil {
		t.Fatalf("取消收藏失败: %v", err)
	}
	if got, _ = s.ListDiscriminations("", "", "", nil); len(got) != 1 {
		t.Fatalf("取消收藏后记录应保留: %+v", got)
	}

	// 表达收藏同口径
	x := &Expression{Chinese: "知识就是力量。", Recommended: "Knowledge is power."}
	if err := s.InsertExpression(x); err != nil {
		t.Fatalf("写入表达失败: %v", err)
	}
	if err := s.SetExpressionFavorite(x.ID, true); err != nil {
		t.Fatalf("表达收藏失败: %v", err)
	}
	if got, _ := s.ListExpressions(0, boolPtr(true)); len(got) != 1 || !got[0].Favorite {
		t.Fatalf("表达 favorite=1 过滤应命中: %+v", got)
	}
	if got, _ := s.ListExpressions(0, boolPtr(false)); len(got) != 0 {
		t.Fatalf("表达 favorite=0 过滤应为空: %+v", got)
	}
}
