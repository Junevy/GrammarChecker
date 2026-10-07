package store

import (
	"fmt"
	"strings"
)

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
	return s.listExpressions(limit, favorite, uidAll)
}

// listExpressions 内部实现：uid = uidAll 时不按用户过滤，否则只返回该用户的记录。
func (s *Store) listExpressions(limit int, favorite *bool, uid int64) ([]Expression, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if uid != uidAll {
		where = append(where, "user_id = ?")
		args = append(args, uid)
	}
	if favorite != nil {
		if *favorite {
			where = append(where, "favorite = 1")
		} else {
			where = append(where, "favorite = 0")
		}
	}
	// SQL 子句顺序固定为 WHERE → ORDER BY → LIMIT
	sqlStr := `SELECT id, chinese, recommended, variants_json, favorite, created_at FROM expressions
		WHERE ` + strings.Join(where, " AND ") + " ORDER BY created_at DESC, id DESC"
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
	return s.insertExpression(x, uidAll)
}

// insertExpression 内部实现：uid = uidAll 时不写归属（保持 0），否则写入该用户。
func (s *Store) insertExpression(x *Expression, uid int64) error {
	if uid == uidAll {
		uid = 0
	}
	if x.CreatedAt == "" {
		x.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO expressions (user_id, chinese, recommended, variants_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		uid, x.Chinese, x.Recommended, x.VariantsJSON, x.CreatedAt)
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
