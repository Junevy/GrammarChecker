package api

// LLM 类接口的处理器（2026-09-25 接通 DeepSeek）。
//
// 供应商：DeepSeek（deepseek-chat，OpenAI 兼容），prompt 初稿见 server/llm/prompts.go。
// 四个接口统一流程：校验入参 → 读 API KEY（未配置 400 api_key_missing）→ 调 LLM
// → 解析 JSON（非法 502 llm_upstream）→ 结果入库 → 返回契约结构。
// 响应契约以前端 mock 为准，字段说明见 api/readme.md §1.1 / §3.1 / §4.1 / §7.2。
//
// 本文件的职责边界：HTTP 编解码 + LLM 结果到存储结构的映射；
// LLM 协议细节（超时/重试/鉴权）在 server/llm，SQL 在 server/store，均不在此处。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"grammarchecker/server/llm"
	"grammarchecker/server/store"
)

// maxCheckText 语法检查入参长度上限（api/readme.md §1.1：text ≤ 500 字符）。
const maxCheckText = 500

// chatMessages 组装 system + user 消息。
func chatMessages(system, user string) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}

// callLLM 统一 LLM 调用链：读 KEY → Chat → 解析 JSON。
// 返回 false 表示已写错误响应，调用方直接 return。
func (h *Handler) callLLM(w http.ResponseWriter, r *http.Request, system, user string) (map[string]any, bool) {
	apiKey, err := h.store.GetSetting("api_key")
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return nil, false
	}
	if apiKey == "" {
		fail(w, http.StatusBadRequest, "api_key_missing", "API KEY 未配置，请先在「配置」页填写")
		return nil, false
	}

	text, err := h.llm.Chat(r.Context(), apiKey, chatMessages(system, user))
	if err != nil {
		if errors.Is(err, llm.ErrNoAPIKey) {
			fail(w, http.StatusBadRequest, "api_key_missing", "API KEY 未配置，请先在「配置」页填写")
			return nil, false
		}
		// 上游网络/鉴权/内容非法统一 502（区别于 500 db_error 与 503 整体超时）
		fail(w, http.StatusBadGateway, "llm_upstream", err.Error())
		return nil, false
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil || out == nil {
		fail(w, http.StatusBadGateway, "llm_upstream",
			fmt.Sprintf("LLM 返回内容不是合法 JSON: %.300s", text))
		return nil, false
	}
	return out, true
}

// jsonStr 提取对象中的字符串字段（缺省为空串）。
func jsonStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// jsonChips 提取 words 数组各条目的 chip 字段（作为近义词列表）。
func jsonChips(m map[string]any) []string {
	raw, ok := m["words"].([]any)
	if !ok {
		return nil
	}
	chips := []string{}
	for _, it := range raw {
		if obj, ok := it.(map[string]any); ok {
			if c := jsonStr(obj, "chip"); c != "" {
				chips = append(chips, c)
			}
		}
	}
	return chips
}

// expressVariants 把 LLM 的 words 条目转换为 expressions.variants_json 的存储结构
// （api/readme.md §4.2：style/scene/sentence/translation）。
func expressVariants(m map[string]any) []map[string]string {
	raw, ok := m["words"].([]any)
	if !ok {
		return []map[string]string{}
	}
	variants := []map[string]string{}
	for _, it := range raw {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		variants = append(variants, map[string]string{
			"style":       jsonStr(obj, "chip"),
			"scene":       jsonStr(obj, "scene"),
			"sentence":    jsonStr(obj, "en"),
			"translation": jsonStr(obj, "zh"),
		})
	}
	return variants
}

// Check POST /api/check —— 语法检查（LLM 已接通）。
// 调用 LLM → 返回 {errTotal, translation, errors, example, idiomatic, struct}
// （契约见 api/readme.md §1.1）并写入 check_history（文档 8.1）。
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "text 不能为空")
		return
	}
	if utf8.RuneCountInString(body.Text) > maxCheckText {
		fail(w, http.StatusBadRequest, "bad_request", "text 长度不能超过 500 字符")
		return
	}

	out, ok := h.callLLM(w, r, llm.PromptCheck, body.Text)
	if !ok {
		return
	}
	// errTotal 以 errors 数组长度兜底，保证与列表一致
	errCount := 0
	if errs, ok := out["errors"].([]any); ok {
		errCount = len(errs)
		out["errTotal"] = errCount
	}

	// 入库：result_json 存 LLM 原始结果，供历史/趋势/错词本复用
	rec := store.CheckHistory{
		Sentence:   body.Text,
		ErrorCount: errCount,
		ResultJSON: mustMarshal(out),
	}
	if err := h.store.InsertCheckHistory(&rec); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Discriminate POST /api/discriminate —— 辨析（LLM 已接通 + 历史复用短路）。
