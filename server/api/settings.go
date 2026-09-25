package api

// 设置类接口（文档 6.8：辨析复用开关 + API KEY）。

import (
	"net/http"
)

// allowedSettings 允许通过 API 写入的设置键白名单（技术文档第 3/4 节）。
var allowedSettings = map[string]bool{
	"api_key":                      true, // LLM 密钥，仅存本地 SQLite
	"reuse_discrimination_history": true, // 辨析复用历史开关，"0"/"1"，默认开
}

// GetSettings GET /api/settings —— 返回全部设置项。
// 说明：api_key 以明文返回给本机前端（掩码/显隐由前端实现，文档 6.8）；
// 服务仅监听 127.0.0.1，不出本机。
func (h *Handler) GetSettings(w http.ResponseWriter, _ *http.Request) {
	items, err := h.store.GetAllSettings()
	if mapStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// PutSettings PUT /api/settings —— 保存设置项。
// 仅接受白名单键；先整体校验再写入，避免出现部分成功。
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !decodeJSON(w, r, &body) {
		return
	}
	for key, value := range body {
		if !allowedSettings[key] {
			// 白名单外的键整体拒绝；错误码统一归为 bad_request（api/readme.md §0 错误表）
			fail(w, http.StatusBadRequest, "bad_request", "不支持的设置键: "+key)
			return
		}
		if key == "reuse_discrimination_history" && value != "0" && value != "1" {
			fail(w, http.StatusBadRequest, "bad_request", `reuse_discrimination_history 仅接受 "0" 或 "1"`)
			return
		}
	}
	for key, value := range body {
		if err := h.store.SetSetting(key, value); err != nil {
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
	}
	h.GetSettings(w, r)
}
