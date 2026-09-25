package api

// GET /api/comp-info —— 句子成分说明（预置 seed，DB 优先，不调 LLM）。
//
// 数据为 migrate 时预置的固定语法知识（server/store/comp_seed.go），
// 未收录角色返回 404 not_found，由前端回退本地「默认」说明。

import (
	"net/http"
	"strings"
)

// GetCompInfo GET /api/comp-info?role=主语
// 响应：{role, what, why, how, demo}，字段口径与前端 compInfo mock 一致。
func (h *Handler) GetCompInfo(w http.ResponseWriter, r *http.Request) {
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	if role == "" {
		fail(w, http.StatusBadRequest, "bad_request", "role 不能为空")
		return
	}
	info, err := h.store.GetCompInfo(role)
	if mapStoreErr(w, err) {
		// 404 not_found：未收录角色，前端回退通用说明
		return
	}
	writeJSON(w, http.StatusOK, info)
}
