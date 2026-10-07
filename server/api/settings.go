package api

// 设置类接口（文档 6.8：辨析复用开关 + API KEY）。
//
// API KEY 有两种托管形态（2026-09-26 云服务器部署支持，取值口径见 apikey.go）：
//   - 环境变量 GC_API_KEY（部署形态）：不回传明文，且拒绝经 API 改写；
//   - 本地 SQLite（桌面单机形态）：沿用原契约明文回传，掩码/显隐由前端负责。

import (
	"net/http"
)

// allowedSettings 允许通过 API 写入的设置键白名单（技术文档第 3/4 节）。
// 注：api_key_source 为只读派生字段，刻意不入白名单，写入会被整体拒绝。
var allowedSettings = map[string]bool{
	"api_key":                      true, // LLM 密钥；环境变量托管时禁止写入
	"reuse_discrimination_history": true, // 辨析复用历史开关，"0"/"1"，默认开
}

// GetSettings GET /api/settings —— 返回全部设置项。
//
// 响应额外携带 api_key_source 标识密钥来源（"env" 环境变量 / "db" 数据库）：
//   - 环境变量托管：api_key 不回传明文（置空串），否则「页面拿到掩码→保存回写」
//     会把真实密钥覆盖成掩码值；
//   - 数据库托管：明文返回给前端（掩码/显隐由前端实现，文档 6.8）——
//     但仅限单用户模式与多用户模式下的管理员；普通用户一律回空串
//     （全局密钥不向非管理员泄露，2026-09-29 安全加固），前端对非管理员
//     隐藏 API KEY 卡片。
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.GetAllSettings()
	if mapStoreErr(w, err) {
		return
	}
	key, source, err := resolveAPIKey(h.store)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	items["api_key_source"] = source
	if source == apiKeySourceEnv || !h.canSeeAPIKey(r) {
		items["api_key"] = ""
	} else {
		items["api_key"] = key
	}
	writeJSON(w, http.StatusOK, items)
}

// canSeeAPIKey 报告当前请求者是否可读全局 API KEY 明文：
// 单用户模式恒可（桌面形态原契约）；多用户模式仅管理员。
func (h *Handler) canSeeAPIKey(r *http.Request) bool {
	if !h.authRequired() {
		return true
	}
	u := currentUserInfo(r)
	return u != nil && u.IsAdmin
}

// PutSettings PUT /api/settings —— 保存设置项。
// 仅接受白名单键；先整体校验再写入，避免出现部分成功。
// 多用户模式下仅管理员可写（全局设置 affects 所有人，2026-09-29 安全加固）；
// 单用户模式保持原有开放行为。
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	if h.authRequired() {
		u := currentUserInfo(r)
		if u == nil {
			writeUnauthorized(w)
			return
		}
		if !u.IsAdmin {
			fail(w, http.StatusForbidden, "forbidden", "全局设置仅管理员可修改")
			return
		}
	}
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
	// 环境变量托管时拒绝改写密钥：否则库值与进程实际生效值不一致，
	// 且重启后页面填写的密钥会被环境变量静默覆盖（难排障的隐性坑）。
	if apiKeyManagedByEnv() {
		if _, ok := body["api_key"]; ok {
			fail(w, http.StatusBadRequest, "bad_request",
				"API KEY 由服务器环境变量 "+EnvAPIKey+" 托管，不可在页面修改")
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
