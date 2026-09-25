package api

// 历史 / 统计类接口：辨析列表与清空、表达历史、检查历史、趋势统计。

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"grammarchecker/server/store"
)

// ListDiscriminations GET /api/discriminations?q=&from=&to=&favorite=
// 辨析收藏/历史列表（文档 6.4；q 搜索收藏的解析 + 日期筛选 + 收藏状态过滤）。
func (h *Handler) ListDiscriminations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fav, ok := parseFavorite(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListDiscriminations(q.Get("q"), q.Get("from"), q.Get("to"), fav)
	if mapStoreErr(w, err) {
		return
	}
	writeList(w, items)
}

// DeleteDiscrimination DELETE /api/discriminations/{id}
// 删除单条辨析记录（仓库·辨析选项卡编辑模式，api/readme.md §2.7）。
func (h *Handler) DeleteDiscrimination(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if mapStoreErr(w, h.store.DeleteDiscrimination(id)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ClearDiscriminationHistory DELETE /api/discrimination-history —— 清空辨析历史。
func (h *Handler) ClearDiscriminationHistory(w http.ResponseWriter, _ *http.Request) {
	if _, err := h.store.DeleteAllDiscriminations(); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListExpressionHistory GET /api/expression-history?limit=&favorite= —— 表达历史列表。
func (h *Handler) ListExpressionHistory(w http.ResponseWriter, r *http.Request) {
	fav, ok := parseFavorite(w, r)
	if !ok {
		return
	}
	items, err := h.store.ListExpressions(parseLimit(r, 20, 200), fav)
	if mapStoreErr(w, err) {
		return
	}
	writeList(w, items)
}

// ClearExpressionHistory DELETE /api/expression-history —— 清空表达历史。
func (h *Handler) ClearExpressionHistory(w http.ResponseWriter, _ *http.Request) {
	if _, err := h.store.DeleteAllExpressions(); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCheckHistory GET /api/history/check?limit= —— 最近的检查历史。
// 历史记录卡展示 5 条（文档 6.1），前端按需传 limit。
func (h *Handler) ListCheckHistory(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListCheckHistory(parseLimit(r, 20, 200))
	if mapStoreErr(w, err) {
		return
	}
	writeList(w, items)
}

// ClearCheckHistory DELETE /api/history/check —— 清空检查历史。
func (h *Handler) ClearCheckHistory(w http.ResponseWriter, _ *http.Request) {
	if _, err := h.store.DeleteAllCheckHistory(); err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// trendsSummary 趋势统计摘要（文档 6.5：今日 / 平均 / 提升）。
// today 无数据时为 null（JSON null）。
type trendsSummary struct {
	Today *float64 `json:"today"`
	Avg   float64  `json:"avg"`
	Delta float64  `json:"delta"`
}

// trendsResponse GET /api/trends 响应体。
type trendsResponse struct {
	Points  []store.TrendPoint `json:"points"`
	Summary trendsSummary      `json:"summary"`
}

// Trends GET /api/trends?range=&type=
// range 为天数（默认 14，1-365）；统计源为 check_history。
// 口径说明（初始化版）：
//   - type 为空：accuracy = 当日 0 错误句子数 / 当日检查总数 × 100；
//   - type 非空：accuracy = 当日不含该错误类型的句子数 / 当日检查总数 × 100
//     （错误类型取自检查结果 errors[].type，与检查页返回的错误类型短语精确匹配）；
//   - delta = 今日正确率 − 区间平均。
func (h *Handler) Trends(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v := r.URL.Query().Get("range"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			fail(w, http.StatusBadRequest, "bad_request", "range 须为 1-365 的天数")
			return
		}
		days = n
	}
	from := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	points, err := h.store.ListTrendPoints(from, r.URL.Query().Get("type"))
	if mapStoreErr(w, err) {
		return
	}
	/* trendsResponse 的唯一出口是下方 writeJSON（含 points+summary 完整结构）。
	   此处绝不能再 writeList(w, points)——那会先写一段数组再写对象，
	   响应体变成两段 JSON 拼接（非法 JSON），前端 json() 解析失败拿到 null。 */
	if points == nil {
		points = []store.TrendPoint{}
	}

	var (
		today    *float64
		sum      float64
		todayStr = time.Now().Format("2006-01-02")
	)
	for i := range points {
		sum += points[i].Accuracy
		if points[i].Date == todayStr {
			v := points[i].Accuracy
			today = &v
		}
	}
	avg := 0.0
	if len(points) > 0 {
		avg = round1(sum / float64(len(points)))
	}
	delta := 0.0
	if today != nil {
		delta = round1(*today - avg)
	}
	writeJSON(w, http.StatusOK, trendsResponse{
		Points:  points,
		Summary: trendsSummary{Today: today, Avg: avg, Delta: delta},
	})
}

// round1 四舍五入保留 1 位小数。
func round1(v float64) float64 { return math.Round(v*10) / 10 }
