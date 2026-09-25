package api

// 收藏持久化接口（2026-09-25 人员确认方案）：
//
//   - POST   /api/discriminations/{id}/favorite   → 收藏（favorite = 1）
//   - DELETE /api/discriminations/{id}/favorite   → 取消收藏（favorite = 0，记录保留）
//   - POST   /api/expression-history/{id}/favorite
//   - DELETE /api/expression-history/{id}/favorite
//
// 语义：收藏为记录上的标记位而非独立记录——写入幂等、取消不删数据，
// 列表可用 ?favorite=1/0 过滤（GET /api/discriminations、/api/expression-history）。
// 前置条件：记录已存在（辨析/表达结果入库后返回 id），不存在 → 404 not_found。

import "net/http"

// FavoriteDiscrimination POST /api/discriminations/{id}/favorite
func (h *Handler) FavoriteDiscrimination(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.SetDiscriminationFavorite(id, true)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnfavoriteDiscrimination DELETE /api/discriminations/{id}/favorite
func (h *Handler) UnfavoriteDiscrimination(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.SetDiscriminationFavorite(id, false)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// FavoriteExpression POST /api/expression-history/{id}/favorite
func (h *Handler) FavoriteExpression(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.SetExpressionFavorite(id, true)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnfavoriteExpression DELETE /api/expression-history/{id}/favorite
func (h *Handler) UnfavoriteExpression(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.SetExpressionFavorite(id, false)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
