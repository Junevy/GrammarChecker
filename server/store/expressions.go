package store

import "fmt"

// Expression 表达记录（expressions 表，对应文档 6.7）。
type Expression struct {
	ID           int64  `json:"id"`
	Chinese      string `json:"chinese"`
	Recommended  string `json:"recommended"`   // 推荐译法
	VariantsJSON string `json:"variants_json"` // 口语/书面/简洁三变体（空值序列化为 ""，字段恒存在）
	Favorite     bool   `json:"favorite"`      // 收藏标记（POST/DELETE favorite 接口维护，取消收藏不清除记录）
	CreatedAt    string `json:"created_at"`
}

// ListExpressions 表达历史列表，按创建时间倒序；limit > 0 时限制条数。
// favorite 非 nil 时按收藏状态过滤（nil = 不过滤）。
func (s *Store) ListExpressions(limit int, favorite *bool) ([]Expression, error) {
	// SQL 子句顺序固定为 WHERE → ORDER BY → LIMIT，故先拼 WHERE/ORDER BY 再按需拼 LIMIT
	sqlStr := `SELECT id, chinese, recommended, variants_json, favorite, created_at FROM expressions`
	args := []any{}
	if favorite != nil {
		if *favorite {
			sqlStr += " WHERE favorite = 1"
		} else {
			sqlStr += " WHERE favorite = 0"
		}
	}
	sqlStr += " ORDER BY created_at DESC, id DESC"
	if limit > 0 {
		sqlStr += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("查询表达历史失败: %w", err)
	}
	defer rows.Close()

	items := []Expression{}
	for rows.Next() {
		var x Expression
		if err := rows.Scan(&x.ID, &x.Chinese, &x.Recommended, &x.VariantsJSON, &x.Favorite, &x.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取表达历史失败: %w", err)
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

// InsertExpression 写入表达记录（/api/express 接通 LLM 后调用）。
func (s *Store) InsertExpression(x *Expression) error {
	if x.CreatedAt == "" {
		x.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO expressions (chinese, recommended, variants_json, created_at)
		 VALUES (?, ?, ?, ?)`,
		x.Chinese, x.Recommended, x.VariantsJSON, x.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入表达记录失败: %w", err)
	}
	if x.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新表达记录 ID 失败: %w", err)
	}
	return nil
}

// SetExpressionFavorite 置位/取消表达收藏（POST/DELETE favorite 接口）。
// 幂等：重复置位结果一致；记录不存在返回 ErrNotFound。
func (s *Store) SetExpressionFavorite(id int64, fav bool) error {
	return s.setFavorite("expressions", id, fav)
}

// DeleteAllExpressions 清空表达历史，返回删除条数。
func (s *Store) DeleteAllExpressions() (int64, error) {
	return s.deleteAll("expressions")
}
