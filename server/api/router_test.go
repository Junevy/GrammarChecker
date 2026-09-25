package api

// 处理器层测试：httptest 走完整路由，验证 api/readme.md 评审对齐项——
// 辨析复用短路、POST /api/wrong-words、设置错误码归一、text 长度校验、
// 空字段序列化（omitempty 移除后字段恒存在）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"grammarchecker/server/llm"
	"grammarchecker/server/store"
)

// newTestHandler 构造临时库 + 完整路由的测试服务。
func newTestHandler(t *testing.T) (*httptest.Server, store.Repository) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, llm.NewClient(llm.DefaultConfig()))
	assets := fstest.MapFS{
		"app/vue/index.html": &fstest.MapFile{Data: []byte("<html>test</html>")},
	}
	ts := httptest.NewServer(h.Routes(assets))
	t.Cleanup(ts.Close)
	return ts, s
}

// newTestHandlerWithLLM 在临时库路由基础上注入 fake LLM：
// 本地 httptest 模拟 /chat/completions，固定返回 reply（作为 content 的 JSON 文本）。
// 供 LLM 类接口的全链路测试使用，不产生真实外呼。
func newTestHandlerWithLLM(t *testing.T, reply string) (*httptest.Server, store.Repository) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` +
			strconv.Quote(reply) + `}}]}`))
	}))
	t.Cleanup(fake.Close)

	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, llm.NewClient(llm.Config{
		BaseURL: fake.URL, Model: "fake-model", Timeout: 5 * time.Second, Retries: 0,
	}))
	assets := fstest.MapFS{
		"app/vue/index.html": &fstest.MapFile{Data: []byte("<html>test</html>")},
	}
	ts := httptest.NewServer(h.Routes(assets))
	t.Cleanup(ts.Close)
	return ts, s
}

// call 执行 JSON 请求，返回状态码与原始响应体（空体为 nil）。
func call(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求失败: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rd)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return resp.StatusCode, raw
}

// mustJSON 把响应体解码为对象。
func mustJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应不是 JSON 对象: %s", raw)
	}
	return m
}

// TestDiscriminateReuseShortCircuit 复用短路：命中缓存直接 200（且不需要 API KEY）。
func TestDiscriminateReuseShortCircuit(t *testing.T) {
	ts, s := newTestHandler(t)

	if err := s.InsertDiscrimination(&store.Discrimination{
		Word:         "receive",
		SynonymsJSON: `["accept"]`,
		ResultJSON:   `{"sub":"receive vs accept","core":"receive 表客观收到","words":[]}`,
	}); err != nil {
		t.Fatalf("预置辨析记录失败: %v", err)
	}

	// 命中：api_key 未配置也应 200（短路先于 KEY 校验）
	code, raw := call(t, ts, http.MethodPost, "/api/discriminate",
		map[string]any{"word": "receive", "reuse_history": true})
	if code != http.StatusOK {
		t.Fatalf("复用命中应 200, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	if m["reused"] != true {
		t.Fatalf("应带 reused=true: %v", m)
	}
	if m["sub"] != "receive vs accept" || m["created_at"] == "" {
		t.Fatalf("应返回 LLM 结果结构 + 原记录 created_at: %v", m)
	}

	// 未命中：走占位流程（无 KEY → 400 api_key_missing）
	code, raw = call(t, ts, http.MethodPost, "/api/discriminate",
		map[string]any{"word": "accept", "reuse_history": true})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "api_key_missing" {
		t.Fatalf("未命中应 400 api_key_missing, got %d: %s", code, raw)
	}

	// 未开启复用：同样占位流程
	code, raw = call(t, ts, http.MethodPost, "/api/discriminate",
		map[string]any{"word": "receive", "reuse_history": false})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "api_key_missing" {
		t.Fatalf("未开启复用应 400 api_key_missing, got %d: %s", code, raw)
	}
}

// TestAddWrongWord POST /api/wrong-words：201 回填 id/created_at，空字段恒存在。
func TestAddWrongWord(t *testing.T) {
	ts, _ := newTestHandler(t)

	code, raw := call(t, ts, http.MethodPost, "/api/wrong-words", map[string]any{
		"word": "have went", "error_type": "主谓一致",
		"original_sentence": "she have went", "corrected_sentence": "she went",
	})
	if code != http.StatusCreated {
		t.Fatalf("新增错词应 201, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	if id, ok := m["id"].(float64); !ok || id <= 0 {
		t.Fatalf("应回填正数 id: %v", m)
	}
	if m["created_at"] == "" {
		t.Fatalf("应回填 created_at: %v", m)
	}

	// analysis_json 未传 → 字段仍存在且为 ""（omitempty 已移除）
	code, raw = call(t, ts, http.MethodPost, "/api/wrong-words", map[string]any{"word": "childrens"})
	if code != http.StatusCreated {
		t.Fatalf("仅 word 新增应 201, got %d: %s", code, raw)
	}
	m = mustJSON(t, raw)
	if v, exists := m["analysis_json"]; !exists || v != "" {
		t.Fatalf("空 analysis_json 应序列化为字段且为 \"\": %v", m)
	}

	// word 为空 → 400 bad_request
	code, raw = call(t, ts, http.MethodPost, "/api/wrong-words", map[string]any{"word": "  "})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("空 word 应 400 bad_request, got %d: %s", code, raw)
	}

	// 列表可查到
	code, raw = call(t, ts, http.MethodGet, "/api/wrong-words?q=went", nil)
	if code != http.StatusOK {
		t.Fatalf("列表应 200, got %d: %s", code, raw)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("搜索应命中 1 条: %s err=%v", raw, err)
	}
}

// TestAddSentence POST /api/sentences：source 归一与校验、analysis_json 透传。
func TestAddSentence(t *testing.T) {
	ts, _ := newTestHandler(t)

	// 检查页收藏：source=check + analysis_json → 201 且回显
	code, raw := call(t, ts, http.MethodPost, "/api/sentences", map[string]any{
		"english": "I have an apple.", "chinese": "我有一个苹果。",
		"source": "check", "analysis_json": `{"pattern":"主谓宾"}`,
	})
	if code != http.StatusCreated {
		t.Fatalf("收藏例句应 201, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	if m["source"] != "check" || m["analysis_json"] != `{"pattern":"主谓宾"}` {
		t.Fatalf("应回显 source=check 与 analysis_json: %v", m)
	}
	if id, ok := m["id"].(float64); !ok || id <= 0 {
		t.Fatalf("应回填正数 id: %v", m)
	}

	// 不传 source → 记为 manual
	code, raw = call(t, ts, http.MethodPost, "/api/sentences", map[string]any{"english": "Knowledge is power."})
	if code != http.StatusCreated || mustJSON(t, raw)["source"] != "manual" {
		t.Fatalf("缺省 source 应记 manual, got %d: %s", code, raw)
	}

	// 非法 source → 400 bad_request
	code, raw = call(t, ts, http.MethodPost, "/api/sentences", map[string]any{"english": "x", "source": "other"})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("非法 source 应 400 bad_request, got %d: %s", code, raw)
	}
}

// TestSettingsUnknownKeyBadRequest 白名单外的键 → 400 bad_request（码表归一）。
func TestSettingsUnknownKeyBadRequest(t *testing.T) {
	ts, _ := newTestHandler(t)

	code, raw := call(t, ts, http.MethodPut, "/api/settings", map[string]any{"no_such": "x"})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("未知键应 400 bad_request, got %d: %s", code, raw)
	}

	// 值非法同样归一为 bad_request
	code, raw = call(t, ts, http.MethodPut, "/api/settings",
		map[string]any{"reuse_discrimination_history": "2"})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("非法值应 400 bad_request, got %d: %s", code, raw)
	}
}

// TestCheckTextLengthLimit text ≤500 字符校验。
func TestCheckTextLengthLimit(t *testing.T) {
	ts, _ := newTestHandler(t)

	// 501 字符 → 400 bad_request
	long := strings.Repeat("a", 501)
	code, raw := call(t, ts, http.MethodPost, "/api/check", map[string]any{"text": long})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("超长 text 应 400 bad_request, got %d: %s", code, raw)
	}

	// 500 字符（边界）通过长度校验 → 走占位（无 KEY → 400 api_key_missing）
	code, raw = call(t, ts, http.MethodPost, "/api/check",
		map[string]any{"text": strings.Repeat("a", 500)})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "api_key_missing" {
		t.Fatalf("边界长度应通过校验进入占位流程, got %d: %s", code, raw)
	}

	// 中文按字符计数：500 个汉字应通过，501 个应拒绝
	code, _ = call(t, ts, http.MethodPost, "/api/check", map[string]any{"text": strings.Repeat("语", 500)})
	if code != http.StatusBadRequest {
		t.Fatalf("500 个汉字应通过长度校验, got %d", code)
	}
	code, raw = call(t, ts, http.MethodPost, "/api/check", map[string]any{"text": strings.Repeat("语", 501)})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("501 个汉字应 400 bad_request, got %d: %s", code, raw)
	}
}

// TestDiscriminationSearchSynonyms 辨析 q 同搜近义词（§2.6：搜索单词 / 近义词）。
func TestDiscriminationSearchSynonyms(t *testing.T) {
	ts, s := newTestHandler(t)

	if err := s.InsertDiscrimination(&store.Discrimination{
		Word:         "receive",
		SynonymsJSON: `["accept","get"]`,
		ResultJSON:   `{"sub":"receive vs accept"}`,
	}); err != nil {
		t.Fatalf("预置辨析记录失败: %v", err)
	}

	// 按近义词 accept 搜索应命中
	code, raw := call(t, ts, http.MethodGet, "/api/discriminations?q=accept", nil)
	if code != http.StatusOK {
		t.Fatalf("列表应 200, got %d: %s", code, raw)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("按近义词搜索应命中 1 条: %s err=%v", raw, err)
	}

	// 无关词不命中
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations?q=zzz", nil)
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 0 {
		t.Fatalf("无关词应 0 条: %s err=%v", raw, err)
	}
}

// TestCompInfoEndpoint GET /api/comp-info：seed 查询 200、缺参 400、未收录 404。
func TestCompInfoEndpoint(t *testing.T) {
	ts, _ := newTestHandler(t)

	// 预置角色（主语 → %E4%B8%BB%E8%AF%AD）：200 且四段字段齐全
	code, raw := call(t, ts, http.MethodGet, "/api/comp-info?role=%E4%B8%BB%E8%AF%AD", nil)
	if code != http.StatusOK {
		t.Fatalf("预置角色应 200, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	if m["role"] != "主语" || m["what"] == "" || m["why"] == "" || m["how"] == "" || m["demo"] == "" {
		t.Fatalf("响应应含 role/what/why/how/demo: %v", m)
	}

	// 缺参 → 400 bad_request
	code, raw = call(t, ts, http.MethodGet, "/api/comp-info", nil)
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("缺 role 应 400 bad_request, got %d: %s", code, raw)
	}

	// 未收录角色 → 404 not_found（前端回退「默认」说明）
	code, raw = call(t, ts, http.MethodGet, "/api/comp-info?role=zzz", nil)
	if code != http.StatusNotFound || mustJSON(t, raw)["code"] != "not_found" {
		t.Fatalf("未收录角色应 404 not_found, got %d: %s", code, raw)
	}
}

// TestFavoriteEndpoints 收藏接口：204 / 幂等 / 不存在 404 / favorite 过滤 / 非法参数 400。
func TestFavoriteEndpoints(t *testing.T) {
	ts, s := newTestHandler(t)

	if err := s.InsertDiscrimination(&store.Discrimination{
		Word:       "receive",
		ResultJSON: `{"word":"receive"}`,
	}); err != nil {
		t.Fatalf("预置辨析记录失败: %v", err)
	}
	items, err := s.ListDiscriminations("", "", "", nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("预置记录读取失败: %v err=%v", items, err)
	}
	path := fmt.Sprintf("/api/discriminations/%d/favorite", items[0].ID)

	// 收藏 → 204；重复收藏幂等 → 204
	for i := 0; i < 2; i++ {
		if code, raw := call(t, ts, http.MethodPost, path, nil); code != http.StatusNoContent {
			t.Fatalf("收藏应 204, got %d: %s", code, raw)
		}
	}
	// favorite=1 过滤命中且 favorite 字段为 true
	code, raw := call(t, ts, http.MethodGet, "/api/discriminations?favorite=1", nil)
	if code != http.StatusOK {
		t.Fatalf("列表应 200, got %d: %s", code, raw)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 || list[0]["favorite"] != true {
		t.Fatalf("favorite=1 过滤应命中且字段为 true: %s err=%v", raw, err)
	}
	// favorite=0 过滤为空
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations?favorite=0", nil)
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 0 {
		t.Fatalf("favorite=0 过滤应为空: %s err=%v", raw, err)
	}
	// 取消收藏 → 204，再查 favorite=1 为空
	if code, raw := call(t, ts, http.MethodDelete, path, nil); code != http.StatusNoContent {
		t.Fatalf("取消收藏应 204, got %d: %s", code, raw)
	}
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations?favorite=1", nil)
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 0 {
		t.Fatalf("取消后 favorite=1 过滤应为空: %s err=%v", raw, err)
	}

	// 不存在的记录 → 404 not_found
	code, raw = call(t, ts, http.MethodPost, "/api/discriminations/999/favorite", nil)
	if code != http.StatusNotFound || mustJSON(t, raw)["code"] != "not_found" {
		t.Fatalf("不存在的 id 应 404 not_found, got %d: %s", code, raw)
	}

	// 非法 favorite 参数 → 400 bad_request
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations?favorite=abc", nil)
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("非法 favorite 应 400 bad_request, got %d: %s", code, raw)
	}

	// 表达收藏路由存在性：不存在的 id → 404（而非其他状态）
	code, raw = call(t, ts, http.MethodPost, "/api/expression-history/1/favorite", nil)
	if code != http.StatusNotFound || mustJSON(t, raw)["code"] != "not_found" {
		t.Fatalf("表达收藏接口应可达（404 not_found）, got %d: %s", code, raw)
	}
}

// TestDeleteDiscrimination DELETE /api/discriminations/{id}：删除 → 204 且列表消失；
// 重复删除同一 id → 404 not_found；非法 id → 400 bad_request。
func TestDeleteDiscrimination(t *testing.T) {
	ts, s := newTestHandler(t)

	if err := s.InsertDiscrimination(&store.Discrimination{
		Word:       "receive",
		ResultJSON: `{"word":"receive"}`,
	}); err != nil {
		t.Fatalf("预置辨析记录失败: %v", err)
	}
	items, err := s.ListDiscriminations("", "", "", nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("预置记录读取失败: %v err=%v", items, err)
	}

	// 删除成功 → 204，列表为空
	code, raw := call(t, ts, http.MethodDelete, fmt.Sprintf("/api/discriminations/%d", items[0].ID), nil)
	if code != http.StatusNoContent {
		t.Fatalf("删除应 204, got %d: %s", code, raw)
	}
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations", nil)
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 0 {
		t.Fatalf("删除后列表应为空: %s err=%v", raw, err)
	}

	// 重复删除 → 404 not_found
	code, raw = call(t, ts, http.MethodDelete, fmt.Sprintf("/api/discriminations/%d", items[0].ID), nil)
	if code != http.StatusNotFound || mustJSON(t, raw)["code"] != "not_found" {
		t.Fatalf("重复删除应 404 not_found, got %d: %s", code, raw)
	}

	// 非法 id → 400 bad_request
	code, raw = call(t, ts, http.MethodDelete, "/api/discriminations/abc", nil)
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("非法 id 应 400 bad_request, got %d: %s", code, raw)
	}
}

// TestUsageExampleLLM POST /api/llm/usage-example：空 sentence 400 → 无 KEY 400 →
// 有 KEY 走 LLM（fake）200 返回 example。
func TestUsageExampleLLM(t *testing.T) {
	ts, s := newTestHandlerWithLLM(t, `{"example":"The eager students finished their homework."}`)

	// sentence 为空 → 400 bad_request
	code, raw := call(t, ts, http.MethodPost, "/api/llm/usage-example",
		map[string]any{"sentence": " ", "role": "主语"})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "bad_request" {
		t.Fatalf("空 sentence 应 400 bad_request, got %d: %s", code, raw)
	}

	// 无 KEY → 400 api_key_missing
	code, raw = call(t, ts, http.MethodPost, "/api/llm/usage-example",
		map[string]any{"sentence": "She writes letters.", "role": "主语"})
	if code != http.StatusBadRequest || mustJSON(t, raw)["code"] != "api_key_missing" {
		t.Fatalf("无 KEY 应 400 api_key_missing, got %d: %s", code, raw)
	}

	// 有 KEY → LLM 真实链路（fake）→ 200
	if err := s.SetSetting("api_key", "sk-test"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}
	code, raw = call(t, ts, http.MethodPost, "/api/llm/usage-example",
		map[string]any{"sentence": "She writes letters.", "role": "主语"})
	if code != http.StatusOK {
		t.Fatalf("有 KEY 应 200, got %d: %s", code, raw)
	}
	if m := mustJSON(t, raw); m["example"] == "" {
		t.Fatalf("应返回 example 字段: %v", m)
	}
}

// TestCheckLLMFlow POST /api/check 全链路：LLM 结果透出（errTotal 与 errors 对齐）+ 写入检查历史。
func TestCheckLLMFlow(t *testing.T) {
	reply := `{"errTotal":9,"translation":"fake 翻译","errors":[{"type":"主谓一致","fix":"have went → went","desc":"fake"}],"example":{"en":"She went.","zh":"fake"},"idiomatic":{"chip":"更地道","tip":"t","en":"e","desc":"d"},"struct":{"comps":[],"pattern":"p","clause":null,"tense":{"chip":"一般过去时","desc":"d"}}}`
	ts, s := newTestHandlerWithLLM(t, reply)
	if err := s.SetSetting("api_key", "sk-test"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}

	code, raw := call(t, ts, http.MethodPost, "/api/check",
		map[string]any{"text": "she have went."})
	if code != http.StatusOK {
		t.Fatalf("check 应 200, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	// errTotal 与 errors 长度对齐（LLM 给 9 也应被兜底为 1）
	if m["errTotal"] != float64(1) {
		t.Fatalf("errTotal 应兜底为 errors 长度 1, got %v", m["errTotal"])
	}
	if m["translation"] != "fake 翻译" {
		t.Fatalf("应透出 LLM 结果: %v", m)
	}

	// 已入库：检查历史可查且 error_count=1
	code, raw = call(t, ts, http.MethodGet, "/api/history/check?limit=5", nil)
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("检查历史应 1 条: %s err=%v", raw, err)
	}
	if items[0]["error_count"] != float64(1) || items[0]["sentence"] != "she have went." {
		t.Fatalf("历史记录字段不符: %v", items[0])
	}
}

// TestDiscriminateLLMFlow 复用未命中 → LLM → 200 附 id → 入库（synonyms 取 words 中非主词 chip）→ 收藏可用。
func TestDiscriminateLLMFlow(t *testing.T) {
	reply := `{"sub":"receive vs accept","core":"fake","words":[{"chip":"receive"},{"chip":"accept"}]}`
	ts, s := newTestHandlerWithLLM(t, reply)
	if err := s.SetSetting("api_key", "sk-test"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}

	code, raw := call(t, ts, http.MethodPost, "/api/discriminate",
		map[string]any{"word": "receive", "reuse_history": true})
	if code != http.StatusOK {
		t.Fatalf("discriminate 应 200, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	id, ok := m["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("响应应附正数 id（供收藏使用）: %v", m)
	}
	if _, exists := m["reused"]; exists {
		t.Fatalf("LLM 新结果不应带 reused 标记: %v", m)
	}

	// 入库：列表可查，synonyms_json 为非主词 chip
	code, raw = call(t, ts, http.MethodGet, "/api/discriminations?q=receive", nil)
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("辨析列表应 1 条: %s err=%v", raw, err)
	}
	var syns []string
	if err := json.Unmarshal([]byte(items[0]["synonyms_json"].(string)), &syns); err != nil ||
		len(syns) != 1 || syns[0] != "accept" {
		t.Fatalf("synonyms_json 应为 [accept]: %v", items[0]["synonyms_json"])
	}

	// 收藏接口可用（id 回填闭环）
	code, raw = call(t, ts, http.MethodPost,
		fmt.Sprintf("/api/discriminations/%d/favorite", int64(id)), nil)
	if code != http.StatusNoContent {
		t.Fatalf("对新记录收藏应 204, got %d: %s", code, raw)
	}
}

// TestExpressLLMFlow POST /api/express 全链路：200 附 id → 入库 variants_json 为存储结构。
func TestExpressLLMFlow(t *testing.T) {
	reply := `{"core":"I received your letter yesterday.","words":[{"chip":"口语","solid":true,"pos":"日常对话","scene":"朋友交流","en":"I got your letter yesterday.","zh":"我昨天收到了你的信。"}]}`
	ts, s := newTestHandlerWithLLM(t, reply)
	if err := s.SetSetting("api_key", "sk-test"); err != nil {
		t.Fatalf("写入 api_key 失败: %v", err)
	}

	code, raw := call(t, ts, http.MethodPost, "/api/express",
		map[string]any{"text": "我昨天收到了你的信。"})
	if code != http.StatusOK {
		t.Fatalf("express 应 200, got %d: %s", code, raw)
	}
	m := mustJSON(t, raw)
	id, ok := m["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("响应应附正数 id: %v", m)
	}
	if m["core"] != "I received your letter yesterday." {
		t.Fatalf("应透出 LLM 结果: %v", m)
	}

	// 入库：variants_json 转换为 style/scene/sentence/translation 存储结构
	code, raw = call(t, ts, http.MethodGet, "/api/expression-history?limit=5", nil)
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
		t.Fatalf("表达历史应 1 条: %s err=%v", raw, err)
	}
	if items[0]["recommended"] != "I received your letter yesterday." {
		t.Fatalf("recommended 应取自 core: %v", items[0])
	}
	var variants []map[string]any
	if err := json.Unmarshal([]byte(items[0]["variants_json"].(string)), &variants); err != nil ||
		len(variants) != 1 || variants[0]["style"] != "口语" ||
		variants[0]["sentence"] != "I got your letter yesterday." {
		t.Fatalf("variants_json 应为存储结构: %v", items[0]["variants_json"])
	}
}

// TestTrends GET /api/trends 响应必须是可解析的单一 JSON 对象（points + summary）。
// 回归背景：曾因 writeList + writeJSON 双重写出，响应体变成「数组][对象」两段拼接
// 的非法 JSON，前端 json() 解析失败拿到 null，打开应用即报
// "Cannot read properties of null (reading 'points')"。
func TestTrends(t *testing.T) {
	ts, repo := newTestHandler(t)

	/* 空库：points 必须序列化为 []（而非 null），summary 字段齐备 */
	status, body := call(t, ts, http.MethodGet, "/api/trends?range=14", nil)
	if status != http.StatusOK {
		t.Fatalf("空库 trends 状态码 = %d, 期望 200", status)
	}
	var resp struct {
		Points  []store.TrendPoint `json:"points"`
		Summary map[string]any     `json:"summary"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("响应体不是合法单一 JSON（双重写出回归？）: %v; body=%s", err, body)
	}
	if resp.Points == nil {
		t.Fatalf("空库 points 应为 []，实际 null; body=%s", body)
	}

	/* 有数据：今日产生一个点，summary.today 非 null */
	if err := repo.InsertCheckHistory(&store.CheckHistory{
		Sentence: "I have went to school.", ErrorCount: 1, ResultJSON: "",
	}); err != nil {
		t.Fatalf("插入检查历史失败: %v", err)
	}
	status, body = call(t, ts, http.MethodGet, "/api/trends", nil)
	if status != http.StatusOK {
		t.Fatalf("有数据 trends 状态码 = %d, 期望 200", status)
	}
	var resp2 struct {
		Points  []store.TrendPoint `json:"points"`
		Summary map[string]any     `json:"summary"`
	}
	if err := json.Unmarshal(body, &resp2); err != nil {
		t.Fatalf("响应体不是合法单一 JSON: %v; body=%s", err, body)
	}
	if len(resp2.Points) != 1 {
		t.Fatalf("points 长度 = %d, 期望 1; body=%s", len(resp2.Points), body)
	}
	if resp2.Summary["today"] == nil {
		t.Fatalf("今日有记录时 summary.today 不应为 null; body=%s", body)
	}
}
