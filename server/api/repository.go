package api

// 仓库页 CRUD 类接口：先前错误（错词本）与例句的列表 / 新增 / 删除。
// 注意：辨析/表达的「收藏标记」接口在 favorite.go，本文件不含收藏逻辑。

import (
	"net/http"
	"strings"

	"grammarchecker/server/store"
)

// ListWrongWords GET /api/wrong-words?q=&type=&from=&to=
// 先前错误列表：搜索 / 类型 / 日期三类条件 AND 组合（文档 6.2）。
func (h *Handler) ListWrongWords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := h.store.ListWrongWords(store.WrongWordFilter{
		Query: q.Get("q"),
		Type:  q.Get("type"),
		From:  q.Get("from"),
		To:    q.Get("to"),
	})
	if mapStoreErr(w, err) {
		return
	}
	writeList(w, items)
}

// DeleteWrongWord DELETE /api/wrong-words/{id}
func (h *Handler) DeleteWrongWord(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.DeleteWrongWord(id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSentences GET /api/sentences?q= —— 例句列表（搜索例句，文档 6.3）。
func (h *Handler) ListSentences(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListSentences(r.URL.Query().Get("q"))
	if mapStoreErr(w, err) {
		return
	}
	writeList(w, items)
}

// AddSentence POST /api/sentences —— 新增例句。
// 手动添加仅传 {english, chinese}（source 记 manual）；
// 检查页「收藏例句」额外传 source=check 与 analysis_json 结构分析缓存。
func (h *Handler) AddSentence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		English      string `json:"english"`
		Chinese      string `json:"chinese"`
		AnalysisJSON string `json:"analysis_json"`
		Source       string `json:"source"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.English) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "english 不能为空")
		return
	}
	src := strings.TrimSpace(body.Source)
	if src == "" {
		src = "manual"
	}
	if src != "manual" && src != "check" {
		fail(w, http.StatusBadRequest, "bad_request", "source 仅支持 manual / check")
		return
	}
	x := store.Sentence{
		English:      strings.TrimSpace(body.English),
		Chinese:      body.Chinese,
		Source:       src,
		AnalysisJSON: body.AnalysisJSON,
	}
	if err := h.store.InsertSentence(&x); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

// DeleteSentence DELETE /api/sentences/{id}
func (h *Handler) DeleteSentence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.DeleteSentence(id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddWrongWord POST /api/wrong-words —— 新增错词（检查页「加入错词本」5s 倒计时结束后调用，
// api/readme.md §1.4）。word 必填；error_type / 句子 / analysis_json 随检查结果携带，允许为空。
func (h *Handler) AddWrongWord(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Word              string `json:"word"`
		ErrorType         string `json:"error_type"`
		OriginalSentence  string `json:"original_sentence"`
		CorrectedSentence string `json:"corrected_sentence"`
		AnalysisJSON      string `json:"analysis_json"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Word) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "word 不能为空")
		return
	}
	x := store.WrongWord{
		Word:              strings.TrimSpace(body.Word),
		ErrorType:         body.ErrorType,
		OriginalSentence:  body.OriginalSentence,
		CorrectedSentence: body.CorrectedSentence,
		AnalysisJSON:      body.AnalysisJSON,
	}
	if err := h.store.InsertWrongWord(&x); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, x)
}
