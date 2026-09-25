package store

import "fmt"

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
	sqlStr := `SELECT id, english, chinese, source, analysis_json, created_at FROM sentences`
	args := []any{}
	if q != "" {
		sqlStr += " WHERE english LIKE ?"
		args = append(args, "%"+q+"%")
	}
	sqlStr += " ORDER BY created_at DESC, id DESC"

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
	if x.Source == "" {
		x.Source = "manual"
	}
	if x.CreatedAt == "" {
		x.CreatedAt = nowISO()
	}
	res, err := s.db.Exec(
		`INSERT INTO sentences (english, chinese, source, analysis_json, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		x.English, x.Chinese, x.Source, x.AnalysisJSON, x.CreatedAt)
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
