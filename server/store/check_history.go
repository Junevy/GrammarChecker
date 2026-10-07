package store

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// CheckHistory 检查历史（check_history 表；趋势页统计源，文档 6.5）。
type CheckHistory struct {
	ID         int64  `json:"id"`
	Sentence   string `json:"sentence"`
	ErrorCount int    `json:"error_count"`
	ResultJSON string `json:"result_json"` // 完整检查结果（空值序列化为 ""，字段恒存在）
	CreatedAt  string `json:"created_at"`
}

// ListCheckHistory 最近的检查历史，时间倒序；limit <= 0 时默认 20。
func (s *Store) ListCheckHistory(limit int) ([]CheckHistory, error) {
	return s.listCheckHistory(limit, uidAll)
}

// listCheckHistory 内部实现：uid = uidAll 时不按用户过滤，否则只返回该用户的记录。
func (s *Store) listCheckHistory(limit int, uid int64) ([]CheckHistory, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT id, sentence, error_count, result_json, created_at FROM check_history`
	args := []any{}
	if uid != uidAll {
		query += " WHERE user_id = ?"
		args = append(args, uid)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询检查历史失败: %w", err)
	}
	defer rows.Close()

	items := []CheckHistory{}
	for rows.Next() {
		var x CheckHistory
		if err := rows.Scan(&x.ID, &x.Sentence, &x.ErrorCount, &x.ResultJSON, &x.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取检查历史失败: %w", err)
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

// InsertCheckHistory 写入检查历史（/api/check 完成后自动调用，文档 8.1 步骤 3）。
func (s *Store) InsertCheckHistory(x *CheckHistory) error {
	return s.insertCheckHistory(x, uidAll)
}

// insertCheckHistory 内部实现：uid = uidAll 时不写归属（保持 0），否则写入该用户。
func (s *Store) insertCheckHistory(x *CheckHistory, uid int64) error {
	if uid == uidAll {
		uid = 0
	}
	if x.CreatedAt == "" {
		x.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO check_history (user_id, sentence, error_count, result_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		uid, x.Sentence, x.ErrorCount, x.ResultJSON, x.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入检查历史失败: %w", err)
	}
	if x.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新检查历史 ID 失败: %w", err)
	}
	return nil
}

// DeleteAllCheckHistory 清空检查历史，返回删除条数。
func (s *Store) DeleteAllCheckHistory() (int64, error) {
	return s.deleteAll("check_history")
}

// TrendPoint 趋势单点：某天的语法正确率。
type TrendPoint struct {
	Date     string  `json:"date"`     // yyyy-mm-dd
	Accuracy float64 `json:"accuracy"` // 该日正确率（%）
}

// ListTrendPoints 汇总 from（含）以来每天的正确率。
// 口径（初始化版）：
//   - errType 为空：accuracy = 当日 0 错误句子数 / 当日检查总数 × 100；
//   - errType 非空：accuracy = 当日不含该错误类型的句子数 / 当日检查总数 × 100
//     （错误类型取自 result_json 的 errors[].type，精确匹配；解析失败视为不含）。
//
// 无检查记录的日期不产生数据点，由前端按需补齐。
// 个人工具数据量小，全量拉取后在 Go 层过滤，避免依赖 SQLite JSON 扩展。
func (s *Store) ListTrendPoints(from, errType string) ([]TrendPoint, error) {
	return s.listTrendPoints(from, errType, uidAll)
}

// listTrendPoints 内部实现：uid = uidAll 时不按用户过滤，否则只统计该用户的记录。
func (s *Store) listTrendPoints(from, errType string, uid int64) ([]TrendPoint, error) {
	query := `SELECT substr(created_at, 1, 10) AS day, error_count, result_json
		FROM check_history WHERE substr(created_at, 1, 10) >= ?`
	args := []any{from}
	if uid != uidAll {
		query += " AND user_id = ?"
		args = append(args, uid)
	}
	query += " ORDER BY created_at"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询趋势数据失败: %w", err)
	}
	defer rows.Close()

	// 按日聚合：total=当日检查总数；ok=计入"正确"的句子数
	type dayStat struct{ total, ok int }
	stats := map[string]*dayStat{}
	for rows.Next() {
		var day string
		var errCount int
		var resultJSON string
		if err := rows.Scan(&day, &errCount, &resultJSON); err != nil {
			return nil, fmt.Errorf("读取趋势数据失败: %w", err)
		}
		st := stats[day]
		if st == nil {
			st = &dayStat{}
			stats[day] = st
		}
		st.total++
		if errType == "" {
			if errCount == 0 {
				st.ok++
			}
		} else if !containsErrorType(resultJSON, errType) {
			st.ok++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历趋势数据失败: %w", err)
	}

	// map 无序，按日期升序产出
	days := make([]string, 0, len(stats))
	for d := range stats {
		days = append(days, d)
	}
	sort.Strings(days)

	points := []TrendPoint{}
	for _, d := range days {
		st := stats[d]
		points = append(points, TrendPoint{
			Date: d,
			// 保留 1 位小数，避免浮点尾差（如 66.66666… → 66.7）
			Accuracy: math.Round(float64(st.ok)/float64(st.total)*1000) / 10,
		})
	}
	return points, nil
}

// containsErrorType 判断检查结果 JSON 是否包含指定错误类型
// （errors[].type 精确匹配）；result_json 为空或解析失败时返回 false。
func containsErrorType(resultJSON, errType string) bool {
	var parsed struct {
		Errors []struct {
			Type string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &parsed); err != nil {
		return false
	}
	for _, e := range parsed.Errors {
		if e.Type == errType {
			return true
		}
	}
	return false
}