//
// reuse_history=true 时先查本地缓存：命中且缓存非空则直接返回 LLM 结果结构
// （附 reused=true 与原记录 created_at，契约见 api/readme.md §3.1），
// 全程不依赖 API KEY —— 故短路必须先于 callLLM 的 KEY 校验。
// 未命中 / 缓存为空 / 未开启复用时，调 LLM 并入库（响应附 id 供收藏使用）。
func (h *Handler) Discriminate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Word         string `json:"word"`
		ReuseHistory bool   `json:"reuse_history"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	word := strings.TrimSpace(body.Word)
	if word == "" {
		fail(w, http.StatusBadRequest, "bad_request", "word 不能为空")
		return
	}
	if body.ReuseHistory {
		hit, err := h.store.FindDiscriminationByWord(word)
		switch {
		case err == nil && strings.TrimSpace(hit.ResultJSON) != "":
			var result map[string]any
			if err := json.Unmarshal([]byte(hit.ResultJSON), &result); err != nil || result == nil {
				result = map[string]any{}
			}
			result["reused"] = true
			result["created_at"] = hit.CreatedAt
			writeJSON(w, http.StatusOK, result)
			return
		case err == nil:
			// 命中记录但缓存为空 → 视为未命中，继续 LLM 流程
		case errors.Is(err, store.ErrNotFound):
			// 本地未命中 → 继续 LLM 流程
		default:
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
	}

	out, ok := h.callLLM(w, r, llm.PromptDiscriminate, word)
	if !ok {
		return
	}

	// 入库：近义词列表 = words 中除主词外的 chip 集合；result_json 存 LLM 原始结果
	chips := jsonChips(out)
	synonyms := []string{}
	for _, c := range chips {
		if !strings.EqualFold(c, word) {
			synonyms = append(synonyms, c)
		}
	}
	synJSON, _ := json.Marshal(synonyms)
	rec := store.Discrimination{
		Word:         word,
		SynonymsJSON: string(synJSON),
		ResultJSON:   mustMarshal(out),
	}
	if err := h.store.InsertDiscrimination(&rec); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	// 附 id：前端据此调用收藏接口（api/readme.md §3.3）
	out["id"] = rec.ID
	writeJSON(w, http.StatusOK, out)
}

// Express POST /api/express —— 中译英表达（LLM 已接通）。
// 调用 LLM → 返回 {core, words:[...]}（契约见 api/readme.md §4.1）
// 并写入 expressions（文档 6.7，响应附 id 供收藏使用）。
func (h *Handler) Express(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "text 不能为空")
		return
	}

	out, ok := h.callLLM(w, r, llm.PromptExpress, body.Text)
	if !ok {
		return
	}

	// 入库：recommended=core；variants_json 按存储结构（style/scene/sentence/translation）转换
	rec := store.Expression{
		Chinese:      body.Text,
		Recommended:  jsonStr(out, "core"),
		VariantsJSON: mustMarshal(expressVariants(out)),
	}
	if err := h.store.InsertExpression(&rec); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	out["id"] = rec.ID
	writeJSON(w, http.StatusOK, out)
}

// UsageExample POST /api/llm/usage-example —— 成分用法示例（LLM 已接通）。
// 场景：成分说明弹窗的「生成用法示例」按钮——针对 sentence 中的 role 成分，
// 让 LLM 生成贴合语境的示例句。响应 {"example": "…"}（api/readme.md §7.2）。
// 结果为一次性生成，不入库。
func (h *Handler) UsageExample(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sentence string `json:"sentence"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Sentence) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "sentence 不能为空")
		return
	}

	user := "句子：" + body.Sentence
	if role := strings.TrimSpace(body.Role); role != "" {
		user += "\n成分：" + role
	}
	out, ok := h.callLLM(w, r, llm.PromptUsageExample, user)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// mustMarshal 序列化失败时回退空对象（理论不可达：out 来自合法 JSON 解析）。
func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
