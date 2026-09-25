package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"grammarchecker/server/store"
)

// errBody 统一错误响应结构。
type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON 以 UTF-8 JSON 写响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail 写统一错误响应。
func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Code: code, Message: msg})
}

// decodeJSON 解析 JSON 请求体（上限 1MB），失败时自动写 400 并返回 false。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
		return false
	}
	return true
}

// pathID 解析路径参数 {id}，非法时写 400 并返回 false。
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "bad_request", "路径参数 id 必须为正整数")
		return 0, false
	}
	return id, true
}

// mapStoreErr 将数据层错误映射为响应：ErrNotFound → 404，其余 → 500。
// err 为 nil 时返回 false（未写响应）。
func mapStoreErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "记录不存在")
		return true
	}
	fail(w, http.StatusInternalServerError, "db_error", err.Error())
	return true
}

// writeList 写列表类 200 响应，nil 统一序列化为 []（而非 JSON null），
// 保证前端 items.map() 等操作不因空结果崩溃。
// 全部列表 handler 必须经此出口，禁止各写一份 nil 兜底。
func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, items)
}

// parseFavorite 解析 favorite 查询参数（可选）：
// 未传 → nil（不过滤）；"1"/"true" → true；"0"/"false" → false；
// 非法值写 400 并返回 ok = false。
func parseFavorite(w http.ResponseWriter, r *http.Request) (*bool, bool) {
	v := r.URL.Query().Get("favorite")
	if v == "" {
		return nil, true
	}
	var b bool
	switch v {
	case "1", "true":
		b = true
	case "0", "false":
		b = false
	default:
		fail(w, http.StatusBadRequest, "bad_request", "favorite 仅接受 1/true 或 0/false")
		return nil, false
	}
	return &b, true
}

// parseLimit 解析 limit 查询参数：非法或缺省返回 def，并夹取到 1..max。
func parseLimit(r *http.Request, def, max int) int {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < 1 {
		n = 1
	}
	if n > max {
		n = max
	}
	return n
}
