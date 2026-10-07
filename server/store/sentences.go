package store

import (
	"fmt"
	"strings"
)

// Sentence 例句收藏（sentences 表，对应文档 6.3）。
// 命名澄清：「生词本」为早期废弃叫法，正式名称为「例句」。
type Sentence struct {
	ID           int64  `json:"id"`
	English      string `json:"english"`
	Chinese      string `json:"chinese"`
	Source       string `json:"source"` // check：来自检查流程；manual：手动添加
	AnalysisJSON string `json:"analysis_json"` // 空值序列化为 ""，字段恒存在
	CreatedAt    string `json:"created_at"`
}

// ListSentences 查询例句列表；q 按英文句子模糊搜索，按创建时间倒序。
func (s *Store) ListSentences(q string) ([]Sentence, error) {
	return s.listSentences(q, uidAll)
}

// listSentences 内部实现：uid = uidAll 时不按用户过滤，否则只返回该用户的记录。
func (s *Store) listSentences(q string, uid int64) ([]Sentence, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if uid != uidAll {
		where = append(where, "user_id = ?")
		args = append(args, uid)
	}
	if q != "" {
		where = append(where, "english LIKE ?")
		args = append(args, "%"+q+"%")
	}
	sqlStr := `SELECT id, english, chinese, source, analysis_json, created_at FROM sentences
		WHERE ` + strings.Join(where, " AND ") + " ORDER BY created_at DESC, id DESC"

	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("查询例句失败: %w", err)
	}
	defer rows.Close()

	items := []Sentence{}
	for rows.Next() {
		var x Sentence
		if err := rows.Scan(&x.ID, &x.English, &x.Chinese, &x.Source, &x.AnalysisJSON, &x.CreatedAt); err != nil {
			return nil, fmt.Errorf("读取例句失败: %w", err)
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

// InsertSentence 新增例句（手动添加时 source 留空则记为 manual）。
// 写入后回填 x.ID 与 x.CreatedAt。
func (s *Store) InsertSentence(x *Sentence) error {
	return s.insertSentence(x, uidAll)
}

// insertSentence 内部实现：uid = uidAll 时不写归属（保持 0），否则写入该用户。
func (s *Store) insertSentence(x *Sentence, uid int64) error {
	if uid == uidAll {
		uid = 0
	}
	if x.Source == "" {
		x.Source = "manual"
	}
	if x.CreatedAt == "" {
		x.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO sentences (user_id, english, chinese, source, analysis_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uid, x.English, x.Chinese, x.Source, x.AnalysisJSON, x.CreatedAt)
	if err != nil {
		return fmt.Errorf("写入例句失败: %w", err)
	}
	if x.ID, err = res.LastInsertId(); err != nil {
		return fmt.Errorf("获取新例句 ID 失败: %w", err)
	}
	return nil
}

// DeleteSentence 按 ID 删除例句收藏；未命中返回 ErrNotFound。
func (s *Store) DeleteSentence(id int64) error {
	return s.deleteByID("sentences", id)
}
